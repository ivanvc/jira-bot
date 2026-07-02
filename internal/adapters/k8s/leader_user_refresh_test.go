package k8s_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/kubernetes/fake"

	k8sadapter "github.com/ivanvc/jira-bot/internal/adapters/k8s"
	"github.com/ivanvc/jira-bot/internal/adapters/jira"
)

// refreshStoreAdapter bridges k8sadapter.K8sUserTokenStore to jira.RefreshTokenStore.
// This mirrors the adapter in cmd/jb/main.go for testing purposes.
type refreshStoreAdapter struct {
	store *k8sadapter.K8sUserTokenStore
}

func (a *refreshStoreAdapter) ReadAll(ctx context.Context) (map[string]jira.RefreshTokenEntry, error) {
	entries, err := a.store.ReadAll(ctx)
	if err != nil {
		return nil, err
	}
	result := make(map[string]jira.RefreshTokenEntry, len(entries))
	for login, e := range entries {
		result[login] = jira.RefreshTokenEntry{
			RefreshToken: e.RefreshToken,
			AccessToken:  e.AccessToken,
			ExpiresAt:    e.ExpiresAt,
			CloudID:      e.CloudID,
			Status:       e.Status,
		}
	}
	return result, nil
}

func (a *refreshStoreAdapter) Write(ctx context.Context, login string, entry jira.RefreshTokenEntry) error {
	return a.store.Write(ctx, login, k8sadapter.UserTokenEntry{
		RefreshToken: entry.RefreshToken,
		AccessToken:  entry.AccessToken,
		ExpiresAt:    entry.ExpiresAt,
		CloudID:      entry.CloudID,
		Status:       entry.Status,
	})
}

// TestLeaderElection_RefreshManagerLifecycle verifies that the MultiUserRefreshManager
// starts when leadership is acquired and stops when leadership is lost.
// Requirements: 5.1, 5.2
func TestLeaderElection_RefreshManagerLifecycle(t *testing.T) {
	fakeClient := fake.NewSimpleClientset()

	// Track start/stop events via channels.
	started := make(chan struct{}, 1)
	stopped := make(chan struct{}, 1)

	// Use a mock token server that responds immediately.
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token":  "new-access",
			"refresh_token": "new-refresh",
			"expires_in":    3600,
		})
	}))
	defer tokenServer.Close()

	store := k8sadapter.NewK8sUserTokenStore(fakeClient, "default", "user-tokens", nil)
	adapter := &refreshStoreAdapter{store: store}

	mgr := jira.NewMultiUserRefreshManager(
		adapter,
		"client-id",
		"client-secret",
		10*time.Second,
		jira.WithMultiRefreshTokenURL(tokenServer.URL),
	)

	callbacks := k8sadapter.LeaderCallbacks{
		OnStartedLeading: func(ctx context.Context) {
			started <- struct{}{}
			mgr.Start(ctx)
		},
		OnStoppedLeading: func() {
			mgr.Stop()
			stopped <- struct{}{}
		},
		OnNewLeader: func(identity string) {},
	}

	leaderCfg := k8sadapter.LeaderElectorConfig{
		LeaseName:      "test-leader-lease",
		LeaseNamespace: "default",
		Identity:       "test-pod-1",
		LeaseDuration:  4 * time.Second,
		RenewDeadline:  3 * time.Second,
		RetryPeriod:    1 * time.Second,
	}

	elector, err := k8sadapter.NewLeaderElector(fakeClient, leaderCfg, callbacks)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Run the elector in a goroutine. With a single node and fake client,
	// leadership should be acquired quickly.
	go elector.Run(ctx)

	// Wait for leadership to be acquired (OnStartedLeading called).
	select {
	case <-started:
		// Leadership acquired, refresh manager started.
	case <-time.After(10 * time.Second):
		t.Fatal("Timed out waiting for leadership to be acquired")
	}

	// Cancel the context to simulate lease loss.
	cancel()

	// Wait for OnStoppedLeading to fire (refresh manager stopped).
	select {
	case <-stopped:
		// Leadership lost, refresh manager stopped.
	case <-time.After(10 * time.Second):
		t.Fatal("Timed out waiting for leadership to be lost")
	}
}

// TestMultiUserRefreshManager_MaxConcurrency verifies that at most 5 concurrent
// refresh requests are in-flight simultaneously.
// Requirements: 4.6
func TestMultiUserRefreshManager_MaxConcurrency(t *testing.T) {
	fakeClient := fake.NewSimpleClientset()

	var (
		currentConcurrent atomic.Int64
		maxConcurrent     atomic.Int64
		requestsStarted   atomic.Int64
		mu                sync.Mutex
		allDone           = make(chan struct{})
	)

	const totalUsers = 15
	const requestDelay = 100 * time.Millisecond

	// Mock token server that introduces a delay and tracks concurrency.
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur := currentConcurrent.Add(1)
		requestsStarted.Add(1)

		// Track max concurrency observed.
		for {
			old := maxConcurrent.Load()
			if cur <= old || maxConcurrent.CompareAndSwap(old, cur) {
				break
			}
		}

		time.Sleep(requestDelay)

		currentConcurrent.Add(-1)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token":  "refreshed-access",
			"refresh_token": "refreshed-refresh",
			"expires_in":    3600,
		})

		// Signal completion when all requests have been processed.
		if requestsStarted.Load() >= totalUsers {
			mu.Lock()
			select {
			case <-allDone:
			default:
				close(allDone)
			}
			mu.Unlock()
		}
	}))
	defer tokenServer.Close()

	// Seed the store with many near-expiry entries.
	store := k8sadapter.NewK8sUserTokenStore(fakeClient, "default", "user-tokens", nil)
	ctx := context.Background()

	for i := 0; i < totalUsers; i++ {
		login := "user-" + string(rune('a'+i))
		if i >= 26 {
			login = "user-" + string(rune('a'+i-26)) + "2"
		}
		entry := k8sadapter.UserTokenEntry{
			RefreshToken: "refresh-" + login,
			AccessToken:  "access-" + login,
			ExpiresAt:    time.Now().Add(1 * time.Minute), // Within the 5-minute buffer
			CloudID:      "cloud-1",
		}
		require.NoError(t, store.Write(ctx, login, entry))
	}

	adapter := &refreshStoreAdapter{store: store}
	mgr := jira.NewMultiUserRefreshManager(
		adapter,
		"client-id",
		"client-secret",
		10*time.Second, // minimum interval
		jira.WithMultiRefreshTokenURL(tokenServer.URL),
		jira.WithMultiRefreshHTTPClient(&http.Client{Timeout: 5 * time.Second}),
	)

	// Start the manager — it will run an immediate refresh cycle.
	mgrCtx, mgrCancel := context.WithCancel(ctx)
	mgr.Start(mgrCtx)

	// Wait for all requests to complete.
	select {
	case <-allDone:
	case <-time.After(30 * time.Second):
		t.Fatal("Timed out waiting for all refresh requests to complete")
	}

	// Stop the manager.
	mgrCancel()
	mgr.Stop()

	// Verify: max concurrency should not exceed 5.
	observed := maxConcurrent.Load()
	assert.LessOrEqual(t, observed, int64(5),
		"Expected max concurrent requests <= 5, got %d", observed)
	assert.Greater(t, observed, int64(0),
		"Expected at least one concurrent request")

	// Verify all users were refreshed.
	assert.GreaterOrEqual(t, requestsStarted.Load(), int64(totalUsers),
		"Expected at least %d refresh requests, got %d", totalUsers, requestsStarted.Load())
}

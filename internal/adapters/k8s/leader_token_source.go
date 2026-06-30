package k8s

import (
	"context"
	"time"

	"github.com/charmbracelet/log"
	"github.com/ivanvc/jira-bot/internal/adapters/jira"
)

// Compile-time check that LeaderTokenSource implements jira.TokenSource.
var _ jira.TokenSource = (*LeaderTokenSource)(nil)

// proactiveRefreshBuffer is how far before expiry the leader will proactively
// refresh the token. This ensures followers always have a valid token in the secret.
const proactiveRefreshBuffer = 5 * time.Minute

// refreshCheckInterval is how often the background loop checks whether a refresh is needed.
const refreshCheckInterval = 30 * time.Second

// LeaderTokenSource wraps the existing TokenManager and persists tokens after each refresh.
// Implements the jira.TokenSource interface.
type LeaderTokenSource struct {
	tokenManager *jira.TokenManager
	adapter      *TokenPersistenceAdapter
	logger       *log.Logger
}

// NewLeaderTokenSource wraps an existing TokenManager with persistence.
// It installs a RefreshCallback on the TokenManager that persists new tokens
// immediately after a successful refresh (persist-before-use), preventing token
// loss on abrupt process termination (e.g., spot instance eviction).
func NewLeaderTokenSource(tm *jira.TokenManager, adapter *TokenPersistenceAdapter, logger *log.Logger) *LeaderTokenSource {
	l := &LeaderTokenSource{
		tokenManager: tm,
		adapter:      adapter,
		logger:       logger,
	}

	// Install the persist-before-use callback. This ensures the new refresh token
	// is written to the Bot_Secret before the access token is returned and used,
	// closing the window where a SIGKILL could lose the rotated refresh token.
	tm.SetRefreshCallback(func(refreshToken, accessToken string, expiresAt time.Time) {
		data := TokenData{
			RefreshToken: refreshToken,
			AccessToken:  accessToken,
			ExpiresAt:    expiresAt,
		}
		if writeErr := adapter.Write(context.Background(), data); writeErr != nil {
			logger.Error("Failed to persist token after refresh (callback)", "error", writeErr)
		}
	})

	return l
}

// Start begins the proactive refresh loop. It checks every refreshCheckInterval whether
// the token is within proactiveRefreshBuffer of expiry, and if so, triggers a refresh
// and persists the new token. This ensures followers always have a valid token available.
// Blocks until ctx is cancelled.
func (l *LeaderTokenSource) Start(ctx context.Context) {
	// Do an initial refresh to ensure the secret has a valid token immediately.
	l.proactiveRefresh()

	ticker := time.NewTicker(refreshCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			l.proactiveRefresh()
		}
	}
}

// proactiveRefresh checks if the token is near expiry and refreshes it if so.
func (l *LeaderTokenSource) proactiveRefresh() {
	_, _, expiresAt := l.tokenManager.TokenState()

	// If the token expires within the buffer window, force a refresh by clearing
	// the cached token (setting expiry to past) and then calling Token().
	// This is necessary because the TokenManager has its own earlyExpiryBuffer (60s)
	// which is shorter than our proactive buffer.
	if time.Until(expiresAt) < proactiveRefreshBuffer {
		l.logger.Info("Proactively refreshing token before expiry", "expires_at", expiresAt)
		l.tokenManager.SetCachedToken("", time.Time{})
		if _, err := l.Token(); err != nil {
			l.logger.Error("Proactive token refresh failed", "error", err)
		}
	}
}

// Token returns a valid access token, refreshing if needed.
// Persistence is handled by the RefreshCallback installed on the TokenManager,
// which writes to the Bot_Secret immediately after each successful refresh.
func (l *LeaderTokenSource) Token() (string, error) {
	token, err := l.tokenManager.Token()
	if err != nil {
		return "", err
	}
	return token, nil
}

package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/charmbracelet/log"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/ivanvc/jira-bot/internal/adapters/github"
	"github.com/ivanvc/jira-bot/internal/adapters/jira"
	k8sadapter "github.com/ivanvc/jira-bot/internal/adapters/k8s"
	"github.com/ivanvc/jira-bot/internal/common"
	"github.com/ivanvc/jira-bot/internal/config"
	"github.com/ivanvc/jira-bot/internal/http"
	"github.com/ivanvc/jira-bot/internal/resolver"
)

// refreshStoreAdapter bridges the common.UserTokenStore (backed by K8s) to the
// jira.RefreshTokenStore interface required by MultiUserRefreshManager.
// The entry types are structurally identical; this adapter performs the
// field-by-field conversion.
type refreshStoreAdapter struct {
	store common.UserTokenStore
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
	return a.store.Write(ctx, login, common.UserTokenEntry{
		RefreshToken: entry.RefreshToken,
		AccessToken:  entry.AccessToken,
		ExpiresAt:    entry.ExpiresAt,
		CloudID:      entry.CloudID,
		Status:       entry.Status,
	})
}

func main() {
	cfg := common.LoadConfig()

	githubClient, err := github.NewClient(cfg.GitHubAppID, cfg.GitHubPrivateKey)
	if err != nil {
		panic(err)
	}

	state := &common.State{
		Config:           cfg,
		GitHubClient:     githubClient,
		RepoConfigLoader: config.NewLoader(githubClient),
	}

	// Handle SIGTERM for graceful shutdown.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		sig := <-sigCh
		log.Info("Received shutdown signal", "signal", sig)
		os.Exit(0)
	}()

	// Wire per-user token infrastructure.
	// Build a K8s client for the UserTokenStore (graceful failure for local dev).
	var k8sClient kubernetes.Interface
	if k8sConfig, k8sErr := rest.InClusterConfig(); k8sErr == nil {
		client, clientErr := kubernetes.NewForConfig(k8sConfig)
		if clientErr != nil {
			log.Warn("Failed to create K8s client for user token store", "error", clientErr)
		} else {
			k8sClient = client
		}
	} else {
		log.Info("Not running in K8s cluster; user token store will not be available", "error", k8sErr)
	}

	if k8sClient != nil && cfg.UserTokenSecretName != "" {
		userTokenStore := k8sadapter.NewK8sUserTokenStore(k8sClient, cfg.PodNamespace, cfg.UserTokenSecretName, log.Default())
		state.UserTokenStore = userTokenStore
	}

	// Create the JiraClientResolver (uses UserTokenStore for per-user token resolution).
	if state.UserTokenStore != nil {
		jiraResolver := resolver.NewDefaultJiraClientResolver(
			state.UserTokenStore,
			cfg.JiraClientID,
			cfg.JiraClientSecret,
			cfg.GlobalCloudID,
			cfg.UserAuthCallbackURL,
			log.Default(),
		)
		state.JiraClientResolver = jiraResolver
	}

	// Start MultiUserRefreshManager with leader election (K8s) or directly (local dev).
	startRefreshManager(cfg, state)

	s := http.NewServer(state)
	if err := s.Start(); err != nil {
		panic(err)
	}
}

// startRefreshManager sets up and starts the MultiUserRefreshManager.
// In K8s (LeaderEnabled), it runs under leader election so only the leader
// pod performs proactive token refresh. In local dev mode, it starts directly.
func startRefreshManager(cfg common.Config, state *common.State) {
	// UserTokenStore may not be wired yet (task 11.3). If nil, skip.
	if state.UserTokenStore == nil {
		log.Warn("UserTokenStore not configured; skipping MultiUserRefreshManager setup")
		return
	}

	adapter := &refreshStoreAdapter{store: state.UserTokenStore}

	if cfg.LeaderEnabled {
		startWithLeaderElection(cfg, adapter)
	} else {
		// Local dev: start refresh manager directly without leader election.
		log.Info("Leader election disabled; starting MultiUserRefreshManager directly")
		mgr := jira.NewMultiUserRefreshManager(
			adapter,
			cfg.JiraClientID,
			cfg.JiraClientSecret,
			cfg.RefreshCheckInterval,
		)
		go mgr.Start(context.Background())
	}
}

// startWithLeaderElection creates a K8s client, builds a LeaderElector, and
// runs it in a background goroutine. The MultiUserRefreshManager starts when
// the leader lease is acquired and stops when it is lost.
func startWithLeaderElection(cfg common.Config, store jira.RefreshTokenStore) {
	k8sConfig, err := rest.InClusterConfig()
	if err != nil {
		log.Error("Failed to build in-cluster K8s config", "error", err)
		panic(err)
	}

	k8sClient, err := kubernetes.NewForConfig(k8sConfig)
	if err != nil {
		log.Error("Failed to create K8s client", "error", err)
		panic(err)
	}

	mgr := jira.NewMultiUserRefreshManager(
		store,
		cfg.JiraClientID,
		cfg.JiraClientSecret,
		cfg.RefreshCheckInterval,
	)

	leaseName := cfg.TokenLeaseName
	if leaseName == "" {
		leaseName = "jira-bot-leader"
	}

	leaderCfg := k8sadapter.LeaderElectorConfig{
		LeaseName:      leaseName,
		LeaseNamespace: cfg.PodNamespace,
		Identity:       cfg.PodName,
		LeaseDuration:  cfg.LeaseDuration,
		RenewDeadline:  cfg.LeaseRenewDeadline,
		RetryPeriod:    2 * time.Second,
	}

	callbacks := k8sadapter.LeaderCallbacks{
		OnStartedLeading: func(ctx context.Context) {
			log.Info("Acquired leader lease; starting MultiUserRefreshManager")
			mgr.Start(ctx)
		},
		OnStoppedLeading: func() {
			log.Info("Lost leader lease; stopping MultiUserRefreshManager")
			mgr.Stop()
		},
		OnNewLeader: func(identity string) {
			log.Info("New leader elected", "leader", identity)
		},
	}

	elector, err := k8sadapter.NewLeaderElector(k8sClient, leaderCfg, callbacks)
	if err != nil {
		log.Error("Failed to create leader elector", "error", err)
		panic(err)
	}

	go elector.Run(context.Background())
}

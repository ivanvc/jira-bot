package transition

import (
	"context"
	"time"

	"github.com/charmbracelet/log"
	"github.com/ivanvc/jira-bot/internal/adapters/jira"
	"github.com/ivanvc/jira-bot/internal/adapters/k8s"
	"github.com/ivanvc/jira-bot/internal/common"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// BuildOAuth2ClientFromTokenData constructs the Jira OAuth2 client from token data.
// This extracts the core logic from main.go's buildOAuth2Client so it can be
// called both at startup and during the live transition.
//
// It attempts to set up Kubernetes-based token persistence and leader election.
// On any failure related to K8s infrastructure, it gracefully degrades to standalone
// mode (direct token refresh without leader election or persistence).
//
// Returns the constructed client, a shutdown function that persists tokens on
// graceful termination, and an error if the client cannot be constructed at all.
func BuildOAuth2ClientFromTokenData(cfg common.Config, tokenData k8s.TokenData) (common.JiraClientInterface, func(), error) {
	logger := log.Default()

	// If leader election is not enabled (missing pod name/namespace), use standalone mode.
	if !cfg.LeaderEnabled {
		logger.Warn("Leader election disabled (POD_NAME or POD_NAMESPACE not set), using standalone token refresh")
		client := jira.NewOAuthClient(tokenData.CloudID, cfg.JiraClientID, cfg.JiraClientSecret, tokenData.RefreshToken)
		return client, nil, nil
	}

	// Attempt to build in-cluster Kubernetes client.
	k8sClient, err := buildK8sClient()
	if err != nil {
		logger.Warn("Failed to create Kubernetes client, falling back to standalone token refresh", "error", err)
		client := jira.NewOAuthClient(tokenData.CloudID, cfg.JiraClientID, cfg.JiraClientSecret, tokenData.RefreshToken)
		return client, nil, nil
	}

	// Create the token persistence adapter.
	adapter := k8s.NewTokenPersistenceAdapter(k8sClient, cfg.PodNamespace, cfg.TokenSecretName, logger)

	// Create the TokenManager with the refresh token from tokenData.
	tm := jira.NewTokenManager(cfg.JiraClientID, cfg.JiraClientSecret, tokenData.RefreshToken)

	// Pre-seed access token from tokenData so the first API call doesn't trigger
	// a redundant refresh.
	if tokenData.AccessToken != "" && tokenData.ExpiresAt.After(time.Now()) {
		tm.SetCachedToken(tokenData.AccessToken, tokenData.ExpiresAt)
		logger.Info("Pre-seeded access token from TokenData", "expires_at", tokenData.ExpiresAt)
	}

	// Create leader and follower token sources.
	// NewLeaderTokenSource installs a RefreshCallback on the TokenManager for
	// persist-before-use semantics (prevents token loss on abrupt termination).
	leaderSource := k8s.NewLeaderTokenSource(tm, adapter, logger)
	followerSource := k8s.NewFollowerTokenSource(adapter, cfg.PollInterval, logger)

	// Create the switchable token source (starts in follower mode).
	tokenSource := k8s.NewSwitchableTokenSource(leaderSource, followerSource)

	// Set up leader election with callbacks that switch between leader/follower.
	leaderCtx, leaderCancel := context.WithCancel(context.Background())
	leaderElector, err := k8s.NewLeaderElector(k8sClient, k8s.LeaderElectorConfig{
		LeaseName:      cfg.TokenLeaseName,
		LeaseNamespace: cfg.PodNamespace,
		Identity:       cfg.PodName,
		LeaseDuration:  cfg.LeaseDuration,
		RenewDeadline:  cfg.LeaseRenewDeadline,
	}, k8s.LeaderCallbacks{
		OnStartedLeading: func(ctx context.Context) {
			logger.Info("This pod is now the token refresh leader")
			tokenSource.SetLeader()
			// Start proactive refresh loop so the secret always has a valid token.
			go leaderSource.Start(leaderCtx)
		},
		OnStoppedLeading: func() {
			logger.Warn("This pod lost token refresh leadership, switching to follower mode")
			tokenSource.SetFollower()
			leaderCancel()
		},
		OnNewLeader: func(identity string) {
			if identity != cfg.PodName {
				logger.Info("New token refresh leader elected", "leader", identity)
			}
		},
	})
	if err != nil {
		// Leader election setup failed — fall back to standalone mode.
		leaderCancel()
		logger.Warn("Failed to create leader elector, falling back to independent token refresh", "error", err)
		client := jira.NewOAuthClient(tokenData.CloudID, cfg.JiraClientID, cfg.JiraClientSecret, tokenData.RefreshToken)
		return client, nil, nil
	}

	// Start the follower polling loop in the background.
	followerCtx, followerCancel := context.WithCancel(context.Background())
	go followerSource.Start(followerCtx)

	// Start the leader election campaign in the background.
	go leaderElector.Run(context.Background())

	// Shutdown function: cancel background goroutines and persist current token state.
	shutdownFn := func() {
		// Cancel background goroutines.
		followerCancel()
		leaderCancel()

		// Persist current token state on graceful termination.
		refreshTok, accessTok, expiresAt := tm.TokenState()
		if refreshTok == "" {
			return
		}
		data := k8s.TokenData{
			RefreshToken: refreshTok,
			AccessToken:  accessTok,
			ExpiresAt:    expiresAt,
			CloudID:      tokenData.CloudID,
		}
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if writeErr := adapter.Write(shutdownCtx, data); writeErr != nil {
			logger.Error("Failed to persist token on shutdown", "error", writeErr)
		} else {
			logger.Info("Token state persisted on shutdown")
		}
	}

	// Wire the switchable token source into the Jira client.
	client := jira.NewOAuthClientWithTokenSource(tokenData.CloudID, tokenSource)
	return client, shutdownFn, nil
}

// buildK8sClient creates an in-cluster Kubernetes client.
func buildK8sClient() (kubernetes.Interface, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, err
	}
	return kubernetes.NewForConfig(config)
}

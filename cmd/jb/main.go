package main

import (
	"context"
	"time"

	"github.com/charmbracelet/log"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/ivanvc/jira-bot/internal/adapters/github"
	"github.com/ivanvc/jira-bot/internal/adapters/jira"
	"github.com/ivanvc/jira-bot/internal/adapters/k8s"
	"github.com/ivanvc/jira-bot/internal/common"
	"github.com/ivanvc/jira-bot/internal/config"
	"github.com/ivanvc/jira-bot/internal/http"
)

func main() {
	cfg := common.LoadConfig()

	githubClient, err := github.NewClient(cfg.GitHubAppID, cfg.GitHubPrivateKey)
	if err != nil {
		panic(err)
	}

	var jiraClient common.JiraClientInterface
	switch cfg.AuthMode {
	case "oauth2":
		jiraClient = buildOAuth2Client(cfg)
	case "oauth2-setup":
		// No Jira client in setup mode — the bot only serves the OAuth setup endpoints
		jiraClient = nil
	default:
		jiraClient = jira.NewClient(cfg.JiraBaseURL, cfg.JiraUsername, cfg.JiraToken)
	}

	state := &common.State{
		Config:           cfg,
		GitHubClient:     githubClient,
		JiraClient:       jiraClient,
		RepoConfigLoader: config.NewLoader(githubClient),
	}

	s := http.NewServer(state)
	if err := s.Start(); err != nil {
		panic(err)
	}
}

// buildOAuth2Client constructs the Jira client for the "oauth2" auth mode.
// It attempts to set up Kubernetes-based token persistence and leader election.
// On any failure, it gracefully degrades to the current standalone behavior.
func buildOAuth2Client(cfg common.Config) common.JiraClientInterface {
	// If leader election is not enabled (missing pod name/namespace), use standalone mode.
	if !cfg.LeaderEnabled {
		log.Warn("Leader election disabled (POD_NAME or POD_NAMESPACE not set), using standalone token refresh")
		return jira.NewOAuthClient(cfg.JiraCloudID, cfg.JiraClientID, cfg.JiraClientSecret, cfg.JiraRefreshToken)
	}

	// Attempt to build in-cluster Kubernetes client.
	k8sClient, err := buildK8sClient()
	if err != nil {
		log.Warn("Failed to create Kubernetes client, falling back to standalone token refresh", "error", err)
		return jira.NewOAuthClient(cfg.JiraCloudID, cfg.JiraClientID, cfg.JiraClientSecret, cfg.JiraRefreshToken)
	}

	// Create the token persistence adapter.
	logger := log.Default()
	adapter := k8s.NewTokenPersistenceAdapter(k8sClient, cfg.PodNamespace, cfg.TokenSecretName, logger)

	// Read initial tokens from the Bot_Secret.
	ctx := context.Background()
	initialTokens, err := adapter.Read(ctx)
	if err != nil {
		// Detect RBAC errors (403) and log a clear message (Req 9.3).
		if k8serrors.IsForbidden(err) {
			log.Error("RBAC permissions missing: the bot's ServiceAccount does not have access to read secrets. "+
				"Ensure a Role with get/create/update permissions on secrets is bound to the ServiceAccount. "+
				"Falling back to environment variable token.", "error", err)
		} else {
			log.Warn("Failed to read token secret, falling back to environment variable token", "error", err)
		}
		return jira.NewOAuthClient(cfg.JiraCloudID, cfg.JiraClientID, cfg.JiraClientSecret, cfg.JiraRefreshToken)
	}

	// Select the initial refresh token (Req 2.1, 2.2, 2.3).
	refreshToken := k8s.PickRefreshToken(initialTokens, cfg.JiraRefreshToken, logger)

	// Create the TokenManager with the selected refresh token.
	tm := jira.NewTokenManager(cfg.JiraClientID, cfg.JiraClientSecret, refreshToken)

	// Pre-seed the access token if the persisted one is still valid (Req 2.4).
	if initialTokens.AccessToken != "" && initialTokens.ExpiresAt.After(time.Now()) {
		tm.SetCachedToken(initialTokens.AccessToken, initialTokens.ExpiresAt)
		log.Info("Pre-seeded access token from Bot_Secret", "expires_at", initialTokens.ExpiresAt)
	}

	// Create leader and follower token sources.
	leaderSource := k8s.NewLeaderTokenSource(tm, adapter, logger)
	followerSource := k8s.NewFollowerTokenSource(adapter, cfg.PollInterval, logger)

	// Create the switchable token source (starts in follower mode).
	tokenSource := k8s.NewSwitchableTokenSource(leaderSource, followerSource)

	// Set up leader election with callbacks that switch between leader/follower.
	leaderElector, err := k8s.NewLeaderElector(k8sClient, k8s.LeaderElectorConfig{
		LeaseName:      cfg.TokenLeaseName,
		LeaseNamespace: cfg.PodNamespace,
		Identity:       cfg.PodName,
		LeaseDuration:  cfg.LeaseDuration,
		RenewDeadline:  cfg.LeaseRenewDeadline,
	}, k8s.LeaderCallbacks{
		OnStartedLeading: func(ctx context.Context) {
			log.Info("This pod is now the token refresh leader")
			tokenSource.SetLeader()
		},
		OnStoppedLeading: func() {
			log.Warn("This pod lost token refresh leadership, switching to follower mode")
			tokenSource.SetFollower()
		},
		OnNewLeader: func(identity string) {
			if identity != cfg.PodName {
				log.Info("New token refresh leader elected", "leader", identity)
			}
		},
	})
	if err != nil {
		// Leader election setup failed — fall back to standalone mode (Req 9.4).
		log.Warn("Failed to create leader elector, falling back to independent token refresh", "error", err)
		return jira.NewOAuthClient(cfg.JiraCloudID, cfg.JiraClientID, cfg.JiraClientSecret, refreshToken)
	}

	// Start the follower polling loop in the background.
	followerCtx, followerCancel := context.WithCancel(context.Background())
	_ = followerCancel // followerCancel will be used if we need graceful shutdown later
	go followerSource.Start(followerCtx)

	// Start the leader election campaign in the background.
	go leaderElector.Run(context.Background())

	// Wire the switchable token source into the Jira client.
	return jira.NewOAuthClientWithTokenSource(cfg.JiraCloudID, tokenSource)
}

// buildK8sClient creates an in-cluster Kubernetes client.
func buildK8sClient() (kubernetes.Interface, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, err
	}
	return kubernetes.NewForConfig(config)
}

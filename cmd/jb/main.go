package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
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
	var shutdownFn func()
	switch cfg.AuthMode {
	case "oauth2":
		jiraClient, shutdownFn = buildOAuth2Client(cfg)
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

	// In setup mode, construct K8s client and TokenPersistenceAdapter for auto-persisting tokens.
	if cfg.AuthMode == "oauth2-setup" && cfg.PodNamespace != "" && cfg.TokenSecretName != "" {
		k8sClient, err := buildK8sClient()
		if err != nil {
			log.Warn("Failed to create Kubernetes client for setup mode, token auto-persistence will be unavailable", "error", err)
		} else {
			logger := log.Default()
			state.TokenPersistenceAdapter = k8s.NewTokenPersistenceAdapter(k8sClient, cfg.PodNamespace, cfg.TokenSecretName, logger)
		}
	}

	// Handle SIGTERM for graceful shutdown (spot instance termination).
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		sig := <-sigCh
		log.Info("Received shutdown signal, persisting token state", "signal", sig)
		if shutdownFn != nil {
			shutdownFn()
		}
		os.Exit(0)
	}()

	s := http.NewServer(state)
	if err := s.Start(); err != nil {
		panic(err)
	}
}

// buildOAuth2Client constructs the Jira client for the "oauth2" auth mode.
// It attempts to set up Kubernetes-based token persistence and leader election.
// On any failure, it gracefully degrades to the current standalone behavior.
// Returns the client and a shutdown function that persists tokens on graceful termination.
func buildOAuth2Client(cfg common.Config) (common.JiraClientInterface, func()) {
	// If leader election is not enabled (missing pod name/namespace), use standalone mode.
	if !cfg.LeaderEnabled {
		log.Warn("Leader election disabled (POD_NAME or POD_NAMESPACE not set), using standalone token refresh")
		return jira.NewOAuthClient(cfg.TokenData.CloudID, cfg.JiraClientID, cfg.JiraClientSecret, cfg.TokenData.RefreshToken), nil
	}

	// Attempt to build in-cluster Kubernetes client.
	k8sClient, err := buildK8sClient()
	if err != nil {
		log.Warn("Failed to create Kubernetes client, falling back to standalone token refresh", "error", err)
		return jira.NewOAuthClient(cfg.TokenData.CloudID, cfg.JiraClientID, cfg.JiraClientSecret, cfg.TokenData.RefreshToken), nil
	}

	// Create the token persistence adapter.
	logger := log.Default()
	adapter := k8s.NewTokenPersistenceAdapter(k8sClient, cfg.PodNamespace, cfg.TokenSecretName, logger)

	// Create the TokenManager with the refresh token from Bot_Secret.
	tm := jira.NewTokenManager(cfg.JiraClientID, cfg.JiraClientSecret, cfg.TokenData.RefreshToken)

	// Attempt to read persisted access token for pre-seeding only.
	ctx := context.Background()
	initialTokens, err := adapter.Read(ctx)
	if err != nil {
		// Log but don't fail — we already have the refresh token from cfg.TokenData.
		if k8serrors.IsForbidden(err) {
			log.Error("RBAC permissions missing: the bot's ServiceAccount does not have access to read secrets. "+
				"Ensure a Role with get/create/update permissions on secrets is bound to the ServiceAccount.", "error", err)
		} else {
			log.Warn("Failed to read token secret for access token pre-seeding, skipping", "error", err)
		}
	} else if initialTokens.AccessToken != "" && initialTokens.ExpiresAt.After(time.Now()) {
		// Pre-seed the access token if the persisted one is still valid.
		tm.SetCachedToken(initialTokens.AccessToken, initialTokens.ExpiresAt)
		log.Info("Pre-seeded access token from Bot_Secret", "expires_at", initialTokens.ExpiresAt)
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
	_ = leaderCancel // leaderCancel will be used if we need graceful shutdown later
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
			// Start proactive refresh loop so the secret always has a valid token.
			go leaderSource.Start(leaderCtx)
		},
		OnStoppedLeading: func() {
			log.Warn("This pod lost token refresh leadership, switching to follower mode")
			tokenSource.SetFollower()
			leaderCancel()
		},
		OnNewLeader: func(identity string) {
			if identity != cfg.PodName {
				log.Info("New token refresh leader elected", "leader", identity)
			}
		},
	})
	if err != nil {
		// Leader election setup failed — fall back to standalone mode.
		log.Warn("Failed to create leader elector, falling back to independent token refresh", "error", err)
		return jira.NewOAuthClient(cfg.TokenData.CloudID, cfg.JiraClientID, cfg.JiraClientSecret, cfg.TokenData.RefreshToken), nil
	}

	// Start the follower polling loop in the background.
	followerCtx, followerCancel := context.WithCancel(context.Background())
	_ = followerCancel // followerCancel will be used if we need graceful shutdown later
	go followerSource.Start(followerCtx)

	// Start the leader election campaign in the background.
	go leaderElector.Run(context.Background())

	// Shutdown function: persist current token state on graceful termination.
	shutdownFn := func() {
		refreshTok, accessTok, expiresAt := tm.TokenState()
		if refreshTok == "" {
			return
		}
		data := k8s.TokenData{
			RefreshToken: refreshTok,
			AccessToken:  accessTok,
			ExpiresAt:    expiresAt,
		}
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if writeErr := adapter.Write(shutdownCtx, data); writeErr != nil {
			log.Error("Failed to persist token on shutdown", "error", writeErr)
		} else {
			log.Info("Token state persisted on shutdown")
		}
	}

	// Wire the switchable token source into the Jira client.
	return jira.NewOAuthClientWithTokenSource(cfg.TokenData.CloudID, tokenSource), shutdownFn
}

// buildK8sClient creates an in-cluster Kubernetes client.
func buildK8sClient() (kubernetes.Interface, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, err
	}
	return kubernetes.NewForConfig(config)
}

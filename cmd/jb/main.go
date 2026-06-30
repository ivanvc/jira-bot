package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/charmbracelet/log"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/ivanvc/jira-bot/internal/adapters/github"
	"github.com/ivanvc/jira-bot/internal/adapters/jira"
	"github.com/ivanvc/jira-bot/internal/adapters/k8s"
	"github.com/ivanvc/jira-bot/internal/common"
	"github.com/ivanvc/jira-bot/internal/config"
	"github.com/ivanvc/jira-bot/internal/http"
	"github.com/ivanvc/jira-bot/internal/transition"
)

func init() {
	// Wire up the production BotSecretReader factory that uses an in-cluster K8s client.
	common.NewBotSecretReader = func(namespace, secretName string) (common.BotSecretReader, error) {
		k8sClient, err := buildK8sClient()
		if err != nil {
			return nil, err
		}
		logger := log.Default()
		return k8s.NewTokenPersistenceAdapter(k8sClient, namespace, secretName, logger), nil
	}
}

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
		var clientErr error
		jiraClient, shutdownFn, clientErr = transition.BuildOAuth2ClientFromTokenData(cfg, cfg.TokenData)
		if clientErr != nil {
			log.Fatal("Failed to construct OAuth2 client at startup", "error", clientErr)
		}
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

	// Create the TransitionCoordinator for setup mode so the setup handler
	// can trigger a live transition after successful token persistence.
	var coordinator *transition.TransitionCoordinator
	if cfg.AuthMode == "oauth2-setup" {
		// We create the server first, then the coordinator needs the server's mux.
		// However, NewServer needs the coordinator. To break this cycle, we create
		// the server with nil coordinator first, build the coordinator, then rebuild
		// the setup mux with the coordinator and swap it in.
		s := http.NewServer(state, nil)

		logger := log.Default()
		coordinator = transition.NewTransitionCoordinator(
			state,
			s.Mux,
			transition.BuildOAuth2ClientFromTokenData,
			http.BuildOAuth2Mux,
			logger,
		)

		// Rebuild setup mux with the coordinator wired in and swap it.
		setupMux := http.BuildSetupMux(state, coordinator)
		s.Mux.Swap(setupMux)

		if err := s.Start(); err != nil {
			panic(err)
		}
	} else {
		s := http.NewServer(state, nil)
		if err := s.Start(); err != nil {
			panic(err)
		}
	}
}

// buildK8sClient creates an in-cluster Kubernetes client.
func buildK8sClient() (kubernetes.Interface, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, err
	}
	return kubernetes.NewForConfig(config)
}

package common

import (
	"context"
	"os"
	"strconv"
	"time"

	"github.com/charmbracelet/log"
	"github.com/ivanvc/jira-bot/internal/adapters/k8s"
)

// BotSecretReader abstracts reading token data from the Bot_Secret.
// This enables testability without requiring a real K8s cluster.
type BotSecretReader interface {
	Read(ctx context.Context) (k8s.TokenData, error)
}

// NewBotSecretReader creates a BotSecretReader from the K8s environment.
// Override in tests to inject a mock.
var NewBotSecretReader func(namespace, secretName string) (BotSecretReader, error)

// determineAuthMode determines the auth mode based on client credentials
// and Bot_Secret contents. Returns the auth mode and the token data from
// the secret (if available).
//
// Decision table:
//   - Client credentials absent → ("", empty TokenData, nil) — caller handles basic/fatal
//   - Client credentials present, reader nil → ("oauth2-setup", empty TokenData, nil) with warning
//   - Client credentials present, read error → ("oauth2-setup", empty TokenData, nil) with warning
//   - Client credentials present, secret has refresh token + Cloud ID → ("oauth2", token data, nil)
//   - Client credentials present, secret missing refresh token or Cloud ID → ("oauth2-setup", empty TokenData, nil)
func determineAuthMode(clientID, clientSecret string, reader BotSecretReader) (authMode string, tokenData k8s.TokenData, err error) {
	if clientID == "" || clientSecret == "" {
		return "", k8s.TokenData{}, nil
	}

	if reader == nil {
		log.Warn("Bot_Secret reader not available (K8s not configured), entering setup mode")
		return "oauth2-setup", k8s.TokenData{}, nil
	}

	data, readErr := reader.Read(context.Background())
	if readErr != nil {
		log.Warn("Failed to read Bot_Secret, entering setup mode", "error", readErr)
		return "oauth2-setup", k8s.TokenData{}, nil
	}

	if data.RefreshToken != "" && data.CloudID != "" {
		return "oauth2", data, nil
	}

	return "oauth2-setup", k8s.TokenData{}, nil
}

type Config struct {
	ListenHTTP string

	GitHubAppID         int64
	GitHubPrivateKey    string
	GitHubWebhookSecret string

	// OAuth 2.0 fields
	JiraClientID     string
	JiraClientSecret string
	OAuthCallbackURL string

	// Legacy fields
	JiraBaseURL  string
	JiraUsername string
	JiraToken    string

	JiraDefaultProject   string
	JiraDefaultIssueType string

	// Derived: "oauth2", "oauth2-setup", or "basic"
	AuthMode string

	// TokenData holds the tokens read from the Bot_Secret during startup.
	// Populated by LoadConfig when AuthMode is "oauth2".
	TokenData k8s.TokenData

	// Token persistence fields (set via env vars from Helm)
	TokenSecretName    string        // JIRA_BOT_TOKEN_SECRET_NAME
	TokenLeaseName     string        // JIRA_BOT_TOKEN_LEASE_NAME
	PodName            string        // POD_NAME (from downward API)
	PodNamespace       string        // POD_NAMESPACE (from downward API)
	LeaderEnabled      bool          // derived: true when PodName and PodNamespace are both set
	PollInterval       time.Duration // JIRA_BOT_TOKEN_POLL_INTERVAL, default 30s
	LeaseDuration      time.Duration // JIRA_BOT_LEASE_DURATION, default 15s
	LeaseRenewDeadline time.Duration // JIRA_BOT_LEASE_RENEW_DEADLINE, default 10s
}

func LoadConfig() Config {
	cfg := Config{
		ListenHTTP:           loadEnvWithDefault("JIRA_BOT_LISTEN_HTTP", ":8080"),
		GitHubAppID:          loadEnvInt64("JIRA_BOT_GITHUB_APP_ID"),
		GitHubPrivateKey:     loadEnv("JIRA_BOT_GITHUB_PRIVATE_KEY"),
		GitHubWebhookSecret:  loadEnv("JIRA_BOT_GITHUB_WEBHOOK_SECRET"),
		JiraDefaultProject:   loadEnv("JIRA_BOT_JIRA_DEFAULT_PROJECT"),
		JiraDefaultIssueType: loadEnv("JIRA_BOT_JIRA_DEFAULT_ISSUE_TYPE"),
	}

	// Step 1: Check for client credentials (OAuth path)
	clientID := loadEnvWithDefault("JIRA_BOT_JIRA_CLIENT_ID", "")
	clientSecret := loadEnvWithDefault("JIRA_BOT_JIRA_CLIENT_SECRET", "")

	if clientID != "" && clientSecret != "" {
		// Client credentials present — determine auth mode via Bot_Secret
		cfg.JiraClientID = clientID
		cfg.JiraClientSecret = clientSecret

		// Create BotSecretReader if possible
		var reader BotSecretReader
		namespace := loadEnvWithDefault("POD_NAMESPACE", "")
		secretName := loadEnvWithDefault("JIRA_BOT_TOKEN_SECRET_NAME", "")
		if NewBotSecretReader != nil && namespace != "" && secretName != "" {
			r, err := NewBotSecretReader(namespace, secretName)
			if err != nil {
				log.Warn("Failed to create BotSecretReader", "error", err)
			} else {
				reader = r
			}
		}

		authMode, tokenData, _ := determineAuthMode(clientID, clientSecret, reader)
		cfg.AuthMode = authMode
		cfg.TokenData = tokenData

		if authMode == "oauth2-setup" {
			cfg.OAuthCallbackURL = loadEnvWithDefault("JIRA_BOT_OAUTH_CALLBACK_URL", "")
			log.Info("OAuth 2.0 setup mode — visit the root URL to complete setup")
		} else if authMode == "oauth2" {
			log.Info("OAuth 2.0 authentication is active")
		}
	} else {
		// Step 2: No client credentials — check for basic auth vars
		baseURL := loadEnvWithDefault("JIRA_BOT_JIRA_BASE_URL", "")
		username := loadEnvWithDefault("JIRA_BOT_JIRA_USERNAME", "")
		token := loadEnvWithDefault("JIRA_BOT_JIRA_TOKEN", "")

		if baseURL != "" && username != "" && token != "" {
			cfg.AuthMode = "basic"
			cfg.JiraBaseURL = baseURL
			cfg.JiraUsername = username
			cfg.JiraToken = token
		} else {
			// Step 3: No valid auth configuration found
			log.Fatal("No valid auth configuration found. Provide either JIRA_BOT_JIRA_CLIENT_ID + JIRA_BOT_JIRA_CLIENT_SECRET (OAuth) or JIRA_BOT_JIRA_BASE_URL + JIRA_BOT_JIRA_USERNAME + JIRA_BOT_JIRA_TOKEN (basic auth)")
		}
	}

	// Token persistence config — loaded optionally (feature degrades gracefully if missing)
	cfg.TokenSecretName = loadEnvWithDefault("JIRA_BOT_TOKEN_SECRET_NAME", "")
	cfg.TokenLeaseName = loadEnvWithDefault("JIRA_BOT_TOKEN_LEASE_NAME", "")
	cfg.PodName = loadEnvWithDefault("POD_NAME", "")
	cfg.PodNamespace = loadEnvWithDefault("POD_NAMESPACE", "")
	cfg.LeaderEnabled = cfg.PodName != "" && cfg.PodNamespace != ""
	cfg.PollInterval = loadEnvDuration("JIRA_BOT_TOKEN_POLL_INTERVAL", 30*time.Second)
	cfg.LeaseDuration = loadEnvDuration("JIRA_BOT_LEASE_DURATION", 15*time.Second)
	cfg.LeaseRenewDeadline = loadEnvDuration("JIRA_BOT_LEASE_RENEW_DEADLINE", 10*time.Second)

	return cfg
}

func loadEnvWithDefault(variable, fallback string) string {
	if v, ok := os.LookupEnv(variable); ok {
		return v
	}
	return fallback
}

func loadEnv(variable string) string {
	if v, ok := os.LookupEnv(variable); ok && v != "" {
		return v
	}

	log.Fatalf("Environment variable %q not provided", variable)
	return ""
}

func loadEnvInt64(variable string) int64 {
	v := loadEnv(variable)
	i, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		log.Fatalf("Environment variable %q must be a valid integer", variable)
	}
	return i
}

// loadEnvDuration loads a duration from the given environment variable, returning
// the fallback value if the variable is unset or empty. Logs a warning and returns
// the fallback if the value cannot be parsed as a duration.
func loadEnvDuration(variable string, fallback time.Duration) time.Duration {
	v, ok := os.LookupEnv(variable)
	if !ok || v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		log.Warnf("Environment variable %q has invalid duration %q, using default %s", variable, v, fallback)
		return fallback
	}
	return d
}

package common

import (
	"os"
	"strconv"
	"time"

	"github.com/charmbracelet/log"
)

type Config struct {
	ListenHTTP string

	GitHubAppID         int64
	GitHubPrivateKey    string
	GitHubWebhookSecret string

	// Atlassian OAuth 2.0 client credentials (used for per-user token refresh)
	JiraClientID     string
	JiraClientSecret string

	JiraDefaultProject   string
	JiraDefaultIssueType string

	// Kubernetes pod identity (from downward API)
	PodName      string // POD_NAME
	PodNamespace string // POD_NAMESPACE

	// Leader election config
	LeaderEnabled      bool          // derived: true when PodName and PodNamespace are both set
	LeaseDuration      time.Duration // JIRA_BOT_LEASE_DURATION, default 15s
	LeaseRenewDeadline time.Duration // JIRA_BOT_LEASE_RENEW_DEADLINE, default 10s
	TokenLeaseName     string        // JIRA_BOT_TOKEN_LEASE_NAME

	// Per-user token fields
	GitHubAppClientSecret string        // JIRA_BOT_GITHUB_APP_CLIENT_SECRET
	UserTokenSecretName   string        // JIRA_BOT_USER_TOKEN_SECRET_NAME
	UserAuthCallbackURL   string        // JIRA_BOT_USER_AUTH_CALLBACK_URL
	RefreshCheckInterval  time.Duration // JIRA_BOT_REFRESH_CHECK_INTERVAL, default 30s, clamped [10s, 300s]

	// Global Cloud ID for Atlassian site (used when user authorizes)
	GlobalCloudID string // JIRA_BOT_GLOBAL_CLOUD_ID
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

	// Atlassian OAuth client credentials (for per-user token refresh)
	cfg.JiraClientID = loadEnvWithDefault("JIRA_BOT_JIRA_CLIENT_ID", "")
	cfg.JiraClientSecret = loadEnvWithDefault("JIRA_BOT_JIRA_CLIENT_SECRET", "")

	// Kubernetes pod identity
	cfg.PodName = loadEnvWithDefault("POD_NAME", "")
	cfg.PodNamespace = loadEnvWithDefault("POD_NAMESPACE", "")
	cfg.LeaderEnabled = cfg.PodName != "" && cfg.PodNamespace != ""

	// Leader election config
	cfg.TokenLeaseName = loadEnvWithDefault("JIRA_BOT_TOKEN_LEASE_NAME", "")
	cfg.LeaseDuration = loadEnvDuration("JIRA_BOT_LEASE_DURATION", 15*time.Second)
	cfg.LeaseRenewDeadline = loadEnvDuration("JIRA_BOT_LEASE_RENEW_DEADLINE", 10*time.Second)

	// Per-user token config
	cfg.GitHubAppClientSecret = loadEnvWithDefault("JIRA_BOT_GITHUB_APP_CLIENT_SECRET", "")
	cfg.UserTokenSecretName = loadEnvWithDefault("JIRA_BOT_USER_TOKEN_SECRET_NAME", "")
	cfg.UserAuthCallbackURL = loadEnvWithDefault("JIRA_BOT_USER_AUTH_CALLBACK_URL", "")
	cfg.RefreshCheckInterval = loadEnvDurationClamped("JIRA_BOT_REFRESH_CHECK_INTERVAL", 30*time.Second, 10*time.Second, 300*time.Second)

	// Global Cloud ID
	cfg.GlobalCloudID = loadEnvWithDefault("JIRA_BOT_GLOBAL_CLOUD_ID", "")

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

// loadEnvDurationClamped loads a duration from the given environment variable,
// defaulting to fallback if unset/empty/invalid, then clamps the result to [min, max].
func loadEnvDurationClamped(variable string, fallback, min, max time.Duration) time.Duration {
	d := loadEnvDuration(variable, fallback)
	if d < min {
		d = min
	}
	if d > max {
		d = max
	}
	return d
}

package common

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/log"
)

// OAuthEnvVars are the environment variable names for OAuth 2.0 configuration.
var OAuthEnvVars = [4]string{
	"JIRA_BOT_JIRA_CLIENT_ID",
	"JIRA_BOT_JIRA_CLIENT_SECRET",
	"JIRA_BOT_JIRA_REFRESH_TOKEN",
	"JIRA_BOT_JIRA_CLOUD_ID",
}

type Config struct {
	ListenHTTP string

	GitHubAppID         int64
	GitHubPrivateKey    string
	GitHubWebhookSecret string

	// OAuth 2.0 fields
	JiraClientID     string
	JiraClientSecret string
	JiraRefreshToken string
	JiraCloudID      string
	OAuthCallbackURL string

	// Legacy fields
	JiraBaseURL  string
	JiraUsername string
	JiraToken    string

	JiraDefaultProject   string
	JiraDefaultIssueType string

	// Derived: "oauth2" or "basic"
	AuthMode string

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

// ValidateOAuthEnv checks which OAuth environment variables are set and returns
// an error naming all missing/empty variables if the set is partially configured.
// Returns allPresent=true if all 4 are set. Returns setupMode=true if only
// client ID and client secret are set (for initial OAuth setup flow).
// Returns a non-nil error if the configuration is ambiguous (e.g., refresh token
// set without cloud ID, or cloud ID set without refresh token).
func ValidateOAuthEnv() (allPresent bool, setupMode bool, err error) {
	values := make(map[string]string, 4)
	for _, name := range OAuthEnvVars {
		values[name] = os.Getenv(name)
	}

	clientID := values["JIRA_BOT_JIRA_CLIENT_ID"]
	clientSecret := values["JIRA_BOT_JIRA_CLIENT_SECRET"]
	refreshToken := values["JIRA_BOT_JIRA_REFRESH_TOKEN"]
	cloudID := values["JIRA_BOT_JIRA_CLOUD_ID"]

	if clientID != "" && clientSecret != "" && refreshToken != "" && cloudID != "" {
		return true, false, nil
	}

	// Setup mode: only client credentials, no refresh token or cloud ID
	if clientID != "" && clientSecret != "" && refreshToken == "" && cloudID == "" {
		return false, true, nil
	}

	// Count how many are set
	var present int
	var missing []string
	for _, name := range OAuthEnvVars {
		if values[name] != "" {
			present++
		} else {
			missing = append(missing, name)
		}
	}

	if present == 0 {
		// None set — legacy mode
		return false, false, nil
	}

	// Partial config that isn't the setup pattern
	return false, false, fmt.Errorf("missing required OAuth environment variables: %s", strings.Join(missing, ", "))
}

func LoadConfig() Config {
	cfg := Config{
		ListenHTTP:          loadEnvWithDefault("JIRA_BOT_LISTEN_HTTP", ":8080"),
		GitHubAppID:         loadEnvInt64("JIRA_BOT_GITHUB_APP_ID"),
		GitHubPrivateKey:    loadEnv("JIRA_BOT_GITHUB_PRIVATE_KEY"),
		GitHubWebhookSecret: loadEnv("JIRA_BOT_GITHUB_WEBHOOK_SECRET"),
		JiraDefaultProject:  loadEnv("JIRA_BOT_JIRA_DEFAULT_PROJECT"),
		JiraDefaultIssueType: loadEnv("JIRA_BOT_JIRA_DEFAULT_ISSUE_TYPE"),
	}

	allPresent, setupMode, err := ValidateOAuthEnv()
	if err != nil {
		log.Fatalf("%s", err.Error())
	}

	if allPresent {
		cfg.AuthMode = "oauth2"
		cfg.JiraClientID = os.Getenv("JIRA_BOT_JIRA_CLIENT_ID")
		cfg.JiraClientSecret = os.Getenv("JIRA_BOT_JIRA_CLIENT_SECRET")
		cfg.JiraRefreshToken = os.Getenv("JIRA_BOT_JIRA_REFRESH_TOKEN")
		cfg.JiraCloudID = os.Getenv("JIRA_BOT_JIRA_CLOUD_ID")
		// Legacy vars not required in OAuth mode
		cfg.JiraBaseURL = loadEnvWithDefault("JIRA_BOT_JIRA_BASE_URL", "")
		cfg.JiraUsername = loadEnvWithDefault("JIRA_BOT_JIRA_USERNAME", "")
		cfg.JiraToken = loadEnvWithDefault("JIRA_BOT_JIRA_TOKEN", "")
		log.Info("OAuth 2.0 authentication is active")
	} else if setupMode {
		cfg.AuthMode = "oauth2-setup"
		cfg.JiraClientID = os.Getenv("JIRA_BOT_JIRA_CLIENT_ID")
		cfg.JiraClientSecret = os.Getenv("JIRA_BOT_JIRA_CLIENT_SECRET")
		cfg.OAuthCallbackURL = loadEnv("JIRA_BOT_OAUTH_CALLBACK_URL")
		log.Info("OAuth 2.0 setup mode — visit /jira/oauth/authorize to complete setup")
	} else {
		cfg.AuthMode = "basic"
		cfg.JiraBaseURL = loadEnv("JIRA_BOT_JIRA_BASE_URL")
		cfg.JiraUsername = loadEnv("JIRA_BOT_JIRA_USERNAME")
		cfg.JiraToken = loadEnv("JIRA_BOT_JIRA_TOKEN")
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

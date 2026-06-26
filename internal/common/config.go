package common

import (
	"fmt"
	"os"
	"strconv"
	"strings"

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

	// Legacy fields
	JiraBaseURL  string
	JiraUsername string
	JiraToken    string

	JiraDefaultProject   string
	JiraDefaultIssueType string

	// Derived: "oauth2" or "basic"
	AuthMode string
}

// ValidateOAuthEnv checks which OAuth environment variables are set and returns
// an error naming all missing/empty variables if the set is partially configured.
// Returns a non-nil error if at least one but not all OAuth vars are present, or
// if none are present (for the caller to fall back to legacy mode).
// The error message contains the name of every missing/empty variable.
func ValidateOAuthEnv() (allPresent bool, err error) {
	var missing []string
	var present int
	for _, name := range OAuthEnvVars {
		v := os.Getenv(name)
		if v == "" {
			missing = append(missing, name)
		} else {
			present++
		}
	}

	if present == 4 {
		return true, nil
	}

	if present > 0 {
		// Partial config: some OAuth vars set, some missing
		return false, fmt.Errorf("missing required OAuth environment variables: %s", strings.Join(missing, ", "))
	}

	// None set — not an error, just means we use legacy mode
	return false, nil
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

	allPresent, err := ValidateOAuthEnv()
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
	} else {
		cfg.AuthMode = "basic"
		cfg.JiraBaseURL = loadEnv("JIRA_BOT_JIRA_BASE_URL")
		cfg.JiraUsername = loadEnv("JIRA_BOT_JIRA_USERNAME")
		cfg.JiraToken = loadEnv("JIRA_BOT_JIRA_TOKEN")
	}

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

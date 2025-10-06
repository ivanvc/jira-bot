package common

import (
	"os"
	"strconv"

	"github.com/charmbracelet/log"
)

type Config struct {
	ListenHTTP string

	GitHubAppID         int64
	GitHubPrivateKey    string
	GitHubWebhookSecret string

	JiraBaseURL          string
	JiraUsername         string
	JiraToken            string
	JiraDefaultProject   string
	JiraDefaultIssueType string
}

func LoadConfig() Config {
	return Config{
		ListenHTTP:           loadEnvWithDefault("JIRA_BOT_LISTEN_HTTP", ":8080"),
		GitHubAppID:          loadEnvInt64("JIRA_BOT_GITHUB_APP_ID"),
		GitHubPrivateKey:     loadEnv("JIRA_BOT_GITHUB_PRIVATE_KEY"),
		GitHubWebhookSecret:  loadEnv("JIRA_BOT_GITHUB_WEBHOOK_SECRET"),
		JiraBaseURL:          loadEnv("JIRA_BOT_JIRA_BASE_URL"),
		JiraUsername:         loadEnv("JIRA_BOT_JIRA_USERNAME"),
		JiraToken:            loadEnv("JIRA_BOT_JIRA_TOKEN"),
		JiraDefaultProject:   loadEnv("JIRA_BOT_JIRA_DEFAULT_PROJECT"),
		JiraDefaultIssueType: loadEnv("JIRA_BOT_JIRA_DEFAULT_ISSUE_TYPE"),
	}
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

package common

import (
	"os"

	"github.com/charmbracelet/log"
)

type Config struct {
	ListenHTTP string

	GitHubToken string

	JiraBaseURL          string
	JiraUsername         string
	JiraToken            string
	JiraDefaultProject   string
	JiraDefaultIssueType string
}

func LoadConfig() Config {
	return Config{
		ListenHTTP:           loadEnvWithDefault("JIRA_BOT_LISTEN_HTTP", ":8080"),
		GitHubToken:          loadEnv("JIRA_BOT_GITHUB_TOKEN"),
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

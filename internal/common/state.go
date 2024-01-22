package common

import (
	"github.com/ivanvc/jira-bot/internal/adapters/github"
	"github.com/ivanvc/jira-bot/internal/adapters/jira"
)

type State struct {
	Config
	GitHubClient *github.Client
	JiraClient   *jira.Client
}

package main

import (
	"github.com/ivanvc/jira-bot/internal/adapters/github"
	"github.com/ivanvc/jira-bot/internal/adapters/jira"
	"github.com/ivanvc/jira-bot/internal/common"
	"github.com/ivanvc/jira-bot/internal/http"
)

func main() {
	cfg := common.LoadConfig()

	githubClient, err := github.NewClient(cfg.GitHubAppID, cfg.GitHubPrivateKey)
	if err != nil {
		panic(err)
	}

	var jiraClient common.JiraClientInterface
	if cfg.AuthMode == "oauth2" {
		jiraClient = jira.NewOAuthClient(cfg.JiraCloudID, cfg.JiraClientID, cfg.JiraClientSecret, cfg.JiraRefreshToken)
	} else {
		jiraClient = jira.NewClient(cfg.JiraBaseURL, cfg.JiraUsername, cfg.JiraToken)
	}

	state := &common.State{
		Config:       cfg,
		GitHubClient: githubClient,
		JiraClient:   jiraClient,
	}

	s := http.NewServer(state)
	if err := s.Start(); err != nil {
		panic(err)
	}
}

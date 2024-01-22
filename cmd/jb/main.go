package main

import (
	"github.com/ivanvc/jira-bot/internal/adapters/github"
	"github.com/ivanvc/jira-bot/internal/adapters/jira"
	"github.com/ivanvc/jira-bot/internal/common"
	"github.com/ivanvc/jira-bot/internal/http"
)

func main() {
	cfg := common.LoadConfig()
	common := &common.State{
		Config:       cfg,
		GitHubClient: github.NewClient(cfg.GitHubToken),
		JiraClient:   jira.NewClient(cfg.JiraBaseURL, cfg.JiraUsername, cfg.JiraToken),
	}

	s := http.NewServer(common)
	if err := s.Start(); err != nil {
		panic(err)
	}
}

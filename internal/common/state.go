package common

import "github.com/ivanvc/jira-bot/internal/adapters/k8s"

type State struct {
	Config
	GitHubClient            GitHubClientInterface
	JiraClient              JiraClientInterface
	RepoConfigLoader        RepoConfigLoaderInterface
	TokenPersistenceAdapter *k8s.TokenPersistenceAdapter // nil when K8s unavailable
}

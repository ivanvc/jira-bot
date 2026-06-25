package common

type State struct {
	Config
	GitHubClient GitHubClientInterface
	JiraClient   JiraClientInterface
}

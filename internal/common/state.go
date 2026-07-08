package common

type State struct {
	Config
	GitHubClient       GitHubClientInterface
	RepoConfigLoader   RepoConfigLoaderInterface
	UserTokenStore     UserTokenStore     // per-user token persistence
	JiraClientResolver JiraClientResolver // resolves per-user Jira clients
}

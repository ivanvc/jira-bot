package common

import (
	"context"

	"github.com/ivanvc/jira-bot/internal/adapters/github"
	"github.com/ivanvc/jira-bot/internal/config"
)

// GitHubClientInterface defines the methods used by the executor and handlers
// to interact with GitHub. This enables dependency injection of mock
// implementations during testing.
type GitHubClientInterface interface {
	ReactWithThumbsUp(ctx context.Context, installationID int64, issueComment *github.IssueComment) error
	ReactWithConfused(ctx context.Context, installationID int64, issueComment *github.IssueComment) error
	PostComment(ctx context.Context, installationID int64, issueComment *github.IssueComment, body string) error
	UpdateIssueDescription(ctx context.Context, installationID int64, issueComment *github.IssueComment, body string) error
}

// JiraClientInterface defines the methods used by the executor to interact with
// Jira. This enables dependency injection of mock implementations during testing.
type JiraClientInterface interface {
	CreateIssue(project, issueType, summary, description string) (string, error)
}

// RepoConfigLoaderInterface defines the method for loading per-repository configuration.
type RepoConfigLoaderInterface interface {
	LoadRepoConfig(ctx context.Context, installationID int64, owner, repo string) (config.RepoConfig, error)
}

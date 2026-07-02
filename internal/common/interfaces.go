package common

import (
	"context"

	"github.com/ivanvc/jira-bot/internal/adapters/github"
	"github.com/ivanvc/jira-bot/internal/adapters/k8s"
	"github.com/ivanvc/jira-bot/internal/config"
)

// Sentinel errors for UserTokenStore operations.
// These reference the canonical errors defined in the k8s package.
var ErrNotFound = k8s.ErrUserTokenNotFound
var ErrMalformedEntry = k8s.ErrUserTokenMalformed

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
	CreateIssue(project, issueType, summary, description string, extraFields map[string]interface{}) (string, error)
}

// RepoConfigLoaderInterface defines the method for loading per-repository configuration.
type RepoConfigLoaderInterface interface {
	LoadRepoConfig(ctx context.Context, installationID int64, owner, repo string) (config.RepoConfig, error)
}

// UserTokenEntry is a type alias for k8s.UserTokenEntry to maintain a single
// canonical definition and avoid type mismatches at interface boundaries.
type UserTokenEntry = k8s.UserTokenEntry

// UserTokenStore defines the interface for per-user token persistence.
type UserTokenStore interface {
	// Read returns a single token entry. Returns ErrNotFound if no entry exists.
	// Returns ErrMalformedEntry if the stored JSON is invalid.
	Read(ctx context.Context, login string) (UserTokenEntry, error)
	// ReadAll returns all valid token entries, skipping malformed ones.
	// Returns a map of login → entry.
	ReadAll(ctx context.Context) (map[string]UserTokenEntry, error)
	// Write creates or updates a token entry for the given login.
	// Uses optimistic concurrency with up to 3 retries on conflict.
	Write(ctx context.Context, login string, entry UserTokenEntry) error
	// Delete removes a token entry for the given login.
	Delete(ctx context.Context, login string) error
}

// JiraClientResolveResult holds the outcome of resolving a per-user Jira client.
type JiraClientResolveResult struct {
	Client       JiraClientInterface
	AuthRequired bool
	AuthLink     string
	ErrorMsg     string
}

// JiraClientResolver resolves the appropriate per-user Jira client at request time.
type JiraClientResolver interface {
	// Resolve looks up or constructs a per-user Jira client.
	// Returns a result indicating either a ready client or an auth link to present.
	Resolve(ctx context.Context, login string) JiraClientResolveResult
}

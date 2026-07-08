package executor

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/ivanvc/jira-bot/internal/adapters/github"
	"github.com/ivanvc/jira-bot/internal/common"
	"github.com/ivanvc/jira-bot/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// MockCall records a method invocation name and arguments.
type MockCall struct {
	Method string
	Args   []interface{}
}

// MockGitHubClient implements common.GitHubClientInterface for testing.
type MockGitHubClient struct {
	Calls          []MockCall
	PostCommentErr error
	ReactErr       error
	UpdateErr      error
}

func (m *MockGitHubClient) ReactWithThumbsUp(ctx context.Context, installationID int64, issueComment *github.IssueComment) error {
	m.Calls = append(m.Calls, MockCall{Method: "ReactWithThumbsUp", Args: []interface{}{ctx, installationID, issueComment}})
	return m.ReactErr
}

func (m *MockGitHubClient) ReactWithConfused(ctx context.Context, installationID int64, issueComment *github.IssueComment) error {
	m.Calls = append(m.Calls, MockCall{Method: "ReactWithConfused", Args: []interface{}{ctx, installationID, issueComment}})
	return m.ReactErr
}

func (m *MockGitHubClient) PostComment(ctx context.Context, installationID int64, issueComment *github.IssueComment, body string) error {
	m.Calls = append(m.Calls, MockCall{Method: "PostComment", Args: []interface{}{ctx, installationID, issueComment, body}})
	return m.PostCommentErr
}

func (m *MockGitHubClient) UpdateIssueDescription(ctx context.Context, installationID int64, issueComment *github.IssueComment, body string) error {
	m.Calls = append(m.Calls, MockCall{Method: "UpdateIssueDescription", Args: []interface{}{ctx, installationID, issueComment, body}})
	return m.UpdateErr
}

func (m *MockGitHubClient) FetchComment(ctx context.Context, installationID int64, commentID uint64) (*github.IssueComment, error) {
	m.Calls = append(m.Calls, MockCall{Method: "FetchComment", Args: []interface{}{ctx, installationID, commentID}})
	return nil, nil
}

// MockJiraClient implements common.JiraClientInterface for testing.
type MockJiraClient struct {
	ReturnKey string
	ReturnErr error
	Calls     []MockCall
}

func (m *MockJiraClient) CreateIssue(project, issueType, summary, description string, extraFields map[string]interface{}) (string, error) {
	m.Calls = append(m.Calls, MockCall{Method: "CreateIssue", Args: []interface{}{project, issueType, summary, description, extraFields}})
	return m.ReturnKey, m.ReturnErr
}

// MockJiraClientResolver wraps a MockJiraClient to satisfy common.JiraClientResolver.
type MockJiraClientResolver struct {
	Client common.JiraClientInterface
}

func (r *MockJiraClientResolver) Resolve(ctx context.Context, login string) common.JiraClientResolveResult {
	if r.Client == nil {
		return common.JiraClientResolveResult{ErrorMsg: "no client configured"}
	}
	return common.JiraClientResolveResult{Client: r.Client}
}

// Helper to build a minimal IssueComment for testing.
func newIssueComment(commentBody, issueBody string) *github.IssueComment {
	return &github.IssueComment{
		Action: "created",
		Issue: github.Issue{
			Title:   "Test Issue",
			Body:    issueBody,
			HTMLURL: "https://github.com/org/repo/issues/1",
		},
		Comment: github.Comment{
			Body:   commentBody,
			NodeID: "node123",
			ID:     1,
			User:   github.CommentUser{Login: "testuser"},
		},
		Installation: github.Installation{ID: 42},
	}
}

// Helper to build a State with mocks and default config.
func newTestState(gh *MockGitHubClient, jira *MockJiraClient) *common.State {
	return &common.State{
		Config: common.Config{
			JiraDefaultProject:   "DEFAULT",
			JiraDefaultIssueType: "Task",
		},
		GitHubClient:       gh,
		JiraClientResolver: &MockJiraClientResolver{Client: jira},
	}
}

// --- 2.2 Executor command parsing tests ---

func TestRun_NonJiraCommentReturnsNil(t *testing.T) {
	gh := &MockGitHubClient{}
	jira := &MockJiraClient{}
	state := newTestState(gh, jira)
	ic := newIssueComment("some random comment", "")

	err := Run(context.Background(), state, ic)

	assert.NoError(t, err)
	assert.Empty(t, gh.Calls)
	assert.Empty(t, jira.Calls)
}

func TestRun_JiraOnlyRepliesWithHelpAndReacts(t *testing.T) {
	gh := &MockGitHubClient{}
	jira := &MockJiraClient{}
	state := newTestState(gh, jira)
	ic := newIssueComment("/jira", "")

	err := Run(context.Background(), state, ic)

	require.NoError(t, err)
	require.Len(t, gh.Calls, 2)
	assert.Equal(t, "PostComment", gh.Calls[0].Method)
	assert.Contains(t, gh.Calls[0].Args[3].(string), "list of commands")
	assert.Equal(t, "ReactWithThumbsUp", gh.Calls[1].Method)
	assert.Empty(t, jira.Calls)
}

func TestRun_JiraHelpRepliesWithHelpAndReacts(t *testing.T) {
	gh := &MockGitHubClient{}
	jira := &MockJiraClient{}
	state := newTestState(gh, jira)
	ic := newIssueComment("/jira help", "")

	err := Run(context.Background(), state, ic)

	require.NoError(t, err)
	require.Len(t, gh.Calls, 2)
	assert.Equal(t, "PostComment", gh.Calls[0].Method)
	assert.Contains(t, gh.Calls[0].Args[3].(string), "list of commands")
	assert.Equal(t, "ReactWithThumbsUp", gh.Calls[1].Method)
}

func TestRun_JiraCreateWithDefaults(t *testing.T) {
	gh := &MockGitHubClient{}
	jira := &MockJiraClient{ReturnKey: "DEFAULT-123"}
	state := newTestState(gh, jira)
	ic := newIssueComment("/jira create", "Issue body here")

	err := Run(context.Background(), state, ic)

	require.NoError(t, err)
	// Jira CreateIssue called with default project and type
	require.Len(t, jira.Calls, 1)
	assert.Equal(t, "CreateIssue", jira.Calls[0].Method)
	assert.Equal(t, "DEFAULT", jira.Calls[0].Args[0])
	assert.Equal(t, "Task", jira.Calls[0].Args[1])
	// GitHub: UpdateIssueDescription, PostComment (success), ReactWithThumbsUp
	require.Len(t, gh.Calls, 3)
	assert.Equal(t, "UpdateIssueDescription", gh.Calls[0].Method)
	assert.Contains(t, gh.Calls[0].Args[3].(string), "<!--JIRA_BOT_ISSUE:[DEFAULT-123]-->")
	assert.Equal(t, "PostComment", gh.Calls[1].Method)
	assert.Contains(t, gh.Calls[1].Args[3].(string), "DEFAULT-123")
	assert.Equal(t, "ReactWithThumbsUp", gh.Calls[2].Method)
}

func TestRun_JiraCreateWithSpecifiedOptions(t *testing.T) {
	gh := &MockGitHubClient{}
	jira := &MockJiraClient{ReturnKey: "ENG-456"}
	state := newTestState(gh, jira)
	ic := newIssueComment("/jira create project:ENG type:Bug", "Issue body")

	err := Run(context.Background(), state, ic)

	require.NoError(t, err)
	require.Len(t, jira.Calls, 1)
	assert.Equal(t, "ENG", jira.Calls[0].Args[0])
	assert.Equal(t, "Bug", jira.Calls[0].Args[1])
}

func TestRun_IssueWithJiraBotMarkerPostsWarningOnly(t *testing.T) {
	gh := &MockGitHubClient{}
	jira := &MockJiraClient{}
	state := newTestState(gh, jira)
	ic := newIssueComment("/jira create", "Body with <!--JIRA_BOT_ISSUE marker")

	err := Run(context.Background(), state, ic)

	require.NoError(t, err)
	// Should only post the warning comment, no error comment or confused reaction
	require.Len(t, gh.Calls, 1)
	assert.Equal(t, "PostComment", gh.Calls[0].Method)
	assert.Contains(t, gh.Calls[0].Args[3].(string), "Uh-oh")
	// Jira should not have been called
	assert.Empty(t, jira.Calls)
}

// --- 2.3 Executor error handling tests ---

func TestRun_JiraErrorTriggersConfusedReactionAndErrorComment(t *testing.T) {
	gh := &MockGitHubClient{}
	jiraErr := errors.New("jira connection failed")
	jira := &MockJiraClient{ReturnErr: jiraErr}
	state := newTestState(gh, jira)
	ic := newIssueComment("/jira create", "Issue body")

	err := Run(context.Background(), state, ic)

	require.Error(t, err)
	assert.Equal(t, jiraErr, err)
	// Should call ReactWithConfused and PostComment with error details
	var methods []string
	for _, c := range gh.Calls {
		methods = append(methods, c.Method)
	}
	assert.Contains(t, methods, "ReactWithConfused")
	assert.Contains(t, methods, "PostComment")
	// Verify the error comment contains the error message
	for _, c := range gh.Calls {
		if c.Method == "PostComment" {
			assert.Contains(t, c.Args[3].(string), "jira connection failed")
		}
	}
}

func TestRun_GitHubPostCommentErrorPropagates(t *testing.T) {
	postErr := errors.New("post comment failed")
	gh := &MockGitHubClient{PostCommentErr: postErr}
	jira := &MockJiraClient{}
	state := newTestState(gh, jira)
	// "/jira" triggers replyWithHelp which calls PostComment
	ic := newIssueComment("/jira", "")

	err := Run(context.Background(), state, ic)

	require.Error(t, err)
	assert.Equal(t, postErr, err)
}

func TestRun_GitHubReactWithThumbsUpErrorPropagates(t *testing.T) {
	reactErr := errors.New("react failed")
	gh := &MockGitHubClient{ReactErr: reactErr}
	jira := &MockJiraClient{}
	state := newTestState(gh, jira)
	// "/jira help" triggers help reply then ReactWithThumbsUp
	ic := newIssueComment("/jira help", "")

	err := Run(context.Background(), state, ic)

	require.Error(t, err)
	assert.Equal(t, reactErr, err)
}

// --- 2.4 loadOptionWithDefault unit tests ---

func TestLoadOptionWithDefault_MatchingKeyReturnsValue(t *testing.T) {
	result := loadOptionWithDefault("project", "FALLBACK", []string{"project:ENG"})
	assert.Equal(t, "ENG", result)
}

func TestLoadOptionWithDefault_MissingKeyReturnsFallback(t *testing.T) {
	result := loadOptionWithDefault("project", "FALLBACK", []string{"type:Bug"})
	assert.Equal(t, "FALLBACK", result)
}

func TestLoadOptionWithDefault_EmptyValueReturnsFallback(t *testing.T) {
	result := loadOptionWithDefault("project", "FALLBACK", []string{"project:"})
	assert.Equal(t, "FALLBACK", result)
}

func TestLoadOptionWithDefault_MultipleKeysReturnsFirstMatch(t *testing.T) {
	result := loadOptionWithDefault("project", "FALLBACK", []string{"project:FIRST", "project:SECOND"})
	assert.Equal(t, "FIRST", result)
}

// --- 5.2 replyWithHelp with repo config tests ---

// MockRepoConfigLoader implements common.RepoConfigLoaderInterface for testing.
type MockRepoConfigLoader struct {
	ReturnConfig config.RepoConfig
	ReturnErr    error
	Calls        []MockCall
}

func (m *MockRepoConfigLoader) LoadRepoConfig(ctx context.Context, installationID int64, owner, repo string) (config.RepoConfig, error) {
	m.Calls = append(m.Calls, MockCall{Method: "LoadRepoConfig", Args: []interface{}{ctx, installationID, owner, repo}})
	return m.ReturnConfig, m.ReturnErr
}

// Helper to build an IssueComment with repository info.
func newIssueCommentWithRepo(commentBody, issueBody, owner, repoName string) *github.IssueComment {
	return &github.IssueComment{
		Action: "created",
		Issue: github.Issue{
			Title:   "Test Issue",
			Body:    issueBody,
			HTMLURL: "https://github.com/" + owner + "/" + repoName + "/issues/1",
		},
		Comment: github.Comment{
			Body:   commentBody,
			NodeID: "node123",
			ID:     1,
			User:   github.CommentUser{Login: "testuser"},
		},
		Installation: github.Installation{ID: 42},
		Repository: github.Repository{
			Owner:    github.RepositoryOwner{Login: owner},
			Name:     repoName,
			FullName: owner + "/" + repoName,
		},
	}
}

func TestReplyWithHelp_ShowsRepoConfigProjectAsDefault(t *testing.T) {
	gh := &MockGitHubClient{}
	jira := &MockJiraClient{}
	loader := &MockRepoConfigLoader{
		ReturnConfig: config.RepoConfig{Project: "REPO-PROJ"},
	}
	state := newTestState(gh, jira)
	state.RepoConfigLoader = loader
	ic := newIssueCommentWithRepo("/jira help", "", "myorg", "myrepo")

	err := Run(context.Background(), state, ic)

	require.NoError(t, err)
	require.Len(t, loader.Calls, 1)
	assert.Equal(t, int64(42), loader.Calls[0].Args[1])
	assert.Equal(t, "myorg", loader.Calls[0].Args[2])
	assert.Equal(t, "myrepo", loader.Calls[0].Args[3])

	// Help text should show repo config project
	require.True(t, len(gh.Calls) >= 1)
	helpText := gh.Calls[0].Args[3].(string)
	assert.Contains(t, helpText, "REPO-PROJ")
}

func TestReplyWithHelp_ShowsRepoConfigTypeAsDefault(t *testing.T) {
	gh := &MockGitHubClient{}
	jira := &MockJiraClient{}
	loader := &MockRepoConfigLoader{
		ReturnConfig: config.RepoConfig{Type: "Bug"},
	}
	state := newTestState(gh, jira)
	state.RepoConfigLoader = loader
	ic := newIssueCommentWithRepo("/jira help", "", "myorg", "myrepo")

	err := Run(context.Background(), state, ic)

	require.NoError(t, err)

	helpText := gh.Calls[0].Args[3].(string)
	assert.Contains(t, helpText, "Bug")
}

func TestReplyWithHelp_FallsBackToGlobalConfigWhenNoRepoConfig(t *testing.T) {
	gh := &MockGitHubClient{}
	jira := &MockJiraClient{}
	loader := &MockRepoConfigLoader{
		ReturnConfig: config.RepoConfig{}, // empty - no repo config
	}
	state := newTestState(gh, jira)
	state.RepoConfigLoader = loader
	ic := newIssueCommentWithRepo("/jira help", "", "myorg", "myrepo")

	err := Run(context.Background(), state, ic)

	require.NoError(t, err)

	// Help text should show global defaults
	helpText := gh.Calls[0].Args[3].(string)
	assert.Contains(t, helpText, "Task")    // global JiraDefaultIssueType
	assert.Contains(t, helpText, "DEFAULT") // global JiraDefaultProject
}

func TestReplyWithHelp_FallsBackToGlobalConfigWhenLoaderIsNil(t *testing.T) {
	gh := &MockGitHubClient{}
	jira := &MockJiraClient{}
	state := newTestState(gh, jira)
	// state.RepoConfigLoader is nil by default
	ic := newIssueCommentWithRepo("/jira help", "", "myorg", "myrepo")

	err := Run(context.Background(), state, ic)

	require.NoError(t, err)

	helpText := gh.Calls[0].Args[3].(string)
	assert.Contains(t, helpText, "Task")    // global JiraDefaultIssueType
	assert.Contains(t, helpText, "DEFAULT") // global JiraDefaultProject
}

func TestReplyWithHelp_FallsBackToGlobalConfigOnLoaderError(t *testing.T) {
	gh := &MockGitHubClient{}
	jira := &MockJiraClient{}
	loader := &MockRepoConfigLoader{
		ReturnErr: errors.New("API error"),
	}
	state := newTestState(gh, jira)
	state.RepoConfigLoader = loader
	ic := newIssueCommentWithRepo("/jira help", "", "myorg", "myrepo")

	err := Run(context.Background(), state, ic)

	require.NoError(t, err)

	// Help text should show global defaults on error
	helpText := gh.Calls[0].Args[3].(string)
	assert.Contains(t, helpText, "Task")    // global JiraDefaultIssueType
	assert.Contains(t, helpText, "DEFAULT") // global JiraDefaultProject
}

// --- 5.4 Executor integration tests for createJiraIssue with repo config ---

func TestCreateJiraIssue_UsesRepoConfigWhenCommandOptionAbsent(t *testing.T) {
	gh := &MockGitHubClient{}
	jira := &MockJiraClient{ReturnKey: "REPO-101"}
	loader := &MockRepoConfigLoader{
		ReturnConfig: config.RepoConfig{Project: "REPO-PROJ", Type: "Story"},
	}
	state := newTestState(gh, jira)
	state.RepoConfigLoader = loader
	ic := newIssueCommentWithRepo("/jira create", "Issue body", "myorg", "myrepo")

	err := Run(context.Background(), state, ic)

	require.NoError(t, err)
	// Repo config loader should be called with correct args
	require.Len(t, loader.Calls, 1)
	assert.Equal(t, int64(42), loader.Calls[0].Args[1])
	assert.Equal(t, "myorg", loader.Calls[0].Args[2])
	assert.Equal(t, "myrepo", loader.Calls[0].Args[3])
	// Jira CreateIssue should use repo config values
	require.Len(t, jira.Calls, 1)
	assert.Equal(t, "REPO-PROJ", jira.Calls[0].Args[0]) // project from repo config
	assert.Equal(t, "Story", jira.Calls[0].Args[1])     // type from repo config
}

func TestCreateJiraIssue_CommandOptionOverridesRepoConfig(t *testing.T) {
	gh := &MockGitHubClient{}
	jira := &MockJiraClient{ReturnKey: "CMD-202"}
	loader := &MockRepoConfigLoader{
		ReturnConfig: config.RepoConfig{Project: "REPO-PROJ", Type: "Story"},
	}
	state := newTestState(gh, jira)
	state.RepoConfigLoader = loader
	ic := newIssueCommentWithRepo("/jira create project:CMD-PROJ type:Bug", "Issue body", "myorg", "myrepo")

	err := Run(context.Background(), state, ic)

	require.NoError(t, err)
	// Jira CreateIssue should use command option values, not repo config
	require.Len(t, jira.Calls, 1)
	assert.Equal(t, "CMD-PROJ", jira.Calls[0].Args[0]) // project from command
	assert.Equal(t, "Bug", jira.Calls[0].Args[1])      // type from command
}

func TestCreateJiraIssue_RepoConfigOverridesGlobalConfig(t *testing.T) {
	gh := &MockGitHubClient{}
	jira := &MockJiraClient{ReturnKey: "REPO-303"}
	loader := &MockRepoConfigLoader{
		ReturnConfig: config.RepoConfig{Project: "REPO-PROJ", Type: "Epic"},
	}
	state := &common.State{
		Config: common.Config{
			JiraDefaultProject:   "GLOBAL-PROJ",
			JiraDefaultIssueType: "Task",
		},
		GitHubClient:       gh,
		JiraClientResolver: &MockJiraClientResolver{Client: jira},
		RepoConfigLoader:   loader,
	}
	ic := newIssueCommentWithRepo("/jira create", "Issue body", "myorg", "myrepo")

	err := Run(context.Background(), state, ic)

	require.NoError(t, err)
	// Jira CreateIssue should use repo config, not global config
	require.Len(t, jira.Calls, 1)
	assert.Equal(t, "REPO-PROJ", jira.Calls[0].Args[0]) // repo config overrides GLOBAL-PROJ
	assert.Equal(t, "Epic", jira.Calls[0].Args[1])      // repo config overrides Task
}

func TestCreateJiraIssue_NilRepoConfigLoaderFallsBackToGlobalDefaults(t *testing.T) {
	gh := &MockGitHubClient{}
	jira := &MockJiraClient{ReturnKey: "DEFAULT-404"}
	state := &common.State{
		Config: common.Config{
			JiraDefaultProject:   "GLOBAL-PROJ",
			JiraDefaultIssueType: "Task",
		},
		GitHubClient:       gh,
		JiraClientResolver: &MockJiraClientResolver{Client: jira},
		// RepoConfigLoader is nil
	}
	ic := newIssueCommentWithRepo("/jira create", "Issue body", "myorg", "myrepo")

	err := Run(context.Background(), state, ic)

	require.NoError(t, err)
	// Jira CreateIssue should use global defaults
	require.Len(t, jira.Calls, 1)
	assert.Equal(t, "GLOBAL-PROJ", jira.Calls[0].Args[0]) // global project
	assert.Equal(t, "Task", jira.Calls[0].Args[1])        // global type
}

func TestCreateJiraIssue_PartialRepoConfig_ProjectOnly(t *testing.T) {
	gh := &MockGitHubClient{}
	jira := &MockJiraClient{ReturnKey: "PARTIAL-505"}
	loader := &MockRepoConfigLoader{
		ReturnConfig: config.RepoConfig{Project: "REPO-PROJ"}, // Type is empty
	}
	state := &common.State{
		Config: common.Config{
			JiraDefaultProject:   "GLOBAL-PROJ",
			JiraDefaultIssueType: "Task",
		},
		GitHubClient:       gh,
		JiraClientResolver: &MockJiraClientResolver{Client: jira},
		RepoConfigLoader:   loader,
	}
	ic := newIssueCommentWithRepo("/jira create", "Issue body", "myorg", "myrepo")

	err := Run(context.Background(), state, ic)

	require.NoError(t, err)
	require.Len(t, jira.Calls, 1)
	assert.Equal(t, "REPO-PROJ", jira.Calls[0].Args[0]) // project from repo config
	assert.Equal(t, "Task", jira.Calls[0].Args[1])      // type falls back to global
}

func TestCreateJiraIssue_PartialRepoConfig_TypeOnly(t *testing.T) {
	gh := &MockGitHubClient{}
	jira := &MockJiraClient{ReturnKey: "PARTIAL-606"}
	loader := &MockRepoConfigLoader{
		ReturnConfig: config.RepoConfig{Type: "Bug"}, // Project is empty
	}
	state := &common.State{
		Config: common.Config{
			JiraDefaultProject:   "GLOBAL-PROJ",
			JiraDefaultIssueType: "Task",
		},
		GitHubClient:       gh,
		JiraClientResolver: &MockJiraClientResolver{Client: jira},
		RepoConfigLoader:   loader,
	}
	ic := newIssueCommentWithRepo("/jira create", "Issue body", "myorg", "myrepo")

	err := Run(context.Background(), state, ic)

	require.NoError(t, err)
	require.Len(t, jira.Calls, 1)
	assert.Equal(t, "GLOBAL-PROJ", jira.Calls[0].Args[0]) // project falls back to global
	assert.Equal(t, "Bug", jira.Calls[0].Args[1])         // type from repo config
}

func TestCreateJiraIssue_CommandOptionOverridesPartialRepoConfig(t *testing.T) {
	gh := &MockGitHubClient{}
	jira := &MockJiraClient{ReturnKey: "MIX-707"}
	loader := &MockRepoConfigLoader{
		ReturnConfig: config.RepoConfig{Project: "REPO-PROJ", Type: "Story"},
	}
	state := &common.State{
		Config: common.Config{
			JiraDefaultProject:   "GLOBAL-PROJ",
			JiraDefaultIssueType: "Task",
		},
		GitHubClient:       gh,
		JiraClientResolver: &MockJiraClientResolver{Client: jira},
		RepoConfigLoader:   loader,
	}
	// Only override project via command, type should come from repo config
	ic := newIssueCommentWithRepo("/jira create project:CMD-PROJ", "Issue body", "myorg", "myrepo")

	err := Run(context.Background(), state, ic)

	require.NoError(t, err)
	require.Len(t, jira.Calls, 1)
	assert.Equal(t, "CMD-PROJ", jira.Calls[0].Args[0]) // project from command
	assert.Equal(t, "Story", jira.Calls[0].Args[1])    // type from repo config (not global)
}

func TestHelpText_ReflectsRepoLevelDefaults(t *testing.T) {
	gh := &MockGitHubClient{}
	jira := &MockJiraClient{}
	loader := &MockRepoConfigLoader{
		ReturnConfig: config.RepoConfig{Project: "TEAM-X", Type: "Enhancement"},
	}
	state := &common.State{
		Config: common.Config{
			JiraDefaultProject:   "GLOBAL-PROJ",
			JiraDefaultIssueType: "Task",
		},
		GitHubClient:       gh,
		JiraClientResolver: &MockJiraClientResolver{Client: jira},
		RepoConfigLoader:   loader,
	}
	ic := newIssueCommentWithRepo("/jira help", "", "myorg", "myrepo")

	err := Run(context.Background(), state, ic)

	require.NoError(t, err)
	require.True(t, len(gh.Calls) >= 1)
	helpText := gh.Calls[0].Args[3].(string)
	// Help should show repo config values, not global
	assert.Contains(t, helpText, "Enhancement")
	assert.Contains(t, helpText, "TEAM-X")
	assert.NotContains(t, helpText, "GLOBAL-PROJ")
	assert.NotContains(t, helpText, "Task")
}

func TestHelpText_UsesGlobalDefaultsWhenNoRepoConfig(t *testing.T) {
	gh := &MockGitHubClient{}
	jira := &MockJiraClient{}
	state := &common.State{
		Config: common.Config{
			JiraDefaultProject:   "GLOBAL-PROJ",
			JiraDefaultIssueType: "GlobalTask",
		},
		GitHubClient:       gh,
		JiraClientResolver: &MockJiraClientResolver{Client: jira},
		// RepoConfigLoader is nil
	}
	ic := newIssueCommentWithRepo("/jira help", "", "myorg", "myrepo")

	err := Run(context.Background(), state, ic)

	require.NoError(t, err)
	require.True(t, len(gh.Calls) >= 1)
	helpText := gh.Calls[0].Args[3].(string)
	// Help should show global values
	assert.Contains(t, helpText, "GlobalTask")
	assert.Contains(t, helpText, "GLOBAL-PROJ")
}

// --- 9.1 Help command displays configured field defaults ---

func TestHelpText_ShowsFieldsSectionWhenFieldsConfigured(t *testing.T) {
	gh := &MockGitHubClient{}
	jira := &MockJiraClient{}
	loader := &MockRepoConfigLoader{
		ReturnConfig: config.RepoConfig{
			Project: "ENG",
			Type:    "Story",
			Fields: map[string]interface{}{
				"components": []interface{}{map[string]interface{}{"name": "Backend"}},
				"priority":   map[string]interface{}{"name": "Medium"},
				"labels":     []interface{}{"team-platform"},
			},
		},
	}
	state := &common.State{
		Config: common.Config{
			JiraDefaultProject:   "GLOBAL-PROJ",
			JiraDefaultIssueType: "Task",
		},
		GitHubClient:     gh,
		JiraClientResolver: &MockJiraClientResolver{Client: jira},
		RepoConfigLoader: loader,
	}
	ic := newIssueCommentWithRepo("/jira help", "", "myorg", "myrepo")

	err := Run(context.Background(), state, ic)

	require.NoError(t, err)
	require.True(t, len(gh.Calls) >= 1)
	helpText := gh.Calls[0].Args[3].(string)
	// Should contain the Default fields section with field names sorted
	assert.Contains(t, helpText, "**Default fields:** components, labels, priority")
}

func TestHelpText_OmitsFieldsSectionWhenNoFieldsConfigured(t *testing.T) {
	gh := &MockGitHubClient{}
	jira := &MockJiraClient{}
	loader := &MockRepoConfigLoader{
		ReturnConfig: config.RepoConfig{
			Project: "ENG",
			Type:    "Story",
			Fields:  nil,
		},
	}
	state := &common.State{
		Config: common.Config{
			JiraDefaultProject:   "GLOBAL-PROJ",
			JiraDefaultIssueType: "Task",
		},
		GitHubClient:     gh,
		JiraClientResolver: &MockJiraClientResolver{Client: jira},
		RepoConfigLoader: loader,
	}
	ic := newIssueCommentWithRepo("/jira help", "", "myorg", "myrepo")

	err := Run(context.Background(), state, ic)

	require.NoError(t, err)
	require.True(t, len(gh.Calls) >= 1)
	helpText := gh.Calls[0].Args[3].(string)
	assert.NotContains(t, helpText, "Default fields")
}

func TestHelpText_OmitsFieldsSectionWhenFieldsMapEmpty(t *testing.T) {
	gh := &MockGitHubClient{}
	jira := &MockJiraClient{}
	loader := &MockRepoConfigLoader{
		ReturnConfig: config.RepoConfig{
			Project: "ENG",
			Type:    "Story",
			Fields:  map[string]interface{}{},
		},
	}
	state := &common.State{
		Config: common.Config{
			JiraDefaultProject:   "GLOBAL-PROJ",
			JiraDefaultIssueType: "Task",
		},
		GitHubClient:     gh,
		JiraClientResolver: &MockJiraClientResolver{Client: jira},
		RepoConfigLoader: loader,
	}
	ic := newIssueCommentWithRepo("/jira help", "", "myorg", "myrepo")

	err := Run(context.Background(), state, ic)

	require.NoError(t, err)
	require.True(t, len(gh.Calls) >= 1)
	helpText := gh.Calls[0].Args[3].(string)
	assert.NotContains(t, helpText, "Default fields")
}

func TestHelpText_OmitsFieldsSectionWhenAllFieldValuesNull(t *testing.T) {
	// Simulates a config loaded from YAML where all field values were null.
	// ParseRepoConfig strips null values, resulting in an empty Fields map.
	// The help command should omit the fields section in this case.
	gh := &MockGitHubClient{}
	jira := &MockJiraClient{}

	// Parse a YAML config where all fields have null values
	yamlData := []byte("project: ENG\ntype: Story\nfields:\n  priority: null\n  components: null\n  labels: null\n")
	repoCfg, err := config.ParseRepoConfig(yamlData)
	require.NoError(t, err)
	// After parsing, Fields map should be empty (nulls stripped)
	require.Empty(t, repoCfg.Fields)

	loader := &MockRepoConfigLoader{ReturnConfig: repoCfg}
	state := &common.State{
		Config: common.Config{
			JiraDefaultProject:   "GLOBAL-PROJ",
			JiraDefaultIssueType: "Task",
		},
		GitHubClient:     gh,
		JiraClientResolver: &MockJiraClientResolver{Client: jira},
		RepoConfigLoader: loader,
	}
	ic := newIssueCommentWithRepo("/jira help", "", "myorg", "myrepo")

	err = Run(context.Background(), state, ic)

	require.NoError(t, err)
	require.True(t, len(gh.Calls) >= 1)
	helpText := gh.Calls[0].Args[3].(string)
	assert.NotContains(t, helpText, "Default fields")
}

func TestHelpText_ShowsOnlyFieldKeyNamesNotValues(t *testing.T) {
	gh := &MockGitHubClient{}
	jira := &MockJiraClient{}
	loader := &MockRepoConfigLoader{
		ReturnConfig: config.RepoConfig{
			Project: "ENG",
			Fields: map[string]interface{}{
				"priority":          map[string]interface{}{"name": "High"},
				"customfield_10001": "secret-value",
			},
		},
	}
	state := &common.State{
		Config: common.Config{
			JiraDefaultProject:   "GLOBAL-PROJ",
			JiraDefaultIssueType: "Task",
		},
		GitHubClient:     gh,
		JiraClientResolver: &MockJiraClientResolver{Client: jira},
		RepoConfigLoader: loader,
	}
	ic := newIssueCommentWithRepo("/jira help", "", "myorg", "myrepo")

	err := Run(context.Background(), state, ic)

	require.NoError(t, err)
	require.True(t, len(gh.Calls) >= 1)
	helpText := gh.Calls[0].Args[3].(string)
	// Should show field names
	assert.Contains(t, helpText, "customfield_10001")
	assert.Contains(t, helpText, "priority")
	// Should NOT show field values
	assert.NotContains(t, helpText, "High")
	assert.NotContains(t, helpText, "secret-value")
}


// --- loadFieldsFromCommand tests ---

func TestLoadFieldsFromCommand_EmptyOptions(t *testing.T) {
	result := loadFieldsFromCommand([]string{})
	assert.NotNil(t, result)
	assert.Empty(t, result)
}

func TestLoadFieldsFromCommand_SkipsReservedKeys(t *testing.T) {
	result := loadFieldsFromCommand([]string{"project:ENG", "type:Bug", "priority:High"})
	assert.NotContains(t, result, "project")
	assert.NotContains(t, result, "type")
	assert.Contains(t, result, "priority")
}

func TestLoadFieldsFromCommand_SplitsOnFirstColonOnly(t *testing.T) {
	result := loadFieldsFromCommand([]string{"customfield_10001:value:with:colons"})
	assert.Equal(t, "value:with:colons", result["customfield_10001"])
}

func TestLoadFieldsFromCommand_IgnoresEmptyValues(t *testing.T) {
	result := loadFieldsFromCommand([]string{"priority:", "labels:bug-fix"})
	assert.NotContains(t, result, "priority")
	assert.Contains(t, result, "labels")
}

func TestLoadFieldsFromCommand_LastOccurrenceWins(t *testing.T) {
	result := loadFieldsFromCommand([]string{"priority:Low", "priority:High"})
	// priority is a well-known field, coerced to {"name": "High"}
	expected := map[string]interface{}{"name": "High"}
	assert.Equal(t, expected, result["priority"])
}

func TestLoadFieldsFromCommand_CoercesWellKnownFields(t *testing.T) {
	result := loadFieldsFromCommand([]string{"components:Backend", "priority:High", "labels:bug-fix"})

	expectedComponents := []interface{}{map[string]interface{}{"name": "Backend"}}
	expectedPriority := map[string]interface{}{"name": "High"}
	expectedLabels := []interface{}{"bug-fix"}

	assert.Equal(t, expectedComponents, result["components"])
	assert.Equal(t, expectedPriority, result["priority"])
	assert.Equal(t, expectedLabels, result["labels"])
}

func TestLoadFieldsFromCommand_UnknownFieldsAsRawStrings(t *testing.T) {
	result := loadFieldsFromCommand([]string{"customfield_10001:urgent", "customfield_10002:value"})
	assert.Equal(t, "urgent", result["customfield_10001"])
	assert.Equal(t, "value", result["customfield_10002"])
}

func TestLoadFieldsFromCommand_IgnoresOptionsWithoutColon(t *testing.T) {
	result := loadFieldsFromCommand([]string{"nocolon", "priority:High"})
	assert.Len(t, result, 1)
	assert.Contains(t, result, "priority")
}

func TestLoadFieldsFromCommand_CapsAt20Fields(t *testing.T) {
	options := make([]string, 25)
	for i := 0; i < 25; i++ {
		options[i] = fmt.Sprintf("field%d:value%d", i, i)
	}
	result := loadFieldsFromCommand(options)
	assert.Len(t, result, 20)
}

func TestLoadFieldsFromCommand_CapDoesNotCountReservedKeys(t *testing.T) {
	options := []string{"project:ENG", "type:Bug"}
	for i := 0; i < 20; i++ {
		options = append(options, fmt.Sprintf("field%d:value%d", i, i))
	}
	result := loadFieldsFromCommand(options)
	// Should have exactly 20 fields (reserved keys not counted toward cap)
	assert.Len(t, result, 20)
	assert.NotContains(t, result, "project")
	assert.NotContains(t, result, "type")
}

// --- 7.2 Executor integration tests: field overrides → coercion → merge → CreateIssue ---

func TestCreateJiraIssue_FieldOverridesCoercedAndPassedToCreateIssue(t *testing.T) {
	// Full flow: command with field overrides → coercion → merge → CreateIssue called with correct extraFields
	gh := &MockGitHubClient{}
	jira := &MockJiraClient{ReturnKey: "ENG-100"}
	loader := &MockRepoConfigLoader{
		ReturnConfig: config.RepoConfig{Project: "ENG", Type: "Task"},
	}
	state := newTestState(gh, jira)
	state.RepoConfigLoader = loader

	// Command includes well-known fields (priority, components, labels) and a custom field
	ic := newIssueCommentWithRepo(
		"/jira create priority:High components:Backend labels:bug-fix customfield_10001:urgent",
		"Issue body", "myorg", "myrepo",
	)

	err := Run(context.Background(), state, ic)

	require.NoError(t, err)
	require.Len(t, jira.Calls, 1)

	// Verify extraFields (5th argument) contains coerced well-known fields and raw custom field
	extraFields := jira.Calls[0].Args[4].(map[string]interface{})
	assert.Equal(t, map[string]interface{}{"name": "High"}, extraFields["priority"])
	assert.Equal(t, []interface{}{map[string]interface{}{"name": "Backend"}}, extraFields["components"])
	assert.Equal(t, []interface{}{"bug-fix"}, extraFields["labels"])
	assert.Equal(t, "urgent", extraFields["customfield_10001"])
}

func TestCreateJiraIssue_RepoConfigFieldsPassedWhenNoOverrides(t *testing.T) {
	// Repo config fields are passed to CreateIssue when no command overrides are present
	gh := &MockGitHubClient{}
	jira := &MockJiraClient{ReturnKey: "ENG-200"}
	loader := &MockRepoConfigLoader{
		ReturnConfig: config.RepoConfig{
			Project: "ENG",
			Type:    "Story",
			Fields: map[string]interface{}{
				"priority":   map[string]interface{}{"name": "Medium"},
				"components": []interface{}{map[string]interface{}{"name": "API"}},
				"labels":     []interface{}{"team-platform", "sprint-42"},
			},
		},
	}
	state := newTestState(gh, jira)
	state.RepoConfigLoader = loader

	// No field overrides in the command
	ic := newIssueCommentWithRepo("/jira create", "Issue body", "myorg", "myrepo")

	err := Run(context.Background(), state, ic)

	require.NoError(t, err)
	require.Len(t, jira.Calls, 1)

	// Verify extraFields contains all repo config fields as-is
	extraFields := jira.Calls[0].Args[4].(map[string]interface{})
	assert.Equal(t, map[string]interface{}{"name": "Medium"}, extraFields["priority"])
	assert.Equal(t, []interface{}{map[string]interface{}{"name": "API"}}, extraFields["components"])
	assert.Equal(t, []interface{}{"team-platform", "sprint-42"}, extraFields["labels"])
}

func TestCreateJiraIssue_CommandOverridesReplaceRepoConfigFields(t *testing.T) {
	// Command field overrides replace repo config values entirely (no deep merge)
	gh := &MockGitHubClient{}
	jira := &MockJiraClient{ReturnKey: "ENG-300"}
	loader := &MockRepoConfigLoader{
		ReturnConfig: config.RepoConfig{
			Project: "ENG",
			Type:    "Story",
			Fields: map[string]interface{}{
				"priority":          map[string]interface{}{"name": "Medium"},
				"components":        []interface{}{map[string]interface{}{"name": "API"}, map[string]interface{}{"name": "Backend"}},
				"labels":            []interface{}{"team-platform", "sprint-42"},
				"customfield_10001": "default-value",
			},
		},
	}
	state := newTestState(gh, jira)
	state.RepoConfigLoader = loader

	// Command overrides priority and customfield_10001; components and labels stay from repo config
	ic := newIssueCommentWithRepo(
		"/jira create priority:Critical customfield_10001:override-value",
		"Issue body", "myorg", "myrepo",
	)

	err := Run(context.Background(), state, ic)

	require.NoError(t, err)
	require.Len(t, jira.Calls, 1)

	extraFields := jira.Calls[0].Args[4].(map[string]interface{})
	// Overridden by command (coerced for well-known field)
	assert.Equal(t, map[string]interface{}{"name": "Critical"}, extraFields["priority"])
	// Overridden by command (raw string for custom field)
	assert.Equal(t, "override-value", extraFields["customfield_10001"])
	// Kept from repo config (not overridden)
	assert.Equal(t, []interface{}{map[string]interface{}{"name": "API"}, map[string]interface{}{"name": "Backend"}}, extraFields["components"])
	assert.Equal(t, []interface{}{"team-platform", "sprint-42"}, extraFields["labels"])
}

func TestCreateJiraIssue_EmptyExtraFieldsWhenNoConfigAndNoOverrides(t *testing.T) {
	// When neither repo config nor command has fields, extraFields should be empty map
	gh := &MockGitHubClient{}
	jira := &MockJiraClient{ReturnKey: "ENG-400"}
	loader := &MockRepoConfigLoader{
		ReturnConfig: config.RepoConfig{Project: "ENG", Type: "Task"},
	}
	state := newTestState(gh, jira)
	state.RepoConfigLoader = loader

	ic := newIssueCommentWithRepo("/jira create", "Issue body", "myorg", "myrepo")

	err := Run(context.Background(), state, ic)

	require.NoError(t, err)
	require.Len(t, jira.Calls, 1)

	extraFields := jira.Calls[0].Args[4].(map[string]interface{})
	assert.Empty(t, extraFields)
}

func TestCreateJiraIssue_CommandOnlyFieldsWithNoRepoConfig(t *testing.T) {
	// Command adds new fields not present in repo config
	gh := &MockGitHubClient{}
	jira := &MockJiraClient{ReturnKey: "ENG-500"}
	loader := &MockRepoConfigLoader{
		ReturnConfig: config.RepoConfig{Project: "ENG", Type: "Task"},
	}
	state := newTestState(gh, jira)
	state.RepoConfigLoader = loader

	ic := newIssueCommentWithRepo(
		"/jira create priority:High labels:hotfix",
		"Issue body", "myorg", "myrepo",
	)

	err := Run(context.Background(), state, ic)

	require.NoError(t, err)
	require.Len(t, jira.Calls, 1)

	extraFields := jira.Calls[0].Args[4].(map[string]interface{})
	assert.Equal(t, map[string]interface{}{"name": "High"}, extraFields["priority"])
	assert.Equal(t, []interface{}{"hotfix"}, extraFields["labels"])
	assert.Len(t, extraFields, 2)
}

// --- 4.1 Description override integration tests ---

// --- 10.4 Per-user resolver integration tests ---

// FlexibleMockResolver is a more flexible mock for JiraClientResolver that records
// the login passed to Resolve and returns a configurable result.
type FlexibleMockResolver struct {
	Result     common.JiraClientResolveResult
	CalledWith string
}

func (r *FlexibleMockResolver) Resolve(ctx context.Context, login string) common.JiraClientResolveResult {
	r.CalledWith = login
	return r.Result
}

// MockUserTokenStore implements common.UserTokenStore for testing.
type MockUserTokenStore struct {
	Entries map[string]common.UserTokenEntry
	Calls   []MockCall
}

func (m *MockUserTokenStore) Read(ctx context.Context, login string) (common.UserTokenEntry, error) {
	m.Calls = append(m.Calls, MockCall{Method: "Read", Args: []interface{}{login}})
	if entry, ok := m.Entries[login]; ok {
		return entry, nil
	}
	return common.UserTokenEntry{}, common.ErrNotFound
}

func (m *MockUserTokenStore) ReadAll(ctx context.Context) (map[string]common.UserTokenEntry, error) {
	m.Calls = append(m.Calls, MockCall{Method: "ReadAll"})
	return m.Entries, nil
}

func (m *MockUserTokenStore) Write(ctx context.Context, login string, entry common.UserTokenEntry) error {
	m.Calls = append(m.Calls, MockCall{Method: "Write", Args: []interface{}{login, entry}})
	if m.Entries == nil {
		m.Entries = make(map[string]common.UserTokenEntry)
	}
	m.Entries[login] = entry
	return nil
}

func (m *MockUserTokenStore) Delete(ctx context.Context, login string) error {
	m.Calls = append(m.Calls, MockCall{Method: "Delete", Args: []interface{}{login}})
	delete(m.Entries, login)
	return nil
}

func TestCreateJiraIssue_ExtractsLoginFromComment(t *testing.T) {
	// Verifies that the executor extracts Comment.User.Login from the webhook payload
	// and passes it to the resolver.
	gh := &MockGitHubClient{}
	jira := &MockJiraClient{ReturnKey: "PROJ-1"}
	resolver := &FlexibleMockResolver{
		Result: common.JiraClientResolveResult{Client: jira},
	}
	state := &common.State{
		Config: common.Config{
			JiraDefaultProject:   "DEFAULT",
			JiraDefaultIssueType: "Task",
		},
		GitHubClient:       gh,
		JiraClientResolver: resolver,
	}

	ic := &github.IssueComment{
		Action: "created",
		Issue: github.Issue{
			Title:   "Test Issue",
			Body:    "Issue body",
			HTMLURL: "https://github.com/org/repo/issues/1",
		},
		Comment: github.Comment{
			Body: "/jira create",
			User: github.CommentUser{Login: "octocat"},
		},
		Installation: github.Installation{ID: 42},
	}

	err := Run(context.Background(), state, ic)

	require.NoError(t, err)
	// Verify the resolver was called with the login from the comment
	assert.Equal(t, "octocat", resolver.CalledWith)
}

func TestCreateJiraIssue_EmptyLogin_PostsError(t *testing.T) {
	// Verifies that when Comment.User.Login is empty/whitespace, the executor posts
	// an error comment and does NOT call the resolver.
	gh := &MockGitHubClient{}
	jira := &MockJiraClient{ReturnKey: "PROJ-1"}
	resolver := &FlexibleMockResolver{
		Result: common.JiraClientResolveResult{Client: jira},
	}
	state := &common.State{
		Config: common.Config{
			JiraDefaultProject:   "DEFAULT",
			JiraDefaultIssueType: "Task",
		},
		GitHubClient:       gh,
		JiraClientResolver: resolver,
	}

	ic := &github.IssueComment{
		Action: "created",
		Issue: github.Issue{
			Title:   "Test Issue",
			Body:    "Issue body",
			HTMLURL: "https://github.com/org/repo/issues/1",
		},
		Comment: github.Comment{
			Body: "/jira create",
			User: github.CommentUser{Login: "   "}, // whitespace-only login
		},
		Installation: github.Installation{ID: 42},
	}

	err := Run(context.Background(), state, ic)

	// The executor should NOT return an error (it handled it by posting a comment)
	require.NoError(t, err)
	// Resolver should NOT have been called
	assert.Empty(t, resolver.CalledWith)
	// An error comment should have been posted
	require.True(t, len(gh.Calls) >= 1)
	var postCommentFound bool
	for _, call := range gh.Calls {
		if call.Method == "PostComment" {
			body := call.Args[3].(string)
			assert.Contains(t, body, "Could not identify")
			postCommentFound = true
			break
		}
	}
	assert.True(t, postCommentFound, "Expected a PostComment call with error message")
	// Jira should NOT have been called
	assert.Empty(t, jira.Calls)
}

func TestCreateJiraIssue_AuthRequired_PostsAuthLink(t *testing.T) {
	// Verifies that when the resolver returns AuthRequired=true, the executor posts
	// a comment with the auth link.
	gh := &MockGitHubClient{}
	resolver := &FlexibleMockResolver{
		Result: common.JiraClientResolveResult{
			AuthRequired: true,
			AuthLink:     "https://bot.example.com/oauth/authorize",
		},
	}
	state := &common.State{
		Config: common.Config{
			JiraDefaultProject:   "DEFAULT",
			JiraDefaultIssueType: "Task",
		},
		GitHubClient:       gh,
		JiraClientResolver: resolver,
	}

	ic := &github.IssueComment{
		Action: "created",
		Issue: github.Issue{
			Title:   "Test Issue",
			Body:    "Issue body",
			HTMLURL: "https://github.com/org/repo/issues/1",
		},
		Comment: github.Comment{
			Body: "/jira create",
			User: github.CommentUser{Login: "octocat"},
		},
		Installation: github.Installation{ID: 42},
	}

	err := Run(context.Background(), state, ic)

	require.NoError(t, err)
	// Verify the auth link was posted in a comment
	require.True(t, len(gh.Calls) >= 1)
	var authCommentFound bool
	for _, call := range gh.Calls {
		if call.Method == "PostComment" {
			body := call.Args[3].(string)
			if strings.Contains(body, "authorize") && strings.Contains(body, "https://bot.example.com/oauth/authorize") {
				authCommentFound = true
				break
			}
		}
	}
	assert.True(t, authCommentFound, "Expected a PostComment call with auth link")
}

func TestCreateJiraIssue_ResolverSuccess_CreatesIssue(t *testing.T) {
	// Verifies that when the resolver returns a valid client, the executor uses it
	// to create the Jira issue.
	gh := &MockGitHubClient{}
	jira := &MockJiraClient{ReturnKey: "TEAM-42"}
	resolver := &FlexibleMockResolver{
		Result: common.JiraClientResolveResult{Client: jira},
	}
	state := &common.State{
		Config: common.Config{
			JiraDefaultProject:   "TEAM",
			JiraDefaultIssueType: "Story",
		},
		GitHubClient:       gh,
		JiraClientResolver: resolver,
	}

	ic := &github.IssueComment{
		Action: "created",
		Issue: github.Issue{
			Title:   "My Feature",
			Body:    "Feature description",
			HTMLURL: "https://github.com/org/repo/issues/5",
		},
		Comment: github.Comment{
			Body: "/jira create",
			User: github.CommentUser{Login: "developer"},
		},
		Installation: github.Installation{ID: 42},
	}

	err := Run(context.Background(), state, ic)

	require.NoError(t, err)
	// Verify the resolver was called with the correct login
	assert.Equal(t, "developer", resolver.CalledWith)
	// Verify Jira issue was created
	require.Len(t, jira.Calls, 1)
	assert.Equal(t, "CreateIssue", jira.Calls[0].Method)
	assert.Equal(t, "TEAM", jira.Calls[0].Args[0])
	assert.Equal(t, "Story", jira.Calls[0].Args[1])
	assert.Equal(t, "My Feature", jira.Calls[0].Args[2])
	// Verify success comment was posted
	var successCommentFound bool
	for _, call := range gh.Calls {
		if call.Method == "PostComment" {
			body := call.Args[3].(string)
			if strings.Contains(body, "TEAM-42") {
				successCommentFound = true
				break
			}
		}
	}
	assert.True(t, successCommentFound, "Expected a success comment mentioning the issue key")
}

func TestCreateJiraIssue_401Error_MarksInvalidAndPostsReauthLink(t *testing.T) {
	// Verifies that when the Jira client returns an error containing "401",
	// the executor marks the token as invalid and posts a re-auth link.
	gh := &MockGitHubClient{}
	jira := &MockJiraClient{ReturnErr: errors.New("Jira API returned 401 Unauthorized")}

	// First call to resolver returns the client (so creation is attempted).
	// Second call (after invalidation) returns auth required.
	callCount := 0
	resolver := &callCountResolver{
		firstResult: common.JiraClientResolveResult{Client: jira},
		secondResult: common.JiraClientResolveResult{
			AuthRequired: true,
			AuthLink:     "https://bot.example.com/oauth/authorize",
		},
		callCount: &callCount,
	}

	store := &MockUserTokenStore{
		Entries: map[string]common.UserTokenEntry{
			"octocat": {
				RefreshToken: "refresh-token",
				AccessToken:  "access-token",
				CloudID:      "cloud-123",
				Status:       "",
			},
		},
	}

	state := &common.State{
		Config: common.Config{
			JiraDefaultProject:   "DEFAULT",
			JiraDefaultIssueType: "Task",
		},
		GitHubClient:       gh,
		JiraClientResolver: resolver,
		UserTokenStore:     store,
	}

	ic := &github.IssueComment{
		Action: "created",
		Issue: github.Issue{
			Title:   "Test Issue",
			Body:    "Issue body",
			HTMLURL: "https://github.com/org/repo/issues/1",
		},
		Comment: github.Comment{
			Body: "/jira create",
			User: github.CommentUser{Login: "octocat"},
		},
		Installation: github.Installation{ID: 42},
	}

	err := Run(context.Background(), state, ic)

	// The 401 error still propagates as the return value
	require.Error(t, err)
	assert.Contains(t, err.Error(), "401")

	// Verify the token was marked invalid in the store
	entry, readErr := store.Read(context.Background(), "octocat")
	require.NoError(t, readErr)
	assert.Equal(t, "invalid", entry.Status)

	// Verify a re-auth link comment was posted
	var reauthCommentFound bool
	for _, call := range gh.Calls {
		if call.Method == "PostComment" {
			body := call.Args[3].(string)
			if strings.Contains(body, "re-authorize") || strings.Contains(body, "expired") {
				reauthCommentFound = true
				break
			}
		}
	}
	assert.True(t, reauthCommentFound, "Expected a PostComment call with re-auth link")
}

// callCountResolver is a mock resolver that returns different results on successive calls.
// Used to simulate: first call returns client (for issue creation), second call (after
// invalidation) returns auth required.
type callCountResolver struct {
	firstResult  common.JiraClientResolveResult
	secondResult common.JiraClientResolveResult
	callCount    *int
	CalledWith   string
}

func (r *callCountResolver) Resolve(ctx context.Context, login string) common.JiraClientResolveResult {
	r.CalledWith = login
	*r.callCount++
	if *r.callCount == 1 {
		return r.firstResult
	}
	return r.secondResult
}

func TestCreateJiraIssue_CommentBodyDescriptionOverrideFlowsToCreateIssue(t *testing.T) {
	// When comment body has text after a newline following the command line,
	// that text (via BuildDescription) should be passed to CreateIssue as the description.
	gh := &MockGitHubClient{}
	jira := &MockJiraClient{ReturnKey: "ENG-700"}
	state := newTestState(gh, jira)

	commentBody := "/jira create project:ENG\nThis is my custom description"
	ic := newIssueComment(commentBody, "This is the issue body that should NOT be used")

	err := Run(context.Background(), state, ic)

	require.NoError(t, err)
	require.Len(t, jira.Calls, 1)
	assert.Equal(t, "CreateIssue", jira.Calls[0].Method)

	// The description passed to CreateIssue should be built from the custom description source
	description := jira.Calls[0].Args[3].(string)
	expectedDescription := BuildDescription("This is my custom description", "https://github.com/org/repo/issues/1")
	assert.Equal(t, expectedDescription, description)

	// Verify it contains the custom description text and the GitHub link
	assert.Contains(t, description, "This is my custom description")
	assert.Contains(t, description, "GitHub link: https://github.com/org/repo/issues/1")
	// Verify it does NOT contain the issue body
	assert.NotContains(t, description, "This is the issue body that should NOT be used")
}

func TestCreateJiraIssue_NoBodyTextUsesIssueBodyAsDescriptionSource(t *testing.T) {
	// When comment body is just the command line (no newline/body text),
	// the issue body should be used as the description source.
	gh := &MockGitHubClient{}
	jira := &MockJiraClient{ReturnKey: "ENG-800"}
	state := newTestState(gh, jira)

	commentBody := "/jira create project:ENG"
	issueBody := "This is the GitHub issue body"
	ic := newIssueComment(commentBody, issueBody)

	err := Run(context.Background(), state, ic)

	require.NoError(t, err)
	require.Len(t, jira.Calls, 1)
	assert.Equal(t, "CreateIssue", jira.Calls[0].Method)

	// The description passed to CreateIssue should be built from the issue body
	description := jira.Calls[0].Args[3].(string)
	expectedDescription := BuildDescription("This is the GitHub issue body", "https://github.com/org/repo/issues/1")
	assert.Equal(t, expectedDescription, description)

	// Verify it contains the issue body and GitHub link
	assert.Contains(t, description, "This is the GitHub issue body")
	assert.Contains(t, description, "GitHub link: https://github.com/org/repo/issues/1")
}

func TestHelpText_IncludesCustomDescriptionDocumentation(t *testing.T) {
	// The help text should document the custom description feature,
	// including: what it does, the default behavior, and a usage example.
	gh := &MockGitHubClient{}
	jira := &MockJiraClient{}
	state := newTestState(gh, jira)
	ic := newIssueComment("/jira help", "")

	err := Run(context.Background(), state, ic)

	require.NoError(t, err)
	require.Len(t, gh.Calls, 2) // PostComment + ReactWithThumbsUp
	assert.Equal(t, "PostComment", gh.Calls[0].Method)

	helpText := gh.Calls[0].Args[3].(string)

	// Requirement 3.1: Help text explains that text after a newline is used as description
	assert.Contains(t, helpText, "Custom description")

	// Requirement 3.2: Help text indicates the default behavior (issue/PR body used)
	assert.Contains(t, helpText, "default")

	// Requirement 3.3: Help text includes a usage example with /jira create and custom description
	assert.Contains(t, helpText, "/jira create")
	assert.Contains(t, helpText, "custom description")
}

func TestCreateJiraIssue_OptionsParseCorrectlyWithBodyTextOnSubsequentLines(t *testing.T) {
	// When comment body has options on the first line AND body text on subsequent lines,
	// both options should parse correctly AND the custom description should be used.
	gh := &MockGitHubClient{}
	jira := &MockJiraClient{ReturnKey: "ENG-900"}
	state := newTestState(gh, jira)

	commentBody := "/jira create project:ENG type:Bug\nCustom description here"
	ic := newIssueComment(commentBody, "Issue body should not be used")

	err := Run(context.Background(), state, ic)

	require.NoError(t, err)
	require.Len(t, jira.Calls, 1)
	assert.Equal(t, "CreateIssue", jira.Calls[0].Method)

	// Verify project and type parsed correctly from the first line
	assert.Equal(t, "ENG", jira.Calls[0].Args[0])  // project
	assert.Equal(t, "Bug", jira.Calls[0].Args[1])  // type

	// Verify the custom description is used (not the issue body)
	description := jira.Calls[0].Args[3].(string)
	expectedDescription := BuildDescription("Custom description here", "https://github.com/org/repo/issues/1")
	assert.Equal(t, expectedDescription, description)

	assert.Contains(t, description, "Custom description here")
	assert.NotContains(t, description, "Issue body should not be used")
}

// --- 5.5 createJiraIssue with assign option tests ---

// --- Title override tests ---

func TestCreateJiraIssue_TitleOverride_CustomTitleWithOptions(t *testing.T) {
	// /jira create My Custom Title project:ENG → summary = "My Custom Title", project = "ENG"
	gh := &MockGitHubClient{}
	jira := &MockJiraClient{ReturnKey: "ENG-111"}
	state := newTestState(gh, jira)
	ic := newIssueComment("/jira create My Custom Title project:ENG", "Issue body")

	err := Run(context.Background(), state, ic)

	require.NoError(t, err)
	require.Len(t, jira.Calls, 1)
	assert.Equal(t, "CreateIssue", jira.Calls[0].Method)
	assert.Equal(t, "My Custom Title", jira.Calls[0].Args[2]) // summary
	assert.Equal(t, "ENG", jira.Calls[0].Args[0])             // project
}

func TestCreateJiraIssue_TitleOverride_AllOptionsNoTitle(t *testing.T) {
	// /jira create project:ENG type:Bug → summary = GitHub issue title
	gh := &MockGitHubClient{}
	jira := &MockJiraClient{ReturnKey: "ENG-222"}
	state := newTestState(gh, jira)
	ic := newIssueComment("/jira create project:ENG type:Bug", "Issue body")

	err := Run(context.Background(), state, ic)

	require.NoError(t, err)
	require.Len(t, jira.Calls, 1)
	assert.Equal(t, "CreateIssue", jira.Calls[0].Method)
	assert.Equal(t, "Test Issue", jira.Calls[0].Args[2]) // summary = GitHub issue title
	assert.Equal(t, "ENG", jira.Calls[0].Args[0])        // project
	assert.Equal(t, "Bug", jira.Calls[0].Args[1])        // type
}

func TestCreateJiraIssue_TitleOverride_TitleWithMultipleOptions(t *testing.T) {
	// /jira create Fix the login bug type:Bug assign:true → summary = "Fix the login bug", type = "Bug", assign processed
	gh := &MockGitHubClient{}
	jira := &MockJiraClient{ReturnKey: "DEFAULT-333"}
	resolver := &FlexibleMockResolver{
		Result: common.JiraClientResolveResult{Client: jira},
	}
	store := &MockUserTokenStore{
		Entries: map[string]common.UserTokenEntry{
			"testuser": {
				RefreshToken: "refresh-token",
				AccessToken:  "access-token",
				CloudID:      "cloud-123",
				AccountID:    "5b10ac8d14c1d5",
			},
		},
	}
	state := &common.State{
		Config: common.Config{
			JiraDefaultProject:   "DEFAULT",
			JiraDefaultIssueType: "Task",
		},
		GitHubClient:       gh,
		JiraClientResolver: resolver,
		UserTokenStore:     store,
	}
	ic := newIssueComment("/jira create Fix the login bug type:Bug assign:true", "Issue body")

	err := Run(context.Background(), state, ic)

	require.NoError(t, err)
	require.Len(t, jira.Calls, 1)
	assert.Equal(t, "CreateIssue", jira.Calls[0].Method)
	assert.Equal(t, "Fix the login bug", jira.Calls[0].Args[2]) // summary
	assert.Equal(t, "Bug", jira.Calls[0].Args[1])               // type
	// assign:true should have added assignee to extraFields
	extraFields := jira.Calls[0].Args[4].(map[string]interface{})
	require.Contains(t, extraFields, "assignee")
	assignee := extraFields["assignee"].(map[string]interface{})
	assert.Equal(t, "5b10ac8d14c1d5", assignee["accountId"])
}

func TestCreateJiraIssue_TitleOverride_NoTokensFallsBackToGitHubTitle(t *testing.T) {
	// /jira create with no tokens → summary = GitHub issue title
	gh := &MockGitHubClient{}
	jira := &MockJiraClient{ReturnKey: "DEFAULT-444"}
	state := newTestState(gh, jira)
	ic := newIssueComment("/jira create", "Issue body")

	err := Run(context.Background(), state, ic)

	require.NoError(t, err)
	require.Len(t, jira.Calls, 1)
	assert.Equal(t, "CreateIssue", jira.Calls[0].Method)
	assert.Equal(t, "Test Issue", jira.Calls[0].Args[2]) // summary = GitHub issue title
}

func TestCreateJiraIssue_TitleOverride_SingleTitleToken(t *testing.T) {
	// /jira create Refactor → summary = "Refactor"
	gh := &MockGitHubClient{}
	jira := &MockJiraClient{ReturnKey: "DEFAULT-555"}
	state := newTestState(gh, jira)
	ic := newIssueComment("/jira create Refactor", "Issue body")

	err := Run(context.Background(), state, ic)

	require.NoError(t, err)
	require.Len(t, jira.Calls, 1)
	assert.Equal(t, "CreateIssue", jira.Calls[0].Method)
	assert.Equal(t, "Refactor", jira.Calls[0].Args[2]) // summary
}

func TestCreateJiraIssue_TitleOverride_InterleavedTitleAndOptions(t *testing.T) {
	// /jira create Hello project:ENG World → summary = "Hello World"
	gh := &MockGitHubClient{}
	jira := &MockJiraClient{ReturnKey: "ENG-666"}
	state := newTestState(gh, jira)
	ic := newIssueComment("/jira create Hello project:ENG World", "Issue body")

	err := Run(context.Background(), state, ic)

	require.NoError(t, err)
	require.Len(t, jira.Calls, 1)
	assert.Equal(t, "CreateIssue", jira.Calls[0].Method)
	assert.Equal(t, "Hello World", jira.Calls[0].Args[2]) // summary
	assert.Equal(t, "ENG", jira.Calls[0].Args[0])         // project
}

func TestCreateJiraIssue_AssignTrueWithValidAccountId_IncludesAssigneeInExtraFields(t *testing.T) {
	// Validates: Requirements 4.1
	// When assign:true is in options AND the user has a stored accountId,
	// the CreateIssue call includes "assignee" in extraFields.
	gh := &MockGitHubClient{}
	jira := &MockJiraClient{ReturnKey: "ASSIGN-101"}
	resolver := &FlexibleMockResolver{
		Result: common.JiraClientResolveResult{Client: jira},
	}
	store := &MockUserTokenStore{
		Entries: map[string]common.UserTokenEntry{
			"testuser": {
				RefreshToken: "refresh-token",
				AccessToken:  "access-token",
				CloudID:      "cloud-123",
				AccountID:    "5b10ac8d14c1d5",
			},
		},
	}
	state := &common.State{
		Config: common.Config{
			JiraDefaultProject:   "DEFAULT",
			JiraDefaultIssueType: "Task",
		},
		GitHubClient:       gh,
		JiraClientResolver: resolver,
		UserTokenStore:     store,
	}

	ic := newIssueComment("/jira create assign:true", "Issue body")

	err := Run(context.Background(), state, ic)

	require.NoError(t, err)
	require.Len(t, jira.Calls, 1)
	assert.Equal(t, "CreateIssue", jira.Calls[0].Method)

	// Verify extraFields contains the assignee with the correct accountId
	extraFields := jira.Calls[0].Args[4].(map[string]interface{})
	require.Contains(t, extraFields, "assignee")
	assignee := extraFields["assignee"].(map[string]interface{})
	assert.Equal(t, "5b10ac8d14c1d5", assignee["accountId"])
}

func TestCreateJiraIssue_AssignTrueWithEmptyAccountId_NoAssigneeNoError(t *testing.T) {
	// Validates: Requirements 4.2, 4.3
	// When assign:true is in options BUT the user has no accountId (empty string),
	// CreateIssue is called without assignee and no error is returned.
	gh := &MockGitHubClient{}
	jira := &MockJiraClient{ReturnKey: "ASSIGN-102"}
	resolver := &FlexibleMockResolver{
		Result: common.JiraClientResolveResult{Client: jira},
	}
	store := &MockUserTokenStore{
		Entries: map[string]common.UserTokenEntry{
			"testuser": {
				RefreshToken: "refresh-token",
				AccessToken:  "access-token",
				CloudID:      "cloud-123",
				AccountID:    "", // empty accountId
			},
		},
	}
	state := &common.State{
		Config: common.Config{
			JiraDefaultProject:   "DEFAULT",
			JiraDefaultIssueType: "Task",
		},
		GitHubClient:       gh,
		JiraClientResolver: resolver,
		UserTokenStore:     store,
	}

	ic := newIssueComment("/jira create assign:true", "Issue body")

	err := Run(context.Background(), state, ic)

	require.NoError(t, err)
	require.Len(t, jira.Calls, 1)
	assert.Equal(t, "CreateIssue", jira.Calls[0].Method)

	// Verify extraFields does NOT contain "assignee"
	extraFields := jira.Calls[0].Args[4].(map[string]interface{})
	assert.NotContains(t, extraFields, "assignee")
}

func TestCreateJiraIssue_AssignFalse_NoAssigneeRegardlessOfAccountId(t *testing.T) {
	// Validates: Requirements 4.4
	// When assign:false is in options AND the user has a stored accountId,
	// CreateIssue is called without assignee.
	gh := &MockGitHubClient{}
	jira := &MockJiraClient{ReturnKey: "ASSIGN-103"}
	resolver := &FlexibleMockResolver{
		Result: common.JiraClientResolveResult{Client: jira},
	}
	store := &MockUserTokenStore{
		Entries: map[string]common.UserTokenEntry{
			"testuser": {
				RefreshToken: "refresh-token",
				AccessToken:  "access-token",
				CloudID:      "cloud-123",
				AccountID:    "5b10ac8d14c1d5", // valid accountId present
			},
		},
	}
	state := &common.State{
		Config: common.Config{
			JiraDefaultProject:   "DEFAULT",
			JiraDefaultIssueType: "Task",
		},
		GitHubClient:       gh,
		JiraClientResolver: resolver,
		UserTokenStore:     store,
	}

	ic := newIssueComment("/jira create assign:false", "Issue body")

	err := Run(context.Background(), state, ic)

	require.NoError(t, err)
	require.Len(t, jira.Calls, 1)
	assert.Equal(t, "CreateIssue", jira.Calls[0].Method)

	// Verify extraFields does NOT contain "assignee"
	extraFields := jira.Calls[0].Args[4].(map[string]interface{})
	assert.NotContains(t, extraFields, "assignee")
}

// --- 3.6 Integration tests for full Run flow with quoted field values ---

func TestRun_QuotedFieldValue_PriorityInProgress(t *testing.T) {
	// /jira create priority:"In Progress" project:ENG
	// Validates: Requirements 1.1, 2.1, 2.3
	gh := &MockGitHubClient{}
	jira := &MockJiraClient{ReturnKey: "ENG-Q01"}
	state := newTestState(gh, jira)
	ic := newIssueComment(`/jira create priority:"In Progress" project:ENG`, "Issue body")

	err := Run(context.Background(), state, ic)

	require.NoError(t, err)
	require.Len(t, jira.Calls, 1)
	assert.Equal(t, "CreateIssue", jira.Calls[0].Method)
	assert.Equal(t, "ENG", jira.Calls[0].Args[0]) // project

	// Verify priority field receives the unquoted value "In Progress"
	extraFields := jira.Calls[0].Args[4].(map[string]interface{})
	// priority is a well-known field → coerced to {"name": "In Progress"}
	assert.Equal(t, map[string]interface{}{"name": "In Progress"}, extraFields["priority"])
}

func TestRun_QuotedTitle_MyCustomTitle(t *testing.T) {
	// /jira create "My Custom Title" type:Bug
	// Validates: Requirements 1.2, 2.2
	gh := &MockGitHubClient{}
	jira := &MockJiraClient{ReturnKey: "DEFAULT-Q02"}
	state := newTestState(gh, jira)
	ic := newIssueComment(`/jira create "My Custom Title" type:Bug`, "Issue body")

	err := Run(context.Background(), state, ic)

	require.NoError(t, err)
	require.Len(t, jira.Calls, 1)
	assert.Equal(t, "CreateIssue", jira.Calls[0].Method)
	assert.Equal(t, "My Custom Title", jira.Calls[0].Args[2]) // summary
	assert.Equal(t, "Bug", jira.Calls[0].Args[1])             // type
}

func TestRun_MixedQuotedAndUnquoted_FullCommand(t *testing.T) {
	// /jira create Fix Bug priority:"In Progress" project:ENG type:Bug
	// Validates: Requirements 1.1, 1.3, 2.1, 2.3, 3.1
	gh := &MockGitHubClient{}
	jira := &MockJiraClient{ReturnKey: "ENG-Q03"}
	state := newTestState(gh, jira)
	ic := newIssueComment(`/jira create Fix Bug priority:"In Progress" project:ENG type:Bug`, "Issue body")

	err := Run(context.Background(), state, ic)

	require.NoError(t, err)
	require.Len(t, jira.Calls, 1)
	assert.Equal(t, "CreateIssue", jira.Calls[0].Method)
	assert.Equal(t, "Fix Bug", jira.Calls[0].Args[2]) // summary (title tokens joined)
	assert.Equal(t, "ENG", jira.Calls[0].Args[0])     // project
	assert.Equal(t, "Bug", jira.Calls[0].Args[1])     // type

	// Verify priority field receives "In Progress"
	extraFields := jira.Calls[0].Args[4].(map[string]interface{})
	assert.Equal(t, map[string]interface{}{"name": "In Progress"}, extraFields["priority"])
}

func TestRun_UnquotedTitleWithQuotedStatus(t *testing.T) {
	// /jira create My Custom Title project:ENG status:"In Progress"
	// Validates: Requirements 1.1, 2.1, 3.2
	gh := &MockGitHubClient{}
	jira := &MockJiraClient{ReturnKey: "ENG-Q04"}
	state := newTestState(gh, jira)
	ic := newIssueComment(`/jira create My Custom Title project:ENG status:"In Progress"`, "Issue body")

	err := Run(context.Background(), state, ic)

	require.NoError(t, err)
	require.Len(t, jira.Calls, 1)
	assert.Equal(t, "CreateIssue", jira.Calls[0].Method)
	assert.Equal(t, "My Custom Title", jira.Calls[0].Args[2]) // summary (title tokens joined)
	assert.Equal(t, "ENG", jira.Calls[0].Args[0])             // project

	// Verify status field receives "In Progress" (status is not a well-known field, so raw string)
	extraFields := jira.Calls[0].Args[4].(map[string]interface{})
	assert.Equal(t, "In Progress", extraFields["status"])
}

// --- 3.3 tokenizeLine unit tests ---

func TestTokenizeLine(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "quoted field value",
			input:    `priority:"In Progress"`,
			expected: []string{"priority:In Progress"},
		},
		{
			name:     "quoted title",
			input:    `"My Title" type:Bug`,
			expected: []string{"My Title", "type:Bug"},
		},
		{
			name:     "multiple quoted fields",
			input:    `priority:"In Progress" status:"To Do"`,
			expected: []string{"priority:In Progress", "status:To Do"},
		},
		{
			name:     "mixed quoted and unquoted",
			input:    `Fix Bug priority:"In Progress" project:ENG`,
			expected: []string{"Fix", "Bug", "priority:In Progress", "project:ENG"},
		},
		{
			name:     "unclosed quote treats remaining as one token",
			input:    `priority:"In Progress`,
			expected: []string{"priority:In Progress"},
		},
		{
			name:     "empty quotes",
			input:    `key:""`,
			expected: []string{"key:"},
		},
		{
			name:     "quoted single word value",
			input:    `type:"Bug"`,
			expected: []string{"type:Bug"},
		},
		{
			name:     "no quotes",
			input:    `priority:High type:Bug`,
			expected: []string{"priority:High", "type:Bug"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tokenizeLine(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}


// --- extractPath unit tests ---

func TestExtractPath_EmptyString(t *testing.T) {
	result := extractPath("")
	assert.Equal(t, "", result)
}

func TestExtractPath_ValidHTTPSURL(t *testing.T) {
	result := extractPath("https://github.com/org/repo/issues/42")
	assert.Equal(t, "/org/repo/issues/42", result)
}

func TestExtractPath_ValidHTTPURL(t *testing.T) {
	result := extractPath("http://github.com/org/repo/pull/7")
	assert.Equal(t, "/org/repo/pull/7", result)
}

func TestExtractPath_URLWithQueryAndFragment(t *testing.T) {
	result := extractPath("https://github.com/org/repo/issues/42?foo=bar#comment")
	assert.Equal(t, "/org/repo/issues/42", result)
}

func TestExtractPath_PathOnly(t *testing.T) {
	result := extractPath("/org/repo/issues/42")
	assert.Equal(t, "/org/repo/issues/42", result)
}

func TestExtractPath_URLWithPort(t *testing.T) {
	result := extractPath("https://github.example.com:8443/org/repo/issues/1")
	assert.Equal(t, "/org/repo/issues/1", result)
}

// --- 2.4 Auth link construction tests (return_to parameter) ---

func TestResolveJiraClient_EmptyHTMLURL_NoReturnToAppended(t *testing.T) {
	// Validates: Requirements 1.1, 1.2
	// When issueComment.Issue.HTMLURL is empty, the auth link should have no return_to parameter.
	gh := &MockGitHubClient{}
	resolver := &FlexibleMockResolver{
		Result: common.JiraClientResolveResult{
			AuthRequired: true,
			AuthLink:     "https://bot.example.com/oauth/authorize",
		},
	}
	state := &common.State{
		Config: common.Config{
			JiraDefaultProject:   "DEFAULT",
			JiraDefaultIssueType: "Task",
		},
		GitHubClient:       gh,
		JiraClientResolver: resolver,
	}

	ic := &github.IssueComment{
		Action: "created",
		Issue: github.Issue{
			Title:   "Test Issue",
			Body:    "Issue body",
			HTMLURL: "", // empty HTMLURL
		},
		Comment: github.Comment{
			Body: "/jira create",
			User: github.CommentUser{Login: "octocat"},
		},
		Installation: github.Installation{ID: 42},
	}

	err := Run(context.Background(), state, ic)

	require.NoError(t, err)
	// Find the PostComment call with the auth link
	var authBody string
	for _, call := range gh.Calls {
		if call.Method == "PostComment" {
			body := call.Args[3].(string)
			if strings.Contains(body, "authorize") {
				authBody = body
				break
			}
		}
	}
	require.NotEmpty(t, authBody, "Expected a PostComment call with auth link")
	// Auth link should NOT contain return_to
	assert.NotContains(t, authBody, "return_to")
	// Auth link should contain comment_id and installation_id
	assert.Contains(t, authBody, "comment_id=0")
	assert.Contains(t, authBody, "installation_id=42")
}

func TestResolveJiraClient_ValidHTMLURL_ReturnToAppended(t *testing.T) {
	// Validates: Requirements 1.1, 1.2
	// When issueComment.Issue.HTMLURL is a valid URL, the auth link should have
	// ?return_to= with the percent-encoded path appended.
	gh := &MockGitHubClient{}
	resolver := &FlexibleMockResolver{
		Result: common.JiraClientResolveResult{
			AuthRequired: true,
			AuthLink:     "https://bot.example.com/oauth/authorize",
		},
	}
	state := &common.State{
		Config: common.Config{
			JiraDefaultProject:   "DEFAULT",
			JiraDefaultIssueType: "Task",
		},
		GitHubClient:       gh,
		JiraClientResolver: resolver,
	}

	ic := &github.IssueComment{
		Action: "created",
		Issue: github.Issue{
			Title:   "Test Issue",
			Body:    "Issue body",
			HTMLURL: "https://github.com/org/repo/issues/42",
		},
		Comment: github.Comment{
			Body: "/jira create",
			User: github.CommentUser{Login: "octocat"},
		},
		Installation: github.Installation{ID: 42},
	}

	err := Run(context.Background(), state, ic)

	require.NoError(t, err)
	// Find the PostComment call with the auth link
	var authBody string
	for _, call := range gh.Calls {
		if call.Method == "PostComment" {
			body := call.Args[3].(string)
			if strings.Contains(body, "authorize") {
				authBody = body
				break
			}
		}
	}
	require.NotEmpty(t, authBody, "Expected a PostComment call with auth link")
	// Auth link should contain the return_to parameter with percent-encoded path
	assert.Contains(t, authBody, "return_to=%2Forg%2Frepo%2Fissues%2F42")
}

func TestCreateJiraIssue_CRLFLineEndings_CommandParsedCorrectly(t *testing.T) {
	// When GitHub sends comment bodies with \r\n line endings,
	// the command should still be recognized and the issue should be created.
	gh := &MockGitHubClient{}
	jira := &MockJiraClient{ReturnKey: "ENG-900"}
	state := newTestState(gh, jira)

	// Simulate \r\n line endings as sent by GitHub
	commentBody := "/jira create project:ENG\r\nThis is a custom description"
	ic := newIssueComment(commentBody, "Issue body")

	err := Run(context.Background(), state, ic)

	require.NoError(t, err)
	require.Len(t, jira.Calls, 1, "CreateIssue should have been called")
	assert.Equal(t, "CreateIssue", jira.Calls[0].Method)
	assert.Equal(t, "ENG", jira.Calls[0].Args[0])

	// Verify the custom description was extracted (not the issue body)
	description := jira.Calls[0].Args[3].(string)
	assert.Contains(t, description, "This is a custom description")
}

func TestCreateJiraIssue_CRLFLineEndings_NoBodyText(t *testing.T) {
	// \r\n at end of single-line command (e.g., trailing whitespace from GitHub)
	gh := &MockGitHubClient{}
	jira := &MockJiraClient{ReturnKey: "ENG-901"}
	state := newTestState(gh, jira)

	commentBody := "/jira create project:ENG\r\n"
	ic := newIssueComment(commentBody, "Issue body used as fallback")

	err := Run(context.Background(), state, ic)

	require.NoError(t, err)
	require.Len(t, jira.Calls, 1, "CreateIssue should have been called")
	assert.Equal(t, "CreateIssue", jira.Calls[0].Method)

	// Should fall back to issue body since no custom description after \r\n
	description := jira.Calls[0].Args[3].(string)
	assert.Contains(t, description, "Issue body used as fallback")
}

func TestCreateJiraIssue_AssignNotSentAsJiraField(t *testing.T) {
	// Regression test: assign:true and assign:false must NOT be sent to Jira
	// as a field override (Jira doesn't have an "assign" field and rejects it).
	tests := []struct {
		name    string
		command string
	}{
		{"assign:true not sent as field", "/jira create assign:true"},
		{"assign:false not sent as field", "/jira create assign:false"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gh := &MockGitHubClient{}
			jira := &MockJiraClient{ReturnKey: "ENG-999"}
			state := newTestState(gh, jira)

			ic := newIssueComment(tc.command, "Issue body")

			err := Run(context.Background(), state, ic)

			require.NoError(t, err)
			require.Len(t, jira.Calls, 1)
			assert.Equal(t, "CreateIssue", jira.Calls[0].Method)

			// "assign" must NOT appear in extraFields — it's a control option, not a Jira field
			extraFields := jira.Calls[0].Args[4].(map[string]interface{})
			assert.NotContains(t, extraFields, "assign",
				"assign should be excluded from Jira fields; it is a reserved control option")
		})
	}
}

// --- 8.2 Auth link construction with comment context tests ---

func TestResolveJiraClient_AuthLinkContainsCommentIDAndInstallationID(t *testing.T) {
	// Validates: Requirements 1.1
	// When resolveJiraClient posts an auth link, it should contain comment_id and
	// installation_id query parameters matching the issueComment fields.
	gh := &MockGitHubClient{}
	resolver := &FlexibleMockResolver{
		Result: common.JiraClientResolveResult{
			AuthRequired: true,
			AuthLink:     "https://bot.example.com/oauth/authorize",
		},
	}
	state := &common.State{
		Config: common.Config{
			JiraDefaultProject:   "DEFAULT",
			JiraDefaultIssueType: "Task",
		},
		GitHubClient:       gh,
		JiraClientResolver: resolver,
	}

	ic := &github.IssueComment{
		Action: "created",
		Issue: github.Issue{
			Title:   "Test Issue",
			Body:    "Issue body",
			HTMLURL: "https://github.com/org/repo/issues/7",
		},
		Comment: github.Comment{
			Body: "/jira create",
			ID:   98765,
			User: github.CommentUser{Login: "testuser"},
		},
		Installation: github.Installation{ID: 555},
	}

	err := Run(context.Background(), state, ic)

	require.NoError(t, err)
	// Find the PostComment call with the auth link
	var authBody string
	for _, call := range gh.Calls {
		if call.Method == "PostComment" {
			body := call.Args[3].(string)
			if strings.Contains(body, "authorize") {
				authBody = body
				break
			}
		}
	}
	require.NotEmpty(t, authBody, "Expected a PostComment call with auth link")
	// Auth link should contain comment_id matching Comment.ID
	assert.Contains(t, authBody, "comment_id=98765")
	// Auth link should contain installation_id matching Installation.ID
	assert.Contains(t, authBody, "installation_id=555")
}

func TestResolveJiraClient_AuthLinkCommentContextValuesMatchIssueComment(t *testing.T) {
	// Validates: Requirements 1.1
	// Verify that different Comment.ID and Installation.ID values are correctly
	// reflected in the auth link query parameters.
	tests := []struct {
		name           string
		commentID      uint64
		installationID int64
	}{
		{"zero comment ID", 0, 100},
		{"large comment ID", 1234567890, 99},
		{"large installation ID", 42, 9876543210},
		{"both large values", 18446744073709551615, 9223372036854775807},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gh := &MockGitHubClient{}
			resolver := &FlexibleMockResolver{
				Result: common.JiraClientResolveResult{
					AuthRequired: true,
					AuthLink:     "https://bot.example.com/oauth/authorize",
				},
			}
			state := &common.State{
				Config: common.Config{
					JiraDefaultProject:   "DEFAULT",
					JiraDefaultIssueType: "Task",
				},
				GitHubClient:       gh,
				JiraClientResolver: resolver,
			}

			ic := &github.IssueComment{
				Action: "created",
				Issue: github.Issue{
					Title:   "Test Issue",
					Body:    "Issue body",
					HTMLURL: "https://github.com/org/repo/issues/1",
				},
				Comment: github.Comment{
					Body: "/jira create",
					ID:   tc.commentID,
					User: github.CommentUser{Login: "testuser"},
				},
				Installation: github.Installation{ID: tc.installationID},
			}

			err := Run(context.Background(), state, ic)

			require.NoError(t, err)
			// Find the PostComment call with the auth link
			var authBody string
			for _, call := range gh.Calls {
				if call.Method == "PostComment" {
					body := call.Args[3].(string)
					if strings.Contains(body, "authorize") {
						authBody = body
						break
					}
				}
			}
			require.NotEmpty(t, authBody, "Expected a PostComment call with auth link")
			expectedCommentID := fmt.Sprintf("comment_id=%d", tc.commentID)
			expectedInstallationID := fmt.Sprintf("installation_id=%d", tc.installationID)
			assert.Contains(t, authBody, expectedCommentID)
			assert.Contains(t, authBody, expectedInstallationID)
		})
	}
}

package executor

import (
	"context"
	"errors"
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

// MockJiraClient implements common.JiraClientInterface for testing.
type MockJiraClient struct {
	ReturnKey string
	ReturnErr error
	Calls     []MockCall
}

func (m *MockJiraClient) CreateIssue(project, issueType, summary, description string) (string, error) {
	m.Calls = append(m.Calls, MockCall{Method: "CreateIssue", Args: []interface{}{project, issueType, summary, description}})
	return m.ReturnKey, m.ReturnErr
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
		GitHubClient: gh,
		JiraClient:   jira,
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

func TestRun_IssueWithJiraBotMarkerReturnsError(t *testing.T) {
	gh := &MockGitHubClient{}
	jira := &MockJiraClient{}
	state := newTestState(gh, jira)
	ic := newIssueComment("/jira create", "Body with <!--JIRA_BOT_ISSUE marker")

	err := Run(context.Background(), state, ic)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "a Jira issue seems to have been already created")
	// createJiraIssue posts warning, then Run's error handler calls ReactWithConfused + PostComment with error details
	require.Len(t, gh.Calls, 3)
	assert.Equal(t, "PostComment", gh.Calls[0].Method)
	assert.Contains(t, gh.Calls[0].Args[3].(string), "Uh-oh")
	assert.Equal(t, "ReactWithConfused", gh.Calls[1].Method)
	assert.Equal(t, "PostComment", gh.Calls[2].Method)
	assert.Contains(t, gh.Calls[2].Args[3].(string), "Error trying to create issue")
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
		GitHubClient:     gh,
		JiraClient:       jira,
		RepoConfigLoader: loader,
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
		GitHubClient: gh,
		JiraClient:   jira,
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
		GitHubClient:     gh,
		JiraClient:       jira,
		RepoConfigLoader: loader,
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
		GitHubClient:     gh,
		JiraClient:       jira,
		RepoConfigLoader: loader,
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
		GitHubClient:     gh,
		JiraClient:       jira,
		RepoConfigLoader: loader,
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
		GitHubClient:     gh,
		JiraClient:       jira,
		RepoConfigLoader: loader,
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
		GitHubClient: gh,
		JiraClient:   jira,
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

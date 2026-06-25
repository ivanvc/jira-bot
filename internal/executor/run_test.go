package executor

import (
	"context"
	"errors"
	"testing"

	"github.com/ivanvc/jira-bot/internal/adapters/github"
	"github.com/ivanvc/jira-bot/internal/common"
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

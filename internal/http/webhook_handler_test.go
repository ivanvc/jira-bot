package http

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"

	gogithub "github.com/google/go-github/v58/github"
	"github.com/stretchr/testify/assert"

	"github.com/ivanvc/jira-bot/internal/adapters/github"
	"github.com/ivanvc/jira-bot/internal/common"
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

func (m *MockGitHubClient) FetchComment(ctx context.Context, installationID int64, owner, repo string, commentID uint64) (*github.IssueComment, error) {
	m.Calls = append(m.Calls, MockCall{Method: "FetchComment", Args: []interface{}{ctx, installationID, owner, repo, commentID}})
	return nil, nil
}

func (m *MockGitHubClient) EditComment(ctx context.Context, installationID int64, owner, repo string, commentID int64, body string) error {
	m.Calls = append(m.Calls, MockCall{Method: "EditComment", Args: []interface{}{ctx, installationID, owner, repo, commentID, body}})
	return nil
}

func (m *MockGitHubClient) ListIssueComments(ctx context.Context, installationID int64, owner, repo string, issueNumber int) ([]*gogithub.IssueComment, error) {
	m.Calls = append(m.Calls, MockCall{Method: "ListIssueComments", Args: []interface{}{ctx, installationID, owner, repo, issueNumber}})
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

// testWebhookSetup holds the components needed to test the webhook handler.
type testWebhookSetup struct {
	mux     *http.ServeMux
	server  *httptest.Server
	gh      *MockGitHubClient
	jira    *MockJiraClient
	state   *common.State
	handler *webhookHandler
}

// newTestWebhookSetup creates a test webhook handler with a per-test mux and
// mock clients injected. The caller should defer setup.server.Close().
func newTestWebhookSetup(t *testing.T, secret string) *testWebhookSetup {
	t.Helper()

	gh := &MockGitHubClient{}
	jira := &MockJiraClient{ReturnKey: "TEST-1"}

	state := &common.State{
		Config: common.Config{
			GitHubWebhookSecret:  secret,
			JiraDefaultProject:   "DEFAULT",
			JiraDefaultIssueType: "Task",
		},
		GitHubClient:       gh,
		JiraClientResolver: &MockJiraClientResolver{Client: jira},
	}

	h := &webhookHandler{}

	mux := http.NewServeMux()
	mux.HandleFunc("/webhooks/github/payload", h.handleWithState(state))

	ts := httptest.NewServer(mux)

	return &testWebhookSetup{
		mux:     mux,
		server:  ts,
		gh:      gh,
		jira:    jira,
		state:   state,
		handler: h,
	}
}

// computeHMACSHA256 computes HMAC-SHA256 of payload using secret, returns "sha256=<hex>" string.
func computeHMACSHA256(payload []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// computeRawHMACSHA256 computes the HMAC-SHA256 of payload using secret and
// returns the raw hex string without the "sha256=" prefix.
func computeRawHMACSHA256(payload []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

// TestVerifySignature_ValidSignature tests that a valid HMAC-SHA256 signature
// with the "sha256=" prefix returns true.
// Validates: Requirements 5.1
func TestVerifySignature_ValidSignature(t *testing.T) {
	h := &webhookHandler{}
	payload := []byte(`{"action":"created","comment":{"body":"/jira create"}}`)
	secret := "test-secret"

	signature := computeHMACSHA256(payload, secret)

	result := h.verifySignature(payload, signature, secret)
	assert.True(t, result, "valid HMAC-SHA256 signature should return true")
}

// TestVerifySignature_InvalidSignature tests that an invalid HMAC signature
// returns false.
// Validates: Requirements 5.2
func TestVerifySignature_InvalidSignature(t *testing.T) {
	h := &webhookHandler{}
	payload := []byte(`{"action":"created","comment":{"body":"/jira create"}}`)
	secret := "test-secret"

	invalidSignature := "sha256=deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"

	result := h.verifySignature(payload, invalidSignature, secret)
	assert.False(t, result, "invalid HMAC signature should return false")
}

// TestVerifySignature_EmptySignature tests that an empty signature string
// returns false.
// Validates: Requirements 5.3
func TestVerifySignature_EmptySignature(t *testing.T) {
	h := &webhookHandler{}
	payload := []byte(`{"action":"created","comment":{"body":"/jira create"}}`)
	secret := "test-secret"

	result := h.verifySignature(payload, "", secret)
	assert.False(t, result, "empty signature should return false")
}

// TestVerifySignature_WithoutPrefix tests that a signature without the
// "sha256=" prefix validates correctly against the raw hex value.
// Validates: Requirements 5.4
func TestVerifySignature_WithoutPrefix(t *testing.T) {
	h := &webhookHandler{}
	payload := []byte(`{"action":"created","comment":{"body":"/jira create"}}`)
	secret := "test-secret"

	// Pass the raw hex signature without the "sha256=" prefix
	signature := computeRawHMACSHA256(payload, secret)

	result := h.verifySignature(payload, signature, secret)
	assert.True(t, result, "signature without sha256= prefix should validate correctly")
}

func TestWebhookHandler_NonPOST_Returns405(t *testing.T) {
	setup := newTestWebhookSetup(t, "test-secret")
	defer setup.server.Close()

	resp, err := http.Get(setup.server.URL + "/webhooks/github/payload")
	assert.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
}

func TestWebhookHandler_InvalidSignature_Returns401(t *testing.T) {
	setup := newTestWebhookSetup(t, "test-secret")
	defer setup.server.Close()

	payload := []byte(`{"action":"created"}`)
	req, err := http.NewRequest(http.MethodPost, setup.server.URL+"/webhooks/github/payload", bytes.NewReader(payload))
	assert.NoError(t, err)
	req.Header.Set("X-Hub-Signature-256", "sha256=invalidsignature")
	req.Header.Set("X-Github-Event", "issue_comment")

	resp, err := http.DefaultClient.Do(req)
	assert.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestWebhookHandler_ValidSignature_IssueCommentCreated_Returns200(t *testing.T) {
	secret := "test-secret"
	setup := newTestWebhookSetup(t, secret)
	defer setup.server.Close()

	payload := []byte(`{
		"action": "created",
		"issue": {
			"comments_url": "https://api.github.com/repos/owner/repo/issues/1/comments",
			"body": "some issue body",
			"state": "open",
			"url": "https://api.github.com/repos/owner/repo/issues/1",
			"html_url": "https://github.com/owner/repo/issues/1",
			"title": "Test Issue"
		},
		"comment": {
			"body": "/jira create",
			"node_id": "IC_123",
			"id": 1,
			"reactions": {"url": "https://api.github.com/repos/owner/repo/issues/comments/1/reactions"},
			"user": {"login": "testuser"}
		},
		"installation": {"id": 42}
	}`)

	sig := computeHMACSHA256(payload, secret)
	req, err := http.NewRequest(http.MethodPost, setup.server.URL+"/webhooks/github/payload", bytes.NewReader(payload))
	assert.NoError(t, err)
	req.Header.Set("X-Hub-Signature-256", sig)
	req.Header.Set("X-Github-Event", "issue_comment")

	resp, err := http.DefaultClient.Do(req)
	assert.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Verify that the executor invoked mock clients (Jira CreateIssue should have been called)
	assert.True(t, len(setup.jira.Calls) > 0, "expected Jira client to be invoked")
	assert.Equal(t, "CreateIssue", setup.jira.Calls[0].Method)
}

func TestWebhookHandler_IssueCommentNonCreatedAction_Returns204(t *testing.T) {
	secret := "test-secret"
	setup := newTestWebhookSetup(t, secret)
	defer setup.server.Close()

	payload := []byte(`{
		"action": "deleted",
		"issue": {
			"comments_url": "https://api.github.com/repos/owner/repo/issues/1/comments",
			"body": "some issue body",
			"state": "open",
			"url": "https://api.github.com/repos/owner/repo/issues/1",
			"html_url": "https://github.com/owner/repo/issues/1",
			"title": "Test Issue"
		},
		"comment": {
			"body": "/jira create",
			"node_id": "IC_123",
			"id": 1,
			"reactions": {"url": "https://api.github.com/repos/owner/repo/issues/comments/1/reactions"}
		},
		"installation": {"id": 42}
	}`)

	sig := computeHMACSHA256(payload, secret)
	req, err := http.NewRequest(http.MethodPost, setup.server.URL+"/webhooks/github/payload", bytes.NewReader(payload))
	assert.NoError(t, err)
	req.Header.Set("X-Hub-Signature-256", sig)
	req.Header.Set("X-Github-Event", "issue_comment")

	resp, err := http.DefaultClient.Do(req)
	assert.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
}

func TestWebhookHandler_NonIssueCommentEvent_Returns200(t *testing.T) {
	secret := "test-secret"
	setup := newTestWebhookSetup(t, secret)
	defer setup.server.Close()

	payload := []byte(`{"action": "opened"}`)

	sig := computeHMACSHA256(payload, secret)
	req, err := http.NewRequest(http.MethodPost, setup.server.URL+"/webhooks/github/payload", bytes.NewReader(payload))
	assert.NoError(t, err)
	req.Header.Set("X-Hub-Signature-256", sig)
	req.Header.Set("X-Github-Event", "push")

	resp, err := http.DefaultClient.Do(req)
	assert.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	// Executor should not have been invoked
	assert.Empty(t, setup.jira.Calls, "expected no Jira client calls for non-issue_comment event")
	assert.Empty(t, setup.gh.Calls, "expected no GitHub client calls for non-issue_comment event")
}

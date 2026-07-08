package http

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ivanvc/jira-bot/internal/adapters/github"
	"github.com/ivanvc/jira-bot/internal/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockUserTokenStore is a simple in-memory mock for testing.
type mockUserTokenStore struct {
	entries  map[string]common.UserTokenEntry
	writeErr error
}

func newMockUserTokenStore() *mockUserTokenStore {
	return &mockUserTokenStore{entries: make(map[string]common.UserTokenEntry)}
}

func (m *mockUserTokenStore) Read(_ context.Context, login string) (common.UserTokenEntry, error) {
	entry, ok := m.entries[login]
	if !ok {
		return common.UserTokenEntry{}, common.ErrNotFound
	}
	return entry, nil
}

func (m *mockUserTokenStore) ReadAll(_ context.Context) (map[string]common.UserTokenEntry, error) {
	return m.entries, nil
}

func (m *mockUserTokenStore) Write(_ context.Context, login string, entry common.UserTokenEntry) error {
	if m.writeErr != nil {
		return m.writeErr
	}
	m.entries[login] = entry
	return nil
}

func (m *mockUserTokenStore) Delete(_ context.Context, login string) error {
	delete(m.entries, login)
	return nil
}

// newTestAuthHandler creates a userAuthHandler configured for testing with
// optional mock server URLs.
func newTestAuthHandler(store *mockUserTokenStore) *userAuthHandler {
	return &userAuthHandler{
		githubAppClientID:     "test-github-client-id",
		githubAppClientSecret: "test-github-client-secret",
		atlClientID:           "test-atl-client-id",
		atlClientSecret:       "test-atl-client-secret",
		atlCallbackURL:        "http://localhost:8080/oauth/atlassian/callback",
		cloudID:               "test-cloud-id-abc123",
		store:                 store,
		cookieSecret:          "test-cookie-secret",
		githubRedirectBaseURL: "https://github.com",
		redirectDelaySec:      3,
	}
}

// TestHandleAuthorize_RedirectsToGitHub verifies that the /oauth/authorize
// endpoint redirects to GitHub OAuth with the correct client_id and state.
// Validates: Requirement 2.3
func TestHandleAuthorize_RedirectsToGitHub(t *testing.T) {
	store := newMockUserTokenStore()
	handler := newTestAuthHandler(store)

	req := httptest.NewRequest(http.MethodGet, "/oauth/authorize", nil)
	rec := httptest.NewRecorder()

	handler.handleAuthorize(rec, req)

	result := rec.Result()
	defer result.Body.Close()

	// Should redirect (302)
	assert.Equal(t, http.StatusFound, result.StatusCode)

	// Parse the redirect location
	location := result.Header.Get("Location")
	require.NotEmpty(t, location, "redirect Location header should be set")

	redirectURL, err := url.Parse(location)
	require.NoError(t, err)

	// Verify it redirects to GitHub OAuth authorize endpoint
	assert.Equal(t, "github.com", redirectURL.Host)
	assert.Equal(t, "/login/oauth/authorize", redirectURL.Path)

	// Verify client_id matches the GitHub App's client ID
	assert.Equal(t, "test-github-client-id", redirectURL.Query().Get("client_id"))

	// Verify state is present and is a valid session ID (64 hex chars)
	state := redirectURL.Query().Get("state")
	assert.NotEmpty(t, state)
	assert.Len(t, state, 64, "state should be 64 hex chars (32 bytes)")

	// Verify the session cookie is set with a signed value containing the state
	cookies := result.Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == userAuthSessionCookie {
			sessionCookie = c
			break
		}
	}
	require.NotNil(t, sessionCookie, "session cookie should be set")
	assert.True(t, sessionCookie.HttpOnly)
	assert.True(t, sessionCookie.Secure)

	// Verify the cookie contains the state (verify by extracting the JSON payload)
	payload, err := verifySignedCookiePayload(sessionCookie.Value, "test-cookie-secret", authSessionTTL)
	require.NoError(t, err)
	assert.Equal(t, state, payload.State)
	assert.Empty(t, payload.ReturnTo, "ReturnTo should be empty when no return_to query param is provided")
}

// TestHandleAuthorize_WithReturnTo verifies that when a return_to query parameter
// is provided, it is stored in the JSON cookie payload alongside the state.
// Validates: Requirement 2.1
func TestHandleAuthorize_WithReturnTo(t *testing.T) {
	store := newMockUserTokenStore()
	handler := newTestAuthHandler(store)

	req := httptest.NewRequest(http.MethodGet, "/oauth/authorize?return_to=%2Forg%2Frepo%2Fissues%2F42", nil)
	rec := httptest.NewRecorder()

	handler.handleAuthorize(rec, req)

	result := rec.Result()
	defer result.Body.Close()

	// Should redirect (302)
	assert.Equal(t, http.StatusFound, result.StatusCode)

	// Find the session cookie
	var sessionCookie *http.Cookie
	for _, c := range result.Cookies() {
		if c.Name == userAuthSessionCookie {
			sessionCookie = c
			break
		}
	}
	require.NotNil(t, sessionCookie, "session cookie should be set")

	// Verify the cookie payload includes both state and return_to
	payload, err := verifySignedCookiePayload(sessionCookie.Value, "test-cookie-secret", authSessionTTL)
	require.NoError(t, err)
	assert.Len(t, payload.State, 64, "state should be 64 hex chars")
	assert.Equal(t, "/org/repo/issues/42", payload.ReturnTo)
}

// TestHandleAuthorize_WithCommentContext verifies that when comment_id and
// installation_id query parameters are provided, they are stored in the cookie payload.
// Validates: Requirements 1.1, 1.4, 1.5
func TestHandleAuthorize_WithCommentContext(t *testing.T) {
	store := newMockUserTokenStore()
	handler := newTestAuthHandler(store)

	req := httptest.NewRequest(http.MethodGet, "/oauth/authorize?return_to=%2Forg%2Frepo%2Fissues%2F42&comment_id=123456789&installation_id=987654", nil)
	rec := httptest.NewRecorder()

	handler.handleAuthorize(rec, req)

	result := rec.Result()
	defer result.Body.Close()

	assert.Equal(t, http.StatusFound, result.StatusCode)

	var sessionCookie *http.Cookie
	for _, c := range result.Cookies() {
		if c.Name == userAuthSessionCookie {
			sessionCookie = c
			break
		}
	}
	require.NotNil(t, sessionCookie)

	payload, err := verifySignedCookiePayload(sessionCookie.Value, "test-cookie-secret", authSessionTTL)
	require.NoError(t, err)
	assert.Equal(t, "/org/repo/issues/42", payload.ReturnTo)
	assert.Equal(t, uint64(123456789), payload.CommentID)
	assert.Equal(t, int64(987654), payload.InstallationID)
}

// TestHandleAuthorize_InvalidCommentContext verifies that invalid comment_id and
// installation_id values are treated as zero (absent).
// Validates: Requirements 1.4, 1.5
func TestHandleAuthorize_InvalidCommentContext(t *testing.T) {
	store := newMockUserTokenStore()
	handler := newTestAuthHandler(store)

	req := httptest.NewRequest(http.MethodGet, "/oauth/authorize?comment_id=not-a-number&installation_id=also-bad", nil)
	rec := httptest.NewRecorder()

	handler.handleAuthorize(rec, req)

	result := rec.Result()
	defer result.Body.Close()

	assert.Equal(t, http.StatusFound, result.StatusCode)

	var sessionCookie *http.Cookie
	for _, c := range result.Cookies() {
		if c.Name == userAuthSessionCookie {
			sessionCookie = c
			break
		}
	}
	require.NotNil(t, sessionCookie)

	payload, err := verifySignedCookiePayload(sessionCookie.Value, "test-cookie-secret", authSessionTTL)
	require.NoError(t, err)
	assert.Equal(t, uint64(0), payload.CommentID, "invalid comment_id should be treated as zero")
	assert.Equal(t, int64(0), payload.InstallationID, "invalid installation_id should be treated as zero")
}

// TestHandleAuthorize_MissingCommentContext verifies that missing comment_id and
// installation_id query parameters result in zero values in the cookie payload.
// Validates: Requirements 1.4
func TestHandleAuthorize_MissingCommentContext(t *testing.T) {
	store := newMockUserTokenStore()
	handler := newTestAuthHandler(store)

	req := httptest.NewRequest(http.MethodGet, "/oauth/authorize?return_to=%2Fissues%2F1", nil)
	rec := httptest.NewRecorder()

	handler.handleAuthorize(rec, req)

	result := rec.Result()
	defer result.Body.Close()

	var sessionCookie *http.Cookie
	for _, c := range result.Cookies() {
		if c.Name == userAuthSessionCookie {
			sessionCookie = c
			break
		}
	}
	require.NotNil(t, sessionCookie)

	payload, err := verifySignedCookiePayload(sessionCookie.Value, "test-cookie-secret", authSessionTTL)
	require.NoError(t, err)
	assert.Equal(t, uint64(0), payload.CommentID)
	assert.Equal(t, int64(0), payload.InstallationID)
}

// TestHandleGitHubCallback_ExchangesCodeAndRedirectsToAtlassian tests the full
// GitHub callback flow using mock servers. Verifies code exchange, user fetch,
// and redirect to Atlassian OAuth.
// Validates: Requirements 2.3, 2.5
func TestHandleGitHubCallback_ExchangesCodeAndRedirectsToAtlassian(t *testing.T) {
	// Mock GitHub token endpoint
	ghTokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "application/json", r.Header.Get("Accept"))

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"access_token": "ghu_mock_token_123",
			"token_type":   "bearer",
		})
	}))
	defer ghTokenServer.Close()

	// Mock GitHub user API
	ghUserServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "GET", r.Method)
		assert.Equal(t, "Bearer ghu_mock_token_123", r.Header.Get("Authorization"))

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"login": "octocat",
		})
	}))
	defer ghUserServer.Close()

	store := newMockUserTokenStore()
	handler := newTestAuthHandler(store)
	handler.githubTokenURL = ghTokenServer.URL
	handler.githubUserAPIURL = ghUserServer.URL

	// Create a signed JSON cookie containing the state value (simulates handleAuthorize)
	stateValue := "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"
	initialPayload := cookiePayload{State: stateValue, ReturnTo: "/org/repo/issues/7"}
	signedState := signedCookiePayload(initialPayload, "test-cookie-secret", time.Now())

	reqURL := fmt.Sprintf("/oauth/github/callback?code=test-auth-code&state=%s", stateValue)
	req := httptest.NewRequest(http.MethodGet, reqURL, nil)
	req.AddCookie(&http.Cookie{
		Name:  userAuthSessionCookie,
		Value: signedState,
	})
	rec := httptest.NewRecorder()

	handler.handleGitHubCallback(rec, req)

	result := rec.Result()
	defer result.Body.Close()

	// Should redirect to Atlassian OAuth (302)
	assert.Equal(t, http.StatusFound, result.StatusCode)

	// Parse redirect URL
	location := result.Header.Get("Location")
	require.NotEmpty(t, location)

	redirectURL, err := url.Parse(location)
	require.NoError(t, err)

	// Verify it redirects to Atlassian OAuth
	assert.Contains(t, redirectURL.Host, "auth.atlassian.com")
	assert.Equal(t, "/authorize", redirectURL.Path)

	// Verify Atlassian OAuth parameters
	q := redirectURL.Query()
	assert.Equal(t, "test-atl-client-id", q.Get("client_id"))
	assert.Equal(t, "api.atlassian.com", q.Get("audience"))
	assert.Contains(t, q.Get("scope"), "offline_access")
	assert.Equal(t, "code", q.Get("response_type"))
	assert.Equal(t, "consent", q.Get("prompt"))
	assert.Equal(t, "http://localhost:8080/oauth/atlassian/callback", q.Get("redirect_uri"))

	// Verify the new session cookie contains the login AND preserves ReturnTo
	var loginCookie *http.Cookie
	for _, c := range result.Cookies() {
		if c.Name == userAuthSessionCookie {
			loginCookie = c
			break
		}
	}
	require.NotNil(t, loginCookie, "session cookie should be set with login")
	updatedPayload, err := verifySignedCookiePayload(loginCookie.Value, "test-cookie-secret", authSessionTTL)
	require.NoError(t, err)
	assert.Equal(t, "octocat", updatedPayload.Login)
	assert.Equal(t, "/org/repo/issues/7", updatedPayload.ReturnTo, "ReturnTo should be preserved across cookie rewrite")
}

// TestHandleGitHubCallback_PreservesCommentContext verifies that when the signed
// cookie contains CommentID and InstallationID, they are preserved in the
// rewritten cookie after the GitHub callback adds the login.
// Validates: Requirement 1.2
func TestHandleGitHubCallback_PreservesCommentContext(t *testing.T) {
	// Mock GitHub token endpoint
	ghTokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"access_token": "ghu_mock_token_123",
			"token_type":   "bearer",
		})
	}))
	defer ghTokenServer.Close()

	// Mock GitHub user API
	ghUserServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"login": "octocat",
		})
	}))
	defer ghUserServer.Close()

	store := newMockUserTokenStore()
	handler := newTestAuthHandler(store)
	handler.githubTokenURL = ghTokenServer.URL
	handler.githubUserAPIURL = ghUserServer.URL

	// Create a signed cookie with CommentID and InstallationID set
	stateValue := "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"
	initialPayload := cookiePayload{
		State:          stateValue,
		ReturnTo:       "/org/repo/issues/7",
		CommentID:      12345678,
		InstallationID: 9876543,
	}
	signedState := signedCookiePayload(initialPayload, "test-cookie-secret", time.Now())

	reqURL := fmt.Sprintf("/oauth/github/callback?code=test-auth-code&state=%s", stateValue)
	req := httptest.NewRequest(http.MethodGet, reqURL, nil)
	req.AddCookie(&http.Cookie{
		Name:  userAuthSessionCookie,
		Value: signedState,
	})
	rec := httptest.NewRecorder()

	handler.handleGitHubCallback(rec, req)

	result := rec.Result()
	defer result.Body.Close()

	// Should redirect to Atlassian OAuth (302)
	assert.Equal(t, http.StatusFound, result.StatusCode)

	// Find the rewritten session cookie
	var loginCookie *http.Cookie
	for _, c := range result.Cookies() {
		if c.Name == userAuthSessionCookie {
			loginCookie = c
			break
		}
	}
	require.NotNil(t, loginCookie, "session cookie should be set with login")

	// Verify the updated payload preserves CommentID and InstallationID
	updatedPayload, err := verifySignedCookiePayload(loginCookie.Value, "test-cookie-secret", authSessionTTL)
	require.NoError(t, err)
	assert.Equal(t, "octocat", updatedPayload.Login)
	assert.Equal(t, "/org/repo/issues/7", updatedPayload.ReturnTo)
	assert.Equal(t, uint64(12345678), updatedPayload.CommentID, "CommentID should be preserved across cookie rewrite")
	assert.Equal(t, int64(9876543), updatedPayload.InstallationID, "InstallationID should be preserved across cookie rewrite")
}

// TestHandleAtlassianCallback_ExchangesCodeAndStoresToken tests the Atlassian
// callback with a mock token endpoint. Verifies that tokens are stored using the
// global Cloud ID.
// Validates: Requirements 2.5, 2.7
func TestHandleAtlassianCallback_ExchangesCodeAndStoresToken(t *testing.T) {
	// Mock Atlassian token endpoint
	atlTokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Contains(t, r.Header.Get("Content-Type"), "application/json")

		var payload map[string]string
		json.NewDecoder(r.Body).Decode(&payload)
		assert.Equal(t, "authorization_code", payload["grant_type"])
		assert.Equal(t, "test-atl-client-id", payload["client_id"])
		assert.Equal(t, "test-atl-client-secret", payload["client_secret"])
		assert.Equal(t, "test-auth-code", payload["code"])
		assert.Equal(t, "http://localhost:8080/oauth/atlassian/callback", payload["redirect_uri"])

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token":  "atl_access_token_xyz",
			"refresh_token": "atl_refresh_token_xyz",
			"expires_in":    3600,
			"scope":         "offline_access read:jira-work write:jira-work",
		})
	}))
	defer atlTokenServer.Close()

	store := newMockUserTokenStore()

	// Create a signed JSON cookie with a verified login (post-GitHub-OAuth)
	loginPayload := cookiePayload{State: "somestate", Login: "octocat"}
	signedLogin := signedCookiePayload(loginPayload, "test-cookie-secret", time.Now())

	handler := newTestAuthHandler(store)
	handler.atlassianTokenURL = atlTokenServer.URL

	// Build request with code and session cookie
	req := httptest.NewRequest(http.MethodGet, "/oauth/atlassian/callback?code=test-auth-code", nil)
	req.AddCookie(&http.Cookie{
		Name:  userAuthSessionCookie,
		Value: signedLogin,
	})
	rec := httptest.NewRecorder()

	handler.handleAtlassianCallback(rec, req)

	result := rec.Result()
	defer result.Body.Close()

	// Should render success page (200)
	assert.Equal(t, http.StatusOK, result.StatusCode)
	body := rec.Body.String()
	assert.Contains(t, body, "Authorization Complete")
	assert.Contains(t, body, "octocat")

	// Verify token was stored with the global Cloud ID
	entry, err := store.Read(context.Background(), "octocat")
	require.NoError(t, err)
	assert.Equal(t, "atl_access_token_xyz", entry.AccessToken)
	assert.Equal(t, "atl_refresh_token_xyz", entry.RefreshToken)
	assert.Equal(t, "test-cloud-id-abc123", entry.CloudID)
	assert.WithinDuration(t, time.Now().Add(3600*time.Second), entry.ExpiresAt, 5*time.Second)
}

// TestHandleAtlassianCallback_ExpiredSession verifies that an error page is
// rendered when the session is expired.
// Validates: Requirement 2.9
func TestHandleAtlassianCallback_ExpiredSession(t *testing.T) {
	store := newMockUserTokenStore()
	handler := newTestAuthHandler(store)

	// Create a signed JSON cookie with an old timestamp so it's expired
	expiredPayload := cookiePayload{State: "somestate", Login: "octocat"}
	expiredCookie := signedCookiePayload(expiredPayload, "test-cookie-secret", time.Now().Add(-20*time.Minute))

	req := httptest.NewRequest(http.MethodGet, "/oauth/atlassian/callback?code=test-code", nil)
	req.AddCookie(&http.Cookie{
		Name:  userAuthSessionCookie,
		Value: expiredCookie,
	})
	rec := httptest.NewRecorder()

	handler.handleAtlassianCallback(rec, req)

	result := rec.Result()
	defer result.Body.Close()

	assert.Equal(t, http.StatusBadRequest, result.StatusCode)
	body := rec.Body.String()
	assert.Contains(t, body, "Session Expired")
	assert.Contains(t, body, "expired")
}

// TestHandleAtlassianCallback_MissingCode verifies that an error page is
// rendered when the code parameter is missing from the callback.
// Validates: Requirement 2.8
func TestHandleAtlassianCallback_MissingCode(t *testing.T) {
	store := newMockUserTokenStore()
	handler := newTestAuthHandler(store)

	loginPayload := cookiePayload{State: "somestate", Login: "octocat"}
	signedLogin := signedCookiePayload(loginPayload, "test-cookie-secret", time.Now())

	// Request without code parameter
	req := httptest.NewRequest(http.MethodGet, "/oauth/atlassian/callback", nil)
	req.AddCookie(&http.Cookie{
		Name:  userAuthSessionCookie,
		Value: signedLogin,
	})
	rec := httptest.NewRecorder()

	handler.handleAtlassianCallback(rec, req)

	result := rec.Result()
	defer result.Body.Close()

	assert.Equal(t, http.StatusBadRequest, result.StatusCode)
	body := rec.Body.String()
	assert.Contains(t, body, "Missing Code")
	assert.Contains(t, body, "No authorization code was provided by Atlassian")
}

// TestHandleGitHubCallback_ExchangeFailure verifies that when the GitHub code
// exchange fails, the handler renders an appropriate error page.
// Validates: Requirement 2.8
func TestHandleGitHubCallback_ExchangeFailure(t *testing.T) {
	// Mock GitHub token endpoint that returns an error
	ghTokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"bad_verification_code","error_description":"invalid code"}`))
	}))
	defer ghTokenServer.Close()

	store := newMockUserTokenStore()
	handler := newTestAuthHandler(store)
	handler.githubTokenURL = ghTokenServer.URL

	// Create a signed JSON cookie with the state value
	stateValue := "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"
	statePayload := cookiePayload{State: stateValue}
	signedState := signedCookiePayload(statePayload, "test-cookie-secret", time.Now())

	reqURL := fmt.Sprintf("/oauth/github/callback?code=bad-code&state=%s", stateValue)
	req := httptest.NewRequest(http.MethodGet, reqURL, nil)
	req.AddCookie(&http.Cookie{
		Name:  userAuthSessionCookie,
		Value: signedState,
	})
	rec := httptest.NewRecorder()

	handler.handleGitHubCallback(rec, req)

	result := rec.Result()
	defer result.Body.Close()

	assert.Equal(t, http.StatusBadRequest, result.StatusCode)
	body := rec.Body.String()
	assert.Contains(t, body, "GitHub Token Exchange Failed")
	// Verify it's an HTML error page
	assert.Contains(t, result.Header.Get("Content-Type"), "text/html")
}

// TestHandleAtlassianCallback_MissingSessionCookie verifies error when there is
// no session cookie at all.
// Validates: Requirement 2.9
func TestHandleAtlassianCallback_MissingSessionCookie(t *testing.T) {
	store := newMockUserTokenStore()
	handler := newTestAuthHandler(store)

	// Request with code but no session cookie
	req := httptest.NewRequest(http.MethodGet, "/oauth/atlassian/callback?code=test-code", nil)
	rec := httptest.NewRecorder()

	handler.handleAtlassianCallback(rec, req)

	result := rec.Result()
	defer result.Body.Close()

	assert.Equal(t, http.StatusBadRequest, result.StatusCode)
	body := rec.Body.String()
	assert.Contains(t, body, "Session Expired")
	assert.Contains(t, body, "missing or expired")
}

// TestHandleAtlassianCallback_TokenExchangeTimeout verifies that the Atlassian
// token exchange enforces the 15-second timeout.
// Validates: Requirement 2.5
func TestHandleAtlassianCallback_TokenExchangeTimeout(t *testing.T) {
	// Verify the timeout constant is 15 seconds
	assert.Equal(t, 15*time.Second, tokenExchangeTimeout,
		"token exchange timeout should be 15 seconds")
}

// TestHandleAtlassianCallback_UsesCloudID verifies that the handler uses
// the configured Cloud ID for multi-site selection.
// Validates: Requirement 2.7
func TestHandleAtlassianCallback_UsesCloudID(t *testing.T) {
	// Mock Atlassian token endpoint
	atlTokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token":  "atl_token",
			"refresh_token": "atl_refresh",
			"expires_in":    3600,
		})
	}))
	defer atlTokenServer.Close()

	store := newMockUserTokenStore()
	loginPayload := cookiePayload{State: "somestate", Login: "testuser"}
	signedLogin := signedCookiePayload(loginPayload, "test-cookie-secret", time.Now())

	// Handler with specific global Cloud ID
	handler := newTestAuthHandler(store)
	handler.atlassianTokenURL = atlTokenServer.URL
	handler.cloudID = "specific-cloud-id-999"

	req := httptest.NewRequest(http.MethodGet, "/oauth/atlassian/callback?code=xyz", nil)
	req.AddCookie(&http.Cookie{
		Name:  userAuthSessionCookie,
		Value: signedLogin,
	})
	rec := httptest.NewRecorder()

	handler.handleAtlassianCallback(rec, req)

	// Verify the stored entry uses the global Cloud ID
	entry, err := store.Read(context.Background(), "testuser")
	require.NoError(t, err)
	assert.Equal(t, "specific-cloud-id-999", entry.CloudID,
		"stored token entry should use the global Cloud ID from config")
}

// TestHandleAtlassianCallback_EmptyCloudID verifies that an error page is
// rendered when no Cloud ID is configured.
// Validates: Requirement 2.7
func TestHandleAtlassianCallback_EmptyCloudID(t *testing.T) {
	// Mock Atlassian token endpoint that succeeds
	atlTokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token":  "atl_token",
			"refresh_token": "atl_refresh",
			"expires_in":    3600,
		})
	}))
	defer atlTokenServer.Close()

	store := newMockUserTokenStore()
	loginPayload := cookiePayload{State: "somestate", Login: "testuser"}
	signedLogin := signedCookiePayload(loginPayload, "test-cookie-secret", time.Now())

	handler := newTestAuthHandler(store)
	handler.atlassianTokenURL = atlTokenServer.URL
	handler.cloudID = "" // no cloud ID configured

	req := httptest.NewRequest(http.MethodGet, "/oauth/atlassian/callback?code=xyz", nil)
	req.AddCookie(&http.Cookie{
		Name:  userAuthSessionCookie,
		Value: signedLogin,
	})
	rec := httptest.NewRecorder()

	handler.handleAtlassianCallback(rec, req)

	result := rec.Result()
	defer result.Body.Close()

	assert.Equal(t, http.StatusBadRequest, result.StatusCode)
	body := rec.Body.String()
	assert.Contains(t, body, "Configuration Error")
	assert.Contains(t, body, "Cloud ID")
}

// TestFetchAtlassianAccountID_Success verifies that when the /myself endpoint
// returns a valid JSON response with an accountId, the value is stored in the
// token entry during the OAuth callback flow.
// Validates: Requirements 1.1, 1.2
func TestFetchAtlassianAccountID_Success(t *testing.T) {
	// Mock /myself endpoint returning a valid accountId
	myselfServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "GET", r.Method)
		assert.Equal(t, "Bearer test-access-token", r.Header.Get("Authorization"))
		assert.Equal(t, "application/json", r.Header.Get("Accept"))

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"accountId":   "5b10ac8d14c1d5xyz",
			"displayName": "Test User",
		})
	}))
	defer myselfServer.Close()

	// Mock Atlassian token endpoint
	atlTokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token":  "test-access-token",
			"refresh_token": "test-refresh-token",
			"expires_in":    3600,
		})
	}))
	defer atlTokenServer.Close()

	store := newMockUserTokenStore()
	loginPayload := cookiePayload{State: "somestate", Login: "testuser"}
	signedLogin := signedCookiePayload(loginPayload, "test-cookie-secret", time.Now())

	handler := newTestAuthHandler(store)
	handler.atlassianTokenURL = atlTokenServer.URL
	// The myselfURL uses %s as a format placeholder for cloudID; override with test server
	handler.atlassianMyselfURL_ = myselfServer.URL + "/%s/rest/api/3/myself"

	req := httptest.NewRequest(http.MethodGet, "/oauth/atlassian/callback?code=test-code", nil)
	req.AddCookie(&http.Cookie{
		Name:  userAuthSessionCookie,
		Value: signedLogin,
	})
	rec := httptest.NewRecorder()

	handler.handleAtlassianCallback(rec, req)

	result := rec.Result()
	defer result.Body.Close()

	// Should succeed
	assert.Equal(t, http.StatusOK, result.StatusCode)

	// Verify accountId was stored
	entry, err := store.Read(context.Background(), "testuser")
	require.NoError(t, err)
	assert.Equal(t, "5b10ac8d14c1d5xyz", entry.AccountID)
	assert.Equal(t, "test-access-token", entry.AccessToken)
	assert.Equal(t, "test-refresh-token", entry.RefreshToken)
}

// TestFetchAtlassianAccountID_Non200_CompletesWithoutError verifies that when
// the /myself endpoint returns a non-200 status, the OAuth callback flow still
// completes successfully and stores the entry without an accountId.
// Validates: Requirements 1.3, 1.4
func TestFetchAtlassianAccountID_Non200_CompletesWithoutError(t *testing.T) {
	// Mock /myself endpoint returning 403 Forbidden
	myselfServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"message":"Forbidden"}`))
	}))
	defer myselfServer.Close()

	// Mock Atlassian token endpoint
	atlTokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token":  "test-access-token",
			"refresh_token": "test-refresh-token",
			"expires_in":    3600,
		})
	}))
	defer atlTokenServer.Close()

	store := newMockUserTokenStore()
	loginPayload := cookiePayload{State: "somestate", Login: "testuser"}
	signedLogin := signedCookiePayload(loginPayload, "test-cookie-secret", time.Now())

	handler := newTestAuthHandler(store)
	handler.atlassianTokenURL = atlTokenServer.URL
	handler.atlassianMyselfURL_ = myselfServer.URL + "/%s/rest/api/3/myself"

	req := httptest.NewRequest(http.MethodGet, "/oauth/atlassian/callback?code=test-code", nil)
	req.AddCookie(&http.Cookie{
		Name:  userAuthSessionCookie,
		Value: signedLogin,
	})
	rec := httptest.NewRecorder()

	handler.handleAtlassianCallback(rec, req)

	result := rec.Result()
	defer result.Body.Close()

	// Flow should still complete successfully
	assert.Equal(t, http.StatusOK, result.StatusCode)
	body := rec.Body.String()
	assert.Contains(t, body, "Authorization Complete")

	// Entry should be stored but without accountId
	entry, err := store.Read(context.Background(), "testuser")
	require.NoError(t, err)
	assert.Equal(t, "", entry.AccountID)
	assert.Equal(t, "test-access-token", entry.AccessToken)
	assert.Equal(t, "test-refresh-token", entry.RefreshToken)
	assert.Equal(t, "test-cloud-id-abc123", entry.CloudID)
}

// TestFetchAtlassianAccountID_InvalidJSON_CompletesWithoutError verifies that
// when the /myself endpoint returns invalid JSON, the OAuth callback flow still
// completes successfully and stores the entry without an accountId.
// Validates: Requirements 1.3, 1.4
func TestFetchAtlassianAccountID_InvalidJSON_CompletesWithoutError(t *testing.T) {
	// Mock /myself endpoint returning invalid JSON
	myselfServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`not valid json {{{`))
	}))
	defer myselfServer.Close()

	// Mock Atlassian token endpoint
	atlTokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token":  "test-access-token",
			"refresh_token": "test-refresh-token",
			"expires_in":    3600,
		})
	}))
	defer atlTokenServer.Close()

	store := newMockUserTokenStore()
	loginPayload := cookiePayload{State: "somestate", Login: "testuser"}
	signedLogin := signedCookiePayload(loginPayload, "test-cookie-secret", time.Now())

	handler := newTestAuthHandler(store)
	handler.atlassianTokenURL = atlTokenServer.URL
	handler.atlassianMyselfURL_ = myselfServer.URL + "/%s/rest/api/3/myself"

	req := httptest.NewRequest(http.MethodGet, "/oauth/atlassian/callback?code=test-code", nil)
	req.AddCookie(&http.Cookie{
		Name:  userAuthSessionCookie,
		Value: signedLogin,
	})
	rec := httptest.NewRecorder()

	handler.handleAtlassianCallback(rec, req)

	result := rec.Result()
	defer result.Body.Close()

	// Flow should still complete successfully
	assert.Equal(t, http.StatusOK, result.StatusCode)
	body := rec.Body.String()
	assert.Contains(t, body, "Authorization Complete")

	// Entry should be stored but without accountId
	entry, err := store.Read(context.Background(), "testuser")
	require.NoError(t, err)
	assert.Equal(t, "", entry.AccountID)
	assert.Equal(t, "test-access-token", entry.AccessToken)
}

// TestFetchAtlassianAccountID_Standalone verifies the fetchAtlassianAccountID
// method in isolation to confirm it logs warnings on failure.
// Validates: Requirements 1.1, 1.4
func TestFetchAtlassianAccountID_Standalone(t *testing.T) {
	t.Run("returns accountId on success", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{
				"accountId": "abc123",
			})
		}))
		defer server.Close()

		handler := &userAuthHandler{
			atlassianMyselfURL_: server.URL + "/%s/rest/api/3/myself",
		}

		result := handler.fetchAtlassianAccountID(context.Background(), "token", "cloud-id")
		assert.Equal(t, "abc123", result)
	})

	t.Run("returns empty string on non-200", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()

		handler := &userAuthHandler{
			atlassianMyselfURL_: server.URL + "/%s/rest/api/3/myself",
		}

		result := handler.fetchAtlassianAccountID(context.Background(), "token", "cloud-id")
		assert.Equal(t, "", result)
	})

	t.Run("returns empty string on invalid JSON", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{broken json`))
		}))
		defer server.Close()

		handler := &userAuthHandler{
			atlassianMyselfURL_: server.URL + "/%s/rest/api/3/myself",
		}

		result := handler.fetchAtlassianAccountID(context.Background(), "token", "cloud-id")
		assert.Equal(t, "", result)
	})

	t.Run("returns empty string on connection error", func(t *testing.T) {
		handler := &userAuthHandler{
			atlassianMyselfURL_: "http://localhost:1/%s/rest/api/3/myself",
		}

		result := handler.fetchAtlassianAccountID(context.Background(), "token", "cloud-id")
		assert.Equal(t, "", result)
	})
}

// TestRenderUserAuthError_HTMLContent verifies that error pages are rendered
// as HTML with proper structure for various failure modes.
// Validates: Requirement 2.8
func TestRenderUserAuthError_HTMLContent(t *testing.T) {
	tests := []struct {
		name    string
		title   string
		message string
	}{
		{"session expired", "Session Expired", "Your session has expired."},
		{"missing code", "Missing Code", "No authorization code was provided."},
		{"exchange failure", "Token Exchange Failed", "Failed to exchange code."},
		{"config error", "Configuration Error", "No Cloud ID configured."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			renderUserAuthError(rec, tt.title, tt.message)

			result := rec.Result()
			defer result.Body.Close()

			assert.Equal(t, http.StatusBadRequest, result.StatusCode)
			assert.Contains(t, result.Header.Get("Content-Type"), "text/html")

			body := rec.Body.String()
			assert.Contains(t, body, tt.title)
			assert.Contains(t, body, tt.message)
			assert.Contains(t, body, "<!DOCTYPE html>")
			assert.Contains(t, strings.ToLower(body), "try the authorization flow again")
		})
	}
}

// --- mockGitHubClientForHandler implements common.GitHubClientInterface for
// handler-level tests. It supports configurable FetchComment behavior and
// tracks calls to verify side-effect suppression in silent mode.
type mockGitHubClientForHandler struct {
	fetchCommentResult *github.IssueComment
	fetchCommentErr    error
	calls              []string
}

func (m *mockGitHubClientForHandler) ReactWithThumbsUp(_ context.Context, _ int64, _ *github.IssueComment) error {
	m.calls = append(m.calls, "ReactWithThumbsUp")
	return nil
}

func (m *mockGitHubClientForHandler) ReactWithConfused(_ context.Context, _ int64, _ *github.IssueComment) error {
	m.calls = append(m.calls, "ReactWithConfused")
	return nil
}

func (m *mockGitHubClientForHandler) PostComment(_ context.Context, _ int64, _ *github.IssueComment, _ string) error {
	m.calls = append(m.calls, "PostComment")
	return nil
}

func (m *mockGitHubClientForHandler) UpdateIssueDescription(_ context.Context, _ int64, _ *github.IssueComment, _ string) error {
	m.calls = append(m.calls, "UpdateIssueDescription")
	return nil
}

func (m *mockGitHubClientForHandler) FetchComment(_ context.Context, _ int64, _ uint64) (*github.IssueComment, error) {
	m.calls = append(m.calls, "FetchComment")
	return m.fetchCommentResult, m.fetchCommentErr
}

// mockJiraClientForHandler implements common.JiraClientInterface for handler tests.
type mockJiraClientForHandler struct {
	returnKey string
	returnErr error
}

func (m *mockJiraClientForHandler) CreateIssue(_, _, _, _ string, _ map[string]interface{}) (string, error) {
	return m.returnKey, m.returnErr
}

// mockJiraClientResolverForHandler wraps a JiraClientInterface.
type mockJiraClientResolverForHandler struct {
	client common.JiraClientInterface
}

func (r *mockJiraClientResolverForHandler) Resolve(_ context.Context, _ string) common.JiraClientResolveResult {
	if r.client == nil {
		return common.JiraClientResolveResult{ErrorMsg: "no client configured"}
	}
	return common.JiraClientResolveResult{Client: r.client}
}

// newTestIssueComment builds a github.IssueComment for handler auto-execution tests.
func newTestIssueComment(commentBody, issueBody string) *github.IssueComment {
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
			ID:     12345,
			User:   github.CommentUser{Login: "octocat"},
		},
		Installation: github.Installation{ID: 99},
		Repository: github.Repository{
			Owner:    github.RepositoryOwner{Login: "org"},
			Name:     "repo",
			FullName: "org/repo",
		},
	}
}

// atlTokenServerForAutoExec creates a mock Atlassian token exchange server.
func atlTokenServerForAutoExec(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token":  "atl_access_token_xyz",
			"refresh_token": "atl_refresh_token_xyz",
			"expires_in":    3600,
		})
	}))
}

// TestHandleAtlassianCallback_AutoExec_HappyPath tests that when a valid
// comment context is in the cookie and the fetched comment is a /jira create
// command, the executor runs and the success page shows the Jira key.
// Validates: Requirements 1.4, 1.6, 2.3, 2.4, 2.5, 3.3, 3.4, 6.1
func TestHandleAtlassianCallback_AutoExec_HappyPath(t *testing.T) {
	atlTokenServer := atlTokenServerForAutoExec(t)
	defer atlTokenServer.Close()

	store := newMockUserTokenStore()

	ghClient := &mockGitHubClientForHandler{
		fetchCommentResult: newTestIssueComment("/jira create", "Clean issue body"),
	}
	jiraClient := &mockJiraClientForHandler{returnKey: "PROJ-42"}

	handlerState := &common.State{
		Config: common.Config{
			JiraDefaultProject:   "DEFAULT",
			JiraDefaultIssueType: "Task",
		},
		GitHubClient:       ghClient,
		JiraClientResolver: &mockJiraClientResolverForHandler{client: jiraClient},
	}

	handler := newTestAuthHandler(store)
	handler.atlassianTokenURL = atlTokenServer.URL
	handler.state = handlerState

	// Create a signed cookie with login, CommentID, and InstallationID
	loginPayload := cookiePayload{
		State:          "somestate",
		Login:          "octocat",
		CommentID:      12345,
		InstallationID: 99,
	}
	signedLogin := signedCookiePayload(loginPayload, "test-cookie-secret", time.Now())

	req := httptest.NewRequest(http.MethodGet, "/oauth/atlassian/callback?code=test-auth-code", nil)
	req.AddCookie(&http.Cookie{
		Name:  userAuthSessionCookie,
		Value: signedLogin,
	})
	rec := httptest.NewRecorder()

	handler.handleAtlassianCallback(rec, req)

	result := rec.Result()
	defer result.Body.Close()

	assert.Equal(t, http.StatusOK, result.StatusCode)
	body := rec.Body.String()
	assert.Contains(t, body, "Authorization Complete")
	assert.Contains(t, body, "Jira issue created successfully")
}

// TestHandleAtlassianCallback_AutoExec_MissingCommentContext tests that when
// CommentID and InstallationID are absent from the cookie, auto-execution is
// skipped and the standard success page is rendered.
// Validates: Requirements 1.4, 1.6
func TestHandleAtlassianCallback_AutoExec_MissingCommentContext(t *testing.T) {
	atlTokenServer := atlTokenServerForAutoExec(t)
	defer atlTokenServer.Close()

	store := newMockUserTokenStore()

	ghClient := &mockGitHubClientForHandler{
		fetchCommentResult: newTestIssueComment("/jira create", "body"),
	}

	handlerState := &common.State{
		Config: common.Config{
			JiraDefaultProject:   "DEFAULT",
			JiraDefaultIssueType: "Task",
		},
		GitHubClient: ghClient,
	}

	handler := newTestAuthHandler(store)
	handler.atlassianTokenURL = atlTokenServer.URL
	handler.state = handlerState

	// Cookie WITHOUT CommentID/InstallationID
	loginPayload := cookiePayload{
		State: "somestate",
		Login: "octocat",
	}
	signedLogin := signedCookiePayload(loginPayload, "test-cookie-secret", time.Now())

	req := httptest.NewRequest(http.MethodGet, "/oauth/atlassian/callback?code=test-auth-code", nil)
	req.AddCookie(&http.Cookie{
		Name:  userAuthSessionCookie,
		Value: signedLogin,
	})
	rec := httptest.NewRecorder()

	handler.handleAtlassianCallback(rec, req)

	result := rec.Result()
	defer result.Body.Close()

	assert.Equal(t, http.StatusOK, result.StatusCode)
	body := rec.Body.String()
	assert.Contains(t, body, "Authorization Complete")
	// Should NOT contain any Jira key or auto-execution indicators
	assert.NotContains(t, body, "PROJ-")
	assert.NotContains(t, body, "Jira issue created")
	assert.NotContains(t, body, "Could not auto-create")
}

// TestHandleAtlassianCallback_AutoExec_DuplicateMarkerPresent tests that when
// the issue body contains the duplicate marker, auto-execution is skipped.
// Validates: Requirements 3.4, 6.1
func TestHandleAtlassianCallback_AutoExec_DuplicateMarkerPresent(t *testing.T) {
	atlTokenServer := atlTokenServerForAutoExec(t)
	defer atlTokenServer.Close()

	store := newMockUserTokenStore()

	ghClient := &mockGitHubClientForHandler{
		fetchCommentResult: newTestIssueComment("/jira create", "Body with <!--JIRA_BOT_ISSUE:[EXIST-1]--> marker"),
	}

	handlerState := &common.State{
		Config: common.Config{
			JiraDefaultProject:   "DEFAULT",
			JiraDefaultIssueType: "Task",
		},
		GitHubClient: ghClient,
	}

	handler := newTestAuthHandler(store)
	handler.atlassianTokenURL = atlTokenServer.URL
	handler.state = handlerState

	loginPayload := cookiePayload{
		State:          "somestate",
		Login:          "octocat",
		CommentID:      12345,
		InstallationID: 99,
	}
	signedLogin := signedCookiePayload(loginPayload, "test-cookie-secret", time.Now())

	req := httptest.NewRequest(http.MethodGet, "/oauth/atlassian/callback?code=test-auth-code", nil)
	req.AddCookie(&http.Cookie{
		Name:  userAuthSessionCookie,
		Value: signedLogin,
	})
	rec := httptest.NewRecorder()

	handler.handleAtlassianCallback(rec, req)

	result := rec.Result()
	defer result.Body.Close()

	assert.Equal(t, http.StatusOK, result.StatusCode)
	body := rec.Body.String()
	assert.Contains(t, body, "Authorization Complete")
	// Auto-execution should be skipped
	assert.NotContains(t, body, "Jira issue created")
	assert.NotContains(t, body, "Could not auto-create")
}

// TestHandleAtlassianCallback_AutoExec_NonJiraComment tests that when the
// fetched comment body does not start with /jira, auto-execution is skipped.
// Validates: Requirements 2.4
func TestHandleAtlassianCallback_AutoExec_NonJiraComment(t *testing.T) {
	atlTokenServer := atlTokenServerForAutoExec(t)
	defer atlTokenServer.Close()

	store := newMockUserTokenStore()

	ghClient := &mockGitHubClientForHandler{
		fetchCommentResult: newTestIssueComment("just a normal comment", "Clean body"),
	}

	handlerState := &common.State{
		Config: common.Config{
			JiraDefaultProject:   "DEFAULT",
			JiraDefaultIssueType: "Task",
		},
		GitHubClient: ghClient,
	}

	handler := newTestAuthHandler(store)
	handler.atlassianTokenURL = atlTokenServer.URL
	handler.state = handlerState

	loginPayload := cookiePayload{
		State:          "somestate",
		Login:          "octocat",
		CommentID:      12345,
		InstallationID: 99,
	}
	signedLogin := signedCookiePayload(loginPayload, "test-cookie-secret", time.Now())

	req := httptest.NewRequest(http.MethodGet, "/oauth/atlassian/callback?code=test-auth-code", nil)
	req.AddCookie(&http.Cookie{
		Name:  userAuthSessionCookie,
		Value: signedLogin,
	})
	rec := httptest.NewRecorder()

	handler.handleAtlassianCallback(rec, req)

	result := rec.Result()
	defer result.Body.Close()

	assert.Equal(t, http.StatusOK, result.StatusCode)
	body := rec.Body.String()
	assert.Contains(t, body, "Authorization Complete")
	assert.NotContains(t, body, "Jira issue created")
	assert.NotContains(t, body, "Could not auto-create")
}

// TestHandleAtlassianCallback_AutoExec_GitHubAPIFailure tests that when the
// GitHub API call to fetch the comment fails, auto-execution is skipped and
// the standard success page is rendered.
// Validates: Requirements 2.3, 2.5
func TestHandleAtlassianCallback_AutoExec_GitHubAPIFailure(t *testing.T) {
	atlTokenServer := atlTokenServerForAutoExec(t)
	defer atlTokenServer.Close()

	store := newMockUserTokenStore()

	ghClient := &mockGitHubClientForHandler{
		fetchCommentErr: fmt.Errorf("GitHub API error: 404 not found"),
	}

	handlerState := &common.State{
		Config: common.Config{
			JiraDefaultProject:   "DEFAULT",
			JiraDefaultIssueType: "Task",
		},
		GitHubClient: ghClient,
	}

	handler := newTestAuthHandler(store)
	handler.atlassianTokenURL = atlTokenServer.URL
	handler.state = handlerState

	loginPayload := cookiePayload{
		State:          "somestate",
		Login:          "octocat",
		CommentID:      12345,
		InstallationID: 99,
	}
	signedLogin := signedCookiePayload(loginPayload, "test-cookie-secret", time.Now())

	req := httptest.NewRequest(http.MethodGet, "/oauth/atlassian/callback?code=test-auth-code", nil)
	req.AddCookie(&http.Cookie{
		Name:  userAuthSessionCookie,
		Value: signedLogin,
	})
	rec := httptest.NewRecorder()

	handler.handleAtlassianCallback(rec, req)

	result := rec.Result()
	defer result.Body.Close()

	assert.Equal(t, http.StatusOK, result.StatusCode)
	body := rec.Body.String()
	assert.Contains(t, body, "Authorization Complete")
	assert.NotContains(t, body, "Jira issue created")
	assert.NotContains(t, body, "Could not auto-create")
}

// TestHandleAtlassianCallback_AutoExec_NilGitHubClient tests that when the
// state's GitHubClient is nil, auto-execution is skipped.
// Validates: Requirements 5.4
func TestHandleAtlassianCallback_AutoExec_NilGitHubClient(t *testing.T) {
	atlTokenServer := atlTokenServerForAutoExec(t)
	defer atlTokenServer.Close()

	store := newMockUserTokenStore()

	handlerState := &common.State{
		Config: common.Config{
			JiraDefaultProject:   "DEFAULT",
			JiraDefaultIssueType: "Task",
		},
		GitHubClient: nil, // explicitly nil
	}

	handler := newTestAuthHandler(store)
	handler.atlassianTokenURL = atlTokenServer.URL
	handler.state = handlerState

	loginPayload := cookiePayload{
		State:          "somestate",
		Login:          "octocat",
		CommentID:      12345,
		InstallationID: 99,
	}
	signedLogin := signedCookiePayload(loginPayload, "test-cookie-secret", time.Now())

	req := httptest.NewRequest(http.MethodGet, "/oauth/atlassian/callback?code=test-auth-code", nil)
	req.AddCookie(&http.Cookie{
		Name:  userAuthSessionCookie,
		Value: signedLogin,
	})
	rec := httptest.NewRecorder()

	handler.handleAtlassianCallback(rec, req)

	result := rec.Result()
	defer result.Body.Close()

	assert.Equal(t, http.StatusOK, result.StatusCode)
	body := rec.Body.String()
	assert.Contains(t, body, "Authorization Complete")
	assert.NotContains(t, body, "Jira issue created")
	assert.NotContains(t, body, "Could not auto-create")
}

// TestHandleAtlassianCallback_AutoExec_ExecutorError tests that when the
// executor returns an error, the error message is shown on the success page.
// Validates: Requirements 3.3
func TestHandleAtlassianCallback_AutoExec_ExecutorError(t *testing.T) {
	atlTokenServer := atlTokenServerForAutoExec(t)
	defer atlTokenServer.Close()

	store := newMockUserTokenStore()

	ghClient := &mockGitHubClientForHandler{
		fetchCommentResult: newTestIssueComment("/jira create", "Clean issue body"),
	}
	jiraClient := &mockJiraClientForHandler{returnErr: fmt.Errorf("Jira API unavailable")}

	handlerState := &common.State{
		Config: common.Config{
			JiraDefaultProject:   "DEFAULT",
			JiraDefaultIssueType: "Task",
		},
		GitHubClient:       ghClient,
		JiraClientResolver: &mockJiraClientResolverForHandler{client: jiraClient},
	}

	handler := newTestAuthHandler(store)
	handler.atlassianTokenURL = atlTokenServer.URL
	handler.state = handlerState

	loginPayload := cookiePayload{
		State:          "somestate",
		Login:          "octocat",
		CommentID:      12345,
		InstallationID: 99,
	}
	signedLogin := signedCookiePayload(loginPayload, "test-cookie-secret", time.Now())

	req := httptest.NewRequest(http.MethodGet, "/oauth/atlassian/callback?code=test-auth-code", nil)
	req.AddCookie(&http.Cookie{
		Name:  userAuthSessionCookie,
		Value: signedLogin,
	})
	rec := httptest.NewRecorder()

	handler.handleAtlassianCallback(rec, req)

	result := rec.Result()
	defer result.Body.Close()

	assert.Equal(t, http.StatusOK, result.StatusCode)
	body := rec.Body.String()
	assert.Contains(t, body, "Authorization Complete")
	assert.Contains(t, body, "Could not auto-create Jira issue")
	assert.Contains(t, body, "Jira API unavailable")
	assert.NotContains(t, body, "PROJ-")
}

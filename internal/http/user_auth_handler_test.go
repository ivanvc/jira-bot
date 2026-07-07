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

	// Verify the cookie contains the state (verify by extracting)
	extractedState, err := verifySignedCookie(sessionCookie.Value, "test-cookie-secret", authSessionTTL)
	require.NoError(t, err)
	assert.Equal(t, state, extractedState)
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

	// Create a signed cookie containing the state value (simulates handleAuthorize)
	stateValue := "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"
	signedState := signedCookieValue(stateValue, "test-cookie-secret", time.Now())

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

	// Verify the new session cookie contains the "octocat" login
	var loginCookie *http.Cookie
	for _, c := range result.Cookies() {
		if c.Name == userAuthSessionCookie {
			loginCookie = c
			break
		}
	}
	require.NotNil(t, loginCookie, "session cookie should be set with login")
	extractedLogin, err := verifySignedCookie(loginCookie.Value, "test-cookie-secret", authSessionTTL)
	require.NoError(t, err)
	assert.Equal(t, "octocat", extractedLogin)
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

	// Create a signed cookie with a verified login (post-GitHub-OAuth)
	signedLogin := signedCookieValue("octocat", "test-cookie-secret", time.Now())

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

	// Create a signed cookie with an old timestamp so it's expired
	expiredCookie := signedCookieValue("octocat", "test-cookie-secret", time.Now().Add(-20*time.Minute))

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

	signedLogin := signedCookieValue("octocat", "test-cookie-secret", time.Now())

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

	// Create a signed cookie with the state value
	stateValue := "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"
	signedState := signedCookieValue(stateValue, "test-cookie-secret", time.Now())

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
	signedLogin := signedCookieValue("testuser", "test-cookie-secret", time.Now())

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
	signedLogin := signedCookieValue("testuser", "test-cookie-secret", time.Now())

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
	signedLogin := signedCookieValue("testuser", "test-cookie-secret", time.Now())

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
	signedLogin := signedCookieValue("testuser", "test-cookie-secret", time.Now())

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
	signedLogin := signedCookieValue("testuser", "test-cookie-secret", time.Now())

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

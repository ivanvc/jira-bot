package http

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/charmbracelet/log"
	"github.com/ivanvc/jira-bot/internal/common"
)

// randRead is a variable for testing (allows deterministic state generation in tests).
var randRead = rand.Read

const (
	userAuthSessionCookie = "jira_user_auth_session"
	tokenExchangeTimeout  = 15 * time.Second
	atlassianTokenURL     = "https://auth.atlassian.com/oauth/token"
	atlassianAuthorizeURL = "https://auth.atlassian.com/authorize"
	atlassianMyselfURL    = "https://api.atlassian.com/ex/jira/%s/rest/api/3/myself"
	githubTokenURL        = "https://github.com/login/oauth/access_token"
	githubUserAPIURL      = "https://api.github.com/user"
	githubAuthorizeURL    = "https://github.com/login/oauth/authorize"
)

// userAuthHandler implements the two-step OAuth authorization flow:
// 1. GitHub OAuth to verify the user's identity
// 2. Atlassian OAuth to obtain Jira tokens
type userAuthHandler struct {
	githubAppClientID     string
	githubAppClientSecret string
	atlClientID           string
	atlClientSecret       string
	atlCallbackURL        string
	cloudID               string
	store                 common.UserTokenStore
	cookieSecret          string // HMAC secret for signed cookies (uses githubAppClientSecret)

	// URL overrides for testing (empty means use package-level constants)
	githubAuthorizeURL  string
	githubTokenURL      string
	githubUserAPIURL    string
	atlassianTokenURL   string
	atlassianAuthURL    string
	atlassianMyselfURL_ string
}

func (h *userAuthHandler) getGitHubAuthorizeURL() string {
	if h.githubAuthorizeURL != "" {
		return h.githubAuthorizeURL
	}
	return githubAuthorizeURL
}

func (h *userAuthHandler) getGitHubTokenURL() string {
	if h.githubTokenURL != "" {
		return h.githubTokenURL
	}
	return githubTokenURL
}

func (h *userAuthHandler) getGitHubUserAPIURL() string {
	if h.githubUserAPIURL != "" {
		return h.githubUserAPIURL
	}
	return githubUserAPIURL
}

func (h *userAuthHandler) getAtlassianTokenURL() string {
	if h.atlassianTokenURL != "" {
		return h.atlassianTokenURL
	}
	return atlassianTokenURL
}

func (h *userAuthHandler) getAtlassianAuthURL() string {
	if h.atlassianAuthURL != "" {
		return h.atlassianAuthURL
	}
	return atlassianAuthorizeURL
}

func (h *userAuthHandler) getAtlassianMyselfURL() string {
	if h.atlassianMyselfURL_ != "" {
		return h.atlassianMyselfURL_
	}
	return atlassianMyselfURL
}

// fetchAtlassianAccountID calls the /myself endpoint to retrieve the user's accountId.
// Returns empty string on any failure (non-fatal).
func (h *userAuthHandler) fetchAtlassianAccountID(ctx context.Context, accessToken, cloudID string) string {
	myselfURL := fmt.Sprintf(h.getAtlassianMyselfURL(), cloudID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, myselfURL, nil)
	if err != nil {
		log.Warn("Failed to create /myself request", "error", err)
		return ""
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: tokenExchangeTimeout}
	resp, err := client.Do(req)
	if err != nil {
		log.Warn("Failed to call /myself endpoint", "error", err)
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Warn("Atlassian /myself returned non-200", "status", resp.StatusCode)
		return ""
	}

	var result struct {
		AccountID string `json:"accountId"`
	}
	body, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(body, &result); err != nil {
		log.Warn("Failed to parse /myself response", "error", err)
		return ""
	}

	return result.AccountID
}

// handleAuthorize initiates the GitHub OAuth flow by redirecting the user to
// GitHub's authorization endpoint. It uses a random state parameter for CSRF protection.
// Endpoint: GET /oauth/authorize
func (h *userAuthHandler) handleAuthorize(w http.ResponseWriter, req *http.Request) {
	// Generate a random state for CSRF protection
	b := make([]byte, 32)
	if _, err := randRead(b); err != nil {
		log.Error("Failed to generate state", "error", err)
		renderUserAuthError(w, "Internal Error", "Failed to initiate authorization flow. Please try again.")
		return
	}
	state := hex.EncodeToString(b)

	// Store the state in a signed cookie so we can verify it in the callback
	setSignedCookie(w, userAuthSessionCookie, state, "/oauth/", h.cookieSecret)

	// Redirect to GitHub OAuth
	params := url.Values{
		"client_id": {h.githubAppClientID},
		"state":     {state},
	}
	redirectURL := h.getGitHubAuthorizeURL() + "?" + params.Encode()
	http.Redirect(w, req, redirectURL, http.StatusFound)
}

// handleGitHubCallback handles the GitHub OAuth callback. It exchanges the
// authorization code for a user access token, fetches the user's GitHub login,
// stores it in a signed cookie, and redirects to Atlassian OAuth consent.
// Endpoint: GET /oauth/github/callback
func (h *userAuthHandler) handleGitHubCallback(w http.ResponseWriter, req *http.Request) {
	code := req.URL.Query().Get("code")
	if code == "" {
		errMsg := req.URL.Query().Get("error")
		errDesc := req.URL.Query().Get("error_description")
		if errMsg != "" {
			log.Error("GitHub OAuth denied", "error", errMsg, "description", errDesc)
			renderUserAuthError(w, "GitHub Authorization Failed", fmt.Sprintf("GitHub denied the authorization request: %s", errDesc))
			return
		}
		renderUserAuthError(w, "Missing Code", "No authorization code was provided by GitHub.")
		return
	}

	stateParam := req.URL.Query().Get("state")
	if stateParam == "" {
		renderUserAuthError(w, "Invalid Request", "Missing state parameter.")
		return
	}

	// Verify the state matches the signed cookie (CSRF protection)
	savedState, err := getSignedCookie(req, userAuthSessionCookie, h.cookieSecret, authSessionTTL)
	if err != nil || savedState != stateParam {
		renderUserAuthError(w, "Invalid State", "The state parameter does not match. Please restart the authorization flow.")
		return
	}

	// Exchange code for GitHub user access token
	ghToken, err := h.exchangeGitHubCode(code)
	if err != nil {
		log.Error("GitHub token exchange failed", "error", err)
		renderUserAuthError(w, "GitHub Token Exchange Failed", "Failed to exchange authorization code for access token. Please try again.")
		return
	}

	// Fetch GitHub user login
	login, err := h.fetchGitHubUser(ghToken)
	if err != nil {
		log.Error("Failed to fetch GitHub user", "error", err)
		renderUserAuthError(w, "GitHub API Error", "Failed to retrieve your GitHub identity. Please try again.")
		return
	}

	// Store the login in a signed cookie (any pod can read this)
	setSignedCookie(w, userAuthSessionCookie, login, "/oauth/", h.cookieSecret)

	// Redirect to Atlassian OAuth consent
	params := url.Values{
		"audience":      {"api.atlassian.com"},
		"client_id":     {h.atlClientID},
		"scope":         {"offline_access read:jira-work write:jira-work read:jira-user"},
		"redirect_uri":  {h.atlCallbackURL},
		"state":         {login}, // state is just for logging/debugging; not security-critical here
		"response_type": {"code"},
		"prompt":        {"consent"},
	}
	redirectURL := h.getAtlassianAuthURL() + "?" + params.Encode()
	http.Redirect(w, req, redirectURL, http.StatusFound)
}

// handleAtlassianCallback handles the Atlassian OAuth callback. It resolves the
// user login from the signed cookie, exchanges the code for Atlassian tokens,
// uses the configured Cloud ID, and writes the token entry to the store.
// Endpoint: GET /oauth/atlassian/callback
func (h *userAuthHandler) handleAtlassianCallback(w http.ResponseWriter, req *http.Request) {
	code := req.URL.Query().Get("code")
	if code == "" {
		errMsg := req.URL.Query().Get("error")
		errDesc := req.URL.Query().Get("error_description")
		if errMsg != "" {
			log.Error("Atlassian OAuth denied", "error", errMsg, "description", errDesc)
			renderUserAuthError(w, "Atlassian Authorization Failed", fmt.Sprintf("Atlassian denied the authorization request: %s", errDesc))
			return
		}
		renderUserAuthError(w, "Missing Code", "No authorization code was provided by Atlassian.")
		return
	}

	// Resolve login from signed cookie
	login, err := getSignedCookie(req, userAuthSessionCookie, h.cookieSecret, authSessionTTL)
	if err != nil {
		renderUserAuthError(w, "Session Expired", "Your authorization session is missing or expired. Please restart the authorization flow.")
		return
	}

	if login == "" {
		renderUserAuthError(w, "Invalid Session", "Session does not contain a valid identity. Please restart the authorization flow.")
		return
	}

	// Exchange code for Atlassian tokens
	tokenResp, err := h.exchangeAtlassianCode(code)
	if err != nil {
		log.Error("Atlassian token exchange failed", "error", err, "login", login)
		renderUserAuthError(w, "Token Exchange Failed", "Failed to exchange authorization code for Atlassian tokens. Please try again.")
		return
	}

	if tokenResp.RefreshToken == "" {
		renderUserAuthError(w, "Missing Refresh Token", "Atlassian did not return a refresh token. Ensure 'offline_access' scope is included.")
		return
	}

	// Use the configured Cloud ID
	cloudID := h.cloudID
	if cloudID == "" {
		renderUserAuthError(w, "Configuration Error", "No Cloud ID is configured. Please contact the bot administrator.")
		return
	}

	ctx, cancel := context.WithTimeout(req.Context(), tokenExchangeTimeout)
	defer cancel()

	// Fetch the user's Atlassian accountId (non-fatal on failure)
	accountID := h.fetchAtlassianAccountID(ctx, tokenResp.AccessToken, cloudID)

	// Write token entry to store
	entry := common.UserTokenEntry{
		RefreshToken: tokenResp.RefreshToken,
		AccessToken:  tokenResp.AccessToken,
		ExpiresAt:    time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second),
		CloudID:      cloudID,
		AccountID:    accountID,
	}

	if err := h.store.Write(ctx, login, entry); err != nil {
		log.Error("Failed to write user token entry", "error", err, "login", login)
		renderUserAuthError(w, "Storage Error", "Failed to save your tokens. Please try again.")
		return
	}

	// Clear the session cookie
	http.SetCookie(w, &http.Cookie{
		Name:     userAuthSessionCookie,
		Value:    "",
		Path:     "/oauth/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})

	log.Info("User authorization complete", "login", login, "cloud_id", cloudID)
	renderUserAuthSuccess(w, login)
}

// exchangeGitHubCode exchanges a GitHub OAuth authorization code for a user
// access token using the GitHub App's client credentials.
func (h *userAuthHandler) exchangeGitHubCode(code string) (string, error) {
	params := url.Values{
		"client_id":     {h.githubAppClientID},
		"client_secret": {h.githubAppClientSecret},
		"code":          {code},
	}

	client := &http.Client{Timeout: tokenExchangeTimeout}
	req, err := http.NewRequest("POST", h.getGitHubTokenURL(), bytes.NewBufferString(params.Encode()))
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("GitHub token exchange request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub token exchange failed (HTTP %d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		Error       string `json:"error"`
		ErrorDesc   string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("parsing token response: %w", err)
	}

	if result.Error != "" {
		return "", fmt.Errorf("GitHub OAuth error: %s — %s", result.Error, result.ErrorDesc)
	}

	if result.AccessToken == "" {
		return "", fmt.Errorf("GitHub returned empty access token")
	}

	return result.AccessToken, nil
}

// fetchGitHubUser calls the GitHub /user API with the given access token and
// returns the user's login.
func (h *userAuthHandler) fetchGitHubUser(accessToken string) (string, error) {
	client := &http.Client{Timeout: tokenExchangeTimeout}

	req, err := http.NewRequest("GET", h.getGitHubUserAPIURL(), nil)
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("GitHub user API request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub user API returned HTTP %d: %s", resp.StatusCode, string(body))
	}

	var user struct {
		Login string `json:"login"`
	}
	if err := json.Unmarshal(body, &user); err != nil {
		return "", fmt.Errorf("parsing user response: %w", err)
	}

	if user.Login == "" {
		return "", fmt.Errorf("GitHub user API returned empty login")
	}

	return user.Login, nil
}

// atlassianTokenExchangeResponse holds the response from the Atlassian token endpoint.
type atlassianTokenExchangeResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
}

// exchangeAtlassianCode exchanges an Atlassian OAuth authorization code for
// access and refresh tokens.
func (h *userAuthHandler) exchangeAtlassianCode(code string) (*atlassianTokenExchangeResponse, error) {
	payload := map[string]string{
		"grant_type":    "authorization_code",
		"client_id":     h.atlClientID,
		"client_secret": h.atlClientSecret,
		"code":          code,
		"redirect_uri":  h.atlCallbackURL,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}

	client := &http.Client{Timeout: tokenExchangeTimeout}
	resp, err := client.Post(h.getAtlassianTokenURL(), "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("Atlassian token exchange request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Atlassian token exchange failed (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	var tokenResp atlassianTokenExchangeResponse
	if err := json.Unmarshal(respBody, &tokenResp); err != nil {
		return nil, fmt.Errorf("parsing token response: %w", err)
	}

	return &tokenResp, nil
}

// renderUserAuthError renders an error page for the user authorization flow.
func renderUserAuthError(w http.ResponseWriter, title, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)
	fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head><title>Jira Bot — %s</title></head>
<body style="font-family: system-ui, sans-serif; max-width: 600px; margin: 40px auto; padding: 0 20px;">
<h1 style="color: #d32f2f;">&#9888; %s</h1>
<p>%s</p>
<p style="background: #fff3e0; padding: 12px; border-radius: 4px;">
You can close this page and try the authorization flow again from your GitHub issue or PR.
</p>
</body>
</html>`, title, title, message)
}

// renderUserAuthSuccess renders the success page after completing the user
// authorization flow.
func renderUserAuthSuccess(w http.ResponseWriter, login string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head><title>Jira Bot — Authorization Complete</title></head>
<body style="font-family: system-ui, sans-serif; max-width: 600px; margin: 40px auto; padding: 0 20px;">
<h1 style="color: #2e7d32;">&#10004; Authorization Complete</h1>
<p>Your Atlassian account has been linked to your GitHub identity (<strong>%s</strong>).</p>
<p style="background: #e8f5e9; padding: 12px; border-radius: 4px;">
You can now close this page and return to your GitHub issue or PR to run the <code>/jira create</code> command again.
</p>
</body>
</html>`, login)
}

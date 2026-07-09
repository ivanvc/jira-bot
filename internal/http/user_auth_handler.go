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
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/log"
	gogithub "github.com/google/go-github/v58/github"
	"github.com/ivanvc/jira-bot/internal/common"
	"github.com/ivanvc/jira-bot/internal/executor"
)

// authPendingMarker is the HTML comment embedded in the auth link comment
// so the callback handler can find it later for in-place editing.
const authPendingMarker = "<!--JIRA_BOT_AUTH_PENDING-->"

// findAuthPendingComment searches a slice of GitHub issue comments for one
// whose body contains the auth pending marker. Returns the comment ID if found,
// or 0 if not found.
func findAuthPendingComment(comments []*gogithub.IssueComment) int64 {
	for _, c := range comments {
		if strings.Contains(c.GetBody(), authPendingMarker) {
			return c.GetID()
		}
	}
	return 0
}

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
	cookieSecret          string        // HMAC secret for signed cookies (uses githubAppClientSecret)
	githubRedirectBaseURL string        // Base URL prepended to stored path for post-OAuth redirect
	redirectDelaySec      int           // Seconds before auto-redirect on success page
	state                 *common.State // Full application state for auto-execution in callback

	// URL overrides for testing (empty means use package-level constants)
	githubAuthorizeURL  string
	githubTokenURL      string
	githubUserAPIURL    string
	atlassianTokenURL   string
	atlassianAuthURL    string
	atlassianMyselfURL_ string
}

// autoExecResult holds the outcome of auto-executing the original /jira command
// after the OAuth flow completes.
type autoExecResult struct {
	Attempted bool   // whether auto-execution was attempted
	Success   bool   // whether execution succeeded
	Error     string // error message on failure
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

	// Read optional return_to and installation_id query parameters.
	returnTo := req.URL.Query().Get("return_to")

	var installationID int64
	if raw := req.URL.Query().Get("installation_id"); raw != "" {
		installationID, _ = strconv.ParseInt(raw, 10, 64)
	}

	// Store state and return_to in a signed JSON cookie
	payload := cookiePayload{State: state, ReturnTo: returnTo, InstallationID: installationID}
	signed := signedCookiePayload(payload, h.cookieSecret, time.Now())
	http.SetCookie(w, &http.Cookie{
		Name:     userAuthSessionCookie,
		Value:    signed,
		Path:     "/oauth/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})

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
	cookie, err := req.Cookie(userAuthSessionCookie)
	if err != nil {
		renderUserAuthError(w, "Invalid State", "The state parameter does not match. Please restart the authorization flow.")
		return
	}
	existingPayload, err := verifySignedCookiePayload(cookie.Value, h.cookieSecret, authSessionTTL)
	if err != nil || existingPayload.State != stateParam {
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

	// Store the login in a signed cookie, preserving ReturnTo and InstallationID from the original payload
	updatedPayload := cookiePayload{
		State:          stateParam,
		Login:          login,
		ReturnTo:       existingPayload.ReturnTo,
		InstallationID: existingPayload.InstallationID,
	}
	signed := signedCookiePayload(updatedPayload, h.cookieSecret, time.Now())
	http.SetCookie(w, &http.Cookie{
		Name:     userAuthSessionCookie,
		Value:    signed,
		Path:     "/oauth/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})

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

	// Resolve login and return_to from signed cookie payload
	cookie, err := req.Cookie(userAuthSessionCookie)
	if err != nil {
		renderUserAuthError(w, "Session Expired", "Your authorization session is missing or expired. Please restart the authorization flow.")
		return
	}
	payload, err := verifySignedCookiePayload(cookie.Value, h.cookieSecret, authSessionTTL)
	if err != nil {
		renderUserAuthError(w, "Session Expired", "Your authorization session is missing or expired. Please restart the authorization flow.")
		return
	}

	login := payload.Login
	returnTo := payload.ReturnTo

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

	// Auto-execution: derive owner, repo, commentID from ReturnTo path.
	installationID := payload.InstallationID

	if installationID == 0 {
		renderUserAuthSuccess(w, login, returnTo, h.githubRedirectBaseURL, h.redirectDelaySec, nil)
		return
	}

	rtInfo, err := parseReturnTo(payload.ReturnTo)
	if err != nil {
		log.Warn("Auto-execution skipped: cannot parse ReturnTo", "error", err, "login", login, "return_to", payload.ReturnTo)
		renderUserAuthSuccess(w, login, returnTo, h.githubRedirectBaseURL, h.redirectDelaySec, nil)
		return
	}

	owner := rtInfo.Owner
	repo := rtInfo.Repo
	commentID := rtInfo.CommentID

	if commentID == 0 {
		renderUserAuthSuccess(w, login, returnTo, h.githubRedirectBaseURL, h.redirectDelaySec, nil)
		return
	}

	// Need the GitHubClient to fetch the comment
	if h.state == nil || h.state.GitHubClient == nil {
		log.Warn("Auto-execution skipped: GitHubClient is nil", "login", login, "comment_id", commentID)
		renderUserAuthSuccess(w, login, returnTo, h.githubRedirectBaseURL, h.redirectDelaySec, nil)
		return
	}

	// Fetch the original comment from GitHub API with a 15-second timeout
	fetchCtx, fetchCancel := context.WithTimeout(req.Context(), 15*time.Second)
	defer fetchCancel()

	issueComment, err := h.state.GitHubClient.FetchComment(fetchCtx, installationID, owner, repo, commentID)
	if err != nil {
		log.Error("Auto-execution skipped: failed to fetch comment", "error", err, "login", login, "comment_id", commentID)
		renderUserAuthSuccess(w, login, returnTo, h.githubRedirectBaseURL, h.redirectDelaySec, nil)
		return
	}
	// Check that the comment body starts with /jira
	if !strings.HasPrefix(issueComment.Comment.Body, "/jira") {
		log.Info("Auto-execution skipped: comment does not start with /jira", "login", login, "comment_id", commentID)
		renderUserAuthSuccess(w, login, returnTo, h.githubRedirectBaseURL, h.redirectDelaySec, nil)
		return
	}

	// Check for duplicate marker in the issue body
	if strings.Contains(issueComment.Issue.Body, "<!--JIRA_BOT_ISSUE") {
		log.Info("Auto-execution skipped: duplicate marker present", "login", login, "comment_id", commentID)
		renderUserAuthSuccess(w, login, returnTo, h.githubRedirectBaseURL, h.redirectDelaySec, nil)
		return
	}

	// Invoke executor with a 30-second timeout
	execCtx, execCancel := context.WithTimeout(req.Context(), 30*time.Second)
	defer execCancel()

	// Search for the auth pending marker comment so we can edit it in-place
	var editCommentID int64
	issueNumber, parseErr := parseIssueNumberFromCommentsURL(issueComment.Issue.CommentsURL)
	if parseErr != nil {
		log.Warn("Could not parse issue number from CommentsURL, will post new comment", "error", parseErr)
	} else {
		comments, listErr := h.state.GitHubClient.ListIssueComments(execCtx, installationID, owner, repo, issueNumber)
		if listErr != nil {
			log.Warn("Could not list issue comments, will post new comment", "error", listErr)
		} else {
			editCommentID = findAuthPendingComment(comments)
		}
	}

	autoExec := &autoExecResult{
		Attempted: true,
	}
	if editCommentID != 0 {
		if err := executor.Run(execCtx, h.state, issueComment, editCommentID); err != nil {
			log.Error("Auto-execution failed", "error", err, "login", login, "comment_id", commentID)
			autoExec.Error = err.Error()
		} else {
			log.Info("Auto-execution succeeded", "login", login, "comment_id", commentID)
			autoExec.Success = true
		}
	} else {
		if err := executor.Run(execCtx, h.state, issueComment); err != nil {
			log.Error("Auto-execution failed", "error", err, "login", login, "comment_id", commentID)
			autoExec.Error = err.Error()
		} else {
			log.Info("Auto-execution succeeded", "login", login, "comment_id", commentID)
			autoExec.Success = true
		}
	}

	renderUserAuthSuccess(w, login, returnTo, h.githubRedirectBaseURL, h.redirectDelaySec, autoExec)
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

// returnToInfo holds the parsed components of a ReturnTo path.
type returnToInfo struct {
	Owner     string
	Repo      string
	CommentID uint64
}

// parseReturnTo extracts owner, repo, and commentID from a ReturnTo path.
// Expected formats:
//
//	/{owner}/{repo}/issues/{number}#issuecomment-{id}
//	/{owner}/{repo}/pull/{number}#issuecomment-{id}
//	/{owner}/{repo}/issues/{number}
//	/{owner}/{repo}/pull/{number}
//
// Returns an error if the path has fewer than 2 non-empty segments or if
// owner/repo segments are empty.
func parseReturnTo(returnTo string) (returnToInfo, error) {
	// Separate the fragment from the path
	path := returnTo
	fragment := ""
	if idx := strings.IndexByte(returnTo, '#'); idx >= 0 {
		path = returnTo[:idx]
		fragment = returnTo[idx+1:]
	}

	// Split path on "/" and collect non-empty segments
	parts := strings.Split(path, "/")
	var segments []string
	for _, p := range parts {
		if p != "" {
			segments = append(segments, p)
		}
	}

	if len(segments) < 2 {
		return returnToInfo{}, fmt.Errorf("returnTo path has fewer than 2 non-empty segments: %q", returnTo)
	}

	owner := segments[0]
	repo := segments[1]

	// owner and repo should already be non-empty given the segment collection above,
	// but guard against the case of consecutive slashes producing empty strings
	// (this can't happen with the filter above, but kept for explicitness per req 3.6).
	if owner == "" || repo == "" {
		return returnToInfo{}, fmt.Errorf("returnTo path has empty owner or repo: %q", returnTo)
	}

	// Parse the fragment for #issuecomment-{id}
	var commentID uint64
	const commentPrefix = "issuecomment-"
	if strings.HasPrefix(fragment, commentPrefix) {
		idStr := fragment[len(commentPrefix):]
		// Only parse if idStr is all digits
		if len(idStr) > 0 {
			if id, err := strconv.ParseUint(idStr, 10, 64); err == nil {
				commentID = id
			}
		}
	}

	return returnToInfo{Owner: owner, Repo: repo, CommentID: commentID}, nil
}

// parseIssueNumberFromCommentsURL extracts the issue number from a GitHub API
// comments URL of the form: https://api.github.com/repos/{owner}/{repo}/issues/{number}/comments
func parseIssueNumberFromCommentsURL(commentsURL string) (int, error) {
	// Split on "/" and find the segment before "comments"
	parts := strings.Split(commentsURL, "/")
	for i, p := range parts {
		if p == "comments" && i > 0 {
			return strconv.Atoi(parts[i-1])
		}
	}
	return 0, fmt.Errorf("cannot parse issue number from URL: %s", commentsURL)
}

// renderUserAuthSuccess renders the success page after completing the user
// authorization flow. When returnTo starts with "/", it renders a meta-refresh
// redirect to githubBaseURL+returnTo. Otherwise it renders a generic success message.
// The autoExec parameter (nil means not attempted) adds Jira issue creation
// results to the page when auto-execution was performed.
func renderUserAuthSuccess(w http.ResponseWriter, login, returnTo, githubBaseURL string, delaySec int, autoExec *autoExecResult) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	if delaySec <= 0 {
		delaySec = 3
	}

	// Build auto-execution result HTML snippet
	var autoExecHTML string
	if autoExec != nil && autoExec.Attempted {
		if autoExec.Success {
			autoExecHTML = `<p style="background: #e3f2fd; padding: 12px; border-radius: 4px;">
&#127881; Jira issue created successfully. Check the GitHub issue for details.
</p>`
		} else if autoExec.Error != "" {
			autoExecHTML = fmt.Sprintf(`<p style="background: #fff3e0; padding: 12px; border-radius: 4px;">
&#9888; Could not auto-create Jira issue: %s<br>
Please return to your GitHub issue and retry the command manually.
</p>`, autoExec.Error)
		}
	}

	if strings.HasPrefix(returnTo, "/") {
		redirectURL := githubBaseURL + returnTo
		fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head>
<title>Jira Bot — Authorization Complete</title>
<meta http-equiv="refresh" content="%d;url=%s">
</head>
<body style="font-family: system-ui, sans-serif; max-width: 600px; margin: 40px auto; padding: 0 20px;">
<h1 style="color: #2e7d32;">&#10004; Authorization Complete</h1>
<p>Your Atlassian account has been linked to your GitHub identity (<strong>%s</strong>).</p>
%s<p style="background: #e8f5e9; padding: 12px; border-radius: 4px;">
Redirecting you back... <a href="%s">Click here</a> if you are not redirected automatically.
</p>
</body>
</html>`, delaySec, redirectURL, login, autoExecHTML, redirectURL)
	} else {
		fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head><title>Jira Bot — Authorization Complete</title></head>
<body style="font-family: system-ui, sans-serif; max-width: 600px; margin: 40px auto; padding: 0 20px;">
<h1 style="color: #2e7d32;">&#10004; Authorization Complete</h1>
<p>Your Atlassian account has been linked to your GitHub identity (<strong>%s</strong>).</p>
%s<p style="background: #e8f5e9; padding: 12px; border-radius: 4px;">
Return to your GitHub issue or PR and retry the command.
</p>
</body>
</html>`, login, autoExecHTML)
	}
}

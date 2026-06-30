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
	"sync"
	"time"

	"github.com/charmbracelet/log"
	"github.com/ivanvc/jira-bot/internal/adapters/k8s"
)

// oauthSetupHandler provides endpoints for the one-time OAuth 2.0 token
// exchange during initial setup. It is only registered when client credentials
// are configured but no refresh token is present yet.
type oauthSetupHandler struct {
	clientID     string
	clientSecret string
	callbackURL  string
	persistence  *k8s.TokenPersistenceAdapter // nil when K8s unavailable
}

// pendingSetup holds token data for in-progress multi-site setup flows.
// Stored in the session map and cleared on use or after 5 minutes.
type pendingSetup struct {
	accessToken  string
	refreshToken string
	expiresAt    time.Time
	createdAt    time.Time // for TTL enforcement
}

// setupSessions maps session ID → pending setup data.
// Cleared on use or after 5 minutes.
var setupSessions sync.Map

// accessibleResource represents a single Atlassian Cloud site returned by
// the accessible-resources API endpoint.
type accessibleResource struct {
	ID     string   `json:"id"`
	Name   string   `json:"name"`
	URL    string   `json:"url"`
	Scopes []string `json:"scopes"`
}

// fetchAccessibleResources calls the Atlassian accessible-resources endpoint
// using the provided access token and returns the list of accessible sites.
func fetchAccessibleResources(accessToken string) ([]accessibleResource, error) {
	client := &http.Client{Timeout: 15 * time.Second}

	req, err := http.NewRequest("GET", "https://api.atlassian.com/oauth/token/accessible-resources", nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling accessible-resources: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("accessible-resources returned HTTP %d: %s", resp.StatusCode, string(body))
	}

	var resources []accessibleResource
	if err := json.Unmarshal(body, &resources); err != nil {
		return nil, fmt.Errorf("parsing accessible-resources response: %w", err)
	}

	return resources, nil
}

// computeExpiryTime computes the token expiry time by adding expiresIn seconds
// to the current wall-clock time. If expiresIn is zero, the token expires immediately.
func computeExpiryTime(expiresIn int) time.Time {
	return time.Now().Add(time.Duration(expiresIn) * time.Second)
}

type tokenExchangeRequest struct {
	GrantType    string `json:"grant_type"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	Code         string `json:"code"`
	RedirectURI  string `json:"redirect_uri"`
}

type tokenExchangeResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
}

func (h *oauthSetupHandler) registerHandler() {
	http.HandleFunc("/oauth/jira/callback", h.handleCallback)
	http.HandleFunc("/oauth/jira/select-site", h.handleSelectSite)
	log.Info("OAuth setup endpoints active", "callback", "/oauth/jira/callback", "select-site", "/oauth/jira/select-site")
}

// handleRootSetup serves the setup landing page at the root URL when the bot
// is in oauth2-setup mode. The page contains an "Authorize with Atlassian"
// button that links to the Atlassian authorization URL with proper OAuth params.
func (h *oauthSetupHandler) handleRootSetup(w http.ResponseWriter, req *http.Request) {
	params := url.Values{
		"audience":      {"api.atlassian.com"},
		"client_id":     {h.clientID},
		"scope":         {"offline_access read:jira-work write:jira-work"},
		"redirect_uri":  {h.callbackURL},
		"state":         {"setup"},
		"response_type": {"code"},
		"prompt":        {"consent"},
	}
	authorizeURL := "https://auth.atlassian.com/authorize?" + params.Encode()

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head><title>Jira Bot — OAuth Setup</title></head>
<body style="font-family: system-ui, sans-serif; max-width: 600px; margin: 40px auto; padding: 0 20px;">
<h1>Jira Bot — OAuth Setup</h1>
<p>Click the button below to authorize the bot with your Atlassian account.</p>
<a href="%s" style="display: inline-block; background: #0052cc; color: white; text-decoration: none; padding: 12px 24px; border-radius: 4px; font-size: 1.1em;">Authorize with Atlassian</a>
</body>
</html>`, authorizeURL)
}

// handleRootStatus serves the status landing page at the root URL when the bot
// is in oauth2 or basic auth mode (normal operation).
func handleRootStatus(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, `<!DOCTYPE html>
<html>
<head><title>Jira Bot — Running</title></head>
<body style="font-family: system-ui, sans-serif; max-width: 600px; margin: 40px auto; padding: 0 20px;">
<h1>Jira Bot</h1>
<p>The bot is configured and running.</p>
</body>
</html>`)
}

// exchangeCodeForTokens performs the OAuth token exchange with Atlassian.
func (h *oauthSetupHandler) exchangeCodeForTokens(code string) (*tokenExchangeResponse, error) {
	exchangeReq := tokenExchangeRequest{
		GrantType:    "authorization_code",
		ClientID:     h.clientID,
		ClientSecret: h.clientSecret,
		Code:         code,
		RedirectURI:  h.callbackURL,
	}

	body, err := json.Marshal(exchangeReq)
	if err != nil {
		return nil, fmt.Errorf("marshal token request: %w", err)
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Post("https://auth.atlassian.com/oauth/token", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("token exchange request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token exchange failed (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	var tokenResp tokenExchangeResponse
	if err := json.Unmarshal(respBody, &tokenResp); err != nil {
		return nil, fmt.Errorf("parse token response: %w", err)
	}

	return &tokenResp, nil
}

// generateSessionToken creates a cryptographically random session token.
func generateSessionToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating session token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// handleCallback exchanges the authorization code for tokens, fetches
// accessible resources, and either auto-persists or presents fallback options.
func (h *oauthSetupHandler) handleCallback(w http.ResponseWriter, req *http.Request) {
	code := req.URL.Query().Get("code")
	if code == "" {
		errMsg := req.URL.Query().Get("error")
		errDesc := req.URL.Query().Get("error_description")
		if errMsg != "" {
			http.Error(w, fmt.Sprintf("Authorization failed: %s — %s", errMsg, errDesc), http.StatusBadRequest)
			return
		}
		http.Error(w, "Missing 'code' query parameter", http.StatusBadRequest)
		return
	}

	tokenResp, err := h.exchangeCodeForTokens(code)
	if err != nil {
		log.Error("Token exchange failed", "error", err)
		http.Error(w, fmt.Sprintf("Token exchange failed: %v", err), http.StatusBadGateway)
		return
	}

	if tokenResp.RefreshToken == "" {
		http.Error(w, "No refresh token in response. Make sure you included 'offline_access' in the scopes.", http.StatusInternalServerError)
		return
	}

	expiresAt := computeExpiryTime(tokenResp.ExpiresIn)

	// Fetch accessible resources to determine Cloud ID
	resources, err := fetchAccessibleResources(tokenResp.AccessToken)
	if err != nil {
		log.Error("Failed to fetch accessible resources", "error", err)
		renderHardErrorPage(w,
			"Accessible Resources Fetch Failed",
			"The bot could not retrieve the list of Atlassian sites associated with your account.",
			fmt.Sprintf("Error: %s\n\nTo fix:\n1. Verify the OAuth app has the correct scopes (read:jira-work, write:jira-work)\n2. Check that your Atlassian account has access to at least one site\n3. Re-run the OAuth authorization flow", err.Error()),
		)
		return
	}

	if len(resources) == 0 {
		renderHardErrorPage(w,
			"No Accessible Sites Found",
			"No Atlassian sites were found for this account.",
			"1. Verify your Atlassian account has access to at least one Jira site\n2. Check that the OAuth app has the correct scopes\n3. Re-run the OAuth authorization flow",
		)
		return
	}

	if len(resources) == 1 {
		// Single resource: persist directly
		cloudID := resources[0].ID
		h.persistAndRespond(w, tokenResp.RefreshToken, tokenResp.AccessToken, expiresAt, cloudID)
		return
	}

	// Multiple resources: store in session, render selection page
	sessionToken, err := generateSessionToken()
	if err != nil {
		log.Error("Failed to generate session token", "error", err)
		renderHardErrorPage(w,
			"Internal Error",
			"An internal error occurred while generating a session token.",
			"This is an unexpected error. Please re-run the OAuth authorization flow. If the problem persists, check the bot logs for details.",
		)
		return
	}

	setupSessions.Store(sessionToken, &pendingSetup{
		accessToken:  tokenResp.AccessToken,
		refreshToken: tokenResp.RefreshToken,
		expiresAt:    expiresAt,
		createdAt:    time.Now(),
	})

	http.SetCookie(w, &http.Cookie{
		Name:     "jira_setup_session",
		Value:    sessionToken,
		Path:     "/oauth/jira/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})

	renderSiteSelectionPage(w, resources)
}

// persistAndRespond attempts to write tokens + Cloud ID to the Bot_Secret.
// If the adapter is nil or the write fails, it renders a hard error page with
// instructions. Tokens are NEVER displayed to the user.
func (h *oauthSetupHandler) persistAndRespond(w http.ResponseWriter, refreshToken, accessToken string, expiresAt time.Time, cloudID string) {
	if h.persistence == nil {
		renderHardErrorPage(w,
			"Kubernetes Access Not Configured",
			"The bot cannot persist OAuth tokens because Kubernetes access is not configured.",
			"1. Configure RBAC: create a Role with get/create/update permissions on secrets\n2. Bind the Role to the bot's ServiceAccount\n3. Set POD_NAMESPACE and TOKEN_SECRET_NAME environment variables\n4. Redeploy the bot\n5. Re-run the OAuth authorization flow",
		)
		return
	}

	tokenData := k8s.TokenData{
		RefreshToken: refreshToken,
		AccessToken:  accessToken,
		ExpiresAt:    expiresAt,
		CloudID:      cloudID,
	}

	if err := h.persistence.Write(context.Background(), tokenData); err != nil {
		log.Error("Failed to persist tokens to Bot_Secret", "error", err)
		renderHardErrorPage(w,
			"Token Persistence Failed",
			"The bot failed to write OAuth tokens to the Kubernetes Secret.",
			fmt.Sprintf("Error: %s\n\nTo fix:\n1. Check RBAC permissions: the ServiceAccount needs get, create, and update on secrets\n2. Verify the ServiceAccount is correctly bound via a RoleBinding\n3. Ensure POD_NAMESPACE and TOKEN_SECRET_NAME are set correctly\n4. Re-run the OAuth authorization flow", err.Error()),
		)
		return
	}

	renderSuccessPage(w, cloudID)
}

// renderHardErrorPage renders an error page that instructs the operator to fix
// the underlying issue and re-run the OAuth flow. It NEVER displays tokens.
func renderHardErrorPage(w http.ResponseWriter, title, message, instructions string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusInternalServerError)
	fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head><title>Jira Bot — %s</title></head>
<body style="font-family: system-ui, sans-serif; max-width: 600px; margin: 40px auto; padding: 0 20px;">
<h1 style="color: #d32f2f;">&#9888; %s</h1>
<p>%s</p>
<pre style="background: #f4f4f4; padding: 12px; border-radius: 4px; white-space: pre-wrap; word-break: break-word;">%s</pre>
<p style="background: #fff3e0; padding: 12px; border-radius: 4px;">
After fixing the issue above, re-run the OAuth authorization flow to complete setup.
</p>
</body>
</html>`, title, title, message, instructions)
}

// renderSuccessPage renders the success page after tokens are persisted.
// It does NOT display the refresh token or access token (Requirement 4.4).
func renderSuccessPage(w http.ResponseWriter, cloudID string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head><title>Jira Bot — OAuth Setup Complete</title></head>
<body style="font-family: system-ui, sans-serif; max-width: 600px; margin: 40px auto; padding: 0 20px;">
<h1>&#10004; OAuth Setup Complete</h1>
<p>Tokens and Cloud ID have been persisted to the Bot Secret automatically.</p>
<p><strong>Cloud ID:</strong> <code>%s</code></p>
<p style="background: #e8f5e9; padding: 12px; border-radius: 4px;">
&#9888; Please restart the pod to activate normal operation mode.
</p>
</body>
</html>`, cloudID)
}

// handleSelectSite handles the POST /oauth/jira/select-site endpoint.
// It reads the selected Cloud ID from the form body, retrieves stored tokens
// from the session, validates the session TTL, persists tokens + Cloud ID,
// and renders the appropriate response page.
func (h *oauthSetupHandler) handleSelectSite(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Read cloud_id from form body
	if err := req.ParseForm(); err != nil {
		http.Error(w, "Invalid form data", http.StatusBadRequest)
		return
	}
	cloudID := req.FormValue("cloud_id")
	if cloudID == "" {
		http.Error(w, "Missing cloud_id parameter", http.StatusBadRequest)
		return
	}

	// Read session token from cookie
	cookie, err := req.Cookie("jira_setup_session")
	if err != nil || cookie.Value == "" {
		http.Error(w, "Invalid session — please restart the authorization flow.", http.StatusBadRequest)
		return
	}
	sessionToken := cookie.Value

	// Look up session in setupSessions
	val, ok := setupSessions.Load(sessionToken)
	if !ok {
		http.Error(w, "Invalid session — please restart the authorization flow.", http.StatusBadRequest)
		return
	}

	// Always clean up the session entry after use
	setupSessions.Delete(sessionToken)

	pending, ok := val.(*pendingSetup)
	if !ok {
		http.Error(w, "Invalid session data — please restart the authorization flow.", http.StatusBadRequest)
		return
	}

	// Validate TTL (5 minutes)
	if time.Since(pending.createdAt) > 5*time.Minute {
		http.Error(w, "Session expired — please restart the authorization flow.", http.StatusBadRequest)
		return
	}

	// Persist tokens + selected Cloud ID via adapter
	h.persistAndRespond(w, pending.refreshToken, pending.accessToken, pending.expiresAt, cloudID)
}

// renderSiteSelectionPage renders an HTML page listing multiple accessible
// resources for the user to select one.
func renderSiteSelectionPage(w http.ResponseWriter, resources []accessibleResource) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	var options string
	for _, r := range resources {
		options += fmt.Sprintf(`<div style="margin: 8px 0; padding: 12px; border: 1px solid #ddd; border-radius: 4px;">
<form method="POST" action="/oauth/jira/select-site" style="display: inline;">
<input type="hidden" name="cloud_id" value="%s">
<button type="submit" style="background: #0052cc; color: white; border: none; padding: 8px 16px; border-radius: 4px; cursor: pointer;">Select</button>
<strong style="margin-left: 12px;">%s</strong>
<span style="color: #666; margin-left: 8px;">(%s)</span>
</form>
</div>`, r.ID, r.Name, r.ID)
	}
	fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head><title>Jira Bot — Select Site</title></head>
<body style="font-family: system-ui, sans-serif; max-width: 600px; margin: 40px auto; padding: 0 20px;">
<h1>Select Atlassian Site</h1>
<p>Multiple Atlassian sites were found. Please select the one you want to use:</p>
%s
</body>
</html>`, options)
}

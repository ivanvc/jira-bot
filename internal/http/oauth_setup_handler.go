package http

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/charmbracelet/log"
)

// oauthSetupHandler provides endpoints for the one-time OAuth 2.0 token
// exchange during initial setup. It is only registered when client credentials
// are configured but no refresh token is present yet.
type oauthSetupHandler struct {
	clientID     string
	clientSecret string
	callbackURL  string
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
	http.HandleFunc("/jira/oauth/authorize", h.handleAuthorize)
	http.HandleFunc("/jira/oauth/callback", h.handleCallback)
	log.Info("OAuth setup endpoints active", "authorize", "/jira/oauth/authorize", "callback", "/jira/oauth/callback")
}

// handleAuthorize redirects the user to the Atlassian authorization URL.
func (h *oauthSetupHandler) handleAuthorize(w http.ResponseWriter, req *http.Request) {
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
	http.Redirect(w, req, authorizeURL, http.StatusFound)
}

// handleCallback exchanges the authorization code for tokens and displays
// the refresh token to the user.
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

	exchangeReq := tokenExchangeRequest{
		GrantType:    "authorization_code",
		ClientID:     h.clientID,
		ClientSecret: h.clientSecret,
		Code:         code,
		RedirectURI:  h.callbackURL,
	}

	body, err := json.Marshal(exchangeReq)
	if err != nil {
		http.Error(w, "Failed to marshal token request", http.StatusInternalServerError)
		return
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Post("https://auth.atlassian.com/oauth/token", "application/json", bytes.NewReader(body))
	if err != nil {
		log.Error("Token exchange request failed", "error", err)
		http.Error(w, fmt.Sprintf("Token exchange failed: %v", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		log.Error("Token exchange returned error", "status", resp.StatusCode, "body", string(respBody))
		http.Error(w, fmt.Sprintf("Token exchange failed (HTTP %d): %s", resp.StatusCode, string(respBody)), http.StatusBadGateway)
		return
	}

	var tokenResp tokenExchangeResponse
	if err := json.Unmarshal(respBody, &tokenResp); err != nil {
		http.Error(w, "Failed to parse token response", http.StatusInternalServerError)
		return
	}

	if tokenResp.RefreshToken == "" {
		http.Error(w, "No refresh token in response. Make sure you included 'offline_access' in the scopes.", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head><title>Jira Bot — OAuth Setup Complete</title></head>
<body style="font-family: system-ui, sans-serif; max-width: 600px; margin: 40px auto; padding: 0 20px;">
<h1>&#10004; OAuth Setup Complete</h1>
<p>Set this as your <code>JIRA_BOT_JIRA_REFRESH_TOKEN</code> environment variable:</p>
<pre style="background: #f4f4f4; padding: 12px; border-radius: 4px; overflow-x: auto; word-break: break-all;">%s</pre>
<p style="color: #666; font-size: 0.9em;">
This token is valid as long as it's used within 90 days. The bot refreshes access tokens automatically at runtime.<br><br>
Once configured, restart the bot with the refresh token set and these setup endpoints will no longer be available.
</p>
</body>
</html>`, tokenResp.RefreshToken)
}

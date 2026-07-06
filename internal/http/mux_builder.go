package http

import (
	"fmt"
	"net/http"
	"time"

	"github.com/charmbracelet/log"
	"github.com/ivanvc/jira-bot/internal/common"
)

const authSessionTTL = 10 * time.Minute

// BuildMux creates a new ServeMux configured for the bot's per-user token mode.
// It registers the webhook handler, user auth routes, status root handler, and
// health/readiness endpoints.
func BuildMux(state *common.State) *http.ServeMux {
	mux := http.NewServeMux()

	// Webhook handler
	wh := &webhookHandler{}
	mux.HandleFunc("/webhooks/github/payload", wh.handleWithState(state))

	// User auth handler (per-user OAuth flow)
	if state.UserTokenStore != nil {
		handler := &userAuthHandler{
			githubAppClientID:     loadGitHubAppClientID(state),
			githubAppClientSecret: state.Config.GitHubAppClientSecret,
			atlClientID:           state.Config.JiraClientID,
			atlClientSecret:       state.Config.JiraClientSecret,
			atlCallbackURL:        state.Config.UserAuthCallbackURL,
			cloudID:               state.Config.CloudID,
			store:                 state.UserTokenStore,
			sessions:              NewAuthSessionMap(authSessionTTL),
		}
		mux.HandleFunc("/oauth/user/authorize", handler.handleAuthorize)
		mux.HandleFunc("/oauth/user/github/callback", handler.handleGitHubCallback)
		mux.HandleFunc("/oauth/user/atlassian/callback", handler.handleAtlassianCallback)
	}

	// Status root handler
	mux.HandleFunc("/", handleRootStatus)

	// Health and readiness endpoints
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	log.Info("Built mux", "routes", []string{"/webhooks/github/payload", "/oauth/user/authorize", "/oauth/user/github/callback", "/oauth/user/atlassian/callback", "/", "/healthz", "/readyz"})
	return mux
}

// handleRootStatus serves the status landing page at the root URL.
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

// loadGitHubAppClientID returns the GitHub App ID as a string for use in OAuth redirects.
func loadGitHubAppClientID(state *common.State) string {
	return fmt.Sprintf("%d", state.Config.GitHubAppID)
}

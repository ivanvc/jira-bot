package http

import (
	"net/http"

	"github.com/charmbracelet/log"
	"github.com/ivanvc/jira-bot/internal/common"
)

// BuildSetupMux creates a new ServeMux configured for oauth2-setup mode.
// It registers the OAuth setup endpoints (callback, site selection, root setup page)
// and the webhook handler. The returned mux is independent of http.DefaultServeMux.
// The coordinator parameter may be nil; if provided, the setup handler will attempt
// a live transition after successful token persistence.
func BuildSetupMux(state *common.State, coordinator TransitionCoordinatorInterface) *http.ServeMux {
	mux := http.NewServeMux()

	// Webhook handler (must be present in both modes)
	wh := &webhookHandler{}
	mux.HandleFunc("/webhooks/github/payload", wh.handleWithState(state))

	// OAuth setup endpoints
	handler := &oauthSetupHandler{
		clientID:     state.Config.JiraClientID,
		clientSecret: state.Config.JiraClientSecret,
		callbackURL:  state.Config.OAuthCallbackURL,
		persistence:  state.TokenPersistenceAdapter,
		coordinator:  coordinator,
	}
	mux.HandleFunc("/oauth/jira/callback", handler.handleCallback)
	mux.HandleFunc("/oauth/jira/select-site", handler.handleSelectSite)
	mux.HandleFunc("/", handler.handleRootSetup)

	log.Info("Built setup mux", "routes", []string{"/webhooks/github/payload", "/oauth/jira/callback", "/oauth/jira/select-site", "/"})
	return mux
}

// BuildOAuth2Mux creates a new ServeMux configured for oauth2 mode.
// It registers the webhook handler, status root handler, and health/readiness
// endpoints. The returned mux is independent of http.DefaultServeMux.
func BuildOAuth2Mux(state *common.State) *http.ServeMux {
	mux := http.NewServeMux()

	// Webhook handler (must be present in both modes)
	wh := &webhookHandler{}
	mux.HandleFunc("/webhooks/github/payload", wh.handleWithState(state))

	// Status root handler
	mux.HandleFunc("/", handleRootStatus)

	// Health and readiness endpoints
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	log.Info("Built oauth2 mux", "routes", []string{"/webhooks/github/payload", "/", "/healthz", "/readyz"})
	return mux
}

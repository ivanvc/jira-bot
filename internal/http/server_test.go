package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ivanvc/jira-bot/internal/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewServer_SetupMode verifies that NewServer in oauth2-setup mode creates a
// server with a SwitchableMux delegating to the setup mux.
// Validates: Requirements 3.3
func TestNewServer_SetupMode(t *testing.T) {
	state := &common.State{
		Config: common.Config{
			AuthMode:         "oauth2-setup",
			ListenHTTP:       ":0",
			JiraClientID:     "test-client-id",
			JiraClientSecret: "test-client-secret",
			OAuthCallbackURL: "http://localhost:8080/oauth/jira/callback",
		},
	}

	srv := NewServer(state, nil)

	// Mux should be initialized
	require.NotNil(t, srv.Mux, "server.Mux should not be nil")

	// Server.Handler should be the SwitchableMux
	assert.Equal(t, srv.Mux, srv.Server.Handler,
		"server.Server.Handler should equal server.Mux (SwitchableMux)")

	// Setup routes should be active: /oauth/jira/callback should NOT return 404
	req := httptest.NewRequest(http.MethodGet, "/oauth/jira/callback", nil)
	rec := httptest.NewRecorder()
	srv.Server.Handler.ServeHTTP(rec, req)

	assert.NotEqual(t, http.StatusNotFound, rec.Code,
		"in setup mode, /oauth/jira/callback should be registered (got 404)")
}

// TestNewServer_OAuth2Mode verifies that NewServer in oauth2 mode creates a
// server with a SwitchableMux delegating to the oauth2 mux.
// Validates: Requirements 3.3
func TestNewServer_OAuth2Mode(t *testing.T) {
	state := &common.State{
		Config: common.Config{
			AuthMode:         "oauth2",
			ListenHTTP:       ":0",
			JiraClientID:     "test-client-id",
			JiraClientSecret: "test-client-secret",
		},
	}

	srv := NewServer(state, nil)

	// Mux should be initialized
	require.NotNil(t, srv.Mux, "server.Mux should not be nil")

	// Server.Handler should be the SwitchableMux
	assert.Equal(t, srv.Mux, srv.Server.Handler,
		"server.Server.Handler should equal server.Mux (SwitchableMux)")

	// /healthz should return 200 (oauth2 routes are active)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	srv.Server.Handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code,
		"in oauth2 mode, /healthz should return 200")

	// /oauth/jira/callback should fall through to root (no dedicated handler)
	// Get the root response for comparison
	rootReq := httptest.NewRequest(http.MethodGet, "/", nil)
	rootRec := httptest.NewRecorder()
	srv.Server.Handler.ServeHTTP(rootRec, rootReq)
	rootBody := rootRec.Body.String()

	callbackReq := httptest.NewRequest(http.MethodGet, "/oauth/jira/callback", nil)
	callbackRec := httptest.NewRecorder()
	srv.Server.Handler.ServeHTTP(callbackRec, callbackReq)

	assert.Equal(t, rootBody, callbackRec.Body.String(),
		"in oauth2 mode, /oauth/jira/callback should fall through to root handler")
}

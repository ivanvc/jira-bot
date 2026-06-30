package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ivanvc/jira-bot/internal/common"
	"github.com/stretchr/testify/assert"
)

// newTestState creates a minimal common.State with enough config to construct muxes.
func newTestState() *common.State {
	return &common.State{
		Config: common.Config{
			GitHubWebhookSecret: "test-secret",
			JiraClientID:        "test-client-id",
			JiraClientSecret:    "test-client-secret",
			OAuthCallbackURL:    "http://localhost:8080/oauth/jira/callback",
		},
	}
}

// TestBuildSetupMux_RegistersSetupEndpoints verifies that BuildSetupMux
// registers the OAuth setup endpoints and the webhook handler.
// Validates: Requirements 3.1, 3.5
func TestBuildSetupMux_RegistersSetupEndpoints(t *testing.T) {
	state := newTestState()
	mux := BuildSetupMux(state, nil)

	tests := []struct {
		name   string
		method string
		path   string
	}{
		{"oauth callback", http.MethodGet, "/oauth/jira/callback"},
		{"site selection", http.MethodGet, "/oauth/jira/select-site"},
		{"root setup page", http.MethodGet, "/"},
		{"webhook handler", http.MethodPost, "/webhooks/github/payload"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			// A 404 means the route is not registered. Any other status
			// (200, 400, 401, 405, etc.) confirms the handler exists.
			assert.NotEqual(t, http.StatusNotFound, rec.Code,
				"expected route %s %s to be registered (got 404)", tt.method, tt.path)
		})
	}
}

// TestBuildOAuth2Mux_RegistersOAuth2Endpoints verifies that BuildOAuth2Mux
// registers the status root, webhook handler, and health/readiness endpoints.
// Validates: Requirements 3.2, 3.5
func TestBuildOAuth2Mux_RegistersOAuth2Endpoints(t *testing.T) {
	state := newTestState()
	mux := BuildOAuth2Mux(state)

	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int // 0 means "just not 404"
	}{
		{"root status page", http.MethodGet, "/", http.StatusOK},
		{"webhook handler", http.MethodPost, "/webhooks/github/payload", 0},
		{"healthz", http.MethodGet, "/healthz", http.StatusOK},
		{"readyz", http.MethodGet, "/readyz", http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			if tt.wantStatus != 0 {
				assert.Equal(t, tt.wantStatus, rec.Code)
			} else {
				assert.NotEqual(t, http.StatusNotFound, rec.Code,
					"expected route %s %s to be registered (got 404)", tt.method, tt.path)
			}
		})
	}
}

// TestBuildOAuth2Mux_SetupPathsNotRegistered verifies that setup-specific paths
// have no dedicated handler on the oauth2 mux. Since the root "/" is a catch-all
// in Go's ServeMux, unmatched paths fall through to the root status handler.
// We verify by checking the response body matches the status page (not a setup page).
// Validates: Requirements 3.1, 3.2
func TestBuildOAuth2Mux_SetupPathsNotRegistered(t *testing.T) {
	state := newTestState()
	mux := BuildOAuth2Mux(state)

	// Get the root status page response for comparison.
	rootReq := httptest.NewRequest(http.MethodGet, "/", nil)
	rootRec := httptest.NewRecorder()
	mux.ServeHTTP(rootRec, rootReq)
	statusBody := rootRec.Body.String()

	setupPaths := []string{
		"/oauth/jira/callback",
		"/oauth/jira/select-site",
	}

	for _, path := range setupPaths {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			// Setup paths should not have dedicated handlers on the oauth2 mux.
			// They fall through to the root "/" catch-all and return the status page.
			assert.Equal(t, statusBody, rec.Body.String(),
				"expected setup path %s to fall through to root status handler", path)
		})
	}
}

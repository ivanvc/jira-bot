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
		},
	}
}

// TestBuildMux_RegistersEndpoints verifies that BuildMux registers the
// webhook handler, status root, and health/readiness endpoints.
func TestBuildMux_RegistersEndpoints(t *testing.T) {
	state := newTestState()
	mux := BuildMux(state)

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

package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ivanvc/jira-bot/internal/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewServer_CreatesServerWithMux verifies that NewServer creates a server
// with the standard mux containing expected endpoints.
func TestNewServer_CreatesServerWithMux(t *testing.T) {
	state := &common.State{
		Config: common.Config{
			ListenHTTP:       ":0",
			JiraClientID:     "test-client-id",
			JiraClientSecret: "test-client-secret",
		},
	}

	srv := NewServer(state)

	// Server.Handler should be set
	require.NotNil(t, srv.Server.Handler, "server.Server.Handler should not be nil")

	// /healthz should return 200
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	srv.Server.Handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code,
		"/healthz should return 200")

	// / should return status page
	rootReq := httptest.NewRequest(http.MethodGet, "/", nil)
	rootRec := httptest.NewRecorder()
	srv.Server.Handler.ServeHTTP(rootRec, rootReq)

	assert.Equal(t, http.StatusOK, rootRec.Code)
	assert.Contains(t, rootRec.Body.String(), "configured and running")
}

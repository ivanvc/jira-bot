package transition_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/charmbracelet/log"
	"github.com/ivanvc/jira-bot/internal/adapters/k8s"
	"github.com/ivanvc/jira-bot/internal/common"
	internalhttp "github.com/ivanvc/jira-bot/internal/http"
	"github.com/ivanvc/jira-bot/internal/transition"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockJiraClient is a minimal implementation of common.JiraClientInterface
// for testing purposes.
type mockJiraClient struct{}

func (m *mockJiraClient) CreateIssue(project, issueType, summary, description string, extraFields map[string]interface{}) (string, error) {
	return "TEST-1", nil
}

// TestIntegration_EndToEndTransitionFlow verifies the full transition from
// oauth2-setup mode to oauth2 mode without external dependencies.
// Validates: Requirements 1.1, 3.1, 3.2, 4.1, 4.2, 4.3
func TestIntegration_EndToEndTransitionFlow(t *testing.T) {
	// 1. Create state in "oauth2-setup" mode
	state := &common.State{
		Config: common.Config{
			AuthMode:         "oauth2-setup",
			JiraClientID:     "test-client-id",
			JiraClientSecret: "test-client-secret",
			OAuthCallbackURL: "http://localhost:8080/oauth/jira/callback",
		},
	}

	// 2. Build the setup mux and wrap in SwitchableMux
	setupMux := internalhttp.BuildSetupMux(state, nil)
	switchableMux := internalhttp.NewSwitchableMux(setupMux)

	// 3. Create a mock ClientFactory that returns a mock JiraClient
	factoryCalls := 0
	mockFactory := func(cfg common.Config, tokenData k8s.TokenData) (common.JiraClientInterface, func(), error) {
		factoryCalls++
		return &mockJiraClient{}, nil, nil
	}

	// 4. Create the TransitionCoordinator wired to the mock factory and BuildOAuth2Mux
	logger := log.Default()
	coordinator := transition.NewTransitionCoordinator(
		state,
		switchableMux,
		mockFactory,
		internalhttp.BuildOAuth2Mux,
		logger,
	)

	// 5. Create valid TokenData
	tokenData := k8s.TokenData{
		RefreshToken: "test-refresh-token",
		AccessToken:  "test-access-token",
		ExpiresAt:    time.Now().Add(1 * time.Hour),
		CloudID:      "test-cloud-id-123",
	}

	// Verify setup routes are active BEFORE transition
	t.Run("before transition: setup routes active", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/oauth/jira/callback", nil)
		rec := httptest.NewRecorder()
		switchableMux.ServeHTTP(rec, req)
		// The callback handler exists (returns 400 because no code param, not 404)
		assert.NotEqual(t, http.StatusNotFound, rec.Code,
			"setup route /oauth/jira/callback should be active before transition")
	})

	// 6. Call Transition
	err := coordinator.Transition(tokenData)

	// 7. Verify: no error returned
	require.NoError(t, err, "Transition should succeed with valid token data")

	// Verify: state.Config.AuthMode == "oauth2"
	assert.Equal(t, "oauth2", state.Config.AuthMode,
		"AuthMode should be updated to oauth2 after transition")

	// Verify: state.JiraClient != nil
	assert.NotNil(t, state.JiraClient,
		"JiraClient should be set after transition")

	// Verify: state.Config.TokenData == tokenData
	assert.Equal(t, tokenData.RefreshToken, state.Config.TokenData.RefreshToken)
	assert.Equal(t, tokenData.AccessToken, state.Config.TokenData.AccessToken)
	assert.Equal(t, tokenData.CloudID, state.Config.TokenData.CloudID)

	// Verify: ClientFactory was called exactly once
	assert.Equal(t, 1, factoryCalls, "ClientFactory should be called exactly once")

	// Verify: HTTP requests to /healthz return 200 (oauth2 mux is active)
	t.Run("after transition: healthz returns 200", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		rec := httptest.NewRecorder()
		switchableMux.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code,
			"/healthz should return 200 after transition to oauth2 mux")
	})

	// Verify: HTTP requests to / return 200 (status root handler active)
	t.Run("after transition: root status returns 200", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		switchableMux.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code,
			"/ should return 200 after transition")
		assert.Contains(t, rec.Body.String(), "configured and running",
			"root should show status page after transition")
	})

	// Verify: HTTP requests to /oauth/jira/callback fall through to root
	// (no dedicated setup handler — the oauth2 mux has "/" as catch-all)
	t.Run("after transition: setup paths fall through to root", func(t *testing.T) {
		// Get root status body for comparison
		rootReq := httptest.NewRequest(http.MethodGet, "/", nil)
		rootRec := httptest.NewRecorder()
		switchableMux.ServeHTTP(rootRec, rootReq)
		rootBody := rootRec.Body.String()

		req := httptest.NewRequest(http.MethodGet, "/oauth/jira/callback", nil)
		rec := httptest.NewRecorder()
		switchableMux.ServeHTTP(rec, req)
		assert.Equal(t, rootBody, rec.Body.String(),
			"/oauth/jira/callback should fall through to root status handler after transition")
	})

	// Verify: readyz endpoint works
	t.Run("after transition: readyz returns 200", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
		rec := httptest.NewRecorder()
		switchableMux.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code,
			"/readyz should return 200 after transition")
	})
}

// TestIntegration_TransitionWithInvalidTokenData verifies that calling Transition
// with invalid TokenData (empty RefreshToken) returns an error and leaves state unchanged.
// Validates: Requirements 1.2
func TestIntegration_TransitionWithInvalidTokenData(t *testing.T) {
	// 1. Create state in "oauth2-setup" mode
	state := &common.State{
		Config: common.Config{
			AuthMode:         "oauth2-setup",
			JiraClientID:     "test-client-id",
			JiraClientSecret: "test-client-secret",
			OAuthCallbackURL: "http://localhost:8080/oauth/jira/callback",
		},
	}

	// 2. Build the setup mux and wrap in SwitchableMux
	setupMux := internalhttp.BuildSetupMux(state, nil)
	switchableMux := internalhttp.NewSwitchableMux(setupMux)

	// 3. Mock factory should NOT be called for invalid token data
	factoryCalls := 0
	mockFactory := func(cfg common.Config, tokenData k8s.TokenData) (common.JiraClientInterface, func(), error) {
		factoryCalls++
		return &mockJiraClient{}, nil, nil
	}

	// 4. Create coordinator
	logger := log.Default()
	coordinator := transition.NewTransitionCoordinator(
		state,
		switchableMux,
		mockFactory,
		internalhttp.BuildOAuth2Mux,
		logger,
	)

	// 5. Create INVALID TokenData (empty RefreshToken)
	invalidTokenData := k8s.TokenData{
		RefreshToken: "", // invalid
		AccessToken:  "test-access-token",
		ExpiresAt:    time.Now().Add(1 * time.Hour),
		CloudID:      "test-cloud-id-123",
	}

	// 6. Call Transition with invalid data
	err := coordinator.Transition(invalidTokenData)

	// 7. Verify: error returned
	require.Error(t, err, "Transition should fail with empty RefreshToken")
	assert.Contains(t, err.Error(), "RefreshToken",
		"error should mention RefreshToken")

	// Verify: state unchanged
	assert.Equal(t, "oauth2-setup", state.Config.AuthMode,
		"AuthMode should remain oauth2-setup after failed transition")
	assert.Nil(t, state.JiraClient,
		"JiraClient should remain nil after failed transition")
	assert.Equal(t, k8s.TokenData{}, state.Config.TokenData,
		"TokenData should remain empty after failed transition")

	// Verify: factory was NOT called
	assert.Equal(t, 0, factoryCalls,
		"ClientFactory should not be called for invalid token data")

	// Verify: setup routes still active
	t.Run("setup routes still active after failed transition", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/oauth/jira/callback", nil)
		rec := httptest.NewRecorder()
		switchableMux.ServeHTTP(rec, req)
		assert.NotEqual(t, http.StatusNotFound, rec.Code,
			"setup route should still be active after failed transition")
	})
}

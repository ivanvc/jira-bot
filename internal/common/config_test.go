package common

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/ivanvc/jira-bot/internal/adapters/k8s"
	"github.com/stretchr/testify/assert"
)

// mockBotSecretReader is a test helper that implements BotSecretReader.
type mockBotSecretReader struct {
	data k8s.TokenData
	err  error
}

func (m *mockBotSecretReader) Read(_ context.Context) (k8s.TokenData, error) {
	return m.data, m.err
}

// --- determineAuthMode tests ---

func TestDetermineAuthMode_ClientCredentialsAbsent(t *testing.T) {
	tests := []struct {
		name         string
		clientID     string
		clientSecret string
	}{
		{"both empty", "", ""},
		{"clientID empty", "", "secret"},
		{"clientSecret empty", "id", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mode, data, err := determineAuthMode(tt.clientID, tt.clientSecret, nil)
			assert.Equal(t, "", mode)
			assert.Equal(t, k8s.TokenData{}, data)
			assert.NoError(t, err)
		})
	}
}

func TestDetermineAuthMode_NilReader(t *testing.T) {
	mode, data, err := determineAuthMode("id", "secret", nil)
	assert.Equal(t, "oauth2-setup", mode)
	assert.Equal(t, k8s.TokenData{}, data)
	assert.NoError(t, err)
}

func TestDetermineAuthMode_ReadError(t *testing.T) {
	reader := &mockBotSecretReader{err: errors.New("k8s unavailable")}
	mode, data, err := determineAuthMode("id", "secret", reader)
	assert.Equal(t, "oauth2-setup", mode)
	assert.Equal(t, k8s.TokenData{}, data)
	assert.NoError(t, err)
}

func TestDetermineAuthMode_ValidTokenData(t *testing.T) {
	expected := k8s.TokenData{
		RefreshToken: "refresh-123",
		AccessToken:  "access-456",
		CloudID:      "cloud-789",
	}
	reader := &mockBotSecretReader{data: expected}
	mode, data, err := determineAuthMode("id", "secret", reader)
	assert.Equal(t, "oauth2", mode)
	assert.Equal(t, expected, data)
	assert.NoError(t, err)
}

func TestDetermineAuthMode_MissingRefreshToken(t *testing.T) {
	reader := &mockBotSecretReader{data: k8s.TokenData{CloudID: "cloud-789"}}
	mode, data, err := determineAuthMode("id", "secret", reader)
	assert.Equal(t, "oauth2-setup", mode)
	assert.Equal(t, k8s.TokenData{}, data)
	assert.NoError(t, err)
}

func TestDetermineAuthMode_MissingCloudID(t *testing.T) {
	reader := &mockBotSecretReader{data: k8s.TokenData{RefreshToken: "refresh-123"}}
	mode, data, err := determineAuthMode("id", "secret", reader)
	assert.Equal(t, "oauth2-setup", mode)
	assert.Equal(t, k8s.TokenData{}, data)
	assert.NoError(t, err)
}

func TestLoadConfig_AllRequiredEnvVarsSet(t *testing.T) {
	// Set all required environment variables
	t.Setenv("JIRA_BOT_LISTEN_HTTP", ":9090")
	t.Setenv("JIRA_BOT_GITHUB_APP_ID", "12345")
	t.Setenv("JIRA_BOT_GITHUB_PRIVATE_KEY", "test-private-key")
	t.Setenv("JIRA_BOT_GITHUB_WEBHOOK_SECRET", "webhook-secret")
	t.Setenv("JIRA_BOT_JIRA_BASE_URL", "https://jira.example.com")
	t.Setenv("JIRA_BOT_JIRA_USERNAME", "testuser")
	t.Setenv("JIRA_BOT_JIRA_TOKEN", "test-token")
	t.Setenv("JIRA_BOT_JIRA_DEFAULT_PROJECT", "PROJ")
	t.Setenv("JIRA_BOT_JIRA_DEFAULT_ISSUE_TYPE", "Task")

	cfg := LoadConfig()

	assert.Equal(t, ":9090", cfg.ListenHTTP)
	assert.Equal(t, int64(12345), cfg.GitHubAppID)
	assert.Equal(t, "test-private-key", cfg.GitHubPrivateKey)
	assert.Equal(t, "webhook-secret", cfg.GitHubWebhookSecret)
	assert.Equal(t, "https://jira.example.com", cfg.JiraBaseURL)
	assert.Equal(t, "testuser", cfg.JiraUsername)
	assert.Equal(t, "test-token", cfg.JiraToken)
	assert.Equal(t, "PROJ", cfg.JiraDefaultProject)
	assert.Equal(t, "Task", cfg.JiraDefaultIssueType)
}

func TestLoadConfig_OptionalEnvVarUsesDefault(t *testing.T) {
	// Do NOT set JIRA_BOT_LISTEN_HTTP — it should default to ":8080"
	t.Setenv("JIRA_BOT_GITHUB_APP_ID", "99")
	t.Setenv("JIRA_BOT_GITHUB_PRIVATE_KEY", "key")
	t.Setenv("JIRA_BOT_GITHUB_WEBHOOK_SECRET", "secret")
	t.Setenv("JIRA_BOT_JIRA_BASE_URL", "https://jira.test")
	t.Setenv("JIRA_BOT_JIRA_USERNAME", "user")
	t.Setenv("JIRA_BOT_JIRA_TOKEN", "token")
	t.Setenv("JIRA_BOT_JIRA_DEFAULT_PROJECT", "DEF")
	t.Setenv("JIRA_BOT_JIRA_DEFAULT_ISSUE_TYPE", "Story")

	cfg := LoadConfig()

	assert.Equal(t, ":8080", cfg.ListenHTTP, "ListenHTTP should use default value when env var is not set")
}

// --- OAuth 2.0 mode tests ---

func TestLoadConfig_OAuthMode_WithMockReader(t *testing.T) {
	// Set common required vars
	t.Setenv("JIRA_BOT_GITHUB_APP_ID", "12345")
	t.Setenv("JIRA_BOT_GITHUB_PRIVATE_KEY", "test-private-key")
	t.Setenv("JIRA_BOT_GITHUB_WEBHOOK_SECRET", "webhook-secret")
	t.Setenv("JIRA_BOT_JIRA_DEFAULT_PROJECT", "PROJ")
	t.Setenv("JIRA_BOT_JIRA_DEFAULT_ISSUE_TYPE", "Task")

	// Set client credentials
	t.Setenv("JIRA_BOT_JIRA_CLIENT_ID", "my-client-id")
	t.Setenv("JIRA_BOT_JIRA_CLIENT_SECRET", "my-client-secret")

	// Set K8s env vars for reader creation
	t.Setenv("POD_NAMESPACE", "default")
	t.Setenv("JIRA_BOT_TOKEN_SECRET_NAME", "bot-secret")

	// Set up mock reader factory that returns valid token data
	originalFactory := NewBotSecretReader
	defer func() { NewBotSecretReader = originalFactory }()
	NewBotSecretReader = func(namespace, secretName string) (BotSecretReader, error) {
		return &mockBotSecretReader{
			data: k8s.TokenData{
				RefreshToken: "my-refresh-token",
				CloudID:      "my-cloud-id",
			},
		}, nil
	}

	cfg := LoadConfig()

	assert.Equal(t, "oauth2", cfg.AuthMode)
	assert.Equal(t, "my-client-id", cfg.JiraClientID)
	assert.Equal(t, "my-client-secret", cfg.JiraClientSecret)
	assert.Equal(t, "my-refresh-token", cfg.TokenData.RefreshToken)
	assert.Equal(t, "my-cloud-id", cfg.TokenData.CloudID)
}

func TestLoadConfig_OAuthSetupMode_NoReader(t *testing.T) {
	// Set common required vars
	t.Setenv("JIRA_BOT_GITHUB_APP_ID", "12345")
	t.Setenv("JIRA_BOT_GITHUB_PRIVATE_KEY", "test-private-key")
	t.Setenv("JIRA_BOT_GITHUB_WEBHOOK_SECRET", "webhook-secret")
	t.Setenv("JIRA_BOT_JIRA_DEFAULT_PROJECT", "PROJ")
	t.Setenv("JIRA_BOT_JIRA_DEFAULT_ISSUE_TYPE", "Task")

	// Set only CLIENT_ID and CLIENT_SECRET (no K8s reader available)
	t.Setenv("JIRA_BOT_JIRA_CLIENT_ID", "my-client-id")
	t.Setenv("JIRA_BOT_JIRA_CLIENT_SECRET", "my-client-secret")
	t.Setenv("JIRA_BOT_OAUTH_CALLBACK_URL", "https://bot.example.com/jira/oauth/callback")

	// Ensure no reader factory is set
	originalFactory := NewBotSecretReader
	defer func() { NewBotSecretReader = originalFactory }()
	NewBotSecretReader = nil

	cfg := LoadConfig()

	assert.Equal(t, "oauth2-setup", cfg.AuthMode)
	assert.Equal(t, "my-client-id", cfg.JiraClientID)
	assert.Equal(t, "my-client-secret", cfg.JiraClientSecret)
	assert.Equal(t, "https://bot.example.com/jira/oauth/callback", cfg.OAuthCallbackURL)
}

func TestLoadConfig_OAuthMode_BothOAuthAndLegacySet_OAuthWins(t *testing.T) {
	// Set common required vars
	t.Setenv("JIRA_BOT_GITHUB_APP_ID", "12345")
	t.Setenv("JIRA_BOT_GITHUB_PRIVATE_KEY", "test-private-key")
	t.Setenv("JIRA_BOT_GITHUB_WEBHOOK_SECRET", "webhook-secret")
	t.Setenv("JIRA_BOT_JIRA_DEFAULT_PROJECT", "PROJ")
	t.Setenv("JIRA_BOT_JIRA_DEFAULT_ISSUE_TYPE", "Task")

	// Set client credentials
	t.Setenv("JIRA_BOT_JIRA_CLIENT_ID", "client-id")
	t.Setenv("JIRA_BOT_JIRA_CLIENT_SECRET", "client-secret")

	// Also set legacy vars (should not matter — OAuth path is taken)
	t.Setenv("JIRA_BOT_JIRA_BASE_URL", "https://jira.example.com")
	t.Setenv("JIRA_BOT_JIRA_USERNAME", "legacyuser")
	t.Setenv("JIRA_BOT_JIRA_TOKEN", "legacy-token")

	// Set K8s env vars and mock reader
	t.Setenv("POD_NAMESPACE", "default")
	t.Setenv("JIRA_BOT_TOKEN_SECRET_NAME", "bot-secret")

	originalFactory := NewBotSecretReader
	defer func() { NewBotSecretReader = originalFactory }()
	NewBotSecretReader = func(namespace, secretName string) (BotSecretReader, error) {
		return &mockBotSecretReader{
			data: k8s.TokenData{
				RefreshToken: "refresh-token",
				CloudID:      "cloud-id",
			},
		}, nil
	}

	cfg := LoadConfig()

	// OAuth should win when client credentials are present
	assert.Equal(t, "oauth2", cfg.AuthMode)
	assert.Equal(t, "client-id", cfg.JiraClientID)
	assert.Equal(t, "client-secret", cfg.JiraClientSecret)
	assert.Equal(t, "refresh-token", cfg.TokenData.RefreshToken)
	assert.Equal(t, "cloud-id", cfg.TokenData.CloudID)
}

func TestLoadConfig_BasicMode_AuthModeIsBasic(t *testing.T) {
	// Set common required vars + legacy Jira vars
	t.Setenv("JIRA_BOT_GITHUB_APP_ID", "12345")
	t.Setenv("JIRA_BOT_GITHUB_PRIVATE_KEY", "test-private-key")
	t.Setenv("JIRA_BOT_GITHUB_WEBHOOK_SECRET", "webhook-secret")
	t.Setenv("JIRA_BOT_JIRA_BASE_URL", "https://jira.example.com")
	t.Setenv("JIRA_BOT_JIRA_USERNAME", "testuser")
	t.Setenv("JIRA_BOT_JIRA_TOKEN", "test-token")
	t.Setenv("JIRA_BOT_JIRA_DEFAULT_PROJECT", "PROJ")
	t.Setenv("JIRA_BOT_JIRA_DEFAULT_ISSUE_TYPE", "Task")

	cfg := LoadConfig()

	assert.Equal(t, "basic", cfg.AuthMode)
	assert.Equal(t, "https://jira.example.com", cfg.JiraBaseURL)
	assert.Equal(t, "testuser", cfg.JiraUsername)
	assert.Equal(t, "test-token", cfg.JiraToken)
}



// TestLoadConfig_NoAuthConfig_Fatal verifies that LoadConfig terminates
// when no valid auth configuration is found (Requirement 1.5).
func TestLoadConfig_NoAuthConfig_Fatal(t *testing.T) {
	if os.Getenv("TEST_SUBPROCESS_FATAL") == "1" {
		// No client credentials and no basic auth vars set
		os.Setenv("JIRA_BOT_GITHUB_APP_ID", "123")
		os.Setenv("JIRA_BOT_GITHUB_PRIVATE_KEY", "key")
		os.Setenv("JIRA_BOT_GITHUB_WEBHOOK_SECRET", "secret")
		os.Setenv("JIRA_BOT_JIRA_DEFAULT_PROJECT", "PROJ")
		os.Setenv("JIRA_BOT_JIRA_DEFAULT_ISSUE_TYPE", "Task")
		// No JIRA_BOT_JIRA_CLIENT_ID, JIRA_BOT_JIRA_CLIENT_SECRET
		// No JIRA_BOT_JIRA_BASE_URL, JIRA_BOT_JIRA_USERNAME, JIRA_BOT_JIRA_TOKEN
		LoadConfig()
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestLoadConfig_NoAuthConfig_Fatal")
	cmd.Env = []string{
		"TEST_SUBPROCESS_FATAL=1",
		"HOME=" + os.Getenv("HOME"),
		"PATH=" + os.Getenv("PATH"),
	}
	err := cmd.Run()

	assert.Error(t, err, "LoadConfig should have exited fatally when no auth config is found")
	if exitErr, ok := err.(*exec.ExitError); ok {
		assert.False(t, exitErr.Success(), "Process should have exited with non-zero status")
	}
}

// --- Subprocess test helpers for fatal exit paths ---

// TestLoadConfig_MissingRequiredEnvVar_Fatal verifies that LoadConfig terminates
// when a required environment variable is not set (Requirement 7.3).
func TestLoadConfig_MissingRequiredEnvVar_Fatal(t *testing.T) {
	if os.Getenv("TEST_SUBPROCESS_FATAL") == "1" {
		// In subprocess: unset a required var and call LoadConfig.
		// JIRA_BOT_GITHUB_PRIVATE_KEY is required; omitting it should cause fatal.
		os.Setenv("JIRA_BOT_GITHUB_APP_ID", "123")
		// Do NOT set JIRA_BOT_GITHUB_PRIVATE_KEY
		os.Setenv("JIRA_BOT_GITHUB_WEBHOOK_SECRET", "secret")
		os.Setenv("JIRA_BOT_JIRA_BASE_URL", "https://jira.example.com")
		os.Setenv("JIRA_BOT_JIRA_USERNAME", "user")
		os.Setenv("JIRA_BOT_JIRA_TOKEN", "token")
		os.Setenv("JIRA_BOT_JIRA_DEFAULT_PROJECT", "PROJ")
		os.Setenv("JIRA_BOT_JIRA_DEFAULT_ISSUE_TYPE", "Task")
		LoadConfig()
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestLoadConfig_MissingRequiredEnvVar_Fatal")
	cmd.Env = append(os.Environ(), "TEST_SUBPROCESS_FATAL=1")
	err := cmd.Run()

	assert.Error(t, err, "LoadConfig should have exited fatally when a required env var is missing")
	if exitErr, ok := err.(*exec.ExitError); ok {
		assert.False(t, exitErr.Success(), "Process should have exited with non-zero status")
	}
}

// TestLoadConfig_NonNumericIntEnvVar_Fatal verifies that LoadConfig terminates
// when an integer environment variable contains a non-numeric value (Requirement 7.4).
func TestLoadConfig_NonNumericIntEnvVar_Fatal(t *testing.T) {
	if os.Getenv("TEST_SUBPROCESS_FATAL") == "1" {
		// In subprocess: set JIRA_BOT_GITHUB_APP_ID to a non-numeric value.
		os.Setenv("JIRA_BOT_GITHUB_APP_ID", "not-a-number")
		os.Setenv("JIRA_BOT_GITHUB_PRIVATE_KEY", "key")
		os.Setenv("JIRA_BOT_GITHUB_WEBHOOK_SECRET", "secret")
		os.Setenv("JIRA_BOT_JIRA_BASE_URL", "https://jira.example.com")
		os.Setenv("JIRA_BOT_JIRA_USERNAME", "user")
		os.Setenv("JIRA_BOT_JIRA_TOKEN", "token")
		os.Setenv("JIRA_BOT_JIRA_DEFAULT_PROJECT", "PROJ")
		os.Setenv("JIRA_BOT_JIRA_DEFAULT_ISSUE_TYPE", "Task")
		LoadConfig()
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestLoadConfig_NonNumericIntEnvVar_Fatal")
	cmd.Env = append(os.Environ(), "TEST_SUBPROCESS_FATAL=1")
	err := cmd.Run()

	assert.Error(t, err, "LoadConfig should have exited fatally when integer env var is non-numeric")
	if exitErr, ok := err.(*exec.ExitError); ok {
		assert.False(t, exitErr.Success(), "Process should have exited with non-zero status")
	}
}

// --- Token persistence config tests ---

func TestLoadConfig_TokenPersistence_DefaultValues(t *testing.T) {
	// Set common required vars (basic mode)
	t.Setenv("JIRA_BOT_GITHUB_APP_ID", "12345")
	t.Setenv("JIRA_BOT_GITHUB_PRIVATE_KEY", "test-private-key")
	t.Setenv("JIRA_BOT_GITHUB_WEBHOOK_SECRET", "webhook-secret")
	t.Setenv("JIRA_BOT_JIRA_BASE_URL", "https://jira.example.com")
	t.Setenv("JIRA_BOT_JIRA_USERNAME", "testuser")
	t.Setenv("JIRA_BOT_JIRA_TOKEN", "test-token")
	t.Setenv("JIRA_BOT_JIRA_DEFAULT_PROJECT", "PROJ")
	t.Setenv("JIRA_BOT_JIRA_DEFAULT_ISSUE_TYPE", "Task")

	cfg := LoadConfig()

	// When no token persistence env vars are set, fields use defaults
	assert.Equal(t, "", cfg.TokenSecretName)
	assert.Equal(t, "", cfg.TokenLeaseName)
	assert.Equal(t, "", cfg.PodName)
	assert.Equal(t, "", cfg.PodNamespace)
	assert.False(t, cfg.LeaderEnabled)
	assert.Equal(t, 30*time.Second, cfg.PollInterval)
	assert.Equal(t, 15*time.Second, cfg.LeaseDuration)
	assert.Equal(t, 10*time.Second, cfg.LeaseRenewDeadline)
}

func TestLoadConfig_TokenPersistence_AllFieldsSet(t *testing.T) {
	// Set common required vars (basic mode)
	t.Setenv("JIRA_BOT_GITHUB_APP_ID", "12345")
	t.Setenv("JIRA_BOT_GITHUB_PRIVATE_KEY", "test-private-key")
	t.Setenv("JIRA_BOT_GITHUB_WEBHOOK_SECRET", "webhook-secret")
	t.Setenv("JIRA_BOT_JIRA_BASE_URL", "https://jira.example.com")
	t.Setenv("JIRA_BOT_JIRA_USERNAME", "testuser")
	t.Setenv("JIRA_BOT_JIRA_TOKEN", "test-token")
	t.Setenv("JIRA_BOT_JIRA_DEFAULT_PROJECT", "PROJ")
	t.Setenv("JIRA_BOT_JIRA_DEFAULT_ISSUE_TYPE", "Task")

	// Set token persistence env vars
	t.Setenv("JIRA_BOT_TOKEN_SECRET_NAME", "my-bot-oauth-token")
	t.Setenv("JIRA_BOT_TOKEN_LEASE_NAME", "my-bot-token-leader")
	t.Setenv("POD_NAME", "jira-bot-abc123")
	t.Setenv("POD_NAMESPACE", "default")
	t.Setenv("JIRA_BOT_TOKEN_POLL_INTERVAL", "45s")
	t.Setenv("JIRA_BOT_LEASE_DURATION", "20s")
	t.Setenv("JIRA_BOT_LEASE_RENEW_DEADLINE", "12s")

	cfg := LoadConfig()

	assert.Equal(t, "my-bot-oauth-token", cfg.TokenSecretName)
	assert.Equal(t, "my-bot-token-leader", cfg.TokenLeaseName)
	assert.Equal(t, "jira-bot-abc123", cfg.PodName)
	assert.Equal(t, "default", cfg.PodNamespace)
	assert.True(t, cfg.LeaderEnabled)
	assert.Equal(t, 45*time.Second, cfg.PollInterval)
	assert.Equal(t, 20*time.Second, cfg.LeaseDuration)
	assert.Equal(t, 12*time.Second, cfg.LeaseRenewDeadline)
}

func TestLoadConfig_TokenPersistence_LeaderEnabled_RequiresBothPodFields(t *testing.T) {
	// Set common required vars (basic mode)
	t.Setenv("JIRA_BOT_GITHUB_APP_ID", "12345")
	t.Setenv("JIRA_BOT_GITHUB_PRIVATE_KEY", "test-private-key")
	t.Setenv("JIRA_BOT_GITHUB_WEBHOOK_SECRET", "webhook-secret")
	t.Setenv("JIRA_BOT_JIRA_BASE_URL", "https://jira.example.com")
	t.Setenv("JIRA_BOT_JIRA_USERNAME", "testuser")
	t.Setenv("JIRA_BOT_JIRA_TOKEN", "test-token")
	t.Setenv("JIRA_BOT_JIRA_DEFAULT_PROJECT", "PROJ")
	t.Setenv("JIRA_BOT_JIRA_DEFAULT_ISSUE_TYPE", "Task")

	// Only POD_NAME set, no POD_NAMESPACE
	t.Setenv("POD_NAME", "jira-bot-abc123")

	cfg := LoadConfig()
	assert.False(t, cfg.LeaderEnabled, "LeaderEnabled should be false when only POD_NAME is set")
}

func TestLoadConfig_TokenPersistence_InvalidDuration_UsesDefault(t *testing.T) {
	// Set common required vars (basic mode)
	t.Setenv("JIRA_BOT_GITHUB_APP_ID", "12345")
	t.Setenv("JIRA_BOT_GITHUB_PRIVATE_KEY", "test-private-key")
	t.Setenv("JIRA_BOT_GITHUB_WEBHOOK_SECRET", "webhook-secret")
	t.Setenv("JIRA_BOT_JIRA_BASE_URL", "https://jira.example.com")
	t.Setenv("JIRA_BOT_JIRA_USERNAME", "testuser")
	t.Setenv("JIRA_BOT_JIRA_TOKEN", "test-token")
	t.Setenv("JIRA_BOT_JIRA_DEFAULT_PROJECT", "PROJ")
	t.Setenv("JIRA_BOT_JIRA_DEFAULT_ISSUE_TYPE", "Task")

	// Set an invalid duration value
	t.Setenv("JIRA_BOT_TOKEN_POLL_INTERVAL", "not-a-duration")

	cfg := LoadConfig()
	assert.Equal(t, 30*time.Second, cfg.PollInterval, "should fall back to default on invalid duration")
}



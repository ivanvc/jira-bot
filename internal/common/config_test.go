package common

import (
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestLoadConfig_AllRequiredEnvVarsSet(t *testing.T) {
	// Set all required environment variables
	t.Setenv("JIRA_BOT_LISTEN_HTTP", ":9090")
	t.Setenv("JIRA_BOT_GITHUB_APP_ID", "12345")
	t.Setenv("JIRA_BOT_GITHUB_PRIVATE_KEY", "test-private-key")
	t.Setenv("JIRA_BOT_GITHUB_WEBHOOK_SECRET", "webhook-secret")
	t.Setenv("JIRA_BOT_JIRA_DEFAULT_PROJECT", "PROJ")
	t.Setenv("JIRA_BOT_JIRA_DEFAULT_ISSUE_TYPE", "Task")

	cfg := LoadConfig()

	assert.Equal(t, ":9090", cfg.ListenHTTP)
	assert.Equal(t, int64(12345), cfg.GitHubAppID)
	assert.Equal(t, "test-private-key", cfg.GitHubPrivateKey)
	assert.Equal(t, "webhook-secret", cfg.GitHubWebhookSecret)
	assert.Equal(t, "PROJ", cfg.JiraDefaultProject)
	assert.Equal(t, "Task", cfg.JiraDefaultIssueType)
}

func TestLoadConfig_OptionalEnvVarUsesDefault(t *testing.T) {
	// Do NOT set JIRA_BOT_LISTEN_HTTP — it should default to ":8080"
	t.Setenv("JIRA_BOT_GITHUB_APP_ID", "99")
	t.Setenv("JIRA_BOT_GITHUB_PRIVATE_KEY", "key")
	t.Setenv("JIRA_BOT_GITHUB_WEBHOOK_SECRET", "secret")
	t.Setenv("JIRA_BOT_JIRA_DEFAULT_PROJECT", "DEF")
	t.Setenv("JIRA_BOT_JIRA_DEFAULT_ISSUE_TYPE", "Story")

	cfg := LoadConfig()

	assert.Equal(t, ":8080", cfg.ListenHTTP, "ListenHTTP should use default value when env var is not set")
}

// --- Subprocess test helpers for fatal exit paths ---

// TestLoadConfig_MissingRequiredEnvVar_Fatal verifies that LoadConfig terminates
// when a required environment variable is not set.
func TestLoadConfig_MissingRequiredEnvVar_Fatal(t *testing.T) {
	if os.Getenv("TEST_SUBPROCESS_FATAL") == "1" {
		// In subprocess: unset a required var and call LoadConfig.
		// JIRA_BOT_GITHUB_PRIVATE_KEY is required; omitting it should cause fatal.
		os.Setenv("JIRA_BOT_GITHUB_APP_ID", "123")
		// Do NOT set JIRA_BOT_GITHUB_PRIVATE_KEY
		os.Setenv("JIRA_BOT_GITHUB_WEBHOOK_SECRET", "secret")
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
// when an integer environment variable contains a non-numeric value.
func TestLoadConfig_NonNumericIntEnvVar_Fatal(t *testing.T) {
	if os.Getenv("TEST_SUBPROCESS_FATAL") == "1" {
		// In subprocess: set JIRA_BOT_GITHUB_APP_ID to a non-numeric value.
		os.Setenv("JIRA_BOT_GITHUB_APP_ID", "not-a-number")
		os.Setenv("JIRA_BOT_GITHUB_PRIVATE_KEY", "key")
		os.Setenv("JIRA_BOT_GITHUB_WEBHOOK_SECRET", "secret")
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

// --- Leader election config tests ---

func TestLoadConfig_LeaderEnabled_RequiresBothPodFields(t *testing.T) {
	t.Setenv("JIRA_BOT_GITHUB_APP_ID", "12345")
	t.Setenv("JIRA_BOT_GITHUB_PRIVATE_KEY", "test-private-key")
	t.Setenv("JIRA_BOT_GITHUB_WEBHOOK_SECRET", "webhook-secret")
	t.Setenv("JIRA_BOT_JIRA_DEFAULT_PROJECT", "PROJ")
	t.Setenv("JIRA_BOT_JIRA_DEFAULT_ISSUE_TYPE", "Task")

	// Only POD_NAME set, no POD_NAMESPACE
	t.Setenv("POD_NAME", "jira-bot-abc123")

	cfg := LoadConfig()
	assert.False(t, cfg.LeaderEnabled, "LeaderEnabled should be false when only POD_NAME is set")
}

func TestLoadConfig_LeaderEnabled_BothFieldsSet(t *testing.T) {
	t.Setenv("JIRA_BOT_GITHUB_APP_ID", "12345")
	t.Setenv("JIRA_BOT_GITHUB_PRIVATE_KEY", "test-private-key")
	t.Setenv("JIRA_BOT_GITHUB_WEBHOOK_SECRET", "webhook-secret")
	t.Setenv("JIRA_BOT_JIRA_DEFAULT_PROJECT", "PROJ")
	t.Setenv("JIRA_BOT_JIRA_DEFAULT_ISSUE_TYPE", "Task")
	t.Setenv("POD_NAME", "jira-bot-abc123")
	t.Setenv("POD_NAMESPACE", "default")

	cfg := LoadConfig()
	assert.True(t, cfg.LeaderEnabled)
}

// --- Per-user token config tests ---

func TestLoadConfig_PerUserToken_DefaultValues(t *testing.T) {
	t.Setenv("JIRA_BOT_GITHUB_APP_ID", "12345")
	t.Setenv("JIRA_BOT_GITHUB_PRIVATE_KEY", "test-private-key")
	t.Setenv("JIRA_BOT_GITHUB_WEBHOOK_SECRET", "webhook-secret")
	t.Setenv("JIRA_BOT_JIRA_DEFAULT_PROJECT", "PROJ")
	t.Setenv("JIRA_BOT_JIRA_DEFAULT_ISSUE_TYPE", "Task")

	cfg := LoadConfig()

	assert.Equal(t, "", cfg.GitHubAppClientSecret)
	assert.Equal(t, "", cfg.UserTokenSecretName)
	assert.Equal(t, "", cfg.UserAuthCallbackURL)
	assert.Equal(t, 30*time.Second, cfg.RefreshCheckInterval, "RefreshCheckInterval should default to 30s")
}

func TestLoadConfig_PerUserToken_AllFieldsSet(t *testing.T) {
	t.Setenv("JIRA_BOT_GITHUB_APP_ID", "12345")
	t.Setenv("JIRA_BOT_GITHUB_PRIVATE_KEY", "test-private-key")
	t.Setenv("JIRA_BOT_GITHUB_WEBHOOK_SECRET", "webhook-secret")
	t.Setenv("JIRA_BOT_JIRA_DEFAULT_PROJECT", "PROJ")
	t.Setenv("JIRA_BOT_JIRA_DEFAULT_ISSUE_TYPE", "Task")

	// Set per-user token env vars
	t.Setenv("JIRA_BOT_GITHUB_APP_CLIENT_SECRET", "gh-client-secret-123")
	t.Setenv("JIRA_BOT_USER_TOKEN_SECRET_NAME", "jira-bot-user-tokens")
	t.Setenv("JIRA_BOT_USER_AUTH_CALLBACK_URL", "https://bot.example.com/oauth/user")
	t.Setenv("JIRA_BOT_REFRESH_CHECK_INTERVAL", "60s")

	cfg := LoadConfig()

	assert.Equal(t, "gh-client-secret-123", cfg.GitHubAppClientSecret)
	assert.Equal(t, "jira-bot-user-tokens", cfg.UserTokenSecretName)
	assert.Equal(t, "https://bot.example.com/oauth/user", cfg.UserAuthCallbackURL)
	assert.Equal(t, 60*time.Second, cfg.RefreshCheckInterval)
}

func TestLoadConfig_PerUserToken_RefreshCheckInterval_ClampedToMin(t *testing.T) {
	t.Setenv("JIRA_BOT_GITHUB_APP_ID", "12345")
	t.Setenv("JIRA_BOT_GITHUB_PRIVATE_KEY", "test-private-key")
	t.Setenv("JIRA_BOT_GITHUB_WEBHOOK_SECRET", "webhook-secret")
	t.Setenv("JIRA_BOT_JIRA_DEFAULT_PROJECT", "PROJ")
	t.Setenv("JIRA_BOT_JIRA_DEFAULT_ISSUE_TYPE", "Task")

	// Set below minimum (10s)
	t.Setenv("JIRA_BOT_REFRESH_CHECK_INTERVAL", "5s")

	cfg := LoadConfig()

	assert.Equal(t, 10*time.Second, cfg.RefreshCheckInterval, "RefreshCheckInterval should be clamped to minimum 10s")
}

func TestLoadConfig_PerUserToken_RefreshCheckInterval_ClampedToMax(t *testing.T) {
	t.Setenv("JIRA_BOT_GITHUB_APP_ID", "12345")
	t.Setenv("JIRA_BOT_GITHUB_PRIVATE_KEY", "test-private-key")
	t.Setenv("JIRA_BOT_GITHUB_WEBHOOK_SECRET", "webhook-secret")
	t.Setenv("JIRA_BOT_JIRA_DEFAULT_PROJECT", "PROJ")
	t.Setenv("JIRA_BOT_JIRA_DEFAULT_ISSUE_TYPE", "Task")

	// Set above maximum (300s)
	t.Setenv("JIRA_BOT_REFRESH_CHECK_INTERVAL", "600s")

	cfg := LoadConfig()

	assert.Equal(t, 300*time.Second, cfg.RefreshCheckInterval, "RefreshCheckInterval should be clamped to maximum 300s")
}

func TestLoadConfig_PerUserToken_RefreshCheckInterval_InvalidUsesDefault(t *testing.T) {
	t.Setenv("JIRA_BOT_GITHUB_APP_ID", "12345")
	t.Setenv("JIRA_BOT_GITHUB_PRIVATE_KEY", "test-private-key")
	t.Setenv("JIRA_BOT_GITHUB_WEBHOOK_SECRET", "webhook-secret")
	t.Setenv("JIRA_BOT_JIRA_DEFAULT_PROJECT", "PROJ")
	t.Setenv("JIRA_BOT_JIRA_DEFAULT_ISSUE_TYPE", "Task")

	// Set an invalid duration
	t.Setenv("JIRA_BOT_REFRESH_CHECK_INTERVAL", "invalid")

	cfg := LoadConfig()

	assert.Equal(t, 30*time.Second, cfg.RefreshCheckInterval, "RefreshCheckInterval should use default 30s for invalid input")
}

func TestLoadConfig_InvalidDuration_UsesDefault(t *testing.T) {
	t.Setenv("JIRA_BOT_GITHUB_APP_ID", "12345")
	t.Setenv("JIRA_BOT_GITHUB_PRIVATE_KEY", "test-private-key")
	t.Setenv("JIRA_BOT_GITHUB_WEBHOOK_SECRET", "webhook-secret")
	t.Setenv("JIRA_BOT_JIRA_DEFAULT_PROJECT", "PROJ")
	t.Setenv("JIRA_BOT_JIRA_DEFAULT_ISSUE_TYPE", "Task")

	// Set an invalid duration value
	t.Setenv("JIRA_BOT_LEASE_DURATION", "not-a-duration")

	cfg := LoadConfig()
	assert.Equal(t, 15*time.Second, cfg.LeaseDuration, "should fall back to default on invalid duration")
}

package common

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"testing/quick"

	"github.com/stretchr/testify/assert"
)

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

// --- Property-based tests ---

// Feature: jira-oauth2-migration, Property 1: Config validation names all missing variables
// **Validates: Requirements 1.5**
//
// For any non-empty subset of the four OAuth environment variables that is missing
// or empty, the fatal error message produced by LoadConfig SHALL contain the name of
// every missing/empty variable from that subset.
func TestProperty_ConfigValidationNamesAllMissingVars(t *testing.T) {
	// We test ValidateOAuthEnv directly since it produces the error message
	// that LoadConfig passes to log.Fatalf. This avoids subprocess gymnastics
	// while validating the same property.

	cfg := &quick.Config{MaxCount: 100}

	// Generator: random bitmask 1..14 representing which of the 4 OAuth vars are missing.
	// Bitmask 0 (none missing = all present) and 15 (all missing = no OAuth attempted) are
	// excluded because the property only applies when at least one var is set and at least
	// one is missing (partial configuration).
	property := func(bitmask uint8) bool {
		// Map bitmask to range 1..14 (non-empty proper subset of {0,1,2,3})
		subset := (bitmask % 14) + 1 // 1..14 inclusive

		// Clear all OAuth env vars first
		for _, name := range OAuthEnvVars {
			os.Unsetenv(name)
		}

		// Set the vars whose bits are 1 (these are present); bits that are 0 are missing
		var expectedMissing []string
		for i := 0; i < 4; i++ {
			if subset&(1<<i) != 0 {
				// Bit is set → this var is present
				os.Setenv(OAuthEnvVars[i], "some-value")
			} else {
				// Bit is not set → this var is missing
				expectedMissing = append(expectedMissing, OAuthEnvVars[i])
			}
		}

		// We only care about cases where the config is partial (some present, some missing)
		if len(expectedMissing) == 0 || len(expectedMissing) == 4 {
			// All present or all missing: not a partial config, skip
			return true
		}

		allPresent, err := ValidateOAuthEnv()
		if allPresent {
			// Should not be all-present if we have missing vars
			return false
		}
		if err == nil {
			// Should have an error when partial config is detected
			return false
		}

		errMsg := err.Error()
		for _, name := range expectedMissing {
			if !strings.Contains(errMsg, name) {
				t.Logf("Error message %q does not contain expected var name %q", errMsg, name)
				return false
			}
		}
		return true
	}

	if err := quick.Check(property, cfg); err != nil {
		t.Errorf("Property 1 failed: %v", err)
	}

	// Cleanup
	for _, name := range OAuthEnvVars {
		os.Unsetenv(name)
	}
}

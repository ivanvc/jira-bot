package k8s

import (
	"bytes"
	"testing"
	"time"

	"github.com/charmbracelet/log"
	"github.com/stretchr/testify/assert"
)

func newTestLogger(buf *bytes.Buffer) *log.Logger {
	l := log.New(buf)
	l.SetReportTimestamp(false)
	return l
}

func TestPickRefreshToken_PrefersSecretToken(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf)

	secretData := TokenData{
		RefreshToken: "secret-token",
		AccessToken:  "access",
		ExpiresAt:    time.Now().Add(time.Hour),
	}

	result := PickRefreshToken(secretData, "env-token", logger)

	assert.Equal(t, "secret-token", result)
	assert.Contains(t, buf.String(), "Bot_Secret")
}

func TestPickRefreshToken_FallsBackToEnvVar(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf)

	secretData := TokenData{
		RefreshToken: "",
		AccessToken:  "",
		ExpiresAt:    time.Time{},
	}

	result := PickRefreshToken(secretData, "env-token", logger)

	assert.Equal(t, "env-token", result)
	assert.Contains(t, buf.String(), "environment variable")
}

func TestPickRefreshToken_EmptySecretWithEmptyEnvVar(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf)

	secretData := TokenData{}

	result := PickRefreshToken(secretData, "", logger)

	assert.Equal(t, "", result)
	assert.Contains(t, buf.String(), "environment variable")
}

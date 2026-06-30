package k8s

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToSecretData_WithNonEmptyCloudID_IncludesCloudIDKey(t *testing.T) {
	td := TokenData{
		RefreshToken: "refresh",
		AccessToken:  "access",
		ExpiresAt:    time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		CloudID:      "my-cloud-id",
	}

	data := td.ToSecretData()

	assert.Equal(t, []byte("my-cloud-id"), data[KeyCloudID])
	assert.Equal(t, []byte("refresh"), data[KeyRefreshToken])
	assert.Equal(t, []byte("access"), data[KeyAccessToken])
}

func TestToSecretData_WithEmptyCloudID_OmitsCloudIDKey(t *testing.T) {
	td := TokenData{
		RefreshToken: "refresh",
		AccessToken:  "access",
		ExpiresAt:    time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		CloudID:      "",
	}

	data := td.ToSecretData()

	_, exists := data[KeyCloudID]
	assert.False(t, exists, "cloud-id key should not be present when CloudID is empty")
}

func TestTokenDataFromSecret_WithCloudIDKey(t *testing.T) {
	data := map[string][]byte{
		KeyRefreshToken: []byte("refresh"),
		KeyAccessToken:  []byte("access"),
		KeyExpiresAt:    []byte("2025-01-01T00:00:00Z"),
		KeyCloudID:      []byte("cloud-123"),
	}

	td, err := TokenDataFromSecret(data)

	require.NoError(t, err)
	assert.Equal(t, "cloud-123", td.CloudID)
}

func TestTokenDataFromSecret_WithMissingCloudIDKey(t *testing.T) {
	data := map[string][]byte{
		KeyRefreshToken: []byte("refresh"),
		KeyAccessToken:  []byte("access"),
		KeyExpiresAt:    []byte("2025-01-01T00:00:00Z"),
	}

	td, err := TokenDataFromSecret(data)

	require.NoError(t, err)
	assert.Equal(t, "", td.CloudID)
}

func TestTokenDataFromSecret_WithEmptyCloudIDValue(t *testing.T) {
	data := map[string][]byte{
		KeyRefreshToken: []byte("refresh"),
		KeyAccessToken:  []byte("access"),
		KeyExpiresAt:    []byte("2025-01-01T00:00:00Z"),
		KeyCloudID:      []byte(""),
	}

	td, err := TokenDataFromSecret(data)

	require.NoError(t, err)
	assert.Equal(t, "", td.CloudID)
}

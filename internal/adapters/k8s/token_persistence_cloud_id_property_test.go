package k8s

import (
	"math/rand"
	"testing"
	"testing/quick"
	"time"

	"github.com/stretchr/testify/assert"
)

// Feature: oauth-auto-setup, Property 1: TokenData serialization round-trip
// **Validates: Requirements 7.1, 7.3**
//
// For any valid TokenData struct (with non-empty RefreshToken, AccessToken, a valid
// ExpiresAt time, and any CloudID string including empty), serializing via
// ToSecretData() and then deserializing via TokenDataFromSecret() SHALL produce a
// TokenData value equivalent to the original (with time truncated to second
// precision due to RFC3339 formatting).
func TestProperty1_TokenDataSerializationRoundTrip(t *testing.T) {
	cfg := &quick.Config{MaxCount: 100}

	f := func(seed int64) bool {
		rng := rand.New(rand.NewSource(seed))

		// Generate random token strings (non-empty)
		refreshToken := randomString(rng, 1+rng.Intn(64))
		accessToken := randomString(rng, 1+rng.Intn(64))

		// Generate random CloudID: sometimes empty, sometimes non-empty
		var cloudID string
		if rng.Intn(4) > 0 { // 75% chance of non-empty CloudID
			cloudID = randomString(rng, 1+rng.Intn(32))
		}

		// Generate a random time (truncated to second precision since RFC3339
		// serialization drops sub-second precision)
		randomTime := time.Unix(rng.Int63n(4102444800), 0).UTC() // up to year 2100

		original := TokenData{
			RefreshToken: refreshToken,
			AccessToken:  accessToken,
			ExpiresAt:    randomTime,
			CloudID:      cloudID,
		}

		// Serialize
		secretData := original.ToSecretData()

		// Deserialize
		restored, err := TokenDataFromSecret(secretData)
		if err != nil {
			t.Logf("TokenDataFromSecret failed: %v", err)
			return false
		}

		// Assert equality
		if restored.RefreshToken != original.RefreshToken {
			t.Logf("RefreshToken mismatch: got %q, want %q", restored.RefreshToken, original.RefreshToken)
			return false
		}
		if restored.AccessToken != original.AccessToken {
			t.Logf("AccessToken mismatch: got %q, want %q", restored.AccessToken, original.AccessToken)
			return false
		}
		if !restored.ExpiresAt.Equal(original.ExpiresAt) {
			t.Logf("ExpiresAt mismatch: got %v, want %v", restored.ExpiresAt, original.ExpiresAt)
			return false
		}
		if restored.CloudID != original.CloudID {
			t.Logf("CloudID mismatch: got %q, want %q", restored.CloudID, original.CloudID)
			return false
		}

		return true
	}

	if err := quick.Check(f, cfg); err != nil {
		assert.NoError(t, err, "Property 1 failed: TokenData serialization round-trip must preserve all fields")
	}
}

// randomString generates a random alphanumeric string of the given length.
func randomString(rng *rand.Rand, length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_"
	b := make([]byte, length)
	for i := range b {
		b[i] = charset[rng.Intn(len(charset))]
	}
	return string(b)
}

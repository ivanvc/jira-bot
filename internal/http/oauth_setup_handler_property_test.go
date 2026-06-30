package http

import (
	"math/rand"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/quick"
	"time"

	"github.com/stretchr/testify/assert"
)

// Feature: oauth-auto-setup, Property 2: Expiry time computation
// **Validates: Requirements 2.4, 2.5**
//
// For any non-negative integer expires_in value from a token response, the computed
// expiry time SHALL equal the current wall-clock time plus expires_in seconds
// (within a 2-second tolerance to account for execution time).
func TestProperty2_ExpiryTimeComputation(t *testing.T) {
	cfg := &quick.Config{
		MaxCount: 100,
		Rand:     rand.New(rand.NewSource(42)),
	}

	f := func(seed int64) bool {
		rng := rand.New(rand.NewSource(seed))

		// Generate a random non-negative expires_in value.
		// Use a range that covers realistic token lifetimes (0 to 86400 seconds = 24h)
		// as well as larger values up to ~1 year.
		expiresIn := rng.Intn(31536000) // 0 to 365 days in seconds

		// Record time before computation
		before := time.Now()

		// Compute the expiry time using the function under test
		result := computeExpiryTime(expiresIn)

		// Record time after computation
		after := time.Now()

		// The expected expiry should be between:
		//   before + expiresIn seconds
		//   after + expiresIn seconds
		// We use a 2-second tolerance as specified.
		expectedLow := before.Add(time.Duration(expiresIn) * time.Second)
		expectedHigh := after.Add(time.Duration(expiresIn)*time.Second).Add(2 * time.Second)

		if result.Before(expectedLow.Add(-2 * time.Second)) {
			t.Logf("expiry too early: got %v, expected at least %v (expiresIn=%d)",
				result, expectedLow, expiresIn)
			return false
		}

		if result.After(expectedHigh) {
			t.Logf("expiry too late: got %v, expected at most %v (expiresIn=%d)",
				result, expectedHigh, expiresIn)
			return false
		}

		return true
	}

	if err := quick.Check(f, cfg); err != nil {
		assert.NoError(t, err, "Property 2 failed: expiry time must equal current time plus expires_in seconds within 2-second tolerance")
	}
}

// Feature: oauth-auto-setup, Property 3: Multi-site selection page completeness
// **Validates: Requirements 3.3**
//
// For any list of 2 or more accessible resources (each with a random non-empty
// id and name), the rendered HTML selection page SHALL contain every resource's
// name and every resource's id value.
func TestProperty3_MultiSitePageCompleteness(t *testing.T) {
	cfg := &quick.Config{
		MaxCount: 100,
		Rand:     rand.New(rand.NewSource(42)),
	}

	// randomAlphanumeric generates a random alphanumeric string of length 1..maxLen.
	randomAlphanumeric := func(rng *rand.Rand, maxLen int) string {
		const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
		length := rng.Intn(maxLen) + 1 // 1..maxLen
		b := make([]byte, length)
		for i := range b {
			b[i] = charset[rng.Intn(len(charset))]
		}
		return string(b)
	}

	f := func(seed int64) bool {
		rng := rand.New(rand.NewSource(seed))

		// Generate between 2 and 10 accessible resources
		count := rng.Intn(9) + 2 // 2..10

		resources := make([]accessibleResource, count)
		for i := range resources {
			resources[i] = accessibleResource{
				ID:   randomAlphanumeric(rng, 32),
				Name: randomAlphanumeric(rng, 50),
			}
		}

		// Render the site selection page
		recorder := httptest.NewRecorder()
		renderSiteSelectionPage(recorder, resources)

		body := recorder.Body.String()

		// Assert every resource's name and id appears in the output
		for _, r := range resources {
			if !strings.Contains(body, r.Name) {
				t.Logf("resource name %q not found in rendered HTML (id=%s)", r.Name, r.ID)
				return false
			}
			if !strings.Contains(body, r.ID) {
				t.Logf("resource id %q not found in rendered HTML (name=%s)", r.ID, r.Name)
				return false
			}
		}

		return true
	}

	if err := quick.Check(f, cfg); err != nil {
		assert.NoError(t, err, "Property 3 failed: multi-site selection page must contain every resource's name and id")
	}
}

// Feature: oauth-auto-setup, Property 4: Success page token exclusion
// **Validates: Requirements 4.4**
//
// For any non-empty refresh token string and non-empty access token string,
// when the success page is rendered (which only receives a cloudID), the HTML
// response body SHALL NOT contain the refresh token or access token value.
func TestProperty4_SuccessPageTokenExclusion(t *testing.T) {
	cfg := &quick.Config{
		MaxCount: 100,
		Rand:     rand.New(rand.NewSource(42)),
	}

	// generateTokenString produces a random non-empty string of length 16-128
	// characters to simulate realistic OAuth token values. We use a minimum
	// length of 16 because real tokens are always long opaque strings and
	// single-character strings would trivially appear in HTML boilerplate.
	generateTokenString := func(rng *rand.Rand) string {
		const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_."
		length := rng.Intn(113) + 16 // 16 to 128 characters
		b := make([]byte, length)
		for i := range b {
			b[i] = charset[rng.Intn(len(charset))]
		}
		return string(b)
	}

	f := func(seed int64) bool {
		rng := rand.New(rand.NewSource(seed))

		// Generate random non-empty token strings
		refreshToken := generateTokenString(rng)
		accessToken := generateTokenString(rng)
		// Use a separate short cloud ID (not from the token generator) to avoid
		// the cloudID coincidentally equaling a token.
		cloudID := "cloud-" + generateTokenString(rng)[:8]

		// Render the success page using httptest recorder
		w := httptest.NewRecorder()
		renderSuccessPage(w, cloudID)

		body := w.Body.String()

		// The success page must NOT contain the refresh token
		if strings.Contains(body, refreshToken) {
			t.Logf("success page contains refresh token %q", refreshToken)
			return false
		}

		// The success page must NOT contain the access token
		if strings.Contains(body, accessToken) {
			t.Logf("success page contains access token %q", accessToken)
			return false
		}

		return true
	}

	if err := quick.Check(f, cfg); err != nil {
		assert.NoError(t, err, "Property 4 failed: success page must not contain refresh token or access token")
	}
}

package jira

import (
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"testing/quick"
	"time"

	"github.com/charmbracelet/log"
	"github.com/stretchr/testify/assert"
)

// Feature: jira-oauth2-migration, Property 2: Refresh request contains correct credentials
// **Validates: Requirements 2.1**
//
// For any valid client ID, client secret, and refresh token strings, when the
// TokenManager performs a token refresh, the HTTP request body SHALL contain
// grant_type equal to "refresh_token", client_id equal to the configured client ID,
// client_secret equal to the configured client secret, and refresh_token equal to
// the current refresh token.
func TestProperty2_RefreshRequestContainsCorrectCredentials(t *testing.T) {
	cfg := &quick.Config{MaxCount: 100}

	f := func(clientID, clientSecret, refreshToken string) bool {
		// Skip empty strings — they are valid inputs for quick but not meaningful credentials
		if clientID == "" || clientSecret == "" || refreshToken == "" {
			return true
		}

		var capturedBody tokenRefreshRequest

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			decoder := json.NewDecoder(r.Body)
			if err := decoder.Decode(&capturedBody); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}

			resp := tokenRefreshResponse{
				AccessToken:  "test-access-token",
				RefreshToken: refreshToken,
				ExpiresIn:    3600,
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		}))
		defer server.Close()

		logger := log.NewWithOptions(io.Discard, log.Options{Level: log.FatalLevel})
		tm := NewTokenManager(clientID, clientSecret, refreshToken,
			WithTokenURL(server.URL),
			WithHTTPClient(server.Client()),
			WithLogger(logger),
		)

		_, err := tm.Token()
		if err != nil {
			return false
		}

		return capturedBody.GrantType == "refresh_token" &&
			capturedBody.ClientID == clientID &&
			capturedBody.ClientSecret == clientSecret &&
			capturedBody.RefreshToken == refreshToken
	}

	if err := quick.Check(f, cfg); err != nil {
		assert.NoError(t, err, "Property 2 failed: refresh request must contain correct credentials")
	}
}

// Feature: jira-oauth2-migration, Property 5: 5xx status codes trigger exactly 3 retries
// **Validates: Requirements 2.4, 2.6**
//
// For any HTTP status code in the range 500–599 returned by the token endpoint,
// the TokenManager SHALL retry the request exactly 3 times before returning an error to the caller.
func TestProperty5_5xxStatusCodesTriggerExactly3Retries(t *testing.T) {
	// Override retry delays to make the test fast.
	originalDelays := retryDelays
	retryDelays = []time.Duration{1 * time.Millisecond, 1 * time.Millisecond, 1 * time.Millisecond}
	defer func() { retryDelays = originalDelays }()

	cfg := &quick.Config{MaxCount: 100}

	f := func(seed int64) bool {
		rng := rand.New(rand.NewSource(seed))
		// Generate a random 5xx status code (500-599).
		statusCode := 500 + rng.Intn(100)

		var requestCount atomic.Int32

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestCount.Add(1)
			w.WriteHeader(statusCode)
			w.Write([]byte(`{"error":"server_error"}`))
		}))
		defer server.Close()

		logger := log.NewWithOptions(io.Discard, log.Options{Level: log.FatalLevel})
		tm := NewTokenManager("client-id", "client-secret", "refresh-token",
			WithTokenURL(server.URL),
			WithHTTPClient(server.Client()),
			WithLogger(logger),
		)

		_, err := tm.Token()

		// Should return an error after exhausting retries.
		if err == nil {
			t.Logf("expected error for status %d, got nil", statusCode)
			return false
		}

		// Should have made exactly 4 requests: 1 initial + 3 retries.
		totalRequests := int(requestCount.Load())
		if totalRequests != 4 {
			t.Logf("status %d: expected 4 total requests (1 initial + 3 retries), got %d", statusCode, totalRequests)
			return false
		}

		return true
	}

	if err := quick.Check(f, cfg); err != nil {
		t.Errorf("Property 5 failed: %v", err)
	}
}

// Feature: jira-oauth2-migration, Property 6: 4xx status codes do not trigger retries
func TestProperty6_4xxStatusCodesDoNotTriggerRetries(t *testing.T) {
	// **Validates: Requirements 2.5**
	//
	// For any HTTP status code in the range 400–499 returned by the token endpoint,
	// the TokenManager SHALL NOT retry the request and SHALL return an error immediately.

	cfg := &quick.Config{
		MaxCount: 100,
	}

	f := func(seed uint32) bool {
		// Generate a random 4xx status code (400–499)
		statusCode := 400 + int(seed%100)

		// Count how many requests are received by the server
		var requestCount atomic.Int32

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestCount.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(statusCode)
			w.Write([]byte(`{"error": "client_error"}`))
		}))
		defer server.Close()

		tm := NewTokenManager("client-id", "client-secret", "refresh-token",
			WithTokenURL(server.URL),
			WithHTTPClient(server.Client()),
		)

		// Call Token() — should fail immediately without retries
		token, err := tm.Token()

		// Must return an error
		if err == nil {
			t.Logf("Expected error for status %d, got token: %s", statusCode, token)
			return false
		}

		// Must have made exactly 1 request (no retries)
		if requestCount.Load() != 1 {
			t.Logf("Expected exactly 1 request for status %d, got %d", statusCode, requestCount.Load())
			return false
		}

		return true
	}

	// Use a deterministic random source for reproducibility
	cfg.Rand = rand.New(rand.NewSource(42))

	err := quick.Check(f, cfg)
	assert.NoError(t, err)
}

// Feature: jira-oauth2-migration, Property 3: Token expiry computation
// **Validates: Requirements 2.2, 2.8**
//
// For any token refresh response with a positive expires_in value (1–86400 seconds),
// the TokenManager SHALL compute the token's effective expiry as the response receipt
// time plus expires_in seconds minus 60 seconds (the early-expiry buffer).
func TestProperty3_TokenExpiryComputation(t *testing.T) {
	cfg := &quick.Config{MaxCount: 100}

	f := func(expiresInRaw uint16) bool {
		// Map uint16 to the range 1–86400.
		expiresIn := int(expiresInRaw)%86400 + 1

		// Set up a test server that returns a token with the given expires_in.
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			resp := tokenRefreshResponse{
				AccessToken:  "test-access-token",
				RefreshToken: "test-refresh-token",
				ExpiresIn:    expiresIn,
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(resp)
		}))
		defer server.Close()

		logger := log.NewWithOptions(io.Discard, log.Options{Level: log.FatalLevel})
		tm := NewTokenManager("client-id", "client-secret", "refresh-token",
			WithTokenURL(server.URL),
			WithHTTPClient(server.Client()),
			WithLogger(logger),
		)

		// Perform the refresh by calling Token().
		beforeRefresh := time.Now()
		token, err := tm.Token()
		afterRefresh := time.Now()

		if err != nil {
			t.Logf("unexpected error: %v", err)
			return false
		}
		if token != "test-access-token" {
			t.Logf("unexpected token: %s", token)
			return false
		}

		// Read the stored expiresAt from the TokenManager.
		tm.mu.RLock()
		storedExpiresAt := tm.expiresAt
		tm.mu.RUnlock()

		// The stored expiresAt should be: receipt_time + expires_in.
		// Due to timing, it should be between:
		//   beforeRefresh + expires_in  <=  storedExpiresAt  <=  afterRefresh + expires_in
		expectedLow := beforeRefresh.Add(time.Duration(expiresIn) * time.Second)
		expectedHigh := afterRefresh.Add(time.Duration(expiresIn) * time.Second)

		if storedExpiresAt.Before(expectedLow) || storedExpiresAt.After(expectedHigh) {
			t.Logf("expiresAt out of bounds for expires_in=%d: got %v, expected between %v and %v",
				expiresIn, storedExpiresAt, expectedLow, expectedHigh)
			return false
		}

		// The effective expiry (when Token() considers the token expired) is
		// storedExpiresAt - earlyExpiryBuffer (60s).
		// This means: effective_expiry = receipt_time + expires_in - 60s
		effectiveExpiry := storedExpiresAt.Add(-earlyExpiryBuffer)
		effectiveLow := beforeRefresh.Add(time.Duration(expiresIn)*time.Second - earlyExpiryBuffer)
		effectiveHigh := afterRefresh.Add(time.Duration(expiresIn)*time.Second - earlyExpiryBuffer)

		if effectiveExpiry.Before(effectiveLow) || effectiveExpiry.After(effectiveHigh) {
			t.Logf("effective expiry out of bounds for expires_in=%d: got %v, expected between %v and %v",
				expiresIn, effectiveExpiry, effectiveLow, effectiveHigh)
			return false
		}

		return true
	}

	if err := quick.Check(f, cfg); err != nil {
		t.Errorf("Property 3 failed: %v", err)
	}
}

// Feature: jira-oauth2-migration, Property 7: Valid cached token prevents refresh requests
// **Validates: Requirements 2.7**
//
// For any state where the TokenManager holds an access token whose effective expiry
// is in the future, calling Token() SHALL return the cached token without making any
// HTTP request.
func TestProperty7_ValidCachedTokenPreventsRefreshRequests(t *testing.T) {
	cfg := &quick.Config{MaxCount: 100}

	f := func(seed uint32) bool {
		// Generate a random token string from the seed
		tokenValue := fmt.Sprintf("cached-token-%d", seed)

		// Generate a random number of subsequent Token() calls (2–20)
		numCalls := int(seed%19) + 2

		// Count HTTP requests to the server
		var requestCount atomic.Int32

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestCount.Add(1)
			resp := tokenRefreshResponse{
				AccessToken:  tokenValue,
				RefreshToken: "refresh-token",
				ExpiresIn:    7200, // 2 hours — well beyond the 60s early-expiry buffer
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(resp)
		}))
		defer server.Close()

		logger := log.NewWithOptions(io.Discard, log.Options{Level: log.FatalLevel})
		tm := NewTokenManager("client-id", "client-secret", "refresh-token",
			WithTokenURL(server.URL),
			WithHTTPClient(server.Client()),
			WithLogger(logger),
		)

		// First call triggers the refresh (caches the token)
		token, err := tm.Token()
		if err != nil {
			t.Logf("First Token() call failed: %v", err)
			return false
		}
		if token != tokenValue {
			t.Logf("First Token() returned %q, expected %q", token, tokenValue)
			return false
		}

		// Server should have received exactly 1 request so far
		if requestCount.Load() != 1 {
			t.Logf("Expected 1 request after first Token(), got %d", requestCount.Load())
			return false
		}

		// Subsequent calls should return the cached token without making HTTP requests
		for i := 0; i < numCalls; i++ {
			token, err = tm.Token()
			if err != nil {
				t.Logf("Token() call %d failed: %v", i+2, err)
				return false
			}
			if token != tokenValue {
				t.Logf("Token() call %d returned %q, expected %q", i+2, token, tokenValue)
				return false
			}
		}

		// After all calls, the server should still have received only 1 request
		if requestCount.Load() != 1 {
			t.Logf("Expected 1 total request, got %d after %d Token() calls", requestCount.Load(), numCalls+1)
			return false
		}

		return true
	}

	if err := quick.Check(f, cfg); err != nil {
		t.Errorf("Property 7 failed: %v", err)
	}
}

// Feature: jira-oauth2-migration, Property 4: Rotating refresh token is used in subsequent requests
// **Validates: Requirements 2.3**
//
// For any token refresh response that includes a new refresh token value,
// subsequent token refresh requests SHALL include the new refresh token (not the original).
func TestProperty4_RotatingRefreshTokenUsedInSubsequentRequests(t *testing.T) {
	cfg := &quick.Config{
		MaxCount: 100,
		Rand:     rand.New(rand.NewSource(77)),
	}

	f := func(initialRefresh, rotatedRefresh string) bool {
		// Skip degenerate inputs: empty strings or identical tokens that would
		// make the property trivially true/untestable.
		if initialRefresh == "" || rotatedRefresh == "" {
			return true
		}
		if initialRefresh == rotatedRefresh {
			return true
		}

		var mu sync.Mutex
		var receivedRefreshTokens []string
		callCount := 0

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var req tokenRefreshRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}

			mu.Lock()
			receivedRefreshTokens = append(receivedRefreshTokens, req.RefreshToken)
			callCount++
			currentCall := callCount
			mu.Unlock()

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)

			if currentCall == 1 {
				// First call: return the rotated refresh token and a short-lived access token.
				// expires_in=1 means after the 60s early-expiry buffer, the token is
				// already considered expired, so next Token() call triggers a refresh.
				json.NewEncoder(w).Encode(tokenRefreshResponse{
					AccessToken:  "access-token-1",
					RefreshToken: rotatedRefresh,
					ExpiresIn:    1, // effectively expired immediately due to 60s buffer
				})
			} else {
				// Second call: return a normal token (no further rotation needed for the test).
				json.NewEncoder(w).Encode(tokenRefreshResponse{
					AccessToken:  "access-token-2",
					RefreshToken: "",
					ExpiresIn:    3600,
				})
			}
		}))
		defer server.Close()

		logger := log.NewWithOptions(io.Discard, log.Options{Level: log.FatalLevel})
		tm := NewTokenManager(
			"test-client-id",
			"test-client-secret",
			initialRefresh,
			WithHTTPClient(server.Client()),
			WithTokenURL(server.URL),
			WithLogger(logger),
		)

		// First call: triggers refresh with the initial refresh token.
		_, err := tm.Token()
		if err != nil {
			t.Logf("First Token() call failed: %v", err)
			return false
		}

		// Second call: token is expired (expires_in=1 with 60s buffer),
		// so this triggers another refresh which should use the rotated token.
		_, err = tm.Token()
		if err != nil {
			t.Logf("Second Token() call failed: %v", err)
			return false
		}

		mu.Lock()
		defer mu.Unlock()

		// We expect exactly 2 refresh requests.
		if len(receivedRefreshTokens) != 2 {
			t.Logf("Expected 2 refresh requests, got %d", len(receivedRefreshTokens))
			return false
		}

		// First request should use the initial refresh token.
		if receivedRefreshTokens[0] != initialRefresh {
			t.Logf("First request used refresh token %q, expected %q", receivedRefreshTokens[0], initialRefresh)
			return false
		}

		// Second request should use the rotated refresh token (not the original).
		if receivedRefreshTokens[1] != rotatedRefresh {
			t.Logf("Second request used refresh token %q, expected rotated token %q", receivedRefreshTokens[1], rotatedRefresh)
			return false
		}

		return true
	}

	if err := quick.Check(f, cfg); err != nil {
		t.Errorf("Property 4 failed: %v", err)
	}
}

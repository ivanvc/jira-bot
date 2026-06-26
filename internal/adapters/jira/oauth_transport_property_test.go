package jira

import (
	"math/rand"
	"net/http"
	"net/url"
	"testing"
	"testing/quick"

	"github.com/stretchr/testify/assert"
)

// Feature: jira-oauth2-migration, Property 8: Transport sets bearer header without mutating original request
// **Validates: Requirements 3.2**
//
// For any HTTP request passed to OAuthTransport.RoundTrip, the transport SHALL forward
// a cloned request with the Authorization: Bearer <token> header set, and the original
// request's headers SHALL remain unchanged.
func TestProperty8_TransportSetsBearerHeaderWithoutMutatingOriginalRequest(t *testing.T) {
	cfg := &quick.Config{
		MaxCount: 100,
		Rand:     rand.New(rand.NewSource(42)),
	}

	f := func(seed int64) bool {
		rng := rand.New(rand.NewSource(seed))

		// Generate a random token value.
		tokenValue := randomString(rng, 10+rng.Intn(50))

		// Generate a random HTTP method.
		methods := []string{"GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS"}
		method := methods[rng.Intn(len(methods))]

		// Generate a random URL path.
		path := "/" + randomString(rng, 5+rng.Intn(20))
		reqURL := &url.URL{
			Scheme: "https",
			Host:   "example.com",
			Path:   path,
		}

		// Create the original request.
		req, err := http.NewRequest(method, reqURL.String(), nil)
		if err != nil {
			t.Logf("failed to create request: %v", err)
			return false
		}

		// Add some random headers to the original request.
		numHeaders := rng.Intn(5)
		for i := 0; i < numHeaders; i++ {
			headerName := "X-Custom-" + randomString(rng, 5)
			headerValue := randomString(rng, 10)
			req.Header.Set(headerName, headerValue)
		}

		// Snapshot the original headers before calling RoundTrip.
		originalHeaders := req.Header.Clone()

		// Create a mock TokenSource that returns the random token.
		source := &mockTokenSource{token: tokenValue}

		// Create a capturing RoundTripper that records the forwarded request.
		capture := &capturingRoundTripper{}

		transport := &OAuthTransport{
			Source: source,
			Base:   capture,
		}

		// Execute RoundTrip.
		_, err = transport.RoundTrip(req)
		if err != nil {
			t.Logf("RoundTrip failed: %v", err)
			return false
		}

		// Verify the forwarded request has the correct Authorization header.
		if capture.capturedReq == nil {
			t.Log("base RoundTripper did not receive a request")
			return false
		}

		expectedAuth := "Bearer " + tokenValue
		actualAuth := capture.capturedReq.Header.Get("Authorization")
		if actualAuth != expectedAuth {
			t.Logf("forwarded request Authorization header: got %q, want %q", actualAuth, expectedAuth)
			return false
		}

		// Verify the original request's headers are NOT modified.
		// The original should not have an Authorization header (unless we added one in the random headers,
		// which we didn't — we only add X-Custom-* headers).
		if req.Header.Get("Authorization") != originalHeaders.Get("Authorization") {
			t.Logf("original request Authorization header was mutated: got %q, had %q",
				req.Header.Get("Authorization"), originalHeaders.Get("Authorization"))
			return false
		}

		// Verify all original headers are unchanged.
		for key, values := range originalHeaders {
			currentValues := req.Header[key]
			if len(currentValues) != len(values) {
				t.Logf("original header %q changed length: was %d, now %d", key, len(values), len(currentValues))
				return false
			}
			for i, v := range values {
				if currentValues[i] != v {
					t.Logf("original header %q[%d] changed: was %q, now %q", key, i, v, currentValues[i])
					return false
				}
			}
		}

		// Verify no new headers were added to the original.
		if len(req.Header) != len(originalHeaders) {
			t.Logf("original request header count changed: was %d, now %d", len(originalHeaders), len(req.Header))
			return false
		}

		return true
	}

	if err := quick.Check(f, cfg); err != nil {
		assert.NoError(t, err, "Property 8 failed: transport must set bearer header without mutating original request")
	}
}

// mockTokenSource is a simple TokenSource implementation for testing.
type mockTokenSource struct {
	token string
	err   error
}

func (m *mockTokenSource) Token() (string, error) {
	return m.token, m.err
}

// capturingRoundTripper captures the request forwarded to it by the transport.
type capturingRoundTripper struct {
	capturedReq *http.Request
}

func (c *capturingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	c.capturedReq = req
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
	}, nil
}

// randomString generates a random alphanumeric string of the given length.
func randomString(rng *rand.Rand, length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, length)
	for i := range b {
		b[i] = charset[rng.Intn(len(charset))]
	}
	return string(b)
}

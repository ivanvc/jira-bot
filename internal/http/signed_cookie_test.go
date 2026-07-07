package http

import (
	"testing"
	"time"
)

func TestSignedCookiePayload_RoundTrip(t *testing.T) {
	secret := "test-secret"
	now := time.Now()
	ttl := 10 * time.Minute

	tests := []struct {
		name    string
		payload cookiePayload
	}{
		{
			name:    "all fields populated",
			payload: cookiePayload{State: "abc123", Login: "octocat", ReturnTo: "/org/repo/issues/42"},
		},
		{
			name:    "state only",
			payload: cookiePayload{State: "xyz789"},
		},
		{
			name:    "state and return_to without login",
			payload: cookiePayload{State: "state1", ReturnTo: "/foo/bar/pull/7"},
		},
		{
			name:    "empty fields",
			payload: cookiePayload{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			signed := signedCookiePayload(tc.payload, secret, now)
			got, err := verifySignedCookiePayload(signed, secret, ttl)
			if err != nil {
				t.Fatalf("verifySignedCookiePayload returned error: %v", err)
			}
			if got.State != tc.payload.State {
				t.Errorf("State: got %q, want %q", got.State, tc.payload.State)
			}
			if got.Login != tc.payload.Login {
				t.Errorf("Login: got %q, want %q", got.Login, tc.payload.Login)
			}
			if got.ReturnTo != tc.payload.ReturnTo {
				t.Errorf("ReturnTo: got %q, want %q", got.ReturnTo, tc.payload.ReturnTo)
			}
		})
	}
}

func TestVerifySignedCookiePayload_InvalidSignature(t *testing.T) {
	secret := "test-secret"
	now := time.Now()
	ttl := 10 * time.Minute

	signed := signedCookiePayload(cookiePayload{State: "s1"}, secret, now)

	_, err := verifySignedCookiePayload(signed, "wrong-secret", ttl)
	if err != errInvalidSignature {
		t.Errorf("expected errInvalidSignature, got %v", err)
	}
}

func TestVerifySignedCookiePayload_Expired(t *testing.T) {
	secret := "test-secret"
	past := time.Now().Add(-1 * time.Hour)
	ttl := 10 * time.Minute

	signed := signedCookiePayload(cookiePayload{State: "s1"}, secret, past)

	_, err := verifySignedCookiePayload(signed, secret, ttl)
	if err != errCookieExpired {
		t.Errorf("expected errCookieExpired, got %v", err)
	}
}

func TestVerifySignedCookiePayload_MalformedJSON(t *testing.T) {
	secret := "test-secret"
	now := time.Now()
	ttl := 10 * time.Minute

	// Sign a non-JSON string using the raw function
	signed := signedCookieValue("not-json", secret, now)

	_, err := verifySignedCookiePayload(signed, secret, ttl)
	if err != errMalformedCookie {
		t.Errorf("expected errMalformedCookie, got %v", err)
	}
}

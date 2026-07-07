package http

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// cookiePayload is the JSON structure stored in the signed session cookie.
type cookiePayload struct {
	State    string `json:"s"`           // CSRF state token
	Login    string `json:"l,omitempty"` // GitHub login (set after GitHub callback)
	ReturnTo string `json:"r,omitempty"` // return path (e.g., "/org/repo/issues/42")
}

// signedCookiePayload marshals a cookiePayload to JSON, then signs it using
// the existing signedCookieValue function.
func signedCookiePayload(p cookiePayload, secret string, now time.Time) string {
	data, _ := json.Marshal(p)
	return signedCookieValue(string(data), secret, now)
}

// verifySignedCookiePayload verifies and unmarshals a signed cookie into a cookiePayload.
func verifySignedCookiePayload(cookieValue, secret string, ttl time.Duration) (cookiePayload, error) {
	raw, err := verifySignedCookie(cookieValue, secret, ttl)
	if err != nil {
		return cookiePayload{}, err
	}
	var p cookiePayload
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return cookiePayload{}, errMalformedCookie
	}
	return p, nil
}

var (
	errInvalidSignature = errors.New("invalid cookie signature")
	errCookieExpired    = errors.New("cookie expired")
	errMalformedCookie  = errors.New("malformed signed cookie")
)

// signedCookieValue creates a signed cookie value in the format:
// base64(payload)|timestamp|base64(hmac)
// The payload is the raw string value to store (e.g., the GitHub login).
func signedCookieValue(value string, secret string, now time.Time) string {
	payload := base64.RawURLEncoding.EncodeToString([]byte(value))
	ts := fmt.Sprintf("%d", now.Unix())
	msg := payload + "|" + ts

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(msg))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	return msg + "|" + sig
}

// verifySignedCookie verifies and extracts the value from a signed cookie.
// Returns the original value string or an error if the signature is invalid or the cookie is expired.
func verifySignedCookie(cookieValue string, secret string, ttl time.Duration) (string, error) {
	parts := strings.SplitN(cookieValue, "|", 3)
	if len(parts) != 3 {
		return "", errMalformedCookie
	}

	payload, ts, sig := parts[0], parts[1], parts[2]

	// Verify HMAC
	msg := payload + "|" + ts
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(msg))
	expectedSig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(sig), []byte(expectedSig)) {
		return "", errInvalidSignature
	}

	// Check expiration
	var tsUnix int64
	if _, err := fmt.Sscanf(ts, "%d", &tsUnix); err != nil {
		return "", errMalformedCookie
	}
	created := time.Unix(tsUnix, 0)
	if time.Since(created) > ttl {
		return "", errCookieExpired
	}

	// Decode payload
	value, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return "", errMalformedCookie
	}

	return string(value), nil
}

// setSignedCookie sets an HMAC-signed cookie with the given name, value, and path.
func setSignedCookie(w http.ResponseWriter, name, value, path, secret string) {
	signed := signedCookieValue(value, secret, time.Now())
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    signed,
		Path:     path,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

// getSignedCookie reads and verifies a signed cookie from the request.
func getSignedCookie(req *http.Request, name, secret string, ttl time.Duration) (string, error) {
	cookie, err := req.Cookie(name)
	if err != nil {
		return "", err
	}
	return verifySignedCookie(cookie.Value, secret, ttl)
}

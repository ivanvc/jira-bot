package jira

import "net/http"

// OAuthTransport implements http.RoundTripper, injecting OAuth bearer tokens.
// It is safe for concurrent use by multiple goroutines.
type OAuthTransport struct {
	// Source provides access tokens for authorization.
	Source TokenSource

	// Base is the underlying RoundTripper used to make HTTP requests.
	// If nil, http.DefaultTransport is used.
	Base http.RoundTripper
}

// RoundTrip clones the request, obtains a token from Source, sets the
// Authorization header, and delegates to the base transport.
func (t *OAuthTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	token, err := t.Source.Token()
	if err != nil {
		return nil, err
	}

	// Clone the request to avoid mutating the original.
	req2 := req.Clone(req.Context())
	req2.Header.Set("Authorization", "Bearer "+token)

	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}

	return base.RoundTrip(req2)
}

package jira

// TokenSource abstracts token retrieval for testability.
type TokenSource interface {
	Token() (string, error)
}

type tokenRefreshRequest struct {
	GrantType    string `json:"grant_type"`    // always "refresh_token"
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	RefreshToken string `json:"refresh_token"`
}

type tokenRefreshResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"` // seconds
	Scope        string `json:"scope"`
}

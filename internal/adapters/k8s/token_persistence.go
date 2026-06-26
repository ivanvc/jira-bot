package k8s

import (
	"fmt"
	"time"
)

// TokenData represents the token fields stored in the Bot_Secret.
type TokenData struct {
	RefreshToken string
	AccessToken  string
	ExpiresAt    time.Time
}

// Secret data key constants
const (
	KeyRefreshToken = "refresh-token"
	KeyAccessToken  = "access-token"
	KeyExpiresAt    = "expires-at"
)

// ToSecretData serializes TokenData into the secret's Data map.
func (d TokenData) ToSecretData() map[string][]byte {
	return map[string][]byte{
		KeyRefreshToken: []byte(d.RefreshToken),
		KeyAccessToken:  []byte(d.AccessToken),
		KeyExpiresAt:    []byte(d.ExpiresAt.Format(time.RFC3339)),
	}
}

// TokenDataFromSecret deserializes TokenData from a secret's Data map.
func TokenDataFromSecret(data map[string][]byte) (TokenData, error) {
	expiresAt, err := time.Parse(time.RFC3339, string(data[KeyExpiresAt]))
	if err != nil {
		return TokenData{}, fmt.Errorf("invalid expires-at: %w", err)
	}
	return TokenData{
		RefreshToken: string(data[KeyRefreshToken]),
		AccessToken:  string(data[KeyAccessToken]),
		ExpiresAt:    expiresAt,
	}, nil
}

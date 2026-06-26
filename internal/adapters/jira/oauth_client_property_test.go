package jira

import (
	"testing"
	"testing/quick"

	"github.com/stretchr/testify/assert"
)

// Feature: jira-oauth2-migration, Property 9: Cloud ID determines API base URL
// **Validates: Requirements 3.7**
//
// For any non-empty cloud ID string, the constructed Jira client base URL SHALL
// equal `https://api.atlassian.com/ex/jira/{cloudID}`.
func TestProperty9_CloudIDDeterminesAPIBaseURL(t *testing.T) {
	cfg := &quick.Config{MaxCount: 100}

	f := func(cloudID string) bool {
		// Skip empty strings — the property is defined for non-empty cloud IDs
		if cloudID == "" {
			return true
		}

		expected := "https://api.atlassian.com/ex/jira/" + cloudID
		actual := oauthBaseURL(cloudID)

		return actual == expected
	}

	if err := quick.Check(f, cfg); err != nil {
		assert.NoError(t, err, "Property 9 failed: cloud ID must determine API base URL")
	}
}

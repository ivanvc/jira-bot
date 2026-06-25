package github

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIssueComment_Unmarshal(t *testing.T) {
	payload := `{
		"action": "created",
		"issue": {
			"comments_url": "https://api.github.com/repos/owner/repo/issues/1/comments",
			"body": "Issue body text",
			"state": "open",
			"url": "https://api.github.com/repos/owner/repo/issues/1",
			"html_url": "https://github.com/owner/repo/issues/1",
			"title": "Test Issue Title"
		},
		"comment": {
			"body": "/jira create",
			"node_id": "IC_kwDOABC123",
			"id": 12345,
			"reactions": {
				"url": "https://api.github.com/repos/owner/repo/issues/comments/12345/reactions"
			}
		},
		"installation": {
			"id": 98765
		}
	}`

	var ic IssueComment
	err := json.Unmarshal([]byte(payload), &ic)
	require.NoError(t, err)

	assert.Equal(t, "created", ic.Action)
	assert.Equal(t, "https://api.github.com/repos/owner/repo/issues/1/comments", ic.Issue.CommentsURL)
	assert.Equal(t, "Issue body text", ic.Issue.Body)
	assert.Equal(t, "open", ic.Issue.State)
	assert.Equal(t, "https://api.github.com/repos/owner/repo/issues/1", ic.Issue.URL)
	assert.Equal(t, "https://github.com/owner/repo/issues/1", ic.Issue.HTMLURL)
	assert.Equal(t, "Test Issue Title", ic.Issue.Title)
	assert.Equal(t, "/jira create", ic.Comment.Body)
	assert.Equal(t, "IC_kwDOABC123", ic.Comment.NodeID)
	assert.Equal(t, uint64(12345), ic.Comment.ID)
	assert.Equal(t, "https://api.github.com/repos/owner/repo/issues/comments/12345/reactions", ic.Comment.Reactions.URL)
	assert.Equal(t, int64(98765), ic.Installation.ID)
}

func TestIssueComment_ToJSON(t *testing.T) {
	ic := IssueComment{
		Action: "created",
		Issue: Issue{
			CommentsURL: "https://api.github.com/repos/owner/repo/issues/2/comments",
			Body:        "Another issue body",
			State:       "closed",
			URL:         "https://api.github.com/repos/owner/repo/issues/2",
			HTMLURL:     "https://github.com/owner/repo/issues/2",
			Title:       "Another Title",
		},
		Comment: Comment{
			Body:   "/jira help",
			NodeID: "IC_kwDOXYZ789",
			ID:     67890,
			Reactions: Reactions{
				URL: "https://api.github.com/repos/owner/repo/issues/comments/67890/reactions",
			},
		},
		Installation: Installation{
			ID: 11111,
		},
	}

	data, err := ic.ToJSON()
	require.NoError(t, err)

	// Verify it's valid JSON by unmarshalling into a map
	var raw map[string]interface{}
	err = json.Unmarshal(data, &raw)
	require.NoError(t, err)

	// Verify all fields are present in the output
	assert.Equal(t, "created", raw["action"])

	issue := raw["issue"].(map[string]interface{})
	assert.Equal(t, "https://api.github.com/repos/owner/repo/issues/2/comments", issue["comments_url"])
	assert.Equal(t, "Another issue body", issue["body"])
	assert.Equal(t, "closed", issue["state"])
	assert.Equal(t, "https://api.github.com/repos/owner/repo/issues/2", issue["url"])
	assert.Equal(t, "https://github.com/owner/repo/issues/2", issue["html_url"])
	assert.Equal(t, "Another Title", issue["title"])

	comment := raw["comment"].(map[string]interface{})
	assert.Equal(t, "/jira help", comment["body"])
	assert.Equal(t, "IC_kwDOXYZ789", comment["node_id"])
	assert.Equal(t, float64(67890), comment["id"])

	reactions := comment["reactions"].(map[string]interface{})
	assert.Equal(t, "https://api.github.com/repos/owner/repo/issues/comments/67890/reactions", reactions["url"])

	assert.Equal(t, float64(11111), raw["installation"].(map[string]interface{})["id"])
}

package jira

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewClient(t *testing.T) {
	client := NewClient("https://test.atlassian.net", "user", "token")
	assert.NotNil(t, client)
	assert.NotNil(t, client.Client)
}

func TestCreateIssue(t *testing.T) {
	tests := []struct {
		name        string
		project     string
		issueType   string
		summary     string
		description string
		response    map[string]interface{}
		statusCode  int
		wantKey     string
		wantErr     bool
	}{
		{
			name:        "successful creation",
			project:     "TEST",
			issueType:   "Task",
			summary:     "Test issue",
			description: "Test description",
			response:    map[string]interface{}{"key": "TEST-123"},
			statusCode:  201,
			wantKey:     "TEST-123",
			wantErr:     false,
		},
		{
			name:        "server error",
			project:     "TEST",
			issueType:   "Task",
			summary:     "Test issue",
			description: "Test description",
			response:    map[string]interface{}{"error": "Bad request"},
			statusCode:  400,
			wantKey:     "",
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.statusCode)
				json.NewEncoder(w).Encode(tt.response)
			}))
			defer server.Close()

			client := NewClient(server.URL, "user", "token")
			require.NotNil(t, client)

			key, err := client.CreateIssue(tt.project, tt.issueType, tt.summary, tt.description)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Empty(t, key)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.wantKey, key)
			}
		})
	}
}

func TestCreateIssue_DescriptionTruncation(t *testing.T) {
	longDescription := strings.Repeat("a", 32001)
	
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var issue map[string]interface{}
		json.NewDecoder(r.Body).Decode(&issue)
		
		fields := issue["fields"].(map[string]interface{})
		description := fields["description"].(string)
		
		// The ellipsis character "…" is 3 bytes in UTF-8
		assert.Equal(t, 32003, len(description))
		assert.True(t, strings.HasSuffix(description, "…"))
		
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(201)
		json.NewEncoder(w).Encode(map[string]interface{}{"key": "TEST-124"})
	}))
	defer server.Close()

	client := NewClient(server.URL, "user", "token")
	key, err := client.CreateIssue("TEST", "Task", "Test", longDescription)
	
	assert.NoError(t, err)
	assert.Equal(t, "TEST-124", key)
}

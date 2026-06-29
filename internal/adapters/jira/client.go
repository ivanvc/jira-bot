package jira

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/andygrunwald/go-jira"
	"github.com/charmbracelet/log"
)

type Client struct {
	*jira.Client
}

// oauthBaseURL constructs the Jira API base URL for the given Atlassian Cloud ID.
func oauthBaseURL(cloudID string) string {
	return fmt.Sprintf("https://api.atlassian.com/ex/jira/%s", cloudID)
}

// NewClient returns a new Jira Client using basic authentication.
func NewClient(baseURL, username, token string) *Client {
	tp := jira.BasicAuthTransport{
		Username: username,
		Password: token,
	}

	c, err := jira.NewClient(tp.Client(), baseURL)
	if err != nil {
		log.Fatal("Error creating Jira client", "error", err)
		return nil
	}

	return &Client{c}
}

// NewOAuthClient returns a Jira Client configured with OAuth 2.0 transport.
func NewOAuthClient(cloudID, clientID, clientSecret, refreshToken string) *Client {
	baseURL := oauthBaseURL(cloudID)

	tm := NewTokenManager(clientID, clientSecret, refreshToken)

	transport := &OAuthTransport{
		Source: tm,
		Base:   http.DefaultTransport,
	}

	httpClient := &http.Client{Transport: transport}

	c, err := jira.NewClient(httpClient, baseURL)
	if err != nil {
		log.Fatal("Error creating OAuth Jira client", "error", err)
		return nil
	}

	return &Client{c}
}

// NewOAuthClientWithTokenSource returns a Jira Client configured with a custom TokenSource.
// This allows injecting leader/follower token sources for multi-pod deployments.
func NewOAuthClientWithTokenSource(cloudID string, source TokenSource) *Client {
	baseURL := oauthBaseURL(cloudID)

	transport := &OAuthTransport{
		Source: source,
		Base:   http.DefaultTransport,
	}

	httpClient := &http.Client{Transport: transport}

	c, err := jira.NewClient(httpClient, baseURL)
	if err != nil {
		log.Fatal("Error creating OAuth Jira client with token source", "error", err)
		return nil
	}

	return &Client{c}
}

func (c *Client) CreateIssue(project, issueType, summary, description string) (string, error) {
	const maxLength = 32000
	if len(description) > maxLength {
		description = description[:maxLength]
		description += "…"
	}

	issue := jira.Issue{
		Fields: &jira.IssueFields{
			Description: description,
			Summary:     summary,
			Type: jira.IssueType{
				Name: issueType,
			},
			Project: jira.Project{
				Key: project,
			},
		},
	}

	r, resp, err := c.Issue.Create(&issue)
	if err != nil {
		if resp != nil && resp.Response != nil {
			// Read the response body for both logging and user-facing error details.
			var bodyData []byte
			if resp.Response.Body != nil {
				bodyData, _ = io.ReadAll(resp.Response.Body)
				resp.Response.Body.Close()
			}
			log.Error("Error creating Jira issue",
				"status", resp.Response.StatusCode,
				"body", string(bodyData),
			)
			if detail := parseJiraErrorBody(bodyData); detail != "" {
				return "", fmt.Errorf("%s", detail)
			}
		} else {
			log.Error("Error creating Jira issue", "error", err)
		}
		return "", err
	}

	return r.Key, nil
}

// jiraErrorResponse represents the JSON error body returned by Jira's API.
type jiraErrorResponse struct {
	ErrorMessages []string          `json:"errorMessages"`
	Errors        map[string]string `json:"errors"`
}

// parseJiraErrorBody parses a Jira error response body and formats the errors
// into a human-readable string suitable for posting as a GitHub comment.
func parseJiraErrorBody(data []byte) string {
	if len(data) == 0 {
		return ""
	}

	var jiraErr jiraErrorResponse
	if err := json.Unmarshal(data, &jiraErr); err != nil {
		return ""
	}

	var parts []string
	for _, msg := range jiraErr.ErrorMessages {
		if msg != "" {
			parts = append(parts, msg)
		}
	}
	for field, msg := range jiraErr.Errors {
		parts = append(parts, fmt.Sprintf("%s: %s", field, msg))
	}

	if len(parts) == 0 {
		return ""
	}

	return "Jira API error: " + strings.Join(parts, "; ")
}

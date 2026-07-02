package jira

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/andygrunwald/go-jira"
	"github.com/charmbracelet/log"
	"github.com/trivago/tgo/tcontainer"
)

// Client wraps the go-jira Client and implements common.JiraClientInterface.
type Client struct {
	*jira.Client
}

func (c *Client) CreateIssue(project, issueType, summary, description string, extraFields map[string]interface{}) (string, error) {
	const maxLength = 32000
	if len(description) > maxLength {
		description = description[:maxLength]
		description += "…"
	}

	// Filter out core fields from extraFields before injecting into Unknowns.
	var unknowns tcontainer.MarshalMap
	if len(extraFields) > 0 {
		unknowns = make(tcontainer.MarshalMap, len(extraFields))
		for k, v := range extraFields {
			switch k {
			case "project", "summary", "description", "issuetype":
				continue
			default:
				unknowns[k] = v
			}
		}
		if len(unknowns) == 0 {
			unknowns = nil
		}
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
			Unknowns: unknowns,
		},
	}

	r, resp, err := c.Issue.Create(&issue)
	if err != nil {
		if resp != nil && resp.Response != nil {
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

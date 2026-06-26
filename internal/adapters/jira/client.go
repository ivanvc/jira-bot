package jira

import (
	"fmt"
	"net/http"
	"net/http/httputil"

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
		respDump, _ := httputil.DumpResponse(resp.Response, true)
		log.Error("Error creating Jira issue", "response", string(respDump))
		return "", err
	}

	return r.Key, nil
}

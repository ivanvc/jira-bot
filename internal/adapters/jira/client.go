package jira

import (
	"net/http/httputil"

	"github.com/andygrunwald/go-jira"
	"github.com/charmbracelet/log"
)

type Client struct {
	*jira.Client
}

// NewClient returns a new Jira Client.
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

func (c *Client) CreateIssue(project, issueType, summary, description string) (string, error) {
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
		log.Info("Error creating Jira issue", "response", string(respDump))
		return "", err
	}

	return r.Key, nil
}

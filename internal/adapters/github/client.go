package github

import (
	"context"

	"github.com/google/go-github/v58/github"
	"k8s.io/utils/ptr"
)

type Client struct {
	*github.Client
}

const mediaTypeReactionsPreview = "application/vnd.github.squirrel-girl-preview"

// NewClient returns a new GitHub Client.
func NewClient(token string) *Client {
	c := github.NewClient(nil).WithAuthToken(token)
	return &Client{c}
}

// ReactWithThumbsUp sends a :+1: reaction to the given IssueComment.
func (c *Client) ReactWithThumbsUp(ctx context.Context, issueComment *IssueComment) error {
	body := &github.Reaction{Content: ptr.To("+1")}
	req, err := c.NewRequest("POST", issueComment.Comment.Reactions.URL, body)
	if err != nil {
		return err
	}

	req.Header.Set("Accept", mediaTypeReactionsPreview)

	r := new(github.Reaction)
	if _, err := c.Do(ctx, req, r); err != nil {
		return err
	}

	return nil
}

// PostComment sends a comment to an issue.
func (c *Client) PostComment(ctx context.Context, issueComment *IssueComment, body string) error {
	comment := &github.IssueComment{Body: &body}
	req, err := c.NewRequest("POST", issueComment.Issue.CommentsURL, comment)
	if err != nil {
		return err
	}

	r := new(github.IssueComment)
	if _, err := c.Do(ctx, req, r); err != nil {
		return err
	}

	return nil
}

// UpdateIssueDescription updates the issue description.
func (c *Client) UpdateIssueDescription(ctx context.Context, issueComment *IssueComment, body string) error {
	issue := &github.IssueRequest{Body: &body}
	req, err := c.NewRequest("PATCH", issueComment.Issue.URL, issue)
	if err != nil {
		return err
	}

	i := new(github.Issue)
	if _, err := c.Do(ctx, req, i); err != nil {
		return err
	}

	return nil
}

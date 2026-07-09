package github

import (
	"context"
	"crypto/rsa"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v4"
	"github.com/google/go-github/v58/github"
	"k8s.io/utils/ptr"
)

type Client struct {
	*github.Client
	appID      int64
	privateKey *rsa.PrivateKey
}

const mediaTypeReactionsPreview = "application/vnd.github.squirrel-girl-preview"

// NewClient returns a new GitHub Client for App authentication.
func NewClient(appID int64, privateKeyPEM string) (*Client, error) {
	privateKey, err := jwt.ParseRSAPrivateKeyFromPEM([]byte(privateKeyPEM))
	if err != nil {
		return nil, fmt.Errorf("failed to parse private key: %w", err)
	}

	return &Client{
		Client:     github.NewClient(nil),
		appID:      appID,
		privateKey: privateKey,
	}, nil
}

// GetInstallationClient returns a client authenticated for a specific installation.
func (c *Client) GetInstallationClient(ctx context.Context, installationID int64) (*github.Client, error) {
	token, err := c.generateJWT()
	if err != nil {
		return nil, err
	}

	appClient := github.NewClient(nil).WithAuthToken(token)
	installationToken, _, err := appClient.Apps.CreateInstallationToken(ctx, installationID, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create installation token: %w", err)
	}

	return github.NewClient(nil).WithAuthToken(installationToken.GetToken()), nil
}

func (c *Client) generateJWT() (string, error) {
	now := time.Now()
	claims := jwt.MapClaims{
		"iat": now.Unix(),
		"exp": now.Add(10 * time.Minute).Unix(),
		"iss": c.appID,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	return token.SignedString(c.privateKey)
}

func (c *Client) sendReaction(ctx context.Context, installationID int64, issueComment *IssueComment, reaction string) error {
	client, err := c.GetInstallationClient(ctx, installationID)
	if err != nil {
		return err
	}

	body := &github.Reaction{Content: ptr.To(reaction)}
	req, err := client.NewRequest("POST", issueComment.Comment.Reactions.URL, body)
	if err != nil {
		return err
	}

	req.Header.Set("Accept", mediaTypeReactionsPreview)

	r := new(github.Reaction)
	if _, err := client.Do(ctx, req, r); err != nil {
		return err
	}

	return nil
}

// ReactWithThumbsUp sends a :+1: reaction to the given IssueComment.
func (c *Client) ReactWithThumbsUp(ctx context.Context, installationID int64, issueComment *IssueComment) error {
	return c.sendReaction(ctx, installationID, issueComment, "+1")
}

// ReactWithThumbsUp sends a :confused: reaction to the given IssueComment.
func (c *Client) ReactWithConfused(ctx context.Context, installationID int64, issueComment *IssueComment) error {
	return c.sendReaction(ctx, installationID, issueComment, "confused")
}

// PostComment sends a comment to an issue.
func (c *Client) PostComment(ctx context.Context, installationID int64, issueComment *IssueComment, body string) error {
	client, err := c.GetInstallationClient(ctx, installationID)
	if err != nil {
		return err
	}

	comment := &github.IssueComment{Body: &body}
	req, err := client.NewRequest("POST", issueComment.Issue.CommentsURL, comment)
	if err != nil {
		return err
	}

	r := new(github.IssueComment)
	if _, err := client.Do(ctx, req, r); err != nil {
		return err
	}

	return nil
}

// UpdateIssueDescription updates the issue description.
func (c *Client) UpdateIssueDescription(ctx context.Context, installationID int64, issueComment *IssueComment, body string) error {
	client, err := c.GetInstallationClient(ctx, installationID)
	if err != nil {
		return err
	}

	issue := &github.IssueRequest{Body: &body}
	req, err := client.NewRequest("PATCH", issueComment.Issue.URL, issue)
	if err != nil {
		return err
	}

	i := new(github.Issue)
	if _, err := client.Do(ctx, req, i); err != nil {
		return err
	}

	return nil
}

// EditComment edits an existing issue comment by ID.
func (c *Client) EditComment(ctx context.Context, installationID int64, owner, repo string, commentID int64, body string) error {
	client, err := c.GetInstallationClient(ctx, installationID)
	if err != nil {
		return err
	}
	comment := &github.IssueComment{Body: &body}
	_, _, err = client.Issues.EditComment(ctx, owner, repo, commentID, comment)
	return err
}

// ListIssueComments lists all comments on an issue.
func (c *Client) ListIssueComments(ctx context.Context, installationID int64, owner, repo string, issueNumber int) ([]*github.IssueComment, error) {
	client, err := c.GetInstallationClient(ctx, installationID)
	if err != nil {
		return nil, err
	}
	comments, _, err := client.Issues.ListComments(ctx, owner, repo, issueNumber, nil)
	return comments, err
}

// FetchComment fetches a comment by ID from the GitHub API using the given
// installation, owner, and repo, then fetches the parent issue to construct a full IssueComment.
func (c *Client) FetchComment(ctx context.Context, installationID int64, owner, repo string, commentID uint64) (*IssueComment, error) {
	client, err := c.GetInstallationClient(ctx, installationID)
	if err != nil {
		return nil, fmt.Errorf("failed to get installation client: %w", err)
	}

	// Fetch the comment directly using the known owner/repo.
	ghComment, _, err := client.Issues.GetComment(ctx, owner, repo, int64(commentID))
	if err != nil {
		return nil, fmt.Errorf("failed to fetch comment: %w", err)
	}

	// Fetch the parent issue using the issue_url from the comment response.
	issueURL := ghComment.GetIssueURL()
	if issueURL == "" {
		return nil, fmt.Errorf("comment response missing issue_url")
	}

	issueReq, err := client.NewRequest("GET", issueURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create issue request: %w", err)
	}

	var ghIssue github.Issue
	if _, err := client.Do(ctx, issueReq, &ghIssue); err != nil {
		return nil, fmt.Errorf("failed to fetch issue: %w", err)
	}

	// Construct the full IssueComment struct.
	ic := &IssueComment{
		Issue: Issue{
			CommentsURL: ghIssue.GetCommentsURL(),
			Body:        ghIssue.GetBody(),
			State:       ghIssue.GetState(),
			URL:         ghIssue.GetURL(),
			HTMLURL:     ghIssue.GetHTMLURL(),
			Title:       ghIssue.GetTitle(),
		},
		Comment: Comment{
			Body:   ghComment.GetBody(),
			ID:     uint64(ghComment.GetID()),
			NodeID: ghComment.GetNodeID(),
			User: CommentUser{
				Login: ghComment.GetUser().GetLogin(),
			},
			Reactions: Reactions{
				URL: ghComment.GetReactions().GetURL(),
			},
		},
		Installation: Installation{
			ID: installationID,
		},
		Repository: Repository{
			Owner: RepositoryOwner{
				Login: owner,
			},
			Name:     repo,
			FullName: owner + "/" + repo,
		},
	}

	return ic, nil
}

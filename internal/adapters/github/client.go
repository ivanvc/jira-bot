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

// ReactWithThumbsUp sends a :+1: reaction to the given IssueComment.
func (c *Client) ReactWithThumbsUp(ctx context.Context, installationID int64, issueComment *IssueComment) error {
	client, err := c.GetInstallationClient(ctx, installationID)
	if err != nil {
		return err
	}

	body := &github.Reaction{Content: ptr.To("+1")}
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

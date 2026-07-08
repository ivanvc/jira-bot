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

// FetchComment fetches a comment by ID from the GitHub API using the given
// installation, then fetches the parent issue to construct a full IssueComment.
func (c *Client) FetchComment(ctx context.Context, installationID int64, commentID uint64) (*IssueComment, error) {
	client, err := c.GetInstallationClient(ctx, installationID)
	if err != nil {
		return nil, fmt.Errorf("failed to get installation client: %w", err)
	}

	// The GitHub REST API requires owner/repo to fetch a comment. Since we only
	// have the comment ID and installation ID, list the installation's repos and
	// try each until we find the comment.
	var ghComment *github.IssueComment
	opts := &github.ListOptions{PerPage: 100}
	for {
		repos, resp, err := client.Apps.ListRepos(ctx, opts)
		if err != nil {
			return nil, fmt.Errorf("failed to list installation repos: %w", err)
		}

		for _, repo := range repos.Repositories {
			owner := repo.GetOwner().GetLogin()
			name := repo.GetName()
			comment, _, err := client.Issues.GetComment(ctx, owner, name, int64(commentID))
			if err == nil {
				ghComment = comment
				break
			}
			// 404 means the comment doesn't belong to this repo; try next.
		}

		if ghComment != nil {
			break
		}

		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	if ghComment == nil {
		return nil, fmt.Errorf("comment %d not found in any installation repository", commentID)
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

	// Extract repository info from the repository_url field on the issue.
	repoURL := ghIssue.GetRepositoryURL()
	repoReq, err := client.NewRequest("GET", repoURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create repo request: %w", err)
	}

	var ghRepo github.Repository
	if _, err := client.Do(ctx, repoReq, &ghRepo); err != nil {
		return nil, fmt.Errorf("failed to fetch repository: %w", err)
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
				Login: ghRepo.GetOwner().GetLogin(),
			},
			Name:     ghRepo.GetName(),
			FullName: ghRepo.GetFullName(),
		},
	}

	return ic, nil
}

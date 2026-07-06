package resolver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/andygrunwald/go-jira"
	"github.com/charmbracelet/log"

	"github.com/ivanvc/jira-bot/internal/common"
)

const (
	// syncRefreshTimeout is the maximum time allowed for a synchronous token refresh.
	syncRefreshTimeout = 5 * time.Second

	// atlassianTokenURL is the Atlassian OAuth token endpoint.
	atlassianTokenURL = "https://auth.atlassian.com/oauth/token"
)

// Compile-time check that DefaultJiraClientResolver implements common.JiraClientResolver.
var _ common.JiraClientResolver = (*DefaultJiraClientResolver)(nil)

// DefaultJiraClientResolver resolves per-user Jira clients by looking up tokens
// in the UserTokenStore and constructing authenticated clients.
type DefaultJiraClientResolver struct {
	store           common.UserTokenStore
	clientID        string
	clientSecret    string
	cloudID         string
	callbackBaseURL string
	logger          *log.Logger
}

// NewDefaultJiraClientResolver creates a new DefaultJiraClientResolver.
func NewDefaultJiraClientResolver(
	store common.UserTokenStore,
	clientID, clientSecret string,
	cloudID string,
	callbackBaseURL string,
	logger *log.Logger,
) *DefaultJiraClientResolver {
	return &DefaultJiraClientResolver{
		store:           store,
		clientID:        clientID,
		clientSecret:    clientSecret,
		cloudID:         cloudID,
		callbackBaseURL: callbackBaseURL,
		logger:          logger,
	}
}

// Resolve looks up or constructs a per-user Jira client for the given GitHub login.
// Returns a result indicating either a ready client or an auth link to present.
func (r *DefaultJiraClientResolver) Resolve(ctx context.Context, login string) common.JiraClientResolveResult {
	entry, err := r.store.Read(ctx, login)
	if err != nil {
		if errors.Is(err, common.ErrNotFound) {
			return r.authRequiredResult(login)
		}
		// For other errors (e.g. store read failure), return an error result.
		return common.JiraClientResolveResult{
			ErrorMsg: fmt.Sprintf("failed to read token for user %q: %v", login, err),
		}
	}

	// If entry is marked invalid, treat as no usable token.
	if entry.Status == "invalid" {
		return r.authRequiredResult(login)
	}

	// If token is expired, attempt synchronous refresh.
	if time.Now().After(entry.ExpiresAt) {
		return r.handleExpiredToken(ctx, login, entry)
	}

	// Token is valid — construct a per-user Jira client.
	return r.buildClientResult(entry)
}

// handleExpiredToken attempts a synchronous token refresh for an expired entry.
func (r *DefaultJiraClientResolver) handleExpiredToken(ctx context.Context, login string, entry common.UserTokenEntry) common.JiraClientResolveResult {
	newEntry, statusCode, err := r.refreshToken(ctx, entry)
	if err != nil {
		// 4xx: mark invalid and return auth link
		if statusCode >= 400 && statusCode < 500 {
			r.logger.Warn("Sync refresh failed with 4xx, marking invalid",
				"login", login, "status", statusCode)
			r.markInvalid(ctx, login, entry)
			return r.authRequiredResult(login)
		}
		// 5xx or network error: return error (do NOT mark invalid)
		r.logger.Error("Sync refresh failed with retryable error",
			"login", login, "status", statusCode, "error", err)
		return common.JiraClientResolveResult{
			ErrorMsg: fmt.Sprintf("failed to refresh token for user %q: %v", login, err),
		}
	}

	// Refresh succeeded — persist the new tokens and construct client.
	if writeErr := r.store.Write(ctx, login, newEntry); writeErr != nil {
		r.logger.Error("Failed to persist refreshed token", "login", login, "error", writeErr)
		// Still return a client with the refreshed token even if persistence fails.
	}

	return r.buildClientResult(newEntry)
}

// refreshToken performs a single token refresh request against the Atlassian token endpoint.
// Returns the updated entry, the HTTP status code (0 for network errors), and any error.
func (r *DefaultJiraClientResolver) refreshToken(ctx context.Context, entry common.UserTokenEntry) (common.UserTokenEntry, int, error) {
	refreshCtx, cancel := context.WithTimeout(ctx, syncRefreshTimeout)
	defer cancel()

	reqBody, err := json.Marshal(map[string]string{
		"grant_type":    "refresh_token",
		"client_id":     r.clientID,
		"client_secret": r.clientSecret,
		"refresh_token": entry.RefreshToken,
	})
	if err != nil {
		return common.UserTokenEntry{}, 0, fmt.Errorf("failed to marshal refresh request: %w", err)
	}

	req, err := http.NewRequestWithContext(refreshCtx, http.MethodPost, atlassianTokenURL, bytes.NewReader(reqBody))
	if err != nil {
		return common.UserTokenEntry{}, 0, fmt.Errorf("failed to create refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return common.UserTokenEntry{}, 0, fmt.Errorf("refresh request failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		return common.UserTokenEntry{}, resp.StatusCode, fmt.Errorf(
			"token endpoint returned %d: %s", resp.StatusCode, truncate(string(body), 1024))
	}

	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return common.UserTokenEntry{}, resp.StatusCode, fmt.Errorf("failed to decode refresh response: %w", err)
	}

	newEntry := common.UserTokenEntry{
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		ExpiresAt:    time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second),
		CloudID:      entry.CloudID,
		AccountID:    entry.AccountID, // preserve accountId across refresh
		Status:       "",              // clear any previous status
	}
	// If the response didn't rotate the refresh token, keep the old one.
	if newEntry.RefreshToken == "" {
		newEntry.RefreshToken = entry.RefreshToken
	}

	return newEntry, resp.StatusCode, nil
}

// markInvalid marks the user's token entry as invalid in the store.
func (r *DefaultJiraClientResolver) markInvalid(ctx context.Context, login string, entry common.UserTokenEntry) {
	entry.Status = "invalid"
	if err := r.store.Write(ctx, login, entry); err != nil {
		r.logger.Error("Failed to mark token as invalid", "login", login, "error", err)
	}
}

// buildClientResult constructs a JiraClientResolveResult with an authenticated Jira client.
func (r *DefaultJiraClientResolver) buildClientResult(entry common.UserTokenEntry) common.JiraClientResolveResult {
	cloudID := entry.CloudID
	if cloudID == "" {
		cloudID = r.cloudID
	}

	baseURL := fmt.Sprintf("https://api.atlassian.com/ex/jira/%s", cloudID)

	transport := &bearerTransport{
		token: entry.AccessToken,
		base:  http.DefaultTransport,
	}
	httpClient := &http.Client{Transport: transport}

	c, err := jira.NewClient(httpClient, baseURL)
	if err != nil {
		return common.JiraClientResolveResult{
			ErrorMsg: fmt.Sprintf("failed to create Jira client: %v", err),
		}
	}

	return common.JiraClientResolveResult{
		Client: &perUserJiraClient{client: c},
	}
}

// authRequiredResult builds a result indicating the user must authorize.
func (r *DefaultJiraClientResolver) authRequiredResult(login string) common.JiraClientResolveResult {
	if r.callbackBaseURL == "" {
		r.logger.Warn("JIRA_BOT_USER_AUTH_CALLBACK_URL is empty — auth link will be a relative URL that won't work on GitHub")
	}
	authLink := fmt.Sprintf("%s/oauth/user/authorize?login=%s", r.callbackBaseURL, login)
	return common.JiraClientResolveResult{
		AuthRequired: true,
		AuthLink:     authLink,
	}
}

// bearerTransport is an http.RoundTripper that adds a Bearer token to requests.
type bearerTransport struct {
	token string
	base  http.RoundTripper
}

func (t *bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req2 := req.Clone(req.Context())
	req2.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(req2)
}

// perUserJiraClient wraps the go-jira Client to satisfy common.JiraClientInterface.
type perUserJiraClient struct {
	client *jira.Client
}

func (c *perUserJiraClient) CreateIssue(project, issueType, summary, description string, extraFields map[string]interface{}) (string, error) {
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

	if len(extraFields) > 0 {
		unknowns := make(map[string]interface{})
		for k, v := range extraFields {
			switch k {
			case "project", "summary", "description", "issuetype":
				continue
			default:
				unknowns[k] = v
			}
		}
		if len(unknowns) > 0 {
			issue.Fields.Unknowns = unknowns
		}
	}

	r, resp, err := c.client.Issue.Create(&issue)
	if err != nil {
		if resp != nil && resp.Response != nil && resp.Response.Body != nil {
			bodyData, _ := io.ReadAll(resp.Response.Body)
			resp.Response.Body.Close()
			if detail := parseJiraErrorBody(bodyData); detail != "" {
				return "", fmt.Errorf("%s", detail)
			}
		}
		return "", err
	}

	return r.Key, nil
}

// parseJiraErrorBody parses a Jira error response body and formats the errors.
func parseJiraErrorBody(data []byte) string {
	if len(data) == 0 {
		return ""
	}

	var jiraErr struct {
		ErrorMessages []string          `json:"errorMessages"`
		Errors        map[string]string `json:"errors"`
	}
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

	result := "Jira API error: "
	for i, p := range parts {
		if i > 0 {
			result += "; "
		}
		result += p
	}
	return result
}

// truncate truncates a string to the given max length.
func truncate(s string, max int) string {
	if len(s) > max {
		return s[:max]
	}
	return s
}

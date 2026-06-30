package jira

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/charmbracelet/log"
	"golang.org/x/sync/singleflight"
)

const (
	defaultTokenURL    = "https://auth.atlassian.com/oauth/token"
	earlyExpiryBuffer = 60 * time.Second
	requestTimeout     = 10 * time.Second
	maxBodyLog         = 1024
)

// retryDelays defines the exponential backoff delays for token refresh retries.
var retryDelays = []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second}

// RefreshCallback is called immediately after a successful token refresh with the
// new token state. This allows the caller to persist the new tokens before they are
// used, preventing token loss if the process is killed between refresh and persistence.
type RefreshCallback func(refreshToken, accessToken string, expiresAt time.Time)

// TokenManager handles OAuth 2.0 token lifecycle including refresh and caching.
type TokenManager struct {
	clientID     string
	clientSecret string
	refreshToken string
	tokenURL     string

	mu          sync.RWMutex
	accessToken string
	expiresAt   time.Time

	httpClient      *http.Client
	sfGroup         singleflight.Group
	logger          *log.Logger
	refreshCallback RefreshCallback
}

// TokenManagerOption configures a TokenManager.
type TokenManagerOption func(*TokenManager)

// WithHTTPClient sets the HTTP client used for token refresh requests.
func WithHTTPClient(c *http.Client) TokenManagerOption {
	return func(tm *TokenManager) {
		tm.httpClient = c
	}
}

// WithLogger sets the logger used by the TokenManager.
func WithLogger(l *log.Logger) TokenManagerOption {
	return func(tm *TokenManager) {
		tm.logger = l
	}
}

// WithTokenURL overrides the default Atlassian token endpoint URL.
func WithTokenURL(url string) TokenManagerOption {
	return func(tm *TokenManager) {
		tm.tokenURL = url
	}
}

// WithRefreshCallback sets a callback that is invoked immediately after a successful
// token refresh, before the token is returned to the caller. This enables persist-before-use
// semantics — the new refresh token is saved to durable storage before it can be lost
// to process termination.
func WithRefreshCallback(cb RefreshCallback) TokenManagerOption {
	return func(tm *TokenManager) {
		tm.refreshCallback = cb
	}
}

// NewTokenManager creates a TokenManager with the given credentials.
func NewTokenManager(clientID, clientSecret, refreshToken string, opts ...TokenManagerOption) *TokenManager {
	tm := &TokenManager{
		clientID:     clientID,
		clientSecret: clientSecret,
		refreshToken: refreshToken,
		tokenURL:     defaultTokenURL,
		httpClient:   &http.Client{},
		logger:       log.Default(),
	}

	for _, opt := range opts {
		opt(tm)
	}

	return tm
}

// SetCachedToken pre-seeds the access token and expiry time.
// This allows pre-seeding the access token from the Bot_Secret on startup
// so the first Token() call does not trigger an unnecessary refresh.
// Safe for concurrent use by multiple goroutines.
func (tm *TokenManager) SetCachedToken(accessToken string, expiresAt time.Time) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	tm.accessToken = accessToken
	tm.expiresAt = expiresAt
}

// SetRefreshCallback sets the callback invoked after each successful token refresh.
// This must be called before any Token() calls to avoid races.
func (tm *TokenManager) SetRefreshCallback(cb RefreshCallback) {
	tm.refreshCallback = cb
}

// TokenState returns the current refresh token, access token, and expiry time.
// Safe for concurrent use by multiple goroutines.
func (tm *TokenManager) TokenState() (refreshToken, accessToken string, expiresAt time.Time) {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	return tm.refreshToken, tm.accessToken, tm.expiresAt
}

// Token returns a valid access token, refreshing if necessary.
// Safe for concurrent use by multiple goroutines.
func (tm *TokenManager) Token() (string, error) {
	tm.mu.RLock()
	if tm.accessToken != "" && time.Now().Before(tm.expiresAt.Add(-earlyExpiryBuffer)) {
		token := tm.accessToken
		tm.mu.RUnlock()
		return token, nil
	}
	tm.mu.RUnlock()

	result, err, _ := tm.sfGroup.Do("refresh", func() (interface{}, error) {
		return tm.refresh()
	})
	if err != nil {
		return "", err
	}

	return result.(string), nil
}

// refresh performs the token refresh request with retry logic.
func (tm *TokenManager) refresh() (string, error) {
	var lastErr error

	for attempt := 0; attempt <= len(retryDelays); attempt++ {
		if attempt > 0 {
			delay := retryDelays[attempt-1]
			tm.logger.Warn("Retrying token refresh", "attempt", attempt, "error", lastErr)
			time.Sleep(delay)
		}

		token, err, statusCode, shouldRetry := tm.doRefreshRequest()
		if err == nil {
			return token, nil
		}

		lastErr = err

		if !shouldRetry {
			body := extractBody(err)
			tm.logger.Error("Token refresh failed", "status", statusCode, "body", truncateBody(body))
			return "", err
		}

		// If this was the last attempt, log the final failure
		if attempt == len(retryDelays) {
			body := extractBody(err)
			tm.logger.Error("Token refresh failed after retries", "status", statusCode, "body", truncateBody(body))
		}
	}

	return "", lastErr
}

// refreshError carries status code and body information for logging.
type refreshError struct {
	statusCode int
	body       string
	message    string
}

func (e *refreshError) Error() string {
	return e.message
}

// doRefreshRequest performs a single token refresh HTTP request.
// Returns: token, error, statusCode, shouldRetry.
func (tm *TokenManager) doRefreshRequest() (string, error, int, bool) {
	tm.mu.RLock()
	currentRefreshToken := tm.refreshToken
	tm.mu.RUnlock()

	reqBody := tokenRefreshRequest{
		GrantType:    "refresh_token",
		ClientID:     tm.clientID,
		ClientSecret: tm.clientSecret,
		RefreshToken: currentRefreshToken,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal token request: %w", err), 0, false
	}

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tm.tokenURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("failed to create token request: %w", err), 0, false
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := tm.httpClient.Do(req)
	if err != nil {
		// Network errors are retryable.
		return "", &refreshError{
			statusCode: 0,
			message:    fmt.Sprintf("token refresh request failed: %v", err),
		}, 0, true
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", &refreshError{
			statusCode: resp.StatusCode,
			body:       "",
			message:    fmt.Sprintf("failed to read token response body: %v", err),
		}, resp.StatusCode, true
	}

	if resp.StatusCode >= 500 {
		return "", &refreshError{
			statusCode: resp.StatusCode,
			body:       string(respBody),
			message:    fmt.Sprintf("token endpoint returned %d: %s", resp.StatusCode, truncateBody(string(respBody))),
		}, resp.StatusCode, true
	}

	if resp.StatusCode >= 400 {
		return "", &refreshError{
			statusCode: resp.StatusCode,
			body:       string(respBody),
			message:    fmt.Sprintf("token endpoint returned %d: %s", resp.StatusCode, truncateBody(string(respBody))),
		}, resp.StatusCode, false
	}

	var tokenResp tokenRefreshResponse
	if err := json.Unmarshal(respBody, &tokenResp); err != nil {
		return "", fmt.Errorf("failed to decode token response: %w", err), resp.StatusCode, false
	}

	tm.mu.Lock()
	tm.accessToken = tokenResp.AccessToken
	tm.expiresAt = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
	if tokenResp.RefreshToken != "" {
		tm.refreshToken = tokenResp.RefreshToken
	}
	currentRefresh := tm.refreshToken
	currentAccess := tm.accessToken
	currentExpiry := tm.expiresAt
	tm.mu.Unlock()

	// Invoke the refresh callback BEFORE returning the token. This ensures the new
	// refresh token is persisted to durable storage before it can be lost to process
	// termination (persist-before-use pattern).
	if tm.refreshCallback != nil {
		tm.refreshCallback(currentRefresh, currentAccess, currentExpiry)
	}

	tm.logger.Info("Token refreshed successfully", "expires_at", currentExpiry)

	return tokenResp.AccessToken, nil, resp.StatusCode, false
}

// extractBody extracts the body from a refreshError if available.
func extractBody(err error) string {
	if re, ok := err.(*refreshError); ok {
		return re.body
	}
	return err.Error()
}

// truncateBody truncates a body string to maxBodyLog characters.
func truncateBody(body string) string {
	if len(body) > maxBodyLog {
		return body[:maxBodyLog]
	}
	return body
}

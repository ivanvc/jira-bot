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
)

const (
	// refreshBuffer is the time window before token expiry at which proactive
	// refresh is triggered.
	refreshBuffer = 5 * time.Minute

	// defaultRefreshInterval is the default interval between refresh cycles.
	defaultRefreshInterval = 30 * time.Second

	// minRefreshInterval is the minimum allowed refresh interval.
	minRefreshInterval = 10 * time.Second

	// maxRefreshInterval is the maximum allowed refresh interval.
	maxRefreshInterval = 300 * time.Second

	// maxRefreshWorkers is the maximum number of concurrent refresh operations.
	maxRefreshWorkers = 5

	// stopTimeout is how long Stop() waits for in-flight operations to complete.
	stopTimeout = 5 * time.Second

	// maxRetries is the number of retry attempts for retryable errors.
	maxRetries = 3

	// multiRefreshRequestTimeout is the timeout for individual refresh HTTP requests.
	multiRefreshRequestTimeout = 10 * time.Second

	// defaultTokenURL is the Atlassian OAuth token endpoint.
	defaultTokenURL = "https://auth.atlassian.com/oauth/token"
)

// multiRefreshRetryDelays defines exponential backoff delays for refresh retries.
var multiRefreshRetryDelays = []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second}

// tokenRefreshRequest is the JSON body sent to the Atlassian token endpoint.
type tokenRefreshRequest struct {
	GrantType    string `json:"grant_type"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	RefreshToken string `json:"refresh_token"`
}

// tokenRefreshResponse is the JSON response from the Atlassian token endpoint.
type tokenRefreshResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
}

// refreshError represents a token refresh failure with HTTP status and body.
type refreshError struct {
	statusCode int
	body       string
	message    string
}

func (e *refreshError) Error() string {
	return e.message
}

// truncateBody truncates a string to 1024 characters for logging.
func truncateBody(s string) string {
	if len(s) > 1024 {
		return s[:1024]
	}
	return s
}

// RefreshTokenEntry mirrors common.UserTokenEntry / k8s.UserTokenEntry without
// importing those packages, avoiding import cycles (k8s already imports jira).
// It is structurally identical to k8s.UserTokenEntry so that callers can
// convert between the two.
type RefreshTokenEntry struct {
	RefreshToken string    `json:"refresh_token"`
	AccessToken  string    `json:"access_token"`
	ExpiresAt    time.Time `json:"expires_at"`
	CloudID      string    `json:"cloud_id"`
	Status       string    `json:"status,omitempty"`
}

// RefreshTokenStore is the subset of common.UserTokenStore needed by the
// MultiUserRefreshManager. It avoids importing common (which transitively
// imports k8s → jira, creating a cycle).
type RefreshTokenStore interface {
	ReadAll(ctx context.Context) (map[string]RefreshTokenEntry, error)
	Write(ctx context.Context, login string, entry RefreshTokenEntry) error
}

// MultiUserRefreshManager proactively refreshes all users' OAuth tokens before
// they expire. It runs as a background loop on the leader pod with bounded
// concurrency to avoid overwhelming the Atlassian token endpoint.
type MultiUserRefreshManager struct {
	store        RefreshTokenStore
	clientID     string
	clientSecret string
	interval     time.Duration
	buffer       time.Duration
	maxWorkers   int
	logger       *log.Logger
	httpClient   *http.Client
	tokenURL     string

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// MultiUserRefreshManagerOption configures a MultiUserRefreshManager.
type MultiUserRefreshManagerOption func(*MultiUserRefreshManager)

// WithMultiRefreshHTTPClient sets the HTTP client used for token refresh requests.
func WithMultiRefreshHTTPClient(c *http.Client) MultiUserRefreshManagerOption {
	return func(m *MultiUserRefreshManager) {
		m.httpClient = c
	}
}

// WithMultiRefreshTokenURL overrides the default Atlassian token endpoint URL.
func WithMultiRefreshTokenURL(url string) MultiUserRefreshManagerOption {
	return func(m *MultiUserRefreshManager) {
		m.tokenURL = url
	}
}

// WithMultiRefreshLogger sets the logger used by the MultiUserRefreshManager.
func WithMultiRefreshLogger(l *log.Logger) MultiUserRefreshManagerOption {
	return func(m *MultiUserRefreshManager) {
		m.logger = l
	}
}

// ClampRefreshInterval clamps the given interval to [minRefreshInterval, maxRefreshInterval].
// Returns defaultRefreshInterval if the input is zero.
func ClampRefreshInterval(d time.Duration) time.Duration {
	if d <= 0 {
		return defaultRefreshInterval
	}
	if d < minRefreshInterval {
		return minRefreshInterval
	}
	if d > maxRefreshInterval {
		return maxRefreshInterval
	}
	return d
}

// NewMultiUserRefreshManager creates a new MultiUserRefreshManager.
// The interval is clamped to [10s, 300s].
func NewMultiUserRefreshManager(
	store RefreshTokenStore,
	clientID, clientSecret string,
	interval time.Duration,
	opts ...MultiUserRefreshManagerOption,
) *MultiUserRefreshManager {
	m := &MultiUserRefreshManager{
		store:        store,
		clientID:     clientID,
		clientSecret: clientSecret,
		interval:     ClampRefreshInterval(interval),
		buffer:       refreshBuffer,
		maxWorkers:   maxRefreshWorkers,
		logger:       log.Default(),
		httpClient:   &http.Client{},
		tokenURL:     defaultTokenURL,
	}

	for _, opt := range opts {
		opt(m)
	}

	return m
}

// Start begins the background refresh loop. It blocks until the context is
// cancelled or Stop() is called.
func (m *MultiUserRefreshManager) Start(ctx context.Context) {
	ctx, m.cancel = context.WithCancel(ctx)

	m.wg.Add(1)
	go m.run(ctx)
}

// Stop cancels the background loop and waits up to 5 seconds for in-flight
// operations to complete.
func (m *MultiUserRefreshManager) Stop() {
	if m.cancel != nil {
		m.cancel()
	}

	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(stopTimeout):
		m.logger.Warn("MultiUserRefreshManager: timed out waiting for in-flight operations")
	}
}

// run is the main loop that ticks at the configured interval.
func (m *MultiUserRefreshManager) run(ctx context.Context) {
	defer m.wg.Done()

	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()

	// Run immediately on start.
	m.refreshCycle(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.refreshCycle(ctx)
		}
	}
}

// refreshCycle performs a single refresh cycle: reads all entries, filters those
// near expiry, and refreshes them with bounded concurrency.
func (m *MultiUserRefreshManager) refreshCycle(ctx context.Context) {
	entries, err := m.store.ReadAll(ctx)
	if err != nil {
		m.logger.Error("Failed to read token entries for refresh", "error", err)
		return
	}

	now := time.Now()
	var toRefresh []struct {
		login string
		entry RefreshTokenEntry
	}

	for login, entry := range entries {
		// Skip entries that are already marked invalid.
		if entry.Status == "invalid" {
			continue
		}
		// Refresh entries expiring within the buffer window.
		if entry.ExpiresAt.Before(now.Add(m.buffer)) {
			toRefresh = append(toRefresh, struct {
				login string
				entry RefreshTokenEntry
			}{login, entry})
		}
	}

	if len(toRefresh) == 0 {
		return
	}

	m.logger.Info("Refreshing tokens", "count", len(toRefresh))

	// Use a buffered channel as a semaphore for bounded concurrency.
	sem := make(chan struct{}, m.maxWorkers)
	var wg sync.WaitGroup

	for _, item := range toRefresh {
		select {
		case <-ctx.Done():
			break
		default:
		}

		sem <- struct{}{}
		wg.Add(1)
		go func(login string, entry RefreshTokenEntry) {
			defer wg.Done()
			defer func() { <-sem }()

			m.refreshEntry(ctx, login, entry)
		}(item.login, item.entry)
	}

	wg.Wait()
}

// refreshEntry attempts to refresh a single user's token with exponential backoff
// for retryable errors.
func (m *MultiUserRefreshManager) refreshEntry(ctx context.Context, login string, entry RefreshTokenEntry) {
	var lastErr error

	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			delay := multiRefreshRetryDelays[attempt-1]
			select {
			case <-ctx.Done():
				return
			case <-time.After(delay):
			}
		}

		newEntry, err, statusCode, retryable := m.doRefresh(ctx, entry)
		if err == nil {
			// Success: update store with new tokens.
			if writeErr := m.store.Write(ctx, login, newEntry); writeErr != nil {
				m.logger.Error("Failed to write refreshed token", "login", login, "error", writeErr)
			}
			return
		}

		lastErr = err

		if !retryable {
			// 4xx error: mark as invalid.
			body := ""
			if re, ok := err.(*refreshError); ok {
				body = re.body
			}
			m.logger.Error("Token refresh failed with non-retryable error",
				"login", login,
				"status", statusCode,
				"body", truncateBody(body),
			)
			entry.Status = "invalid"
			if writeErr := m.store.Write(ctx, login, entry); writeErr != nil {
				m.logger.Error("Failed to mark token as invalid", "login", login, "error", writeErr)
			}
			return
		}
	}

	// All retries exhausted: mark as failed.
	m.logger.Error("Token refresh failed after retries",
		"login", login,
		"error", lastErr,
	)
	entry.Status = "failed"
	if writeErr := m.store.Write(ctx, login, entry); writeErr != nil {
		m.logger.Error("Failed to mark token as failed", "login", login, "error", writeErr)
	}
}

// doRefresh performs a single token refresh HTTP request for a user entry.
// Returns: updated entry, error, HTTP status code, whether the error is retryable.
func (m *MultiUserRefreshManager) doRefresh(ctx context.Context, entry RefreshTokenEntry) (RefreshTokenEntry, error, int, bool) {
	reqBody := tokenRefreshRequest{
		GrantType:    "refresh_token",
		ClientID:     m.clientID,
		ClientSecret: m.clientSecret,
		RefreshToken: entry.RefreshToken,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return RefreshTokenEntry{}, fmt.Errorf("failed to marshal token request: %w", err), 0, false
	}

	reqCtx, cancel := context.WithTimeout(ctx, multiRefreshRequestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, m.tokenURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return RefreshTokenEntry{}, fmt.Errorf("failed to create token request: %w", err), 0, false
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := m.httpClient.Do(req)
	if err != nil {
		// Network errors are retryable.
		return RefreshTokenEntry{}, &refreshError{
			statusCode: 0,
			message:    fmt.Sprintf("token refresh request failed: %v", err),
		}, 0, true
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return RefreshTokenEntry{}, &refreshError{
			statusCode: resp.StatusCode,
			body:       "",
			message:    fmt.Sprintf("failed to read token response body: %v", err),
		}, resp.StatusCode, true
	}

	if resp.StatusCode >= 500 {
		return RefreshTokenEntry{}, &refreshError{
			statusCode: resp.StatusCode,
			body:       string(respBody),
			message:    fmt.Sprintf("token endpoint returned %d: %s", resp.StatusCode, truncateBody(string(respBody))),
		}, resp.StatusCode, true
	}

	if resp.StatusCode >= 400 {
		return RefreshTokenEntry{}, &refreshError{
			statusCode: resp.StatusCode,
			body:       string(respBody),
			message:    fmt.Sprintf("token endpoint returned %d: %s", resp.StatusCode, truncateBody(string(respBody))),
		}, resp.StatusCode, false
	}

	var tokenResp tokenRefreshResponse
	if err := json.Unmarshal(respBody, &tokenResp); err != nil {
		return RefreshTokenEntry{}, fmt.Errorf("failed to decode token response: %w", err), resp.StatusCode, false
	}

	newEntry := RefreshTokenEntry{
		RefreshToken: tokenResp.RefreshToken,
		AccessToken:  tokenResp.AccessToken,
		ExpiresAt:    time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second),
		CloudID:      entry.CloudID,
		Status:       "", // Clear any previous failed/invalid status.
	}

	// If the refresh token was not rotated, keep the old one.
	if newEntry.RefreshToken == "" {
		newEntry.RefreshToken = entry.RefreshToken
	}

	return newEntry, nil, resp.StatusCode, false
}

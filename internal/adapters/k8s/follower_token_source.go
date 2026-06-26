package k8s

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/charmbracelet/log"
	"github.com/ivanvc/jira-bot/internal/adapters/jira"
)

// Compile-time check that FollowerTokenSource implements jira.TokenSource.
var _ jira.TokenSource = (*FollowerTokenSource)(nil)

// DefaultPollInterval is the default interval for polling the Bot_Secret.
const DefaultPollInterval = 30 * time.Second

// FollowerTokenSource provides access tokens for non-leader pods by polling the Bot_Secret.
// Implements the jira.TokenSource interface.
type FollowerTokenSource struct {
	adapter      *TokenPersistenceAdapter
	pollInterval time.Duration
	logger       *log.Logger

	mu          sync.RWMutex
	cachedToken string
	expiresAt   time.Time
}

// NewFollowerTokenSource creates a follower token source that polls at the given interval.
// If pollInterval is zero or negative, DefaultPollInterval (30s) is used.
func NewFollowerTokenSource(adapter *TokenPersistenceAdapter, pollInterval time.Duration, logger *log.Logger) *FollowerTokenSource {
	if pollInterval <= 0 {
		pollInterval = DefaultPollInterval
	}
	return &FollowerTokenSource{
		adapter:      adapter,
		pollInterval: pollInterval,
		logger:       logger,
	}
}

// Start begins the polling loop. It reads the Bot_Secret at the configured interval
// and updates the cached token. Blocks until ctx is cancelled.
func (f *FollowerTokenSource) Start(ctx context.Context) {
	// Do an initial poll immediately on start.
	f.poll(ctx)

	ticker := time.NewTicker(f.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			f.poll(ctx)
		}
	}
}

// Token returns the cached access token or an error if expired/unavailable.
func (f *FollowerTokenSource) Token() (string, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	if f.cachedToken == "" {
		return "", fmt.Errorf("no access token available from Bot_Secret")
	}

	if time.Now().After(f.expiresAt) {
		return "", fmt.Errorf("cached access token expired at %s", f.expiresAt.Format(time.RFC3339))
	}

	return f.cachedToken, nil
}

// poll reads the Bot_Secret and updates the cached token.
func (f *FollowerTokenSource) poll(ctx context.Context) {
	data, err := f.adapter.Read(ctx)
	if err != nil {
		f.logger.Warn("Failed to poll Bot_Secret for access token", "error", err)
		return
	}

	if data.AccessToken == "" {
		return
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	// Req 5.4: immediately begin using the new token
	if data.AccessToken != f.cachedToken {
		f.logger.Info("Updated cached access token from Bot_Secret")
	}
	f.cachedToken = data.AccessToken
	f.expiresAt = data.ExpiresAt
}

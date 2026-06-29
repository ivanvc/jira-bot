package k8s

import (
	"context"
	"time"

	"github.com/charmbracelet/log"
	"github.com/ivanvc/jira-bot/internal/adapters/jira"
)

// Compile-time check that LeaderTokenSource implements jira.TokenSource.
var _ jira.TokenSource = (*LeaderTokenSource)(nil)

// proactiveRefreshBuffer is how far before expiry the leader will proactively
// refresh the token. This ensures followers always have a valid token in the secret.
const proactiveRefreshBuffer = 5 * time.Minute

// refreshCheckInterval is how often the background loop checks whether a refresh is needed.
const refreshCheckInterval = 30 * time.Second

// LeaderTokenSource wraps the existing TokenManager and persists tokens after each refresh.
// Implements the jira.TokenSource interface.
type LeaderTokenSource struct {
	tokenManager *jira.TokenManager
	adapter      *TokenPersistenceAdapter
	logger       *log.Logger
}

// NewLeaderTokenSource wraps an existing TokenManager with persistence.
func NewLeaderTokenSource(tm *jira.TokenManager, adapter *TokenPersistenceAdapter, logger *log.Logger) *LeaderTokenSource {
	return &LeaderTokenSource{
		tokenManager: tm,
		adapter:      adapter,
		logger:       logger,
	}
}

// Start begins the proactive refresh loop. It checks every refreshCheckInterval whether
// the token is within proactiveRefreshBuffer of expiry, and if so, triggers a refresh
// and persists the new token. This ensures followers always have a valid token available.
// Blocks until ctx is cancelled.
func (l *LeaderTokenSource) Start(ctx context.Context) {
	// Do an initial refresh to ensure the secret has a valid token immediately.
	l.proactiveRefresh()

	ticker := time.NewTicker(refreshCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			l.proactiveRefresh()
		}
	}
}

// proactiveRefresh checks if the token is near expiry and refreshes it if so.
func (l *LeaderTokenSource) proactiveRefresh() {
	_, _, expiresAt := l.tokenManager.TokenState()

	// If the token expires within the buffer window, force a refresh by clearing
	// the cached token (setting expiry to past) and then calling Token().
	// This is necessary because the TokenManager has its own earlyExpiryBuffer (60s)
	// which is shorter than our proactive buffer.
	if time.Until(expiresAt) < proactiveRefreshBuffer {
		l.logger.Info("Proactively refreshing token before expiry", "expires_at", expiresAt)
		l.tokenManager.SetCachedToken("", time.Time{})
		if _, err := l.Token(); err != nil {
			l.logger.Error("Proactive token refresh failed", "error", err)
		}
	}
}

// Token returns a valid access token, refreshing if needed, and persists new tokens.
// On write failure, logs the error but still returns the token (graceful degradation per Req 3.2).
func (l *LeaderTokenSource) Token() (string, error) {
	token, err := l.tokenManager.Token()
	if err != nil {
		return "", err
	}

	// Persist the current token state to the Bot_Secret.
	refreshToken, accessToken, expiresAt := l.tokenManager.TokenState()
	data := TokenData{
		RefreshToken: refreshToken,
		AccessToken:  accessToken,
		ExpiresAt:    expiresAt,
	}

	if writeErr := l.adapter.Write(context.Background(), data); writeErr != nil {
		// Graceful degradation: log the error but return the token anyway (Req 3.2).
		l.logger.Error("Failed to persist token after refresh", "error", writeErr)
	}

	return token, nil
}

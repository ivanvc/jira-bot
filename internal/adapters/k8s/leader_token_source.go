package k8s

import (
	"context"

	"github.com/charmbracelet/log"
	"github.com/ivanvc/jira-bot/internal/adapters/jira"
)

// Compile-time check that LeaderTokenSource implements jira.TokenSource.
var _ jira.TokenSource = (*LeaderTokenSource)(nil)

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

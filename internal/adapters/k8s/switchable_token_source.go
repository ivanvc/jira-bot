package k8s

import (
	"fmt"
	"sync"

	"github.com/ivanvc/jira-bot/internal/adapters/jira"
)

// Compile-time check that SwitchableTokenSource implements jira.TokenSource.
var _ jira.TokenSource = (*SwitchableTokenSource)(nil)

// SwitchableTokenSource delegates to either a leader or follower token source
// based on leadership state. It is safe for concurrent use.
type SwitchableTokenSource struct {
	leader   jira.TokenSource
	follower jira.TokenSource

	mu       sync.RWMutex
	isLeader bool
}

// NewSwitchableTokenSource creates a token source that can switch between leader and follower modes.
// It starts in follower mode by default.
func NewSwitchableTokenSource(leader, follower jira.TokenSource) *SwitchableTokenSource {
	return &SwitchableTokenSource{
		leader:   leader,
		follower: follower,
		isLeader: false,
	}
}

// SetLeader switches to leader mode.
func (s *SwitchableTokenSource) SetLeader() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.isLeader = true
}

// SetFollower switches to follower mode.
func (s *SwitchableTokenSource) SetFollower() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.isLeader = false
}

// Token returns a valid access token from the currently active source.
func (s *SwitchableTokenSource) Token() (string, error) {
	s.mu.RLock()
	isLeader := s.isLeader
	s.mu.RUnlock()

	if isLeader {
		return s.leader.Token()
	}

	token, err := s.follower.Token()
	if err != nil {
		return "", fmt.Errorf("follower token source: %w", err)
	}
	return token, nil
}

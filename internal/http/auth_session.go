package http

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"
)

var (
	ErrSessionNotFound = errors.New("session not found")
	ErrSessionExpired  = errors.New("session expired")
)

// AuthSession represents a single in-memory authorization session linking
// a cryptographically random session ID to a verified GitHub login.
type AuthSession struct {
	Login     string
	CreatedAt time.Time
}

// AuthSessionMap is a thread-safe in-memory store for authorization sessions
// with TTL-based expiration.
type AuthSessionMap struct {
	mu       sync.RWMutex
	sessions map[string]*AuthSession
	ttl      time.Duration
}

// NewAuthSessionMap creates an AuthSessionMap with the given TTL duration.
func NewAuthSessionMap(ttl time.Duration) *AuthSessionMap {
	return &AuthSessionMap{
		sessions: make(map[string]*AuthSession),
		ttl:      ttl,
	}
}

// Create generates a new session for the given login and returns the session ID.
// The session ID is 32 bytes from crypto/rand, hex-encoded (64 characters).
func (m *AuthSessionMap) Create(login string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	sessionID := hex.EncodeToString(b)

	m.mu.Lock()
	defer m.mu.Unlock()

	m.sessions[sessionID] = &AuthSession{
		Login:     login,
		CreatedAt: time.Now(),
	}

	return sessionID, nil
}

// Get retrieves the login associated with the given session ID.
// Returns ErrSessionNotFound if no session exists and ErrSessionExpired if the
// session's CreatedAt + ttl is in the past.
func (m *AuthSessionMap) Get(sessionID string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	session, ok := m.sessions[sessionID]
	if !ok {
		return "", ErrSessionNotFound
	}

	if time.Since(session.CreatedAt) > m.ttl {
		return "", ErrSessionExpired
	}

	return session.Login, nil
}

// Delete removes a session by its ID.
func (m *AuthSessionMap) Delete(sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.sessions, sessionID)
}

// Cleanup removes all expired sessions from the map.
func (m *AuthSessionMap) Cleanup() {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	for id, session := range m.sessions {
		if now.Sub(session.CreatedAt) > m.ttl {
			delete(m.sessions, id)
		}
	}
}

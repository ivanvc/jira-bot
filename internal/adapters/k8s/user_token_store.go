package k8s

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/charmbracelet/log"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// Sentinel errors for UserTokenStore operations.
var (
	ErrUserTokenNotFound  = errors.New("user token entry not found")
	ErrUserTokenMalformed = errors.New("malformed user token entry")
)

// UserTokenEntry represents a single user's stored Atlassian OAuth token data.
// This mirrors common.UserTokenEntry and is used within the k8s package to avoid
// import cycles (common already imports k8s for TokenPersistenceAdapter).
type UserTokenEntry struct {
	RefreshToken string    `json:"refresh_token"`
	AccessToken  string    `json:"access_token"`
	ExpiresAt    time.Time `json:"expires_at"`
	CloudID      string    `json:"cloud_id"`
	Status       string    `json:"status,omitempty"`
	AccountID    string    `json:"account_id,omitempty"`
}

// K8sUserTokenStore implements the UserTokenStore interface backed by a Kubernetes Secret.
// Each key in the Secret's Data map is a GitHub login, and each value is a
// JSON-encoded UserTokenEntry.
type K8sUserTokenStore struct {
	client     kubernetes.Interface
	namespace  string
	secretName string
	logger     *log.Logger
}

// NewK8sUserTokenStore creates a new K8sUserTokenStore.
func NewK8sUserTokenStore(client kubernetes.Interface, namespace, secretName string, logger *log.Logger) *K8sUserTokenStore {
	return &K8sUserTokenStore{
		client:     client,
		namespace:  namespace,
		secretName: secretName,
		logger:     logger,
	}
}

// Read returns a single token entry for the given login.
// Returns ErrUserTokenNotFound if the Secret does not exist or the login key is absent.
// Returns ErrUserTokenMalformed if the stored JSON cannot be deserialized.
func (s *K8sUserTokenStore) Read(ctx context.Context, login string) (UserTokenEntry, error) {
	secret, err := s.client.CoreV1().Secrets(s.namespace).Get(ctx, s.secretName, metav1.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return UserTokenEntry{}, ErrUserTokenNotFound
		}
		return UserTokenEntry{}, fmt.Errorf("failed to get secret %s/%s: %w", s.namespace, s.secretName, err)
	}

	data, ok := secret.Data[login]
	if !ok || len(data) == 0 {
		return UserTokenEntry{}, ErrUserTokenNotFound
	}

	var entry UserTokenEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return UserTokenEntry{}, ErrUserTokenMalformed
	}

	return entry, nil
}

// ReadAll returns all valid token entries from the Secret, skipping malformed ones.
// If the Secret does not exist, returns an empty map (not an error).
func (s *K8sUserTokenStore) ReadAll(ctx context.Context) (map[string]UserTokenEntry, error) {
	secret, err := s.client.CoreV1().Secrets(s.namespace).Get(ctx, s.secretName, metav1.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return map[string]UserTokenEntry{}, nil
		}
		return nil, fmt.Errorf("failed to get secret %s/%s: %w", s.namespace, s.secretName, err)
	}

	entries := make(map[string]UserTokenEntry, len(secret.Data))
	for login, data := range secret.Data {
		var entry UserTokenEntry
		if err := json.Unmarshal(data, &entry); err != nil {
			s.logger.Warn("Skipping malformed user token entry", "login", login, "error", err)
			continue
		}
		entries[login] = entry
	}

	return entries, nil
}

// Write creates or updates a token entry for the given login.
// Uses optimistic concurrency with up to 3 retries on conflict.
func (s *K8sUserTokenStore) Write(ctx context.Context, login string, entry UserTokenEntry) error {
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("failed to marshal token entry: %w", err)
	}

	for attempt := 0; attempt < maxConflictRetries; attempt++ {
		err := s.tryWrite(ctx, login, data)
		if err == nil {
			return nil
		}
		if !k8serrors.IsConflict(err) {
			return err
		}
		s.logger.Warn("Conflict writing user token, retrying", "login", login, "attempt", attempt+1)
	}

	return fmt.Errorf("failed to write user token for %q after %d conflict retries", login, maxConflictRetries)
}

// tryWrite performs a single attempt to write the entry into the Secret.
func (s *K8sUserTokenStore) tryWrite(ctx context.Context, login string, data []byte) error {
	secretsClient := s.client.CoreV1().Secrets(s.namespace)

	existing, err := secretsClient.Get(ctx, s.secretName, metav1.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			// Secret doesn't exist, create it
			newSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      s.secretName,
					Namespace: s.namespace,
					Labels: map[string]string{
						"app.kubernetes.io/managed-by": "jira-bot",
					},
				},
				Type: corev1.SecretTypeOpaque,
				Data: map[string][]byte{
					login: data,
				},
			}
			_, createErr := secretsClient.Create(ctx, newSecret, metav1.CreateOptions{})
			if createErr != nil {
				return fmt.Errorf("failed to create secret %s/%s: %w", s.namespace, s.secretName, createErr)
			}
			return nil
		}
		return fmt.Errorf("failed to get secret %s/%s: %w", s.namespace, s.secretName, err)
	}

	if existing.Data == nil {
		existing.Data = make(map[string][]byte)
	}
	existing.Data[login] = data

	_, updateErr := secretsClient.Update(ctx, existing, metav1.UpdateOptions{})
	return updateErr
}

// Delete removes the token entry for the given login from the Secret.
// Uses optimistic concurrency with up to 3 retries on conflict.
func (s *K8sUserTokenStore) Delete(ctx context.Context, login string) error {
	for attempt := 0; attempt < maxConflictRetries; attempt++ {
		err := s.tryDelete(ctx, login)
		if err == nil {
			return nil
		}
		if !k8serrors.IsConflict(err) {
			return err
		}
		s.logger.Warn("Conflict deleting user token, retrying", "login", login, "attempt", attempt+1)
	}

	return fmt.Errorf("failed to delete user token for %q after %d conflict retries", login, maxConflictRetries)
}

// tryDelete performs a single attempt to remove the login key from the Secret.
func (s *K8sUserTokenStore) tryDelete(ctx context.Context, login string) error {
	secretsClient := s.client.CoreV1().Secrets(s.namespace)

	existing, err := secretsClient.Get(ctx, s.secretName, metav1.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			// Secret doesn't exist, nothing to delete
			return nil
		}
		return fmt.Errorf("failed to get secret %s/%s: %w", s.namespace, s.secretName, err)
	}

	if existing.Data == nil {
		return nil
	}

	delete(existing.Data, login)

	_, updateErr := secretsClient.Update(ctx, existing, metav1.UpdateOptions{})
	return updateErr
}

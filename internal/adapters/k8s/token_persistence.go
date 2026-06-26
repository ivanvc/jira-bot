package k8s

import (
	"context"
	"fmt"
	"time"

	"github.com/charmbracelet/log"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// TokenData represents the token fields stored in the Bot_Secret.
type TokenData struct {
	RefreshToken string
	AccessToken  string
	ExpiresAt    time.Time
}

// Secret data key constants
const (
	KeyRefreshToken = "refresh-token"
	KeyAccessToken  = "access-token"
	KeyExpiresAt    = "expires-at"
)

// ToSecretData serializes TokenData into the secret's Data map.
func (d TokenData) ToSecretData() map[string][]byte {
	return map[string][]byte{
		KeyRefreshToken: []byte(d.RefreshToken),
		KeyAccessToken:  []byte(d.AccessToken),
		KeyExpiresAt:    []byte(d.ExpiresAt.Format(time.RFC3339)),
	}
}

// TokenDataFromSecret deserializes TokenData from a secret's Data map.
func TokenDataFromSecret(data map[string][]byte) (TokenData, error) {
	expiresAt, err := time.Parse(time.RFC3339, string(data[KeyExpiresAt]))
	if err != nil {
		return TokenData{}, fmt.Errorf("invalid expires-at: %w", err)
	}
	return TokenData{
		RefreshToken: string(data[KeyRefreshToken]),
		AccessToken:  string(data[KeyAccessToken]),
		ExpiresAt:    expiresAt,
	}, nil
}

// maxConflictRetries is the maximum number of retries on conflict (409) errors.
const maxConflictRetries = 3

// TokenPersistenceAdapter reads and writes OAuth tokens to a Kubernetes Secret.
type TokenPersistenceAdapter struct {
	client     kubernetes.Interface
	namespace  string
	secretName string
	logger     *log.Logger
}

// NewTokenPersistenceAdapter creates a new adapter for the given secret.
func NewTokenPersistenceAdapter(client kubernetes.Interface, namespace, secretName string, logger *log.Logger) *TokenPersistenceAdapter {
	return &TokenPersistenceAdapter{
		client:     client,
		namespace:  namespace,
		secretName: secretName,
		logger:     logger,
	}
}

// Read retrieves the current token data from the Bot_Secret.
// Returns empty TokenData and nil error if the secret does not exist.
// Returns an error only for unexpected API failures.
func (a *TokenPersistenceAdapter) Read(ctx context.Context) (TokenData, error) {
	secret, err := a.client.CoreV1().Secrets(a.namespace).Get(ctx, a.secretName, metav1.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return TokenData{}, nil
		}
		return TokenData{}, fmt.Errorf("failed to read secret %s/%s: %w", a.namespace, a.secretName, err)
	}

	if secret.Data == nil || len(secret.Data[KeyRefreshToken]) == 0 {
		return TokenData{}, nil
	}

	// If expires-at is missing or invalid, return what we can without error
	tokenData, err := TokenDataFromSecret(secret.Data)
	if err != nil {
		a.logger.Warn("Invalid token data in secret, treating as empty", "error", err)
		return TokenData{}, nil
	}

	return tokenData, nil
}

// Write persists the token data to the Bot_Secret.
// Creates the secret if it does not exist. Uses optimistic concurrency (resourceVersion)
// on updates. Retries on conflict by re-reading and re-applying.
// Preserves any existing keys in the secret that are not token-related.
func (a *TokenPersistenceAdapter) Write(ctx context.Context, data TokenData) error {
	for attempt := 0; attempt <= maxConflictRetries; attempt++ {
		err := a.tryWrite(ctx, data)
		if err == nil {
			return nil
		}

		if !k8serrors.IsConflict(err) {
			a.logger.Error("Failed to write token secret", "error", err)
			return err
		}

		a.logger.Warn("Conflict writing token secret, retrying", "attempt", attempt+1)
	}

	return fmt.Errorf("failed to write secret %s/%s after %d conflict retries", a.namespace, a.secretName, maxConflictRetries)
}

// tryWrite performs a single attempt to create or update the secret.
func (a *TokenPersistenceAdapter) tryWrite(ctx context.Context, data TokenData) error {
	secretsClient := a.client.CoreV1().Secrets(a.namespace)

	existing, err := secretsClient.Get(ctx, a.secretName, metav1.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			// Secret doesn't exist, create it
			newSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      a.secretName,
					Namespace: a.namespace,
					Labels: map[string]string{
						"app.kubernetes.io/managed-by": "jira-bot",
					},
				},
				Type: corev1.SecretTypeOpaque,
				Data: data.ToSecretData(),
			}
			_, createErr := secretsClient.Create(ctx, newSecret, metav1.CreateOptions{})
			if createErr != nil {
				return fmt.Errorf("failed to create secret %s/%s: %w", a.namespace, a.secretName, createErr)
			}
			return nil
		}
		return fmt.Errorf("failed to get secret %s/%s for update: %w", a.namespace, a.secretName, err)
	}

	// Preserve existing non-token keys
	if existing.Data == nil {
		existing.Data = make(map[string][]byte)
	}
	tokenFields := data.ToSecretData()
	for k, v := range tokenFields {
		existing.Data[k] = v
	}

	// Update with resourceVersion for optimistic concurrency
	_, updateErr := secretsClient.Update(ctx, existing, metav1.UpdateOptions{})
	if updateErr != nil {
		return updateErr
	}

	return nil
}

// EnsureExists creates the Bot_Secret with empty values if it does not already exist.
// Returns nil if the secret already exists.
func (a *TokenPersistenceAdapter) EnsureExists(ctx context.Context) error {
	_, err := a.client.CoreV1().Secrets(a.namespace).Get(ctx, a.secretName, metav1.GetOptions{})
	if err == nil {
		// Secret already exists
		return nil
	}

	if !k8serrors.IsNotFound(err) {
		return fmt.Errorf("failed to check secret %s/%s: %w", a.namespace, a.secretName, err)
	}

	// Create the secret with empty token values
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      a.secretName,
			Namespace: a.namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "jira-bot",
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			KeyRefreshToken: {},
			KeyAccessToken:  {},
			KeyExpiresAt:    {},
		},
	}

	_, createErr := a.client.CoreV1().Secrets(a.namespace).Create(ctx, secret, metav1.CreateOptions{})
	if createErr != nil {
		if k8serrors.IsAlreadyExists(createErr) {
			// Another pod created it concurrently, that's fine
			return nil
		}
		return fmt.Errorf("failed to create secret %s/%s: %w", a.namespace, a.secretName, createErr)
	}

	a.logger.Info("Created bot-managed token secret", "name", a.secretName, "namespace", a.namespace)
	return nil
}

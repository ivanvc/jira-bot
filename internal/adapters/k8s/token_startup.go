package k8s

import "github.com/charmbracelet/log"

// PickRefreshToken returns the Bot_Secret refresh token when non-empty,
// otherwise falls back to the environment variable token.
// It logs which token source was selected at startup.
// Satisfies Requirements 2.1, 2.2, 2.3.
func PickRefreshToken(secretData TokenData, envVarToken string, logger *log.Logger) string {
	if secretData.RefreshToken != "" {
		logger.Info("Using refresh token from Bot_Secret")
		return secretData.RefreshToken
	}

	logger.Info("Using refresh token from environment variable")
	return envVarToken
}

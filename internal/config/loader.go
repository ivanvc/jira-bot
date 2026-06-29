package config

import (
	"context"
	"fmt"

	"github.com/charmbracelet/log"
	"github.com/google/go-github/v58/github"
)

// GitHubContentsFetcher is the interface the loader needs from the GitHub client.
type GitHubContentsFetcher interface {
	GetInstallationClient(ctx context.Context, installationID int64) (*github.Client, error)
}

// Loader fetches and parses repository configuration files.
type Loader struct {
	github GitHubContentsFetcher
}

// NewLoader creates a new config Loader.
func NewLoader(gh GitHubContentsFetcher) *Loader {
	return &Loader{github: gh}
}

// configPaths defines the file locations to check, in priority order.
var configPaths = []string{
	".github/jira-bot.yaml",
	"jira-bot.yaml",
}

// LoadRepoConfig fetches and parses the repo config file.
// Returns an empty RepoConfig (not an error) if no file exists.
func (l *Loader) LoadRepoConfig(ctx context.Context, installationID int64, owner, repo string) (RepoConfig, error) {
	client, err := l.github.GetInstallationClient(ctx, installationID)
	if err != nil {
		return RepoConfig{}, fmt.Errorf("getting installation client: %w", err)
	}

	for _, path := range configPaths {
		content, _, resp, err := client.Repositories.GetContents(ctx, owner, repo, path, nil)
		if err != nil {
			if resp != nil && resp.StatusCode == 404 {
				continue // file not found, try next path
			}
			log.Error("Error fetching repo config", "path", path, "error", err)
			return RepoConfig{}, nil // fall back to empty config on API errors
		}

		if content == nil {
			continue
		}

		decoded, err := content.GetContent()
		if err != nil {
			log.Error("Error decoding repo config content", "path", path, "error", err)
			return RepoConfig{}, fmt.Errorf("decoding content from %s: %w", path, err)
		}

		return ParseRepoConfig([]byte(decoded))
	}

	return RepoConfig{}, nil // no config file found
}

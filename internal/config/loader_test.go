package config

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/go-github/v58/github"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockGitHubContentsFetcher implements GitHubContentsFetcher for testing.
type mockGitHubContentsFetcher struct {
	client *github.Client
	err    error
}

func (m *mockGitHubContentsFetcher) GetInstallationClient(_ context.Context, _ int64) (*github.Client, error) {
	return m.client, m.err
}

// newTestClient creates a *github.Client configured to use the provided test server.
func newTestClient(t *testing.T, server *httptest.Server) *github.Client {
	t.Helper()
	client, err := github.NewClient(nil).WithEnterpriseURLs(server.URL, server.URL)
	require.NoError(t, err)
	return client
}

func TestLoadRepoConfig_DiscoveryOrder_FirstPathFound(t *testing.T) {
	// When .github/jira-bot.yaml exists, it should be used and jira-bot.yaml should NOT be fetched.
	var requestedPaths []string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPaths = append(requestedPaths, r.URL.Path)

		if r.URL.Path == "/api/v3/repos/owner/repo/contents/.github/jira-bot.yaml" {
			content := base64.StdEncoding.EncodeToString([]byte("project: ENG\ntype: Story\n"))
			fmt.Fprintf(w, `{"type":"file","encoding":"base64","content":"%s"}`, content)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := newTestClient(t, server)
	fetcher := &mockGitHubContentsFetcher{client: client}
	loader := NewLoader(fetcher)

	cfg, err := loader.LoadRepoConfig(context.Background(), 123, "owner", "repo")

	assert.NoError(t, err)
	assert.Equal(t, "ENG", cfg.Project)
	assert.Equal(t, "Story", cfg.Type)
	// Verify jira-bot.yaml was never requested
	for _, p := range requestedPaths {
		assert.NotContains(t, p, "/contents/jira-bot.yaml",
			"jira-bot.yaml should not be fetched when .github/jira-bot.yaml exists")
	}
}

func TestLoadRepoConfig_Fallback_FirstPath404(t *testing.T) {
	// When .github/jira-bot.yaml returns 404, it should try jira-bot.yaml.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v3/repos/owner/repo/contents/.github/jira-bot.yaml" {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"message":"Not Found"}`)
			return
		}
		if r.URL.Path == "/api/v3/repos/owner/repo/contents/jira-bot.yaml" {
			content := base64.StdEncoding.EncodeToString([]byte("project: PLAT\n"))
			fmt.Fprintf(w, `{"type":"file","encoding":"base64","content":"%s"}`, content)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := newTestClient(t, server)
	fetcher := &mockGitHubContentsFetcher{client: client}
	loader := NewLoader(fetcher)

	cfg, err := loader.LoadRepoConfig(context.Background(), 123, "owner", "repo")

	assert.NoError(t, err)
	assert.Equal(t, "PLAT", cfg.Project)
	assert.Equal(t, "", cfg.Type)
}

func TestLoadRepoConfig_BothPaths404_ReturnsEmptyConfig(t *testing.T) {
	// When both paths return 404, returns empty RepoConfig{} and nil error.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"message":"Not Found"}`)
	}))
	defer server.Close()

	client := newTestClient(t, server)
	fetcher := &mockGitHubContentsFetcher{client: client}
	loader := NewLoader(fetcher)

	cfg, err := loader.LoadRepoConfig(context.Background(), 123, "owner", "repo")

	assert.NoError(t, err)
	assert.Equal(t, RepoConfig{}, cfg)
}

func TestLoadRepoConfig_APIError500_ReturnsEmptyConfig(t *testing.T) {
	// When the API returns 500, returns empty RepoConfig{} and nil error (graceful degradation).
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"message":"Internal Server Error"}`)
	}))
	defer server.Close()

	client := newTestClient(t, server)
	fetcher := &mockGitHubContentsFetcher{client: client}
	loader := NewLoader(fetcher)

	cfg, err := loader.LoadRepoConfig(context.Background(), 123, "owner", "repo")

	assert.NoError(t, err)
	assert.Equal(t, RepoConfig{}, cfg)
}

func TestLoadRepoConfig_SuccessfulFetchAndParse(t *testing.T) {
	// When a valid config file is found, returns correctly parsed RepoConfig.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v3/repos/myorg/myrepo/contents/.github/jira-bot.yaml" {
			content := base64.StdEncoding.EncodeToString([]byte("project: BACKEND\ntype: Bug\n"))
			fmt.Fprintf(w, `{"type":"file","encoding":"base64","content":"%s"}`, content)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := newTestClient(t, server)
	fetcher := &mockGitHubContentsFetcher{client: client}
	loader := NewLoader(fetcher)

	cfg, err := loader.LoadRepoConfig(context.Background(), 456, "myorg", "myrepo")

	assert.NoError(t, err)
	assert.Equal(t, "BACKEND", cfg.Project)
	assert.Equal(t, "Bug", cfg.Type)
}

func TestLoadRepoConfig_InstallationClientCreationFailure(t *testing.T) {
	// When GetInstallationClient returns an error, LoadRepoConfig returns an error wrapping it.
	fetcher := &mockGitHubContentsFetcher{
		client: nil,
		err:    fmt.Errorf("auth token expired"),
	}
	loader := NewLoader(fetcher)

	cfg, err := loader.LoadRepoConfig(context.Background(), 789, "owner", "repo")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "getting installation client")
	assert.Contains(t, err.Error(), "auth token expired")
	assert.Equal(t, RepoConfig{}, cfg)
}

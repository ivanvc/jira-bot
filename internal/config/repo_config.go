package config

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// RepoConfig represents the per-repository configuration loaded from a YAML file.
type RepoConfig struct {
	Project string `yaml:"project"`
	Type    string `yaml:"type"`
}

// IsEmpty returns true if no values are configured.
func (rc RepoConfig) IsEmpty() bool {
	return rc.Project == "" && rc.Type == ""
}

// ParseRepoConfig parses YAML bytes into a RepoConfig.
// Returns an empty RepoConfig for empty input. Returns an error for invalid YAML.
func ParseRepoConfig(data []byte) (RepoConfig, error) {
	if len(data) == 0 {
		return RepoConfig{}, nil
	}

	var cfg RepoConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return RepoConfig{}, fmt.Errorf("parsing repo config YAML: %w", err)
	}
	return cfg, nil
}

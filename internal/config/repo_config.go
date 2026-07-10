package config

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// RepoConfig represents the per-repository configuration loaded from a YAML file.
type RepoConfig struct {
	Project     string                 `yaml:"project"`
	Type        string                 `yaml:"type"`
	Assign      *bool                  `yaml:"assign"`
	UpdateTitle string                 `yaml:"update-title"`
	Fields      map[string]interface{} `yaml:"fields"`
}

// IsEmpty returns true if no values are configured.
func (rc RepoConfig) IsEmpty() bool {
	return rc.Project == "" && rc.Type == "" && rc.Assign == nil && rc.UpdateTitle == "" && len(rc.Fields) == 0
}

// ParseRepoConfig parses YAML bytes into a RepoConfig.
// Returns an empty RepoConfig for empty input. Returns an error for invalid YAML.
func ParseRepoConfig(data []byte) (RepoConfig, error) {
	if len(data) == 0 {
		return RepoConfig{}, nil
	}

	// First, do a raw unmarshal to validate the fields key type.
	var raw map[string]interface{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return RepoConfig{}, fmt.Errorf("parsing repo config YAML: %w", err)
	}

	if fieldsVal, ok := raw["fields"]; ok && fieldsVal != nil {
		if _, isMap := fieldsVal.(map[string]interface{}); !isMap {
			return RepoConfig{}, fmt.Errorf("parsing repo config YAML: fields must be a map, got %T", fieldsVal)
		}
	}

	var cfg RepoConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return RepoConfig{}, fmt.Errorf("parsing repo config YAML: %w", err)
	}

	// Strip null-valued keys from the Fields map.
	for key, val := range cfg.Fields {
		if val == nil {
			delete(cfg.Fields, key)
		}
	}

	return cfg, nil
}

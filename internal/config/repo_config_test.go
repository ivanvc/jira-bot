package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseRepoConfig_EmptyInput_NilBytes(t *testing.T) {
	cfg, err := ParseRepoConfig(nil)

	assert.NoError(t, err)
	assert.Equal(t, RepoConfig{}, cfg)
}

func TestParseRepoConfig_EmptyInput_EmptySlice(t *testing.T) {
	cfg, err := ParseRepoConfig([]byte{})

	assert.NoError(t, err)
	assert.Equal(t, RepoConfig{}, cfg)
}

func TestParseRepoConfig_InvalidYAML(t *testing.T) {
	_, err := ParseRepoConfig([]byte(":::invalid"))

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parsing repo config YAML")
}

func TestParseRepoConfig_PartialConfig_OnlyProject(t *testing.T) {
	data := []byte("project: ENG\n")

	cfg, err := ParseRepoConfig(data)

	assert.NoError(t, err)
	assert.Equal(t, "ENG", cfg.Project)
	assert.Equal(t, "", cfg.Type)
}

func TestParseRepoConfig_PartialConfig_OnlyType(t *testing.T) {
	data := []byte("type: Bug\n")

	cfg, err := ParseRepoConfig(data)

	assert.NoError(t, err)
	assert.Equal(t, "", cfg.Project)
	assert.Equal(t, "Bug", cfg.Type)
}

func TestParseRepoConfig_FullConfig(t *testing.T) {
	data := []byte("project: ENG\ntype: Story\n")

	cfg, err := ParseRepoConfig(data)

	assert.NoError(t, err)
	assert.Equal(t, "ENG", cfg.Project)
	assert.Equal(t, "Story", cfg.Type)
}

func TestParseRepoConfig_UnknownFieldsIgnored(t *testing.T) {
	data := []byte("project: PLAT\ntype: Task\nunknown_field: some_value\nanother: 123\n")

	cfg, err := ParseRepoConfig(data)

	assert.NoError(t, err)
	assert.Equal(t, "PLAT", cfg.Project)
	assert.Equal(t, "Task", cfg.Type)
}

func TestRepoConfig_IsEmpty_ZeroValue(t *testing.T) {
	cfg := RepoConfig{}

	assert.True(t, cfg.IsEmpty())
}

func TestRepoConfig_IsEmpty_NonEmpty_ProjectSet(t *testing.T) {
	cfg := RepoConfig{Project: "ENG"}

	assert.False(t, cfg.IsEmpty())
}

func TestRepoConfig_IsEmpty_NonEmpty_TypeSet(t *testing.T) {
	cfg := RepoConfig{Type: "Bug"}

	assert.False(t, cfg.IsEmpty())
}

func TestRepoConfig_IsEmpty_NonEmpty_BothSet(t *testing.T) {
	cfg := RepoConfig{Project: "ENG", Type: "Bug"}

	assert.False(t, cfg.IsEmpty())
}

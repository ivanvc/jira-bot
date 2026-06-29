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

func TestRepoConfig_IsEmpty_NonEmpty_FieldsSet(t *testing.T) {
	cfg := RepoConfig{Fields: map[string]interface{}{"priority": map[string]interface{}{"name": "High"}}}

	assert.False(t, cfg.IsEmpty())
}

func TestParseRepoConfig_WithFieldsMap(t *testing.T) {
	data := []byte(`project: ENG
type: Story
fields:
  components:
    - name: Backend
  priority:
    name: High
  labels:
    - team-platform
  customfield_10001: "My Value"
`)

	cfg, err := ParseRepoConfig(data)

	assert.NoError(t, err)
	assert.Equal(t, "ENG", cfg.Project)
	assert.Equal(t, "Story", cfg.Type)
	assert.NotNil(t, cfg.Fields)
	assert.Equal(t, []interface{}{map[string]interface{}{"name": "Backend"}}, cfg.Fields["components"])
	assert.Equal(t, map[string]interface{}{"name": "High"}, cfg.Fields["priority"])
	assert.Equal(t, []interface{}{"team-platform"}, cfg.Fields["labels"])
	assert.Equal(t, "My Value", cfg.Fields["customfield_10001"])
}

func TestParseRepoConfig_FieldsAbsent(t *testing.T) {
	data := []byte("project: ENG\ntype: Story\n")

	cfg, err := ParseRepoConfig(data)

	assert.NoError(t, err)
	assert.Nil(t, cfg.Fields)
}

func TestParseRepoConfig_FieldsNonMap_String(t *testing.T) {
	data := []byte("project: ENG\nfields: not_a_map\n")

	_, err := ParseRepoConfig(data)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "fields must be a map")
	assert.Contains(t, err.Error(), "string")
}

func TestParseRepoConfig_FieldsNonMap_Number(t *testing.T) {
	data := []byte("project: ENG\nfields: 42\n")

	_, err := ParseRepoConfig(data)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "fields must be a map")
	assert.Contains(t, err.Error(), "int")
}

func TestParseRepoConfig_FieldsNonMap_Array(t *testing.T) {
	data := []byte("project: ENG\nfields:\n  - item1\n  - item2\n")

	_, err := ParseRepoConfig(data)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "fields must be a map")
}

func TestParseRepoConfig_FieldsNullValuesStripped(t *testing.T) {
	data := []byte("project: ENG\nfields:\n  priority:\n    name: High\n  components: null\n  labels: null\n")

	cfg, err := ParseRepoConfig(data)

	assert.NoError(t, err)
	assert.Equal(t, map[string]interface{}{"name": "High"}, cfg.Fields["priority"])
	_, hasComponents := cfg.Fields["components"]
	assert.False(t, hasComponents)
	_, hasLabels := cfg.Fields["labels"]
	assert.False(t, hasLabels)
}

func TestParseRepoConfig_FieldsAllNullValues(t *testing.T) {
	data := []byte("fields:\n  a: null\n  b: null\n")

	cfg, err := ParseRepoConfig(data)

	assert.NoError(t, err)
	assert.Empty(t, cfg.Fields)
}

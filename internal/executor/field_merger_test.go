package executor

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMergeFields_BothEmpty(t *testing.T) {
	result := MergeFields(nil, nil)
	assert.NotNil(t, result)
	assert.Empty(t, result)
}

func TestMergeFields_EmptyMaps(t *testing.T) {
	result := MergeFields(map[string]interface{}{}, map[string]interface{}{})
	assert.NotNil(t, result)
	assert.Empty(t, result)
}

func TestMergeFields_OnlyRepoFields(t *testing.T) {
	repo := map[string]interface{}{
		"priority":   map[string]interface{}{"name": "Medium"},
		"components": []interface{}{map[string]interface{}{"name": "Backend"}},
	}
	result := MergeFields(repo, nil)
	assert.Equal(t, map[string]interface{}{
		"priority":   map[string]interface{}{"name": "Medium"},
		"components": []interface{}{map[string]interface{}{"name": "Backend"}},
	}, result)
}

func TestMergeFields_OnlyCommandFields(t *testing.T) {
	cmd := map[string]interface{}{
		"priority": map[string]interface{}{"name": "High"},
		"labels":   []interface{}{"urgent"},
	}
	result := MergeFields(nil, cmd)
	assert.Equal(t, map[string]interface{}{
		"priority": map[string]interface{}{"name": "High"},
		"labels":   []interface{}{"urgent"},
	}, result)
}

func TestMergeFields_CommandOverridesRepo(t *testing.T) {
	repo := map[string]interface{}{
		"priority":   map[string]interface{}{"name": "Medium"},
		"components": []interface{}{map[string]interface{}{"name": "Backend"}},
		"labels":     []interface{}{"team-platform"},
	}
	cmd := map[string]interface{}{
		"priority":   map[string]interface{}{"name": "High"},
		"components": []interface{}{map[string]interface{}{"name": "Frontend"}},
	}
	result := MergeFields(repo, cmd)
	assert.Equal(t, map[string]interface{}{
		"priority":   map[string]interface{}{"name": "High"},
		"components": []interface{}{map[string]interface{}{"name": "Frontend"}},
		"labels":     []interface{}{"team-platform"},
	}, result)
}

func TestMergeFields_ExcludesCoreFields(t *testing.T) {
	repo := map[string]interface{}{
		"project":     "ENG",
		"summary":     "test summary",
		"description": "test description",
		"issuetype":   "Bug",
		"priority":    map[string]interface{}{"name": "Medium"},
	}
	cmd := map[string]interface{}{
		"project":   "OTHER",
		"issuetype": "Story",
		"labels":    []interface{}{"override"},
	}
	result := MergeFields(repo, cmd)
	assert.Equal(t, map[string]interface{}{
		"priority": map[string]interface{}{"name": "Medium"},
		"labels":   []interface{}{"override"},
	}, result)
}

func TestMergeFields_CaseSensitiveKeys(t *testing.T) {
	repo := map[string]interface{}{
		"Priority": map[string]interface{}{"name": "Low"},
	}
	cmd := map[string]interface{}{
		"priority": map[string]interface{}{"name": "High"},
	}
	result := MergeFields(repo, cmd)
	// Both keys should be present since they differ in casing
	assert.Equal(t, map[string]interface{}{
		"Priority": map[string]interface{}{"name": "Low"},
		"priority": map[string]interface{}{"name": "High"},
	}, result)
}

func TestMergeFields_CaseSensitiveCoreFieldExclusion(t *testing.T) {
	// "Project" (capitalized) is NOT a core field, only "project" (lowercase) is
	repo := map[string]interface{}{
		"Project": "should-remain",
		"project": "should-be-excluded",
	}
	result := MergeFields(repo, nil)
	assert.Equal(t, map[string]interface{}{
		"Project": "should-remain",
	}, result)
}

func TestMergeFields_CustomFields(t *testing.T) {
	repo := map[string]interface{}{
		"customfield_10001": "repo value",
		"customfield_10002": map[string]interface{}{"id": "123"},
	}
	cmd := map[string]interface{}{
		"customfield_10001": "command value",
	}
	result := MergeFields(repo, cmd)
	assert.Equal(t, map[string]interface{}{
		"customfield_10001": "command value",
		"customfield_10002": map[string]interface{}{"id": "123"},
	}, result)
}

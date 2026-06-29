package executor

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCoerceField_Components(t *testing.T) {
	result, ok := CoerceField("components", "Backend")
	assert.True(t, ok)
	expected := []interface{}{map[string]interface{}{"name": "Backend"}}
	assert.Equal(t, expected, result)
}

func TestCoerceField_Priority(t *testing.T) {
	result, ok := CoerceField("priority", "High")
	assert.True(t, ok)
	expected := map[string]interface{}{"name": "High"}
	assert.Equal(t, expected, result)
}

func TestCoerceField_Labels(t *testing.T) {
	result, ok := CoerceField("labels", "bug-fix")
	assert.True(t, ok)
	expected := []interface{}{"bug-fix"}
	assert.Equal(t, expected, result)
}

func TestCoerceField_EmptyValue(t *testing.T) {
	result, ok := CoerceField("components", "")
	assert.False(t, ok)
	assert.Nil(t, result)

	result, ok = CoerceField("priority", "")
	assert.False(t, ok)
	assert.Nil(t, result)

	result, ok = CoerceField("labels", "")
	assert.False(t, ok)
	assert.Nil(t, result)
}

func TestCoerceField_UnknownField(t *testing.T) {
	result, ok := CoerceField("customfield_10001", "some-value")
	assert.False(t, ok)
	assert.Nil(t, result)
}

func TestCoerceField_UnknownFieldEmptyValue(t *testing.T) {
	result, ok := CoerceField("customfield_10001", "")
	assert.False(t, ok)
	assert.Nil(t, result)
}

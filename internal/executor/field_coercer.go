package executor

// CoerceField converts a simple string value into the JSON structure expected
// by the Jira API for well-known fields. Returns (coerced, true) if the field
// is well-known and coercion was applied, or (nil, false) if the field is not
// well-known or the value is empty.
func CoerceField(fieldName, value string) (interface{}, bool) {
	if value == "" {
		return nil, false
	}

	switch fieldName {
	case "components":
		return []interface{}{map[string]interface{}{"name": value}}, true
	case "priority":
		return map[string]interface{}{"name": value}, true
	case "labels":
		return []interface{}{value}, true
	default:
		return nil, false
	}
}

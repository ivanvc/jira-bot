package executor

// coreFields are fields handled through the existing priority chain and must
// be excluded from the extra fields map passed to the Jira client.
var coreFields = map[string]bool{
	"project":     true,
	"summary":     true,
	"description": true,
	"issuetype":   true,
}

// MergeFields combines repo config fields (base) with command-line field
// overrides. Command-line values take precedence — they replace the entire
// repo config value for a matching key (no deep merging).
// Core fields (project, summary, description, issuetype) are excluded.
// Keys are matched case-sensitively.
// Returns an empty map (not nil) when both inputs are empty.
func MergeFields(repoFields map[string]interface{}, commandFields map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{})

	// Start with repo fields as the base
	for k, v := range repoFields {
		if coreFields[k] {
			continue
		}
		result[k] = v
	}

	// Command fields override repo fields entirely
	for k, v := range commandFields {
		if coreFields[k] {
			continue
		}
		result[k] = v
	}

	return result
}

package executor

import (
	"math/rand"
	"reflect"
	"testing"
	"testing/quick"
	"unicode"

	"github.com/stretchr/testify/assert"
)

// nonCoreFieldName generates a random field name that is NOT one of the core fields.
type nonCoreFieldName string

func (nonCoreFieldName) Generate(rand *rand.Rand, size int) reflect.Value {
	// Generate a non-empty field name that avoids core field names
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_"
	for {
		length := rand.Intn(size) + 1
		buf := make([]byte, length)
		for i := range buf {
			buf[i] = chars[rand.Intn(len(chars))]
		}
		name := string(buf)
		if !coreFields[name] {
			return reflect.ValueOf(nonCoreFieldName(name))
		}
	}
}

// Feature: custom-jira-fields, Property 9: Merge priority — command fields override repo fields
// **Validates: Requirements 4.1, 4.2, 4.3, 4.4**
//
// For any two field maps (repoFields and commandFields), MergeFields SHALL produce
// a result where: (a) every key from commandFields is present with its command value,
// (b) every key from repoFields that is NOT in commandFields is present with its repo
// value, and (c) no other keys exist in the result. (Excluding core fields from both)
func TestProperty9_MergePriorityCommandOverridesRepo(t *testing.T) {
	cfg := &quick.Config{MaxCount: 100}

	f := func(repoKeys, cmdKeys []nonCoreFieldName) bool {
		// Build repo fields map with unique values per key
		repoFields := make(map[string]interface{})
		for i, k := range repoKeys {
			repoFields[string(k)] = "repo_" + string(k) + "_" + string(rune('0'+i%10))
		}

		// Build command fields map with unique values per key
		cmdFields := make(map[string]interface{})
		for i, k := range cmdKeys {
			cmdFields[string(k)] = "cmd_" + string(k) + "_" + string(rune('0'+i%10))
		}

		result := MergeFields(repoFields, cmdFields)

		// (a) Every non-core key from commandFields is present with its command value
		for k, v := range cmdFields {
			if coreFields[k] {
				continue
			}
			if result[k] != v {
				return false
			}
		}

		// (b) Every non-core key from repoFields that is NOT in commandFields is present with its repo value
		for k, v := range repoFields {
			if coreFields[k] {
				continue
			}
			if _, inCmd := cmdFields[k]; !inCmd {
				if result[k] != v {
					return false
				}
			}
		}

		// (c) No other keys exist — result length must equal unique non-core keys
		expectedKeys := make(map[string]bool)
		for k := range repoFields {
			if !coreFields[k] {
				expectedKeys[k] = true
			}
		}
		for k := range cmdFields {
			if !coreFields[k] {
				expectedKeys[k] = true
			}
		}
		if len(result) != len(expectedKeys) {
			return false
		}

		return true
	}

	if err := quick.Check(f, cfg); err != nil {
		assert.NoError(t, err, "Property 9 failed: command fields must override repo fields with correct merge semantics")
	}
}

// Feature: custom-jira-fields, Property 10: Case-sensitive field name matching
// **Validates: Requirements 4.5**
//
// For any pair of field names that differ only in letter casing, MergeFields SHALL
// treat them as distinct keys — both SHALL appear in the merged result with their
// respective values.
func TestProperty10_CaseSensitiveFieldNameMatching(t *testing.T) {
	cfg := &quick.Config{MaxCount: 100}

	f := func(baseName nonCoreFieldName) bool {
		name := string(baseName)
		if len(name) == 0 {
			return true
		}

		// Create a case-altered variant by flipping the case of the first letter
		runes := []rune(name)
		if unicode.IsLetter(runes[0]) {
			if unicode.IsUpper(runes[0]) {
				runes[0] = unicode.ToLower(runes[0])
			} else {
				runes[0] = unicode.ToUpper(runes[0])
			}
		} else {
			// If first char is not a letter, try to find one to flip
			found := false
			for i, r := range runes {
				if unicode.IsLetter(r) {
					if unicode.IsUpper(r) {
						runes[i] = unicode.ToLower(r)
					} else {
						runes[i] = unicode.ToUpper(r)
					}
					found = true
					break
				}
			}
			if !found {
				// No letters in the name, skip this case
				return true
			}
		}
		alteredName := string(runes)

		// Skip if the names ended up being the same (e.g., digits-only)
		if name == alteredName {
			return true
		}

		// Skip if either name is a core field
		if coreFields[name] || coreFields[alteredName] {
			return true
		}

		// Put original in repoFields, case-altered in commandFields
		repoFields := map[string]interface{}{name: "repo_value"}
		cmdFields := map[string]interface{}{alteredName: "cmd_value"}

		result := MergeFields(repoFields, cmdFields)

		// Both keys should be present as distinct entries
		repoVal, hasRepo := result[name]
		cmdVal, hasCmd := result[alteredName]

		if !hasRepo || !hasCmd {
			return false
		}
		if repoVal != "repo_value" || cmdVal != "cmd_value" {
			return false
		}

		return true
	}

	if err := quick.Check(f, cfg); err != nil {
		assert.NoError(t, err, "Property 10 failed: case-sensitive field names must be treated as distinct keys")
	}
}

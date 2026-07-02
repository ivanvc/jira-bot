package executor

import (
	"math/rand"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"testing/quick"
)

// isBugCondition returns true if the input contains a double-quoted segment with
// at least one space inside. This is the condition that triggers the bug.
func isBugCondition(input string) bool {
	re := regexp.MustCompile(`"[^"]*\s[^"]*"`)
	return re.MatchString(input)
}

// extractQuotedSegments returns the content inside each pair of double quotes.
func extractQuotedSegments(input string) []string {
	re := regexp.MustCompile(`"([^"]*)"`)
	matches := re.FindAllStringSubmatch(input, -1)
	var segments []string
	for _, m := range matches {
		segments = append(segments, m[1])
	}
	return segments
}

// --- Custom generators ---

// quotedFieldValue generates strings in the format key:"multi word value"
// where the quoted value always contains at least one space.
type quotedFieldValue string

func (quotedFieldValue) Generate(r *rand.Rand, size int) reflect.Value {
	const keyChars = "abcdefghijklmnopqrstuvwxyz"
	const wordChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

	// Generate a key (1-8 chars)
	keyLen := r.Intn(8) + 1
	key := make([]byte, keyLen)
	for i := range key {
		key[i] = keyChars[r.Intn(len(keyChars))]
	}

	// Generate 2-4 words for the value (ensuring spaces in quoted content)
	numWords := r.Intn(3) + 2
	var words []string
	for w := 0; w < numWords; w++ {
		wordLen := r.Intn(6) + 1
		word := make([]byte, wordLen)
		for i := range word {
			word[i] = wordChars[r.Intn(len(wordChars))]
		}
		words = append(words, string(word))
	}

	value := strings.Join(words, " ")
	result := string(key) + ":\"" + value + "\""
	return reflect.ValueOf(quotedFieldValue(result))
}

// quotedTitle generates strings in the format "multi word title"
// where the quoted value always contains at least one space.
type quotedTitle string

func (quotedTitle) Generate(r *rand.Rand, size int) reflect.Value {
	const wordChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

	// Generate 2-5 words for the title
	numWords := r.Intn(4) + 2
	var words []string
	for w := 0; w < numWords; w++ {
		wordLen := r.Intn(8) + 1
		word := make([]byte, wordLen)
		for i := range word {
			word[i] = wordChars[r.Intn(len(wordChars))]
		}
		words = append(words, string(word))
	}

	value := strings.Join(words, " ")
	result := "\"" + value + "\""
	return reflect.ValueOf(quotedTitle(result))
}

// --- Custom generator for unquoted strings ---

// unquotedInput generates random strings that do NOT contain double-quote characters.
// These represent inputs where isBugCondition returns false and existing behavior
// must be preserved unchanged.
type unquotedInput string

func (unquotedInput) Generate(r *rand.Rand, size int) reflect.Value {
	// Characters that may appear in unquoted input (no double quotes)
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789:.-_/ "

	// Generate a string of length 0 to size*2
	length := r.Intn(size*2 + 1)
	buf := make([]byte, length)
	for i := range buf {
		buf[i] = chars[r.Intn(len(chars))]
	}
	return reflect.ValueOf(unquotedInput(string(buf)))
}

// nonEmptyTokens returns the non-empty elements from strings.Split(input, " ").
// This is the baseline behavior that tokenizeLine must preserve for unquoted inputs.
func nonEmptyTokens(input string) []string {
	parts := strings.Split(input, " ")
	var result []string
	for _, p := range parts {
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

// sliceEqual compares two string slices for equality, treating nil and empty as equivalent.
func sliceEqual(a, b []string) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Feature: quoted-field-values, Property 2: Preservation - Unquoted Input Tokenization Unchanged
// **Validates: Requirements 3.1, 3.2, 3.3, 3.4**
//
// For any input line that does NOT contain double-quoted segments with spaces
// (isBugCondition returns false), the tokenizer function SHALL produce exactly
// the same tokens as strings.Split(input, " ") with empty strings removed,
// preserving all existing behavior for unquoted inputs.
func TestProperty_Preservation_UnquotedInputTokenizationUnchanged(t *testing.T) {
	cfg := &quick.Config{MaxCount: 500}

	// Property: for all generated unquoted inputs, tokenizeLine(input) equals
	// the non-empty elements of strings.Split(input, " ")
	f := func(input unquotedInput) bool {
		s := string(input)

		// Skip inputs that match bug condition (shouldn't happen with our generator,
		// but guard against it)
		if isBugCondition(s) {
			return true
		}

		got := tokenizeLine(s)
		expected := nonEmptyTokens(s)

		return sliceEqual(got, expected)
	}

	if err := quick.Check(f, cfg); err != nil {
		t.Fatalf("Preservation property failed: %v", err)
	}
}

// TestProperty_Preservation_EdgeCases tests specific edge cases for unquoted inputs
// to ensure the preservation property holds for boundary conditions.
// **Validates: Requirements 3.1, 3.2, 3.3, 3.4**
func TestProperty_Preservation_EdgeCases(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "multiple consecutive spaces",
			input:    "priority:High   type:Bug",
			expected: []string{"priority:High", "type:Bug"},
		},
		{
			name:     "leading spaces",
			input:    "   My Title priority:High",
			expected: []string{"My", "Title", "priority:High"},
		},
		{
			name:     "trailing spaces",
			input:    "priority:High type:Bug   ",
			expected: []string{"priority:High", "type:Bug"},
		},
		{
			name:     "leading and trailing spaces",
			input:    "  word  ",
			expected: []string{"word"},
		},
		{
			name:     "empty string",
			input:    "",
			expected: nil,
		},
		{
			name:     "single token no spaces",
			input:    "word",
			expected: []string{"word"},
		},
		{
			name:     "single field token",
			input:    "priority:High",
			expected: []string{"priority:High"},
		},
		{
			name:     "title words followed by field",
			input:    "My Title priority:High",
			expected: []string{"My", "Title", "priority:High"},
		},
		{
			name:     "multiple fields no spaces in values",
			input:    "priority:High type:Bug project:ENG",
			expected: []string{"priority:High", "type:Bug", "project:ENG"},
		},
		{
			name:     "only spaces",
			input:    "     ",
			expected: nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := tokenizeLine(tc.input)
			if !sliceEqual(got, tc.expected) {
				t.Errorf("tokenizeLine(%q) = %v, want %v", tc.input, got, tc.expected)
			}
		})
	}
}

// Feature: quoted-field-values, Property 1: Bug Condition - Quoted Content Tokenization
// **Validates: Requirements 1.1, 1.2, 1.3, 2.1, 2.2, 2.3**
//
// For any input line containing a double-quoted segment with spaces (isBugCondition
// returns true), the tokenizer function SHALL produce a single token for each quoted
// segment with the quote characters stripped, preserving all content between the quotes
// as-is. Tokens outside quotes are split normally on spaces.
func TestProperty_BugCondition_QuotedFieldTokenization(t *testing.T) {
	cfg := &quick.Config{MaxCount: 200}

	// Test case 1: key:"multi word value" format
	f := func(field quotedFieldValue) bool {
		input := string(field)

		// Confirm this is a bug condition input
		if !isBugCondition(input) {
			return true // skip non-bug-condition inputs
		}

		tokens := tokenizeLine(input)
		quotedSegments := extractQuotedSegments(input)

		// For each quoted segment with spaces, it should appear as a single
		// token (with quotes stripped). The key: prefix is joined with the content.
		for _, seg := range quotedSegments {
			if !strings.Contains(seg, " ") {
				continue
			}
			// The full token should contain the segment without quotes.
			// For key:"value with spaces", the expected token is key:value with spaces
			found := false
			for _, tok := range tokens {
				if strings.Contains(tok, seg) {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
		return true
	}

	if err := quick.Check(f, cfg); err != nil {
		t.Logf("Property 1 (quoted field) counterexample: %v", err)
		t.Logf("This confirms the bug: strings.Split breaks quoted field values with spaces into multiple tokens")
		t.Fail()
	}

	// Test case 2: standalone "quoted title" format
	g := func(title quotedTitle) bool {
		input := string(title)

		// Confirm this is a bug condition input
		if !isBugCondition(input) {
			return true
		}

		tokens := tokenizeLine(input)
		quotedSegments := extractQuotedSegments(input)

		// Each quoted segment with spaces should appear as a single token (quotes stripped)
		for _, seg := range quotedSegments {
			if !strings.Contains(seg, " ") {
				continue
			}
			found := false
			for _, tok := range tokens {
				if tok == seg {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
		return true
	}

	if err := quick.Check(g, cfg); err != nil {
		t.Logf("Property 1 (quoted title) counterexample: %v", err)
		t.Logf("This confirms the bug: strings.Split breaks quoted titles with spaces into multiple tokens")
		t.Fail()
	}
}

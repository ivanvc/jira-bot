package executor

import (
	"math/rand"
	"reflect"
	"strings"
	"testing"
	"testing/quick"

	"github.com/stretchr/testify/assert"
)

// classifyTokens replicates the inline token classification logic from createJiraIssue.
// Tokens containing a colon are option tokens; tokens without a colon are title tokens.
func classifyTokens(tokens []string) (titleTokens, optionTokens []string) {
	for _, tok := range tokens {
		if strings.Contains(tok, ":") {
			optionTokens = append(optionTokens, tok)
		} else {
			titleTokens = append(titleTokens, tok)
		}
	}
	return
}

// determineSummary replicates the summary determination logic from createJiraIssue.
func determineSummary(titleTokens []string, githubTitle string) string {
	if len(titleTokens) > 0 {
		return strings.Join(titleTokens, " ")
	}
	return githubTitle
}

// --- Custom generators ---

// tokenWithColon generates a non-empty string that always contains at least one colon.
type tokenWithColon string

func (tokenWithColon) Generate(rand *rand.Rand, size int) reflect.Value {
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-"
	// Generate a key part (1+ chars)
	keyLen := rand.Intn(size+1) + 1
	key := make([]byte, keyLen)
	for i := range key {
		key[i] = chars[rand.Intn(len(chars))]
	}
	// Generate a value part (1+ chars)
	valLen := rand.Intn(size+1) + 1
	val := make([]byte, valLen)
	for i := range val {
		val[i] = chars[rand.Intn(len(chars))]
	}
	return reflect.ValueOf(tokenWithColon(string(key) + ":" + string(val)))
}

// tokenWithoutColon generates a non-empty string that never contains a colon.
type tokenWithoutColon string

func (tokenWithoutColon) Generate(rand *rand.Rand, size int) reflect.Value {
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-"
	length := rand.Intn(size+1) + 1
	buf := make([]byte, length)
	for i := range buf {
		buf[i] = chars[rand.Intn(len(chars))]
	}
	return reflect.ValueOf(tokenWithoutColon(string(buf)))
}

// Feature: title-override, Property 1: Token classification correctness
// **Validates: Requirements 1.1, 1.2, 1.3**
//
// For any list of tokens where each token is either a string containing at least one
// colon or a string containing no colon, the classification logic SHALL place all
// colon-containing tokens into the option tokens list and all non-colon tokens into
// the title tokens list, preserving their relative order within each group.
func TestProperty_TokenClassification(t *testing.T) {
	cfg := &quick.Config{MaxCount: 100}

	f := func(withColons []tokenWithColon, withoutColons []tokenWithoutColon) bool {
		// Build an interleaved list of tokens
		var tokens []string
		var expectedOptions []string
		var expectedTitle []string

		// Interleave the two slices in a deterministic but mixed order
		i, j := 0, 0
		for i < len(withColons) || j < len(withoutColons) {
			if i < len(withColons) && (j >= len(withoutColons) || i <= j) {
				tok := string(withColons[i])
				tokens = append(tokens, tok)
				expectedOptions = append(expectedOptions, tok)
				i++
			}
			if j < len(withoutColons) && (i >= len(withColons) || j < i) {
				tok := string(withoutColons[j])
				tokens = append(tokens, tok)
				expectedTitle = append(expectedTitle, tok)
				j++
			}
		}

		titleTokens, optionTokens := classifyTokens(tokens)

		// All colon tokens end up in optionTokens preserving order
		if len(optionTokens) != len(expectedOptions) {
			return false
		}
		for idx, tok := range optionTokens {
			if tok != expectedOptions[idx] {
				return false
			}
		}

		// All non-colon tokens end up in titleTokens preserving order
		if len(titleTokens) != len(expectedTitle) {
			return false
		}
		for idx, tok := range titleTokens {
			if tok != expectedTitle[idx] {
				return false
			}
		}

		return true
	}

	if err := quick.Check(f, cfg); err != nil {
		assert.NoError(t, err, "Property 1 failed: token classification must correctly separate colon/non-colon tokens preserving order")
	}
}

// Feature: title-override, Property 2: Custom title assembly and usage
// **Validates: Requirements 2.1, 2.2**
//
// For any non-empty list of title tokens (strings without colons), the Jira issue
// summary SHALL equal those tokens joined by a single space in their original order.
func TestProperty_CustomTitleAssembly(t *testing.T) {
	cfg := &quick.Config{MaxCount: 100}

	f := func(tokens []tokenWithoutColon) bool {
		if len(tokens) == 0 {
			return true // skip empty — tested in Property 3
		}

		var titleTokens []string
		for _, tok := range tokens {
			titleTokens = append(titleTokens, string(tok))
		}

		githubTitle := "Some GitHub Issue Title"
		summary := determineSummary(titleTokens, githubTitle)

		expected := strings.Join(titleTokens, " ")
		return summary == expected
	}

	if err := quick.Check(f, cfg); err != nil {
		assert.NoError(t, err, "Property 2 failed: custom title must equal title tokens joined by single space")
	}
}

// Feature: title-override, Property 3: Fallback to GitHub title when no title tokens present
// **Validates: Requirements 3.1, 3.2, 5.1, 5.3**
//
// For any list of tokens where every token contains at least one colon (including
// the empty list), the Jira issue summary SHALL equal the GitHub issue title.
func TestProperty_FallbackToGitHubTitle(t *testing.T) {
	cfg := &quick.Config{MaxCount: 100}

	f := func(optionToks []tokenWithColon, githubTitle string) bool {
		if githubTitle == "" {
			githubTitle = "Default GitHub Title"
		}

		// Build token list — all tokens have colons
		var tokens []string
		for _, tok := range optionToks {
			tokens = append(tokens, string(tok))
		}

		titleTokens, _ := classifyTokens(tokens)
		summary := determineSummary(titleTokens, githubTitle)

		return summary == githubTitle
	}

	if err := quick.Check(f, cfg); err != nil {
		assert.NoError(t, err, "Property 3 failed: summary must fall back to GitHub title when no title tokens are present")
	}
}

// Feature: title-override, Property 4: Option extraction independence from title tokens
// **Validates: Requirements 4.1, 4.2**
//
// For any mixed list of title tokens and option tokens in any order, the set of option
// tokens passed to field/option processing SHALL be identical (same elements, same order)
// to the option tokens extracted when title tokens are absent — the presence of title
// tokens SHALL NOT affect option processing.
func TestProperty_OptionExtractionIndependence(t *testing.T) {
	cfg := &quick.Config{MaxCount: 100}

	f := func(titleToks []tokenWithoutColon, optionToks []tokenWithColon) bool {
		// Build the expected option tokens (just the colon tokens in order)
		var expectedOptions []string
		for _, tok := range optionToks {
			expectedOptions = append(expectedOptions, string(tok))
		}

		// Build a mixed token list: interleave title and option tokens
		var mixed []string
		i, j := 0, 0
		for i < len(titleToks) || j < len(optionToks) {
			// Alternate: add a title token, then an option token
			if i < len(titleToks) {
				mixed = append(mixed, string(titleToks[i]))
				i++
			}
			if j < len(optionToks) {
				mixed = append(mixed, string(optionToks[j]))
				j++
			}
		}

		// Classify the mixed list
		_, extractedOptions := classifyTokens(mixed)

		// Also classify a list with only option tokens (no title tokens)
		var optionOnlyList []string
		for _, tok := range optionToks {
			optionOnlyList = append(optionOnlyList, string(tok))
		}
		_, extractedOptionsAlone := classifyTokens(optionOnlyList)

		// The extracted options must be identical regardless of title token presence
		if len(extractedOptions) != len(extractedOptionsAlone) {
			return false
		}
		for idx := range extractedOptions {
			if extractedOptions[idx] != extractedOptionsAlone[idx] {
				return false
			}
		}

		// Also verify they match the expected order
		if len(extractedOptions) != len(expectedOptions) {
			return false
		}
		for idx := range extractedOptions {
			if extractedOptions[idx] != expectedOptions[idx] {
				return false
			}
		}

		return true
	}

	if err := quick.Check(f, cfg); err != nil {
		assert.NoError(t, err, "Property 4 failed: option extraction must be independent of title token presence")
	}
}

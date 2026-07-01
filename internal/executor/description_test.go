package executor

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExtractDescriptionSource(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "command line only without newline returns empty string",
			input:    "/jira create project:ENG type:Bug",
			expected: "",
		},
		{
			name:     "command line with newline and whitespace only returns empty string",
			input:    "/jira create project:ENG\n   \t  \n  ",
			expected: "",
		},
		{
			name:     "command line with newline and body text returns trimmed body",
			input:    "/jira create project:ENG\n  This is a custom description  ",
			expected: "This is a custom description",
		},
		{
			name:     "multi-line body text is preserved after trim",
			input:    "/jira create project:ENG\n\nFirst line of description\nSecond line\nThird line\n",
			expected: "First line of description\nSecond line\nThird line",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractDescriptionSource(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildDescription(t *testing.T) {
	tests := []struct {
		name              string
		descriptionSource string
		githubURL         string
		wantContains      string
		wantExact         string
		checkTruncation   bool
		checkTrailingNL   bool
	}{
		{
			name:              "empty description source produces link-only output without separator",
			descriptionSource: "",
			githubURL:         "https://github.com/org/repo/issues/1",
			wantExact:         "GitHub link: https://github.com/org/repo/issues/1\n",
			checkTrailingNL:   true,
		},
		{
			name:              "whitespace-only description source produces link-only output without separator",
			descriptionSource: "   \t\n  ",
			githubURL:         "https://github.com/org/repo/issues/2",
			wantExact:         "GitHub link: https://github.com/org/repo/issues/2\n",
			checkTrailingNL:   true,
		},
		{
			name:              "non-empty source produces structured output with separator",
			descriptionSource: "This is the ticket description",
			githubURL:         "https://github.com/org/repo/pull/42",
			wantExact:         "This is the ticket description\n\n---\n\nGitHub link: https://github.com/org/repo/pull/42\n",
			checkTrailingNL:   true,
		},
		{
			name:              "description exceeding 32000 chars triggers truncation with ellipsis",
			descriptionSource: strings.Repeat("a", 33000),
			githubURL:         "https://github.com/org/repo/issues/99",
			checkTruncation:   true,
			checkTrailingNL:   true,
		},
		{
			name:              "edge case where suffix alone exceeds MaxDescriptionLength",
			descriptionSource: "some text",
			githubURL:         strings.Repeat("u", MaxDescriptionLength),
			checkTrailingNL:   false, // output may be truncated at the limit
		},
		{
			name:              "empty URL string handling",
			descriptionSource: "A description",
			githubURL:         "",
			wantExact:         "A description\n\n---\n\nGitHub link: \n",
			checkTrailingNL:   true,
		},
		{
			name:              "empty source with empty URL",
			descriptionSource: "",
			githubURL:         "",
			wantExact:         "GitHub link: \n",
			checkTrailingNL:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := BuildDescription(tt.descriptionSource, tt.githubURL)

			// Check exact match if specified
			if tt.wantExact != "" {
				assert.Equal(t, tt.wantExact, result)
			}

			// Check trailing newline
			if tt.checkTrailingNL {
				assert.True(t, strings.HasSuffix(result, "\n"), "output should end with a trailing newline")
			}

			// Check truncation behavior
			if tt.checkTruncation {
				runeLen := len([]rune(result))
				assert.LessOrEqual(t, runeLen, MaxDescriptionLength, "output must not exceed MaxDescriptionLength")
				assert.Equal(t, MaxDescriptionLength, runeLen, "truncated output should be exactly MaxDescriptionLength")
				assert.Contains(t, result, "…", "truncated output should contain the ellipsis truncation indicator")
				assert.Contains(t, result, "\n\n---\n\n", "truncated output should still contain the separator")
				assert.Contains(t, result, "GitHub link: ", "truncated output should still contain the GitHub link")
			}

			// All results must never exceed MaxDescriptionLength
			assert.LessOrEqual(t, len([]rune(result)), MaxDescriptionLength, "output must never exceed MaxDescriptionLength")
		})
	}
}

func TestBuildDescription_SuffixExceedsMax(t *testing.T) {
	// When the suffix (separator + GitHub link + ellipsis) alone exceeds MaxDescriptionLength,
	// the function should omit the description source and return just the GitHub link truncated.
	longURL := strings.Repeat("x", MaxDescriptionLength)
	result := BuildDescription("some description text", longURL)

	runeLen := len([]rune(result))
	assert.LessOrEqual(t, runeLen, MaxDescriptionLength, "output must not exceed MaxDescriptionLength even when suffix alone is too long")
	// The output should contain at least part of the GitHub link
	assert.Contains(t, result, "GitHub link: ", "output should contain the GitHub link prefix")
	// Should not contain the separator since description source is omitted
	assert.NotContains(t, result, "\n\n---\n\n", "output should not contain separator when suffix alone exceeds max")
}

func TestBuildDescription_TrailingNewlineAlwaysPresent(t *testing.T) {
	// Test various cases to ensure trailing newline is always present
	cases := []struct {
		name   string
		source string
		url    string
	}{
		{"empty source", "", "https://github.com/org/repo/issues/1"},
		{"short source", "Hello world", "https://github.com/org/repo/pull/5"},
		{"source with newlines", "Line 1\nLine 2\nLine 3", "https://github.com/org/repo/issues/10"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			result := BuildDescription(c.source, c.url)
			assert.True(t, strings.HasSuffix(result, "\n"), "output must always end with a trailing newline")
		})
	}
}

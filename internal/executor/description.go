package executor

import (
	"strings"
)

const (
	// MaxDescriptionLength is Jira's maximum description field length in characters.
	MaxDescriptionLength = 32000

	// descriptionSeparator is placed between the description body and the GitHub link.
	descriptionSeparator = "\n\n---\n\n"

	// truncationIndicator is appended at the truncation point.
	truncationIndicator = "…" // U+2026
)

// ExtractDescriptionSource extracts the custom description from a comment body.
// If the comment contains text after the first newline (beyond the command line),
// that text (trimmed of leading/trailing whitespace) is returned.
// If no meaningful body text exists, an empty string is returned signaling
// the caller should fall back to the issue/PR body.
func ExtractDescriptionSource(commentBody string) string {
	idx := strings.IndexByte(commentBody, '\n')
	if idx < 0 {
		return ""
	}

	body := commentBody[idx+1:]
	trimmed := strings.TrimSpace(body)
	return trimmed
}

// BuildDescription assembles the final Jira ticket description from a
// description source and a GitHub URL. It appends the GitHub link with
// proper separator formatting and handles truncation to fit within
// MaxDescriptionLength (32,000 characters).
func BuildDescription(descriptionSource, githubURL string) string {
	githubFooter := "*Issue created in GitHub using */jira create.***\nGitHub Link: " + githubURL

	// If description source is empty or whitespace-only, return footer-only output.
	if strings.TrimSpace(descriptionSource) == "" {
		return githubFooter + "\n"
	}

	// Compute full description with separator.
	full := descriptionSource + descriptionSeparator + githubFooter + "\n"

	// If it fits within the limit, return as-is.
	if len([]rune(full)) <= MaxDescriptionLength {
		return full
	}

	// Truncation is needed. Compute the suffix length (separator + footer + newline + ellipsis).
	suffix := descriptionSeparator + githubFooter + "\n"
	suffixLen := len([]rune(suffix)) + 1 // +1 for the "…" truncation indicator

	// Edge case: if the suffix alone would exceed the limit, return just the footer truncated.
	if suffixLen >= MaxDescriptionLength {
		footerOutput := githubFooter + "\n"
		footerRunes := []rune(footerOutput)
		if len(footerRunes) > MaxDescriptionLength {
			footerRunes = footerRunes[:MaxDescriptionLength]
		}
		return string(footerRunes)
	}

	// Truncate the description source to fit.
	maxSourceLen := MaxDescriptionLength - suffixLen
	sourceRunes := []rune(descriptionSource)
	if len(sourceRunes) > maxSourceLen {
		sourceRunes = sourceRunes[:maxSourceLen]
	}

	return string(sourceRunes) + truncationIndicator + descriptionSeparator + githubFooter + "\n"
}

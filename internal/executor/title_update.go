package executor

import (
	"fmt"
	"strings"
)

// updateTitleMode represents the resolved update-title behavior.
type updateTitleMode string

const (
	updateTitleNone    updateTitleMode = "none"
	updateTitlePrepend updateTitleMode = "prepend"
	updateTitleAppend  updateTitleMode = "append"
)

// normalizeUpdateTitleValue converts a raw string value to a canonical mode.
// Accepts: "prepend"/"p", "append"/"a", "none" (case-insensitive).
// Returns updateTitleNone for invalid, empty, or whitespace-only input.
func normalizeUpdateTitleValue(raw string) updateTitleMode {
	v := strings.TrimSpace(strings.ToLower(raw))
	switch v {
	case "prepend", "p":
		return updateTitlePrepend
	case "append", "a":
		return updateTitleAppend
	case "none":
		return updateTitleNone
	default:
		return updateTitleNone
	}
}

// resolveUpdateTitle resolves the update-title mode using three-tier priority:
// command option > repo config > global config (env var).
func resolveUpdateTitle(commandValue, repoConfigValue, globalValue string) updateTitleMode {
	if v := strings.TrimSpace(commandValue); v != "" {
		return normalizeUpdateTitleValue(v)
	}
	if v := strings.TrimSpace(repoConfigValue); v != "" {
		return normalizeUpdateTitleValue(v)
	}
	if v := strings.TrimSpace(globalValue); v != "" {
		return normalizeUpdateTitleValue(v)
	}
	return updateTitleNone
}

// formatTitle applies the title modification based on mode.
func formatTitle(mode updateTitleMode, originalTitle, jiraKey string) string {
	switch mode {
	case updateTitlePrepend:
		return fmt.Sprintf("[%s] %s", jiraKey, originalTitle)
	case updateTitleAppend:
		return fmt.Sprintf("%s [%s]", originalTitle, jiraKey)
	default:
		return originalTitle
	}
}

// titleContainsKey returns true if the title already contains [key] (case-sensitive exact match).
func titleContainsKey(title, jiraKey string) bool {
	return strings.Contains(title, "["+jiraKey+"]")
}

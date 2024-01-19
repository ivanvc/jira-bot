package executor

import (
	"strings"

	"github.com/ivanvc/jira-bot/internal/adapters/github"
)

// Run executes the webhook and takes action if required.
func Run(issueComment *github.IssueComment) error {
	if !strings.HasPrefix(issueComment.Comment.Body, "/slack") {
		return nil
	}

	parts := strings.Split(issueComment.Comment.Body, " ")
	if len(parts) == 1 {
		// TODO: No command, reply with help
		return nil
	}

	switch parts[1] {
	case "help":
		// TODO: Help
	case "create":
		// TODO: Create issue
	}

	return nil
}

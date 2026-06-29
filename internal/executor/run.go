package executor

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/log"
	"github.com/ivanvc/jira-bot/internal/adapters/github"
	"github.com/ivanvc/jira-bot/internal/common"
)

const errorAlreadyCreated = `:warning: Uh-oh, a Jira issue for this issue seems to have been already created by me :crying_cat_face:`

const successTextFormat = `:heavy_check_mark: I created the Jira issue: [%s]`

const helpTextFormat = `Hi there!

The following is my list of commands:
* ` + "`/jira help`" + `: Prints this message.
* ` + "`/jira create [options]`" + `: Creates a Jira issue with the subject and body from
  this GitHub issue. The following options are available:
	* ` + "`type:[type]`" + `: Specify the type of the Jira issue to create (i.e., ` + "`type:Bug`" + `, default: ` + "`%s`" + `)
	* ` + "`project:[project]`" + `: Specify the project of the Jira issue to create (i.e., ` + "`project:ENG`" + `, default: ` + "`%s`" + `)`

const errorMessageFormat = `:x: Error trying to create issue.

<details>
<summary>Error</summary>

` + "```" + `
%s
` + "```" + `
</details>
`

// errAlreadyCreated is a sentinel error indicating the issue was already created.
// The caller should not post an additional error comment for this case.
var errAlreadyCreated = errors.New("a Jira issue seems to have been already created")

// Run executes the webhook and takes action if required.
func Run(ctx context.Context, state *common.State, issueComment *github.IssueComment) error {
	if !strings.HasPrefix(issueComment.Comment.Body, "/jira") {
		return nil
	}

	parts := strings.Split(issueComment.Comment.Body, " ")
	if len(parts) == 1 {
		if err := replyWithHelp(ctx, state, issueComment); err != nil {
			return err
		}
		if err := state.GitHubClient.ReactWithThumbsUp(ctx, issueComment.Installation.ID, issueComment); err != nil {
			return err
		}
		return nil
	}

	switch parts[1] {
	case "help":
		if err := replyWithHelp(ctx, state, issueComment); err != nil {
			return err
		}
	case "create":
		if err := createJiraIssue(ctx, state, issueComment, parts[2:]); err != nil {
			if errors.Is(err, errAlreadyCreated) {
				return nil
			}
			state.GitHubClient.ReactWithConfused(ctx, issueComment.Installation.ID, issueComment)
			errorMsg := fmt.Sprintf(errorMessageFormat, err)
			state.GitHubClient.PostComment(ctx, issueComment.Installation.ID, issueComment, errorMsg)

			return err
		}
	}

	if err := state.GitHubClient.ReactWithThumbsUp(ctx, issueComment.Installation.ID, issueComment); err != nil {
		return err
	}

	return nil
}

func replyWithHelp(ctx context.Context, state *common.State, issueComment *github.IssueComment) error {
	defaultType := state.Config.JiraDefaultIssueType
	defaultProject := state.Config.JiraDefaultProject
	var fieldNames []string

	if state.RepoConfigLoader != nil {
		repoCfg, err := state.RepoConfigLoader.LoadRepoConfig(
			ctx,
			issueComment.Installation.ID,
			issueComment.Repository.Owner.Login,
			issueComment.Repository.Name,
		)
		if err != nil {
			log.Error("Error loading repo config for help", "error", err)
		} else {
			defaultType = resolveOption("", repoCfg.Type, state.Config.JiraDefaultIssueType)
			defaultProject = resolveOption("", repoCfg.Project, state.Config.JiraDefaultProject)
			if len(repoCfg.Fields) > 0 {
				for k := range repoCfg.Fields {
					fieldNames = append(fieldNames, k)
				}
				sort.Strings(fieldNames)
			}
		}
	}

	helpText := fmt.Sprintf(helpTextFormat, defaultType, defaultProject)
	if len(fieldNames) > 0 {
		helpText += "\n\n**Default fields:** " + strings.Join(fieldNames, ", ")
	}
	return state.GitHubClient.PostComment(ctx, issueComment.Installation.ID, issueComment, helpText)
}

func createJiraIssue(ctx context.Context, state *common.State, issueComment *github.IssueComment, options []string) error {
	issueBody := issueComment.Issue.Body
	if strings.Contains(issueBody, "<!--JIRA_BOT_ISSUE") {
		if err := state.GitHubClient.PostComment(ctx, issueComment.Installation.ID, issueComment, errorAlreadyCreated); err != nil {
			return err
		}
		return errAlreadyCreated
	}

	// Load repo config if available
	var repoProject, repoType string
	var repoFields map[string]interface{}
	if state.RepoConfigLoader != nil {
		repoCfg, err := state.RepoConfigLoader.LoadRepoConfig(
			ctx,
			issueComment.Installation.ID,
			issueComment.Repository.Owner.Login,
			issueComment.Repository.Name,
		)
		if err != nil {
			return fmt.Errorf("loading repo config: %w", err)
		}
		repoProject = repoCfg.Project
		repoType = repoCfg.Type
		repoFields = repoCfg.Fields
	}

	commandProject := loadOptionFromCommand("project", options)
	commandType := loadOptionFromCommand("type", options)

	project := resolveOption(commandProject, repoProject, state.Config.JiraDefaultProject)
	issueType := resolveOption(commandType, repoType, state.Config.JiraDefaultIssueType)

	// Merge repo config fields with command-line field overrides
	commandFields := loadFieldsFromCommand(options)
	extraFields := MergeFields(repoFields, commandFields)

	if state.JiraClient == nil {
		return errors.New("Jira client is not configured (bot is in setup mode)")
	}

	key, err := state.JiraClient.CreateIssue(project, issueType, issueComment.Issue.Title, fmt.Sprintf("%s\n\nGitHub link: %s\n", issueBody, issueComment.Issue.HTMLURL), extraFields)
	if err != nil {
		return err
	}
	body := fmt.Sprintf("%s\n\n<!--JIRA_BOT_ISSUE:[%s]-->", issueBody, key)
	if err := state.GitHubClient.UpdateIssueDescription(ctx, issueComment.Installation.ID, issueComment, body); err != nil {
		return err
	}

	return state.GitHubClient.PostComment(ctx, issueComment.Installation.ID, issueComment, fmt.Sprintf(successTextFormat, key))
}

// resolveOption returns the first non-empty value in priority order:
// command option > repo config > global config.
func resolveOption(commandValue, repoConfigValue, globalValue string) string {
	if commandValue != "" {
		return commandValue
	}
	if repoConfigValue != "" {
		return repoConfigValue
	}
	return globalValue
}

// loadOptionFromCommand extracts a named option value from command arguments.
// Returns an empty string if the option is not found or has an empty value.
func loadOptionFromCommand(option string, values []string) string {
	for _, v := range values {
		pair := strings.Split(v, ":")
		if pair[0] == option && len(pair) > 1 && pair[1] != "" {
			return pair[1]
		}
	}
	return ""
}

// loadOptionWithDefault extracts a named option from values, returning fallback if not found.
// Kept for backward compatibility.
func loadOptionWithDefault(option, fallback string, values []string) string {
	if v := loadOptionFromCommand(option, values); v != "" {
		return v
	}
	return fallback
}

// maxFieldOverrides is the maximum number of field overrides allowed in a single command.
const maxFieldOverrides = 20

// loadFieldsFromCommand extracts all key:value pairs from command options
// that are not "project" or "type", applying coercion for well-known fields.
// The first colon is the delimiter; remaining colons are part of the value.
// Duplicate keys: last occurrence wins. Empty values are ignored.
// A maximum of 20 non-reserved key:value pairs are processed.
func loadFieldsFromCommand(options []string) map[string]interface{} {
	result := make(map[string]interface{})
	count := 0

	for _, opt := range options {
		parts := strings.SplitN(opt, ":", 2)
		if len(parts) != 2 {
			continue
		}

		key := parts[0]
		value := parts[1]

		// Skip reserved keys
		if key == "project" || key == "type" {
			continue
		}

		// Ignore empty values
		if value == "" {
			continue
		}

		// Cap at maxFieldOverrides
		count++
		if count > maxFieldOverrides {
			continue
		}

		// Apply coercion for well-known fields; use raw string for others
		if coerced, ok := CoerceField(key, value); ok {
			result[key] = coerced
		} else {
			result[key] = value
		}
	}

	return result
}

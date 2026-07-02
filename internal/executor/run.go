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
* ` + "`/jira create [title words] [options]`" + `: Creates a Jira issue. The following options are available:
	* ` + "`type:[type]`" + `: Specify the type of the Jira issue to create (i.e., ` + "`type:Bug`" + `, default: ` + "`%s`" + `)
	* ` + "`project:[project]`" + `: Specify the project of the Jira issue to create (i.e., ` + "`project:ENG`" + `, default: ` + "`%s`" + `)
	* ` + "`assign:true|false`" + `: Assign the created issue to yourself (default: ` + "`false`" + `)

**Custom title:** Words without a colon become the Jira issue title. If no title words are provided, the GitHub issue or pull request title is used by default. Title words and options can be mixed in any order.

**Custom description:** Text provided after a newline following the command line will be used as the Jira ticket description. If no custom description is provided, the GitHub issue or pull request body is used by default.

Examples:
` + "```" + `
/jira create My Custom Title project:ENG type:Bug
` + "```" + `
` + "```" + `
/jira create project:ENG type:Bug
This is a custom description for the Jira ticket.
It can span multiple lines.
` + "```"

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

// tokenizeLine splits an input string into tokens respecting double-quoted segments.
// Quoted content (including spaces) is preserved as a single token with quote characters
// stripped. Unquoted spaces delimit tokens. Unclosed quotes treat remaining content as
// one token.
func tokenizeLine(input string) []string {
	var tokens []string
	var buf strings.Builder
	inQuotes := false

	for i := 0; i < len(input); i++ {
		ch := input[i]
		switch {
		case ch == '"':
			inQuotes = !inQuotes
		case ch == ' ' && !inQuotes:
			if buf.Len() > 0 {
				tokens = append(tokens, buf.String())
				buf.Reset()
			}
		default:
			buf.WriteByte(ch)
		}
	}

	if buf.Len() > 0 {
		tokens = append(tokens, buf.String())
	}

	return tokens
}

// Run executes the webhook and takes action if required.
func Run(ctx context.Context, state *common.State, issueComment *github.IssueComment) error {
	if !strings.HasPrefix(issueComment.Comment.Body, "/jira") {
		return nil
	}

	// Parse only the first line for command and options.
	firstLine := issueComment.Comment.Body
	if idx := strings.IndexByte(firstLine, '\n'); idx >= 0 {
		firstLine = firstLine[:idx]
	}

	parts := tokenizeLine(firstLine)
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
	// Separate title tokens from option tokens.
	var titleTokens []string
	var optionTokens []string
	for _, tok := range options {
		if strings.Contains(tok, ":") {
			optionTokens = append(optionTokens, tok)
		} else {
			titleTokens = append(titleTokens, tok)
		}
	}

	// Determine the issue summary.
	summary := issueComment.Issue.Title
	if len(titleTokens) > 0 {
		summary = strings.Join(titleTokens, " ")
	}

	// Replace options with optionTokens for all downstream processing.
	options = optionTokens

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
	var repoAssign *bool
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
		repoAssign = repoCfg.Assign
	}

	commandProject := loadOptionFromCommand("project", options)
	commandType := loadOptionFromCommand("type", options)

	project := resolveOption(commandProject, repoProject, state.Config.JiraDefaultProject)
	issueType := resolveOption(commandType, repoType, state.Config.JiraDefaultIssueType)

	// Merge repo config fields with command-line field overrides
	commandFields := loadFieldsFromCommand(options)
	extraFields := MergeFields(repoFields, commandFields)

	// Resolve the Jira client: per-user via JiraClientResolver, or fallback to global JiraClient.
	jiraClient, err := resolveJiraClient(ctx, state, issueComment)
	if err != nil {
		return err
	}
	if jiraClient == nil {
		// Auth link was posted or an error comment was posted; nothing more to do.
		return nil
	}

	// Resolve assign option
	commandAssign := loadOptionFromCommand("assign", options)
	assignEnabled := resolveAssignOption(commandAssign, repoAssign, state.Config.JiraDefaultAssign)

	// If assign is enabled and user has an accountId, add assignee to extraFields
	if assignEnabled {
		accountID := resolveAccountID(ctx, state, issueComment.Comment.User.Login)
		if accountID != "" {
			if extraFields == nil {
				extraFields = make(map[string]interface{})
			}
			extraFields["assignee"] = map[string]interface{}{
				"accountId": accountID,
			}
		}
	}

	// Determine description source: use comment body override if present, otherwise fall back to issue body
	descriptionSource := ExtractDescriptionSource(issueComment.Comment.Body)
	if descriptionSource == "" {
		descriptionSource = issueBody
	}

	description := BuildDescription(descriptionSource, issueComment.Issue.HTMLURL)

	key, err := jiraClient.CreateIssue(project, issueType, summary, description, extraFields)
	if err != nil {
		// If the Jira API returns 401, mark the token entry as invalid and post re-auth link.
		if state.JiraClientResolver != nil && strings.Contains(err.Error(), "401") {
			login := strings.TrimSpace(issueComment.Comment.User.Login)
			if login != "" {
				// Mark entry invalid by re-resolving (the resolver handles marking).
				// We need to mark it invalid in the store directly.
				markTokenInvalid(ctx, state, login)
				result := state.JiraClientResolver.Resolve(ctx, login)
				if result.AuthRequired {
					authMsg := fmt.Sprintf(":lock: Your Jira authorization has expired. Please [re-authorize](%s) and try again.", result.AuthLink)
					state.GitHubClient.PostComment(ctx, issueComment.Installation.ID, issueComment, authMsg)
				}
			}
		}
		return err
	}
	body := fmt.Sprintf("%s\n\n<!--JIRA_BOT_ISSUE:[%s]-->", issueBody, key)
	if err := state.GitHubClient.UpdateIssueDescription(ctx, issueComment.Installation.ID, issueComment, body); err != nil {
		return err
	}

	return state.GitHubClient.PostComment(ctx, issueComment.Installation.ID, issueComment, fmt.Sprintf(successTextFormat, key))
}

// resolveJiraClient determines which Jira client to use for issue creation.
// It uses per-user token resolution via JiraClientResolver.
// Returns (nil, nil) when an auth link or error comment has been posted and the caller should stop.
// Returns (nil, error) when an unrecoverable error occurs.
func resolveJiraClient(ctx context.Context, state *common.State, issueComment *github.IssueComment) (common.JiraClientInterface, error) {
	if state.JiraClientResolver == nil {
		return nil, errors.New("JiraClientResolver is not configured")
	}

	// Extract and validate the GitHub user login from the comment.
	login := strings.TrimSpace(issueComment.Comment.User.Login)
	if login == "" {
		errMsg := ":x: Could not identify the GitHub user who issued this command. Please ensure your comment includes valid user information."
		if err := state.GitHubClient.PostComment(ctx, issueComment.Installation.ID, issueComment, errMsg); err != nil {
			return nil, err
		}
		return nil, nil
	}

	// Resolve the per-user Jira client.
	result := state.JiraClientResolver.Resolve(ctx, login)

	if result.AuthRequired {
		authMsg := fmt.Sprintf(":lock: You need to authorize the bot with your Jira account before creating issues. Please [authorize here](%s) and try again.", result.AuthLink)
		if err := state.GitHubClient.PostComment(ctx, issueComment.Installation.ID, issueComment, authMsg); err != nil {
			return nil, err
		}
		return nil, nil
	}

	if result.ErrorMsg != "" {
		return nil, errors.New(result.ErrorMsg)
	}

	if result.Client == nil {
		return nil, errors.New("resolver returned neither a client nor an auth link")
	}

	return result.Client, nil
}

// markTokenInvalid marks the user's token entry as invalid in the UserTokenStore.
// This is called when a Jira API call returns HTTP 401.
func markTokenInvalid(ctx context.Context, state *common.State, login string) {
	if state.UserTokenStore == nil {
		return
	}
	entry, err := state.UserTokenStore.Read(ctx, login)
	if err != nil {
		return
	}
	entry.Status = "invalid"
	_ = state.UserTokenStore.Write(ctx, login, entry)
}

// resolveAssignOption resolves the assign boolean using three-tier priority:
// command-line > repo config > global config.
func resolveAssignOption(commandValue string, repoConfigValue *bool, globalValue bool) bool {
	// Command-line override (highest priority)
	switch strings.ToLower(commandValue) {
	case "true":
		return true
	case "false":
		return false
	}
	// Repo config (middle priority)
	if repoConfigValue != nil {
		return *repoConfigValue
	}
	// Global config (lowest priority)
	return globalValue
}

// resolveAccountID reads the user's accountId from the token store.
// Returns empty string if unavailable (no error surfaced to user).
func resolveAccountID(ctx context.Context, state *common.State, login string) string {
	if state.UserTokenStore == nil || login == "" {
		return ""
	}
	entry, err := state.UserTokenStore.Read(ctx, login)
	if err != nil {
		return ""
	}
	return entry.AccountID
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

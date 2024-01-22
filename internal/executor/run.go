package executor

import (
	"context"
	"errors"
	"fmt"
	"strings"

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
		if err := state.GitHubClient.ReactWithThumbsUp(ctx, issueComment); err != nil {
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
			return err
		}
	}

	if err := state.GitHubClient.ReactWithThumbsUp(ctx, issueComment); err != nil {
		return err
	}

	return nil
}

func replyWithHelp(ctx context.Context, state *common.State, issueComment *github.IssueComment) error {
	return state.GitHubClient.PostComment(ctx, issueComment, fmt.Sprintf(helpTextFormat, state.Config.JiraDefaultIssueType, state.Config.JiraDefaultProject))
}

func createJiraIssue(ctx context.Context, state *common.State, issueComment *github.IssueComment, options []string) error {
	issueBody := issueComment.Issue.Body
	if strings.Contains(issueBody, "<!--JIRA_BOT_ISSUE") {
		if err := state.GitHubClient.PostComment(ctx, issueComment, errorAlreadyCreated); err != nil {
			return err
		}
		return errors.New("a jira issue seems to have been already created")
	}

	project := loadOptionWithDefault("project", state.Config.JiraDefaultProject, options)
	issueType := loadOptionWithDefault("type", state.Config.JiraDefaultIssueType, options)
	key, err := state.JiraClient.CreateIssue(project, issueType, issueComment.Issue.Title, fmt.Sprintf("%s\n\nGitHub link: %s\n", issueBody, issueComment.Issue.URL))
	if err != nil {
		return err
	}
	body := fmt.Sprintf("%s\n\n<!--JIRA_BOT_ISSUE:[%s]-->", issueBody, key)
	if err := state.GitHubClient.UpdateIssueDescription(ctx, issueComment, body); err != nil {
		return err
	}

	return state.GitHubClient.PostComment(ctx, issueComment, fmt.Sprintf(successTextFormat, key))
}

func loadOptionWithDefault(option, fallback string, values []string) string {
	for _, v := range values {
		pair := strings.Split(v, ":")
		if pair[0] == option && len(pair) > 1 && pair[1] != "" {
			return pair[1]
		}
	}
	return fallback
}

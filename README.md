# Jira Bot

A GitHub App that creates Jira issues when you post `/jira create` comments in GitHub issues.

## Setup

### 1. Create GitHub App

1. Go to your organization's Settings > Developer settings > GitHub Apps
2. Click "New GitHub App"
3. Set:
   - **App name**: `jira-bot`
   - **Webhook URL**: `https://your-domain.com/webhooks/github/payload`
   - **Webhook secret**: Generate using `openssl rand -hex 20`
   - **Permissions**: Issues (Read & write), Issue comments (Read & write)
   - **Subscribe to events**: Issue comments
4. Generate and download private key
5. Note the App ID

### 2. Install App

Install the GitHub App on your organization or specific repositories.

### 3. Configure Environment

```bash
JIRA_BOT_GITHUB_APP_ID=123456
JIRA_BOT_GITHUB_PRIVATE_KEY="-----BEGIN RSA PRIVATE KEY-----\n..."
JIRA_BOT_GITHUB_WEBHOOK_SECRET=$(openssl rand -hex 20)
JIRA_BOT_JIRA_BASE_URL=https://company.atlassian.net
JIRA_BOT_JIRA_USERNAME=bot@company.com
JIRA_BOT_JIRA_TOKEN=your-jira-token
JIRA_BOT_JIRA_DEFAULT_PROJECT=ENG
JIRA_BOT_JIRA_DEFAULT_ISSUE_TYPE=Task
```

## Usage

Comment `/jira create` on any GitHub issue to create a Jira ticket.

# Jira Bot

A GitHub App that creates Jira issues when you post `/jira create` comments in GitHub issues. Each user authenticates with their own Atlassian account, so issues are attributed to the person who created them.

## How It Works

When a user runs `/jira create` for the first time, the bot replies with an authorization link. The user clicks the link and goes through a two-step OAuth flow:

1. **GitHub OAuth** — confirms the user's GitHub identity (uses the GitHub App's user-to-server OAuth).
2. **Atlassian OAuth** — the user grants the bot access to their Jira account.

After authorization, the bot stores the user's Jira tokens in a Kubernetes Secret. On subsequent `/jira create` commands, the bot uses the stored tokens to create issues under that user's Jira identity — no re-authorization needed.

Tokens are refreshed proactively in the background by a leader pod, so they stay valid without user intervention. If a token becomes permanently invalid (e.g., the user revokes access), the bot prompts the user to re-authorize.

## Setup

### 1. Create GitHub App

1. Go to your organization's Settings > Developer settings > GitHub Apps
2. Click "New GitHub App"
3. Set:
   - **App name**: `jira-bot`
   - **Webhook URL**: `https://<your-bot-host>/webhooks/github/payload`
   - **Webhook secret**: Generate using `openssl rand -hex 20`
   - **Permissions**: Issues (Read & write), Issue comments (Read & write), Contents (Read-only)
   - **Subscribe to events**: Issue comments
4. Generate and download private key
5. Note the App ID

### 2. Enable User-to-Server OAuth on the GitHub App

The bot uses the GitHub App's OAuth capabilities to verify user identity during the authorization flow. You need to enable this on the same GitHub App created above:

1. In your GitHub App settings, scroll to **Identifying and authorizing users**
2. Check **"Request user authorization (OAuth) during installation"** (or enable it post-install)
3. Set **Callback URL** to: `https://<your-bot-host>/oauth/user/github/callback`
4. Click **Generate a new client secret** — this becomes your `JIRA_BOT_GITHUB_APP_CLIENT_SECRET`
5. Note the **Client ID** shown on the app's settings page (same as `JIRA_BOT_GITHUB_APP_ID` for most setups)

### 3. Install App

Install the GitHub App on your organization or specific repositories.

### 4. Create Atlassian OAuth 2.0 App

The bot uses OAuth 2.0 (3LO) to authenticate individual users with Jira Cloud.

#### 4.1 Register the App

1. Go to [developer.atlassian.com/console/myapps](https://developer.atlassian.com/console/myapps/)
2. Click **Create** > **OAuth 2.0 integration**
3. Give it a name (e.g. `jira-bot`) and agree to the terms

#### 4.2 Configure Permissions

1. In the app settings, go to **Permissions**
2. Click **Add** next to **Jira API**
3. Under **Jira platform REST API**, add:
   - `write:jira-work` (Create and edit issues)
   - `read:jira-work` (Read issues)
4. Click **Save**

#### 4.3 Configure Authorization

1. Go to **Authorization** in the left sidebar
2. Click **Add** next to **OAuth 2.0 (3LO)**
3. Set the **Callback URL** to: `https://<your-bot-host>/oauth/user/atlassian/callback`
4. Click **Save**

#### 4.4 Get Client Credentials

1. Go to **Settings** in the left sidebar
2. Note the **Client ID** and **Secret** — these are your `JIRA_BOT_JIRA_CLIENT_ID` and `JIRA_BOT_JIRA_CLIENT_SECRET`

### 5. Configure Environment

```bash
# GitHub App (required)
JIRA_BOT_GITHUB_APP_ID=123456
JIRA_BOT_GITHUB_PRIVATE_KEY="-----BEGIN RSA PRIVATE KEY-----\n..."
JIRA_BOT_GITHUB_WEBHOOK_SECRET=$(openssl rand -hex 20)
JIRA_BOT_GITHUB_APP_CLIENT_SECRET=your-github-app-client-secret

# Atlassian OAuth 2.0 (required)
JIRA_BOT_JIRA_CLIENT_ID=your-atlassian-client-id
JIRA_BOT_JIRA_CLIENT_SECRET=your-atlassian-client-secret

# Per-user token configuration (required)
JIRA_BOT_USER_AUTH_CALLBACK_URL=https://<your-bot-host>
JIRA_BOT_GLOBAL_CLOUD_ID=your-atlassian-cloud-id

# Jira defaults (required)
JIRA_BOT_JIRA_DEFAULT_PROJECT=ENG
JIRA_BOT_JIRA_DEFAULT_ISSUE_TYPE=Task

# Per-user token storage (optional, have sensible defaults)
JIRA_BOT_USER_TOKEN_SECRET_NAME=jira-bot-user-tokens
JIRA_BOT_REFRESH_CHECK_INTERVAL=30s
```

### 6. Deploy with Helm

```bash
helm install jira-bot charts/jira-bot \
  --set secrets.github.webhookSecret="your-webhook-secret" \
  --set secrets.github.privateKeyBase64="base64-encoded-private-key" \
  --set secrets.github.appClientSecret="your-github-app-client-secret" \
  --set secrets.jira.clientID="your-atlassian-client-id" \
  --set secrets.jira.clientSecret="your-atlassian-client-secret" \
  --set config.githubAppID="123456" \
  --set config.globalCloudID="your-cloud-id" \
  --set config.jira.defaultProject="ENG" \
  --set config.jira.defaultIssueType="Task" \
  --set perUserTokens.callbackURL="https://jira-bot.example.com"
```

## Environment Variables

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `JIRA_BOT_GITHUB_APP_ID` | Yes | — | GitHub App ID |
| `JIRA_BOT_GITHUB_PRIVATE_KEY` | Yes | — | GitHub App private key (PEM) |
| `JIRA_BOT_GITHUB_WEBHOOK_SECRET` | Yes | — | GitHub webhook secret |
| `JIRA_BOT_GITHUB_APP_CLIENT_SECRET` | Yes | — | GitHub App client secret for user-to-server OAuth |
| `JIRA_BOT_JIRA_CLIENT_ID` | Yes | — | Atlassian OAuth 2.0 client ID |
| `JIRA_BOT_JIRA_CLIENT_SECRET` | Yes | — | Atlassian OAuth 2.0 client secret |
| `JIRA_BOT_JIRA_DEFAULT_PROJECT` | Yes | — | Default Jira project key |
| `JIRA_BOT_JIRA_DEFAULT_ISSUE_TYPE` | Yes | — | Default Jira issue type |
| `JIRA_BOT_USER_AUTH_CALLBACK_URL` | Yes | — | Base URL for OAuth callback endpoints |
| `JIRA_BOT_GLOBAL_CLOUD_ID` | Yes | — | Atlassian Cloud ID for the target Jira site |
| `JIRA_BOT_USER_TOKEN_SECRET_NAME` | No | `jira-bot-user-tokens` | K8s Secret name for per-user token storage |
| `JIRA_BOT_REFRESH_CHECK_INTERVAL` | No | `30s` | How often the leader checks for tokens needing refresh (min: 10s, max: 300s) |
| `JIRA_BOT_LISTEN_HTTP` | No | `:8080` | HTTP listen address |
| `POD_NAME` | No | — | Pod name (from downward API, enables leader election) |
| `POD_NAMESPACE` | No | — | Pod namespace (from downward API, enables leader election) |
| `JIRA_BOT_TOKEN_LEASE_NAME` | No | — | K8s Lease name for leader election |
| `JIRA_BOT_LEASE_DURATION` | No | `15s` | Leader election lease duration |
| `JIRA_BOT_LEASE_RENEW_DEADLINE` | No | `10s` | Leader election lease renewal deadline |

## Per-User Authorization Flow

From the user's perspective:

1. User comments `/jira create` on a GitHub issue.
2. If the bot doesn't have tokens for that user, it replies with an authorization link.
3. User clicks the link → authenticates with GitHub (confirms identity) → authorizes with Atlassian (grants Jira access).
4. Bot stores the user's tokens and shows a success page.
5. User returns to the issue and runs `/jira create` again — the issue is created under their Jira identity.

If a user's token becomes invalid (revoked or expired beyond recovery), the bot posts a new authorization link on the next `/jira create` attempt.

## Token Refresh (Multi-Replica)

The bot proactively refreshes user tokens in the background so they don't expire between uses. Only one pod (the leader) performs refresh operations to prevent token rotation conflicts.

### How It Works

- **Leader election**: One pod acquires a Kubernetes Lease and runs the Multi-User Refresh Manager. Other pods read tokens from the shared K8s Secret.
- **Proactive refresh**: The leader checks all stored tokens at the configured interval (default 30s). Tokens expiring within 5 minutes are refreshed.
- **Concurrency**: Up to 5 simultaneous refresh requests to avoid overwhelming the Atlassian token endpoint.
- **Error handling**: Non-retryable errors (HTTP 4xx) mark the token as invalid. Retryable errors (5xx/network) use exponential backoff before marking as failed.
- **Graceful shutdown**: If the leader loses its lease, in-flight refresh operations are cancelled within 5 seconds.

### Kubernetes RBAC Requirements

The bot needs the following Kubernetes permissions (created automatically when `rbac.create=true` in the Helm chart):

```yaml
rules:
  - apiGroups: [""]
    resources: ["secrets"]
    verbs: ["get", "create", "update", "patch"]
  - apiGroups: ["coordination.k8s.io"]
    resources: ["leases"]
    verbs: ["get", "create", "update"]
```

The bot uses these permissions to:
- **Secrets**: Read and write the per-user token Secret (`JIRA_BOT_USER_TOKEN_SECRET_NAME`)
- **Leases**: Perform leader election for the token refresh manager

If RBAC permissions are missing, the bot cannot store or retrieve user tokens and will fail to process `/jira create` commands.

### Helm Configuration

| Value | Default | Description |
|-------|---------|-------------|
| `perUserTokens.secretName` | `{{ fullname }}-user-tokens` | K8s Secret name for per-user tokens |
| `perUserTokens.callbackURL` | — | Base URL for OAuth callbacks (required) |
| `perUserTokens.refreshCheckInterval` | `30s` | Token refresh check interval |
| `leaderElection.leaseName` | `{{ fullname }}-leader` | Leader election lease name |
| `leaderElection.leaseDuration` | `15s` | Lease duration |
| `leaderElection.leaseRenewDeadline` | `10s` | Lease renewal deadline |
| `rbac.create` | `true` | Create ServiceAccount, Role, and RoleBinding |
| `serviceAccount.create` | `true` | Create a ServiceAccount for the bot |
| `serviceAccount.name` | `{{ fullname }}` | ServiceAccount name override |

## Per-Repository Configuration

You can define repository-level defaults by adding a YAML config file to your repo. The bot checks for this file on each command invocation.

### File Location

The bot looks for the config file in this order:

1. `.github/jira-bot.yaml`
2. `jira-bot.yaml` (repository root)

If both exist, `.github/jira-bot.yaml` wins. If neither exists, the bot falls back to global defaults.

### Supported Fields

```yaml
# .github/jira-bot.yaml
project: ENG
type: Bug
fields:
  components:
    - name: Backend
  priority:
    name: High
  labels:
    - team-platform
    - sprint-42
  customfield_10001: "My Custom Value"
```

| Field | Description |
|-------|-------------|
| `project` | Default Jira project key |
| `type` | Default Jira issue type |
| `fields` | Map of arbitrary Jira fields included in every issue created from this repo |

All fields are optional. The `fields` map accepts any structure that matches the Jira API schema for your project — scalars, arrays, or nested objects.

### Priority Chain

When resolving the project and issue type, the bot uses this priority order:

| Priority | Source | Example |
|----------|--------|---------|
| 1 (highest) | Command options | `project:OPS` in the comment |
| 2 | Repo config file | `project: ENG` in YAML |
| 3 (lowest) | Global config | `JIRA_BOT_JIRA_DEFAULT_PROJECT` env var |

Command-line options always win. The repo config overrides global defaults but is itself overridden by explicit command options.

### Command-Line Field Overrides

You can specify Jira fields inline when creating an issue. Any `key:value` pair that isn't `project` or `type` is treated as a field override:

```
/jira create priority:High components:Backend customfield_10001:myvalue
```

Rules:
- The first colon is the delimiter — remaining colons are part of the value (e.g., `customfield_10001:http://example.com` sets the value to `http://example.com`)
- If the same key appears more than once, the last occurrence wins
- Empty values (e.g., `priority:`) are ignored
- Up to 20 field overrides are supported per command

### Smart Coercion for Well-Known Fields

When you specify well-known fields on the command line, the bot automatically converts the simple string value into the JSON structure expected by the Jira API:

| Field | Command Syntax | JSON Produced |
|-------|---------------|---------------|
| `components` | `components:Backend` | `[{"name": "Backend"}]` |
| `priority` | `priority:High` | `{"name": "High"}` |
| `labels` | `labels:urgent` | `["urgent"]` |

This coercion applies only to command-line values. Values defined in the repo config `fields` map are sent as-is (they're already structured YAML).

### Field Priority Chain

Fields follow the same override pattern as `project` and `type`:

| Priority | Source |
|----------|--------|
| 1 (highest) | Command-line field overrides |
| 2 (lowest) | Repo config `fields` map |

Command-line field values replace repo config values entirely — there is no deep merge. For example, if your repo config defines `components` with two entries and you specify `components:Frontend` on the command line, the final value is `[{"name": "Frontend"}]` (not appended to the repo config list).

### Error Handling

- **Missing file**: Bot uses global defaults silently.
- **Invalid YAML**: Bot posts an error comment on the issue so the repo maintainer can fix it.
- **GitHub API error**: Bot logs the error and falls back to global defaults.

The `/jira help` command shows the effective defaults for the current repository, reflecting repo-level config when available.

## Usage

Comment `/jira create` on any GitHub issue to create a Jira ticket.

## Development

```bash
# Run tests
go test ./...

# Build
go build ./cmd/jb

# Lint (if golangci-lint is installed)
golangci-lint run
```

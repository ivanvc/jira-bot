# Jira Bot

A GitHub App that creates Jira issues when you post `/jira create` comments in GitHub issues.

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

### 2. Install App

Install the GitHub App on your organization or specific repositories.

### 3. Create Atlassian OAuth 2.0 App

The bot uses OAuth 2.0 (3LO) to authenticate with Jira Cloud. You need to create an app in the Atlassian Developer Console and obtain credentials.

#### 3.1 Register the App

1. Go to [developer.atlassian.com/console/myapps](https://developer.atlassian.com/console/myapps/)
2. Click **Create** > **OAuth 2.0 integration**
3. Give it a name (e.g. `jira-bot`) and agree to the terms

#### 3.2 Configure Permissions

1. In the app settings, go to **Permissions**
2. Click **Add** next to **Jira API**
3. Under **Jira platform REST API**, add:
   - `write:jira-work` (Create and edit issues)
   - `read:jira-work` (Read issues)
4. Click **Save**

#### 3.3 Configure Authorization

1. Go to **Authorization** in the left sidebar
2. Click **Add** next to **OAuth 2.0 (3LO)**
3. Set the **Callback URL** to your bot's `/jira/oauth/callback` endpoint (e.g. `http://localhost:8080/jira/oauth/callback` for local setup, or `https://<your-bot-host>/jira/oauth/callback` for production)
4. Click **Save**

#### 3.4 Get Client Credentials

1. Go to **Settings** in the left sidebar
2. Note the **Client ID** and **Secret** — these are your `JIRA_BOT_JIRA_CLIENT_ID` and `JIRA_BOT_JIRA_CLIENT_SECRET`

#### 3.5 Obtain a Refresh Token

The bot has a built-in setup flow. Start it with only the client credentials and callback URL:

```bash
export JIRA_BOT_JIRA_CLIENT_ID=your-client-id
export JIRA_BOT_JIRA_CLIENT_SECRET=your-client-secret
export JIRA_BOT_OAUTH_CALLBACK_URL=https://<your-bot-host>/jira/oauth/callback
export JIRA_BOT_GITHUB_APP_ID=123456
export JIRA_BOT_GITHUB_PRIVATE_KEY="your-key"
export JIRA_BOT_GITHUB_WEBHOOK_SECRET="your-secret"
export JIRA_BOT_JIRA_DEFAULT_PROJECT=ENG
export JIRA_BOT_JIRA_DEFAULT_ISSUE_TYPE=Task
```

Then run the bot and open `http://localhost:8080/jira/oauth/authorize` (or `https://<your-bot-host>/jira/oauth/authorize` if deployed) in your browser. This redirects you to Atlassian for authorization. After you approve, you're sent back to `/jira/oauth/callback` where the bot exchanges the code and displays your refresh token.

The `JIRA_BOT_OAUTH_CALLBACK_URL` must match exactly what you registered in the Atlassian Developer Console (step 3.3).

Copy the refresh token and set it as `JIRA_BOT_JIRA_REFRESH_TOKEN`. Once configured, restart the bot with all four OAuth variables and the setup endpoints disappear.

> The refresh token remains valid as long as it's used within 90 days. The bot refreshes access tokens automatically at runtime. When deployed to Kubernetes with token persistence enabled (default), the bot stores each rotated refresh token so it survives restarts indefinitely.

#### 3.6 Get Your Cloud ID

The Cloud ID identifies your Jira Cloud site. Retrieve it with:

```bash
curl -s https://YOUR_SITE.atlassian.net/_edge/tenant_info | jq -r '.cloudId'
```

Or visit `https://YOUR_SITE.atlassian.net/_edge/tenant_info` in your browser and note the `cloudId` value. This is your `JIRA_BOT_JIRA_CLOUD_ID`.

### 4. Configure Environment

#### OAuth 2.0 (recommended)

```bash
JIRA_BOT_GITHUB_APP_ID=123456
JIRA_BOT_GITHUB_PRIVATE_KEY="-----BEGIN RSA PRIVATE KEY-----\n..."
JIRA_BOT_GITHUB_WEBHOOK_SECRET=$(openssl rand -hex 20)
JIRA_BOT_JIRA_CLIENT_ID=your-client-id
JIRA_BOT_JIRA_CLIENT_SECRET=your-client-secret
JIRA_BOT_JIRA_REFRESH_TOKEN=your-refresh-token
JIRA_BOT_JIRA_CLOUD_ID=your-cloud-id
JIRA_BOT_JIRA_DEFAULT_PROJECT=ENG
JIRA_BOT_JIRA_DEFAULT_ISSUE_TYPE=Task
```

#### Basic Auth (legacy, PAT)

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

> **Note:** If both OAuth 2.0 and legacy credentials are provided, OAuth 2.0 takes precedence.

### 5. Deploy with Helm

```bash
helm install jira-bot charts/jira-bot \
  --set secrets.github.webhookSecret="your-webhook-secret" \
  --set secrets.github.privateKeyBase64="base64-encoded-private-key" \
  --set secrets.jira.clientID="your-client-id" \
  --set secrets.jira.clientSecret="your-client-secret" \
  --set secrets.jira.refreshToken="your-refresh-token" \
  --set config.jira.cloudID="your-cloud-id" \
  --set config.githubAppID="123456" \
  --set config.jira.defaultProject="ENG" \
  --set config.jira.defaultIssueType="Task"
```

Token persistence and leader election are enabled by default. To disable:

```bash
helm install jira-bot charts/jira-bot \
  ... \
  --set tokenPersistence.enabled=false \
  --set rbac.create=false
```

## Authentication Modes

The bot supports two authentication modes:

| Mode | Variables Required | Notes |
|------|-------------------|-------|
| **OAuth 2.0** (recommended) | `JIRA_BOT_JIRA_CLIENT_ID`, `JIRA_BOT_JIRA_CLIENT_SECRET`, `JIRA_BOT_JIRA_REFRESH_TOKEN`, `JIRA_BOT_JIRA_CLOUD_ID` | Tokens refresh automatically. No manual rotation needed. |
| **Basic Auth** (legacy) | `JIRA_BOT_JIRA_BASE_URL`, `JIRA_BOT_JIRA_USERNAME`, `JIRA_BOT_JIRA_TOKEN` | PAT expires every 90 days. Requires manual rotation. |

The bot detects the mode at startup. If all four OAuth variables are set, it uses OAuth 2.0. Otherwise it falls back to basic auth.

## Token Persistence (Multi-Replica)

When running with multiple replicas in Kubernetes, the bot automatically persists rotated OAuth tokens to a bot-managed Kubernetes secret. This prevents token loss across restarts and avoids refresh token conflicts between pods.

### How It Works

- **Leader election**: One pod acquires a Kubernetes Lease and becomes the sole token refresher. This prevents multiple pods from racing against Atlassian's rotating refresh token.
- **Token persistence**: After each token refresh, the leader writes the new refresh token, access token, and expiry to a separate Kubernetes secret (not the Helm-managed one).
- **Follower polling**: Non-leader pods poll the bot-managed secret (default every 30s) and use the access token directly without calling Atlassian.
- **Startup recovery**: On restart, the bot reads the latest persisted refresh token from the secret, so it always has the most recent rotation — even if 90+ days have passed since the original env var token was issued.

### Graceful Degradation

Token persistence activates automatically when the bot detects it's running in Kubernetes (via `POD_NAME` and `POD_NAMESPACE` from the downward API). When running outside Kubernetes (e.g., local development), the bot falls back to its standard behavior — keeping tokens in memory and using the env var refresh token.

If RBAC permissions are missing or the Kubernetes API is unreachable, the bot logs a warning and continues operating without persistence.

### Configuration

Token persistence is controlled through these Helm values:

| Value | Default | Description |
|-------|---------|-------------|
| `tokenPersistence.enabled` | `true` | Enable/disable token persistence |
| `tokenPersistence.secretName` | `{{ fullname }}-oauth-token` | Name of the bot-managed secret |
| `tokenPersistence.leaseName` | `{{ fullname }}-token-leader` | Name of the leader election lease |
| `tokenPersistence.pollInterval` | `30s` | How often non-leader pods poll for token updates |
| `tokenPersistence.leaseDuration` | `15s` | Lease duration for leader election |
| `tokenPersistence.leaseRenewDeadline` | `10s` | Lease renewal deadline |
| `rbac.create` | `true` | Create ServiceAccount, Role, and RoleBinding |
| `serviceAccount.create` | `true` | Create a ServiceAccount for the bot |
| `serviceAccount.name` | `{{ fullname }}` | ServiceAccount name override |

The RBAC resources grant the bot `get`, `create`, and `update` permissions on secrets and leases within its namespace.

### Environment Variables (injected by Helm)

These are set automatically by the Helm chart via the downward API and values:

| Variable | Source | Description |
|----------|--------|-------------|
| `POD_NAME` | Downward API | Current pod name (identity for leader election) |
| `POD_NAMESPACE` | Downward API | Current namespace |
| `JIRA_BOT_TOKEN_SECRET_NAME` | Helm values | Bot-managed secret name |
| `JIRA_BOT_TOKEN_LEASE_NAME` | Helm values | Leader election lease name |
| `JIRA_BOT_TOKEN_POLL_INTERVAL` | Helm values | Poll interval for non-leader pods |
| `JIRA_BOT_LEASE_DURATION` | Helm values | Lease duration |
| `JIRA_BOT_LEASE_RENEW_DEADLINE` | Helm values | Lease renewal deadline |

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
```

| Field | Description |
|-------|-------------|
| `project` | Default Jira project key |
| `type` | Default Jira issue type |

Both fields are optional. You can set one without the other.

### Priority Chain

When resolving the project and issue type, the bot uses this priority order:

| Priority | Source | Example |
|----------|--------|---------|
| 1 (highest) | Command options | `project:OPS` in the comment |
| 2 | Repo config file | `project: ENG` in YAML |
| 3 (lowest) | Global config | `JIRA_BOT_JIRA_DEFAULT_PROJECT` env var |

Command-line options always win. The repo config overrides global defaults but is itself overridden by explicit command options.

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

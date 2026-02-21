# Setup Guide

## Prerequisites
- Go 1.22+
- Slack workspace with admin access
- GitHub organization with App creation permissions
- Jira Cloud instance

## Quick Start

```bash
# Clone and setup
cp .env.example .env
# Edit .env with your credentials

# Build and test
make test
make build

# Run locally
make run
```

## Slack App Setup
1. Create app at https://api.slack.com/apps
2. Enable Socket Mode → get App-Level Token (`xapp-`)
3. Add Bot Token Scopes: `chat:write`, `app_mentions:read`, `im:read`, `im:write`, `commands`
4. Subscribe to events: `app_mention`, `message.im`
5. Create slash command: `/agent`
6. Install to workspace → get Bot Token (`xoxb-`)

## GitHub App Setup
1. Create GitHub App in your organization settings
2. Permissions: `contents:read`, `pull_requests:write`, `issues:read`, `checks:read`
3. Subscribe to events: `pull_request`, `check_run`
4. Generate private key → save as PEM file
5. Install app to repositories
6. Note App ID and Installation ID

## Jira Setup
1. Create OAuth 2.0 (3LO) app at https://developer.atlassian.com/console/myapps/
2. Add scopes: `read:jira-work`, `write:jira-work`
3. For development: use API token from https://id.atlassian.com/manage-profile/security/api-tokens

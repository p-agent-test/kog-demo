# Architecture

## Overview

Platform Agent is an AI-powered DevOps teammate for the Platform Engineering team. It integrates Slack, GitHub, and Jira into a unified workflow with access control.

## Components

```
┌─────────────┐     ┌──────────────┐     ┌─────────────┐
│  Slack Bot   │────▶│  Supervisor  │────▶│  GitHub App │
│ (Socket Mode)│     │  (Policy +   │     │  (JWT Auth) │
└─────────────┘     │   Approval)  │     └─────────────┘
                    └──────────────┘
                          │
                    ┌─────────────┐
                    │  Jira Client │
                    │  (OAuth/API) │
                    └─────────────┘
```

### Slack Bot
- Socket Mode connection (no public URL needed)
- Handles mentions, DMs, slash commands, interactive components
- Thread-based conversations
- Rate limiting per user

### GitHub App
- JWT-based authentication → Installation tokens (JIT)
- PR review: read diffs, post comments
- Webhook handler for PR events and CI status
- Minimum required scopes

### Jira Client
- REST API v3 client
- OAuth 2.0 (3LO) or Basic Auth (development)
- Full CRUD: create, read, update, transition, search (JQL)
- Sprint query support

### Supervisor
- Policy engine: auto-approve / require-approval / denied
- Interactive Slack approval buttons
- Token TTL management (5-15 min)
- Comprehensive audit logging

## Security Model

1. **JIT Tokens** — Tokens generated only when needed, short TTL
2. **Least Privilege** — Minimum scopes for each integration
3. **Approval Flow** — Write operations require human approval
4. **Audit Trail** — Every action logged with who, what, when, result
5. **Policy Engine** — Configurable rules per action category

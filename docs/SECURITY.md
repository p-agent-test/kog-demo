# Security Model

## Principles
1. **Least Privilege** — Minimum permissions for each integration
2. **JIT Access** — Tokens generated only when needed
3. **Human-in-the-Loop** — Write operations require supervisor approval
4. **Audit Everything** — Complete trail of all actions

## Access Levels

| Level | Description | Examples |
|-------|-------------|----------|
| Auto-Approve | Low-risk read operations | PR read, issue search, logs view |
| Require Approval | Write operations | PR review, issue create, staging deploy |
| Denied | Dangerous operations | Production deploy, repo delete |

## Token Management
- GitHub installation tokens: 1 hour max, refreshed at 55 min
- Supervisor approval tokens: 5-15 min TTL
- All tokens stored in memory (no persistence)
- Expired tokens cleaned up automatically

## Webhook Security
- GitHub: HMAC-SHA256 signature validation
- Slack: Request signing verification
- Jira: IP allowlisting recommended

## Audit Log
Every action records:
- Who (user ID, username)
- What (action, resource)
- When (timestamp)
- Result (approved, denied, auto-approved)
- Details (approver, reason)

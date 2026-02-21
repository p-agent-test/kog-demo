# SECURITY.md — Kog Security Model

## Architecture: Zero-Secret Client

```
External (Kog/OpenClaw)              Agent Runtime (Trusted)
┌──────────────────────┐            ┌──────────────────────────┐
│                      │            │                          │
│  API Key (1 secret)  │──HTTPS───→│  API Key validation      │
│                      │            │       ↓                  │
│  No GitHub PEM       │            │  Task router              │
│  No Slack tokens     │            │   ↓        ↓        ↓   │
│  No K8s kubeconfig   │            │ GitHub   Slack     K8s   │
│  No Jira creds       │            │ PEM      Tokens    Config│
│                      │            │ (N secrets, isolated)    │
└──────────────────────┘            └──────────────────────────┘
```

## Secret Management

| Secret | Location | Rotation | Agent Access |
|--------|----------|----------|-------------|
| Slack Bot Token | Vault/K8s Secret | Manual | Direct (env) |
| Slack App Token | Vault/K8s Secret | Manual | Direct (env) |
| GitHub App PEM | Vault/K8s Secret | Annual | Direct (file mount) |
| K8s Kubeconfig | In-cluster SA | Auto | ServiceAccount |
| Jira OAuth Token | Vault/K8s Secret | Auto-refresh | Direct (env) |
| Mgmt API Key | Vault/K8s Secret | Quarterly | Direct (env) |

**Key property:** External clients (Kog, CI/CD, other services) hold ONLY the Management API key. All other secrets are internal to the agent runtime.

## RBAC Roles

| Role | Tasks | Health | Config | Audit |
|------|-------|--------|--------|-------|
| `admin` | Submit + Cancel + List | ✅ | Read + Write | ✅ |
| `operator` | Submit + List | ✅ | Read only | ✅ |
| `readonly` | List only | ✅ | Read only | ❌ |

## Threat Model

### Threat 1: Prompt Injection via Slack
- **Risk:** User sends crafted message to override agent behavior
- **Mitigation:** Input sanitization, pattern detection, strict system prompt, output filtering
- **Detection:** Audit log flags suspicious patterns

### Threat 2: API Key Compromise
- **Risk:** Attacker gets Management API key
- **Mitigation:** RBAC limits blast radius, all write ops need supervisor approval, audit log
- **Detection:** Rate limit alerts, unusual task patterns
- **Recovery:** Rotate API key in Vault → instant revocation

### Threat 3: Agent Runtime Compromise
- **Risk:** Attacker gains access to agent pod/VM
- **Mitigation:** Minimal permissions, NetworkPolicy, read-only filesystem, no shell access
- **Detection:** K8s audit logs, anomaly detection
- **Recovery:** Kill pod (secrets in Vault, not on disk)

### Threat 4: Supply Chain Attack
- **Risk:** Compromised dependency in agent binary
- **Mitigation:** Trivy scan in CI, cosign image verification, SBOM
- **Detection:** Vulnerability scanning, image signature verification

### Threat 5: Slack Channel Manipulation
- **Risk:** Unauthorized user joins agent channel and issues commands
- **Mitigation:** Channel allowlist, user-level supervisor policies
- **Detection:** Audit log shows unknown user IDs

## Network Policy

```yaml
# Agent pod should only talk to:
# 1. Slack API (outbound WSS)
# 2. GitHub API (outbound HTTPS)
# 3. Jira API (outbound HTTPS)
# 4. K8s API server (in-cluster)
# 5. Management API clients (inbound, port 8090)
# 6. Prometheus scrape (inbound, port 8090 /metrics)

apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: platform-agent
spec:
  podSelector:
    matchLabels:
      app: platform-agent
  policyTypes:
    - Ingress
    - Egress
  ingress:
    - from:
        - namespaceSelector:
            matchLabels:
              name: blackswan
      ports:
        - port: 8090
          protocol: TCP
  egress:
    - to: []  # Allow all egress (Slack WSS, GitHub HTTPS, etc.)
      ports:
        - port: 443
          protocol: TCP
    - to:     # K8s API
        - ipBlock:
            cidr: 10.140.0.0/16
      ports:
        - port: 443
          protocol: TCP
```

## Incident Response

1. **Suspicious activity detected** → Alert to #platform-security
2. **API key compromised** → Rotate in Vault, agent auto-restarts with new key
3. **Agent compromised** → Kill pod, rotate ALL secrets, review audit log
4. **Prompt injection detected** → Log, block user temporarily, review pattern

# USER.md — Team Context

## Primary Contact
- **Name:** Anıl Küçükrecep
- **Role:** Head of Platform Engineering @ Paribu
- **Timezone:** Europe/Istanbul (UTC+3)
- **Preferences:** Kısa mesajlar, bullet points, mobilden çalışıyor

## Team
- **Org:** Paribu Platform Engineering
- **Stack:** Go, Kubernetes (EKS), ArgoCD, Vault, Redpanda (Kafka), gRPC
- **Infra:** AWS (eu-central-1) + Huawei Cloud (Turkey, KVKK)
- **Repos:** Multiple orgs — p-blackswan (main), p-backoffice (gopanel), and agent org

## Conventions
- **Commits:** `type: [JIRA-ID] description`
- **Branches:** `feat/JIRA-ID-description`, `fix/JIRA-ID-description`
- **PRs:** Always to dev/main via PR, never direct push
- **Merging:** Human only — agent NEVER merges
- **CI:** Shared workflows, golangci-lint v2, Trivy, Hadolint
- **Deploy:** GitOps via ArgoCD, Helm charts (blackswan-helm-base)

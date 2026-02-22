# MEMORY.md — Kog Seed Memory

## Team & Org
- Paribu Platform Engineering ekibi
- PR-based workflow, review zorunlu, feature branch only
- Commit format: `type: [EXCH-XXXX] açıklama`
- GoPanel ekibi titiz — lint, architecture checks, depguard strict
- Türkçe iletişim, teknik tartışmalar bazen İngilizce

## Key People (Slack IDs)
- **Anıl Küçükrecep** (<@U012YC9G6UW>) — Head of Platform Engineering, benim yaratıcım
- **Soykan Gülcan** (<@U095ZGEAZST>) — Teknoloji Direktörü
- **Selçuk Usta** (<@U09JKC8FPFC>) — Head of Engineering, Exchange
- **Mehmet Kafadar** (<@U013UV0E49W>) — AI Enablement Direktörü
- **Yasin Oral** (<@UDK62R5QW>) — CEO

## Architecture Overview
- Go microservices, gRPC inter-service
- Kafka (Redpanda) event streaming
- K8s: EKS, Karpenter node management, ArgoCD GitOps
- Redis cache, PostgreSQL + DynamoDB storage
- Vault secret management
- Helm base chart: blackswan-service (Harbor OCI registry)
- KrakenD API Gateway (WebSocket proxy dahil)
- Schema Registry: Confluent (cp-schema-registry)

## Active Projects
- **Market Management**: Operator-based match pod lifecycle — CRD → Deployment/Service/ConfigMap otomatik
- **Leader Election**: K8s Lease ile match & wallet HA
- **WS v2 Stream API**: Enterprise-grade MM WebSocket API tasarımı
- **Notification Service**: FCM + SES + SMS, audit log tasarımı devam
- **Redpanda Connect**: 8 pipeline, CPU optimizasyon ihtiyacı

## Key Decisions
- Single-cluster operator: aynı cluster'da direct Deployment yönetimi (ArgoCD Application yok)
- Recreate deployment strategy: match engine stateful, rolling update uygun değil
- OwnerReference + K8s GC: CRD silinince children otomatik temizlenir
- Label-based legacy detection: `managed-by: market-api` label'ı olmayan deployment'lar legacy
- Schema check-before-register: zaten varsa skip
- ws-hub modify edilecek (yeni servis değil), stream.paribu.com/v2 endpoint
- Zero-secret client pattern: agent credential broker, dış client sadece API key tutar
- JIT permission grants: standing permission yok, TTL-based (5dk auto, 15dk human-approved)

## Infrastructure
- AWS EKS, multi-cluster (dev, test ortamları)
- Karpenter NodePools: standard (compute), observability, redpanda, vault
- ArgoCD GitOps — gitops repo'dan deploy
- Redpanda (Kafka-compatible) event streaming
- Confluent Schema Registry (3 replica)
- Blackswan Helm base chart — tüm servisler aynı template

## Conventions
- Branch: feature branch → PR → review → merge (main'e direct push yasak)
- CI: shared-workflows repo, golangci-lint v2
- Release: `release/*` branch'ler prod için, manual deploy
- Non-release branch'ler: auto-deploy (dev/test)
- Namespace: workload `blackswan`, system `kube-system`
- Market pod naming: `match-{pair}` (order-api bu convention'a bağlı)

## My Capabilities
- PR review, code analysis, architecture assessment
- K8s status monitoring (read-only)
- CI/CD pipeline management (GitOps PR oluşturma)
- Alert triage ve root cause analysis
- PRD/plan/analiz dokümanı yazma
- Redpanda topic management
- Schema registry operations

## How I Work
- Management API üzerinden task çalıştırırım: `POST http://localhost:8090/api/v1/tasks`
- Auth: `MGMT_AUTH_MODE` ayarına göre (`none` = auth yok, `api-key` = `X-API-Key` header)
- **GitHub ana task type: `github.exec`** — operation + params formatında
  - Read (auto-approve): `pr.list`, `pr.get`, `pr.files`, `issue.list`, `issue.get`, `repo.get`, `repo.list`, `run.list`, `run.get`
  - Write (approval gerekli): `pr.create`, `pr.comment`, `pr.review`, `issue.create`, `issue.comment`, `repo.create`
  - Denied: `pr.merge`, `pr.close`, `issue.close`, `repo.delete` ve geri kalan her şey
- K8s: `k8s.pod-status`, `k8s.pod-logs`, `k8s.alert-triage`
- Policy: `policy.list`, `policy.set`, `policy.reset`
- Slack'te approval button'lar ile onay alırım
- Her write operation supervisor onayı gerektirir → task status `requires_approval` döner
- **Direkt GitHub token/erişimim yok** — her şey Management API üzerinden (GitHub App ile)
- Channel allowlist ile sadece izinli Slack channel'lara yazarım
- **IP koruması**: GitHub App ve Slack App sadece bizim makinelerin IP'lerinden erişilebilir — key compromise olsa bile dışarıdan kullanılamaz

# Mem0 Integration Plan

## Overview
Self-hosted Mem0 as smart memory layer for Kog — semantic search, auto-extraction, entity relationships.

## Architecture

```
Kog (OpenClaw) ──→ Management API ──→ Mem0 Client (Go HTTP)
                                          │
                                    Mem0 Server (Python)
                                     ├── Qdrant (vector DB)
                                     └── LLM (fact extraction)
```

## Components
- **Qdrant**: Vector DB, Docker sidecar, ~512MB RAM
- **Mem0 Server**: Self-hosted, REST API, Python
- **LLM**: Fact extraction — küçük model yeter (gpt-4.1-nano veya local)
- **Go HTTP Client**: Agent'ta `/v1/memories/` REST wrapper

## Memory Levels
| Level | Scope | Example |
|-------|-------|---------|
| User | Takım üyesi bazlı | "X reviewer olarak atandı" |
| Session | Konuşma bazlı | "Bu thread'de market-api konuşuldu" |
| Agent | Kog'un genel bilgisi | "Recreate strategy kullanılıyor" |

## API Endpoints (Mem0 REST)
```
POST   /v1/memories/         — Add memory (auto-extract facts)
GET    /v1/memories/search/  — Semantic search
GET    /v1/memories/         — List memories (filtered)
PUT    /v1/memories/{id}/    — Update
DELETE /v1/memories/{id}/    — Delete
```

## Agent Integration (Go)

```go
// internal/memory/client.go
type Mem0Client struct {
    baseURL    string
    httpClient *http.Client
}

func (c *Mem0Client) Add(ctx context.Context, messages []Message, userID string) error
func (c *Mem0Client) Search(ctx context.Context, query string, userID string, limit int) ([]Memory, error)
```

### Management API Endpoints (new)
```
POST /api/v1/memory/store    — Store memory via Kog
GET  /api/v1/memory/search   — Search memories via Kog
GET  /api/v1/memory/stats    — Memory usage stats
```

## Deployment
```yaml
# docker-compose addition
services:
  qdrant:
    image: qdrant/qdrant:latest
    ports: ["6333:6333"]
    volumes: ["qdrant_data:/qdrant/storage"]
    mem_limit: 512m

  mem0:
    image: mem0ai/mem0:latest
    ports: ["8050:8050"]
    environment:
      - QDRANT_URL=http://qdrant:6333
      - OPENAI_API_KEY=${OPENAI_API_KEY}  # for extraction
    depends_on: [qdrant]
    mem_limit: 256m
```

## Resource Impact
| Component | RAM | CPU | Disk |
|-----------|-----|-----|------|
| Qdrant | ~512MB | minimal | ~1GB (grows) |
| Mem0 Server | ~256MB | minimal | — |
| **Total** | **~768MB** | **minimal** | **~1GB** |

Instance upgrade: `t3.small` (2GB) → `t3.medium` (4GB) önerilir.

## Phases

### Phase 1: Basic Integration (~1 gün)
- [ ] docker-compose'a Qdrant + Mem0 ekle
- [ ] Go HTTP client yaz (`internal/memory/`)
- [ ] Management API'ye `/memory/store` ve `/memory/search` ekle
- [ ] Seed data yükle (MEMORY.md → Mem0)

### Phase 2: Auto-Extract (~0.5 gün)
- [ ] Her task completion'da otomatik memory store
- [ ] Konuşma özetlerinden fact extraction
- [ ] Duplicate detection (aynı fact'i tekrar ekleme)

### Phase 3: Graph Memory (~1 gün)
- [ ] Entity ilişkileri (servis → namespace, PR → reviewer)
- [ ] Temporal context (ne zaman karar alındı)
- [ ] Decay scoring (eski bilgilerin relevance azalsın)

## Security
- Mem0 sadece localhost'ta dinler (dışarı açık değil)
- Qdrant data encrypted at rest
- Memory verisi agent makinesinde kalır (dışarı gitmez)
- CISO-friendly: self-hosted, no external API for storage

## Timeline
- Demo öncesi: Seed MEMORY.md yeter ✅
- Demo sonrası: Phase 1 (1 gün) → Phase 2 (0.5 gün) → Phase 3 (1 gün)
- **Toplam: ~2.5 gün**

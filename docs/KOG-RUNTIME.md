# Kog Runtime — Teknik Tasarım v0.1

> Tarih: 2026-02-27  
> Yazar: Kog (Cron kickoff'tan sonra, Opus sub-agent erişilemez olduğu için self-generated)  
> Bağlam: Anıl ile yapılan vizyon konuşmasının ardından (`memory/otonom-agent-runtime-vizyonu.md`)

---

## 1. Architecture Overview

```
┌─────────────────────────────────────────────────────────┐
│                    Kog Runtime Core                     │
│                                                         │
│  ┌──────────────┐    ┌──────────────┐   ┌───────────┐  │
│  │  EventBus    │───▶│  AgentLoop   │──▶│  Planner  │  │
│  │  (internal)  │    │  (main loop) │   │  (LLM)    │  │
│  └──────────────┘    └──────────────┘   └───────────┘  │
│         ▲                   │                   │       │
│         │                   ▼                   ▼       │
│  ┌──────────────┐    ┌──────────────┐   ┌───────────┐  │
│  │EventSources  │    │ToolRegistry  │   │MemoryStore│  │
│  │- Telegram    │    │- ExecTool    │   │- SQLite   │  │
│  │- Cron        │    │- GitTool     │   │- Vector   │  │
│  │- Webhook     │    │- HTTPTool    │   │  (sqlite- │  │
│  │- Watcher     │    │- ...         │   │   vec)    │  │
│  └──────────────┘    └──────────────┘   └───────────┘  │
│                                                         │
│  ┌──────────────────────────────────────────────────┐   │
│  │         LLM Provider Layer                       │   │
│  │  AnthropicProvider | OpenAIProvider | OllamaProvider│ │
│  └──────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────┘
         │                                    │
         ▼                                    ▼
┌─────────────────┐                ┌─────────────────────┐
│   Kog Workers   │                │   Escalation Layer  │
│  (Kog-2, N...)  │                │   (Anıl / Human)    │
│  Tool executors │                │   Telegram, Slack   │
└─────────────────┘                └─────────────────────┘
```

**Temel prensipler:**
- **Event-driven, not request-response**: Mesaj bekleme yok, event loop sürekli döner
- **Model-agnostic**: LLMProvider interface — Claude bugün, başkası yarın
- **Tool-first**: Her capability bir Tool, runtime hiçbir hardcode özellik taşımaz
- **Memory-first**: Her agent action persist edilir, vector search ile recall

---

## 2. Tech Stack

| Katman | Teknoloji | Neden |
|--------|-----------|-------|
| Language | Go 1.23+ | Type safety, goroutine concurrency, platform-agent'la uyum |
| Event Bus | Internal channel-based (`chan Event`) | Başlangıç için yeterli; later → NATS/Redpanda |
| Vector DB | `sqlite-vec` (SQLite extension) | Zero-ops, embedded, platform-agent'ta SQLite zaten var |
| Relational DB | SQLite (modernc WAL) | Aynı file, migration pattern platform-agent'tan alınır |
| LLM Clients | Anthropic SDK, OpenAI SDK (go-openai) | İkisi de Go SDK var |
| Telegram | `go-telegram-bot-api` | Platform-agent'ta zaten kullanılan pattern |
| HTTP | stdlib `net/http` + chi router | Webhook receiver için |
| Cron | `robfig/cron/v3` | Platform-agent'ta precedent var |
| Config | YAML + env override | Mevcut pattern |
| Observability | `slog` (structured) + Prometheus metrics | Zero-dep logging |

**Karar: sqlite-vec** (pgvector değil)
- Ops yükü yok (ayrı process yok)
- 768-dim embedding için yeterli (< 1M vektör)
- Ölçek gerekirse Qdrant/Weaviate swap edilir (MemoryStore interface arkasında)

---

## 3. Kritik Go Interface'leri

```go
// ==============================
// LLM Provider
// ==============================
type Message struct {
    Role    string // "user" | "assistant" | "tool_result"
    Content string
    ToolUse *ToolUse
}

type LLMProvider interface {
    Complete(ctx context.Context, req CompletionRequest) (*CompletionResponse, error)
    Stream(ctx context.Context, req CompletionRequest, out chan<- Token) error
    ModelID() string
    MaxTokens() int
}

type CompletionRequest struct {
    Messages    []Message
    SystemPrompt string
    Tools       []ToolSchema
    MaxTokens   int
    Temperature float64
}

// ==============================
// Tool
// ==============================
type ToolSchema struct {
    Name        string          `json:"name"`
    Description string          `json:"description"`
    InputSchema json.RawMessage `json:"input_schema"` // JSON Schema
}

type Tool interface {
    Schema() ToolSchema
    Execute(ctx context.Context, input json.RawMessage) (output string, err error)
}

// ==============================
// EventSource
// ==============================
type Event struct {
    ID        string
    Source    string // "telegram" | "cron" | "webhook" | "internal"
    Type      string // "message" | "tick" | "alert" | "tool_result"
    Payload   json.RawMessage
    Metadata  map[string]string
    Timestamp time.Time
}

type EventSource interface {
    Name() string
    Subscribe(ctx context.Context, out chan<- Event) error
    Ack(ctx context.Context, eventID string) error
}

// ==============================
// MemoryStore
// ==============================
type MemoryEntry struct {
    ID        string
    AgentID   string
    Content   string
    Embedding []float32
    Tags      []string
    CreatedAt time.Time
}

type MemoryStore interface {
    Save(ctx context.Context, entry MemoryEntry) error
    Search(ctx context.Context, query string, topK int) ([]MemoryEntry, error)
    SearchByEmbedding(ctx context.Context, vec []float32, topK int) ([]MemoryEntry, error)
    Get(ctx context.Context, id string) (*MemoryEntry, error)
    Delete(ctx context.Context, id string) error
}

// ==============================
// AgentCoordinator
// ==============================
type AgentSpec struct {
    ID           string
    Role         string // "planner" | "executor" | "reviewer"
    SystemPrompt string
    Provider     LLMProvider
    Tools        []Tool
    Memory       MemoryStore
}

type AgentCoordinator interface {
    Spawn(ctx context.Context, spec AgentSpec) (Agent, error)
    List() []Agent
    Get(id string) (Agent, bool)
    Kill(id string) error
    Broadcast(ctx context.Context, event Event) error
}

type Agent interface {
    ID() string
    Handle(ctx context.Context, event Event) error
    Status() AgentStatus
}
```

---

## 4. Event Loop Pseudocode

```go
func (r *Runtime) Run(ctx context.Context) error {
    eventCh := make(chan Event, 256)
    
    // Start all event sources
    for _, src := range r.sources {
        go src.Subscribe(ctx, eventCh)
    }
    
    // Start worker pool
    sem := make(chan struct{}, r.config.MaxConcurrency) // default: 4
    
    for {
        select {
        case <-ctx.Done():
            return ctx.Err()
            
        case event := <-eventCh:
            sem <- struct{}{} // acquire slot
            go func(e Event) {
                defer func() { <-sem }() // release slot
                
                // Route event to agent(s)
                agents := r.router.Route(e)
                for _, agent := range agents {
                    if err := agent.Handle(ctx, e); err != nil {
                        r.logger.Error("agent handle error",
                            "agent", agent.ID(),
                            "event", e.ID,
                            "err", err)
                        // Escalate if critical
                        if isCritical(err) {
                            r.escalate(ctx, e, err)
                        }
                    }
                }
                
                // Persist to memory
                r.memory.Save(ctx, MemoryEntry{
                    AgentID: "runtime",
                    Content: fmt.Sprintf("event:%s type:%s", e.ID, e.Type),
                    Tags:    []string{e.Source, e.Type},
                })
            }(event)
        }
    }
}

// Agent's internal loop (one per agent.Handle call):
func (a *baseAgent) Handle(ctx context.Context, event Event) error {
    // 1. Build context from memory
    relevant, _ := a.memory.Search(ctx, event.Payload.String(), 5)
    
    // 2. Construct messages
    msgs := a.buildMessages(event, relevant)
    
    // 3. LLM call (with tool use loop)
    for {
        resp, err := a.provider.Complete(ctx, CompletionRequest{
            Messages:     msgs,
            SystemPrompt: a.spec.SystemPrompt,
            Tools:        a.toolSchemas(),
        })
        if err != nil {
            return err
        }
        
        if resp.StopReason == "end_turn" {
            // Save final response to memory
            a.memory.Save(ctx, MemoryEntry{Content: resp.Text})
            return nil
        }
        
        if resp.StopReason == "tool_use" {
            // Execute tool
            result, err := a.executeTool(ctx, resp.ToolUse)
            msgs = append(msgs, toolResultMessage(resp.ToolUse.ID, result, err))
            continue
        }
    }
}
```

---

## 5. Migration Planı (OpenClaw → Kog Runtime)

### Faz 1 — Parallel (Hafta 1-2): Foundation
- [ ] `feat/kog-runtime` branch'te repo scaffold
- [ ] Core interfaces (`types.go`)
- [ ] SQLite store + sqlite-vec setup
- [ ] Basit LLMProvider (Anthropic)
- [ ] Telegram EventSource (mevcut bot token'ı paylaşır)
- [ ] ExecTool, HTTPTool (platform-agent'tan port)

### Faz 2 — Alpha Loop (Hafta 3-4): Event Loop
- [ ] Runtime.Run() event loop
- [ ] Single agent (planner) working
- [ ] MemoryStore search working
- [ ] Cron EventSource
- [ ] Basic routing (all events → planner)

### Faz 3 — Multi-Agent (Hafta 5-6): Coordinator
- [ ] AgentCoordinator implementation
- [ ] Planner → executor delegation pattern
- [ ] Kog-2 workers as remote executors (HTTP tool calls)
- [ ] Escalation layer (Telegram → Anıl)

### Faz 4 — Cutover (Hafta 7-8): Replace OpenClaw
- [ ] OpenClaw sadece Telegram gateway olarak bırakılır (ya da tamamen kesilir)
- [ ] Kog Runtime tüm event'leri handle eder
- [ ] Memory migration: OpenClaw .md files → sqlite-vec
- [ ] Load testing, fault injection
- [ ] Anıl onayı → production

**Geçiş stratejisi: Parallel run**
```
Telegram ──▶ OpenClaw (mevcut, yavaş yavaş azalan)
         ─┬▶ Kog Runtime (yeni, büyüyen)
           └──── karşılaştırma metrikleri
```

---

## 6. Risk / Trade-off'lar

| Risk | Etki | Mitigation |
|------|------|------------|
| sqlite-vec ölçek limiti | Yüksek vektör sayısında yavaşlama | Interface arkasında, Qdrant'a swap edilebilir |
| Tool execution güvenliği | ExecTool ile arbitrary command | Allowlist + sandbox (chroot/container) |
| LLM context window | Uzun agent loop'larda token overflow | Summarization middleware, rolling window |
| Multi-agent coordination karmaşıklığı | Race conditions, duplicate actions | Event IDs + idempotency keys |
| OpenClaw bağımlılığı geçişte | Telegram routing split riski | Feature flag: `RUNTIME_SHADOW=true` — yeni runtime sadece log atar |
| Memory cold start | İlk açılışta empty context | Seed memory: mevcut .md dosyaları import edilir |
| Cron vs Event spam | Çok event → LLM cost patlar | Rate limiting per source, cost budget per day |

**En büyük trade-off:**
> sqlite-vec vs Qdrant/Weaviate  
> sqlite-vec: sıfır ops, embedded, yeterli 1M vektöre kadar  
> Qdrant: daha güçlü ANN, ayrı process, k8s için uygun  
> **Karar: sqlite-vec ile başla, interface sayesinde geç kolay**

---

## 7. Dosya Yapısı (önerilen)

```
kog-runtime/          # veya platform-agent/internal/runtime/
├── cmd/
│   └── kog/
│       └── main.go
├── internal/
│   ├── agent/
│   │   ├── agent.go       # baseAgent implementation
│   │   ├── coordinator.go # AgentCoordinator
│   │   └── planner.go     # Planner agent
│   ├── event/
│   │   ├── bus.go         # internal EventBus
│   │   ├── telegram.go    # Telegram EventSource
│   │   ├── cron.go        # Cron EventSource
│   │   └── webhook.go     # HTTP webhook EventSource
│   ├── llm/
│   │   ├── provider.go    # LLMProvider interface
│   │   ├── anthropic.go   # Anthropic implementation
│   │   └── openai.go      # OpenAI implementation
│   ├── memory/
│   │   ├── store.go       # MemoryStore interface
│   │   ├── sqlite.go      # SQLite + sqlite-vec implementation
│   │   └── embedder.go    # Embedding generation
│   ├── tool/
│   │   ├── registry.go    # ToolRegistry
│   │   ├── exec.go        # ExecTool
│   │   ├── http.go        # HTTPTool
│   │   └── git.go         # GitTool
│   └── runtime/
│       ├── runtime.go     # Runtime.Run() main loop
│       ├── router.go      # Event → Agent routing
│       └── config.go      # Config struct
├── docs/
│   └── KOG-RUNTIME.md    # Bu doküman (public-friendly version)
└── go.mod
```

---

## Next Steps (Immediate)

1. `feat/kog-runtime` branch aç → `docs/KOG-RUNTIME.md` commit
2. `go mod init` + temel interface dosyaları (`types.go`)
3. SQLite + sqlite-vec setup (migration v1)
4. Anthropic LLMProvider (en basit Complete() call)
5. Telegram EventSource (bot token'dan event'leri oku)
6. Basit single-agent event loop testi

**Hedef:** 2 haftada "Telegram'dan mesaj gelince LLM'e gidip cevap dönüyor" seviyesine ulaş. Sonrası tool use, memory, multi-agent.

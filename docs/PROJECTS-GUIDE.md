# Projects Feature — Kog Kullanım Rehberi

> Platform Agent'ın **Projects** özelliği, Slack üzerinden uzun süreli projeleri yönetmeni sağlar. Her proje kendi session'ına, hafızasına ve geçmişine sahiptir. Proje session'ları kapanmaz, restart'larda bile devam eder.

---

## Hızlı Başlangıç

### Proje Oluştur
```
@kog new project "Leader Election Refactor" --repo github.com/p-blackswan/infra-services
```
- `"İsim"` zorunlu, `--repo` opsiyonel
- Otomatik slug üretir: `leader-election-refactor`
- Session başlatır: `agent:main:project-leader-election-refactor`
- ✨ Block Kit kartıyla onay + "Start Working" butonu

### Projeye Devam Et
```
@kog leader-election-refactor
```
Tek kelime yeter. Yeni thread açılır, proje context'i (kararlar, blocker'lar, son durum) Block Kit kartıyla gösterilir.

### Projeye Mesaj Gönder
```
@kog leader-election-refactor PR #47'nin durumu ne?
```
Slug + mesaj → projenin session'ına route edilir, cevap aynı thread'de gelir.

### Thread İçinde (Slug Gereksiz)
Proje thread'i açıldıktan sonra o thread'deki tüm mesajlar otomatik olarak projenin session'ına gider. `@kog` mention'a gerek yok.

---

## Komutlar

| Komut | Açıklama |
|-------|----------|
| `@kog projects` veya `@kog projeler` | Dashboard (Block Kit kartları) |
| `@kog new project "İsim"` | Yeni proje oluştur |
| `@kog new project "İsim" --repo URL` | Repo bağlantılı proje |
| `@kog <slug>` | Projeye devam et (detay kartı + yeni thread) |
| `@kog <slug> <mesaj>` | Projeye mesaj gönder |
| `@kog decide <slug> <karar>` | Karar kaydet |
| `@kog blocker <slug> <blocker>` | Blocker kaydet |
| `@kog archive <slug>` | Arşivle |
| `@kog resume <slug>` | Tekrar aç |

---

## Slack UX (Block Kit)

### Dashboard (`@kog projects`)

Rich Block Kit kartları — her proje ayrı section, butonlarla:

```
📂 3 Active Projects

🟢 leader-election
Leader Election Refactor
📌 3 decisions · 🚧 1 blocker · 12 tasks
Last: 2h ago — "Implemented lease renewal, PR #47"
[Continue]  [Archive]
─────────────────────────────
🟡 ci-pipeline-v2
CI Pipeline Migration  
📌 2 decisions · 8 tasks
Last: 1d ago — "GitHub Actions testing"
[Continue]  [Archive]
─────────────────────────────
🔵 monitoring-revamp
Monitoring Revamp
📌 1 decision · 3 tasks
Last: 5d ago — "Evaluated Grafana vs Datadog"
[Continue]  [Archive]
```

**Durum emoji'leri** (son aktiviteye göre):
- 🟢 < 6 saat
- 🟡 < 3 gün
- 🔵 < 7 gün
- ⏸️ paused
- 📦 archived

### Proje Oluşturma Kartı
```
✅ Project Created: leader-election-refactor
📋 Leader Election Refactor
🔗 github.com/p-blackswan/infra-services
[Start Working]
```

### Projeye Devam Kartı (`@kog <slug>`)
```
🔄 Leader Election Refactor — Resuming (v3)

📌 Decisions:
• Using etcd 3.5 with TLS
• Lease TTL: 15s, renewal: 5s

🚧 Blockers:
• Waiting on SRE for TLS certs

📝 Last Session:
"Implemented lease renewal, PR #47 open for review"
```

### Karar/Blocker Kartı
```
📌 Decision recorded for leader-election
"etcd 3.5 with TLS kullanılacak"
Total: 4 decisions
```

### Butonlar (Interactive)

Tüm butonlar gerçek Slack interaction callback'leri:
- **Continue** → `project_continue_{slug}` → projeye devam flow'u başlatır
- **Archive** → `project_archive_{slug}` → arşivler
- **Start Working** → `project_start_{slug}` → ilk session'ı başlatır

Butonlara tıklamak = komutu yazmakla aynı. Mobilden tek tap yeter.

---

## Routing Kuralları

Mesaj geldiğinde şu sırayla çözümlenir:

1. **Thread binding** → Bu thread bir projeye bağlı mı? → projeye route et
2. **Built-in komut** → `projects`, `new project`, `decide`, `blocker`, `archive`, `resume`
3. **Slug match** → Kelime bir proje slug'ı mı? → projeye route et
4. **Default** → Mevcut davranış (thread-based session)

**Reserved kelimeler** (slug olarak kullanılamaz): `projects`, `projeler`, `new`, `decide`, `blocker`, `archive`, `resume`, `help`, `handoff`

---

## Proje Hafızası (Memory)

Her proje kendi hafızasına sahiptir. 4 tür:

| Tür | Açıklama | Nasıl oluşur |
|-----|----------|-------------|
| `decision` | Proje kararları | `@kog decide <slug> ...` komutuyla |
| `blocker` | Engeller | `@kog blocker <slug> ...` komutuyla |
| `context_carry` | Session rotation özeti | Token limiti aşıldığında otomatik |
| `summary` | Durum özeti | Manuel veya otomatik |

### Context Preamble

Yeni session açıldığında projenin hafızası otomatik inject edilir:
- Proje bilgileri (isim, repo, açıklama)
- Son kararlar (max 20)
- Aktif blocker'lar (max 10)
- Son session özeti (context_carry, max 3)
- Diğer aktif projelerin kısa indexi (cross-project awareness)

**Toplam preamble bütçesi: ~4000 token**

---

## Session Yönetimi

### Session Key Formatı
```
agent:main:project-{slug}        # ilk session
agent:main:project-{slug}-v{N}   # rotation sonrası
```

### Session Rotation (Otomatik)

Token limit hatasında (`context_length_exceeded`):
1. Mevcut session'dan özet istenir
2. Özet `context_carry` olarak kaydedilir
3. Yeni session açılır (v+1)
4. Context preamble inject edilir
5. Kullanıcıya bildirim: "Session rotated to v{N}"
6. Mesaj yeni session'da retry edilir

Detection: `bridge.IsTokenLimitError(err)` — WS bridge'de otomatik

### Activity Tracking

Her mesaj route edildiğinde `updated_at` güncellenir (`store.TouchProject`).
Dashboard status emoji'leri bu timestamp'e göre hesaplanır.

### Restart Dayanıklılığı
- Projeler, hafıza, thread binding'ler → SQLite (persist)
- OpenClaw session'ları → server-side persistent
- Agent restart → projeler kaldığı yerden devam

---

## Task-Project Association

Proje session'ı üzerinden oluşturulan task'lar otomatik olarak projeye bağlanır:
- `task.project_id` → projenin UUID'si
- Management API'dan: `POST /projects/:slug/message` → task oluşur, `project_id` set edilir
- Slack'ten: thread binding üzerinden otomatik

---

## Management API

Base: `http://localhost:8090/api/v1/projects`

| Method | Endpoint | Açıklama |
|--------|----------|----------|
| `POST` | `/` | Proje oluştur |
| `GET` | `/` | Listele (`?status=active&owner_id=X`) |
| `GET` | `/:slug` | Detay (memory + events + stats) |
| `PATCH` | `/:slug` | Güncelle (name, description, repo_url) |
| `POST` | `/:slug/message` | Session'a mesaj gönder |
| `POST` | `/:slug/memory` | Hafıza ekle (decision/blocker/summary) |
| `GET` | `/:slug/memory` | Hafıza listele (`?type=decision`) |
| `GET` | `/:slug/events` | Event log |
| `POST` | `/:slug/archive` | Arşivle |
| `POST` | `/:slug/resume` | Tekrar aç |
| `DELETE` | `/:slug` | Sil (cascade) |

### Örnekler

```bash
# Proje oluştur
curl -X POST http://localhost:8090/api/v1/projects \
  -H "Content-Type: application/json" \
  -d '{"name":"My Project","owner_id":"U012YC9G6UW"}'

# Mesaj gönder
curl -X POST http://localhost:8090/api/v1/projects/my-project/message \
  -H "Content-Type: application/json" \
  -d '{"message":"check CI status","caller_id":"U012YC9G6UW"}'

# Kararları listele
curl http://localhost:8090/api/v1/projects/my-project/memory?type=decision
```

---

## SQLite Tabloları

### `projects`
| Sütun | Tip | Açıklama |
|-------|-----|----------|
| id | TEXT PK | UUID |
| slug | TEXT UNIQUE | URL-safe isim |
| name | TEXT | Görünen isim |
| description | TEXT | Açıklama |
| repo_url | TEXT | GitHub repo (opsiyonel) |
| status | TEXT | active / paused / archived |
| owner_id | TEXT | Slack user ID |
| active_session | TEXT | Mevcut OpenClaw session key |
| session_version | INTEGER | Rotation sayacı |
| created_at | INTEGER | Unix ms |
| updated_at | INTEGER | Unix ms |
| archived_at | INTEGER | Unix ms (nullable) |

### `project_memory`
| Sütun | Tip | Açıklama |
|-------|-----|----------|
| id | TEXT PK | UUID |
| project_id | TEXT FK | → projects.id |
| type | TEXT | summary / decision / blocker / context_carry |
| content | TEXT | Markdown |
| session_key | TEXT | Hangi session üretti |
| created_at | INTEGER | Unix ms |

### `project_events`
| Sütun | Tip | Açıklama |
|-------|-----|----------|
| id | TEXT PK | UUID |
| project_id | TEXT FK | → projects.id |
| event_type | TEXT | created / session_rotated / task_completed / archived / resumed / message |
| actor_id | TEXT | Slack user ID veya "system" |
| summary | TEXT | Kısa açıklama |
| metadata | TEXT | JSON (opsiyonel) |
| created_at | INTEGER | Unix ms |

---

## Tam Akış Örneği

```
# 1. Proje oluştur
@kog new project "WS v2 Implementation" --repo github.com/p-blackswan/ws-hub
→ ✅ Block Kit kartı + [Start Working] butonu

# 2. Butona tıkla veya yaz
@kog ws-v2-implementation
→ 🔄 Detay kartı (boş context) + yeni thread açılır

# 3. Thread içinde çalış (mention gereksiz)
KrakenD endpoint'i oluştur, /v2/ws path'inde
→ Kog çalışır, cevap verir

# 4. Karar kaydet
@kog decide ws-v2-implementation seq number gap detection client-side
→ 📌 Block Kit onay kartı

# 5. Ertesi gün — tek kelime
@kog ws-v2-implementation
→ 🔄 Kararlar + blocker'lar inject edilmiş yeni thread

# 6. Dashboard
@kog projects
→ 📂 Rich kartlar + [Continue] [Archive] butonları

# 7. Token limit aşıldı (otomatik)
→ Session özeti alınır → v2 session açılır → devam

# 8. Bitince
@kog archive ws-v2-implementation
→ 📦 Arşivlendi
```

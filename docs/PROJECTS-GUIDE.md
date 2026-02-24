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

### Projeye Devam Et
```
@kog leader-election-refactor
```
Tek kelime yeter. Yeni thread açılır, proje context'i (kararlar, blocker'lar, son durum) inject edilir.

### Projeye Mesaj Gönder
```
@kog leader-election-refactor PR #47'nin durumu ne?
```
Slug + mesaj → projenin session'ına route edilir, cevap aynı thread'de gelir.

### Thread İçinde (Slug Gereksiz)
Proje thread'i açıldıktan sonra o thread'deki tüm mesajlar otomatik olarak projenin session'ına gider:
```
[leader-election-refactor thread'inde]
Kullanıcı: etcd TLS sertifikaları hazır mı?
Kog: [kontrol eder, cevaplar]
```

---

## Komutlar

| Komut | Açıklama |
|-------|----------|
| `@kog projects` veya `@kog projeler` | Aktif projeleri listele (dashboard) |
| `@kog new project "İsim"` | Yeni proje oluştur |
| `@kog new project "İsim" --repo URL` | Repo bağlantılı proje oluştur |
| `@kog <slug>` | Projeye devam et (yeni thread) |
| `@kog <slug> <mesaj>` | Projeye mesaj gönder |
| `@kog decide <slug> <karar>` | Karar kaydet |
| `@kog blocker <slug> <blocker>` | Blocker kaydet |
| `@kog archive <slug>` | Projeyi arşivle |
| `@kog resume <slug>` | Arşivlenmiş projeyi tekrar aç |

### Dashboard Çıktısı
```
📂 3 Active Projects

🟢 leader-election — 2h ago
├ 🚧 1 blocker · 📌 3 decisions · 12 tasks
└ Last: "Implemented lease renewal, PR #47 open"

🟡 ci-pipeline-v2 — 1d ago
├ 📌 2 decisions · 8 tasks
└ Last: "Migrated to GitHub Actions, testing"

🔵 monitoring-revamp — 5d ago
├ 📌 1 decision · 3 tasks
└ Last: "Evaluated Grafana vs Datadog"
```
Durum: 🟢 bugün aktif · 🟡 bu hafta · 🔵 >3 gün · ⏸️ durduruldu · 📦 arşiv

---

## Routing Kuralları

Mesaj geldiğinde şu sırayla çözümlenir:

1. **Thread binding** → Bu thread bir projeye bağlı mı? Bağlıysa o projeye route et
2. **Built-in komut** → `projects`, `new project`, `decide`, `blocker`, `archive`, `resume`
3. **Slug match** → Kelime bir proje slug'ı mı? Projeye route et
4. **Default** → Mevcut davranış (thread-based session)

**Reserved kelimeler** (slug olarak kullanılamaz): `projects`, `projeler`, `new`, `decide`, `blocker`, `archive`, `resume`, `help`, `handoff`

---

## Proje Hafızası (Memory)

Her proje kendi hafızasına sahiptir. 4 tür:

| Tür | Açıklama | Nasıl oluşur |
|-----|----------|-------------|
| `decision` | Proje kararları | `@kog decide <slug> ...` komutuyla |
| `blocker` | Engeller, bekleyen işler | `@kog blocker <slug> ...` komutuyla |
| `context_carry` | Session rotation özeti | Session token limiti aştığında otomatik |
| `summary` | Periyodik durum özeti | Manuel veya otomatik |

### Context Preamble
Yeni session açıldığında (veya rotation sonrası) projenin hafızası otomatik olarak session'a inject edilir:
- Proje bilgileri (isim, repo, açıklama)
- Son kararlar (max 20)
- Aktif blocker'lar (max 10)
- Son session özeti (context_carry, max 3)
- Diğer aktif projelerin kısa indexi (cross-project awareness, ~500 token)

**Toplam preamble bütçesi: ~4000 token**

---

## Session Yönetimi

### Session Key Formatı
```
agent:main:project-{slug}        # v1
agent:main:project-{slug}-v{N}   # rotation sonrası
```

### Session Rotation
Session token limitine ulaştığında:
1. Kog'dan session özeti istenir
2. Özet `context_carry` olarak kaydedilir
3. Yeni session açılır (v+1)
4. Context preamble inject edilir
5. Proje kaldığı yerden devam eder

### Restart Dayanıklılığı
- Proje ve hafıza SQLite'da → restart'a dayanır
- Thread binding'ler persist → thread'den devam edilebilir
- OpenClaw session'ları server-side persistent

---

## Management API

Base: `http://localhost:8090/api/v1/projects`

### Endpoints

#### `POST /` — Proje oluştur
```json
{
  "name": "Leader Election Refactor",
  "description": "K8s Lease migration",
  "repo_url": "https://github.com/p-blackswan/infra-services",
  "owner_id": "U012YC9G6UW"
}
```
Response: 201 + Project object (id, slug, active_session, ...)

#### `GET /` — Projeleri listele
Query: `?status=active&owner_id=U012YC9G6UW&limit=20&offset=0`

#### `GET /:slug` — Proje detayı
Response: Project + recent_memory + recent_events + stats

#### `PATCH /:slug` — Güncelle
```json
{ "name": "New Name", "description": "Updated desc" }
```

#### `POST /:slug/message` — Mesaj gönder
```json
{
  "message": "PR #47'nin durumu ne?",
  "caller_id": "U012YC9G6UW"
}
```
Projenin OpenClaw session'ına mesaj gönderir. Response: task_id + status

#### `POST /:slug/memory` — Hafıza ekle
```json
{ "type": "decision", "content": "etcd 3.5 with TLS kullanılacak" }
```

#### `GET /:slug/memory` — Hafıza listele
Query: `?type=decision&limit=50`

#### `GET /:slug/events` — Event log
Query: `?limit=50&offset=0`

#### `POST /:slug/archive` — Arşivle
#### `POST /:slug/resume` — Tekrar aç
#### `DELETE /:slug` — Sil (cascade: memory + events + thread bindings)

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

## Örnekler

### Tam Akış
```
# 1. Proje oluştur
@kog new project "WS v2 Implementation" --repo github.com/p-blackswan/ws-hub

# 2. Çalışmaya başla
@kog ws-v2-implementation
→ Yeni thread açılır, boş context ile başlar

# 3. Thread içinde çalış
KrakenD endpoint'i oluştur, /v2/ws path'inde
→ Kog çalışır, cevap verir

# 4. Karar kaydet
@kog decide ws-v2-implementation seq number gap detection client-side olacak

# 5. Ertesi gün devam et
@kog ws-v2-implementation
→ Yeni thread, ama önceki kararlar ve context inject edilmiş

# 6. Dashboard'a bak
@kog projects
→ Tüm projelerin durumu

# 7. Bitince arşivle
@kog archive ws-v2-implementation
```

### API ile Programmatik Kullanım
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

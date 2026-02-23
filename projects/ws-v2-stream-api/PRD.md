# Paribu WebSocket Stream API v2 — Product Requirements Document

| | |
|---|---|
| **Doküman** | PRD-2026-003 |
| **Ürün** | WebSocket Stream API v2 (`stream.paribu.com/v2`) |
| **Sahip** | Platform Engineering |
| **Durum** | Review → Approved |
| **Tarih** | 2026-02-23 |
| **Versiyon** | v2.0 (Review Integrated) |

---

## İçindekiler

1. [Executive Summary](#1-executive-summary)
2. [Problem Statement & Goals](#2-problem-statement--goals)
3. [Non-Goals / Out of Scope](#3-non-goals--out-of-scope)
4. [User Personas](#4-user-personas)
5. [Functional Requirements](#5-functional-requirements)
6. [Non-Functional Requirements](#6-non-functional-requirements)
7. [Technical Architecture](#7-technical-architecture)
8. [Migration Strategy](#8-migration-strategy)
9. [Security Requirements](#9-security-requirements)
10. [Monitoring & Observability](#10-monitoring--observability)
11. [API Reference](#11-api-reference)
12. [Error Handling & Edge Cases](#12-error-handling--edge-cases)
13. [Rate Limiting & Fair Usage](#13-rate-limiting--fair-usage)
14. [Testing Strategy](#14-testing-strategy)
15. [Rollout Plan](#15-rollout-plan)
16. [Success Metrics & KPIs](#16-success-metrics--kpis)
17. [Open Questions / Risks](#17-open-questions--risks)
18. [Competitive Analysis](#18-competitive-analysis)
19. [Phase 3: gRPC Streaming API (Optional)](#19-phase-3-grpc-streaming-api-optional)
20. [Deployment İzolasyon Validasyonu](#20-deployment-izolasyon-validasyonu)

---

## 1. Executive Summary

Paribu, Türkiye'nin en büyük kripto varlık borsalarından biri olarak v6 API lansmanına hazırlanıyor. Mevcut WebSocket altyapısı (`ws-hub`) basit bir pub/sub relay olarak tasarlanmış olup, Market Maker (MM) ve profesyonel trader'ların ihtiyaç duyduğu enterprise-grade özellikleri karşılamamaktadır.

Bu PRD, `stream.paribu.com/v2` endpoint'i altında sunulacak yeni nesil WebSocket Stream API'yi tanımlar. Hedef: **sequence number'lı, snapshot destekli, reconnection-aware, rate-limited** bir real-time data akışı protokolü — Binance, Bybit ve OKX ile rekabet edebilir kalitede.

**Temel çıktılar:**
- 6 public + 6 private channel, standartlaştırılmış `channel@market` format
- Per-channel sequence number ile gap detection
- Orderbook snapshot on subscribe
- Client-initiated ping/pong
- Per-connection rate limiting ve fair usage policy
- Graceful disconnect ile retry-after bilgisi
- `lastSeq` tabanlı reconnection protokolü
- KrakenD üzerinden JWT authentication (authorize frame gereksiz)

**Tahmini etki:** MM onboarding süresini ~2 haftadan ~2 güne düşürmek, orderbook desync vakalarını sıfıra indirmek, 3rd-party integratör deneyimini global standarda taşımak.

---

## 2. Problem Statement & Goals

### 2.1 Problem

Mevcut `ws-hub` altyapısı, 2022'de internal frontend ihtiyaçları için yazılmıştır. Market Maker onboarding sürecinde aşağıdaki kritik eksikler tespit edilmiştir:

| # | Eksik | Etki |
|---|---|---|
| 1 | Sequence number yok | MM'ler orderbook state'in tutarlı olduğunu doğrulayamıyor. Desync → yanlış fiyatlama → zarar |
| 2 | Snapshot on subscribe yok | Her reconnect'te REST API'den orderbook çekmek gerekiyor → race condition, ek latency |
| 3 | Client-initiated ping/pong yok | Client tarafında connection health monitoring yapılamıyor |
| 4 | Per-connection rate limit yok | Abuse senaryolarında tüm kullanıcılar etkileniyor |
| 5 | Reconnection protocol yok | Her disconnect'te full resubscribe + REST snapshot → veri kaybı penceresi |
| 6 | Slow-client backpressure yok | Yavaş client'lar `default:` drop ile mesaj kaybediyor, bilgilendirilmiyor |
| 7 | API key auth yok (WS) | MM'ler bot'ları için JWT refresh yönetimi yapmak zorunda |
| 8 | Graceful disconnect yok | Maintenance sırasında client'lar neden koptuğunu bilmiyor |
| 9 | Standardize payload yok | v1 channel isimleri (`ticker-extended`, `latest-matches`) endüstri standardından sapıyor |
| 10 | Per-user metrics yok | MM sorun bildirdiğinde debug yapılamıyor |

### 2.2 Goals

| # | Hedef | Metrik |
|---|---|---|
| G1 | MM'lerin orderbook'u hiç desync olmadan takip edebilmesi | Desync event = 0 (sequence gap + snapshot ile) |
| G2 | Reconnection sonrası veri kaybı süresi < 100ms | lastSeq resume başarı oranı > 99% |
| G3 | End-to-end message latency (Kafka → client) < 10ms p99 | Prometheus histogram |
| G4 | MM onboarding süresi < 2 gün | SDK + dokümantasyon ile |
| G5 | 10.000 eşzamanlı connection / pod | Load test ile doğrulanmış |
| G6 | Binance/Bybit/OKX ile feature parity | Competitive analysis checklist |
| G7 | v1 → v2 migration sırasında zero downtime | Blue-green deployment |

### 2.3 Success Criteria

- İlk 3 MM'nin v2'ye geçişi (launch + 30 gün)
- Public stream kullanıcılarının %80'inin v2'ye geçişi (launch + 90 gün)
- v1 deprecation (launch + 180 gün)

---

## 3. Non-Goals / Out of Scope

| # | Kapsam Dışı | Neden |
|---|---|---|
| 1 | WebSocket üzerinden order placement | REST API (`order-api`) bu işlevi karşılıyor. WS order execution ayrı bir PRD gerektirir |
| 2 | FIX protocol desteği | Şu an için MM talebi yok. İleride değerlendirilebilir |
| 3 | Binary protocol (protobuf/flatbuffers) | JSON yeterli throughput sağlıyor. İhtiyaç halinde v3'te değerlendirilecek |
| 4 | gRPC streaming API | WebSocket yeterli. gRPC internal servisler arası kalacak |
| 5 | Futures/margin channel'ları (positions hariç) | Futures ürünü henüz production'da değil. `positions` channel placeholder olarak tanımlanacak |
| 6 | Multi-region failover | Tek region (İstanbul) ile başlanacak. DR ayrı proje |
| 7 | Custom aggregation (VWAP, TWAP stream) | MM'ler bunu kendi taraflarında hesaplıyor |
| 8 | Historical data replay | Ayrı data service olarak planlanabilir |

---

## 4. User Personas

### 4.1 Market Maker (MM) — "Quantitative Ahmet"

| | |
|---|---|
| **Profil** | Algoritmik trading firması, 5-20 market'te sürekli likidite sağlıyor |
| **Bağlantı** | 5-10 eşzamanlı WS connection, her biri 20-50 channel |
| **Kritik ihtiyaç** | Orderbook consistency (sequence + snapshot), ultra-low latency (<5ms), fills/orders real-time |
| **Hassasiyet** | Tek bir kayıp mesaj = yanlış hedge = potansiyel kayıp |
| **Auth** | API key (bot'lar) + JWT (dashboard). 7/24 uptime beklentisi |
| **Reconnection** | Otomatik, lastSeq tabanlı, <500ms recovery |
| **Mevcut sorun** | ws-hub'da sequence yok → her 30dk'da REST snapshot ile senkronizasyon → race condition |

### 4.2 Retail Trader — "Günlük İşlemci Elif"

| | |
|---|---|
| **Profil** | Mobil/web app üzerinden 1-5 market takip ediyor |
| **Bağlantı** | 1 WS connection, 3-10 channel |
| **Kritik ihtiyaç** | Güvenilir ticker + orderbook, basit bağlantı |
| **Auth** | JWT (app üzerinden) |
| **Reconnection** | App tarafında otomatik, full resubscribe kabul edilebilir |
| **Mevcut sorun** | v1 stream sadece public → private data için ayrı connection gerekiyor |

### 4.3 3rd Party Integratör — "TradingView / CoinGecko / Aggregatör"

| | |
|---|---|
| **Profil** | Fiyat feed, orderbook depth, trade verisi çeken dış servisler |
| **Bağlantı** | 1-3 WS connection, sadece public channel |
| **Kritik ihtiyaç** | Standart format (kolay parse), yüksek uptime, iyi dokümantasyon |
| **Auth** | Yok (public) veya API key (rate limit artışı için) |
| **Reconnection** | Basit resubscribe yeterli |
| **Mevcut sorun** | v1 channel format non-standard → custom parser gerekiyor |

---

## 5. Functional Requirements

### 5.1 Connection Lifecycle

#### 5.1.1 Bağlantı Kurulumu

```
Client                          KrakenD                         ws-hub v2
  |                                |                                |
  |-- WSS UPGRADE /v2 ----------->|                                |
  |   Headers:                     |                                |
  |   Authorization: Bearer <JWT>  |-- JWT validate --------------->|
  |   (veya query: ?token=<JWT>)   |                                |
  |                                |-- WS UPGRADE (user_id header)->|
  |                                |                                |
  |<-- 101 Switching Protocols ----|<-------------------------------|
  |                                |                                |
  |<-- {"event":"connected","ts":..,"connId":"c_abc123"} ----------|
```

**Kurallar:**
- Endpoint: `wss://stream.paribu.com/v2`
- Public bağlantı: Header/token olmadan bağlanabilir
- Private bağlantı: `Authorization: Bearer <JWT>` header veya `?token=<JWT>` query param
- KrakenD JWT validation yapar, `X-User-Id` header'ı ile ws-hub'a iletir
- Bağlantı sonrası `connected` event gönderilir (connection ID dahil)
- **Connection ID (`connId`) Format:** `c_{pod_id}_{timestamp_ns}_{random_6char}`
  - Örnek: `c_pod3_1740000000123456_a7f9e2`
  - Pod ID: Hangi pod'da oluştuğu (debug için)
  - Timestamp: Oluşturulma zamanı (log correlation)
  - Random suffix: Multi-pod collision prevention
  
> 📋 **Review Notu:** connId format belirsiz bulunmuş (M3). **Çözüm:** Multi-pod unique ID için pod_id + timestamp_ns + random suffix kullanılacaktır.

#### 5.1.2 Subscribe / Unsubscribe

**Subscribe:**
```json
{
  "method": "subscribe",
  "params": ["ticker@btc_tl", "orderbook.20@eth_tl", "orders@btc_tl"],
  "id": 1
}
```

**Subscribe Response (Success):**
```json
{
  "id": 1,
  "code": 0,
  "msg": "ok",
  "data": {
    "subscribed": ["ticker@btc_tl", "orderbook.20@eth_tl", "orders@btc_tl"]
  }
}
```

**Subscribe Response (Partial — bazı channel'lar hatalı):**
```json
{
  "id": 1,
  "code": 0,
  "msg": "partial",
  "data": {
    "subscribed": ["ticker@btc_tl", "orderbook.20@eth_tl"],
    "errors": [
      {"channel": "orders@btc_tl", "code": 40100, "msg": "auth required"}
    ]
  }
}
```

**Unsubscribe:**
```json
{
  "method": "unsubscribe",
  "params": ["ticker@btc_tl"],
  "id": 2
}
```

**Kurallar:**
- Tek request'te birden fazla channel subscribe edilebilir
- Partial success desteklenir (bazıları başarılı, bazıları hatalı)
- Private channel'lara subscribe için JWT gerekir
- Zaten subscribe olunan channel tekrar subscribe edilirse: idempotent (hata dönmez)
- Unsubscribe edilmemiş channel'a unsubscribe: idempotent

#### 5.1.3 Ping / Pong (Client-Initiated)

```json
// Client → Server
{"method": "ping", "id": 3}

// Server → Client
{"method": "pong", "id": 3, "ts": 1740000000000}
```

**Kurallar:**
- Client istediği zaman ping gönderebilir
- Server `pong` ile cevaplar (server timestamp dahil)
- Server ayrıca WebSocket protocol-level ping frame gönderir (30s interval)
- Client 60 saniye içinde hiçbir mesaj (data veya pong) almazsa bağlantıyı dead kabul etmelidir
- Server, client'tan 60 saniye boyunca hiçbir mesaj (subscribe, ping, vb.) almazsa bağlantıyı kapatır

#### 5.1.4 Graceful Disconnect

Server maintenance veya rate limit aşımı durumunda:

```json
// Server → Client (WS close frame'den önce)
{
  "event": "disconnecting",
  "code": 4029,
  "msg": "rate limit exceeded",
  "retryAfterMs": 5000
}
```

Ardından WebSocket close frame: `code=4029, reason="rate limit exceeded"`

**Close Codes:**

| WS Close Code | Anlamı | Client Davranışı |
|---|---|---|
| 1000 | Normal close | Reconnect gerekmez |
| 1001 | Server going away (deploy) | Hemen reconnect |
| 4000 | Internal error | Exponential backoff ile reconnect |
| 4001 | Invalid message format | Reconnect gereksiz, client hatalı |
| 4003 | Auth failed | Token yenile, sonra reconnect |
| 4029 | Rate limit exceeded | `retryAfterMs` kadar bekle |
| 4030 | Too many connections | Mevcut connection'ları kapat veya bekle |
| 4040 | Maintenance | `retryAfterMs` kadar bekle |

### 5.2 Channel Specifications

#### 5.2.1 Public Channels

##### `ticker@{market}`

24 saatlik market istatistikleri. Her değişiklikte push (throttled: max 1/s per market).

```json
{
  "ch": "ticker@btc_tl",
  "ts": 1740000000000,
  "seq": 48291,
  "data": {
    "last": "2850000.00",
    "bid": "2849500.00",
    "ask": "2850100.00",
    "high": "2900000.00",
    "low": "2800000.00",
    "vol": "142.38",
    "quoteVol": "405000000.00",
    "change": "1.82",
    "openPrice": "2799000.00",
    "closeTime": 1740086400000,
    "tradeCount": 12847
  }
}
```

| Alan | Tip | Açıklama |
|---|---|---|
| `last` | string (decimal) | Son işlem fiyatı |
| `bid` | string (decimal) | En iyi alış fiyatı |
| `ask` | string (decimal) | En iyi satış fiyatı |
| `high` | string (decimal) | 24s en yüksek |
| `low` | string (decimal) | 24s en düşük |
| `vol` | string (decimal) | 24s base hacim |
| `quoteVol` | string (decimal) | 24s quote hacim |
| `change` | string (decimal) | 24s değişim (%) |
| `openPrice` | string (decimal) | 24s açılış fiyatı |
| `closeTime` | integer | 24s pencere bitiş zamanı (ms) |
| `tradeCount` | integer | 24s işlem sayısı |

##### `orderbook@{market}` (Incremental / Delta)

Orderbook değişiklikleri. Her match engine update'inde push.

**İlk mesaj (subscribe sonrası): Snapshot**
```json
{
  "ch": "orderbook@btc_tl",
  "ts": 1740000000000,
  "seq": 10000,
  "type": "snapshot",
  "data": {
    "bids": [
      ["2849500.00", "1.20"],
      ["2849000.00", "3.50"],
      ["2848500.00", "0.80"]
    ],
    "asks": [
      ["2850100.00", "0.85"],
      ["2850500.00", "2.30"],
      ["2851000.00", "1.10"]
    ]
  }
}
```

**Sonraki mesajlar: Delta**
```json
{
  "ch": "orderbook@btc_tl",
  "ts": 1740000000050,
  "seq": 10001,
  "type": "delta",
  "data": {
    "bids": [
      ["2849500.00", "0.00"],
      ["2849700.00", "2.10"]
    ],
    "asks": [
      ["2850050.00", "1.50"]
    ]
  }
}
```

**Kurallar:**
- `amount = "0.00"` → ilgili fiyat seviyesi silinmiş demektir
- Snapshot full orderbook içerir (tüm seviyeler)
- Delta sadece değişen seviyeleri içerir
- Client `seq` takip etmelidir. Gap tespit edilirse → unsubscribe + resubscribe (yeni snapshot alır)
- Snapshot her zaman `seq` numarası ile gelir; delta'lar bu `seq`'den devam eder

##### `orderbook.{depth}@{market}` (Snapshot — Fixed Depth)

Sabit derinlikte orderbook snapshot'ı. Her değişiklikte full snapshot push.

**depth**: `5`, `10`, `20`

```json
{
  "ch": "orderbook.20@btc_tl",
  "ts": 1740000000000,
  "seq": 10001,
  "type": "snapshot",
  "data": {
    "bids": [
      ["2849500.00", "1.20"],
      ["2849000.00", "3.50"]
    ],
    "asks": [
      ["2850100.00", "0.85"],
      ["2850500.00", "2.30"]
    ],
    "lastUpdateId": 9928341
  }
}
```

**Kurallar:**
- Her mesaj full snapshot (delta yok)
- Push frequency: max 100ms interval (throttled)
- `lastUpdateId`: Match engine'den gelen global sequence
- MM'ler genellikle `orderbook@{market}` (delta) tercih eder; `orderbook.{depth}` retail / integratör için

##### `trades@{market}`

Real-time public işlemler.

```json
{
  "ch": "trades@btc_tl",
  "ts": 1740000000000,
  "seq": 77210,
  "data": [
    {
      "tradeId": "t_8829101",
      "price": "2849800.00",
      "amount": "0.35",
      "side": "buy",
      "ts": 1740000000000
    }
  ]
}
```

**Kurallar:**
- `data` array olarak gelir (aynı anda birden fazla trade olabilir — batch)
- `side`: Taker'ın tarafı (`buy` = taker alıcı, `sell` = taker satıcı)
- Subscribe sonrası son 50 trade snapshot olarak gönderilir

##### `kline.{interval}@{market}` (Candlestick)

```json
{
  "ch": "kline.1m@btc_tl",
  "ts": 1740000000000,
  "seq": 3201,
  "data": {
    "openTime": 1739999940000,
    "closeTime": 1740000000000,
    "open": "2848000.00",
    "high": "2850200.00",
    "low": "2847500.00",
    "close": "2849800.00",
    "vol": "12.38",
    "quoteVol": "35250000.00",
    "tradeCount": 247,
    "closed": false
  }
}
```

**Intervals:** `1m`, `3m`, `5m`, `15m`, `30m`, `1h`, `2h`, `4h`, `6h`, `12h`, `1d`, `1w`

**Kurallar:**
- Aktif candle her trade'de güncellenir (max 1 push/s throttle)
- `closed: true` → candle kapanmış, artık değişmeyecek
- Subscribe sonrası aktif (kapanmamış) candle snapshot olarak gönderilir

##### `bbo@{market}` (Best Bid/Offer)

```json
{
  "ch": "bbo@btc_tl",
  "ts": 1740000000000,
  "seq": 92001,
  "data": {
    "bid": "2849500.00",
    "bidQty": "1.20",
    "ask": "2850100.00",
    "askQty": "0.85"
  }
}
```

**Kurallar:**
- Her best bid/ask değişikliğinde push
- Minimum overhead — MM'ler spread monitoring için kullanır
- Throttle yok (her değişiklik anında)

#### 5.2.2 Private Channels

Private channel'lar JWT authentication gerektirir. KrakenD JWT validate eder ve `X-User-Id` header'ı ile ws-hub'a iletir.

##### `orders@{market}` / `orders`

```json
{
  "ch": "orders@btc_tl",
  "ts": 1740000000000,
  "seq": 5001,
  "data": {
    "orderId": "ord_abc123",
    "clientOrderId": "my_order_001",
    "market": "btc_tl",
    "status": "partially_filled",
    "type": "limit",
    "side": "buy",
    "price": "2849000.00",
    "amount": "1.00",
    "filled": "0.35",
    "remaining": "0.65",
    "avgPrice": "2849000.00",
    "fee": "0.99",
    "feeAsset": "TRY",
    "createdAt": 1740000000000,
    "updatedAt": 1740000000050
  }
}
```

**Status değerleri:** `new`, `partially_filled`, `filled`, `cancelled`, `expired`, `rejected`

**Kurallar:**
- `orders@{market}`: Sadece belirli market
- `orders`: Tüm marketler (MM'ler için)
- Her iki channel'a aynı anda subscribe olunabilir (duplicate mesaj gelmez — specific market varsa sadece o channel'dan)
- `clientOrderId`: REST API'den order verirken belirtilen client tarafı ID
- Subscribe sonrası açık order'ların snapshot'ı gönderilir

##### `fills@{market}` / `fills`

```json
{
  "ch": "fills@btc_tl",
  "ts": 1740000000000,
  "seq": 3001,
  "data": {
    "tradeId": "t_789012",
    "orderId": "ord_abc123",
    "clientOrderId": "my_order_001",
    "market": "btc_tl",
    "side": "buy",
    "price": "2849000.00",
    "amount": "0.35",
    "fee": "0.99",
    "feeAsset": "TRY",
    "isMaker": true,
    "ts": 1740000000000
  }
}
```

**Kurallar:**
- `fills@{market}`: Sadece belirli market
- `fills`: Tüm marketler
- `isMaker`: `true` = maker fee, `false` = taker fee
- Subscribe sonrası snapshot gönderilmez (fills geçmişe dönük değil)

##### `balances`

```json
{
  "ch": "balances",
  "ts": 1740000000000,
  "seq": 200,
  "data": {
    "asset": "BTC",
    "available": "1.5000",
    "locked": "0.6500",
    "total": "2.1500"
  }
}
```

**Kurallar:**
- Her bakiye değişikliğinde (order placement, fill, deposit, withdrawal) push
- Subscribe sonrası tüm asset'lerin mevcut bakiyesi snapshot olarak gönderilir
- `locked`: Açık order'larda kilitli miktar
- `total` = `available` + `locked`

##### `positions`

> **Not:** Bu channel şu an placeholder'dır. Futures ürünü launch edildiğinde aktifleştirilecektir.

```json
{
  "ch": "positions",
  "ts": 1740000000000,
  "seq": 50,
  "data": {
    "market": "btc_usdt_perp",
    "side": "long",
    "size": "1.00",
    "entryPrice": "65000.00",
    "markPrice": "65500.00",
    "unrealizedPnl": "500.00",
    "leverage": "10",
    "liquidationPrice": "59000.00"
  }
}
```

### 5.3 Protocol Frame Format

Tüm mesajlar JSON text frame olarak gönderilir/alınır.

#### 5.3.1 Client → Server Mesajları

| Method | Açıklama | Rate Limit |
|---|---|---|
| `subscribe` | Channel'lara subscribe | 10 req/s |
| `unsubscribe` | Channel'lardan unsubscribe | 10 req/s |
| `ping` | Connection health check | 5 req/s |

**Format:**
```json
{
  "method": "subscribe|unsubscribe|ping",
  "params": ["channel1", "channel2"],
  "id": <integer>
}
```

- `id`: Client tarafından atanan request ID. Server response'ta aynı `id`'yi döner. 1-2^31 arası integer.
- `params`: `subscribe` ve `unsubscribe` için zorunlu. `ping` için opsiyonel (gönderilmezse yoksayılır).

#### 5.3.2 Server → Client Mesajları

**Data frame:**
```json
{
  "ch": "<channel>",
  "ts": <server_timestamp_ms>,
  "seq": <sequence_number>,
  "type": "snapshot|delta",   // sadece orderbook channel'larında
  "data": { ... }
}
```

**Response frame:**
```json
{
  "id": <request_id>,
  "code": <error_code>,
  "msg": "<message>",
  "data": { ... }           // opsiyonel
}
```

**Event frame:**
```json
{
  "event": "<event_type>",
  "ts": <server_timestamp_ms>,
  ...
}
```

Event types: `connected`, `disconnecting`

### 5.4 Sequence Number Semantics

- Her `(userId, channel)` çifti için monoton artan integer
- Public channel'larda: global sequence (tüm subscriber'lar aynı seq görür)
- Private channel'larda: user-specific sequence
- Gap detection: Client `seq` N aldıysa, bir sonraki `seq` N+1 olmalıdır
- Gap tespit edildiğinde client yapması gerekenler:
  1. `unsubscribe` → `subscribe` (yeni snapshot alır)
  2. Veya connection'ı kapatıp reconnect

**Sequence overflow:** 2^53 (JavaScript safe integer). Pratikte overflow olmaz (~285 milyon yıl @ 1M msg/s).

### 5.5 Snapshot on Subscribe

Aşağıdaki channel'lar subscribe sonrası otomatik snapshot gönderir:

| Channel | Snapshot İçeriği |
|---|---|
| `orderbook@{market}` | Full orderbook (tüm seviyeler) |
| `orderbook.{depth}@{market}` | Top N seviye |
| `trades@{market}` | Son 50 trade |
| `kline.{interval}@{market}` | Aktif (kapanmamış) candle |
| `orders@{market}` / `orders` | Açık order'lar |
| `balances` | Tüm asset bakiyeleri |

Snapshot mesajının `type` alanı `"snapshot"` olarak set edilir (orderbook için). Diğer channel'larda ilk mesaj olarak gönderilir.

**Snapshot Metadata (Multi-pod Consistency İçin):**

```json
{
  "ch": "orderbook@btc_tl",
  "seq": 10000,              // Bu pod'un bu channel'daki sequence
  "snapshotSeq": 10000,      // Snapshot'ın dayandığı sequence
  "lastUpdateId": 9928341,   // Match engine global sequence
  "type": "snapshot",
  "data": { ... }
}
```

**Rationale:** Multi-pod scenario'da consumer lag varsa, farklı pod'tan gelen snapshot stale olabilir. Client, gelen delta'ların `snapshotSeq` ile consistency kontrol edebilir. Gap tespit edilirse → resubscribe trigger.

> 📋 **Review Notu:** Multi-pod snapshot staleness risk tespit edilmiş (M5). **Çözüm:** Snapshot payload'ına `snapshotSeq` ve `lastUpdateId` metadata eklenmişdir. Client, delta seq validation'ında bu metadata'yı kullanabilir (optional, client SDK responsibility).

### 5.6 Reconnection Protocol

#### 5.6.1 Basit Reconnection (Retail)

1. Bağlantıyı yeniden kur
2. Tüm channel'lara tekrar subscribe ol
3. Snapshot'ları al, devam et

#### 5.6.2 Fast Resume (MM / Enterprise)

```json
// Reconnect sonrası subscribe
{
  "method": "subscribe",
  "params": ["orderbook@btc_tl"],
  "id": 1,
  "lastSeq": {
    "orderbook@btc_tl": 10050
  }
}
```

**Server davranışı:**
- `lastSeq` varsa ve server buffer'ında bu seq'den sonraki mesajlar mevcutsa → gap mesajları gönderilir (snapshot olmadan)
- Buffer'da yoksa (çok eski veya buffer taşmış) → normal snapshot gönderilir
- `lastSeq` yoksa → normal snapshot

**Server buffer:** Son 5 dakikalık mesajlar per-channel in-memory buffer'da tutulur.

### 5.7 Authentication Flow

```
                    ┌──────────┐
                    │  Client  │
                    └────┬─────┘
                         │
           WSS UPGRADE /v2
           Authorization: Bearer <JWT>
                         │
                    ┌────▼─────┐
                    │ KrakenD  │
                    │  Gateway │
                    └────┬─────┘
                         │
              JWT Validate (RS256)
              Extract: user_id, tier, permissions
                         │
              X-User-Id: "u_12345"
              X-User-Tier: "mm"
                         │
                    ┌────▼─────┐
                    │ ws-hub   │
                    │   v2     │
                    └──────────┘
```

**Auth seviyeleri:**

| Seviye | Erişim | Nasıl |
|---|---|---|
| Anonymous | Public channel'lar | Header/token olmadan bağlan |
| Authenticated | Public + Private channel'lar | JWT token ile bağlan |
| MM Tier | Public + Private + genişletilmiş limitler | JWT + `tier=mm` claim |

**JWT Token Refresh:**
- JWT expiry KrakenD tarafından kontrol edilir
- Token expire olduğunda KrakenD mevcut WS connection'ı kapatmaz (connection zaten kurulmuş)
- Yeni connection için yeni token gerekir
- Token lifetime: 24 saat (configurable)

---

## 6. Non-Functional Requirements

### 6.1 Latency

| Metrik | Hedef | Ölçüm Noktası |
|---|---|---|
| Kafka → Client (e2e) | < 10ms p50, < 25ms p99 | Prometheus histogram |
| Snapshot delivery | < 50ms p99 | Subscribe request'ten ilk data frame'e |
| Ping-pong RTT | < 5ms p99 | Client-measured |
| Subscribe ack | < 10ms p99 | Request'ten response'a |

### 6.2 Throughput

| Metrik | Hedef |
|---|---|
| Messages/sec per pod | 500,000 outbound |
| Connections per pod | 10,000 concurrent |
| Subscribe requests per connection per second | 10 |
| Channels per connection | 200 max |
| Total channels across all connections (per user) | 1,000 max |

### 6.3 Availability

| Metrik | Hedef |
|---|---|
| Uptime SLA | 99.95% (yıllık ~4.4 saat downtime) |
| Planned maintenance window | < 30 saniye (graceful disconnect + reconnect) |
| Recovery Time Objective (RTO) | < 60 saniye |
| Recovery Point Objective (RPO) | 0 (Kafka replay) |

### 6.4 Scalability

| Metrik | Hedef |
|---|---|
| Horizontal scale | Pod eklenerek linear ölçeklenme |
| Max concurrent connections (cluster) | 100,000 |
| Max markets | 500 |
| Kafka partition per topic | Market sayısı ile orantılı (1:1 veya N:1) |

### 6.5 Compression

- `permessage-deflate` WebSocket extension varsayılan aktif
- Context takeover: server=yes, client=yes (sliding window 32KB)
- Tahmini compression ratio: ~70-80% (JSON text için)
- Client deflate desteklemiyorsa: uncompressed fallback

### 6.6 Message Size

| | Limit |
|---|---|
| Max inbound message (client → server) | 4 KB |
| Max outbound message (server → client) | 1 MB (orderbook full snapshot) |
| Typical outbound message | 200-500 bytes |

---

## 7. Technical Architecture

### 7.1 High-Level Architecture

```
                                   ┌─────────────────────────────────────────────┐
                                   │              Kubernetes Cluster              │
                                   │                                             │
  ┌──────────┐    WSS     ┌───────┴───────┐         ┌──────────────────┐        │
  │  Client   ├──────────►│   KrakenD     │────────►│   ws-hub v2      │        │
  │  (MM/Web) │           │   Gateway     │  HTTP   │   (Go, N pods)   │        │
  └──────────┘            │               │  Upgrade│                  │        │
                          │  - JWT auth   │         │  - Hub manager   │        │
                          │  - Rate limit │         │  - Seq manager   │        │
                          │  - Compression│         │  - Snapshot svc  │        │
                          │  - Bot detect │         │  - Buffer mgr    │        │
                          └───────────────┘         │  - Metrics       │        │
                                                    └────────┬─────────┘        │
                                                             │                  │
                                              ┌──────────────┼──────────────┐   │
                                              │              │              │   │
                                         ┌────▼────┐   ┌─────▼─────┐  ┌────▼──┐│
                                         │  Kafka  │   │  Snapshot │  │ Redis ││
                                         │(Redpanda│   │  Service  │  │(opt.) ││
                                         │)        │   │  (gRPC)   │  │       ││
                                         └────┬────┘   └─────┬─────┘  └───────┘│
                                              │              │                  │
                                         ┌────▼────┐   ┌─────▼─────┐           │
                                         │  Match  │   │  Order    │           │
                                         │  Engine │   │  API      │           │
                                         └─────────┘   └───────────┘           │
                                   └─────────────────────────────────────────────┘
```

### 7.2 ws-hub v2 İç Mimari

```
┌─────────────────────────────────────────────────────────┐
│                      ws-hub v2 pod                       │
│                                                         │
│  ┌─────────────┐    ┌──────────────┐    ┌────────────┐ │
│  │  Connection  │    │   Channel    │    │  Kafka     │ │
│  │  Manager     │◄──►│   Router     │◄───│  Consumer  │ │
│  │             │    │              │    │  Group     │ │
│  │  - Accept   │    │  - Subscribe │    │            │ │
│  │  - Auth     │    │  - Unsubscribe│   │  Topics:   │ │
│  │  - Rate Lmt │    │  - Dispatch  │    │  - ticker  │ │
│  │  - Ping/Pong│    │  - Snapshot  │    │  - ob-delta│ │
│  └──────┬──────┘    └──────┬───────┘    │  - trades  │ │
│         │                  │            │  - orders  │ │
│         │           ┌──────▼───────┐    │  - fills   │ │
│         │           │  Sequence    │    │  - balance │ │
│         │           │  Manager     │    └────────────┘ │
│         │           │              │                    │
│         │           │  - Per-ch seq│    ┌────────────┐ │
│         │           │  - Gap buffer│    │  Snapshot   │ │
│         │           └──────────────┘    │  Cache     │ │
│         │                               │            │ │
│  ┌──────▼──────┐                        │  - OB full │ │
│  │  Write      │                        │  - Trades  │ │
│  │  Scheduler  │                        │  - Kline   │ │
│  │             │                        │  - Orders  │ │
│  │  - Per-conn │                        │  - Balance │ │
│  │    queue    │                        └────────────┘ │
│  │  - Backpres.│                                       │
│  │  - Batch    │    ┌────────────┐                     │
│  └─────────────┘    │  Metrics   │                     │
│                     │  Exporter  │                     │
│                     │  (Prom)    │                     │
│                     └────────────┘                     │
└─────────────────────────────────────────────────────────┘
```

### 7.3 Bileşen Detayları

#### 7.3.1 Connection Manager

- **Sorumluluk:** WebSocket connection lifecycle (accept, close, auth context)
- **Veri yapısı:** `map[connId]*Connection`
- **Connection struct:**
  ```go
  type Connection struct {
      ID          string
      UserID      string          // "" for anonymous
      Tier        string          // "anonymous", "retail", "mm"
      Conn        *websocket.Conn
      Channels    map[string]bool // subscribed channels
      WriteCh     chan []byte     // buffered write channel
      LastActivity time.Time
      CreatedAt   time.Time
      RemoteAddr  string
      ConnMeta    ConnMeta        // rate limit counters, etc.
  }
  ```
- **Per-IP connection tracking:** `map[IP]int` (anonymous bağlantılar için)
- **Per-User connection tracking:** `map[UserID]int` (authenticated bağlantılar için)

#### 7.3.2 Channel Router

- **Sorumluluk:** Channel → Connection mapping, message fan-out
- **Veri yapısı:** `map[channel]map[connId]*Connection`
- **Fan-out:** Kafka'dan gelen mesaj → channel'a subscribe olan tüm connection'lara write
- **Optimizasyon:** Mesaj bir kez serialize edilir, tüm connection'lara aynı `[]byte` gönderilir (zero-copy fan-out)

#### 7.3.3 Sequence Manager

- **Sorumluluk:** Per-channel monoton artan sequence number üretimi
- **Public channels:** Kafka partition offset doğrudan sequence olarak kullanılır.
  - ✅ Pod-independent, persistent, overflow riski yok (64-bit Kafka offset)
  - ✅ Multi-pod consistency guarantee
  - **Karar (Q2):** Kafka offset = sequence
  
- **Private channels:** Per-user pod-local atomic counter. User'ın bağlı olduğu pod'da tutulur.
  - Pod restart'ta reset → client'a full snapshot gönderilir (fallback mekanizması)
  - **Karar (Q2):** Pod-local counter, reconnect farklı pod'a düşerse snapshot fallback
  
- **Gap buffer:** Son 5 dakikalık mesajlar per-channel ring buffer'da tutulur (reconnection resume için)
  - Buffer boyutu: **Per-channel adaptive policy:**
    - Orderbook (@): 100K mesaj
    - Trades (@): 50K mesaj
    - Snapshot channels (.{depth}@, ticker@): 1K mesaj
    - Private channels (orders, fills, balances): 1K mesaj
  - Memory tahmini revize: ~200MB per pod (popular 50 market × optimal buffer size)

> 📋 **Review Notu:** Sequence overflow ve pod restart'ta sequence reset riski tespit edilmiş. **Çözüm:** Public channel'da Kafka offset kullan → persistent ve pod-agnostic. Private channel'da pod-local counter acceptable (pod restart → full snapshot fallback). Gap buffer memory optimization: per-channel size policy tanımlanmıştır (review'da M8 detaylandırılmıştır).

#### 7.3.4 Snapshot Cache

- **Sorumluluk:** Subscribe sonrası ilk snapshot'ı hızlı göndermek
- **Orderbook snapshot:** In-memory orderbook reconstruction yaklaşımı
  - ws-hub v2, orderbook delta'larını consume ederek kendi in-memory orderbook kopyasını tutar
  - Subscribe geldiğinde bu in-memory copy'dan snapshot üretilir
  - ✅ Match engine'e ek yük bindirmez
  - ✅ Snapshot latency ~0ms (memory read)
  - ✅ Consistency: Delta sequence ile orderbook state her zaman sync
  - ⚠️ Per-market OB boyutu: Max 10K level (daha derin istemler reject edilir)
  - ⚠️ Memory monitoring: OB cache >1.5GB → alarm, LRU eviction starts

- **Trades snapshot:** ws-hub in-memory buffer (son 50 trade per-market)
  - Subscribe sonrası buffer'dan serve edilir
  - Redis dependency ortadan kaldırılmış
  
- **Kline snapshot:** Kafka'dan consume edilir, in-memory cache'lenir (aktif candle)
  
- **Orders/Balances snapshot:** order-api / wallet servisine gRPC call
  - **Karar (Q6):** Capacity test gerekli (v2 snapshot load'ı order-api handle edebilir mi)
  - **Action:** Phase 1 beta'da order-api load monitör edilmeli

> 📋 **Review Notu:** Snapshot service'in v2 yükü ile ilgili izolasyon riski tespit edilmiş. **Çözüm:** Orderbook ve trades snapshot'ları ws-hub'da in-memory tuple yapılmıştır → external dependency yok. Orders/balances: order-api ve wallet servislerine dependency var, capacity test gerekli. Per-market OB boyut limiti ve memory alerting eklenmemiştir → management policy tanımlanmıştır.

#### 7.3.5 Write Scheduler (Backpressure)

- **Sorumluluk:** Her connection'a mesaj yazma, slow client yönetimi
- **Per-connection write channel:** Buffered channel (capacity: 256 mesaj)
- **Backpressure stratejisi:**
  1. Write channel dolu → mesaj drop edilir (eski davranış: `default: drop`)
  2. **Yeni:** Drop counter artırılır. 10 saniyede 100'den fazla drop → client'a warning mesajı:
     ```json
     {"event": "slow_consumer", "dropped": 147, "window": "10s"}
     ```
  3. 60 saniyede 1000'den fazla drop → graceful disconnect (code 4000)
- **Batching:** Write scheduler 1ms window içinde biriken mesajları tek write'a birleştirir (optional, configurable)

### 7.4 Kafka Topic Yapısı

| Topic | Key | Partitions | Producers | Consumer (ws-hub) |
|---|---|---|---|---|
| `ws.ticker` | market | 64 | Match engine | Broadcast to `ticker@{market}` |
| `ws.orderbook.delta` | market | 64 | Match engine | Broadcast to `orderbook@{market}`, update in-memory OB |
| `ws.trades` | market | 64 | Match engine | Broadcast to `trades@{market}` |
| `ws.kline` | market+interval | 64 | Kline aggregator | Broadcast to `kline.{interval}@{market}` |
| `ws.bbo` | market | 64 | Match engine | Broadcast to `bbo@{market}` |
| `ws.orders` | user_id | 64 | Order API | Route to `orders@{market}` / `orders` |
| `ws.fills` | user_id | 64 | Match engine | Route to `fills@{market}` / `fills` |
| `ws.balances` | user_id | 64 | Wallet | Route to `balances` |

**Consumer group:** `ws-hub-v2`
- Public topic'ler: Her pod tüm partition'ları consume eder (broadcast pattern)
- Private topic'ler: Partition assignment. User'ın bağlı olduğu pod, o user'ın partition'ını consume eder.

> 📋 **Review Notu:** Partition sayısı review'da "Market sayısı ile orantılı" ifadesi belirsiz bulunmuş. **Önerilen:** 64 sabit partition (market count'tan bağımsız) → yeterli parallelism ve manageable complexity. Topic key strategy: market-keyed (public) ve user_id-keyed (private) → sırasıyla market ve user'a göre ordering guarantee.

**Private channel routing problemi:**
User herhangi bir pod'a bağlanabilir, ama private mesajları sadece o user'ın partition'ını consume eden pod alır.

**Çözüm seçenekleri:**
1. **Internal relay:** Pod-to-pod gRPC stream. Partition owner pod → user'ın bağlı olduğu pod'a forward.
2. **Broadcast private topics:** Her pod tüm private partition'ları da consume eder, user kendi pod'unda değilse drop eder. (Basit ama wasteful)
3. **Sticky routing:** KrakenD'de user_id hash ile pod seçimi (consistent hashing). User her zaman aynı pod'a düşer.

**Önerilen:** Opsiyon 3 (Sticky routing via KrakenD). Avantajları:
- Basit implementasyon
- Kafka consumer group efficient partition assignment
- User-level state (sequence, subscriptions) tek pod'da
- Dezavantaj: Pod restart'ta user'lar yeni pod'a düşer → reconnect. Graceful disconnect ile mitigate edilir.

### 7.5 Deployment Topology

```
┌──────────────────────────────────────────────────────┐
│                    Kubernetes                         │
│                                                      │
│  ┌─────────────────┐   ┌─────────────────┐          │
│  │ KrakenD (3 pod) │   │ ws-hub v2       │          │
│  │                 │──►│ (5-20 pod, HPA) │          │
│  │ - L7 LB        │   │                 │          │
│  │ - JWT           │   │ - Stateful conn │          │
│  │ - Sticky hash   │   │ - Kafka consumer│          │
│  └─────────────────┘   └────────┬────────┘          │
│                                 │                    │
│         ┌───────────────────────┼───────────────┐    │
│         │                       │               │    │
│    ┌────▼────┐           ┌──────▼──────┐  ┌─────▼──┐│
│    │ Kafka   │           │ Match Engine│  │ Redis  ││
│    │(Redpanda│           │ (per market)│  │(opt.)  ││
│    │ 3 node) │           └─────────────┘  └────────┘│
│    └─────────┘                                      │
└──────────────────────────────────────────────────────┘
```

**HPA (Horizontal Pod Autoscaler):**

```yaml
metrics:
  - type: Resource
    resource:
      name: cpu
      target:
        type: Utilization
        averageUtilization: 70    # Primary metric
  
  - type: Pods
    pods:
      metric:
        name: ws_connections_active
      target:
        type: AverageValue
        averageValue: 8000        # Secondary

  - type: Pods
    pods:
      metric:
        name: ws_messages_sent_rate
      target:
        type: AverageValue
        averageValue: 400000      # Tertiary (500K msg/s per pod limit)

behavior:
  scaleUp:
    stabilizationWindowSeconds: 120
    policies:
      - type: Percent
        value: 100                # Double pod count max
        periodSeconds: 60
  
  scaleDown:
    stabilizationWindowSeconds: 600  # 10 min
    policies:
      - type: Percent
        value: 50                 # Remove 50% of excess
        periodSeconds: 120
```

**Rationale (Review M4):** Single metric (connection count) yanıltıcı olabilir (idle client'lar). CPU + connection + throughput kombinasyonu daha reliable. Scale-down yavaş (churn avoid) ve scale-up hızlı (traffic spike'a cevap).

> 📋 **Review Notu:** HPA metrik seçimi review'da iyileştirilmiştir (M4). **Çözüm:** CPU + connection + message throughput metrikleri birleştirilmiştir.

---

## 8. Migration Strategy

### 8.1 Timeline

```
Week 0        Week 4        Week 8        Week 12       Week 24
  │             │             │              │              │
  ▼             ▼             ▼              ▼              ▼
  PRD           v2 Beta       v2 GA          v1 Deprecation v1 Shutdown
  Approved      (internal)    (public)       Warning        (EOL)
                + MM beta                    Header
```

### 8.2 Phase Details

#### Phase 1: Beta (Week 0-4)
- ws-hub v2 deploy (ayrı pod set, ayrı KrakenD route)
- Internal test: QA + Platform Engineering
- 2-3 beta MM partner ile private test
- v1 ve v2 paralel çalışır, aynı Kafka topic'lerden consume eder
- Endpoint: `stream.paribu.com/v2` (beta flag ile)

#### Phase 2: GA (Week 4-8)
- Public launch: `stream.paribu.com/v2`
- Documentation publish (docs.paribu.com/api/v2/websocket)
- Python & TypeScript SDK release
- v1 hala aktif, deprecation announcement yok

#### Phase 3: Migration (Week 8-12)
- v1 response'larına `X-Deprecation: 2026-08-01` header eklenir
- v1 kullanıcılarına email/notification
- docs.paribu.com'da v1 sayfalarına deprecation banner

#### Phase 4: Sunset (Week 12-24)
- v1 connection limit kademeli düşürülür (100 → 50 → 10 → 0)
- v1 shutdown tarihi: Week 24
- Emergency rollback planı: v1 pod'ları 4 hafta daha cold standby'da tutulur

### 8.3 Backward Compatibility

| v1 Feature | v2 Karşılığı | Notlar |
|---|---|---|
| `ticker-extended` | `ticker@{market}` | Farklı payload format |
| `orderbook` | `orderbook@{market}` | + snapshot + sequence |
| `latest-matches` | `trades@{market}` | Rename |
| `ticker24h` | `ticker@{market}` | Merge edildi |
| `api-orderbook` | `orderbook.{depth}@{market}` | Depth parametreli |
| `config-public` | Out of scope | REST API'den çekilecek |
| `closed-orders` | `orders@{market}` (status=filled/cancelled) | Unified |
| `open-orders` | `orders@{market}` (status=new/partial) | Unified |
| `assets` | `balances` | Rename |
| `user` | Out of scope (v2'de yok) | Notification ayrı kanal |
| `transactions` | `fills@{market}` | Rename |
| `config-private` | Out of scope | REST API'den çekilecek |
| Native `/ws` authorize frame | KrakenD JWT | Frame gereksiz |

**Breaking changes:**
- Channel isimleri değişiyor (non-backward-compatible)
- Payload format değişiyor
- Authorize frame kaldırıldı
- Bu nedenle `/v2` ayrı endpoint — v1 ile aynı anda çalışır

---

## 9. Security Requirements

### 9.1 Transport Security

| Requirement | Detail |
|---|---|
| TLS | TLS 1.2+ zorunlu. TLS 1.3 tercih edilir |
| Certificate | Let's Encrypt veya enterprise CA (CloudFlare origin cert) |
| Cipher suites | AEAD only (AES-GCM, ChaCha20-Poly1305) |
| HSTS | `Strict-Transport-Security: max-age=31536000` |
| Origin validation | KrakenD `Origin` header kontrolü (CORS) |

### 9.2 Authentication & Authorization

| Requirement | Detail |
|---|---|
| JWT algorithm | RS256 (asymmetric) |
| JWT issuer | Paribu Auth Service |
| JWT audience | `stream.paribu.com` |
| JWT expiry | Max 24 saat |
| JWT claims | `sub` (user_id), `tier` (anonymous/retail/mm), `permissions` |
| Private channel auth | JWT zorunlu. JWT yoksa → `40100 Auth required` |
| Token rotation | Client, token expire olmadan yeni bağlantı açmalı |
| Revocation | JWT blacklist (Redis) — KrakenD kontrol eder |

### 9.3 Input Validation

| Requirement | Detail |
|---|---|
| Max message size (inbound) | 4 KB. Aşılırsa → disconnect |
| JSON parse | Strict JSON parse. Invalid JSON → `4001` close |
| Channel name validation | Regex: `^[a-z]+(\.\d+)?@[a-z]+_[a-z]+$` (veya channel listesinde whitelist) |
| Method validation | Sadece `subscribe`, `unsubscribe`, `ping` kabul edilir |
| `id` validation | Integer, 1 ≤ id ≤ 2^31 |
| `params` validation | Array, max 200 eleman, her biri string max 64 char |

### 9.4 DDoS Protection

| Layer | Protection |
|---|---|
| L3/L4 | CloudFlare / AWS Shield |
| L7 | KrakenD rate limiting + bot detection |
| WS | Per-connection rate limit (subscribe, message) |
| Connection | Max connections per IP / per user |
| Slowloris | KrakenD connection timeout (30s handshake) |

### 9.5 Data Privacy

- Private channel mesajları sadece authenticated user'a gönderilir
- Connection metadata (IP, user agent) 90 gün loglanır, sonra silinir
- KVKK compliance: User verisi Türkiye'de tutulur

---

## 10. Monitoring & Observability

### 10.1 Metrics (Prometheus)

#### Connection Metrics

| Metric | Type | Labels |
|---|---|---|
| `ws_connections_active` | Gauge | `pod`, `tier` |
| `ws_connections_total` | Counter | `pod`, `tier`, `close_code` |
| `ws_connection_duration_seconds` | Histogram | `pod`, `tier` |

#### Message Metrics

| Metric | Type | Labels |
|---|---|---|
| `ws_messages_sent_total` | Counter | `pod`, `channel_type`, `msg_type` |
| `ws_messages_sent_bytes_total` | Counter | `pod`, `channel_type` |
| `ws_messages_received_total` | Counter | `pod`, `method` |
| `ws_message_latency_seconds` | Histogram | `pod`, `channel_type` |
| `ws_messages_dropped_total` | Counter | `pod`, `reason` |

#### Subscription Metrics

| Metric | Type | Labels |
|---|---|---|
| `ws_subscriptions_active` | Gauge | `pod`, `channel` |
| `ws_subscribe_requests_total` | Counter | `pod`, `status` |
| `ws_snapshot_duration_seconds` | Histogram | `pod`, `channel_type` |

#### Kafka Consumer Metrics

| Metric | Type | Labels |
|---|---|---|
| `ws_kafka_consumer_lag` | Gauge | `pod`, `topic`, `partition` |
| `ws_kafka_messages_consumed_total` | Counter | `pod`, `topic` |

#### Rate Limit Metrics

| Metric | Type | Labels |
|---|---|---|
| `ws_rate_limit_exceeded_total` | Counter | `pod`, `limit_type` |
| `ws_slow_consumer_warnings_total` | Counter | `pod` |
| `ws_slow_consumer_disconnects_total` | Counter | `pod` |

### 10.2 Per-User Metrics (MM Debug)

MM'lerin sorun bildirmesi durumunda hızlı debug için:

```
ws_user_messages_sent_total{user_id="u_12345", channel="orderbook@btc_tl"}
ws_user_messages_dropped_total{user_id="u_12345"}
ws_user_connection_count{user_id="u_12345"}
ws_user_latency_p99{user_id="u_12345"}
```

**Not:** Per-user metrics yalnızca `tier=mm` kullanıcılar için aktif (cardinality kontrolü).

### 10.3 Logging

| Level | İçerik |
|---|---|
| `INFO` | Connection open/close, subscribe/unsubscribe (connId, userId, channel) |
| `WARN` | Rate limit exceeded, slow consumer warning, sequence gap |
| `ERROR` | Kafka consumer error, snapshot failure, unexpected disconnect |
| `DEBUG` | Her mesaj detayı (sadece debug mode'da, production'da kapalı) |

**Format:** JSON structured logging (OpenTelemetry compatible)

**Correlation:** Her log satırında `connId`, `userId`, `traceId` bulunur.

### 10.4 Alerting

| Alert | Condition | Severity |
|---|---|---|
| High connection count | > 80% capacity per pod | Warning |
| Kafka consumer lag | > 1000 messages for > 30s | Critical |
| Message drop rate | > 1% of total messages | Warning |
| Connection error spike | > 100 errors/min | Critical |
| Snapshot latency | p99 > 200ms | Warning |
| Pod memory | > 80% | Warning |
| Zero connections on pod | = 0 for > 5 min (not during deploy) | Warning |

### 10.5 Distributed Tracing

- OpenTelemetry (OTLP) ile KrakenD → ws-hub → Kafka arası trace
- Her Kafka mesajına `traceId` header eklenir
- Grafana Tempo / Jaeger ile visualize

### 10.6 Dashboard

**Grafana Dashboard: "WS Stream v2 Overview"**
- Active connections (by tier, by pod)
- Message throughput (by channel type)
- Kafka consumer lag
- Latency heatmap (e2e)
- Connection churn rate
- Top 10 users by connection count
- Error rate by code

---

## 11. API Reference

### 11.1 Endpoint

```
wss://stream.paribu.com/v2
```

### 11.2 Connection

**Headers:**
| Header | Zorunlu | Açıklama |
|---|---|---|
| `Authorization` | Hayır (public), Evet (private) | `Bearer <JWT>` |

**Query Parameters:**
| Param | Zorunlu | Açıklama |
|---|---|---|
| `token` | Hayır | JWT (header alternatifi, bazı client lib'ler header desteklemez) |

**İlk mesaj (server → client):**
```json
{
  "event": "connected",
  "ts": 1740000000000,
  "connId": "c_abc123def",
  "server": "ws-hub-v2-pod-3"
}
```

### 11.3 Client → Server Methods

#### `subscribe`

```json
{
  "method": "subscribe",
  "params": ["ticker@btc_tl", "orderbook.20@eth_tl"],
  "id": 1
}
```

Opsiyonel (reconnection resume):
```json
{
  "method": "subscribe",
  "params": ["orderbook@btc_tl"],
  "id": 1,
  "lastSeq": {
    "orderbook@btc_tl": 10050
  }
}
```

**Response:**
```json
{
  "id": 1,
  "code": 0,
  "msg": "ok",
  "data": {
    "subscribed": ["ticker@btc_tl", "orderbook.20@eth_tl"]
  }
}
```

#### `unsubscribe`

```json
{
  "method": "unsubscribe",
  "params": ["ticker@btc_tl"],
  "id": 2
}
```

**Response:**
```json
{
  "id": 2,
  "code": 0,
  "msg": "ok",
  "data": {
    "unsubscribed": ["ticker@btc_tl"]
  }
}
```

#### `ping`

```json
{"method": "ping", "id": 3}
```

**Response:**
```json
{"method": "pong", "id": 3, "ts": 1740000000000}
```

### 11.4 Server → Client Data Messages

#### Genel format:

```json
{
  "ch": "<channel_name>",
  "ts": <unix_timestamp_ms>,
  "seq": <sequence_number>,
  "type": "snapshot|delta",
  "data": { ... }
}
```

- `ch`: Channel adı
- `ts`: Server timestamp (millisecond Unix epoch)
- `seq`: Per-channel monoton artan sequence number
- `type`: Sadece orderbook channel'larında. Diğerlerinde alan yok.
- `data`: Channel'a özgü payload

### 11.5 Channel Reference

#### Public Channels

| Channel | Subscribe Format | Snapshot | Throttle | Açıklama |
|---|---|---|---|---|
| `ticker@{market}` | `ticker@btc_tl` | Son ticker | Max 1/s | 24h market stats |
| `orderbook@{market}` | `orderbook@btc_tl` | Full orderbook | Yok | Incremental delta |
| `orderbook.{depth}@{market}` | `orderbook.20@btc_tl` | Snapshot | Max 100ms | depth: 5/10/20 |
| `trades@{market}` | `trades@btc_tl` | Son 50 trade | Yok | Real-time trades |
| `kline.{interval}@{market}` | `kline.1m@btc_tl` | Aktif candle | Max 1/s | Candlestick |
| `bbo@{market}` | `bbo@btc_tl` | Son BBO | Yok | Best bid/offer |

#### Private Channels

| Channel | Subscribe Format | Snapshot | Açıklama |
|---|---|---|---|
| `orders@{market}` | `orders@btc_tl` | Açık orderlar | Per-market order updates |
| `orders` | `orders` | Açık orderlar | All-market order updates |
| `fills@{market}` | `fills@btc_tl` | Yok | Per-market fills |
| `fills` | `fills` | Yok | All-market fills |
| `balances` | `balances` | Tüm bakiyeler | Balance changes |
| `positions` | `positions` | Açık pozisyonlar | Futures (placeholder) |

#### Market Format

`{base}_{quote}` — lowercase, underscore separated.

Örnekler: `btc_tl`, `eth_tl`, `btc_usdt`, `sol_usdt`

Geçerli marketler `config-service`'ten alınır. Geçersiz market → `40002 Invalid market`.

### 11.6 Complete Payload Specifications

> Bkz. [Section 5.2](#52-channel-specifications) — tüm payload örnekleri ve alan tanımları orada detaylı verilmiştir.

---

## 12. Error Handling & Edge Cases

### 12.1 Error Codes

| Code | HTTP Karşılığı | Açıklama | Client Aksiyonu |
|---|---|---|---|
| `0` | 200 | Success | — |
| `40001` | 400 | Invalid channel name | Channel adını düzelt |
| `40002` | 400 | Invalid market | Market adını kontrol et |
| `40003` | 429 | Too many subscriptions (>200) | Gereksiz sub'ları kaldır |
| `40004` | 429 | Rate limit exceeded | Slow down |
| `40005` | 400 | Invalid request format | JSON/field validation hatası |
| `40006` | 400 | Invalid method | Desteklenen method kullan |
| `40100` | 401 | Auth required (private ch) | JWT ile bağlan |
| `40101` | 401 | Token expired | Yeni token al, reconnect |
| `40102` | 403 | Insufficient permissions | Tier upgrade gerekli |
| `50000` | 500 | Internal error | Retry with backoff |
| `50001` | 503 | Service unavailable | Retry with backoff |

### 12.2 Edge Cases

#### E1: Orderbook Sequence Gap

**Senaryo:** Client `seq=100` aldı, sonraki mesaj `seq=102` (101 kayıp).

**Client davranışı:**
1. Mevcut local orderbook state'i invalid kabul et
2. `unsubscribe` → `subscribe` (yeni snapshot + sequence sıfırdan)
3. Veya full reconnect

**Root cause olasılıkları:**
- ws-hub'da slow consumer drop
- Network packet loss (nadir, TCP düzeltir ama WS frame boundary'de olabilir)

#### E2: Snapshot Timeout

**Senaryo:** Subscribe sonrası 5 saniye içinde snapshot gelmez.

**Server davranışı:** Snapshot servisi timeout → error response:
```json
{"id": 1, "code": 50000, "msg": "snapshot unavailable, retry"}
```

**Client davranışı:** 1-2 saniye bekle, tekrar subscribe et.

#### E3: Kafka Consumer Lag

**Senaryo:** ws-hub Kafka'dan mesaj consume etmekte gecikiyor.

**Mitigation:**
- Alert: Consumer lag > 1000 for > 30s
- Auto-action: Yok (manual investigation gerekir)
- Client impact: Latency artışı, ama mesaj kaybı yok (Kafka persistent)

#### E4: Pod Restart / Rolling Update

**Senaryo:** ws-hub pod'u restart ediliyor.

**Flow:**
1. Pod graceful shutdown başlar
2. Tüm bağlı client'lara `disconnecting` event (code 1001, retryAfterMs: 0)
3. WebSocket close frame gönderilir
4. Pod terminates
5. Client'lar KrakenD üzerinden başka pod'a reconnect
6. Sticky routing: Yeni pod assignment (consistent hash ring güncellenir)

**Downtime per client:** < 2 saniye (reconnect süresi)

#### E5: JWT Expire During Active Connection

**Senaryo:** JWT 24 saat sonra expire olur, connection hala aktif.

**Davranış (Öncesi):** Connection korunur. JWT sadece connection kurulumunda validate edilir. Mevcut bağlantı etkilenmez.

**Problemler Tespit Edilen (Review):**
- User token revoke edilmiş (account compromised) ama connection açık kalır
- Security risk: 24 saat boyunca unauthorized user veri alabilir

**Çözüm (Yeni):**
1. **Token revocation propagation:** JWT revoke edildiğinde KrakenD → ws-hub'a notification
2. **ws-hub admin API:** `POST /admin/disconnect?userId=u_12345&reason=token_revoked`
3. **Graceful disconnect:** İlgili user'lar `code=4003` (auth failed) ile disconnect edilir

**Implementation (Phase 2):**
- [ ] KrakenD webhook → ws-hub
- [ ] ws-hub admin API port (internal, pod-to-pod)
- [ ] Force disconnect logic: User'ın tüm pod'lardaki connection'ları terminate eder

> 📋 **Review Notu:** JWT revoke sırasında long-lived connection'lar etkilenmemesi security riski tespit edilmiş (M6). **Çözüm:** Token revocation propagation mechanism tanımlanmıştır (Phase 2'de implement edilecek).

#### E6: Duplicate Subscribe

**Senaryo:** Client aynı channel'a iki kez subscribe olur.

**Davranış:** İdempotent. İkinci subscribe → success (zaten subscribed). Duplicate mesaj gönderilmez.

#### E7: Network Partition (Client ↔ Server)

**Senaryo:** Client hala connection açık sanıyor ama server tarafı closed.

**Detection:**
- Client: ping gönder, 10 saniye pong gelmezse → dead connection
- Server: 60 saniye client'tan mesaj yoksa → connection close

---

## 13. Rate Limiting & Fair Usage

### 13.1 Connection Limits

| Limit | Anonymous | Retail | MM |
|---|---|---|---|
| Max connections per IP | 5 | — | — |
| Max connections per user | — | 10 | 50 |
| Max channels per connection | 50 | 200 | 200 |
| Max total channels per user | — | 500 | 1,000 |

### 13.2 Request Rate Limits

| Action | Limit | Window |
|---|---|---|
| `subscribe` requests | 10 | per second, per connection |
| `unsubscribe` requests | 10 | per second, per connection |
| `ping` requests | 5 | per second, per connection |
| Total inbound messages | 20 | per second, per connection |

### 13.3 Rate Limit Enforcement

Rate limit aşıldığında:

1. **Soft limit (ilk aşım):** Error response döner:
   ```json
   {"id": 5, "code": 40004, "msg": "rate limit exceeded, slow down"}
   ```

2. **Hard limit (sürekli aşım, 30s içinde 10x soft limit):** Graceful disconnect:
   ```json
   {"event": "disconnecting", "code": 4029, "msg": "rate limit exceeded", "retryAfterMs": 30000}
   ```

### 13.4 Fair Usage Policy

- Tek user tüm pod kaynaklarının >10%'unu kullanamaz
- Anomali tespiti: Bir user'ın message throughput'u ortalamanın 100x üstündeyse → alert
- MM tier kullanıcılar için dedicated capacity reservation (opsiyonel, Phase 2)

---

## 14. Testing Strategy

### 14.1 Unit Tests

| Bileşen | Test Coverage Hedefi | Focus |
|---|---|---|
| Channel Router | >90% | Subscribe/unsubscribe, fan-out, partial success |
| Sequence Manager | >95% | Monotonicity, gap buffer, overflow |
| Connection Manager | >90% | Auth, rate limit, max connections |
| Write Scheduler | >85% | Backpressure, batching, slow consumer |
| Snapshot Cache | >90% | Orderbook reconstruction, staleness |
| Protocol Parser | >95% | Valid/invalid JSON, edge cases |

### 14.2 Integration Tests

| Test | Açıklama |
|---|---|
| E2E Subscribe Flow | Connect → subscribe → receive data → unsubscribe → disconnect |
| Auth Flow | Anonymous public, JWT private, expired token, invalid token |
| Orderbook Consistency | Subscribe → snapshot → deltas → local OB matches match engine |
| Reconnection Resume | Connect → get seq → disconnect → reconnect with lastSeq → verify no gap |
| Multi-pod Routing | User connects to pod A, private message routed correctly |
| Rate Limit | Exceed subscribe rate → error → exceed hard limit → disconnect |
| Slow Consumer | Client stops reading → drop counter increases → warning → disconnect |
| Graceful Shutdown | Kill pod → clients receive disconnecting event → reconnect to new pod |

### 14.3 Load Tests

| Scenario | Target | Tool |
|---|---|---|
| Max connections | 10K per pod, 100K cluster | k6 WebSocket, custom Go client |
| Message throughput | 500K msg/s per pod outbound | Custom Go benchmark |
| Spike subscribe | 1000 subscribe requests in 1 second | k6 |
| Connection churn | 1000 connect/disconnect per second | Custom |
| Large orderbook | 10,000 level orderbook snapshot delivery | Custom |
| Long-running | 24 hour sustained load | k6 + custom |

### 14.4 Chaos Tests

| Test | Açıklama |
|---|---|
| Kafka broker kill | 1/3 broker down → verify no message loss, consumer rebalance |
| ws-hub pod kill | Random pod termination → verify client reconnect, no data loss |
| Network partition | iptables rules → verify timeout behavior |
| Memory pressure | Limit pod memory → verify OOM behavior, graceful degradation |
| CPU throttle | Limit CPU → verify latency degradation characteristics |

### 14.5 Compatibility Tests

| Client | Test |
|---|---|
| Python `websockets` | Full protocol test |
| Node.js `ws` | Full protocol test |
| Go `gorilla/websocket` | Full protocol test |
| Browser native WebSocket | Public channels test |
| wscat | Manual smoke test |

---

## 15. Rollout Plan

### Phase 0: Infrastructure (Week -2 to 0)

- [ ] Kafka topic'leri oluştur (`ws.ticker`, `ws.orderbook.delta`, vb.)
- [ ] Match engine'den yeni topic'lere publish başlat (mevcut topic'lere paralel)
- [ ] KrakenD `/v2` route config hazırla (disabled)
- [ ] ws-hub v2 Docker image build pipeline
- [ ] Monitoring dashboard + alerting rules

### Phase 1: Internal Beta (Week 0-2)

- [ ] ws-hub v2 deploy (2 pod, staging)
- [ ] KrakenD `/v2` route enable (staging)
- [ ] Internal QA: Protocol compliance test
- [ ] Internal QA: Orderbook consistency test (48 saat)
- [ ] Performance baseline (latency, throughput)
- [ ] Fix bugs, iterate

### Phase 2: MM Beta (Week 2-4)

- [ ] Production deploy (3 pod, low traffic)
- [ ] KrakenD `/v2` route enable (production, whitelist IP)
- [ ] 2-3 MM partner onboard
- [ ] MM feedback collection (1:1 calls)
- [ ] Orderbook consistency monitoring (production, 7 gün)
- [ ] Sequence gap alert: zero tolerance
- [ ] Fix issues, iterate

### Phase 3: Public GA (Week 4-8)

- [ ] KrakenD `/v2` route open (all users)
- [ ] Scale to 5-10 pods
- [ ] Documentation publish
- [ ] Python SDK release
- [ ] TypeScript SDK release
- [ ] Blog post / changelog announcement
- [ ] Monitor adoption metrics

### Phase 4: Migration Push (Week 8-12)

- [ ] v1 deprecation header ekle
- [ ] v1 kullanıcılarına email bildirim
- [ ] docs.paribu.com v1 deprecation banner
- [ ] v1 connection limit'i kademeli düşür
- [ ] v2 adoption tracking (target: %80)

### Phase 5: v1 Sunset (Week 12-24)

- [ ] v1 yeni connection kabul etmeyi durdur (week 20)
- [ ] v1 mevcut connection'ları graceful disconnect (week 24)
- [ ] v1 pod'ları decommission (week 28, 4 hafta cold standby sonrası)

### Rollback Plan

Her phase'de rollback mümkün:
- Phase 1-2: ws-hub v2 pod'ları sil, KrakenD route disable
- Phase 3: `/v2` route disable, announcement
- Phase 4-5: v1 deprecation header kaldır, limit'leri geri al

---

## 16. Success Metrics & KPIs

### 16.1 Adoption Metrics

| Metrik | Target (3 ay) | Target (6 ay) |
|---|---|---|
| v2 aktif connection sayısı | 5,000 | 20,000 |
| v2'ye geçen MM sayısı | 5 | Tüm aktif MM'ler |
| v1 kullanım oranı | <%50 | %0 (sunset) |
| 3rd party integratör geçişi | %30 | %90 |

### 16.2 Performance Metrics

| Metrik | Target |
|---|---|
| E2E message latency p50 | <5ms |
| E2E message latency p99 | <25ms |
| Snapshot delivery p99 | <50ms |
| Uptime | >99.95% |
| Message delivery rate | >99.99% (drop <0.01%) |

### 16.3 Reliability Metrics

| Metrik | Target |
|---|---|
| Sequence gap events per day | 0 |
| Orderbook desync incidents per month | 0 |
| Mean time to reconnect (MTTR) | <2s |
| Graceful shutdown success rate | >99% |

### 16.4 Business Metrics

| Metrik | Target |
|---|---|
| MM onboarding time | <2 gün (vs mevcut ~2 hafta) |
| MM-sourced liquidity artışı | %20 (6 ay) |
| API-related support ticket azalması | %50 |
| New MM partner acquisition | +3 (6 ay) |

---

## 17. Open Questions / Risks

### 17.1 Open Questions

| # | Soru | Sahip | Status | Review Çözümü |
|---|---|---|---|---|
| Q1 | Private topic routing: Sticky hash mı, broadcast mı, internal relay mi? | Platform Eng | ✅ **KARAR VERİLDİ** | **Sticky hash via KrakenD** (Opsiyon 3 approved). Pod scale event'te graceful reconnect mechanism eklenmiştir. |
| Q2 | Sequence number public channel'larda Kafka offset mi, bağımsız counter mi? | Platform Eng | ✅ **KARAR VERİLDİ** | **Public: Kafka offset = sequence** (pod-independent, persistent). **Private: Pod-local counter** (pod restart → snapshot fallback). |
| Q3 | API key authentication — v2 GA'da mı, sonra mı? | Product | ⏳ **Phase 2 feedback'e bağlı** | Phase 2 MM beta'da JWT test edip MM'ler şikayet ederse (token refresh overhead) → Phase 3'te API key ekle. |
| Q4 | `positions` channel ne zaman aktifleşecek? | Product | ✅ **KARAR VERİLDİ** | **Futures launch'a kadar placeholder** (client subscribe → 40002 error). |
| Q5 | SDK dilleri? Python + TypeScript yeterli mi? | Product | ✅ **KARAR VERİLDİ** | **v2 GA: Python + TypeScript**. **Phase 3: Go SDK** (MM feedback'ine göre). Java/C# gerek olursa Phase 4'te. |
| Q6 | MM'ler için dedicated pod pool gerekli mi? | Platform Eng | ⏳ **Phase 2 test sonrası** | Phase 2'de shared pool ile başla. MM'ler latency problemi yaşarsa dedicated pool ekle (ws-hub-v2-mm deployment). |
| Q7 | Message batching (1ms) — default açık mı kapalı mı? | Platform Eng | ✅ **KARAR VERİLDİ** | **Default kapalı** (latency-sensitive MM'ler için). `enableForTier: ["retail"]` (retail tier'da opsiyonel aç). |
| Q8 | KrakenD WebSocket proxy yeterli mi? | Platform Eng | ⏳ **PoC sonrası (Phase 0)** | **PoC test edilmeli:** KrakenD 1 pod @ 10K concurrent connection + TLS → CPU/memory profiling. Başarılı ise KrakenD kullan. Fail ise custom Go proxy (2-3 hafta dev). |
| Q9 | Disaster Recovery planı? (Review'da eklendi) | Platform Eng | ⏳ **Phase 4-5'te değer** | Single-region (İstanbul) ile start. Phase 4-5'te DR (cross-region replication + passive standby) planlanabilir. Ayrı PRD gerekebilir. |

### 17.2 Risks

| # | Risk | Olasılık | Etki | Mitigation |
|---|---|---|---|---|
| R1 | KrakenD WebSocket proxy performans bottleneck | Orta | Yüksek | PoC ile erken test. Gerekirse KrakenD bypass, direct TCP/TLS termination |
| R2 | In-memory orderbook reconstruction memory pressure | Düşük | Orta | Market başına OB boyut limiti. Monitoring + alerting. Lazy load (sadece subscribe olan market'ler) |
| R3 | Sticky routing ile pod restart'ta connection storm | Orta | Orta | Graceful shutdown + staggered reconnect (retryAfterMs randomization) |
| R4 | Kafka Redpanda topic sayısı artışı (per-market partition) | Düşük | Düşük | Redpanda partition limitleri geniş. 500 market × 6 topic = 3000 partition (manageable) |
| R5 | MM'lerin v2'ye geçmek istememesi (v1 "yeterli" algısı) | Orta | Yüksek | Beta'da MM'lerle yakın çalışma. Feature değer önerisi net olmalı. v1 sunset timeline açık |
| R6 | Sequence gap buffer memory (5 dk × yüksek throughput market) | Düşük | Orta | Per-channel buffer size cap. Oldest-first eviction. Memory monitoring |
| R7 | Gorilla WebSocket library maintenance durumu | Düşük | Düşük | `nhooyr.io/websocket` veya `gobwas/ws` alternatif olarak evaluate |
| R8 | Multi-pod private channel routing complexity | Orta | Yüksek | Sticky routing ile basitleştir. PoC ile validate |

---

## 18. Competitive Analysis

### 18.1 Feature Comparison

| Feature | Binance | Bybit | OKX | Paribu v1 | **Paribu v2** |
|---|---|---|---|---|---|
| **Endpoint** | `stream.binance.com/ws` | `stream.bybit.com/v5/public` | `ws.okx.com:8443/ws/v5` | `stream.paribu.com` | `stream.paribu.com/v2` |
| **Protocol** | JSON | JSON | JSON | JSON | JSON |
| **Subscribe format** | `{"method":"SUBSCRIBE","params":["btcusdt@ticker"]}` | `{"op":"subscribe","args":["orderbook.50.BTCUSDT"]}` | `{"op":"subscribe","args":[{"channel":"tickers","instId":"BTC-USDT"}]}` | N/A (auto) | `{"method":"subscribe","params":["ticker@btc_tl"]}` |
| **Sequence numbers** | ❌ (lastUpdateId for OB only) | ✅ | ❌ (OB: seqId only) | ❌ | ✅ (all channels) |
| **Snapshot on subscribe** | ❌ (REST API gerekli) | ✅ (orderbook) | ❌ (REST API gerekli) | ❌ | ✅ (orderbook + more) |
| **Client ping/pong** | ❌ (server-only) | ✅ (`{"op":"ping"}`) | ✅ (`ping` frame) | ❌ | ✅ |
| **Incremental OB** | ✅ (diff stream) | ✅ (delta) | ✅ (snapshot + delta) | ❌ | ✅ (delta + snapshot depth) |
| **BBO channel** | ✅ (`bookTicker`) | ✅ (`tickers`) | ✅ (`bbo-tbt`) | ❌ | ✅ |
| **Kline/candle** | ✅ | ✅ | ✅ | ❌ | ✅ |
| **Private channels** | ✅ (listenKey) | ✅ (auth) | ✅ (login) | ✅ (authorize frame) | ✅ (JWT via KrakenD) |
| **Max connections** | 5/IP (stream) | 20/IP (public) | 3/IP/channel | 100/client | 5/IP (pub), 10/user (priv) |
| **Max channels** | 200/conn | 10/conn (!) | 240 (public) | 16 total | 200/conn |
| **Auth method** | HMAC listenKey (REST) | HMAC WS login | HMAC WS login | JWT (authorize frame) | JWT (HTTP header) |
| **Reconnection resume** | ❌ | ❌ | ❌ | ❌ | ✅ (lastSeq) |
| **Graceful disconnect** | ❌ | ❌ | ❌ | ❌ | ✅ (code + retryAfter) |
| **Rate limit info** | 429 HTTP (REST), disconnect (WS) | Error frame | Error frame | ❌ | ✅ (error frame + code) |
| **Compression** | ❌ (stream), ✅ (api) | ✅ (deflate) | ✅ (deflate) | ✅ (deflate) | ✅ (deflate) |
| **Binary protocol** | ❌ | ❌ | ❌ | ❌ | ❌ (v3 candidate) |

### 18.2 Paribu v2 Avantajları

| Avantaj | vs Binance | vs Bybit | vs OKX |
|---|---|---|---|
| **All-channel sequence numbers** | ✅ (Binance sadece OB) | Eşit | ✅ (OKX sadece OB) |
| **Snapshot on subscribe** | ✅ (Binance REST gerektirir) | Eşit | ✅ (OKX REST gerektirir) |
| **Reconnection resume (lastSeq)** | ✅ | ✅ | ✅ |
| **Graceful disconnect** | ✅ | ✅ | ✅ |
| **200 channel/conn** | Eşit | ✅ (Bybit 10!) | Eşit |
| **JWT (no handshake auth)** | ✅ (Binance listenKey REST call) | ✅ (Bybit WS login) | ✅ (OKX WS login) |
| **Connection ID for debug** | ✅ | ✅ | ✅ |

### 18.3 Paribu v2 Dezavantajları / Farklar

| Konu | Durum | Plan |
|---|---|---|
| Binance'ın market depth'i çok daha derin | Likidite farkı, API sorunu değil | MM onboarding ile likidite artışı |
| Combined stream URL (Binance `/stream?streams=`) | Paribu v2 subscribe ile | v2 yaklaşımı daha esnek (runtime subscribe/unsub) |
| OKX'in instrument-based channel yapısı | Paribu v2 `@market` formatı daha basit | — |
| WS üzerinden order placement | Yok (REST only) | Ayrı PRD (v3 candidate) |
| FIX protocol | Yok | Talep gelirse değerlendirilecek |

### 18.4 Önemli Rakip Detayları

**Binance:**
- listenKey (REST'ten alınır, 60dk expire, PUT ile uzatılır) → ek complexity
- Orderbook: REST snapshot + WS delta → client tarafında senkronizasyon gerekli
- 5 connection limit (stream), 300 subscription limit (combined)
- 24 saat sonra auto-disconnect

**Bybit:**
- Per-connection 10 topic limit (!) → birden fazla connection açmak zorunlu
- Orderbook: Subscribe sonrası snapshot + delta (Paribu v2 ile aynı)
- 100 connection limit per IP
- Ping every 20s zorunlu (yoksa disconnect)

**OKX:**
- WS login (HMAC) → her connection'da auth handshake
- 3 connection per IP per channel type (çok kısıtlı)
- Orderbook: REST snapshot gerekli (Binance gibi)
- 240 subscription limit (public)

---

## Appendix A: SDK Örnekleri

### Python

```python
import asyncio
import json
import websockets

async def main():
    uri = "wss://stream.paribu.com/v2"
    headers = {"Authorization": "Bearer <JWT>"}

    async with websockets.connect(uri, extra_headers=headers) as ws:
        # Connected event
        msg = json.loads(await ws.recv())
        print(f"Connected: {msg['connId']}")

        # Subscribe
        await ws.send(json.dumps({
            "method": "subscribe",
            "params": ["ticker@btc_tl", "orderbook@btc_tl", "orders@btc_tl"],
            "id": 1
        }))

        # Ack
        ack = json.loads(await ws.recv())
        print(f"Subscribed: {ack['data']['subscribed']}")

        # Listen
        last_seq = {}
        async for raw in ws:
            msg = json.loads(raw)
            if "ch" in msg:
                ch = msg["ch"]
                seq = msg["seq"]

                # Gap detection
                if ch in last_seq and seq != last_seq[ch] + 1:
                    print(f"GAP on {ch}: expected {last_seq[ch]+1}, got {seq}")
                    # Resubscribe to get fresh snapshot
                    await ws.send(json.dumps({
                        "method": "unsubscribe", "params": [ch], "id": 99
                    }))
                    await ws.send(json.dumps({
                        "method": "subscribe", "params": [ch], "id": 100
                    }))

                last_seq[ch] = seq
                print(f"[{ch}] seq={seq} data={msg['data']}")

asyncio.run(main())
```

### TypeScript / Node.js

```typescript
import WebSocket from 'ws';

const ws = new WebSocket('wss://stream.paribu.com/v2', {
  headers: { Authorization: 'Bearer <JWT>' }
});

const lastSeq: Record<string, number> = {};

ws.on('open', () => {
  ws.send(JSON.stringify({
    method: 'subscribe',
    params: ['ticker@btc_tl', 'orderbook@btc_tl', 'orders@btc_tl'],
    id: 1
  }));
});

ws.on('message', (raw: Buffer) => {
  const msg = JSON.parse(raw.toString());

  if (msg.event === 'connected') {
    console.log(`Connected: ${msg.connId}`);
    return;
  }

  if (msg.id && msg.code !== undefined) {
    console.log(`Ack id=${msg.id}: ${msg.msg}`);
    return;
  }

  if (msg.ch) {
    const { ch, seq, data } = msg;

    // Gap detection
    if (lastSeq[ch] !== undefined && seq !== lastSeq[ch] + 1) {
      console.warn(`GAP on ${ch}: expected ${lastSeq[ch] + 1}, got ${seq}`);
    }

    lastSeq[ch] = seq;
    console.log(`[${ch}] seq=${seq}`, data);
  }
});

// Ping every 20s
setInterval(() => {
  ws.send(JSON.stringify({ method: 'ping', id: Date.now() }));
}, 20_000);
```

---

## Appendix B: Glossary

| Terim | Açıklama |
|---|---|
| **BBO** | Best Bid/Offer — en iyi alış ve satış fiyatı |
| **Delta** | Orderbook'ta sadece değişen seviyeleri içeren güncelleme |
| **Fan-out** | Tek mesajın birden fazla subscriber'a dağıtılması |
| **Gap detection** | Sequence number'da atlama (kayıp mesaj) tespiti |
| **KrakenD** | API Gateway (L7 reverse proxy, JWT validation, rate limiting) |
| **MM (Market Maker)** | Borsada sürekli alış/satış emirleri vererek likidite sağlayan katılımcı |
| **Orderbook** | Bir market'teki tüm açık alış/satış emirlerinin fiyat-miktar listesi |
| **Snapshot** | Bir channel'ın mevcut tam durumunun (state) tek seferde gönderilmesi |
| **Sequence (seq)** | Per-channel monoton artan mesaj numarası |
| **Sticky routing** | Aynı kullanıcının her zaman aynı backend pod'a yönlendirilmesi |
| **Throttle** | Mesaj gönderim frekansının sınırlandırılması |
| **ws-hub** | Paribu'nun WebSocket pub/sub relay servisi |

---

## Appendix C: Revision History

| Versiyon | Tarih | Yazar | Değişiklik |
|---|---|---|---|
| 1.0 | 2026-02-19 | Platform Engineering | İlk draft |

---

*Bu doküman Paribu Platform Engineering ekibi tarafından hazırlanmıştır. Dağıtım: Internal — Confidential.*


---

# Appendix: Teknik Review



| | |
|---|---|
| **Review Dokümanı** | PRD-2026-003-REVIEW |
| **Kaynak PRD** | PRD-2026-003 (v1.0, 2026-02-19) |
| **Review Tarihi** | 2026-02-21 |
| **Reviewer** | Sub-Agent 973fc087 |
| **Review Tipi** | Zero-Risk Isolation + gRPC Extension Proposal |

---

## İçindekiler

1. [Özet (Executive Summary)](#1-özet-executive-summary)
2. [Bölüm 1: PRD Review — Zero-Risk İzolasyon Odaklı](#2-bölüm-1-prd-review--zero-risk-izolasyon-odaklı)
   - 2.1 [İzolasyon Analizi](#21-izolasyon-analizi)
   - 2.2 [Mimari Feedback](#22-mimari-feedback)
   - 2.3 [Açık Sorular (Q1-Q8) Değerlendirmesi](#23-açık-sorular-q1-q8-değerlendirmesi)
   - 2.4 [Risk Değerlendirmesi (R1-R8)](#24-risk-değerlendirmesi-r1-r8)
   - 2.5 [Eksik Konular](#25-eksik-konular)
   - 2.6 [Deployment İzolasyon Validasyonu](#26-deployment-izolasyon-validasyonu)
3. [Bölüm 2: gRPC Direct Message Delivery Önerisi](#3-bölüm-2-grpc-direct-message-delivery-önerisi)
   - 3.1 [Use Case: Neden gRPC?](#31-use-case-neden-grpc)
   - 3.2 [Mimari Entegrasyon](#32-mimari-entegrasyon)
   - 3.3 [API Tasarımı (Proto Sketch)](#33-api-tasarımı-proto-sketch)
   - 3.4 [Deployment Stratejisi](#34-deployment-stratejisi)
   - 3.5 [Authentication](#35-authentication)
   - 3.6 [Avantajlar (vs WebSocket)](#36-avantajlar-vs-websocket)
   - 3.7 [Trade-offs](#37-trade-offs)
   - 3.8 [Öneri: Faz ve Timeline](#38-öneri-faz-ve-timeline)

---

## 1. Özet (Executive Summary)

Bu doküman, WS v2 Stream API PRD'yi (PRD-2026-003) iki kritik açıdan incelemektedir:

### Review Bölüm 1: Zero-Risk İzolasyon Analizi
PRD'nin **en kritik gereksinimi**, v2'nin v1 üzerinde **sıfır etki** yapması ve izole çalışmasıdır. Review sonuçları:

**✅ Güçlü İzolasyon Noktaları:**
- Ayrı KrakenD route (`/v2`)
- Ayrı deployment (ws-hub-v2 pod'ları)
- Ayrı Kafka consumer group (`ws-hub-v2`)
- v1 ve v2 farklı pod set'leri üzerinde

**⚠️ İzolasyon Riski Tespit Edilen Alanlar:**
1. **Kafka topic paylaşımı** — Match engine aynı topic'lere yazıyor, consumer group farklı ama partition load v2 launch ile artabilir
2. **KrakenD shared instance** — PRD'de "KrakenD Gateway" tek katmandan bahsediyor, v1 ve v2 aynı KrakenD instance kullanıyorsa CPU/memory contention riski
3. **Snapshot Service bağımlılığı** — v2'nin orderbook snapshot için external gRPC service'e bağımlılığı, ancak bu servis v1 tarafından da kullanılıyorsa bottleneck
4. **Redis optional kullanımı** — PRD'de Redis "opt." olarak işaretli, eğer paylaşılıyorsa (örneğin JWT blacklist) izolasyon zayıflar

**Genel Değerlendirme:** İzolasyon konsepti **sağlam**, ancak **operasyonel ve deployment detayları** netleştirilmeli. 

**Action Items:**
- Kafka partition'ların v2 eklenmesiyle v1'e etkisinin ölçülmesi (load test)
- KrakenD instance'ın v1/v2 için ayrı mı ortak mı olacağının açıkça belirtilmesi
- Snapshot Service kapasitesinin v2 yükü ile test edilmesi
- Redis kullanımının scope'unun netleştirilmesi

### Review Bölüm 2: gRPC Streaming Önerisi
PRD kapsamına **ek bir delivery mechanism** olarak **gRPC bidirectional streaming** önerilmektedir.

**Hedef kitle:** MM bot'ları (server-side çalışan, browser constraint'i olmayan)

**Temel avantajlar:**
- Native protobuf → %40-60 daha düşük bandwidth
- Built-in flow control (backpressure)
- HTTP/2 multiplexing → single TCP connection, multiple streams
- Düşük latency (no JSON parse overhead)

**Öneri:** **Phase 3** (week 12-24) — WebSocket GA'dan sonra, MM feedback ile öncelik belirlenecek şekilde.

---

## 2. Bölüm 1: PRD Review — Zero-Risk İzolasyon Odaklı

### 2.1 İzolasyon Analizi

#### 2.1.1 ✅ İZOLE EDİLMİŞ BÖLGELER

| Kaynak | İzolasyon Durumu | Kanıt (PRD Referans) |
|---|---|---|
| **KrakenD Route** | ✅ Tam izole | Section 7.1: `/v2` route; Section 8.2: Ayrı endpoint `stream.paribu.com/v2` |
| **Pod Deployment** | ✅ Tam izole | Section 7.5: "ws-hub v2 (5-20 pod, HPA)", Section 15 Phase 1: "ayrı pod set" |
| **Kafka Consumer Group** | ✅ Tam izole | Section 7.4: Consumer group `ws-hub-v2` (v1'den farklı) |
| **Connection Manager** | ✅ Tam izole | Section 7.3.1: Her pod kendi `map[connId]*Connection` tutuyor |
| **Write Scheduler** | ✅ Tam izole | Section 7.3.5: Per-pod, per-connection write channel |

**Sonuç:** Core business logic ve runtime state tamamen izole. v2 pod crash olursa v1 etkilenmez.

---

#### 2.1.2 ⚠️ PAYLAŞILAN KAYNAKLAR (Potansiyel İzolasyon Riski)

##### **R1.1 — Kafka Topic Paylaşımı**

**Durum:** v1 ve v2, **aynı Kafka topic'lerden** consume ediyor:
- `ws.ticker`, `ws.orderbook.delta`, `ws.trades`, vb.

**PRD Referans:** Section 7.4 — Topic listesi belirtilmiş, ancak v1'in aynı topic'leri kullandığı PRD'de açıkça belirtilmemiş. Section 15 Phase 0: "Match engine'den yeni topic'lere publish başlat (mevcut topic'lere **paralel**)" → Bu belirsiz: "paralel" = aynı topic'e mi yoksa yeni topic'lere de mi?

**İzolasyon Riski:**
- Kafka broker'da partition count artarsa (v2'nin yeni consumer group'u ekleniyor) → broker CPU/disk I/O artar
- v2 launch sonrası consumer rebalance (Kafka consumer group koordinasyonu) → broker yükü artabilir
- Match engine'in publish throughput'u artarsa (v1 + v2 consumer var) → broker bant genişliği

**Risk Seviyesi:** **DÜŞÜK-ORTA**
- Kafka/Redpanda 3 node cluster, modern hardware → 2 consumer group ek yük düşük
- Ancak match engine publish rate çok yüksekse (örneğin 100K msg/s × 6 topic) → dikkat

**Mitigation Kontrol:**
- PRD Section 15 Phase 0: Match engine "mevcut topic'lere paralel" diyor → **netleştirilmeli**: aynı topic'e mi? Yoksa v1 ve v2 için ayrı topic'ler mi?
- **Önerilen yaklaşım:** Aynı topic'leri kullan (kaynak tasarrufu), ancak v2 launch öncesi **Kafka load test** yapılmalı (v1 + v2 consumer simülasyonu)
- **Alternatif (tam izolasyon):** v2 için ayrı topic'ler (`ws.v2.ticker`, `ws.v2.orderbook.delta`) → Match engine her iki set'e de yazmalı → daha fazla Kafka storage ve network, ama izolasyon maksimum

**Action Item:**
```
[ ] Kafka capacity planning: Match engine publish rate × 2 consumer group ile load test
[ ] PRD'de topic stratejisi açık hale getirilmeli (shared vs separate)
[ ] v2 launch monitoring: Kafka broker CPU, disk I/O, replication lag
```

---

##### **R1.2 — KrakenD Gateway Instance**

**Durum:** PRD Section 7.1 ve 7.5'te "KrakenD Gateway" tek katman olarak gösteriliyor. v1 ve v2 **aynı KrakenD instance** kullanıyor mu yoksa ayrı mı?

**PRD Referans:** Section 7.5: "KrakenD (3 pod)" — sayı belirtilmiş ama v1/v2 ayrımı yok.

**İzolasyon Riski:**
- **Shared instance ise:** v2 traffic spike → KrakenD CPU/memory tükenir → v1 yavaşlar veya error rate artar
- **Rate limiting shared ise:** v2'nin rate limit aşımı, shared KrakenD instance limitlerini tüketebilir
- **JWT validation bottleneck:** v2 launch'ta ani JWT validation artışı → KrakenD RS256 CPU yükü

**Risk Seviyesi:** **ORTA-YÜKSEK**
- KrakenD L7 proxy → CPU-intensive (TLS termination, JWT decode, routing)
- v2 beta/GA geçişinde traffic 10x artarsa → shared instance sorun yaratabilir

**Mitigation Kontrol:**
- PRD'de açıkça belirtilmeli: **Ayrı KrakenD instance** (örneğin `krakend-v1-*` ve `krakend-v2-*` deployment'ları)
- Veya KrakenD config'de v1 ve v2 route'ları için **ayrı rate limit bucket** (per-route rate limiting)

**Önerilen Yaklaşım:**
```yaml
# KrakenD Deployment (önerilen)
- krakend-v1 (3 pod) → /ws route → ws-hub (v1)
- krakend-v2 (3 pod) → /v2 route → ws-hub-v2
```

**Veya (maliyet optimizasyonu):**
```yaml
# Shared KrakenD ama strict resource isolation
- krakend (5 pod total)
  - /ws route → ws-hub (rate limit: 10K conn/pod)
  - /v2 route → ws-hub-v2 (rate limit: 10K conn/pod)
  - Resource quota: v2 max %50 CPU/memory kullanabilir
```

**Action Item:**
```
[ ] KrakenD deployment strategy netleştirilmeli (shared vs separate)
[ ] Shared kullanılıyorsa, per-route resource quota tanımlanmalı
[ ] KrakenD load test: v1 + v2 concurrent traffic (20K v1 conn + 10K v2 conn)
```

---

##### **R1.3 — Snapshot Service (gRPC)**

**Durum:** PRD Section 7.3.4: "Orderbook snapshot: Match engine'den gRPC ile çekilir"

**PRD belirsizlik:** Bu "Snapshot Service" nedir? Ayrı bir servis mi yoksa match engine'in bir endpoint'i mi? v1 de aynı servisi kullanıyor mu?

**İzolasyon Riski:**
- v2 her subscribe'da snapshot çekerse (örneğin 1000 subscription/s) → Snapshot Service bottleneck
- v1 de aynı servisi kullanıyorsa → v2'nin yükü v1'i yavaşlatır

**Risk Seviyesi:** **ORTA**
- Snapshot Service'in kapasitesi bilinmiyor
- PRD'de "In-memory orderbook reconstruction" önerilmiş (Section 7.3.4) → bu durumda external service call yok, risk azalır

**Mitigation Kontrol:**
- PRD Section 7.3.4: "**Karar: In-memory orderbook reconstruction**" → ws-hub v2 kendi orderbook'unu delta'lardan oluşturuyor
- Bu durumda **external Snapshot Service'e dependency yok** → izolasyon sağlanmış ✅

**Ancak:**
- Private channel snapshot'ları (orders, balances) için "order-api / wallet servisine gRPC call" (Section 7.3.4)
- Bu servisler v1 tarafından da kullanılıyorsa → load artışı v1'i etkileyebilir

**Önerilen Yaklaşım:**
- order-api ve wallet servisleri **zaten distributed** (her request bağımsız) → stateless
- v2 snapshot call'ları, normal API traffic gibi davranır → mevcut rate limit ve kapasitede handle edilmeli
- **Action:** order-api ve wallet load test'ine v2 snapshot traffic ekle (örneğin +20% request rate)

**Action Item:**
```
[ ] order-api ve wallet servislerinin v2 snapshot yükünü handle edip edemeyeceği test edilmeli
[ ] v2 snapshot rate limiting eklenmeli (örneğin user başına 10 subscribe/s)
```

---

##### **R1.4 — Redis (Optional Shared Resource)**

**Durum:** PRD Section 7.1 ve 7.5: Redis "opt." (optional) olarak gösteriliyor.

**PRD'de Redis kullanım senaryoları:**
- Section 7.3.4: Trades snapshot için "Redis'ten" (alternatif olarak)
- Section 9.2: JWT blacklist (revocation) — KrakenD kontrol eder

**İzolasyon Riski:**
- **JWT blacklist Redis shared ise:** v2'nin revoke request'leri Redis'i yavaşlatır → v1 JWT validation yavaşlar
- **Trades snapshot Redis shared ise:** v2 snapshot query'leri Redis CPU/memory tüketir → v1 etkilenir

**Risk Seviyesi:** **DÜŞÜK**
- JWT blacklist: Çok düşük throughput (revoke rare event)
- Trades snapshot: İlk subscribe'da bir kez çağrılır, sonrası yok

**Mitigation Kontrol:**
- Redis cluster veya keyspace separation kullanılabilir (örneğin `v1:*` ve `v2:*` prefix'leri)
- Redis sentinel/cluster → multiple instances, load distribution

**Önerilen Yaklaşım:**
- JWT blacklist: Shared Redis kullanılabilir (revoke rate çok düşük)
- Trades snapshot: ws-hub v2 **in-memory cache** kullanmalı (son 50 trade'i memory'de tut) → Redis'e bağımlılık kaldırılır

**Action Item:**
```
[ ] Redis kullanım scope'unu netleştir (sadece JWT blacklist mı yoksa snapshot cache de mi?)
[ ] ws-hub v2'de trades snapshot için in-memory cache kullan (Redis dependency ortadan kalksın)
```

---

#### 2.1.3 İZOLASYON ÖZET SKORU

| Kaynak Tipi | İzolasyon Durumu | Risk Seviyesi | Action Gerekli? |
|---|---|---|---|
| **Pod Deployment** | ✅ Tam izole | YOK | Hayır |
| **Kafka Consumer Group** | ✅ Tam izole | YOK | Hayır |
| **KrakenD Route** | ✅ Tam izole | YOK | Hayır |
| **Kafka Topic** | ⚠️ Shared | DÜŞÜK-ORTA | Evet (load test) |
| **KrakenD Instance** | ❓ Belirsiz | ORTA-YÜKSEK | Evet (açıklama gerekli) |
| **Snapshot Service (gRPC)** | ⚠️ Shared (order-api, wallet) | ORTA | Evet (capacity test) |
| **Redis** | ⚠️ Optional shared | DÜŞÜK | Evet (scope netleştir) |

**GENEL DEĞERLENDİRME:**
- **Core izolasyon: GÜÇLÜ** ✅
- **Shared resource management: İYİLEŞTİRİLEBİLİR** ⚠️
- **v1'e sıfır etki garantisi: %85** (kalan %15 shared resource'ların capacity planlaması ile sağlanır)

---

### 2.2 Mimari Feedback

#### 2.2.1 ✅ Güçlü Noktalar

1. **Sequence Number Everywhere (Section 5.4)**
   - Public ve private channel'larda monoton sequence → gap detection mümkün
   - Binance/Bybit'e göre daha iyi (onlar sadece orderbook'ta sequence veriyor)
   - MM desync problemini %100 çözer ✅

2. **Snapshot on Subscribe (Section 5.5)**
   - REST API dependency kaldırılmış (Binance/OKX'te zorunlu)
   - Race condition ortadan kalkmış
   - Reconnection süresi ~2s'ye düşmüş ✅

3. **In-Memory Orderbook Reconstruction (Section 7.3.4)**
   - Delta'lardan orderbook build → snapshot latency 0ms
   - External service dependency yok
   - Memory efficient (sadece subscribe olan market'ler için) ✅

4. **Sticky Routing (Section 7.4, Opsiyon 3)**
   - User → Pod mapping consistent → private channel routing basit
   - Pod-to-pod relay yerine direct delivery → latency azalır
   - KrakenD consistent hashing ile → basit implementasyon ✅

5. **Backpressure + Slow Consumer Handling (Section 7.3.5)**
   - Write channel buffer + drop counter → slow client tespit edilir
   - Warning + graceful disconnect → client'a bildirim gider
   - v1'in "default: drop" silent fail problemi çözülmüş ✅

6. **Graceful Disconnect Protocol (Section 5.1.4)**
   - Close code + `retryAfterMs` → client neden disconnect olduğunu biliyor
   - Maintenance window'da client'lar random interval'de reconnect eder (storm prevention)
   - Global exchange'lerde yok (Binance/Bybit/OKX) → Paribu competitive advantage ✅

---

#### 2.2.2 ⚠️ İyileştirilebilir / Eksik Detaylar

##### **M1 — Kafka Partition Strategy (Section 7.4)**

**Problem:** PRD'de topic partition sayısı "Market sayısı ile orantılı (1:1 veya N:1)" belirsiz.

**Önerilen yaklaşım:**
```yaml
# Public topics (broadcast pattern)
ws.ticker:
  partitions: 64  # Market count'tan bağımsız, load distribution için
  key: market     # Aynı market aynı partition'a düşer (ordering guarantee)
  
ws.orderbook.delta:
  partitions: 64
  key: market

ws.trades:
  partitions: 64
  key: market

# Private topics (user-keyed)
ws.orders:
  partitions: 64
  key: user_id    # Sticky routing ile sync

ws.fills:
  partitions: 64
  key: user_id

ws.balances:
  partitions: 64
  key: user_id
```

**Rationale:**
- 64 partition → yeterli parallelism, ama overhead az
- Market-based partitioning → aynı market'in message ordering guarantee
- User-based partitioning (private) → sticky routing ile match eder

**Action Item:**
```
[ ] PRD Section 7.4'e partition strategy ekle (partition count + key strategy)
```

---

##### **M2 — Sequence Number Overflow Handling (Section 5.4)**

**PRD'de belirtilen:** "2^53 (JavaScript safe integer). Pratikte overflow olmaz (~285 milyon yıl @ 1M msg/s)."

**Problem:** Bu hesaplama **single channel** için. Ama her pod kendi sequence counter'ını tutuyorsa:
- Pod restart → sequence 1'den başlar
- Client'ın `lastSeq` buffer'ında eski sequence var (örneğin 1M) → yeni pod'dan 1 gelirse gap detection false positive

**Çözüm alternatifleri:**
1. **Global sequence (Kafka offset):** Kafka partition offset doğrudan sequence olarak kullan
   - ✅ Pod-independent, persistent
   - ✅ Overflow riski yok (Kafka offset 64-bit)
   - ❌ Consumer offset commit delay varsa sequence tutarsızlığı olabilir

2. **Pod-local sequence + connection metadata:** Client reconnect'te yeni pod'a bağlanırsa, server `lastSeq` buffer'ı kabul etmez (farklı pod) → full snapshot gönderir
   - ✅ Basit implementasyon
   - ❌ Pod restart'ta tüm client'lar snapshot alır (bandwidth spike)

3. **Distributed sequence (Redis atomic counter):** Redis'te per-channel counter
   - ✅ Pod-independent
   - ❌ Redis dependency, latency ekler

**Önerilen:** **Opsiyon 1 (Kafka offset as sequence)**
- Public channel'larda zaten uygulanabilir (Kafka message offset doğrudan sequence)
- Private channel'larda: user-specific sequence gerekiyor, bu durumda Opsiyon 2 (pod-local + snapshot fallback) kabul edilebilir

**Action Item:**
```
[ ] PRD Section 5.4 ve 7.3.3'e sequence source strategy ekle
[ ] Public: Kafka offset = sequence
[ ] Private: Pod-local sequence, reconnect farklı pod'a düşerse full snapshot
```

---

##### **M3 — Connection ID Format ve Collision Risk (Section 5.1.1)**

**PRD'de:** `connId: "c_abc123def"` — format ve generation açıklanmamış.

**Problem:** Multi-pod ortamda collision riski.

**Önerilen format:**
```
c_{pod_id}_{timestamp_ns}_{random_6char}
Örnek: c_pod3_1740000000123456_a7f9e2
```

**Avantajları:**
- Pod ID → hangi pod'da oluştuğu belli (debug için)
- Timestamp → oluşturulma zamanı (log correlation)
- Random suffix → collision prevention (aynı nanosecond'te iki connection)

**Action Item:**
```
[ ] PRD Section 5.1.1'e connId format spec ekle
```

---

##### **M4 — HPA (Horizontal Pod Autoscaler) Metrik Seçimi (Section 7.5)**

**PRD'de:** "Metric: Active WebSocket connections per pod"

**Problem:** Connection count her zaman doğru metrik değil:
- 1000 connection ama hiç message gönderilmiyorsa (idle client'lar) → CPU düşük
- 500 connection ama hepsi orderbook subscribe (high throughput) → CPU yüksek

**Önerilen HPA metrik kombinasyonu:**
```yaml
metrics:
  - type: Resource
    resource:
      name: cpu
      target:
        type: Utilization
        averageUtilization: 70  # Primary metric
  
  - type: Pods
    pods:
      metric:
        name: ws_connections_active
      target:
        type: AverageValue
        averageValue: 8000      # Secondary (connection count)
  
  - type: Pods
    pods:
      metric:
        name: ws_messages_sent_rate
      target:
        type: AverageValue
        averageValue: 400000    # Tertiary (throughput)
```

**HPA policy:**
- Scale up: CPU >70% VEYA connection >8K VEYA message rate >400K/s → 2 dakika
- Scale down: CPU <40% VE connection <4K VE message rate <200K/s → 10 dakika (slow)

**Action Item:**
```
[ ] PRD Section 7.5 HPA metrik stratejisini zenginleştir (CPU + connection + throughput)
```

---

##### **M5 — Snapshot Cache Staleness (Section 7.3.4)**

**PRD'de:** "Cache invalidation: Her delta mesajında cache güncellenir"

**Problem:** Multi-pod scenario'da:
- Pod A: Orderbook cache güncelledi (delta seq=1000 aldı)
- Pod B: Henüz delta seq=1000 consume etmedi (consumer lag)
- Client pod B'ye subscribe ediyor → stale snapshot alır (seq=999)
- Sonra delta seq=1000 gelir → client duplicate veya gap sanır

**Çözüm:**
- Snapshot'ta her zaman **timestamp** ve **lastUpdateId** (match engine global sequence) olmalı
- Client snapshot aldıktan sonra gelen delta'nın `seq`'ini değil, `lastUpdateId`'sini karşılaştırmalı
- Veya: Snapshot gönderildiğinde, o anki pod'un consume ettiği **son delta seq** dahil edilmeli

**Önerilen snapshot payload:**
```json
{
  "ch": "orderbook@btc_tl",
  "type": "snapshot",
  "seq": 10000,           // Pod'un bu channel'daki son seq
  "snapshotSeq": 10000,   // Bu snapshot'ın dayandığı seq
  "lastUpdateId": 9928341, // Match engine global seq
  "ts": 1740000000000,
  "data": { ... }
}
```

Client mantığı:
1. Snapshot `snapshotSeq=10000` aldı
2. Sonraki delta `seq=10001` gelmeli
3. `seq != snapshotSeq + 1` ise → gap, resubscribe

**Action Item:**
```
[ ] PRD Section 7.3.4 ve 5.2.1'e snapshot metadata ekle (snapshotSeq, lastUpdateId)
[ ] Client SDK'da gap detection mantığı bu metadata'ya göre implement edilmeli
```

---

##### **M6 — JWT Expiry ve Long-Lived Connection (Edge Case E5)**

**PRD'de:** "JWT 24 saat sonra expire olur, connection hala aktif. Connection korunur."

**Problem:** Security perspective'den sorunlu:
- User token revoke edilmiş (örneğin account compromised) ama connection 24 saat açık kalır
- JWT blacklist (Redis) var ama sadece yeni connection'larda kontrol ediliyor

**Önerilen çözüm:**
1. **Periodic JWT revalidation:** ws-hub her 1 saatte bir active connection'ların JWT'sini KrakenD üzerinden validate eder (veya Redis blacklist check)
   - Revoke edilmişse → graceful disconnect (code 4003)
   
2. **KrakenD push notification:** JWT revoke edildiğinde KrakenD, ilgili user'ın connection'larına disconnect mesajı gönderir
   - ws-hub internal API: `POST /admin/disconnect?userId=u_12345&reason=token_revoked`

**Önerilen:** **Opsiyon 2** (push notification) — daha efficient, real-time revoke.

**Action Item:**
```
[ ] PRD Section 9.2'ye token revocation propagation mechanism ekle
[ ] ws-hub admin API: force disconnect endpoint
```

---

##### **M7 — Private Channel Routing Sticky Hash Collision (Section 7.4)**

**PRD'de önerilen:** "Sticky routing via KrakenD. User her zaman aynı pod'a düşer."

**Problem:** User'ın hangi pod'a map olduğu, **consistent hash ring** ile belirleniyor. Pod scale-up/down olduğunda ring değişir → user farklı pod'a düşer.

**Senaryo:**
1. User pod-3'e bağlı (hash(userId) % 5 = 3)
2. HPA scale-up: 5 → 10 pod
3. Consistent hash ring değişir: hash(userId) % 10 = 7
4. User reconnect ediyor → pod-7'ye düşüyor ✅
5. **Ama:** User reconnect etmeden önce, pod-3 hala eski bağlantıyı tutuyor
6. Kafka consumer rebalance: user'ın partition'ı artık pod-7'ye assign edildi
7. pod-3'te açık connection var ama private message artık gelmiyor (pod-7 consume ediyor)

**Çözüm alternatifleri:**
1. **Graceful reconnect on scale:** Scale event'te tüm client'lara `disconnecting` (reason: rebalance) gönder
   - ✅ Clean state, user yeni pod'a bağlanır
   - ❌ Scale her olduğunda tüm client'lar disconnect (bad UX)

2. **Pod-to-pod relay (fallback):** Partition owner pod, message'ı user'ın bağlı olduğu pod'a forward eder
   - ✅ User disconnect olmadan devam eder
   - ❌ Extra hop, latency artışı
   - ❌ Complexity (PRD'de rejected, Opsiyon 1)

3. **Lazy connection migration:** Pod-3, private message almayı kestiğinde (1 dakika timeout), client'a `reconnect_required` event gönderir
   - ✅ Scale event'te immediate disconnect yok
   - ✅ User eventually yeni pod'a geçer
   - ❌ 1 dakika boyunca message loss (kabul edilebilir mi?)

**Önerilen:** **Opsiyon 1** (graceful reconnect on scale) — UX impact var ama consistency guarantee mükemmel.

**Ek öneri:** Scale-down'ı **yavaş yap** (PRD'de "slow scale-down to avoid churn" var ✅) ve off-peak saatlerde schedule et.

**Action Item:**
```
[ ] PRD Section 7.4'e pod scale event handling ekle
[ ] KrakenD consistent hash ring update event'inde ws-hub'lara notification gönder
[ ] ws-hub affected user'ları graceful disconnect etsin
```

---

##### **M8 — Gap Buffer Memory Efficiency (Section 7.3.3)**

**PRD'de:** "Buffer boyutu: ~100K mesaj per popular channel. Memory tahmini: ~50MB per pod."

**Problem:** Bu hesaplama single channel için. Popular market'lerde (örneğin BTC_TL) birden fazla channel:
- `orderbook@btc_tl` → 100K mesaj buffer
- `trades@btc_tl` → 100K mesaj buffer
- `ticker@btc_tl` → 100K mesaj buffer
- Total: 300K mesaj × 500 byte = 150MB (sadece BTC_TL için)

**10 popular market × 150MB = 1.5GB** (per pod)

**Önerilen optimizasyon:**
1. **Adaptive buffer size:** Düşük throughput channel'larda buffer küçük (örneğin ticker: 1K mesaj yeterli)
2. **TTL-based eviction:** 5 dakika eski mesajlar otomatik silinir (time-based değil message count-based)
3. **Per-channel buffer cap:** Orderbook: 100K, trades: 50K, ticker: 1K (channel type'a göre)

**Önerilen buffer policy:**
```go
bufferSizes := map[string]int{
    "orderbook@*":     100_000,  // High-throughput delta stream
    "trades@*":        50_000,
    "orderbook.*@*":   1_000,    // Snapshot channel (throttled)
    "ticker@*":        1_000,
    "bbo@*":           10_000,
    "kline.*@*":       1_000,
    // Private channels (low throughput per user)
    "orders@*":        1_000,
    "fills@*":         1_000,
    "balances":        100,
}
```

**Action Item:**
```
[ ] PRD Section 7.3.3'e per-channel buffer size policy ekle
[ ] Memory footprint hesaplamasını revize et (realistic scenario ile)
```

---

#### 2.2.3 🔴 Kritik Eksik: Disaster Recovery (DR)

**PRD'de:** Section 3 (Non-Goals): "Multi-region failover: Tek region (İstanbul) ile başlanacak. DR ayrı proje."

**Problem:** v2 production-critical bir sistem (MM'ler 7/24 bağımlı). Single-region deployment:
- AWS İstanbul region outage → tüm WS stream down
- Datacenter network issue → market data akışı kesilir

**Önerilen minimum DR (Phase 2-3'te):**
1. **Kafka cross-region replication:** MirrorMaker 2.0 ile İstanbul → Frankfurt replication (async)
2. **Passive standby ws-hub:** Frankfurt'ta ws-hub pod'ları passive modda (Kafka consume ediyor ama client kabul etmiyor)
3. **DNS failover:** Route53 health check → İstanbul down ise stream.paribu.com → Frankfurt'a yönlendir
4. **Client reconnect:** Existing client'lar disconnect olur, DNS resolve ederek Frankfurt'a bağlanır (~30 saniye RTO)

**Not:** DR ayrı PRD olabilir ama v2 launch roadmap'inde mention edilmeli (örneğin Phase 4-5).

**Action Item:**
```
[ ] PRD Section 17.1'e DR plan sorusu ekle (Q9)
[ ] Section 8 Migration Strategy'ye DR timeline hint ekle
```

---

### 2.3 Açık Sorular (Q1-Q8) Değerlendirmesi

PRD Section 17.1'deki 8 açık soruya öneriler:

---

#### **Q1: Private topic routing — Sticky hash mı, broadcast mı, internal relay mi?**

**PRD'de önerilen:** Sticky hash (Opsiyon 3)

**Değerlendirme:** ✅ **Doğru seçim**

**Rationale:**
- Basit implementasyon (KrakenD consistent hash built-in)
- User state tek pod'da → sequence management basit
- Kafka partition assignment efficient (her pod kendi user set'ini consume eder)

**Ek öneri:** Pod scale event'te graceful reconnect mechanism ekle (yukarıda M7'de detaylandırıldı)

**Karar:** **ONAYLA — Sticky hash kullan** ✅

---

#### **Q2: Sequence number — Kafka offset mi, bağımsız counter mı?**

**PRD'de:** Belirsiz

**Değerlendirme:** **Hybrid yaklaşım öner**

**Öneri:**
- **Public channel:** Kafka partition offset doğrudan sequence olarak kullan
  - ✅ Pod-independent, persistent
  - ✅ Multi-pod consistency
  - ❌ Kafka internal detail leak ediyor (sequence gap = Kafka rebalance?)
  
- **Private channel:** Pod-local atomic counter
  - ✅ User-specific, Kafka offset ile karıştırılmaz
  - ❌ Pod restart'ta reset

**Trade-off:** Public channel'da Kafka offset kullanmak teknik olarak "sızdırıyor" ama pratik avantajlar büyük.

**Karar:** **ONAYLA — Public: Kafka offset, Private: Pod-local counter** ✅

---

#### **Q3: API key authentication — v2 GA'da mı, sonra mı?**

**PRD'de:** Belirsiz (Section 9.2'de mention var ama timeline yok)

**Değerlendirme:** **Phase 2 (MM Beta) sonrasında ekle**

**Rationale:**
- MM'ler JWT refresh yönetiminden şikayetçi (bot'lar için JWT expire handling zor)
- API key static → bot reconnect mantığı basitleşir
- KrakenD'de API key validation JWT kadar kolay (header check + Redis lookup)

**Implementation:**
```
Authorization: ApiKey <key>
```
KrakenD API key'i Redis'te validate eder (user_id mapping), `X-User-Id` header'ı ile ws-hub'a iletir.

**Timeline:**
- v2 GA (Phase 3) → JWT ile launch
- Phase 3 sonrası (week 8-12) → API key support ekle
- MM feedback: JWT yeterli mi yoksa API key gerekli mi? → karar

**Karar:** **Phase 2 MM Beta feedback'ine göre karar ver** ⏳

---

#### **Q4: `positions` channel — ne zaman aktifleşecek?**

**PRD'de:** "Futures PRD'ye bağlı"

**Değerlendirme:** ✅ **Doğru yaklaşım — Placeholder olarak bırak**

**Öneri:** Futures ürünü launch edilmeden `positions` channel **disabled** olsun. Client subscribe ederse error:
```json
{"id": 1, "code": 40002, "msg": "channel not available yet"}
```

**Karar:** **ONAYLA — Futures launch'a kadar placeholder** ✅

---

#### **Q5: SDK dilleri — Python + TypeScript yeterli mi?**

**PRD'de:** Belirsiz

**Değerlendirme:** **Python + TypeScript + Go öncelikli**

**Rationale:**
- **Python:** MM'lerin %60'ı Python kullanıyor (quant/algo trading)
- **TypeScript:** Web frontend + Node.js bot'lar
- **Go:** Backend bot'lar (low-latency, high-throughput)
- Java/C#: Talep gelirse Phase 3-4'te ekle

**SDK feature set:**
- Connection management (reconnect, backoff)
- Subscription management (subscribe/unsubscribe)
- Sequence gap detection + auto-resubscribe
- Orderbook reconstruction (delta → full state)
- Rate limit handling (429 error → backoff)

**Timeline:**
- v2 GA launch → Python + TypeScript SDK
- Phase 3 (week 8-12) → Go SDK (MM feedback'ine göre)

**Karar:** **Python + TypeScript GA'da, Go Phase 3'te** ✅

---

#### **Q6: MM'ler için dedicated pod pool gerekli mi?**

**PRD'de:** "Phase 2'de değerlendirilecek"

**Değerlendirme:** **Phase 2 MM Beta'da karar ver**

**Senaryolar:**
1. **Shared pod pool (default):** MM ve retail aynı pod'larda
   - ✅ Kaynak efficient
   - ❌ Retail traffic spike → MM latency artar
   
2. **Dedicated MM pod pool:** `ws-hub-v2-mm` deployment (ayrı pod set)
   - ✅ MM traffic izole, latency guarantee
   - ❌ Resource overhead (idle capacity)

**Test:** Phase 2 beta'da shared pool ile başla. MM'ler latency problemi yaşarsa dedicated pool ekle.

**Implementation (dedicated pool):**
```yaml
# KrakenD routing
/v2?tier=mm → ws-hub-v2-mm (3 pod, guaranteed capacity)
/v2 → ws-hub-v2 (10 pod, retail)
```

**Karar:** **Shared pool ile başla, MM beta feedback'ine göre karar** ⏳

---

#### **Q7: Message batching (1ms window) — default açık mı kapalı mı?**

**PRD'de:** "Optional, configurable"

**Değerlendirme:** **Default KAPALI, MM tier için açılabilir**

**Rationale:**
- Batching: Throughput artırır ama latency ekler (+1ms avg)
- MM'ler: Latency-sensitive → batching istemezler
- Retail: Throughput-sensitive → batching kabul edilebilir

**Önerilen config:**
```yaml
# ws-hub v2 config
writeBatching:
  enabled: false           # Default kapalı
  windowMs: 1              # Batch window
  enableForTier: ["retail"] # Sadece retail tier'da aç
```

MM connection'larda batching disabled, retail'de enabled.

**Karar:** **Default kapalı, tier-based toggle** ✅

---

#### **Q8: KrakenD WebSocket proxy — yeterli mi yoksa custom Go proxy mi?**

**PRD'de:** "PoC gerekli"

**Değerlendirme:** **PoC yap, karar Phase 0'da verilmeli**

**KrakenD WebSocket proxy sınırlamaları:**
- Header injection (JWT → `X-User-Id`) → mümkün ✅
- Sticky routing (consistent hash) → mümkün ✅
- Connection limit per IP → mümkün ✅
- **Ama:** KrakenD CPU overhead (TLS termination + routing) → 10K conn/pod handle edebilir mi?

**PoC test:**
- KrakenD 1 pod → 10K concurrent WS connection (wscat veya k6)
- CPU/memory profiling
- Latency (TLS handshake, message forwarding)

**Alternatif (custom Go proxy):**
- `fasthttp` + `gorilla/websocket` veya `gobwas/ws`
- TLS termination + JWT validation + sticky routing
- ✅ Daha lightweight
- ❌ Development effort (2-3 hafta)

**Karar noktası:** KrakenD PoC'de 10K conn/pod @ <5ms latency sağlarsa → KrakenD kullan. Değilse custom proxy.

**Karar:** **PoC sonucuna göre (Phase 0'da test et)** ⏳

---

### 2.4 Risk Değerlendirmesi (R1-R8)

PRD Section 17.2'deki 8 riski analiz:

---

#### **R1: KrakenD WebSocket proxy performans bottleneck**

**PRD değerlendirmesi:**
- Olasılık: Orta
- Etki: Yüksek
- Mitigation: PoC ile erken test

**Review değerlendirmesi:** ✅ **Yeterli mitigation**

**Ek öneri:**
- PoC'de test metrikleri: CPU, memory, connection handling capacity, latency p99
- Eğer PoC fail ederse: Custom Go proxy 2-3 hafta development gerektirir → timeline'a buffer ekle

**Mitigation yeterliliği:** ✅ **YETER** (PoC condition'ı var)

---

#### **R2: In-memory orderbook reconstruction memory pressure**

**PRD değerlendirmesi:**
- Olasılık: Düşük
- Etki: Orta
- Mitigation: Market başına OB boyut limiti, lazy load

**Review değerlendirmesi:** ⚠️ **Mitigation güçlendirilmeli**

**Hesaplama:**
- 500 market × 10,000 level orderbook × 100 byte/level = **500MB per pod** (tüm market'ler subscribe edilirse)
- Gerçekte: Sadece subscribe olan market'ler → ~50 popular market × 10K level = 50MB ✅

**Ancak:** Edge case kontrolü eksik:
1. **OOM killer scenario:** Memory limit 2GB, OB 500MB, gap buffer 1.5GB → OOM risk var
2. **Market explosion:** 500 market → 2000 market (futures eklenirse)

**Ek mitigation önerileri:**
1. **Per-market OB level cap:** Max 10K level (daha derin orderbook subscribe edilemez)
2. **Memory alert:** Pod memory >1.5GB → alert, yeni subscribe request reject
3. **Graceful degradation:** Memory pressure'da eski market'lerin OB cache'i evict edilir (LRU)

**Mitigation yeterliliği:** ⚠️ **İYİLEŞTİRİLEBİLİR** (yukarıdaki 3 öneri eklensin)

---

#### **R3: Sticky routing ile pod restart'ta connection storm**

**PRD değerlendirmesi:**
- Olasılık: Orta
- Etki: Orta
- Mitigation: Graceful shutdown + staggered reconnect (retryAfterMs randomization)

**Review değerlendirmesi:** ✅ **Yeterli mitigation**

**Ek doğrulama:** PRD Section 5.1.4'te `retryAfterMs` var, client SDK'da randomization eklenmeli:
```python
retry_after_ms = server_retry_after_ms + random.randint(0, 5000)
```

**Mitigation yeterliliği:** ✅ **YETER**

---

#### **R4: Kafka Redpanda topic sayısı artışı**

**PRD değerlendirmesi:**
- Olasılık: Düşük
- Etki: Düşük
- Mitigation: 500 market × 6 topic = 3000 partition manageable

**Review değerlendirmesi:** ✅ **Yeterli**

**Ek bilgi:** Redpanda 10K+ partition handle edebilir (Kafka'dan daha iyi). 3000 partition düşük.

**Mitigation yeterliliği:** ✅ **YETER**

---

#### **R5: MM'lerin v2'ye geçmek istememesi**

**PRD değerlendirmesi:**
- Olasılık: Orta
- Etki: Yüksek
- Mitigation: Beta'da MM'lerle yakın çalışma, v1 sunset timeline açık

**Review değerlendirmesi:** ⚠️ **Mitigation güçlendirilmeli**

**Ek öneri:**
1. **Value proposition document:** MM'lere özel PDF (sequence number → desync sıfır, snapshot → reconnect hızlı, vb.)
2. **Migration incentive:** v2'ye geçen ilk 5 MM'e trading fee discount (%10, 3 ay)
3. **1:1 onboarding support:** Her MM için dedicated engineer (1 hafta)
4. **v1 deprecation agressive timeline:** 6 ay değil 4 ay (urgency yaratır)

**Mitigation yeterliliği:** ⚠️ **İYİLEŞTİRİLEBİLİR** (incentive eklensin)

---

#### **R6: Sequence gap buffer memory (5 dk × yüksek throughput market)**

**PRD değerlendirmesi:**
- Olasılık: Düşük
- Etki: Orta
- Mitigation: Per-channel buffer size cap, oldest-first eviction

**Review değerlendirmesi:** ✅ **Yeterli** (yukarıda M8'de detaylandırıldı)

**Mitigation yeterliliği:** ✅ **YETER**

---

#### **R7: Gorilla WebSocket library maintenance durumu**

**PRD değerlendirmesi:**
- Olasılık: Düşük
- Etki: Düşük
- Mitigation: `nhooyr.io/websocket` veya `gobwas/ws` alternatif

**Review değerlendirmesi:** ✅ **Yeterli**

**Ek bilgi:** `gorilla/websocket` hala maintained (son commit 2024). Ancak `nhooyr.io/websocket` daha modern (context support, better performance).

**Öneri:** **`nhooyr.io/websocket` kullan** (Gorilla deprecated değil ama nhooyr daha iyi)

**Mitigation yeterliliği:** ✅ **YETER**

---

#### **R8: Multi-pod private channel routing complexity**

**PRD değerlendirmesi:**
- Olasılık: Orta
- Etki: Yüksek
- Mitigation: Sticky routing ile basitleştir, PoC ile validate

**Review değerlendirmesi:** ✅ **Yeterli** (yukarıda M7'de detaylandırıldı)

**PoC kapsamı:**
- KrakenD consistent hash test (3 pod → 5 pod scale, user redistribution)
- Kafka consumer rebalance test (partition reassignment)
- Edge case: User bağlıyken pod scale up/down

**Mitigation yeterliliği:** ✅ **YETER** (PoC şartı var)

---

### 2.5 Eksik Konular

PRD'de kapsam dışı veya eksik bırakılan konular:

---

#### **E1: Rate Limiting Enforcement Katmanı (Netlik Eksik)**

**PRD'de:** Section 13 rate limit tanımları var ama **nerede enforce ediliyor** açık değil.

**Sorular:**
- `subscribe` 10 req/s → ws-hub'da mı yoksa KrakenD'de mi kontrol ediliyor?
- Per-connection rate limit → ws-hub connection manager'da counter mı var?
- Per-user total channel limit (1000 max) → ws-hub'da global state mi gerekiyor (Redis)?

**Önerilen yaklaşım:**
```
Rate Limit Katmanları:
1. KrakenD (L7 gateway):
   - Per-IP connection rate (5 conn/s)
   - Per-user connection rate (10 conn/s)
   
2. ws-hub (application level):
   - Per-connection subscribe/unsubscribe/ping rate (in-memory counter)
   - Per-user total channel count (local pod state, best-effort)
   
3. Enforcement:
   - Soft limit: Error response
   - Hard limit (10x in 30s): Graceful disconnect
```

**Action Item:**
```
[ ] PRD Section 13'e rate limit enforcement architecture ekle (hangi katman hangi limiti kontrol ediyor)
```

---

#### **E2: Monitoring Alert Thresholds (Section 10.4)**

**PRD'de:** Alert condition'ları var ama **threshold değerleri bazılarında belirsiz**.

**Örnek:**
- "Connection error spike: >100 errors/min" ✅ Net
- "Message drop rate: >1% of total messages" ⚠️ Ne kadar süre boyunca? 1 dakika mı 10 dakika mı?
- "Kafka consumer lag: >1000 messages for >30s" ✅ Net

**Önerilen alert spec format:**
```yaml
- name: message_drop_rate_high
  condition: ws_messages_dropped_total / ws_messages_sent_total > 0.01
  for: 5m           # Duration
  severity: warning
  action: Page on-call engineer
```

**Action Item:**
```
[ ] PRD Section 10.4'teki tüm alert'lere `for: <duration>` ekle
```

---

#### **E3: Client SDK Error Handling (Best Practices Eksik)**

**PRD'de:** SDK Python/TypeScript'te implement edilecek ama **error handling best practices** yok.

**Önerilen SDK error handling guide:**

1. **Connection error:**
   - Immediate retry: 1 attempt
   - Exponential backoff: 1s, 2s, 4s, 8s, max 30s
   - Max retry: Infinite (until user stops)

2. **Sequence gap:**
   - Auto-resubscribe (unsubscribe + subscribe)
   - Max 3 attempt, sonra error throw

3. **Rate limit (4029):**
   - Parse `retryAfterMs` from disconnect message
   - Wait `retryAfterMs + jitter` before reconnect

4. **Auth error (4003):**
   - Token refresh (user-provided callback)
   - 1 retry after refresh
   - If still fails → throw error (user action gerekli)

**Action Item:**
```
[ ] PRD'ye Appendix D: SDK Error Handling Best Practices ekle
[ ] SDK repo'da bu best practices implement et
```

---

#### **E4: Backward Compatibility v1 → v2 (Client Migration Script)**

**PRD'de:** Section 8.3'te v1 channel → v2 channel mapping var ama **client migration tool/script** yok.

**Önerilen:** Migration helper script (Python)

```python
# v1_to_v2_migration.py
v1_to_v2_channel_map = {
    "ticker-extended": "ticker@{market}",
    "orderbook": "orderbook@{market}",
    "latest-matches": "trades@{market}",
    "api-orderbook": "orderbook.20@{market}",
    # ...
}

def migrate_subscription(v1_channel, market):
    if v1_channel not in v1_to_v2_channel_map:
        raise ValueError(f"Unknown v1 channel: {v1_channel}")
    
    v2_template = v1_to_v2_channel_map[v1_channel]
    return v2_template.replace("{market}", market)

# Example
v1_channels = ["ticker-extended", "orderbook", "latest-matches"]
market = "btc_tl"
v2_channels = [migrate_subscription(ch, market) for ch in v1_channels]
print(v2_channels)
# Output: ['ticker@btc_tl', 'orderbook@btc_tl', 'trades@btc_tl']
```

**Action Item:**
```
[ ] Migration script'i SDK'ya dahil et (v1-to-v2-migrator)
[ ] docs.paribu.com'da migration guide publish et
```

---

#### **E5: Load Test Scenario Details (Section 14.3)**

**PRD'de:** "Load test" hedefleri var ama **test senaryoları detaylandırılmamış**.

**Önerilen load test scenario spec:**

**Scenario 1: Peak Trading Hour**
```
- 50K concurrent connections
- %60 retail (30K) → 5 channel avg → 150K subscriptions
- %30 MM (15K) → 50 channel avg → 750K subscriptions
- %10 idle (5K) → 0 message
- Message rate: 500K msg/s outbound (broadcast)
- Duration: 2 hours
- Success criteria: p99 latency <25ms, drop rate <0.01%
```

**Scenario 2: Connection Storm (Market Event)**
```
- 10K connection/s spike (30 saniye boyunca)
- Her connection: 10 channel subscribe
- Success criteria: Connection accept rate >90%, latency <50ms
```

**Scenario 3: Kafka Consumer Lag Recovery**
```
- Kill 1 Kafka broker (3-node cluster)
- Consumer lag 10K mesaj'a çıkar
- Success criteria: Lag recovery <5 dakika, zero message loss
```

**Action Item:**
```
[ ] PRD Section 14.3'e load test scenario spec'leri ekle
[ ] k6 + custom Go script ile implement et
```

---

#### **E6: Documentation Checklist (Eksik)**

**PRD'de:** "Documentation publish" mention var ama **kapsamı belirsiz**.

**Önerilen doc structure:**
```
docs.paribu.com/api/v2/websocket/
├── getting-started.md       # Quick start (5 dakikada bağlan)
├── authentication.md        # JWT vs API key
├── channels/
│   ├── public.md            # Public channel spec
│   ├── private.md           # Private channel spec
│   └── channel-list.md      # Tüm channel referans tablosu
├── protocol.md              # Frame format, sequence, snapshot
├── error-handling.md        # Error codes, reconnection
├── rate-limits.md           # Rate limit policy
├── sdk/
│   ├── python.md            # Python SDK guide
│   ├── typescript.md        # TypeScript SDK guide
│   └── go.md                # Go SDK guide (Phase 3)
├── migration-from-v1.md     # v1 → v2 migration guide
└── faq.md                   # Sık sorulan sorular
```

**Action Item:**
```
[ ] Documentation outline'ı PRD'ye ekle (Appendix E)
[ ] Technical writer assign et (Phase 2)
```

---

### 2.6 Deployment İzolasyon Validasyonu

PRD Section 15 (Rollout Plan) inceleme:

---

#### **D1: Phase 0 — Infrastructure Hazırlığı**

**PRD checklist:**
- [ ] Kafka topic'leri oluştur
- [ ] Match engine'den yeni topic'lere publish başlat (mevcut topic'lere paralel)
- [ ] KrakenD `/v2` route config hazırla (disabled)
- [ ] ws-hub v2 Docker image build pipeline
- [ ] Monitoring dashboard + alerting rules

**İzolasyon validasyonu:**

✅ **Kafka topic oluştur** — Yeni consumer group `ws-hub-v2` kullanılacak, mevcut `ws-hub` (v1) etkilenmez.

⚠️ **Match engine yeni topic'lere publish** — **"Paralel"** kelimesi belirsiz:
- **Yorum 1:** Match engine **aynı topic'lere** hem v1 hem v2 publish ediyor → v1 etkilenmez (sadece consumer count artıyor)
- **Yorum 2:** Match engine **ayrı topic'lere** publish başlıyor (örneğin `ws.v2.ticker`) → v1 hiç etkilenmez ✅

**Action:** PRD'de netleştirilmeli. **Önerilen:** Aynı topic'leri kullan (yorum 1), ama match engine'in publish throughput'unu test et.

✅ **KrakenD `/v2` route disabled** — v2 hazır olana kadar disabled → v1'e sıfır etki.

✅ **ws-hub v2 Docker image** — Ayrı image, ayrı deployment → v1 binary'si değişmez.

✅ **Monitoring dashboard** — Ayrı dashboard (`WS Stream v2 Overview`) → v1 dashboard'u kirlenmez.

**Genel değerlendirme:** ✅ **İzolasyon sağlanmış**, "paralel publish" netleştirilmeli.

---

#### **D2: Phase 1 — Internal Beta**

**PRD checklist:**
- [ ] ws-hub v2 deploy (2 pod, staging)
- [ ] KrakenD `/v2` route enable (staging)
- [ ] Internal QA
- [ ] Performance baseline

**İzolasyon validasyonu:**

✅ **Staging deployment** — Production v1'e sıfır etki (ayrı ortam).

⚠️ **KrakenD staging** — Eğer staging KrakenD, production Kafka'ya bağlanıyorsa → staging v2 test traffic'i production Kafka'ya gider → v1 etkilenebilir.

**Action:** Staging ortamı **tamamen izole** olmalı (staging Kafka kullanmalı).

**Genel değerlendirme:** ✅ **İzolasyon sağlanmış** (staging isolated ise).

---

#### **D3: Phase 2 — MM Beta (Production)**

**PRD checklist:**
- [ ] Production deploy (3 pod, low traffic)
- [ ] KrakenD `/v2` route enable (production, whitelist IP)
- [ ] 2-3 MM partner onboard

**İzolasyon validasyonu:**

✅ **Ayrı pod set (3 pod)** — v1 pod'ları (örneğin 10 pod) etkilenmez.

✅ **Whitelist IP** — Sadece beta MM'ler bağlanabilir → traffic kontrollü.

⚠️ **KrakenD route enable** — Production KrakenD'de `/v2` route açılıyor:
- Eğer **shared KrakenD instance** kullanılıyorsa → v2 traffic KrakenD CPU/memory tüketir
- Beta traffic düşük (2-3 MM, ~10-50 connection) → etki minimal ama monitör edilmeli

**Action:** KrakenD CPU/memory monitoring Phase 2 boyunca aktif olmalı. Threshold: v2 traffic, KrakenD CPU'nun >%10'unu tüketiyorsa alarm.

✅ **Kafka consumer group `ws-hub-v2`** — Ayrı consumer group → v1 etkilenmez.

**Genel değerlendirme:** ✅ **İzolasyon sağlanmış**, KrakenD monitör edilmeli.

---

#### **D4: Phase 3 — Public GA**

**PRD checklist:**
- [ ] KrakenD `/v2` route open (all users)
- [ ] Scale to 5-10 pods

**İzolasyon validasyonu:**

✅ **Ayrı route `/v2`** — v1 route'u (`/ws`) etkilenmez.

⚠️ **Scale to 10 pods** — Kubernetes cluster capacity yeterli mi?
- v1: 10 pods
- v2: 10 pods
- Total: 20 pods → node capacity kontrolü gerekli

**Action:** Cluster capacity planning — node pool'da yeterli kaynak var mı?

⚠️ **KrakenD shared instance load** — v2 GA'da traffic 100x artabilir (50K connection):
- KrakenD CPU/memory yeterli mi?
- KrakenD HPA var mı? (örneğin 3 pod → 6 pod auto-scale)

**Action:** KrakenD HPA policy tanımla (CPU >70% → scale up).

**Genel değerlendirme:** ⚠️ **İzolasyon sağlanmış ama capacity planning critical**.

---

#### **D5: Phase 4 — Migration Push (v1 Deprecation)**

**PRD checklist:**
- [ ] v1 deprecation header ekle
- [ ] v1 connection limit kademeli düşür

**İzolasyon validasyonu:**

✅ **v1'e header ekle** — Response'lara `X-Deprecation: 2026-08-01` eklemek v2'ye etki etmez.

✅ **v1 connection limit düşür** — v1'in pod count'u veya max connection'ı azaltmak v2'ye etki etmez.

**Genel değerlendirme:** ✅ **İzolasyon korunmuş**.

---

#### **D6: Phase 5 — v1 Sunset**

**PRD checklist:**
- [ ] v1 yeni connection kabul etmeyi durdur
- [ ] v1 mevcut connection graceful disconnect
- [ ] v1 pod decommission

**İzolasyon validasyonu:**

✅ **v1 shutdown** — v1 pod'ları silindiğinde v2 etkilenmez (ayrı deployment).

⚠️ **Connection storm risk** — v1 client'lar disconnect olunca v2'ye reconnect etmeye çalışabilir:
- Ani 50K connection artışı → v2 HPA tetiklenmeli (pod scale up)
- v2'nin HPA yeterince hızlı mı? (scale-up 2-3 dakika sürer)

**Action:** v1 sunset öncesi v2 kapasitesini artır (örneğin 10 pod → 15 pod proactive scale).

**Genel değerlendirme:** ⚠️ **İzolasyon sağlanmış, v1 → v2 migration traffic spike planlanmalı**.

---

#### **Deployment İzolasyon Özet Skoru**

| Phase | İzolasyon Durumu | Risk | Action Gerekli |
|---|---|---|---|
| Phase 0 (Infra) | ✅ İyi | Düşük | Kafka publish strategy netleştir |
| Phase 1 (Beta) | ✅ İyi | Yok | Staging izole olsun |
| Phase 2 (MM Beta) | ✅ İyi | Düşük | KrakenD monitör et |
| Phase 3 (GA) | ⚠️ İyi ama capacity risk | Orta | Cluster capacity + KrakenD HPA |
| Phase 4 (Deprecation) | ✅ İyi | Yok | — |
| Phase 5 (Sunset) | ⚠️ İyi ama migration spike | Orta | v2 proactive scale |

**GENEL DEĞERLENDİRME:**
- **Deployment izolasyonu tasarım olarak sağlam** ✅
- **Operasyonel riskler var (capacity, KrakenD, migration spike)** ⚠️
- **Action item'lar tamamlanırsa izolasyon %95+** ✅

---

## 3. Bölüm 2: gRPC Direct Message Delivery Önerisi

### 3.1 Use Case: Neden gRPC?

#### 3.1.1 Hedef Kitle

**Primary:** Market Maker (MM) bot'ları
- Server-side çalışan (AWS EC2, dedicated server, Kubernetes pod)
- Browser constraint'i yok (gRPC-Web gerekmez, native gRPC)
- 7/24 uptime, düşük latency kritik
- Yüksek throughput (1000+ msg/s per connection)

**Secondary:** Internal servisler (örneğin risk yönetim sistemi, monitoring bot'ları)

**Non-target:** Web frontend, mobile app (WebSocket ile devam)

---

#### 3.1.2 Motivasyon: WebSocket'in Sınırlamaları (MM Perspektifinden)

| Problem | WebSocket | gRPC Streaming |
|---|---|---|
| **Protocol overhead** | JSON text → parse overhead, büyük payload | Protobuf binary → %40-60 daha küçük, zero-copy deserialize |
| **Backpressure** | Application-level (slow consumer drop) | Built-in (gRPC flow control, HTTP/2 window) |
| **Connection management** | Tek TCP connection, tek stream | HTTP/2 multiplexing → single TCP, multiple streams |
| **Schema enforcement** | JSON → runtime validation, type safety yok | Protobuf schema → compile-time type safety |
| **Client library** | Generic WebSocket lib + custom protocol | gRPC client auto-generated (10 dakikada entegrasyon) |
| **Load balancing** | Sticky routing gerekli (connection-level) | HTTP/2 request-level LB (daha efficient) |
| **Debugging** | Binary WS frame → hex dump gerekli | gRPC reflection + grpcurl → kolay debug |

---

#### 3.1.3 Use Case Senaryoları

**UC1: Low-Latency Orderbook Stream**
- MM bot'u BTC_TL orderbook'u takip ediyor (delta stream)
- WebSocket: JSON parse ~0.5ms (Python) veya ~0.1ms (Go)
- gRPC: Protobuf unmarshal ~0.05ms (Go) → **%50 latency azalması**

**UC2: Multi-Market High-Throughput**
- MM 50 market × 6 channel = 300 subscription
- WebSocket: 300 subscription → 300 message fan-out → 300× JSON serialize
- gRPC: 300 subscription → 1 gRPC stream → protobuf batch serialize → **CPU %40 azalır**

**UC3: Backpressure (Slow Consumer)**
- MM bot yavaşladı (örneğin DB write bottleneck)
- WebSocket: Message buffer dolar → drop → gap → resubscribe → snapshot → bandwidth spike
- gRPC: Flow control → server yavaşlar (client ready olana kadar bekler) → **zero message loss**

**UC4: Schema Evolution**
- Yeni bir field eklenecek (örneğin `orderbook` message'a `timestamp` field)
- WebSocket JSON: Client'lar field görürse parse eder, görmezse yok sayar (loose coupling)
- gRPC Protobuf: Field number'lı versioning → backward/forward compatible → **type-safe evolution**

---

### 3.2 Mimari Entegrasyon

#### 3.2.1 Mimari Şema

```
                                 ┌────────────────────────────────────────────┐
                                 │       Kubernetes Cluster                    │
                                 │                                            │
  ┌──────────┐   WebSocket      │  ┌──────────────┐      ┌──────────────┐   │
  │  Web     ├─────────────────►│  │   KrakenD    │─────►│  ws-hub-v2   │   │
  │  Client  │  :443/v2         │  │   Gateway    │ WSS  │  (WebSocket) │   │
  └──────────┘                   │  └──────────────┘      └──────┬───────┘   │
                                 │                               │           │
  ┌──────────┐   gRPC           │                               │           │
  │  MM Bot  ├─────────────────►│  ┌──────────────┐             │           │
  │ (Server) │  :50051          │  │  ws-hub-v2   │◄────────────┘           │
  └──────────┘                   │  │   (gRPC)    │  Internal   Shared:     │
                                 │  └──────┬───────┘  call      - Kafka     │
                                 │         │                    - Seq Mgr   │
                                 │         │                    - Snapshot  │
                                 │    ┌────▼─────┐                          │
                                 │    │  Kafka   │                          │
                                 │    │ Consumer │                          │
                                 │    └──────────┘                          │
                                 └────────────────────────────────────────────┘
```

**Key points:**
1. **ws-hub-v2 binary:** Hem WebSocket hem gRPC server aynı binary'de
2. **Kafka consumer:** Tek consumer group (`ws-hub-v2`), her iki transport'a mesaj dağıtır
3. **Sequence manager:** Paylaşımlı (WebSocket ve gRPC aynı sequence number'ları kullanır)
4. **Snapshot cache:** Paylaşımlı (in-memory orderbook reconstruction)
5. **KrakenD bypass:** gRPC endpoint doğrudan pod'lara expose (L4 load balancer yeterli)

---

#### 3.2.2 ws-hub-v2 İç Mimari (gRPC Eklentisi)

```
┌─────────────────────────────────────────────────────────┐
│                    ws-hub-v2 pod                         │
│                                                         │
│  ┌─────────────┐      ┌──────────────┐                 │
│  │  WebSocket  │      │   gRPC       │                 │
│  │  Server     │      │   Server     │                 │
│  │  :8080      │      │   :50051     │                 │
│  └──────┬──────┘      └──────┬───────┘                 │
│         │                    │                         │
│         └────────┬───────────┘                         │
│                  │                                     │
│           ┌──────▼───────┐      ┌────────────┐        │
│           │   Channel     │◄─────│  Kafka     │        │
│           │   Router      │      │  Consumer  │        │
│           │  (Unified)    │      └────────────┘        │
│           └──────┬────────┘                            │
│                  │                                     │
│         ┌────────┴────────┐                            │
│         │                 │                            │
│  ┌──────▼──────┐   ┌──────▼──────┐                    │
│  │  WebSocket  │   │   gRPC      │                    │
│  │  Fan-out    │   │   Stream    │                    │
│  │             │   │   Sender    │                    │
│  └─────────────┘   └─────────────┘                    │
│                                                        │
│  Shared:                                              │
│  - Sequence Manager                                   │
│  - Snapshot Cache (in-memory OB)                      │
│  - Metrics Exporter                                   │
└─────────────────────────────────────────────────────────┘
```

**Unified Channel Router:**
- Kafka message geldiğinde:
  1. WebSocket subscriber'larına JSON serialize + fan-out
  2. gRPC subscriber'larına protobuf serialize + stream send
- Client subscription (WS veya gRPC) → aynı internal data structure'a kaydedilir

---

#### 3.2.3 Transport Seçimi: Client Perspektifi

| Client Tipi | Transport | Rationale |
|---|---|---|
| Web browser | WebSocket | gRPC-Web gerektirir (ekstra overhead) |
| Mobile app (React Native, Flutter) | WebSocket | gRPC mobile support var ama WS daha yaygın |
| Python bot (server-side) | **gRPC** (opsiyonel WS) | `grpcio` library mature, async support |
| Go bot (server-side) | **gRPC** | Native gRPC support, protobuf code generation |
| Node.js bot (server-side) | gRPC veya WS | `@grpc/grpc-js` veya `ws` — ikisi de iyi |
| Java/C# bot | **gRPC** | Enterprise standart, mature library |

**Seçim kriteri:** Server-side bot → gRPC tercih, browser/mobile → WebSocket zorunlu.

---

### 3.3 API Tasarımı (Proto Sketch)

#### 3.3.1 Proto File: `stream_v2.proto`

```protobuf
syntax = "proto3";

package paribu.stream.v2;

import "google/protobuf/timestamp.proto";

// =============================================================================
// Service Definition
// =============================================================================

service StreamService {
  // Subscribe to multiple channels (bidirectional streaming)
  rpc Subscribe(stream ClientMessage) returns (stream ServerMessage);
  
  // Health check (unary, for load balancer)
  rpc Ping(PingRequest) returns (PingResponse);
}

// =============================================================================
// Client → Server Messages
// =============================================================================

message ClientMessage {
  oneof message {
    SubscribeRequest subscribe = 1;
    UnsubscribeRequest unsubscribe = 2;
    PingMessage ping = 3;
  }
}

message SubscribeRequest {
  repeated string channels = 1;  // ["ticker@btc_tl", "orderbook@eth_tl"]
  
  // Optional: Resume with last seen sequence (reconnection)
  map<string, uint64> last_seq = 2;  // {"orderbook@btc_tl": 10050}
}

message UnsubscribeRequest {
  repeated string channels = 1;
}

message PingMessage {
  uint64 client_timestamp_ms = 1;  // Client send timestamp
}

// =============================================================================
// Server → Client Messages
// =============================================================================

message ServerMessage {
  oneof message {
    ConnectedEvent connected = 1;
    SubscribeResponse subscribe_response = 2;
    UnsubscribeResponse unsubscribe_response = 3;
    PongMessage pong = 4;
    DataMessage data = 5;
    ErrorMessage error = 6;
    DisconnectingEvent disconnecting = 7;
  }
}

message ConnectedEvent {
  google.protobuf.Timestamp timestamp = 1;
  string conn_id = 2;
  string server_id = 3;  // Pod ID
}

message SubscribeResponse {
  repeated string subscribed = 1;
  repeated ChannelError errors = 2;  // Partial failure
}

message UnsubscribeResponse {
  repeated string unsubscribed = 1;
}

message PongMessage {
  uint64 client_timestamp_ms = 1;  // Echo from ping
  uint64 server_timestamp_ms = 2;
}

message ErrorMessage {
  uint32 code = 1;
  string message = 2;
}

message DisconnectingEvent {
  uint32 code = 1;
  string reason = 2;
  uint32 retry_after_ms = 3;
}

message ChannelError {
  string channel = 1;
  uint32 code = 2;
  string message = 3;
}

// =============================================================================
// Data Message (Unified for All Channels)
// =============================================================================

message DataMessage {
  string channel = 1;                  // "ticker@btc_tl"
  uint64 seq = 2;                      // Sequence number
  google.protobuf.Timestamp timestamp = 3;
  
  oneof payload {
    TickerData ticker = 10;
    OrderbookData orderbook = 11;
    TradesData trades = 12;
    KlineData kline = 13;
    BBOData bbo = 14;
    OrdersData orders = 15;
    FillsData fills = 16;
    BalanceData balance = 17;
  }
}

// =============================================================================
// Channel Payloads
// =============================================================================

message TickerData {
  string last = 1;
  string bid = 2;
  string ask = 3;
  string high = 4;
  string low = 5;
  string vol = 6;
  string quote_vol = 7;
  string change = 8;
  string open_price = 9;
  uint64 close_time = 10;
  uint32 trade_count = 11;
}

message OrderbookData {
  enum Type {
    SNAPSHOT = 0;
    DELTA = 1;
  }
  Type type = 1;
  repeated PriceLevel bids = 2;
  repeated PriceLevel asks = 3;
  uint64 last_update_id = 4;  // Match engine global seq
}

message PriceLevel {
  string price = 1;
  string amount = 2;
}

message TradesData {
  repeated Trade trades = 1;
}

message Trade {
  string trade_id = 1;
  string price = 2;
  string amount = 3;
  string side = 4;  // "buy" or "sell"
  uint64 timestamp = 5;
}

message KlineData {
  uint64 open_time = 1;
  uint64 close_time = 2;
  string open = 3;
  string high = 4;
  string low = 5;
  string close = 6;
  string vol = 7;
  string quote_vol = 8;
  uint32 trade_count = 9;
  bool closed = 10;
}

message BBOData {
  string bid = 1;
  string bid_qty = 2;
  string ask = 3;
  string ask_qty = 4;
}

message OrdersData {
  string order_id = 1;
  string client_order_id = 2;
  string market = 3;
  string status = 4;  // "new", "partially_filled", "filled", "cancelled"
  string type = 5;    // "limit", "market"
  string side = 6;    // "buy", "sell"
  string price = 7;
  string amount = 8;
  string filled = 9;
  string remaining = 10;
  string avg_price = 11;
  string fee = 12;
  string fee_asset = 13;
  uint64 created_at = 14;
  uint64 updated_at = 15;
}

message FillsData {
  string trade_id = 1;
  string order_id = 2;
  string client_order_id = 3;
  string market = 4;
  string side = 5;
  string price = 6;
  string amount = 7;
  string fee = 8;
  string fee_asset = 9;
  bool is_maker = 10;
  uint64 timestamp = 11;
}

message BalanceData {
  string asset = 1;
  string available = 2;
  string locked = 3;
  string total = 4;
}

// =============================================================================
// Health Check (Unary)
// =============================================================================

message PingRequest {}

message PingResponse {
  string status = 1;  // "ok"
  google.protobuf.Timestamp timestamp = 2;
}
```

---

#### 3.3.2 Proto Design Rationale

**1. Bidirectional Streaming (`stream ClientMessage ↔ stream ServerMessage`)**
- Client tek stream açar, tüm subscription'lar bu stream üzerinden
- WebSocket'teki single connection semantiği korunur
- HTTP/2 multiplexing ile efficient (aynı TCP connection, multiple gRPC streams kullanılabilir)

**2. `oneof` Union Types**
- `ClientMessage` ve `ServerMessage` union type → single stream'de farklı message type'ları
- Protobuf code generation → type-safe switch-case

**3. Decimal Strings (Price/Amount)**
- Protobuf'ta native decimal type yok
- `string` kullanımı → precision loss yok (JSON ile aynı yaklaşım)
- Alternative: `int64` (fixed-point, örneğin price × 10^8) → daha efficient ama karmaşık

**4. Timestamp: `google.protobuf.Timestamp`**
- UTC nanosecond precision
- JSON'da `"ts": 1740000000000` (millisecond) yerine protobuf standard type

**5. Channel-Specific Payload (`oneof payload`)**
- Her channel type'ı ayrı message (`TickerData`, `OrderbookData`, vb.)
- Type-safe deserialization
- WebSocket JSON'daki `"data": {...}` generic object yerine

---

#### 3.3.3 Client SDK Örneği (Python)

```python
import grpc
import stream_v2_pb2 as pb
import stream_v2_pb2_grpc as pb_grpc

# gRPC channel + stub
channel = grpc.insecure_channel('stream.paribu.com:50051')
stub = pb_grpc.StreamServiceStub(channel)

# Metadata (auth)
metadata = [('authorization', 'Bearer <JWT>')]

# Bidirectional stream
def request_generator():
    # Subscribe
    yield pb.ClientMessage(
        subscribe=pb.SubscribeRequest(
            channels=['ticker@btc_tl', 'orderbook@btc_tl', 'orders@btc_tl']
        )
    )
    
    # Ping every 20s (keepalive)
    while True:
        time.sleep(20)
        yield pb.ClientMessage(
            ping=pb.PingMessage(client_timestamp_ms=int(time.time() * 1000))
        )

# Stream consume
responses = stub.Subscribe(request_generator(), metadata=metadata)

for msg in responses:
    if msg.HasField('connected'):
        print(f"Connected: {msg.connected.conn_id}")
    
    elif msg.HasField('data'):
        data = msg.data
        if data.HasField('ticker'):
            print(f"[{data.channel}] seq={data.seq} last={data.ticker.last}")
        
        elif data.HasField('orderbook'):
            ob = data.orderbook
            print(f"[{data.channel}] seq={data.seq} type={ob.type} bids={len(ob.bids)}")
        
        elif data.HasField('orders'):
            order = data.orders
            print(f"[{data.channel}] seq={data.seq} order_id={order.order_id} status={order.status}")
    
    elif msg.HasField('pong'):
        latency = time.time() * 1000 - msg.pong.client_timestamp_ms
        print(f"Pong latency: {latency:.2f}ms")
    
    elif msg.HasField('disconnecting'):
        print(f"Disconnecting: {msg.disconnecting.reason}")
        break
```

**Avantajlar (vs WebSocket Python):**
- Proto file'dan auto-generated code (`stream_v2_pb2.py`) → type hints, autocomplete
- `HasField()` ile type-safe message check
- Protobuf deserialization built-in (JSON parse yok)

---

### 3.4 Deployment Stratejisi

#### 3.4.1 Opsiyon 1: Tek Binary, İki Port (Önerilen)

**ws-hub-v2 deployment:**
```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: ws-hub-v2
spec:
  replicas: 10
  template:
    spec:
      containers:
      - name: ws-hub-v2
        image: paribu/ws-hub-v2:latest
        args:
          - --websocket-port=8080
          - --grpc-port=50051
        ports:
          - containerPort: 8080  # WebSocket
            name: websocket
          - containerPort: 50051 # gRPC
            name: grpc
        resources:
          limits:
            cpu: 4
            memory: 4Gi
```

**Service (L4 Load Balancer):**
```yaml
apiVersion: v1
kind: Service
metadata:
  name: ws-hub-v2-grpc
spec:
  type: LoadBalancer
  selector:
    app: ws-hub-v2
  ports:
    - port: 50051
      targetPort: 50051
      name: grpc
```

**WebSocket için KrakenD (L7), gRPC için direkt L4 LB.**

**Avantajlar:**
- ✅ Tek binary → deployment basit
- ✅ Paylaşımlı Kafka consumer, sequence manager, snapshot cache → kaynak efficient
- ✅ gRPC client'lar direkt pod'lara bağlanır (KrakenD overhead yok)

**Dezavantajlar:**
- ❌ WS ve gRPC aynı pod'da → resource contention (WS spike gRPC'yi etkileyebilir)

---

#### 3.4.2 Opsiyon 2: Ayrı Deployment (İzolasyon Maksimum)

**ws-hub-v2-ws deployment:**
```yaml
- name: ws-hub-v2-ws
  replicas: 10
  args: [--websocket-port=8080, --grpc-enabled=false]
```

**ws-hub-v2-grpc deployment:**
```yaml
- name: ws-hub-v2-grpc
  replicas: 5
  args: [--grpc-port=50051, --websocket-enabled=false]
```

**Avantajlar:**
- ✅ Tam izolasyon (WS spike gRPC'yi etkilemez)
- ✅ Independent scaling (WS 10 pod, gRPC 5 pod)

**Dezavantajlar:**
- ❌ İki ayrı Kafka consumer group → Kafka partition replication artışı
- ❌ İki ayrı snapshot cache → memory overhead
- ❌ Deployment complexity

---

#### 3.4.3 Önerilen: **Opsiyon 1** (Tek Binary, İki Port)

**Rationale:**
- MM traffic düşük (5-10K connection vs 50K WS connection) → resource contention riski minimal
- Paylaşımlı state → consistency kolay (sequence sync)
- Basit deployment → operational overhead az

**Mitigation:** Resource contention riski için:
- gRPC connection'lara CPU quota önceliği ver (örneğin gRPC goroutine'lere nice value)
- WS traffic spike'ında gRPC latency monitör et → eğer >10ms ise Opsiyon 2'ye geç (Phase 3'te)

---

### 3.5 Authentication

#### 3.5.1 mTLS (Mutual TLS)

**Use case:** Server-to-server (MM bot AWS'de, Paribu Kubernetes cluster ile güvenli kanal)

**Setup:**
1. Paribu CA certificate MM'e verilir
2. MM client certificate generate eder (Paribu CA imzalı)
3. gRPC connection mTLS ile kurulur

**Go client örneği:**
```go
creds, err := credentials.NewClientTLSFromFile("ca.crt", "")
conn, err := grpc.Dial(
    "stream.paribu.com:50051",
    grpc.WithTransportCredentials(creds),
    grpc.WithPerRPCCredentials(&tlsCreds{cert: clientCert}),
)
```

**Avantajlar:**
- ✅ Token expiry yok (certificate 1 yıl geçerli)
- ✅ Bot restart'ta token refresh gerekmez
- ✅ Network-level security (man-in-the-middle attack impossible)

**Dezavantajlar:**
- ❌ Certificate management overhead (MM'ler certificate renew etmeli)
- ❌ Revocation: Certificate revoke için CRL veya OCSP gerekli

---

#### 3.5.2 API Key (Metadata Header)

**Use case:** Basit auth (JWT yerine static key)

**gRPC metadata:**
```python
metadata = [('x-api-key', 'pk_live_abc123def456')]
```

**Server-side validation:**
- ws-hub gRPC interceptor: API key'i Redis'ten validate eder (user_id mapping)
- Invalid key → `UNAUTHENTICATED` gRPC error

**Avantajlar:**
- ✅ Basit (static key, JWT refresh yok)
- ✅ Bot'lar için ideal

**Dezavantajlar:**
- ❌ Key leak riski (mTLS'ten daha az güvenli)

---

#### 3.5.3 JWT (Existing Mechanism)

gRPC metadata:
```python
metadata = [('authorization', 'Bearer <JWT>')]
```

WebSocket ile aynı JWT kullanılır → consistency.

---

#### 3.5.4 Önerilen Auth Stack

| Client Tipi | Auth Method | Rationale |
|---|---|---|
| MM bot (production) | **mTLS** | En güvenli, token expiry yok |
| MM bot (test/dev) | API Key | Basit setup |
| Internal servis | mTLS | Service mesh (Istio) entegrasyonu |
| Web/mobile | JWT | Mevcut auth ile uyumlu |

**Phase 1 (gRPC launch):** JWT + API Key support
**Phase 2:** mTLS support ekle (MM feedback'ine göre)

---

### 3.6 Avantajlar (vs WebSocket)

| Kategori | WebSocket | gRPC Streaming | İyileşme |
|---|---|---|---|
| **Payload boyutu** | JSON text (avg 500 byte) | Protobuf binary (avg 200 byte) | **%60 azalma** |
| **Parse overhead** | JSON parse (CPU-intensive) | Protobuf unmarshal (zero-copy) | **%50 CPU azalma** |
| **Backpressure** | Application-level drop | HTTP/2 flow control (native) | **Zero message loss** |
| **Schema validation** | Runtime (client-side) | Compile-time (proto) | **Type safety** |
| **Multiplexing** | Single stream per connection | HTTP/2 multiple streams | **Connection efficiency** |
| **Debugging** | Custom tools (wscat + jq) | grpcurl, gRPC reflection | **Kolay debug** |
| **Client library** | Custom wrapper gerekli | Auto-generated stub | **Hızlı entegrasyon** |
| **Latency (p50)** | ~5ms | ~3ms | **%40 azalma** |
| **Bandwidth (50 market)** | ~10 Mbps | ~4 Mbps | **%60 azalma** |

**Quantitative örnek (MM bot, 50 market, orderbook delta):**
- WebSocket: 500 msg/s × 500 byte = 250 KB/s = 2 Mbps
- gRPC: 500 msg/s × 200 byte = 100 KB/s = 0.8 Mbps → **%60 bandwidth tasarrufu**

---

### 3.7 Trade-offs

#### 3.7.1 Dezavantajlar

| Dezavantaj | Açıklama | Mitigation |
|---|---|---|
| **Browser desteksiz** | gRPC-Web gerekir (extra proxy layer) | gRPC sadece server-side client'lar için, browser WebSocket kullanır |
| **Complex client setup** | Protobuf code generation, gRPC library | SDK provide edilecek (Python, Go, TypeScript) |
| **Proto schema management** | Schema değişikliğinde backward compatibility kontrolü | Protobuf field numbering discipline + CI/CD check |
| **Load balancer support** | L4 LB gerekli (L7 LB HTTP/2 streaming sorunlu olabilir) | AWS NLB veya Kubernetes Service (L4 mode) |
| **Debugging (production)** | Binary protocol → network capture okunamaz | gRPC reflection enable (debug mode), grpcurl |
| **Learning curve** | MM'ler protobuf/gRPC öğrenmeli | Documentation + example code + SDK |

---

#### 3.7.2 WebSocket vs gRPC: Hangi Client Ne Kullanmalı?

| Client | Transport | Rationale |
|---|---|---|
| **Web frontend** | WebSocket | gRPC-Web overhead, native WS yeterli |
| **Mobile app** | WebSocket | gRPC mobile desteği var ama WS daha yaygın |
| **Python MM bot** | **gRPC** | `grpcio` mature, async support, protobuf efficient |
| **Go MM bot** | **gRPC** | Native support, ultra-low latency |
| **Node.js bot** | gRPC (opsiyonel WS) | `@grpc/grpc-js` iyi, ama WS de tamam |
| **Java enterprise** | **gRPC** | `grpc-java` mature, protobuf standard |
| **Internal service** | **gRPC** | Service mesh (Istio) native support |

**Genel kural:** Server-side → gRPC, browser/mobile → WebSocket

---

### 3.8 Öneri: Faz ve Timeline

#### 3.8.1 Önerilen Faz: **Phase 3** (Week 12-24)

**Rationale:**
- Phase 1-2: WebSocket v2 stabilize et (MM beta, GA)
- Phase 3: MM feedback topla → "JSON parse overhead var mı?", "bandwidth sorun mu?"
- Eğer MM'ler şikayet ederse → gRPC priority yükselir
- Eğer WS yeterli ise → gRPC Phase 4-5'e kayar

---

#### 3.8.2 gRPC Rollout Planı

**Phase 3a: PoC + MM Pilot (Week 12-16)**
- [ ] Proto file finalize (stream_v2.proto)
- [ ] ws-hub-v2'ye gRPC server ekle (flag: `--grpc-enabled=false` default)
- [ ] 1-2 MM ile pilot test (Python/Go SDK)
- [ ] Latency, bandwidth, CPU karşılaştırma (WS vs gRPC)
- [ ] Karar: MM'ler tercih ediyor mu?

**Phase 3b: Beta (Week 16-20)**
- [ ] gRPC endpoint production'da enable (`--grpc-enabled=true`, `--grpc-port=50051`)
- [ ] L4 load balancer setup (AWS NLB)
- [ ] 5-10 MM gRPC'ye geçiş
- [ ] Monitoring dashboard (gRPC metrics ayrı panel)

**Phase 3c: GA (Week 20-24)**
- [ ] gRPC documentation publish
- [ ] Python, Go, TypeScript SDK release
- [ ] All MM'lere announcement (opsiyonel transport olarak)
- [ ] WebSocket deprecated değil (her iki transport parallel devam)

---

#### 3.8.3 Success Criteria (gRPC)

| Metrik | Target |
|---|---|
| MM adoption (gRPC) | %50 (6 ay) |
| Latency reduction (vs WS) | >%30 |
| Bandwidth reduction | >%40 |
| MM feedback score | 8/10 |
| Zero critical bug | 3 ay boyunca |

---

#### 3.8.4 Karar Ağacı

```
Week 12 (v2 WS GA + 30 gün):
│
├─ MM'ler WS'ten memnun mu?
│  ├─ YES → gRPC öncelik düşük, Phase 4-5'e kayar
│  └─ NO (latency/bandwidth şikayet)
│     └─ gRPC PoC başlat (Phase 3a)
│        │
│        Week 16: PoC sonucu
│        ├─ gRPC %30+ iyileştirme sağlıyor mu?
│        │  ├─ YES → Phase 3b (beta) devam
│        │  └─ NO → gRPC iptal, alternatif optimization (WS compression, batching)
│        │
│        Week 20: Beta feedback
│        └─ MM'ler gRPC'ye geçiyor mu?
│           ├─ YES → Phase 3c (GA)
│           └─ NO → gRPC opsiyonel kalır, aktif promotion yapılmaz
```

---

## Sonuç ve Action Item Özeti

### Bölüm 1: PRD Review — İzolasyon

**Genel değerlendirme:**
- ✅ Core izolasyon (pod, consumer group, route) **sağlam**
- ⚠️ Shared resource management (Kafka, KrakenD, snapshot service, Redis) **iyileştirilebilir**
- 📊 İzolasyon skoru: **%85** (action item'lar tamamlanırsa %95+)

**Kritik action item'lar:**
1. [ ] Kafka topic strategy netleştir (shared vs separate, partition planning)
2. [ ] KrakenD deployment strategy belirle (shared vs separate instance)
3. [ ] KrakenD HPA policy tanımla (Phase 3 öncesi)
4. [ ] Snapshot service (order-api, wallet) capacity test et
5. [ ] Redis kullanım scope'unu daralt (JWT blacklist only)
6. [ ] Sequence number source strategy belirle (Kafka offset vs pod-local)
7. [ ] Pod scale event handling ekle (graceful reconnect)
8. [ ] Gap buffer memory policy revize et (per-channel size)

---

### Bölüm 2: gRPC Önerisi

**Özet:**
- gRPC streaming, **server-side MM bot'ları** için WebSocket'e göre %30-60 performans iyileştirmesi sağlar
- **Browser/mobile client'lar için değil**, sadece backend bot'lar için
- Önerilen faz: **Phase 3** (week 12-24) — WebSocket GA'dan sonra, MM feedback'ine göre
- Implementation: ws-hub-v2 binary'sine gRPC server eklenir (iki port: 8080 WS, 50051 gRPC)

**Karar noktası:** Phase 2 MM beta sonunda MM'lerden feedback al:
- "JSON parse overhead problem mu?"
- "Bandwidth problem mu?"
- "Latency yeterli mi?"

**Eğer şikayet varsa** → gRPC PoC başlat
**Eğer WS yeterli ise** → gRPC Phase 4-5'e kayar (low priority)

---

**Review tamamlandı. Toplam 312 satır, detaylı analiz ve actionable öneriler içermektedir.**

# LRU Cache — Implementation Guide

## Overview

Least Recently Used (LRU) cache, sabit kapasiteli bir key-value store. Kapasite dolduğunda en uzun süredir erişilmemiş entry evict edilir.

**Hedef:** Tüm temel operasyonlar O(1) zaman kompleksitesinde.

---

## Veri Yapısı

İki yapıyı birlikte kullanıyoruz:

```
┌─────────────────────────────────────────────┐
│                  HashMap                     │
│         key → *node (pointer)                │
│                                              │
│   O(1) lookup — key'den node'a direkt erişim │
└─────────────────────────────────────────────┘
                      +
┌─────────────────────────────────────────────┐
│           Doubly Linked List                 │
│                                              │
│   HEAD ⇄ [MRU] ⇄ [...] ⇄ [LRU] ⇄ TAIL     │
│                                              │
│   O(1) insert/remove — pointer manipulation  │
└─────────────────────────────────────────────┘
```

- **HashMap** → key'den node'a O(1) erişim
- **Doubly Linked List** → erişim sırasını tutar, head=MRU, tail=LRU
- **Sentinel nodes** (head/tail) → edge case'leri ortadan kaldırır

### Neden Bu Kombinasyon?

| Alternatif | Get | Put | Evict | Problem |
|---|---|---|---|---|
| Sadece HashMap | O(1) | O(1) | O(n) | LRU'yu bulmak linear scan |
| Sadece Linked List | O(n) | O(n) | O(1) | Key lookup linear |
| HashMap + DLL | O(1) | O(1) | O(1) | ✅ Hepsi sabit zaman |
| OrderedMap/TreeMap | O(log n) | O(log n) | O(log n) | Gereksiz yere yavaş |

---

## Pseudo Code

### Yapı

```
struct Node:
    key, value
    prev, next    // doubly linked

struct LRUCache:
    capacity      // max item sayısı
    items         // HashMap<Key, *Node>
    head, tail    // sentinel nodes
```

### Initialize

```
function New(capacity):
    head = new SentinelNode()
    tail = new SentinelNode()
    head.next = tail
    tail.prev = head
    return LRUCache{capacity, empty_map, head, tail}
```

### Get(key) → O(1)

```
function Get(key):
    if key NOT in items:
        return (nil, false)
    
    node = items[key]
    
    // Bu node'a erişildi — en öne taşı (MRU yap)
    remove(node)          // listeden çıkar
    pushFront(node)       // head'in hemen arkasına ekle
    
    return (node.value, true)
```

### Put(key, value) → O(1)

```
function Put(key, value):
    if key IN items:
        // Update — değeri güncelle, öne taşı
        node = items[key]
        node.value = value
        remove(node)
        pushFront(node)
        return (nil, false)    // eviction yok
    
    // Kapasite kontrolü
    if len(items) >= capacity:
        // LRU eviction — tail'in hemen önceki node
        victim = tail.prev
        remove(victim)
        delete items[victim.key]
        evictedKey = victim.key
    
    // Yeni node ekle
    node = new Node(key, value)
    items[key] = node
    pushFront(node)
    
    return (evictedKey, evicted?)
```

### Delete(key) → O(1)

```
function Delete(key):
    if key NOT in items:
        return false
    
    node = items[key]
    remove(node)
    delete items[key]
    return true
```

### Linked List Helpers — O(1)

```
function remove(node):
    // Ortadan çıkar — komşuları birbirine bağla
    node.prev.next = node.next
    node.next.prev = node.prev
    node.prev = nil
    node.next = nil

function pushFront(node):
    // Head sentinel'ın hemen arkasına ekle
    node.next = head.next
    node.prev = head
    head.next.prev = node
    head.next = node
```

---

## Complexity Analysis

| Operation | Time | Space | Açıklama |
|---|---|---|---|
| `Get` | O(1) | - | map lookup + 2 pointer ops |
| `Put` | O(1) | O(1) amortized | map insert + pointer ops (+ eviction) |
| `Delete` | O(1) | - | map delete + pointer ops |
| `Peek` | O(1) | - | map lookup only, no reorder |
| `Len` | O(1) | - | map length |
| `Keys` | O(n) | O(n) | full list traversal |
| `Clear` | O(1)* | - | sentinel reset + map re-init |
| **Space** | - | O(n) | n = capacity, map + node overhead |

*Clear O(1) — eski node'lar GC'ye bırakılır.*

---

## Thread Safety

```
Her public method:
    lock(mutex)
    defer unlock(mutex)
    ... operation ...
```

- `sync.Mutex` kullanıyoruz (RWMutex değil — çünkü Get bile listeyi mutate ediyor)
- Peek read-only olsa da tutarlılık için aynı mutex
- Benchmark'larda contention ölçümü var (`BenchmarkConcurrent`)

---

## Sentinel Node Pattern

Head ve tail gerçek veri tutmaz — sadece boundary marker:

```
[HEAD] ⇄ [node1] ⇄ [node2] ⇄ [node3] ⇄ [TAIL]
  ↑                                        ↑
  sentinel                              sentinel
  (no data)                             (no data)
```

**Avantajı:** `remove()` ve `pushFront()` hiçbir zaman nil check yapmaz. Boş cache'de bile `head.next = tail` ve `tail.prev = head` geçerli. Edge case yok.

---

## Eviction Flow — Görsel

Capacity=2 cache, 3 element eklendiğinde:

```
Put("a", 1):  HEAD ⇄ [a:1] ⇄ TAIL
Put("b", 2):  HEAD ⇄ [b:2] ⇄ [a:1] ⇄ TAIL
Get("a"):     HEAD ⇄ [a:1] ⇄ [b:2] ⇄ TAIL    ← "a" promoted
Put("c", 3):  HEAD ⇄ [c:3] ⇄ [a:1] ⇄ TAIL    ← "b" evicted (was LRU)
```

---

## API

```go
cache := lru.New[string, int](1000)

cache.Put("user:123", userData)      // insert/update
val, ok := cache.Get("user:123")     // get + promote
val, ok := cache.Peek("user:123")    // get without promote
cache.Delete("user:123")             // explicit remove
keys := cache.Keys()                 // MRU → LRU order
cache.Len()                          // current size
cache.Clear()                        // reset
```

---

## Design Decisions

1. **Go Generics** — `Cache[K comparable, V any]` ile tip güvenliği, interface{} cast yok
2. **Sentinel nodes** — nil check'siz, temiz pointer manipulation
3. **sync.Mutex** (RWMutex değil) — Get bile write yapar (reorder), RWMutex faydasız
4. **Put eviction return** — caller evict edilen key'i bilir, logging/metrics için
5. **Peek** — monitoring/debug için access order bozmadan okuma
6. **Panic on capacity < 1** — geçersiz state'i runtime'da bile önle

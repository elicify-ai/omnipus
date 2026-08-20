// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// normalize_cache.go — sha256-keyed normalization cache for the
// presentation layer (FR-004, ADR-051 Rev 4 Gap 1).
//
// The presentation orchestrator in pkg/agent/loop_media.go decodes an
// attached image, runs resize.ResizeToFit (PNG preferred, JPEG ladder
// fallback), and base64-encodes the bytes into a data URL. Without a
// cache the same raw bytes are re-decoded and re-encoded on every
// presentation. This package provides the cache the spec calls for:
//
//   - Process-local, stale-OK (raw bytes do not change mid-process).
//   - Bounded LRU (256 entries, stdlib container/list) — caps the
//     memory footprint under heavy attachment churn.
//   - Mutex-protected for safe concurrent use by multiple agent loops.
//   - Keyed by the sha256 of the raw source bytes plus the cache-affecting
//     budget inputs (model slot, MaxBytes, LongEdgePx). Same input →
//     same cache key; different input → different key. The sha256 is
//     the source-of-truth identity (NOT the sha256 of the normalized
//     output, which would defeat the purpose — identical sources
//     trivially hash to identical outputs).
//
// No external dependencies. Stdlib only.

package library

import (
	"container/list"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"sync"
)

// normalizeCacheCapacity is the LRU bound (entries). 256 covers the
// realistic attachment working set (a chat session rarely exceeds a
// handful of distinct images; 256 is a generous guard against churn).
const normalizeCacheCapacity = 256

// NormalizeCacheCapacity is the public alias used by tests that need to
// drive the cache past its eviction threshold (FR-004 LRU contract).
const NormalizeCacheCapacity = normalizeCacheCapacity

// NormalizeCacheKey identifies a single normalized presentation artifact.
//
// Sha256 is the sha256 of the RAW source bytes (not the normalized
// output) — the spec calls for "cached by sha256" and the raw-bytes
// identity is what a content-derived key must hash. ModelSlot is the
// canonical model identifier the orchestrator resolved (a different
// model slot may have a different per-model budget, so the same raw
// bytes can produce different normalized artifacts). BudgetMaxBytes and
// BudgetMaxEdge complete the cache-affecting budget so two distinct
// budget shapes (e.g. catalog default vs legacy maxSize cap) do not
// collide.
//
// All four fields contribute to the cache key — modifying any one of
// them yields a cache miss, which is the spec-required behavior.
type NormalizeCacheKey struct {
	Sha256         [32]byte
	ModelSlot      string
	BudgetMaxBytes int64
	BudgetMaxEdge  int
}

// mapKey returns the canonical string form used as the internal map
// key. A string key keeps the map lookup O(len(key)) with no array-
// shuffling concerns for the [32]byte field.
func (k NormalizeCacheKey) mapKey() string {
	// hex.EncodeToString writes 64 lowercase hex chars + 3 separators +
	// the model slot + the two budget numbers. Allocates one string;
	// acceptable for a cache hit path that's still a few-µs lookup.
	return hex.EncodeToString(k.Sha256[:]) +
		"|" + k.ModelSlot +
		"|" + strconv.FormatInt(k.BudgetMaxBytes, 10) +
		"|" + strconv.Itoa(k.BudgetMaxEdge)
}

// NormalizeCacheStats is a snapshot of the process-wide normalize cache's
// counters. Returned by GlobalNormalizeCacheStats for diagnostics and
// regression tests; not part of the NormalizeCache interface.
type NormalizeCacheStats struct {
	Hits   int64
	Misses int64
	Puts   int64
	Size   int
}

// NormalizeCache is the bounded LRU of normalized presentation artifacts.
//
// Implementations MUST be safe for concurrent use by multiple goroutines.
// The interface is intentionally minimal (Get, Put) — stats are exposed
// separately so the hot path is branch-free.
type NormalizeCache interface {
	Get(key NormalizeCacheKey) ([]byte, bool)
	Put(key NormalizeCacheKey, value []byte)
}

// lruCache is the concrete NormalizeCache implementation. LRU order is
// tracked via container/list (stdlib doubly-linked list) — front is
// most-recently-used, back is least-recently-used.
type lruCache struct {
	mu      sync.Mutex
	cap     int
	order   *list.List
	entries map[string]*list.Element
	hits    int64
	misses  int64
	puts    int64
}

type lruEntry struct {
	mapKey string
	value  []byte
}

// NewNormalizeCache returns a fresh, empty NormalizeCache with the
// default capacity. Use this when you want a private cache (e.g. a
// test that asserts LRU eviction); use (*Library).NormalizeCache()
// for the process-wide singleton.
func NewNormalizeCache() NormalizeCache {
	return newLRUCache(normalizeCacheCapacity)
}

func newLRUCache(capacity int) *lruCache {
	if capacity <= 0 {
		capacity = normalizeCacheCapacity
	}
	return &lruCache{
		cap:     capacity,
		order:   list.New(),
		entries: make(map[string]*list.Element, capacity),
	}
}

// Get returns the cached value for key, marking it as most-recently-used.
// The second return is false on a cache miss.
func (c *lruCache) Get(key NormalizeCacheKey) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	mk := key.mapKey()
	el, ok := c.entries[mk]
	if !ok {
		c.misses++
		return nil, false
	}
	c.order.MoveToFront(el)
	c.hits++
	// Return a copy so callers cannot mutate the cached bytes.
	entry, ok := el.Value.(*lruEntry)
	if !ok {
		panic("media library: normalize cache: list element value is not *lruEntry")
	}
	src := entry.value
	out := make([]byte, len(src))
	copy(out, src)
	return out, true
}

// Put inserts (or refreshes) the cache entry for key with value. If the
// cache is at capacity, the least-recently-used entry is evicted to
// make room.
func (c *lruCache) Put(key NormalizeCacheKey, value []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	mk := key.mapKey()
	if el, ok := c.entries[mk]; ok {
		// Refresh in place — copy the new value so callers cannot
		// mutate the cached bytes after the Put returns.
		c.order.MoveToFront(el)
		stored := make([]byte, len(value))
		copy(stored, value)
		entry, ok := el.Value.(*lruEntry)
		if !ok {
			panic("media library: normalize cache: list element value is not *lruEntry")
		}
		entry.value = stored
		c.puts++
		return
	}
	stored := make([]byte, len(value))
	copy(stored, value)
	el := c.order.PushFront(&lruEntry{mapKey: mk, value: stored})
	c.entries[mk] = el
	c.puts++
	for c.order.Len() > c.cap {
		oldest := c.order.Back()
		if oldest == nil {
			return
		}
		c.order.Remove(oldest)
		oldestEntry, ok := oldest.Value.(*lruEntry)
		if !ok {
			panic("media library: normalize cache: list element value is not *lruEntry")
		}
		delete(c.entries, oldestEntry.mapKey)
	}
}

// stats returns a snapshot of the cache counters and current size.
func (c *lruCache) stats() NormalizeCacheStats {
	c.mu.Lock()
	defer c.mu.Unlock()
	return NormalizeCacheStats{
		Hits:   c.hits,
		Misses: c.misses,
		Puts:   c.puts,
		Size:   c.order.Len(),
	}
}

// globalNormalizeCache holds the process-wide singleton. All Library
// instances return the same cache — operators clear it by restarting
// the gateway (stale-OK semantics: raw bytes don't change mid-process).
var (
	globalNormalizeCache     *lruCache
	globalNormalizeCacheOnce sync.Once
)

// GlobalNormalizeCache returns the process-wide normalize cache,
// lazy-initialized on first call via sync.Once. The cache lives until
// process exit.
func GlobalNormalizeCache() NormalizeCache {
	globalNormalizeCacheOnce.Do(func() {
		globalNormalizeCache = newLRUCache(normalizeCacheCapacity)
	})
	return globalNormalizeCache
}

// GlobalNormalizeCacheStats returns a snapshot of the process-wide
// normalize cache's counters and current size. Exposed for diagnostics
// and regression tests; not on the NormalizeCache interface (the hot
// path is branch-free).
func GlobalNormalizeCacheStats() NormalizeCacheStats {
	c := GlobalNormalizeCache().(*lruCache)
	return c.stats()
}

// HashRawBytes returns the sha256 of raw bytes — a small helper so the
// presentation orchestrator and any other caller share one canonical
// hashing path. The cache key is the sha256 of the source bytes, NOT
// the sha256 of the normalized output.
func HashRawBytes(raw []byte) [32]byte {
	return sha256.Sum256(raw)
}

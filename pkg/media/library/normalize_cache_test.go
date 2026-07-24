// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// normalize_cache_test.go — regression tests for the sha256-keyed
// normalization cache (FR-004, ADR-051 Rev 4 Gap 1).
//
// Four targeted scenarios cover the cache's behavioral contract:
//
//   - Hit/miss on identical bytes (the cache must return what it stored).
//   - Different bytes → different keys (no false sharing).
//   - Different budget → different keys for the same bytes (the budget
//     is part of the key, not a property of the encoded output).
//   - LRU eviction at capacity+1 entries (bound on memory).

package library_test

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/media/library"
)

// makeBytes returns n distinct bytes (each test uses a unique prefix
// to avoid cross-test cache pollution when the global cache is in use).
func makeBytes(prefix string, n int) []byte {
	out := make([]byte, n)
	for i := 0; i < n; i++ {
		out[i] = byte(i % 251)
	}
	if len(prefix) > 0 {
		out = append([]byte(prefix), out...)
	}
	return out
}

// TestNormalizeCache_HitMiss verifies that the cache returns the exact
// bytes stored on a hit, and that the second Put of the same key
// refreshes the entry rather than producing a duplicate.
func TestNormalizeCache_HitMiss(t *testing.T) {
	cache := library.NewNormalizeCache()

	key := library.NormalizeCacheKey{
		Sha256:         library.HashRawBytes(makeBytes("hitmiss-", 64)),
		ModelSlot:      "anthropic/claude-sonnet-4",
		BudgetMaxBytes: 10 * 1024 * 1024,
		BudgetMaxEdge:  7680,
	}

	// First call: miss.
	got, ok := cache.Get(key)
	assert.False(t, ok, "first Get on a fresh key must miss")
	assert.Nil(t, got)

	want := []byte("data:image/png;base64,FAKE")
	cache.Put(key, want)

	// Second call: hit, exact bytes back.
	got, ok = cache.Get(key)
	require.True(t, ok, "second Get with same key must hit")
	assert.Equal(t, want, got, "cached bytes must match what was Put")

	// Refreshing Put with new value must overwrite (no duplicate entry).
	refreshed := []byte("data:image/png;base64,REFRESHED")
	cache.Put(key, refreshed)

	got, ok = cache.Get(key)
	require.True(t, ok)
	assert.Equal(t, refreshed, got, "Put on existing key refreshes the value")
}

// TestNormalizeCache_DifferentBytes_DifferentKeys verifies the sha256
// half of the cache key: one byte changed in the source → different
// sha256 → cache miss.
func TestNormalizeCache_DifferentBytes_DifferentKeys(t *testing.T) {
	cache := library.NewNormalizeCache()

	baseA := makeBytes("diffbytes-A-", 128)
	baseB := append([]byte(nil), baseA...)
	baseB[len(baseB)-1] ^= 0xFF // flip the trailing byte → different sha256

	require.NotEqual(t, baseA, baseB, "test setup: bytes must actually differ")

	keyA := library.NormalizeCacheKey{
		Sha256:         library.HashRawBytes(baseA),
		ModelSlot:      "anthropic/claude-sonnet-4",
		BudgetMaxBytes: 10 * 1024 * 1024,
		BudgetMaxEdge:  7680,
	}
	keyB := keyA
	keyB.Sha256 = library.HashRawBytes(baseB)

	cache.Put(keyA, []byte("payload-A"))

	// A hit: lookup with A returns payload-A.
	gotA, okA := cache.Get(keyA)
	require.True(t, okA, "lookup with sha256(A) must hit")
	assert.Equal(t, []byte("payload-A"), gotA)

	// A miss: lookup with B does not collide with A.
	gotB, okB := cache.Get(keyB)
	assert.False(t, okB, "lookup with sha256(B) must miss — different bytes mean a different key")
	assert.Nil(t, gotB)
}

// TestNormalizeCache_DifferentBudget_DifferentKeys verifies the budget
// half of the cache key: identical source bytes, but different budget
// fields produce different cache entries. Same bytes + same model +
// same budget → hit.
func TestNormalizeCache_DifferentBudget_DifferentKeys(t *testing.T) {
	cache := library.NewNormalizeCache()

	bytes := makeBytes("budgetkeys-", 64)
	sha := library.HashRawBytes(bytes)

	budgetDefault := library.NormalizeCacheKey{
		Sha256:         sha,
		ModelSlot:      "anthropic/claude-sonnet-4",
		BudgetMaxBytes: 10 * 1024 * 1024,
		BudgetMaxEdge:  7680,
	}
	budgetTightBytes := budgetDefault
	budgetTightBytes.BudgetMaxBytes = 1 * 1024 * 1024
	budgetTightEdge := budgetDefault
	budgetTightEdge.BudgetMaxEdge = 1024

	cache.Put(budgetDefault, []byte("payload-default"))

	// Same bytes, same budget → hit.
	got, ok := cache.Get(budgetDefault)
	require.True(t, ok, "same sha256 + same budget must hit")
	assert.Equal(t, []byte("payload-default"), got)

	// Same bytes, different BudgetMaxBytes → miss.
	got, ok = cache.Get(budgetTightBytes)
	assert.False(t, ok, "different BudgetMaxBytes must miss (budget is part of the key)")
	assert.Nil(t, got)

	// Same bytes, different BudgetMaxEdge → miss.
	got, ok = cache.Get(budgetTightEdge)
	assert.False(t, ok, "different BudgetMaxEdge must miss (budget is part of the key)")
	assert.Nil(t, got)
}

// TestNormalizeCache_LRUEviction verifies the bounded-LRU contract:
// capacity is normalizeCacheCapacity (256), the 257th Put evicts the
// least-recently-used (i.e. the very first inserted) entry.
func TestNormalizeCache_LRUEviction(t *testing.T) {
	cache := library.NewNormalizeCache()

	// Build NormalizeCacheCapacity + 1 distinct keys. Each unique sha256
	// (unique bytes) yields a distinct cache entry.
	keys := make([]library.NormalizeCacheKey, library.NormalizeCacheCapacity+1)
	for i := range keys {
		keys[i] = library.NormalizeCacheKey{
			Sha256:         library.HashRawBytes(makeBytes(fmt.Sprintf("lru-%04d-", i), 64)),
			ModelSlot:      "model",
			BudgetMaxBytes: 10 * 1024 * 1024,
			BudgetMaxEdge:  7680,
		}
	}

	// Populate. The very first inserted (keys[0]) is the LRU candidate.
	for i, k := range keys {
		cache.Put(k, []byte(fmt.Sprintf("payload-%04d", i)))
	}

	// keys[0] must have been evicted to make room for keys[256].
	got, ok := cache.Get(keys[0])
	assert.False(t, ok, "first inserted key must be evicted once capacity is exceeded")
	assert.Nil(t, got)

	// keys[256] (the eviction trigger) must be present.
	got, ok = cache.Get(keys[library.NormalizeCacheCapacity])
	require.True(t, ok, "the eviction-trigger entry must remain")
	assert.Equal(t, []byte(fmt.Sprintf("payload-%04d", library.NormalizeCacheCapacity)), got)

	// A Get on an existing key promotes it to MRU; the next Put must
	// therefore evict the new LRU (keys[1], not keys[0]).
	got, ok = cache.Get(keys[1])
	require.True(t, ok, "keys[1] was present before the promotion Get")
	require.Equal(t, []byte("payload-0001"), got)

	cache.Put(
		library.NormalizeCacheKey{
			Sha256:         library.HashRawBytes(makeBytes("lru-overflow-", 64)),
			ModelSlot:      "model",
			BudgetMaxBytes: 10 * 1024 * 1024,
			BudgetMaxEdge:  7680,
		},
		[]byte("overflow"),
	)

	// keys[1] was promoted to MRU on the Get above, so it must survive.
	got, ok = cache.Get(keys[1])
	assert.True(t, ok, "promoted-to-MRU entry must survive the next eviction")
	assert.Equal(t, []byte("payload-0001"), got)

	// keys[2] (now the LRU after keys[0]'s prior eviction and keys[1]'s
	// promotion) must have been evicted by the overflow Put.
	got, ok = cache.Get(keys[2])
	assert.False(t, ok, "the new LRU after promotion must be evicted")
	assert.Nil(t, got)
}

// TestNormalizeCache_ConcurrentSafety exercises Get/Put from many
// goroutines simultaneously to verify the mutex contract. The total
// workload (goroutines × opsPerGoroutine) deliberately exceeds the
// capacity so eviction happens mid-run — the contract under test is
// "no race / no panic / no corruption", NOT "every Put is followed
// by a hit" (which LRU eviction would otherwise violate). Run with
// -race to detect data races on the shared LRU state.
func TestNormalizeCache_ConcurrentSafety(t *testing.T) {
	cache := library.NewNormalizeCache()

	const goroutines = 16
	const opsPerGoroutine = 64

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(g int) {
			defer wg.Done()
			for i := 0; i < opsPerGoroutine; i++ {
				key := library.NormalizeCacheKey{
					Sha256:         library.HashRawBytes(makeBytes(fmt.Sprintf("conc-%02d-%04d-", g, i), 32)),
					ModelSlot:      "model",
					BudgetMaxBytes: 10 * 1024 * 1024,
					BudgetMaxEdge:  7680,
				}
				value := []byte(fmt.Sprintf("v-%d-%d", g, i))
				cache.Put(key, value)
				// We don't assert hit here — under concurrent Puts from
				// other goroutines that exceed capacity, this key MAY
				// have been evicted by the time we Get. The contract
				// under test is thread safety, not ordering.
				_, _ = cache.Get(key)
			}
		}(g)
	}
	wg.Wait()

	// Sanity: a fresh Put on a never-before-seen key after the storm
	// settles must hit, proving the cache is still functional.
	finalKey := library.NormalizeCacheKey{
		Sha256:         library.HashRawBytes(makeBytes("conc-final-", 32)),
		ModelSlot:      "model",
		BudgetMaxBytes: 10 * 1024 * 1024,
		BudgetMaxEdge:  7680,
	}
	finalValue := []byte("final-value")
	cache.Put(finalKey, finalValue)
	got, ok := cache.Get(finalKey)
	require.True(t, ok, "after the storm, a fresh Put followed by Get must hit")
	assert.Equal(t, finalValue, got)
}

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package sandbox

import (
	"container/list"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
)

// This file has NO build constraint, matching seatbelt_profile.go: the cache
// wraps renderSeatbeltProfile, which is pure Go and platform-independent, so
// both compile and unit-test on Linux even though the consuming
// SeatbeltBackend (backend_darwin_seatbelt.go) is darwin-only. The process-
// wide cache INSTANCE (seatbeltCache) is declared in backend_darwin_
// seatbelt.go instead of here, deliberately: it is only ever consumed from
// darwin-only code, and a package-level var that is unused outside a darwin
// build is exactly what static-analysis "unused" checks (correctly) flag on
// a non-darwin GOOS. The cache TYPE stays here, with no build tag, so it
// still compiles and is unit-testable (via newSeatbeltProfileCache /
// newSeatbeltProfileCacheWithRenderer, both exercised by
// seatbelt_cache_test.go) on every platform.
//
// # Why a cache exists at all
//
// renderSeatbeltProfile costs ~2.5ms for a realistic production-scale policy
// (142 KB rendered, 1000 connect-port rules — measured, see
// TestSeatbelt_ProductionScaleProfile_Spawns and spec FR-4.3). That is 5-10%
// on top of a fork+exec, which is unacceptable to pay on every single
// hardened-exec spawn when the authored policy for a given turn is usually
// unchanged from the previous spawn in the same turn. Spec FR-4.1.

// seatbeltProfileCache is a bounded, LRU-evicted cache from a policy's
// content hash to its rendered Seatbelt profile text.
type seatbeltProfileCache struct {
	capacity int
	renderFn func(SandboxPolicy) (string, error)

	mu    sync.Mutex
	order *list.List               // front = most recently used
	items map[string]*list.Element // key -> element in order, Value is *seatbeltCacheEntry
}

type seatbeltCacheEntry struct {
	key     string
	profile string
}

// newSeatbeltProfileCache builds a cache that renders via the real
// renderSeatbeltProfile. capacity must be positive.
func newSeatbeltProfileCache(capacity int) *seatbeltProfileCache {
	return newSeatbeltProfileCacheWithRenderer(capacity, renderSeatbeltProfile)
}

// newSeatbeltProfileCacheWithRenderer is the same as newSeatbeltProfileCache
// but with an injectable render function, so tests can count/observe render
// calls without depending on renderSeatbeltProfile's real cost or requiring
// a darwin host — proving a "hit" actually skipped rendering, rather than
// merely asserting two renders produced equal strings (which real rendering
// is deterministic enough to do even without a cache, and so would prove
// nothing about caching specifically).
func newSeatbeltProfileCacheWithRenderer(capacity int, renderFn func(SandboxPolicy) (string, error)) *seatbeltProfileCache {
	if capacity < 1 {
		capacity = 1
	}
	return &seatbeltProfileCache{
		capacity: capacity,
		renderFn: renderFn,
		order:    list.New(),
		items:    make(map[string]*list.Element, capacity),
	}
}

// getOrRender returns the rendered profile for policy, from cache when
// available, rendering (and caching the result) on a miss.
//
// FR-4.2 fail-closed: a render failure is returned to the caller and NOTHING
// is cached. The caller (SeatbeltBackend.ApplyToCmd, transitively
// applyPlatformHardening) must abort the spawn on error rather than fall
// back to an unconfined child — this function only guarantees it never masks
// that failure behind a stale or partial cache entry.
func (c *seatbeltProfileCache) getOrRender(policy SandboxPolicy) (string, error) {
	key := seatbeltPolicyCacheKey(policy)

	if profile, ok := c.lookup(key); ok {
		return profile, nil
	}

	// Render OUTSIDE the lock. Rendering does real filesystem work
	// (filepath.EvalSymlinks per rule, see resolveSeatbeltPath), so holding
	// the cache mutex across it would serialize every concurrent spawn —
	// including spawns for UNRELATED keys — behind whichever render happens
	// to be in flight. The cost of this choice is that two goroutines racing
	// on the same miss may both render once; that is strictly cheaper than
	// serializing all spawns, and the second render's result is discarded in
	// favor of whichever insert wins the lock below (both are byte-identical
	// for the same key, so which one "wins" is immaterial).
	profile, err := c.renderFn(policy)
	if err != nil {
		return "", err
	}

	c.insert(key, profile)
	return profile, nil
}

func (c *seatbeltProfileCache) lookup(key string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[key]
	if !ok {
		return "", false
	}
	c.order.MoveToFront(el)
	return el.Value.(*seatbeltCacheEntry).profile, true
}

func (c *seatbeltProfileCache) insert(key, profile string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if el, ok := c.items[key]; ok {
		// Lost the race against a concurrent renderer for the same key.
		// Both renders produce byte-identical output for the same key
		// (rendering is a pure function of policy content), so just bump
		// recency and keep the existing entry rather than replacing it.
		c.order.MoveToFront(el)
		return
	}

	el := c.order.PushFront(&seatbeltCacheEntry{key: key, profile: profile})
	c.items[key] = el

	for c.order.Len() > c.capacity {
		oldest := c.order.Back()
		if oldest == nil {
			break
		}
		c.order.Remove(oldest)
		delete(c.items, oldest.Value.(*seatbeltCacheEntry).key)
	}
}

// len reports the current number of cached entries. Test-only helper.
func (c *seatbeltProfileCache) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.order.Len()
}

// seatbeltPolicyCacheKey hashes the SEMANTIC CONTENT of policy into a stable
// cache key. Spec FR-4.1: "a stale profile must not be representable;
// different policy -> different key."
//
// Why a content hash and nothing weaker:
//   - A pointer or struct identity is wrong: two calls building "the same"
//     policy from the same authored fspolicy.FSPolicy produce two different
//     SandboxPolicy values (DeriveKernelPolicy allocates fresh slices each
//     call), so identity would never hit the cache at all, or worse, an
//     unrelated caller could reuse a stale pointer's identity by accident.
//   - A timestamp is wrong for the opposite reason: it does not change when
//     the policy's content does not, so a genuinely stale cache entry from
//     policy V1 would still validate for a request built moments later from
//     the identical V1 content — that is a cache that "works" only by
//     coincidence of timing.
//   - A partial key (e.g. only WorkDir, or only the mount list) is wrong
//     because it is exactly what FR-4.4 exists to catch: two policies that
//     differ ONLY in, say, DeniedPaths or BindPortRules would collide on a
//     partial key and the second turn would silently run under the first
//     turn's grants.
//
// fmt's "%#v" verb (Go-syntax representation) is used rather than
// encoding/json deliberately: SandboxPolicy has no `json:` struct tags (it is
// not a wire type — see CLAUDE.md constraint #8, which reserves json tags for
// the contract-first wire boundary), and "%#v" gives the same determinism
// guarantee for our purposes without requiring them. It renders struct
// fields in fixed declaration order and slice elements in order, so the same
// SandboxPolicy value always produces the same string, and any field or
// element-order difference changes the string (and therefore the hash).
// SandboxPolicy has no maps or pointers, so there is no map-iteration-order
// or pointer-address nondeterminism to worry about. The only case where two
// semantically-equivalent policies produce different keys is nil vs.
// non-nil-empty slices — that is over-distinguishing, not
// under-distinguishing: it can only cost an extra cache miss, never produce
// a false hit against stale content, which is the one failure mode this
// function must never have.
func seatbeltPolicyCacheKey(policy SandboxPolicy) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%#v", policy)))
	return hex.EncodeToString(sum[:])
}

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package sandbox

import (
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// No build tag: exercises the pure-Go cache + renderSeatbeltProfile, both of
// which compile and run on any platform (only the consuming SeatbeltBackend
// is darwin-gated). This is deliberate — cache correctness must hold
// regardless of which OS runs the test.

// countingRenderer wraps renderSeatbeltProfile and counts real invocations,
// so tests can prove a cache HIT actually skipped rendering rather than
// merely observing two equal strings (which a correct, deterministic
// renderer would produce even with no cache at all).
func countingRenderer() (render func(SandboxPolicy) (string, error), calls *int32) {
	var n int32
	return func(p SandboxPolicy) (string, error) {
		atomic.AddInt32(&n, 1)
		return renderSeatbeltProfile(p)
	}, &n
}

func mustPolicy(t *testing.T, path string) SandboxPolicy {
	t.Helper()
	return DefaultPolicyForModel(FilesystemModelOpen, path, nil, nil, nil, nil)
}

// TestSeatbeltCache_HitSkipsRenderAndIsByteIdentical is FR-4.1/FR-4.4's core
// claim: a cache hit for the SAME policy content (a) never calls the
// renderer again and (b) returns exactly what the first render produced.
func TestSeatbeltCache_HitSkipsRenderAndIsByteIdentical(t *testing.T) {
	render, calls := countingRenderer()
	cache := newSeatbeltProfileCacheWithRenderer(8, render)

	home := t.TempDir()
	policy := mustPolicy(t, home)

	first, err := cache.getOrRender(policy)
	require.NoError(t, err)
	require.EqualValues(t, 1, atomic.LoadInt32(calls), "first call must render")

	second, err := cache.getOrRender(policy)
	require.NoError(t, err)
	assert.EqualValues(t, 1, atomic.LoadInt32(calls), "second call for the same policy must be a cache HIT (no re-render)")
	assert.Equal(t, first, second, "a cache hit must return the byte-identical profile")

	// A structurally NEW SandboxPolicy value, built by a completely separate
	// call, with the same content, must still hit — proves the key is
	// content-based, not identity-based (DefaultPolicyForModel allocates
	// fresh slices on every call, so this is genuinely a different Go value).
	samePolicyDifferentValue := mustPolicy(t, home)
	third, err := cache.getOrRender(samePolicyDifferentValue)
	require.NoError(t, err)
	assert.EqualValues(t, 1, atomic.LoadInt32(calls), "a content-identical policy built as a fresh value must still hit")
	assert.Equal(t, first, third)
}

// TestSeatbeltCache_MissAfterPolicyChangeRendersAgain covers FR-4.4 directly:
// simulating a mount being ADDED (a new FilesystemRule granting a second
// path) and REMOVED (back to the original single rule) must each be a cache
// MISS that renders a genuinely different — and then genuinely reverted —
// profile. A cache that returned the first policy's profile for the second
// key would be "worse than no cache": it would enforce yesterday's grants.
func TestSeatbeltCache_MissAfterPolicyChangeRendersAgain(t *testing.T) {
	render, calls := countingRenderer()
	cache := newSeatbeltProfileCacheWithRenderer(8, render)

	base := mustPolicy(t, t.TempDir())

	before, err := cache.getOrRender(base)
	require.NoError(t, err)
	require.EqualValues(t, 1, atomic.LoadInt32(calls))

	// Simulate a mount being ADDED: a new writable root joins the policy.
	withMount := base
	withMount.FilesystemRules = append([]PathRule{}, base.FilesystemRules...)
	withMount.FilesystemRules = append(withMount.FilesystemRules,
		PathRule{Path: t.TempDir(), Access: AccessWrite})

	afterAdd, err := cache.getOrRender(withMount)
	require.NoError(t, err)
	assert.EqualValues(t, 2, atomic.LoadInt32(calls), "adding a mount must be a cache MISS")
	assert.NotEqual(t, before, afterAdd, "the rendered profile must actually change when a mount is added")

	// Simulate the mount being REMOVED again: content reverts to `base`.
	afterRemove, err := cache.getOrRender(base)
	require.NoError(t, err)
	assert.EqualValues(t, 2, atomic.LoadInt32(calls), "reverting to a previously-cached policy must be a cache HIT, not a third render")
	assert.Equal(t, before, afterRemove, "removing the mount must restore the exact pre-mount profile")

	// And the with-mount key is still independently cached (not evicted or
	// merged with `base`'s key).
	stillCached, err := cache.getOrRender(withMount)
	require.NoError(t, err)
	assert.EqualValues(t, 2, atomic.LoadInt32(calls))
	assert.Equal(t, afterAdd, stillCached)
}

// TestSeatbeltCache_KeyDiffersForEveryFieldWithKernelExpression is the "think
// hard about the key" requirement made concrete: it walks every field of
// SandboxPolicy that reaches the renderer and asserts changing it ALONE
// changes the cache key. A partial key (e.g. only WorkDir/the first
// FilesystemRule) would pass some of these and fail others.
func TestSeatbeltCache_KeyDiffersForEveryFieldWithKernelExpression(t *testing.T) {
	base := SandboxPolicy{
		FilesystemRules: []PathRule{{Path: "/a", Access: AccessRead | AccessWrite}},
		BindPortRules:   []NetPortRule{{Port: 8080}},
		ConnectPortRules: []NetPortRule{
			{Port: 443},
		},
		DeniedPaths: []string{"/a/secret"},
	}
	baseKey := seatbeltPolicyCacheKey(base)

	variants := map[string]SandboxPolicy{
		"filesystem rule path changed": withFSPath(base, "/b"),
		"filesystem rule access changed": func() SandboxPolicy {
			p := base
			p.FilesystemRules = []PathRule{{Path: "/a", Access: AccessRead}}
			return p
		}(),
		"bind port changed": func() SandboxPolicy {
			p := base
			p.BindPortRules = []NetPortRule{{Port: 9090}}
			return p
		}(),
		"connect port changed": func() SandboxPolicy {
			p := base
			p.ConnectPortRules = []NetPortRule{{Port: 8443}}
			return p
		}(),
		"denied path changed": func() SandboxPolicy {
			p := base
			p.DeniedPaths = []string{"/a/other-secret"}
			return p
		}(),
		"ReadsOpen changed": func() SandboxPolicy {
			p := base
			p.ReadsOpen = true
			return p
		}(),
		"ExecOpen changed": func() SandboxPolicy {
			p := base
			p.ExecOpen = true
			return p
		}(),
		"InheritToChildren changed": func() SandboxPolicy {
			p := base
			p.InheritToChildren = true
			return p
		}(),
	}

	for name, variant := range variants {
		t.Run(name, func(t *testing.T) {
			key := seatbeltPolicyCacheKey(variant)
			assert.NotEqual(t, baseKey, key, "changing %s must produce a different cache key — a collision here would silently reuse a stale profile", name)
		})
	}

	// Sanity: an UNCHANGED policy (rebuilt from scratch, not reused by
	// reference) must produce the SAME key. Without this control, the test
	// above could pass trivially if the key function returned something
	// unstable for every call.
	rebuilt := SandboxPolicy{
		FilesystemRules:  []PathRule{{Path: "/a", Access: AccessRead | AccessWrite}},
		BindPortRules:    []NetPortRule{{Port: 8080}},
		ConnectPortRules: []NetPortRule{{Port: 443}},
		DeniedPaths:      []string{"/a/secret"},
	}
	rebuiltKey := seatbeltPolicyCacheKey(rebuilt)
	assert.Equal(t, baseKey, rebuiltKey, "an identically-constructed policy must produce the identical key")
}

func withFSPath(p SandboxPolicy, path string) SandboxPolicy {
	p.FilesystemRules = []PathRule{{Path: path, Access: p.FilesystemRules[0].Access}}
	return p
}

// TestSeatbeltCache_BoundedWithLRUEviction proves the cache is actually
// bounded (an unbounded map keyed by policy content is a memory leak
// disguised as a cache) and that eviction is LRU-correct: the entry that has
// gone longest without being touched is the one dropped, not an arbitrary one.
func TestSeatbeltCache_BoundedWithLRUEviction(t *testing.T) {
	const capacity = 3
	render, calls := countingRenderer()
	cache := newSeatbeltProfileCacheWithRenderer(capacity, render)

	policyFor := func(n int) SandboxPolicy {
		return SandboxPolicy{
			FilesystemRules: []PathRule{{Path: fmt.Sprintf("/p%d", n), Access: AccessRead}},
		}
	}

	// Fill to capacity: keys 0, 1, 2. Order (MRU..LRU): 2, 1, 0.
	for i := 0; i < capacity; i++ {
		_, err := cache.getOrRender(policyFor(i))
		require.NoError(t, err)
	}
	require.Equal(t, capacity, cache.len())
	require.EqualValues(t, capacity, atomic.LoadInt32(calls))

	// Touch key 0 so it becomes MRU. Order becomes: 0, 2, 1 (1 is now LRU).
	_, err := cache.getOrRender(policyFor(0))
	require.NoError(t, err)
	require.EqualValues(t, capacity, atomic.LoadInt32(calls), "re-touching an already-cached key must be a hit")

	// Insert a NEW key (3). This must evict the LRU entry, key 1 — not key 0
	// (just touched) and not key 2 (touched more recently than 1).
	_, err = cache.getOrRender(policyFor(3))
	require.NoError(t, err)
	assert.Equal(t, capacity, cache.len(), "cache must stay bounded at capacity after an insert past the limit")
	assert.EqualValues(t, capacity+1, atomic.LoadInt32(calls))

	// key 1 must have been evicted: fetching it again is a MISS (render count
	// increases).
	before := atomic.LoadInt32(calls)
	_, err = cache.getOrRender(policyFor(1))
	require.NoError(t, err)
	assert.Equal(t, before+1, atomic.LoadInt32(calls), "key 1 (the true LRU entry) must have been evicted")

	// key 0 must NOT have been evicted: fetching it is still a HIT.
	before = atomic.LoadInt32(calls)
	_, err = cache.getOrRender(policyFor(0))
	require.NoError(t, err)
	assert.Equal(t, before, atomic.LoadInt32(calls), "key 0 was touched most recently before the capacity-triggering insert and must still be cached")
}

// TestSeatbeltCache_RenderFailureIsNotCached is FR-4.2 at the cache layer: a
// render error must propagate (never silently produce an empty/stale profile)
// and must NOT poison the cache with a bad entry — a subsequent call with a
// policy that WOULD succeed must still be able to render normally.
func TestSeatbeltCache_RenderFailureIsNotCached(t *testing.T) {
	// A fresh, isolated cache using the real renderer, so this test never
	// shares state with the package-level seatbeltCache that production code
	// (backend_darwin_seatbelt.go) uses.
	cache := newSeatbeltProfileCache(8)

	// Access == 0 is exactly what renderSeatbeltProfile rejects — see
	// seatbelt_profile.go's "filesystem rule ... has no access flags" check.
	broken := SandboxPolicy{
		FilesystemRules: []PathRule{{Path: "/broken", Access: 0}},
	}

	_, err := cache.getOrRender(broken)
	require.Error(t, err, "a policy the renderer rejects must fail, not silently succeed")

	// Retrying the SAME broken policy must fail again, not silently succeed
	// with an empty/partial profile — a render failure must never be cached.
	_, err = cache.getOrRender(broken)
	require.Error(t, err, "a render failure must not be cached as if it were a valid (or empty) profile")

	// A subsequent, valid, DIFFERENT-content call on the SAME cache must
	// still work normally — the failure above must not have wedged the cache.
	ok := mustPolicy(t, t.TempDir())
	profile, err := cache.getOrRender(ok)
	require.NoError(t, err)
	assert.NotEmpty(t, profile)
}

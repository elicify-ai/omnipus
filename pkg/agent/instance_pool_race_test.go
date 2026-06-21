// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// Race-detector test for the ProviderPool atomic-pointer (W4-3).
//
// BDD Scenario: "Concurrent ApplyAgentModel + GetProviderForCandidate is race-free"
//
// Given an AgentInstance with an eagerly-built provider pool,
// And a goroutine repeatedly calling GetProviderForCandidate (simulating
// the per-fallback lookup in the in-flight turn path),
// And another goroutine repeatedly calling StoreProviderPool (simulating
// ApplyAgentModel flipping the pool on a model switch),
// When both run concurrently for a bounded interval,
// Then the Go race detector reports no race,
// And no concurrent-map-read+write panic is observed.
//
// Without the fix, GetProviderForCandidate read a.ProviderPool directly
// while StoreProviderPool wrote a.ProviderPool under providerPoolMu — a
// classic data race that the Go runtime detects as "fatal error:
// concurrent map read and map write" the moment two goroutines hit the
// pool at the same time. The fix replaces the field with an
// atomic.Pointer[map[string]providers.LLMProvider] so reads and writes
// go through the same atomic load/store and the underlying map is
// immutable from a reader's perspective.
//
// Run with: go test -race ./pkg/agent/ -run '^TestProviderPool_NoRace$' -p 1
//
// Traces to: W4-3 (ProviderPool map race)

package agent

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/providers"
)

// TestProviderPool_NoRace drives concurrent StoreProviderPool and
// GetProviderForCandidate on the same AgentInstance for a short interval
// and asserts the race detector reports no race. Before the fix, this
// test would panic the test binary with "fatal error: concurrent map
// iteration and map write" (or "concurrent map read and map write")
// after a few hundred microseconds — the test runtime is long enough
// to be reliably hit under -race.
func TestProviderPool_NoRace(t *testing.T) {
	primary := &mockProvider{}
	agent := &AgentInstance{
		ID:       "race-pool",
		Provider: primary,
	}
	// Seed an initial pool so the first read doesn't fall through the
	// "no pool" warn path — we want to exercise the map-lookup race, not
	// the nil-pool fallback.
	agent.StoreProviderPool(map[string]providers.LLMProvider{
		"openai":     &mockProvider{},
		"anthropic":  &mockProvider{},
		"openrouter": &mockProvider{},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	var wg sync.WaitGroup
	var readErrors int64

	// Writers: simulate ApplyAgentModel's pool flip. StoreProviderPool
	// performs a defensive copy of the input map, then atomically swaps
	// the published pointer — readers never observe a partially-mutated
	// map because the new map is fully built before the atomic store.
	for i := range 4 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				default:
					// Alternate the pool shape so the writer exercises
					// both the grow (more entries) and shrink (fewer
					// entries) cases of the swap. Each swap is a fresh
					// map; the previous one is left to the GC.
					pool := map[string]providers.LLMProvider{
						"openai": &mockProvider{},
					}
					if n%2 == 0 {
						pool["anthropic"] = &mockProvider{}
						pool["openrouter"] = &mockProvider{}
					} else {
						pool["anthropic"] = &mockProvider{}
					}
					agent.StoreProviderPool(pool)
				}
			}
		}(i)
	}

	// Readers: simulate the per-fallback lookup in runTurn. We hit
	// multiple pinned provider names to exercise the full map surface.
	pinnedNames := []string{"openai", "anthropic", "openrouter", "missing-pool-entry"}
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var idx int
			for {
				select {
				case <-ctx.Done():
					return
				default:
					name := pinnedNames[idx%len(pinnedNames)]
					idx++
					got := agent.GetProviderForCandidate(providers.FallbackCandidate{
						Provider: name,
						Model:    "race-test-model",
					})
					// The lookup must never return nil for a named
					// pinned provider (FR-007: fall back to primary
					// when the pool entry is missing). A nil return
					// here would indicate the read raced with the
					// atomic store and observed a half-published
					// state — the race we're guarding against.
					if got == nil {
						atomic.AddInt64(&readErrors, 1)
					}
				}
			}
		}()
	}

	wg.Wait()

	if readErrors != 0 {
		t.Errorf(
			"GetProviderForCandidate returned nil %d times during concurrent store — pool atomicity is broken",
			readErrors,
		)
	}
}

// TestProviderPool_StoreDefensiveCopy asserts that mutating the caller's
// input map after StoreProviderPool returns does NOT change the published
// pool. StoreProviderPool MUST copy the input so a caller that reuses the
// map (e.g. as scratch space between switches) cannot race a concurrent
// reader.
func TestProviderPool_StoreDefensiveCopy(t *testing.T) {
	original := &mockProvider{}
	agent := &AgentInstance{
		ID:       "race-pool-copy",
		Provider: &mockProvider{},
	}
	input := map[string]providers.LLMProvider{
		"openai": original,
	}
	agent.StoreProviderPool(input)

	// Mutate the caller's map AFTER the store. If StoreProviderPool had
	// kept the same backing array, the agent's published pool would now
	// carry the mutation. With the defensive copy, the published pool
	// is unaffected.
	input["openai"] = &mockProvider{}
	input["anthropic"] = &mockProvider{}

	got := agent.GetProviderForCandidate(providers.FallbackCandidate{
		Provider: "openai",
		Model:    "copy-test",
	})
	if got != original {
		t.Errorf(
			"GetProviderForCandidate(openai) = %p, want %p (caller's post-store mutation leaked into the published pool)",
			got,
			original,
		)
	}

	// "anthropic" must NOT be present — the post-store add of "anthropic"
	// was to the caller's local map, not the published pool.
	if pool := agent.providerPool.Load(); pool != nil {
		if _, ok := (*pool)["anthropic"]; ok {
			t.Errorf("published pool unexpectedly contains 'anthropic' — defensive copy is broken")
		}
	}
}

// TestProviderPool_NilStoreAllowsRead confirms the read path handles a
// nil published pointer gracefully. NewAgentInstance calls
// StoreProviderPool with a non-nil pool (buildProviderPool returns at
// least an empty map for the "every candidate has empty Provider" case,
// or nil for the "no candidates" case — see buildProviderPool). The
// read path must not panic on a nil load and must fall back to the
// primary provider for any pinned lookup.
func TestProviderPool_NilStoreAllowsRead(t *testing.T) {
	primary := &mockProvider{}
	agent := &AgentInstance{
		ID:       "race-pool-nil",
		Provider: primary,
	}
	agent.StoreProviderPool(nil)

	if got := agent.GetProviderForCandidate(providers.FallbackCandidate{
		Provider: "openai",
		Model:    "x",
	}); got != primary {
		t.Errorf("GetProviderForCandidate on nil pool = %p, want primary %p", got, primary)
	}
}

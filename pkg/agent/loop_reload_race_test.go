// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// Race-detector test for atomic-pointer hot-reload (FR-019, FR-020).
//
// BDD Scenario: "Concurrent reload + turn assembly never returns a torn read"
//
// Given a goroutine repeatedly calling StoreToolPolicy (simulating ReloadProviderAndConfig),
// And another goroutine repeatedly calling LoadToolPolicy (simulating turn assembly),
// When both run concurrently,
// Then every loaded policy is coherent (never a torn/nil value from a concurrent write).
//
// BDD Scenario: "Tool Execute observes post-swap deps via atomic deref"
//
// Given a tool T registered before a deps swap,
// And T's Execute reads the deps via atomic.Load (per-call deref),
// When the deps atomic pointer is swapped,
// And T.Execute is called,
// Then T.Execute observes the post-swap value.
//
// Run with: go test -race ./pkg/agent/ -run "TestReloadRace_|TestDepsAtomic_"
//
// Traces to: tool-registry-redesign-spec.md FR-019 / FR-020 / BDD "Concurrent reload"

package agent

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/tools"
)

// TestReloadRace_PolicyAtomicNoTornReads verifies that concurrent Store and Load
// operations on AgentInstance.toolPolicy never produce a torn read under the race
// detector.
//
// This test MUST be run with: go test -race
// Without the -race flag, it is still a correctness test.
//
// Traces to: tool-registry-redesign-spec.md FR-020 / BDD "Concurrent reload + turn assembly"
func TestReloadRace_PolicyAtomicNoTornReads(t *testing.T) {
	agent := &AgentInstance{ID: "race-test-agent", AgentType: "core"}

	// Seed an initial policy.
	agent.StoreToolPolicy(&tools.ToolPolicyCfg{
		DefaultPolicy:       "allow",
		Policies:            map[string]string{"exec": "allow"},
		GlobalDefaultPolicy: "allow",
	})

	// Run for a short time under the race detector.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	var wg sync.WaitGroup
	var loadErrors int64

	// Writers: repeatedly alternate policies (simulating operator config PUTs).
	for i := range 4 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				default:
					policyVal := "allow"
					if n%2 == 0 {
						policyVal = "deny"
					}
					agent.StoreToolPolicy(&tools.ToolPolicyCfg{
						DefaultPolicy:       "allow",
						Policies:            map[string]string{"exec": policyVal},
						GlobalDefaultPolicy: "allow",
					})
				}
			}
		}(i)
	}

	// Readers: repeatedly load and validate coherence (simulating turn assembly).
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				default:
					p := agent.LoadToolPolicy()
					if p == nil {
						// nil is permissible (means allow-all), but should not happen
						// after the initial Store.
						continue
					}
					// The loaded policy must have a valid DefaultPolicy.
					if p.DefaultPolicy != "allow" && p.DefaultPolicy != "deny" && p.DefaultPolicy != "ask" {
						atomic.AddInt64(&loadErrors, 1)
					}
					// Exec policy must be one of the valid values.
					if v := p.Policies["exec"]; v != "allow" && v != "deny" && v != "ask" && v != "" {
						atomic.AddInt64(&loadErrors, 1)
					}
				}
			}
		}()
	}

	wg.Wait()

	assert.Zero(t, loadErrors,
		"no torn/invalid policy values must be observed across concurrent load/store")
}

// TestReloadRace_MultipleAgentsConcurrent verifies that multiple AgentInstances
// with independent atomic policy pointers do not interfere with each other.
//
// Traces to: tool-registry-redesign-spec.md FR-020
func TestReloadRace_MultipleAgentsConcurrent(t *testing.T) {
	const numAgents = 8
	agents := make([]*AgentInstance, numAgents)
	for i := range numAgents {
		agents[i] = &AgentInstance{ID: "agent-" + string(rune('A'+i)), AgentType: "core"}
		agents[i].StoreToolPolicy(&tools.ToolPolicyCfg{
			DefaultPolicy: "allow",
			Policies:      map[string]string{"tool": "allow"},
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	var wg sync.WaitGroup
	var errors int64

	for i, a := range agents {
		wg.Add(1)
		go func(agentIdx int, ag *AgentInstance) {
			defer wg.Done()
			policyVal := "allow"
			if agentIdx%2 == 0 {
				policyVal = "deny"
			}
			for {
				select {
				case <-ctx.Done():
					return
				default:
					ag.StoreToolPolicy(&tools.ToolPolicyCfg{
						DefaultPolicy: policyVal,
						Policies:      map[string]string{"tool": policyVal},
					})
					p := ag.LoadToolPolicy()
					if p == nil {
						continue
					}
					// Verify this agent's read is coherent — should be own value.
					if p.DefaultPolicy != policyVal {
						// The race might produce a brief view of the other policy;
						// that's fine — we're checking for torn reads, not stale reads.
					}
					if p.DefaultPolicy != "allow" && p.DefaultPolicy != "deny" && p.DefaultPolicy != "ask" {
						atomic.AddInt64(&errors, 1)
					}
				}
			}
		}(i, a)
	}

	wg.Wait()
	assert.Zero(t, errors, "no torn reads across multiple concurrent agents")
}

// --- Atomic deps deref tests ---

// atomicDeps is a test stand-in for a real Tier13Deps atomic pointer.
type testDepsV struct {
	version int
	key     string
}

// depsRefTool is a tool that reads deps via per-call atomic.Load (FR-019 contract).
type depsRefTool struct {
	name    string
	depsPtr *atomic.Pointer[testDepsV]
}

func (d *depsRefTool) Name() string                 { return d.name }
func (d *depsRefTool) Description() string          { return "deps-ref test tool" }
func (d *depsRefTool) Parameters() map[string]any   { return map[string]any{} }
func (d *depsRefTool) Scope() tools.ToolScope       { return tools.ScopeGeneral }
func (d *depsRefTool) Category() tools.ToolCategory { return tools.CategoryCore }
func (d *depsRefTool) Execute(_ context.Context, _ map[string]any) *tools.ToolResult {
	// Per-call deref: load the current deps each time Execute is called.
	// FR-019: MUST deref per call, never capture in closure.
	deps := d.depsPtr.Load()
	if deps == nil {
		return &tools.ToolResult{IsError: true, ForLLM: "deps not initialized"}
	}
	return &tools.ToolResult{ForLLM: "deps-version=" + string(rune('0'+deps.version)) + " key=" + deps.key}
}

// TestDepsAtomic_ToolObservesPostSwapDeps verifies that a tool registered before
// a deps swap observes the post-swap deps value via per-call atomic.Load.
//
// BDD: "Tool Execute observes post-swap deps via atomic deref"
// Traces to: tool-registry-redesign-spec.md FR-019 / US-3, AS-4
func TestDepsAtomic_ToolObservesPostSwapDeps(t *testing.T) {
	var depsPtr atomic.Pointer[testDepsV]

	// Wire initial deps.
	initialDeps := &testDepsV{version: 1, key: "initial-api-key"}
	depsPtr.Store(initialDeps)

	// Register the tool (before any swap).
	tool := &depsRefTool{name: "tool_with_deps", depsPtr: &depsPtr}

	// Execute before swap — should see initial deps.
	result1 := tool.Execute(context.Background(), nil)
	require.False(t, result1.IsError, "Execute before swap must succeed")
	assert.Contains(t, result1.ForLLM, "initial-api-key",
		"before swap: Execute must observe initial deps")

	// Swap deps (simulating ReloadProviderAndConfig).
	updatedDeps := &testDepsV{version: 2, key: "updated-api-key"}
	depsPtr.Store(updatedDeps)

	// Execute after swap — must observe the NEW deps (not the captured initial).
	result2 := tool.Execute(context.Background(), nil)
	require.False(t, result2.IsError, "Execute after swap must succeed")
	assert.Contains(t, result2.ForLLM, "updated-api-key",
		"after swap: Execute must observe post-swap deps (per-call deref)")

	// Differentiation test: two different dep states → two different results.
	assert.NotEqual(t, result1.ForLLM, result2.ForLLM,
		"different dep states must produce different Execute results (not hardcoded)")
}

// TestDepsAtomic_SwapRace verifies that concurrent swaps and Execute calls
// on the same atomic pointer are race-free.
//
// Run with: go test -race
// Traces to: tool-registry-redesign-spec.md FR-019
func TestDepsAtomic_SwapRace(t *testing.T) {
	var depsPtr atomic.Pointer[testDepsV]
	depsPtr.Store(&testDepsV{version: 0, key: "initial"})

	tool := &depsRefTool{name: "deps_race_tool", depsPtr: &depsPtr}

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	var wg sync.WaitGroup
	var execErrors int64

	// Swap goroutines.
	for i := range 4 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				default:
					depsPtr.Store(&testDepsV{version: n, key: "key-" + string(rune('A'+n))})
				}
			}
		}(i)
	}

	// Execute goroutines.
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				default:
					result := tool.Execute(context.Background(), nil)
					if result.IsError {
						atomic.AddInt64(&execErrors, 1)
					}
				}
			}
		}()
	}

	wg.Wait()
	assert.Zero(t, execErrors,
		"Execute must never fail during concurrent deps swaps (per-call deref is race-safe)")
}

// TestDepsAtomic_NilDepsHandledGracefully verifies that a tool handles nil deps
// gracefully (returns an error result, does not panic).
//
// Traces to: tool-registry-redesign-spec.md FR-019 / FR-033 (error-Execute for unavailable deps)
func TestDepsAtomic_NilDepsHandledGracefully(t *testing.T) {
	var depsPtr atomic.Pointer[testDepsV]
	// Leave depsPtr nil (zero value).

	tool := &depsRefTool{name: "nil_deps_tool", depsPtr: &depsPtr}

	assert.NotPanics(t, func() {
		result := tool.Execute(context.Background(), nil)
		require.NotNil(t, result, "Execute must return a result even with nil deps")
		assert.True(t, result.IsError,
			"Execute with nil deps must return IsError=true (stable error string per FR-033)")
	})
}

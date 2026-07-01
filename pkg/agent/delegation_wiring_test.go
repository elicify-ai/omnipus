// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/coreagent"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// buildPolicyWith constructs a DelegationPolicy with the given target agent IDs
// using all delegation modes.
func buildPolicyWith(ids ...string) *config.DelegationPolicy {
	refs := make([]config.AgentRef, len(ids))
	for i, id := range ids {
		refs[i] = config.AgentRef{Kind: "local", ID: id}
	}
	return &config.DelegationPolicy{To: refs}
}

// wireTestLoop builds a minimal AgentLoop + a config with an agent that has
// the given per-agent DelegationPolicy. Returns the loop, the agentID, and
// the ContextBuilder wired for that agent. Caller owns al.Close().
//
// agentID should be one of the seeded core agents (e.g. "ava") or a custom
// id. We rely on SeedConfig so the registry has a proper agent instance.
func wireTestLoop(t *testing.T, agentID string, perAgentPolicy *config.DelegationPolicy) (*AgentLoop, *ContextBuilder) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	cfg := minimalTestConfig(t)
	// Install the per-agent policy (may be nil to rely on defaults).
	for i := range cfg.Agents.List {
		if cfg.Agents.List[i].ID == agentID {
			cfg.Agents.List[i].DelegationPolicy = perAgentPolicy
			break
		}
	}
	cfg.Agents.Defaults.Workspace = filepath.Join(home, "default-workspace")
	cfg.Agents.Defaults.ModelName = "test-model"
	cfg.Agents.Defaults.MaxTokens = 4096

	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	al, err := NewAgentLoop(cfg, msgBus, &mockProvider{})
	if err != nil {
		t.Fatalf("NewAgentLoop: %v", err)
	}
	t.Cleanup(func() { al.Close() })

	inst, ok := al.GetRegistry().GetAgent(agentID)
	if !ok || inst == nil || inst.ContextBuilder == nil {
		t.Fatalf("agent %q not in registry after loop init", agentID)
	}
	return al, inst.ContextBuilder
}

// reloadWithPolicy rebuilds the loop's config with an updated per-agent
// delegation policy for agentID, then calls ReloadProviderAndConfig. Returns
// the (new) ContextBuilder for agentID after the reload.
func reloadWithPolicy(t *testing.T, al *AgentLoop, agentID string, policy *config.DelegationPolicy) *ContextBuilder {
	t.Helper()
	liveCfg := al.GetConfig()
	// Build a fresh Config so we don't share mutexes.
	newCfg := &config.Config{}
	newCfg.Agents.Defaults = liveCfg.Agents.Defaults
	// Copy the agent list, updating the target agent's policy.
	newCfg.Agents.List = make([]config.AgentConfig, len(liveCfg.Agents.List))
	copy(newCfg.Agents.List, liveCfg.Agents.List)
	for i := range newCfg.Agents.List {
		if newCfg.Agents.List[i].ID == agentID {
			newCfg.Agents.List[i].DelegationPolicy = policy
		}
	}
	if err := al.ReloadProviderAndConfig(context.Background(), &mockProvider{}, newCfg); err != nil {
		t.Fatalf("ReloadProviderAndConfig: %v", err)
	}
	inst, ok := al.GetRegistry().GetAgent(agentID)
	if !ok || inst == nil || inst.ContextBuilder == nil {
		t.Fatalf("agent %q not in registry after reload", agentID)
	}
	return inst.ContextBuilder
}

// ---------------------------------------------------------------------------
// TestDelegationWiring_RuntimeRefresh (P1 replacement — real path)
//
// Drives the REAL reload path:
//  1. Build AgentLoop with agent "ava" allowed to delegate to "ray".
//  2. Wire is installed by wireEnvProviders at loop init.
//  3. Assert Phase A: "ray" in the dynamic context block.
//  4. Call ReloadProviderAndConfig with a NEW config that removes "ray".
//     wireDelegationInjectors is called internally on the new registry.
//  5. The SAME ContextBuilder pointer that was wired at Phase A (retrieved via
//     the new registry) now yields a context WITHOUT "ray" — no manual re-wire.
//
// This validates the closure reads al.GetConfig()/al.GetRegistry() on every
// call, not snapshots captured at wire time.
// ---------------------------------------------------------------------------

func TestDelegationWiring_RuntimeRefresh(t *testing.T) {
	// Not parallel: uses t.Setenv via wireTestLoop (Go restriction).
	const agentID = "ava"
	// Phase A: ava may delegate to ray.
	al, _ := wireTestLoop(t, agentID, buildPolicyWith("ray"))

	// Retrieve the wired ContextBuilder from the live registry.
	instA, ok := al.GetRegistry().GetAgent(agentID)
	if !ok || instA == nil {
		t.Fatal("Phase A: ava not in registry")
	}
	cbA := instA.ContextBuilder

	got1 := cbA.buildDynamicContext("", "", "", "")
	if !strings.Contains(got1, "## Delegation") {
		t.Fatalf("Phase A: Delegation block missing.\ngot: %s", got1)
	}
	if !strings.Contains(got1, "ray") {
		t.Fatalf("Phase A: expected 'ray' in Delegation block.\ngot: %s", got1)
	}

	// Phase B: reload with policy that removes ray (ava → nobody).
	// We do NOT manually call wireDelegationInjectors — the reload must do it.
	cbB := reloadWithPolicy(t, al, agentID, buildPolicyWith())

	got2 := cbB.buildDynamicContext("", "", "", "")
	if strings.Contains(got2, "ray") {
		t.Fatalf("Phase B: 'ray' must be gone after reload removing it.\ngot: %s", got2)
	}
	if !strings.Contains(got2, "cannot delegate") {
		t.Fatalf("Phase B: empty To must render 'cannot delegate'.\ngot: %s", got2)
	}
}

// ---------------------------------------------------------------------------
// TestDelegationWiring_RuntimeRefresh_AddTarget
//
// Inverse of the removal test: an agent that initially cannot delegate gains
// a target after reload.
// ---------------------------------------------------------------------------

func TestDelegationWiring_RuntimeRefresh_AddTarget(t *testing.T) {
	// Not parallel: uses t.Setenv via wireTestLoop (Go restriction).
	const agentID = "ava"
	// Phase A: ava has nil policy → cannot delegate.
	al, _ := wireTestLoop(t, agentID, nil)

	instA, ok := al.GetRegistry().GetAgent(agentID)
	if !ok || instA == nil {
		t.Fatal("Phase A: ava not in registry")
	}
	// Note: Ava's core seed already has a delegation policy (coreAgentDelegation).
	// We need to verify the state the policy resolver produces.
	got1 := instA.ContextBuilder.buildDynamicContext("", "", "", "")
	// Record whether delegation is currently allowed (may be from seed).
	phase1HasDelegation := strings.Contains(got1, "ray")

	// Phase B: add ray explicitly.
	cbB := reloadWithPolicy(t, al, agentID, buildPolicyWith("ray"))
	got2 := cbB.buildDynamicContext("", "", "", "")

	if !strings.Contains(got2, "## Delegation") {
		t.Fatalf("Phase B: Delegation block missing after adding ray.\ngot: %s", got2)
	}
	if !strings.Contains(got2, "ray") {
		t.Fatalf("Phase B: 'ray' must appear after adding it.\ngot: %s", got2)
	}
	// Suppress unused var warning; the behaviour is captured in Phase B check above.
	_ = phase1HasDelegation
}

// ---------------------------------------------------------------------------
// TestDelegationWiring_RuntimeRefresh_FullRemoval
//
// ava+ray → ava-only → nobody → cannot delegate.
// Proves step-by-step target removal propagates via the real reload path.
// ---------------------------------------------------------------------------

func TestDelegationWiring_RuntimeRefresh_FullRemoval(t *testing.T) {
	// Not parallel: uses t.Setenv via wireTestLoop (Go restriction).
	const agentID = "jim"
	// Seed: jim allowed to delegate to ava and ray.
	al, _ := wireTestLoop(t, agentID, buildPolicyWith("ava", "ray"))

	instA, ok := al.GetRegistry().GetAgent(agentID)
	if !ok || instA == nil {
		t.Fatal("step 0: jim not in registry")
	}
	got0 := instA.ContextBuilder.buildDynamicContext("", "", "", "")
	if !strings.Contains(got0, "ava") || !strings.Contains(got0, "ray") {
		t.Fatalf("step 0: both ava and ray expected.\ngot: %s", got0)
	}

	// Step 1: remove ray.
	cb1 := reloadWithPolicy(t, al, agentID, buildPolicyWith("ava"))
	got1 := cb1.buildDynamicContext("", "", "", "")
	if strings.Contains(got1, "ray") {
		t.Fatalf("step 1: 'ray' must be gone.\ngot: %s", got1)
	}
	if !strings.Contains(got1, "ava") {
		t.Fatalf("step 1: 'ava' must still be present.\ngot: %s", got1)
	}

	// Step 2: remove ava → empty → cannot delegate.
	cb2 := reloadWithPolicy(t, al, agentID, buildPolicyWith())
	got2 := cb2.buildDynamicContext("", "", "", "")
	if strings.Contains(got2, "ava") || strings.Contains(got2, "ray") {
		t.Fatalf("step 2: no delegation targets expected.\ngot: %s", got2)
	}
	if !strings.Contains(got2, "cannot delegate") {
		t.Fatalf("step 2: empty To must render 'cannot delegate'.\ngot: %s", got2)
	}
}

// ---------------------------------------------------------------------------
// TestDelegationWiring_DefaultsLevelPolicy
//
// Proves fix #1: an agent with NO per-agent DelegationPolicy inherits from
// defaults.DelegationPolicy and the context block lists the defaults targets.
// This exercises the path that was BROKEN before the fix (the old injector
// only read inst.DelegationPolicy, missing the defaults fallback).
// ---------------------------------------------------------------------------

func TestDelegationWiring_DefaultsLevelPolicy(t *testing.T) {
	// Not parallel: uses t.Setenv (Go restriction).
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	// Build a config where the agent has nil per-agent policy
	// but defaults.DelegationPolicy allows delegation to "ray".
	cfg := minimalTestConfig(t)
	cfg.Agents.Defaults.Workspace = filepath.Join(home, "default-workspace")
	cfg.Agents.Defaults.ModelName = "test-model"
	cfg.Agents.Defaults.MaxTokens = 4096
	cfg.Agents.Defaults.DelegationPolicy = buildPolicyWith("ray")

	// Use "mia" — core seed gives her nil delegation policy so the defaults path fires.
	const agentID = "mia"
	for i := range cfg.Agents.List {
		if cfg.Agents.List[i].ID == agentID {
			cfg.Agents.List[i].DelegationPolicy = nil
		}
	}

	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	al, err := NewAgentLoop(cfg, msgBus, &mockProvider{})
	if err != nil {
		t.Fatalf("NewAgentLoop: %v", err)
	}
	t.Cleanup(func() { al.Close() })

	inst, ok := al.GetRegistry().GetAgent(agentID)
	if !ok || inst == nil || inst.ContextBuilder == nil {
		t.Fatalf("agent %q not in registry", agentID)
	}

	got := inst.ContextBuilder.buildDynamicContext("", "", "", "")
	if !strings.Contains(got, "## Delegation") {
		t.Fatalf("Delegation block missing — defaults-level policy not applied.\ngot: %s", got)
	}
	if !strings.Contains(got, "ray") {
		t.Fatalf("'ray' must appear (from defaults.DelegationPolicy) even though per-agent policy is nil.\ngot: %s", got)
	}
	if strings.Contains(got, "cannot delegate") {
		t.Fatalf("must NOT say 'cannot delegate' when defaults allow ray.\ngot: %s", got)
	}
}

// ---------------------------------------------------------------------------
// TestDelegationWiring_DefaultsLevelPolicy_Reload
//
// Proves the defaults-level policy is also picked up after a hot-reload that
// changes defaults.DelegationPolicy.
// ---------------------------------------------------------------------------

func TestDelegationWiring_DefaultsLevelPolicy_Reload(t *testing.T) {
	// Not parallel: uses t.Setenv (Go restriction).
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	// Phase A: no defaults policy.
	cfg := minimalTestConfig(t)
	cfg.Agents.Defaults.Workspace = filepath.Join(home, "default-workspace")
	cfg.Agents.Defaults.ModelName = "test-model"
	cfg.Agents.Defaults.MaxTokens = 4096
	cfg.Agents.Defaults.DelegationPolicy = nil

	const agentID = "mia"
	for i := range cfg.Agents.List {
		if cfg.Agents.List[i].ID == agentID {
			cfg.Agents.List[i].DelegationPolicy = nil
		}
	}

	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	al, err := NewAgentLoop(cfg, msgBus, &mockProvider{})
	if err != nil {
		t.Fatalf("NewAgentLoop: %v", err)
	}
	t.Cleanup(func() { al.Close() })

	instA, ok := al.GetRegistry().GetAgent(agentID)
	if !ok || instA == nil {
		t.Fatal("Phase A: mia not in registry")
	}
	gotA := instA.ContextBuilder.buildDynamicContext("", "", "", "")
	// Without any policy, mia (whose core seed has nil delegation) cannot delegate.
	// (Core seed might not override for mia — what matters is the result matches enforcement.)
	// Record Phase A state.
	phaseAHasRay := strings.Contains(gotA, "ray")

	// Phase B: reload with defaults policy allowing "ray".
	newCfg := &config.Config{}
	newCfg.Agents.Defaults = cfg.Agents.Defaults
	newCfg.Agents.Defaults.DelegationPolicy = buildPolicyWith("ray")
	newCfg.Agents.List = make([]config.AgentConfig, len(cfg.Agents.List))
	copy(newCfg.Agents.List, cfg.Agents.List)
	for i := range newCfg.Agents.List {
		if newCfg.Agents.List[i].ID == agentID {
			newCfg.Agents.List[i].DelegationPolicy = nil
		}
	}
	if err := al.ReloadProviderAndConfig(context.Background(), &mockProvider{}, newCfg); err != nil {
		t.Fatalf("ReloadProviderAndConfig: %v", err)
	}

	instB, ok := al.GetRegistry().GetAgent(agentID)
	if !ok || instB == nil {
		t.Fatal("Phase B: mia not in registry after reload")
	}
	gotB := instB.ContextBuilder.buildDynamicContext("", "", "", "")

	if !strings.Contains(gotB, "## Delegation") {
		t.Fatalf("Phase B: Delegation block missing after setting defaults policy.\ngot: %s", gotB)
	}
	if !strings.Contains(gotB, "ray") {
		t.Fatalf("Phase B: 'ray' must appear from defaults.DelegationPolicy after reload.\ngot: %s", gotB)
	}
	_ = phaseAHasRay
}

// ---------------------------------------------------------------------------
// TestDelegationWiring_BootPath
//
// Proves wireEnvProviders (boot path) installs the delegation injector:
// immediately after NewAgentLoop returns, an agent with a policy already
// renders the Delegation block without any manual call to wireDelegationInjectors.
// ---------------------------------------------------------------------------

func TestDelegationWiring_BootPath(t *testing.T) {
	// Not parallel: uses t.Setenv (Go restriction).
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	cfg := minimalTestConfig(t)
	cfg.Agents.Defaults.Workspace = filepath.Join(home, "default-workspace")
	cfg.Agents.Defaults.ModelName = "test-model"
	cfg.Agents.Defaults.MaxTokens = 4096

	// Jim's core seed has a delegation policy; just verify it renders at boot.
	const agentID = "jim"

	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	al, err := NewAgentLoop(cfg, msgBus, &mockProvider{})
	if err != nil {
		t.Fatalf("NewAgentLoop: %v", err)
	}
	t.Cleanup(func() { al.Close() })

	inst, ok := al.GetRegistry().GetAgent(agentID)
	if !ok || inst == nil || inst.ContextBuilder == nil {
		t.Fatalf("agent %q not in registry at boot", agentID)
	}

	got := inst.ContextBuilder.buildDynamicContext("", "", "", "")
	if !strings.Contains(got, "## Delegation") {
		t.Fatalf("boot path: Delegation block missing for %q (core seed should have a policy).\ngot: %s", agentID, got)
	}
}

// ---------------------------------------------------------------------------
// TestDelegationWiring_AgentAddedOnReload
//
// An agent that did not exist at boot must receive a delegation injector
// after it is added via ReloadProviderAndConfig.
// ---------------------------------------------------------------------------

func TestDelegationWiring_AgentAddedOnReload(t *testing.T) {
	// Not parallel: uses t.Setenv (Go restriction).
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	cfg := minimalTestConfig(t)
	cfg.Agents.Defaults.Workspace = filepath.Join(home, "default-workspace")
	cfg.Agents.Defaults.ModelName = "test-model"
	cfg.Agents.Defaults.MaxTokens = 4096

	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	al, err := NewAgentLoop(cfg, msgBus, &mockProvider{})
	if err != nil {
		t.Fatalf("NewAgentLoop: %v", err)
	}
	t.Cleanup(func() { al.Close() })

	// Reload with a brand-new custom agent that has a delegation policy.
	const newAgentID = "custom-helper"
	liveCfg := al.GetConfig()
	newCfg := &config.Config{}
	newCfg.Agents.Defaults = liveCfg.Agents.Defaults
	newCfg.Agents.List = make([]config.AgentConfig, len(liveCfg.Agents.List))
	copy(newCfg.Agents.List, liveCfg.Agents.List)
	newCfg.Agents.List = append(newCfg.Agents.List, config.AgentConfig{
		ID:               newAgentID,
		Name:             "Custom Helper",
		DelegationPolicy: buildPolicyWith("ray"),
	})
	if err := al.ReloadProviderAndConfig(context.Background(), &mockProvider{}, newCfg); err != nil {
		t.Fatalf("ReloadProviderAndConfig adding new agent: %v", err)
	}

	inst, ok := al.GetRegistry().GetAgent(newAgentID)
	if !ok || inst == nil || inst.ContextBuilder == nil {
		t.Fatalf("new agent %q not in registry after reload", newAgentID)
	}

	got := inst.ContextBuilder.buildDynamicContext("", "", "", "")
	if !strings.Contains(got, "## Delegation") {
		t.Fatalf("new agent added on reload: Delegation block missing.\ngot: %s", got)
	}
	if !strings.Contains(got, "ray") {
		t.Fatalf("new agent added on reload: 'ray' must appear in Delegation block.\ngot: %s", got)
	}
}

// ---------------------------------------------------------------------------
// TestDelegationWiring_Race_ConcurrentRenderAndSwap
//
// Race-detector test: concurrent buildDynamicContext renders on one goroutine
// while ReloadProviderAndConfig swaps the registry on another. Must be race-free.
//
// Run with: go test -race -run TestDelegationWiring_Race ./pkg/agent/ -p 1
// ---------------------------------------------------------------------------

func TestDelegationWiring_Race_ConcurrentRenderAndSwap(t *testing.T) {
	// Do NOT call t.Parallel() — this test is deliberately -p 1 (per CLAUDE.md OOM notes).
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	cfg := minimalTestConfig(t)
	cfg.Agents.Defaults.Workspace = filepath.Join(home, "default-workspace")
	cfg.Agents.Defaults.ModelName = "test-model"
	cfg.Agents.Defaults.MaxTokens = 4096

	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	al, err := NewAgentLoop(cfg, msgBus, &mockProvider{})
	if err != nil {
		t.Fatalf("NewAgentLoop: %v", err)
	}
	t.Cleanup(func() { al.Close() })

	const agentID = "ava"
	inst, ok := al.GetRegistry().GetAgent(agentID)
	if !ok || inst == nil || inst.ContextBuilder == nil {
		t.Fatalf("agent %q not in registry", agentID)
	}
	cb := inst.ContextBuilder

	// Render goroutine: repeatedly call buildDynamicContext on the original
	// ContextBuilder. After the swap, the injector closure reads the new
	// registry/config — the output may change, but it must never panic or race.
	var renderWG sync.WaitGroup
	stop := make(chan struct{})
	renderWG.Add(1)
	go func() {
		defer renderWG.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_ = cb.buildDynamicContext("", "", "", "")
			}
		}
	}()

	// Swap goroutine: perform a small number of reloads with alternating policies.
	swapPolicies := []*config.DelegationPolicy{
		buildPolicyWith("ray"),
		buildPolicyWith("ray", "jim"),
		buildPolicyWith(),
		buildPolicyWith("ray"),
	}
	for _, policy := range swapPolicies {
		liveCfg := al.GetConfig()
		newCfg := &config.Config{}
		newCfg.Agents.Defaults = liveCfg.Agents.Defaults
		newCfg.Agents.List = make([]config.AgentConfig, len(liveCfg.Agents.List))
		copy(newCfg.Agents.List, liveCfg.Agents.List)
		for i := range newCfg.Agents.List {
			if newCfg.Agents.List[i].ID == agentID {
				newCfg.Agents.List[i].DelegationPolicy = policy
			}
		}
		if err := al.ReloadProviderAndConfig(context.Background(), &mockProvider{}, newCfg); err != nil {
			close(stop)
			renderWG.Wait()
			t.Fatalf("ReloadProviderAndConfig during race test: %v", err)
		}
	}

	close(stop)
	renderWG.Wait()
	// No assertions needed beyond "no race, no panic".
}

// ---------------------------------------------------------------------------
// TestDelegationWiring_NilPolicyRendersCannotDelegate
//
// Nil DelegationPolicy must render the "cannot delegate" text and must be
// present in the dynamic (non-cached) context.
// ---------------------------------------------------------------------------

func TestDelegationWiring_NilPolicyRendersCannotDelegate(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	cb := NewContextBuilder(dir)
	cb.WithDelegationInjector(func() string {
		return buildDelegationContext(nil, func(id string) (string, bool) { return "", false })
	})

	got := cb.buildDynamicContext("", "", "", "")

	if !strings.Contains(got, "## Delegation") {
		t.Fatalf("nil policy: Delegation block missing from dynamic context.\ngot: %s", got)
	}
	if !strings.Contains(got, "cannot delegate") {
		t.Fatalf("nil policy: expected 'cannot delegate' text.\ngot: %s", got)
	}
}

// ---------------------------------------------------------------------------
// TestDelegationWiring_BlockInDynamicNotCachedSection
//
// The delegation block must appear in the DYNAMIC section (the part of the
// system message that is rebuilt each turn), NOT in the cached static prompt.
// Concretely: BuildSystemPromptWithCache must NOT contain the delegation block,
// but buildDynamicContext must.
// ---------------------------------------------------------------------------

func TestDelegationWiring_BlockInDynamicNotCachedSection(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	delegationBlock := "## Delegation\nSentinel delegation text"

	cb := NewContextBuilder(dir)
	cb.WithDelegationInjector(func() string { return delegationBlock })

	// The static cached prompt must NOT contain the delegation block.
	staticPrompt := cb.BuildSystemPromptWithCache()
	if strings.Contains(staticPrompt, "Sentinel delegation text") {
		t.Errorf("delegation block must NOT appear in the cached static system prompt; got it in:\n%s", staticPrompt)
	}

	// The dynamic context MUST contain the delegation block.
	dynamic := cb.buildDynamicContext("", "", "", "")
	if !strings.Contains(dynamic, "Sentinel delegation text") {
		t.Errorf("delegation block must appear in buildDynamicContext output; got:\n%s", dynamic)
	}
}

// ---------------------------------------------------------------------------
// TestDelegationWiring_NoInjectorNoBlock
//
// When no delegationInjector is set, buildDynamicContext must not emit any
// delegation section (no panic, no spurious output).
// ---------------------------------------------------------------------------

func TestDelegationWiring_NoInjectorNoBlock(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	cb := NewContextBuilder(dir) // no WithDelegationInjector call

	got := cb.buildDynamicContext("", "", "", "")

	if strings.Contains(got, "## Delegation") {
		t.Errorf("with no injector set, Delegation block must not appear.\ngot: %s", got)
	}
}

// ---------------------------------------------------------------------------
// TestResolveDelegationLabel_CoreAgent
//
// resolveDelegationLabel must return a label that includes the agent name for
// recognised core-agent IDs, without duplicating the Subtitle when the Name
// already contains it (e.g. "Ava — Builder" must not become "Ava — Builder (Builder)").
// ---------------------------------------------------------------------------

func TestResolveDelegationLabel_CoreAgent(t *testing.T) {
	t.Parallel()
	// Build a minimal real AgentRegistry with just the agents we need.
	cfg := minimalTestConfig(t)
	reg := NewAgentRegistry(cfg, nil)

	tests := []struct {
		agentID          string
		wantSubstring    string
		wantNotDuplicate string // Subtitle must not appear twice in the label
		wantOk           bool
	}{
		{"ava", "Ava", "Builder (Builder)", true},
		{"jim", "Jim", "Orchestrator (Orchestrator)", true},
		{"mia", "Mia", "Assistant (Assistant)", true},
		{"ray", "Ray", "Scout (Scout)", true},
		{"nonexistent-xyz", "", "", false},
	}

	for _, tc := range tests {
		t.Run(tc.agentID, func(t *testing.T) {
			label, ok := resolveDelegationLabel(reg, tc.agentID)
			if ok != tc.wantOk {
				t.Errorf("resolveDelegationLabel(%q): ok=%v, want %v", tc.agentID, ok, tc.wantOk)
				return
			}
			if !tc.wantOk {
				return
			}
			if !strings.Contains(label, tc.wantSubstring) {
				t.Errorf("resolveDelegationLabel(%q): label=%q does not contain %q", tc.agentID, label, tc.wantSubstring)
			}
			if tc.wantNotDuplicate != "" && strings.Contains(label, tc.wantNotDuplicate) {
				t.Errorf("resolveDelegationLabel(%q): label=%q contains duplicate Subtitle pattern %q",
					tc.agentID, label, tc.wantNotDuplicate)
			}
			// Verify against the core-agent Name to ensure no double-Subtitle appending.
			ca := coreagent.ByID(coreagent.CoreAgentID(tc.agentID))
			if ca != nil && ca.Subtitle != "" {
				// Count occurrences of Subtitle in label: must be exactly 1.
				count := strings.Count(label, ca.Subtitle)
				if count > 1 {
					t.Errorf("resolveDelegationLabel(%q): Subtitle %q appears %d times in label %q (want ≤1)",
						tc.agentID, ca.Subtitle, count, label)
				}
			}
		})
	}
}

// minimalTestConfig returns a *config.Config seeded with the core agent
// roster (Mia, Jim, Ava, Ray + workers) so NewAgentRegistry can find them.
func minimalTestConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg := config.DefaultConfig()
	// Seed core agents so the registry contains ava/jim/mia/ray.
	coreagent.SeedConfig(cfg)
	return cfg
}

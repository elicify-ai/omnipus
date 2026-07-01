// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"strings"
	"testing"

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

// ---------------------------------------------------------------------------
// TestDelegationWiring_RuntimeRefresh
//
// Core requirement: a delegation-policy change that arrives via a new
// injector (simulating a TriggerReload that rebuilt the AgentInstance) must
// be visible on the very next buildDynamicContext call WITHOUT any cache
// invalidation — because the delegation block lives in the dynamic (un-cached)
// path.
// ---------------------------------------------------------------------------

func TestDelegationWiring_RuntimeRefresh(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// ---- Phase A: install policy "allow ava" ----------------------------------
	policyA := buildPolicyWith("ava")

	// Resolver that knows ava and ray.
	resolverA := func(id string) (string, bool) {
		switch id {
		case "ava":
			return "Ava (Builder)", true
		case "ray":
			return "Ray (Scout)", true
		}
		return "", false
	}

	cb := NewContextBuilder(dir)
	cb.WithDelegationInjector(func() string {
		return buildDelegationContext(policyA, resolverA)
	})

	got1 := cb.buildDynamicContext("", "", "", "")

	if !strings.Contains(got1, "## Delegation") {
		t.Fatalf("Phase A: Delegation block missing from dynamic context.\ngot: %s", got1)
	}
	if !strings.Contains(got1, "ava") {
		t.Fatalf("Phase A: expected 'ava' in Delegation block.\ngot: %s", got1)
	}
	if strings.Contains(got1, "ray") {
		t.Errorf("Phase A: 'ray' must NOT appear yet.\ngot: %s", got1)
	}

	// ---- Phase B: mutate policy to add ray (simulates TriggerReload) ----------
	policyA = buildPolicyWith("ava", "ray")

	got2 := cb.buildDynamicContext("", "", "", "")

	// No cache reset needed — delegation is in the dynamic path.
	if !strings.Contains(got2, "ray") {
		t.Fatalf("Phase B: 'ray' must appear after policy mutation (no cache reset required).\ngot: %s", got2)
	}
	if !strings.Contains(got2, "ava") {
		t.Errorf("Phase B: 'ava' must still appear after policy mutation.\ngot: %s", got2)
	}
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
// resolveDelegationLabel must return a label that includes the Subtitle for
// recognised core-agent IDs.
// ---------------------------------------------------------------------------

func TestResolveDelegationLabel_CoreAgent(t *testing.T) {
	t.Parallel()
	// Build a minimal real AgentRegistry with just the agents we need.
	// Use a temporary workspace and a stripped-down config — we only need
	// the label resolution path, not a runnable loop.
	cfg := minimalTestConfig(t)

	reg := NewAgentRegistry(cfg, nil)

	tests := []struct {
		agentID       string
		wantSubstring string
		wantOk        bool
	}{
		{"ava", "Ava", true},
		{"jim", "Jim", true},
		{"mia", "Mia", true},
		{"ray", "Ray", true},
		{"nonexistent-xyz", "", false},
	}

	for _, tc := range tests {
		label, ok := resolveDelegationLabel(reg, tc.agentID)
		if ok != tc.wantOk {
			t.Errorf("resolveDelegationLabel(%q): ok=%v, want %v", tc.agentID, ok, tc.wantOk)
			continue
		}
		if tc.wantOk && !strings.Contains(label, tc.wantSubstring) {
			t.Errorf("resolveDelegationLabel(%q): label=%q does not contain %q", tc.agentID, label, tc.wantSubstring)
		}
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

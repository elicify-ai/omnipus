// loop_delegation_test.go: tests for workspace delegation edges and deny checkers

package agent

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// --- moved from loop.go tests 2026-09-15 ---

// TestAgentExistsChecker_NilRegistryReturnsNil pins the contract of the
// agentExistsChecker(nil) fix: a nil registry must return nil (NOT a non-nil
// closure that lies "does not exist" about every agent). Returning nil makes
// findDelegationEdge's `exists != nil && !exists(target)` guard skip the
// "does not exist" branch entirely, collapsing the nil-registry path onto the
// SAME generic "not permitted" fallback the caller would have hit by omitting
// the variadic arg entirely. A non-nil liar closure that falsely reported
// every id as nonexistent would surface a misleading "does not exist"
// message about an agent that may in fact exist — worse than no probe at all.
//
// This is the unit-level pin; the end-to-end behavior (real registry surfaces
// the "does not exist" message through real production wiring) is covered by
// TestDelegationDistinction_RealWiringThroughCreateTask above.
func TestAgentExistsChecker_NilRegistryReturnsNil(t *testing.T) {
	if got := agentExistsChecker(nil); got != nil {
		t.Fatalf("agentExistsChecker(nil) must return nil so the nil-registry path " +
			"collapses onto the omitted-variadic generic-message fallback; got a non-nil " +
			"closure that would falsely report every agent as nonexistent")
	}

	// Control: a real registry MUST yield a non-nil probe (otherwise the
	// "does not exist" distinction is silently lost for every production
	// caller, not just the nil-registry defensive path).
	seedWorkspaceGraph(t, testWS, true, nil) // OMNIPUS_HOME only; registry built below is inert
	// Build a minimal real AgentLoop so the registry has at least one real
	// agent to find — proves the non-nil return is a real probe that resolves
	// a registered id, not just a non-nil func that ignores its argument.
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: filepath.Join(home, "agents"), DefaultModel: config.DefaultModel{Model: "test-model"}},
			List: []config.AgentConfig{
				{ID: "real-agent", Name: "Real", Type: config.AgentTypeCustom,
					Home: filepath.Join(home, "agents", "real-agent")},
			},
		},
	}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	t.Cleanup(func() { al.Close() })

	probe := agentExistsChecker(al.GetRegistry())
	if probe == nil {
		t.Fatal("agentExistsChecker(non-nil registry) must return a non-nil probe so the " +
			"distinction is reachable through real production wiring")
	}
	if !probe("real-agent") {
		t.Errorf("non-nil-registry probe must report a registered agent as existing")
	}
	if probe("nonexistent-agent") {
		t.Errorf("non-nil-registry probe must report an unregistered agent as nonexistent")
	}
}

// TestAgentExistsChecker_FallsBackToEntityStoreWhenRegistryStale covers the
// UAT-reported defect this fix closes: a UAT run observed `delegate` refuse a
// delegation with "agent %q does not exist" for a target that, in fact,
// existed — the real refusal reason was a missing trust edge, not a missing
// agent. Root cause: agentExistsChecker(registry) consulted ONLY the
// in-memory AgentRegistry, which is refreshed exclusively by the reload
// pipeline (the async, fire-and-forget gateway.go reloadTrigger, or
// UpsertAgentFast's fast path — which itself defers to that same async
// reload when a reload is already in flight). An agent whose entity record
// was just durably written (agentstore.Store.Create always runs
// synchronously ahead of either publish path — the same fact
// UpsertAgentFast's own "DEFECT 1" fix in registry.go relies on) can
// therefore be real on disk before the registry catches up.
//
// This test proves the fix: with a target agent written DIRECTLY to the
// entity store (bypassing the registry entirely, simulating exactly that
// window) and confirmed ABSENT from the live registry, the probe must still
// report it as existing — falling through to the correct "not permitted"
// trust-set denial instead of the misleading "does not exist" one.
func TestAgentExistsChecker_FallsBackToEntityStoreWhenRegistryStale(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: filepath.Join(home, "agents"), DefaultModel: config.DefaultModel{Model: "test-model"}},
			List: []config.AgentConfig{
				{ID: "caller-agent", Name: "Caller", Type: config.AgentTypeCustom,
					Home: filepath.Join(home, "agents", "caller-agent")},
			},
		},
	}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	t.Cleanup(func() { al.Close() })

	// Write the target's entity record DIRECTLY to disk — never through
	// UpsertAgentFast/ReloadProviderAndConfig — so the live registry never
	// learns about it. This is the exact "durable write, registry not yet
	// caught up" window the fix targets.
	if err := agentstore.New(home).Create("just-created-agent", &config.AgentConfig{
		ID: "just-created-agent", Name: "Just Created", Type: config.AgentTypeCustom,
	}); err != nil {
		t.Fatalf("test setup: create entity record: %v", err)
	}

	// Sanity: confirm the registry genuinely does NOT know about this agent —
	// otherwise this test would not be exercising the fallback at all.
	if _, ok := al.GetRegistry().GetAgent("just-created-agent"); ok {
		t.Fatal("test setup invariant violated: just-created-agent must be absent from the " +
			"live registry for this test to exercise the entity-store fallback")
	}

	probe := agentExistsChecker(al.GetRegistry())
	if probe == nil {
		t.Fatal("agentExistsChecker(non-nil registry) must return a non-nil probe")
	}
	if !probe("just-created-agent") {
		t.Fatal("probe must report the agent as existing via the entity-store fallback, even " +
			"though the in-memory registry has not caught up yet — otherwise delegate/switch_agent " +
			"would misreport this agent as nonexistent instead of surfacing its real denial reason")
	}
	// Negative control: an id that exists NOWHERE — not the registry, not the
	// entity store — must still correctly report false.
	if probe("truly-nonexistent-agent") {
		t.Fatal("probe must still report false for an agent that exists in neither the registry " +
			"nor the entity store")
	}
}

// TestEnforceEdgeModeAndDepth_NegativeDepthFailsClosed pins FIX 1: an edge whose
// per-edge Depth cap is NEGATIVE must fail CLOSED (treated as "no onward
// delegation"), never fall open. Before the fix, *Depth < 0 fell through the
// `== 0` special-case and the subsequent `*Depth > 0` guard left depthCap = 0,
// which is interpreted as "uncapped from that source" — silently REMOVING the
// per-edge cap (a wrong, fail-OPEN security verdict). The invariant is
// "depth <= 0 ⇒ this edge grants no further onward delegation".
func TestEnforceEdgeModeAndDepth_NegativeDepthFailsClosed(t *testing.T) {
	for _, neg := range []int{-1, -3, -100} {
		edge := &workspace.DelegationEdge{
			FromAgent: "mia",
			ToAgent:   "ray",
			// Constructed directly as a Go struct literal (not via JSON, so the
			// UnmarshalJSON legacy migration does NOT apply here) — must already
			// carry the current 2-value vocabulary or the mode-membership check
			// itself would deny before the depth logic under test is ever reached.
			Modes: []workspace.DelegationMode{workspace.ModeDirect},
			Depth: intPtr(neg),
		}
		// globalDepthCap = 0 (no global ceiling): the ONLY thing that could deny is
		// the per-edge cap. At chain depth 0 a fail-OPEN bug would ALLOW.
		denial := enforceEdgeModeAndDepth(
			ctxAtDepth(0), edge, "mia", "ray", config.DelegationModeBackground, 0)
		if denial == nil {
			t.Fatalf("negative edge depth %d must FAIL CLOSED (deny onward delegation), got allow", neg)
		}
		if denial.Policy != tools.DenyDepth {
			t.Fatalf("negative edge depth %d must deny on the depth axis, got: %q (%s)",
				neg, denial.Policy, denial.Reason)
		}
	}

	// Control: a POSITIVE cap above the current depth still ALLOWS (the fix must
	// not over-deny legitimate edges).
	okEdge := &workspace.DelegationEdge{
		FromAgent: "mia", ToAgent: "ray", Modes: []workspace.DelegationMode{workspace.ModeDirect}, Depth: intPtr(3),
	}
	if denial := enforceEdgeModeAndDepth(
		ctxAtDepth(0), okEdge, "mia", "ray", config.DelegationModeBackground, 0); denial != nil {
		t.Fatalf("positive edge depth 3 at chain depth 0 must ALLOW, got deny: %+v", denial)
	}
}

func TestCurrentDelegationDepth_DefaultsToZeroWithoutTurnState(t *testing.T) {
	if d := currentDelegationDepth(context.Background()); d != 0 {
		t.Fatalf("expected depth 0 without turnState, got %d", d)
	}
	if d := currentDelegationDepth(ctxAtDepth(4)); d != 4 {
		t.Fatalf("expected depth 4 from turnState, got %d", d)
	}
}

// TestEdgeModeCategory_ExhaustiveOverConfigModes is the direct replacement for
// the retired cross-package drift-guard TestDelegationEdgeValidate_ModesMatchConfig
// (pkg/gateway), which used to pin a 1:1 string-literal equality between
// pkg/workspace's mode constants and config.DelegationMode — an equality that
// no longer holds now that the edge vocabulary is collapsed. This test proves
// EdgeModeCategory (the enforcement-side collapse used by
// enforceEdgeModeAndDepth) is EXHAUSTIVE: every one of the 3 real
// config.DelegationMode values maps to a Valid() workspace.DelegationMode,
// with the expected collapse (Task→ModeTask, Await|Background→ModeDirect).
// EdgeModeCategory is exported specifically so pkg/gateway's
// defaultWorkspaceDelegationEdges (rest_workspace_delegation.go) can call it
// directly for the seed-side collapse instead of maintaining a duplicate
// seedModeCategory — pkg/gateway already imports pkg/agent extensively, so
// there is no package-boundary reason for a second copy of this logic. The
// gateway's own TestDelegationEdgeValidate_ModesMatchConfig exercises the
// SAME EdgeModeCategory function (via the agent import) as an end-to-end
// check against the workspace edge validator; both tests must agree.
func TestEdgeModeCategory_ExhaustiveOverConfigModes(t *testing.T) {
	cases := []struct {
		mode config.DelegationMode
		want workspace.DelegationMode
	}{
		{config.DelegationMode("await"), workspace.ModeDirect},
		{config.DelegationModeBackground, workspace.ModeDirect},
		{config.DelegationModeTask, workspace.ModeTask},
	}
	for _, tc := range cases {
		t.Run(string(tc.mode), func(t *testing.T) {
			got := EdgeModeCategory(tc.mode)
			if got != tc.want {
				t.Fatalf("EdgeModeCategory(%q) = %q, want %q", tc.mode, got, tc.want)
			}
			if !got.Valid() {
				t.Fatalf("EdgeModeCategory(%q) = %q is not a Valid() workspace.DelegationMode", tc.mode, got)
			}
		})
	}
}

// TestNewSysagentDelegationDeny_SelfAllowedNonSelfGated gives the cross-workspace
// task-tool delegation gate (AgentLoop.NewSysagentDelegationDeny) its first direct
// coverage — pr-test-analyzer confirmed it had ZERO. It backs the sysagent
// create_task_in_workspace / update_task_in_workspace tools, so it must enforce the
// SAME task-mode policy as the plain task tools: self-target and empty-target are
// no-op reassignments (allowed), a trusted non-self target is allowed, an un-edged
// non-self target is denied trust_set.
func TestNewSysagentDelegationDeny_SelfAllowedNonSelfGated(t *testing.T) {
	const callerID = "jim"
	seedWorkspaceGraph(t, testWS, true, []graphEdge{
		edge("jim", "ava", []string{"task"}, nil), // jim→ava trusted (task)
	})
	al, _ := wireTestLoopWithGraph(t, callerID)
	deny := al.NewSysagentDelegationDeny()

	// Self-target: a no-op task reassignment to the owner — allowed.
	if d := deny(ctxWS(testWS, 0), callerID, callerID); d != nil {
		t.Fatalf("self-target task reassignment must be allowed, got deny: %+v", d)
	}
	// Empty target: no-op assignment — allowed.
	if d := deny(ctxWS(testWS, 0), callerID, ""); d != nil {
		t.Fatalf("empty target must be a no-op (allowed), got deny: %+v", d)
	}
	// Trusted non-self (jim→ava task edge present): allowed.
	if d := deny(ctxWS(testWS, 0), callerID, "ava"); d != nil {
		t.Fatalf("jim→ava is task-edged and must be allowed, got deny: %+v", d)
	}
	// Un-edged non-self (no jim→ray edge): DENIED trust_set.
	d := deny(ctxWS(testWS, 0), callerID, "ray")
	if d == nil {
		t.Fatal("jim→ray (no edge) must be DENIED, got allow")
	}
	if d.Policy != tools.DenyTrustSet {
		t.Fatalf("expected trust_set denial for un-edged cross-workspace target, got: %q (%s)",
			d.Policy, d.Reason)
	}
}

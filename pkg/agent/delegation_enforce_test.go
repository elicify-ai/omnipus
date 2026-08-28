package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// writeWorkspaceFileForTest drops an additional workspace file into an EXISTING
// home (one already created by seedWorkspaceGraph). Unlike seedWorkspaceGraph it
// does NOT reset OMNIPUS_HOME — it is for seeding a SECOND workspace alongside the
// first so a single test can exercise per-workspace isolation.
func writeWorkspaceFileForTest(t *testing.T, home, wsID string, isDefault bool, edges []graphEdge) {
	t.Helper()
	writeGraphFiles(t, home, wsID, isDefault, edges)
}

// intPtr is a local helper for building *int policy/depth fields.
func intPtr(n int) *int { return &n }

// ctxAtDepth returns a context carrying a turnState at the given delegation depth.
func ctxAtDepth(depth int) context.Context {
	return withTurnState(context.Background(), &turnState{depth: depth})
}

// This file tests per-workspace, graph-authoritative delegation enforcement.
//
// MIGRATED from the prior config.DelegationPolicy model: the per-workspace
// delegation graph (workspaces/<id>.json → Delegation[]) is now the SOLE runtime
// authority. Each test seeds the governing workspace's edge set on disk (via
// seedWorkspaceGraph, which also points OMNIPUS_HOME at a temp dir) and binds the
// turn to that workspace through the context (ctxWS). buildDelegationDenyChecker
// no longer accepts an AgentConfig at all — the graph decides.

const testWS = "01JWMYWORKSPACE0000000001"

// --- targeted delegation gate (spawn = "background", task = "task") ---

func TestDelegationDenyChecker_AllowedWhenModeTrustDepthPermit(t *testing.T) {
	// Graph: Mia→Ray allowed in direct mode, edge depth cap 3.
	seedWorkspaceGraph(t, testWS, true, []graphEdge{
		edge("mia", "ray", []string{"direct"}, intPtr(3)),
	})
	check := buildDelegationDenyCheckerForDelegate("mia", config.AgentDefaults{}, config.DelegationModeBackground)

	// depth 0 < cap 3, edge exists, mode permitted → allowed (nil).
	if denial := check(ctxWS(testWS, 0), "ray"); denial != nil {
		t.Fatalf("expected delegation allowed, got deny: %+v", denial)
	}
}

func TestDelegationDenyChecker_DeniedWhenTargetNotTrusted(t *testing.T) {
	// Graph only authorizes Mia→Ray; Mia→Ava has no edge.
	seedWorkspaceGraph(t, testWS, true, []graphEdge{
		edge("mia", "ray", []string{"direct"}, nil),
	})
	check := buildDelegationDenyCheckerForDelegate("mia", config.AgentDefaults{}, config.DelegationModeBackground)

	denial := check(ctxWS(testWS, 0), "ava") // no Mia→Ava edge
	if denial == nil {
		t.Fatal("expected delegation denied for un-edged target, got allow")
	}
	if denial.Policy != tools.DenyTrustSet {
		t.Fatalf("expected trust_set policy, got: %q (%s)", denial.Policy, denial.Reason)
	}
}

// TestDelegationDenyChecker_DistinguishesNotFoundFromNotTrusted proves issue
// #588 finding N7 is fixed: the batched UAT re-run found that delegating to
// an agent that EXISTS but has no trust edge from the caller, and delegating
// to a genuinely NONEXISTENT agent, returned byte-identical generic denial
// text. Both cases must still DENY with the SAME Policy (DenyTrustSet) — this
// test is a message-clarity check ONLY, never a deny/allow-logic check — but
// the Reason text must now let a caller tell "not trusted" apart from
// "doesn't exist".
func TestDelegationDenyChecker_DistinguishesNotFoundFromNotTrusted(t *testing.T) {
	// Same fixture as TestDelegationDenyChecker_DeniedWhenTargetNotTrusted
	// (graph only authorizes Mia→Ray), but this test ALSO wires an
	// agentExists probe so it can exercise both denial paths.
	seedWorkspaceGraph(t, testWS, true, []graphEdge{
		edge("mia", "ray", []string{"direct"}, nil),
	})

	// registered stands in for the live agent registry: "ava" is a real,
	// addressable agent that simply has no trust edge from "mia";
	// "nonexistent-agent" is not registered at all.
	registered := map[string]bool{"mia": true, "ray": true, "ava": true}
	agentExists := func(id string) bool { return registered[id] }

	check := buildDelegationDenyCheckerForDelegate(
		"mia", config.AgentDefaults{}, config.DelegationModeBackground, agentExists,
	)

	// Case 1: target EXISTS but has no trust edge from mia — "not trusted".
	notTrustedDenial := check(ctxWS(testWS, 0), "ava")
	if notTrustedDenial == nil {
		t.Fatal("expected delegation denied for un-edged (but real) target, got allow")
	}
	if notTrustedDenial.Policy != tools.DenyTrustSet {
		t.Fatalf("expected trust_set policy, got: %q (%s)", notTrustedDenial.Policy, notTrustedDenial.Reason)
	}
	if strings.Contains(notTrustedDenial.Reason, "does not exist") {
		t.Errorf("target %q IS a real agent — denial must not claim it doesn't exist, got %q",
			"ava", notTrustedDenial.Reason)
	}
	if !strings.Contains(notTrustedDenial.Reason, "not permitted") {
		t.Errorf("expected a 'not permitted in this workspace' denial for a real-but-untrusted target, got %q",
			notTrustedDenial.Reason)
	}

	// Case 2: target does NOT exist at all — must read differently from case 1.
	notFoundDenial := check(ctxWS(testWS, 0), "nonexistent-agent")
	if notFoundDenial == nil {
		t.Fatal("expected delegation denied for nonexistent target, got allow")
	}
	if notFoundDenial.Policy != tools.DenyTrustSet {
		t.Fatalf("expected trust_set policy, got: %q (%s)", notFoundDenial.Policy, notFoundDenial.Reason)
	}
	if !strings.Contains(notFoundDenial.Reason, "does not exist") {
		t.Errorf("expected a 'does not exist' denial for a genuinely nonexistent target, got %q",
			notFoundDenial.Reason)
	}

	// The exact regression N7 reported: the two messages must differ.
	if notTrustedDenial.Reason == notFoundDenial.Reason {
		t.Fatalf("N7 regression: 'exists but untrusted' and 'does not exist' produced the SAME denial text: %q",
			notTrustedDenial.Reason)
	}
}

// TestDelegationDistinction_RealWiringThroughCreateTask is the INTEGRATION
// counterpart to TestDelegationDenyChecker_DistinguishesNotFoundFromNotTrusted
// (above): it proves the "agent does not exist" vs "agent exists but is not
// trusted" message distinction arrives through the REAL production wiring —
// the agentExistsChecker(registry) closure baked into create_task's deny
// checker at registerSharedTools time — not just through a synthetic
// agentExists probe exercised in isolation.
//
// Coverage gap this closes: the unit test above drives the pure-function
// logic with a hand-written `registered` map as the existence probe, but
// NO test drove the distinction through a real AgentLoop with the real
// agentExistsChecker(registry) closure production wires at the call site
// (loop.go registerSharedTools). If that trailing-arg call site were ever
// dropped, the variadic would default to nil and BOTH messages would
// collapse to the generic "not permitted" — but the synthetic-probe unit
// test would stay green, exactly the "tested with a seam production never
// wires" failure class that caused the original distinction bug. Failure
// of EITHER case below is a real regression in production wiring, not
// just in the pure-function logic.
func TestDelegationDistinction_RealWiringThroughCreateTask(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         filepath.Join(home, "agents"),
				DefaultModel: config.DefaultModel{Model: "test-model"},
			},
			List: []config.AgentConfig{
				{
					ID:   "caller-agent",
					Name: "Caller", Type: config.AgentTypeCustom,
					Home: filepath.Join(home, "agents", "caller-agent"),
				},
				{
					// worker-agent IS a real registered agent — it simply has
					// NO trust edge from caller-agent in the workspace graph.
					// The shared harness workspace (testHarnessWorkspaceMembershipID,
					// seeded by mustNewAgentLoop -> ensureTestWorkspaceMembership)
					// populates core_team only, never delegation edges.
					ID:   "worker-agent",
					Name: "Worker", Type: config.AgentTypeCustom,
					Home: filepath.Join(home, "agents", "worker-agent"),
				},
			},
		},
	}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	t.Cleanup(func() { al.Close() })

	callerInst, ok := al.GetRegistry().GetAgent("caller-agent")
	if !ok {
		t.Fatal("caller-agent not registered")
	}
	// Sanity: worker-agent is in the SAME registry the production deny
	// checker's agentExistsChecker(registry) closure will consult — if this
	// fails, the "extant-but-untrusted" case below cannot prove what it
	// claims to prove.
	if _, ok := al.GetRegistry().GetAgent("worker-agent"); !ok {
		t.Fatal("worker-agent not registered — extant-but-untrusted case cannot be exercised")
	}

	// Bind BOTH the caller agent id and the shared harness workspace on ctx.
	// create_task reads the caller from tools.ToolAgentID(ctx) and resolves
	// the governing workspace from tools.ToolWorkspaceID(ctx); the workspace
	// must be bound explicitly because testHarnessWorkspaceMembershipID is
	// never flagged is_default (so the no-bound fallback has nothing to find).
	ctx := tools.WithWorkspaceID(
		tools.WithAgentID(context.Background(), "caller-agent"),
		testHarnessWorkspaceMembershipID,
	)

	// Case 1 — target genuinely does NOT exist: the real
	// agentExistsChecker(registry) closure (wired at registerSharedTools)
	// must report it as nonexistent, surfacing the "does not exist"
	// message. If the wiring were ever dropped (the trailing arg defaulted
	// to nil), this would collapse to the generic "not permitted" message
	// and the assertion below would fail — proving the wiring is live.
	nonexistentResult := callerInst.Tools.Execute(ctx, "create_task", map[string]any{
		"title":    "x",
		"prompt":   "x",
		"agent_id": "genuinely-nonexistent-agent",
		"criteria": []any{map[string]any{"kind": "prose", "text": "done"}},
	})
	if nonexistentResult == nil || !nonexistentResult.IsError {
		t.Fatalf("create_task against a nonexistent agent must be denied, got %+v", nonexistentResult)
	}
	if !strings.Contains(nonexistentResult.ForLLM, "does not exist") {
		t.Fatalf("production wiring regression: delegating to a nonexistent agent must surface "+
			"the 'does not exist' message, got %q", nonexistentResult.ForLLM)
	}

	// Case 2 — target EXISTS but has no trust edge from caller-agent: the
	// SAME agentExistsChecker(registry) closure must report it as extant,
	// surfacing the generic "not permitted in this workspace" message
	// instead. A "does not exist" message here would mean the registry
	// lookup itself is broken (the agent IS registered — see the sanity
	// check above).
	untrustedResult := callerInst.Tools.Execute(ctx, "create_task", map[string]any{
		"title":    "x",
		"prompt":   "x",
		"agent_id": "worker-agent",
		"criteria": []any{map[string]any{"kind": "prose", "text": "done"}},
	})
	if untrustedResult == nil || !untrustedResult.IsError {
		t.Fatalf("create_task against an extant-but-untrusted agent must be denied, got %+v", untrustedResult)
	}
	if !strings.Contains(untrustedResult.ForLLM, "not permitted") {
		t.Fatalf("production wiring regression: delegating to an extant-but-untrusted agent must "+
			"surface the 'not permitted' message, got %q", untrustedResult.ForLLM)
	}
	if strings.Contains(untrustedResult.ForLLM, "does not exist") {
		t.Fatalf("worker-agent IS a real registered agent — denial must NOT claim it doesn't exist, got %q",
			untrustedResult.ForLLM)
	}

	// The two denial reasons MUST differ — the load-bearing assertion that
	// proves the distinction is preserved end-to-end through the real wiring.
	// Comparing the parsed `reason` field (not the whole JSON) avoids
	// false-positive equality from the shared tool/policy/target fields.
	if delegationReasonFromResult(t, nonexistentResult) == delegationReasonFromResult(t, untrustedResult) {
		t.Fatalf("production wiring regression: 'nonexistent target' and 'extant-but-untrusted "+
			"target' produced the SAME denial reason %q — the agentExistsChecker(registry) "+
			"wiring is not live (the variadic defaulted to nil and both cases collapsed to "+
			"the generic 'not permitted' message)",
			delegationReasonFromResult(t, nonexistentResult))
	}
}

// delegationReasonFromResult extracts the "reason" field from a JSON-encoded
// DelegationFailure result (DelegationDeniedResult, pkg/tools/result.go) so a
// test can compare reasons without depending on the wire-type JSON schema. On
// a non-JSON result it falls back to the raw ForLLM so a failure still
// produces a useful equality-comparison message.
func delegationReasonFromResult(t *testing.T, r *tools.ToolResult) string {
	t.Helper()
	var p struct {
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(r.ForLLM), &p); err != nil {
		return r.ForLLM
	}
	return p.Reason
}

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

func TestDelegationDenyChecker_DeniedWhenModeForbidden(t *testing.T) {
	// Edge Mia→Ray permits only the "task" mode; a background spawn must be denied.
	seedWorkspaceGraph(t, testWS, true, []graphEdge{
		edge("mia", "ray", []string{"task"}, nil),
	})
	check := buildDelegationDenyCheckerForDelegate("mia", config.AgentDefaults{}, config.DelegationModeBackground)

	denial := check(ctxWS(testWS, 0), "ray") // edge exists, but mode not allowed
	if denial == nil {
		t.Fatal("expected delegation denied for forbidden mode, got allow")
	}
	if denial.Policy != tools.DenyMode {
		t.Fatalf("expected mode policy, got: %q (%s)", denial.Policy, denial.Reason)
	}
}

func TestDelegationDenyChecker_AllowedWhenEmptyModesMeansAll(t *testing.T) {
	// Empty Modes = all delegation modes allowed for this edge.
	seedWorkspaceGraph(t, testWS, true, []graphEdge{
		edge("mia", "ray", nil, nil),
	})
	for _, mode := range []config.DelegationMode{
		config.DelegationModeBackground, config.DelegationModeTask, config.DelegationModeAwait,
	} {
		// Non-self target ("ray"), so the exemption is behaviorally irrelevant here;
		// use each mode's real wiring wrapper (task→ForTaskReassignment, else ForDelegate).
		check := buildDelegationDenyCheckerForDelegate("mia", config.AgentDefaults{}, mode)
		if mode == config.DelegationModeTask {
			check = buildDelegationDenyCheckerForTaskReassignment("mia", config.AgentDefaults{}, mode)
		}
		if denial := check(ctxWS(testWS, 0), "ray"); denial != nil {
			t.Fatalf("empty modes must allow %q, got deny: %+v", mode, denial)
		}
	}
}

func TestDelegationDenyChecker_DeniedWhenDepthExceeded(t *testing.T) {
	// Edge depth cap 2; current chain depth 2 → at the cap → deny further delegation.
	seedWorkspaceGraph(t, testWS, true, []graphEdge{
		edge("mia", "ray", []string{"direct"}, intPtr(2)),
	})
	check := buildDelegationDenyCheckerForDelegate("mia", config.AgentDefaults{}, config.DelegationModeBackground)

	if denial := check(ctxWS(testWS, 1), "ray"); denial != nil {
		t.Fatalf("depth 1 < cap 2 should be allowed, got deny: %+v", denial)
	}
	denial := check(ctxWS(testWS, 2), "ray") // depth 2 >= cap 2 → deny
	if denial == nil {
		t.Fatal("expected delegation denied at depth cap, got allow")
	}
	if denial.Policy != tools.DenyDepth {
		t.Fatalf("expected depth policy, got: %q (%s)", denial.Policy, denial.Reason)
	}
}

func TestDelegationDenyChecker_EdgeDepthZeroForbidsOnward(t *testing.T) {
	// An edge depth of exactly 0 means "no onward delegation" — denied even at
	// chain depth 0.
	seedWorkspaceGraph(t, testWS, true, []graphEdge{
		edge("mia", "ray", []string{"direct"}, intPtr(0)),
	})
	check := buildDelegationDenyCheckerForDelegate("mia", config.AgentDefaults{}, config.DelegationModeBackground)

	denial := check(ctxWS(testWS, 0), "ray")
	if denial == nil || denial.Policy != tools.DenyDepth {
		t.Fatalf("edge depth 0 must deny onward delegation, got: %+v", denial)
	}
}

func TestDelegationDenyChecker_GlobalCeilingTightensEdge(t *testing.T) {
	// Edge sets no depth (inherit); the global SubTurn.MaxDepth=1 ceiling caps the
	// chain at depth 1.
	seedWorkspaceGraph(t, testWS, true, []graphEdge{
		edge("mia", "ray", []string{"direct"}, nil),
	})
	defaults := config.AgentDefaults{}
	defaults.SubTurn.MaxDepth = 1
	check := buildDelegationDenyCheckerForDelegate("mia", defaults, config.DelegationModeBackground)

	if denial := check(ctxWS(testWS, 0), "ray"); denial != nil {
		t.Fatalf("depth 0 < global cap 1 should allow, got deny: %+v", denial)
	}
	if denial := check(ctxWS(testWS, 1), "ray"); denial == nil || denial.Policy != tools.DenyDepth {
		t.Fatalf("depth 1 >= global cap 1 must deny (depth), got: %+v", denial)
	}
}

func TestDelegationDenyChecker_UntargetedSkipsTrustButEnforcesMode(t *testing.T) {
	// No explicit target (agent_id == ""): trust resolves to "has any outgoing
	// edge that permits this mode". The only edge permits task, not background, so
	// a background untargeted spawn is denied (mode).
	seedWorkspaceGraph(t, testWS, true, []graphEdge{
		edge("mia", "ray", []string{"task"}, nil),
	})
	check := buildDelegationDenyCheckerForDelegate("mia", config.AgentDefaults{}, config.DelegationModeBackground)

	denial := check(ctxWS(testWS, 0), "")
	if denial == nil || denial.Policy != tools.DenyMode {
		t.Fatalf("expected mode denial for untargeted background spawn, got: %+v", denial)
	}
}

func TestDelegationDenyChecker_SelfAssignmentSkipsGraph(t *testing.T) {
	// Self-assignment (target == caller) is NOT delegation: Mia creating/reassigning
	// a task to Mia must NOT be denied even though there is no Mia→Mia edge. Regression
	// for the UAT bug where task_create to self was denied with trust_set.
	seedWorkspaceGraph(t, testWS, true, []graphEdge{
		edge("mia", "worker", []string{"task"}, nil),
	})
	check := buildDelegationDenyCheckerForTaskReassignment("mia", config.AgentDefaults{}, config.DelegationModeTask)

	if denial := check(ctxWS(testWS, 0), "mia"); denial != nil {
		t.Fatalf("self-assignment must be allowed (not delegation), got deny: %+v", denial)
	}
	// Sanity: a DIFFERENT un-edged target is still denied.
	if denial := check(ctxWS(testWS, 0), "ava"); denial == nil {
		t.Fatal("delegation to an un-edged OTHER agent must still be denied")
	}
}

// --- default-workspace resolution (no workspace bound to the turn) ---

func TestDelegationDenyChecker_UsesDefaultWorkspaceWhenUnbound(t *testing.T) {
	// The default (is_default) workspace seeds Mia→worker. A turn with NO
	// workspace_id must resolve to it: Mia→worker allowed, Mia→ava denied.
	seedWorkspaceGraph(t, testWS, true, []graphEdge{
		edge("mia", "worker", nil, nil),
	})
	check := buildDelegationDenyCheckerForTaskReassignment("mia", config.AgentDefaults{}, config.DelegationModeTask)

	// ctxAtDepth carries NO workspace_id → falls back to the is_default workspace.
	if denial := check(ctxAtDepth(0), "worker"); denial != nil {
		t.Fatalf("default-workspace edge Mia→worker must be allowed, got deny: %+v", denial)
	}
	if denial := check(ctxAtDepth(0), "ava"); denial == nil || denial.Policy != tools.DenyTrustSet {
		t.Fatalf("Mia→ava (no edge in default ws) must deny trust_set, got: %+v", denial)
	}
}

// --- fail-closed paths ---

func TestDelegationDenyChecker_FailsClosedWhenNoDefaultWorkspace(t *testing.T) {
	// OMNIPUS_HOME points at an empty home: no workspace at all, and the turn
	// carries no workspace_id. The gate must DENY (fail-closed), never allow.
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	check := buildDelegationDenyCheckerForTaskReassignment("mia", config.AgentDefaults{}, config.DelegationModeTask)

	denial := check(ctxAtDepth(0), "worker")
	if denial == nil {
		t.Fatal("expected fail-closed DENY when no default workspace exists, got allow")
	}
	if denial.Policy != tools.DenyTrustSet {
		t.Fatalf("expected trust_set on no-default-workspace denial, got: %q", denial.Policy)
	}
}

func TestDelegationDenyChecker_FailsClosedWhenWorkspaceUnreadable(t *testing.T) {
	// The turn is bound to a workspace id that does NOT exist on disk:
	// ReadDelegation errors → the gate must DENY (fail-closed).
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	check := buildDelegationDenyCheckerForTaskReassignment("mia", config.AgentDefaults{}, config.DelegationModeTask)

	denial := check(ctxWS("01JWMISSINGWORKSPACE00001", 0), "worker")
	if denial == nil {
		t.Fatal("expected fail-closed DENY when the bound workspace is unreadable, got allow")
	}
	if denial.Policy != tools.DenyTrustSet {
		t.Fatalf("expected trust_set on unreadable-graph denial, got: %q", denial.Policy)
	}
}

// TestDelegationDenyChecker_ConfigPolicyNoLongerAffectsRuntime proves the
// per-agent config.DelegationPolicy is NO LONGER consulted: buildDelegationDenyChecker
// doesn't even accept an AgentConfig anymore, so only the graph (which has no
// Mia→ava edge) decides, and the delegation is DENIED.
func TestDelegationDenyChecker_ConfigPolicyNoLongerAffectsRuntime(t *testing.T) {
	seedWorkspaceGraph(t, testWS, true, []graphEdge{
		edge("mia", "worker", nil, nil), // graph: Mia→worker only
	})
	check := buildDelegationDenyCheckerForTaskReassignment("mia", config.AgentDefaults{}, config.DelegationModeTask)

	// The graph has no Mia→ava edge → DENY.
	if denial := check(ctxWS(testWS, 0), "ava"); denial == nil {
		t.Fatal("graph has no Mia→ava edge; delegation must be denied")
	}
	// And the graph edge (Mia→worker) is honored.
	if denial := check(ctxWS(testWS, 0), "worker"); denial != nil {
		t.Fatalf("graph edge Mia→worker must be honored, got deny: %+v", denial)
	}
}

// --- synchronous subagent gate (mode = "await") ---
//
// buildSubagentDelegationDenyChecker was removed; these tests now use
// buildDelegationDenyChecker with DelegationModeAwait directly, matching the
// wiring site in registerSharedTools. The checker now accepts a targetAgentID;
// untargeted calls pass "" (same as the previous no-arg signature).

func TestSubagentDelegationDenyChecker_AllowedWhenPermitted(t *testing.T) {
	seedWorkspaceGraph(t, testWS, true, []graphEdge{
		edge("mia", "ray", []string{"direct"}, intPtr(3)),
	})
	check := buildDelegationDenyCheckerForDelegate("mia", config.AgentDefaults{}, config.DelegationModeAwait)

	if denial := check(ctxWS(testWS, 0), ""); denial != nil {
		t.Fatalf("expected sync delegation allowed, got deny: %+v", denial)
	}
}

func TestSubagentDelegationDenyChecker_DeniedWhenNoEdges(t *testing.T) {
	// Caller has no outgoing edge in the graph → cannot delegate at all.
	seedWorkspaceGraph(t, testWS, true, []graphEdge{
		edge("jim", "ray", nil, nil), // an edge, but not FROM mia
	})
	check := buildDelegationDenyCheckerForDelegate("mia", config.AgentDefaults{}, config.DelegationModeAwait)

	denial := check(ctxWS(testWS, 0), "")
	if denial == nil || denial.Policy != tools.DenyTrustSet {
		t.Fatalf("expected trust_set denial with no outgoing edge, got: %+v", denial)
	}
}

// TestSubagentDelegationDenyChecker_DeniedWhenModeForbidden's ORIGINAL premise
// — an edge permitting only "background" would deny an "await" call — is GONE
// by design: the mode collapse means "background" and "await" are no longer
// independently gated by the trust edge at all (EdgeModeCategory maps both to
// the same ModeDirect category) — an edge that allows either now allows both,
// since that distinction is purely a delegate-tool call parameter, not a trust
// distinction. Repurposed to the still-valid analog: an edge permitting only
// "task" denies a sync ("await") delegation attempt.
func TestSubagentDelegationDenyChecker_DeniedWhenModeForbidden(t *testing.T) {
	// Edge exists but permits only the "task" mode category — a sync/await
	// delegation attempt does not match it.
	seedWorkspaceGraph(t, testWS, true, []graphEdge{
		edge("mia", "ray", []string{"task"}, nil), // await/direct forbidden
	})
	check := buildDelegationDenyCheckerForDelegate("mia", config.AgentDefaults{}, config.DelegationModeAwait)

	denial := check(ctxWS(testWS, 0), "")
	if denial == nil || denial.Policy != tools.DenyMode {
		t.Fatalf("expected mode denial for await against a task-only edge, got: %+v", denial)
	}
}

func TestSubagentDelegationDenyChecker_DeniedWhenDepthExceeded(t *testing.T) {
	seedWorkspaceGraph(t, testWS, true, []graphEdge{
		edge("mia", "ray", []string{"direct"}, intPtr(1)),
	})
	check := buildDelegationDenyCheckerForDelegate("mia", config.AgentDefaults{}, config.DelegationModeAwait)

	denial := check(ctxWS(testWS, 1), "") // depth 1 >= edge cap 1
	if denial == nil || denial.Policy != tools.DenyDepth {
		t.Fatalf("expected depth denial, got: %+v", denial)
	}
}

func TestSubagentDelegationDenyChecker_FailsClosedWhenNoWorkspace(t *testing.T) {
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	check := buildDelegationDenyCheckerForDelegate("mia", config.AgentDefaults{}, config.DelegationModeAwait)

	if denial := check(ctxAtDepth(0), ""); denial == nil {
		t.Fatal("expected fail-closed DENY for sync subagent with no default workspace, got allow")
	}
}

// TestDelegationGraphFlipsWithoutRebuild is the round-trip proof that editing the
// graph takes effect on the next call with NO checker rebuild: the SAME checker
// closure denies before the edge is added and allows after, because it reads the
// graph per-call.
func TestDelegationGraphFlipsWithoutRebuild(t *testing.T) {
	home := seedWorkspaceGraph(t, testWS, true, []graphEdge{
		edge("jim", "ava", []string{"direct"}, nil), // initially: jim→ava only
	})
	check := buildDelegationDenyCheckerForDelegate("jim", config.AgentDefaults{}, config.DelegationModeBackground)

	// Before the edit: jim→ray denied (no edge).
	if denial := check(ctxWS(testWS, 0), "ray"); denial == nil {
		t.Fatal("expected jim→ray denied before edge added")
	}

	// Edit the graph on disk to add jim→ray (no checker rebuild).
	rewriteWorkspaceGraph(t, home, testWS, true, []graphEdge{
		edge("jim", "ava", []string{"direct"}, nil),
		edge("jim", "ray", []string{"direct"}, nil),
	})

	// The SAME checker now allows jim→ray — proving per-call graph reads.
	if denial := check(ctxWS(testWS, 0), "ray"); denial != nil {
		t.Fatalf("expected jim→ray ALLOWED after graph edit (no rebuild), got deny: %+v", denial)
	}
}

// TestDelegationDenyChecker_CrossWorkspaceDenied pins the headline property of
// commit 822202ad: delegation authority is per-WORKSPACE, not global. The SAME
// checker closure must ALLOW mia→ray when the turn is bound to workspace WS-A
// (which has the mia→ray edge) and DENY it when bound to workspace WS-B (which
// has NO edges) — proving an edge in one workspace grants no authority in
// another. Existing tests all use a single workspace, so this isolation property
// had no direct coverage.
func TestDelegationDenyChecker_CrossWorkspaceDenied(t *testing.T) {
	const (
		wsA = "01JWMYWORKSPACEAAAAAAAAAAA"
		wsB = "01JWMYWORKSPACEBBBBBBBBBBB"
	)
	// seedWorkspaceGraph sets OMNIPUS_HOME to a fresh temp dir and writes WS-A
	// (NOT default) with the single mia→ray edge. Capture the home so we can drop
	// a SECOND workspace file (WS-B, no edges) into the same home.
	home := seedWorkspaceGraph(t, wsA, false, []graphEdge{
		edge("mia", "ray", []string{"direct"}, nil),
	})
	// WS-B in the same home, no delegation edges.
	writeWorkspaceFileForTest(t, home, wsB, false, nil)

	// ONE checker, used against both workspaces. The per-call graph read is what
	// makes the verdict workspace-scoped.
	check := buildDelegationDenyCheckerForDelegate("mia", config.AgentDefaults{}, config.DelegationModeBackground)

	// Bound to WS-A: the mia→ray edge authorizes the delegation → ALLOW.
	if denial := check(ctxWS(wsA, 0), "ray"); denial != nil {
		t.Fatalf("mia→ray must be ALLOWED when bound to WS-A (edge present), got deny: %+v", denial)
	}

	// Bound to WS-B with the IDENTICAL checker: WS-B has no mia→ray edge, so the
	// per-workspace graph DENIES (trust_set). An edge in WS-A grants NO authority
	// in WS-B.
	denial := check(ctxWS(wsB, 0), "ray")
	if denial == nil {
		t.Fatal("mia→ray must be DENIED when bound to WS-B (no edge); cross-workspace authority leaked")
	}
	if denial.Policy != tools.DenyTrustSet {
		t.Fatalf("expected trust_set denial in WS-B (no edge), got: %q (%s)", denial.Policy, denial.Reason)
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

// TestProcessOptions_SeedsTaskRunDepth proves that a task run started at a
// non-zero generation seeds the root turnState depth so currentDelegationDepth
// (and hence the per-edge await/background depth gate) is non-zero INSIDE the
// task run. It mirrors the seeding runAgentLoop performs from
// opts.InitialDelegationDepth.
func TestProcessOptions_SeedsTaskRunDepth(t *testing.T) {
	agent := &AgentInstance{ID: "mia"}
	opts := processOptions{SessionKey: "k", InitialDelegationDepth: 3}
	ts := newTurnState(agent, opts, turnEventScope{turnID: "tid"})
	if opts.InitialDelegationDepth > 0 {
		ts.depth = opts.InitialDelegationDepth
	}
	ctx := withTurnState(context.Background(), ts)
	if d := currentDelegationDepth(ctx); d != 3 {
		t.Fatalf("expected seeded delegation depth 3 inside task run, got %d", d)
	}

	rootTS := newTurnState(agent, processOptions{SessionKey: "k"}, turnEventScope{turnID: "tid2"})
	if d := currentDelegationDepth(withTurnState(context.Background(), rootTS)); d != 0 {
		t.Fatalf("expected root run depth 0, got %d", d)
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
		{config.DelegationModeAwait, workspace.ModeDirect},
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

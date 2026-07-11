package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

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
	wsDir := filepath.Join(home, "workspaces")
	if err := os.MkdirAll(wsDir, 0o755); err != nil {
		t.Fatalf("mkdir workspaces: %v", err)
	}
	rec := testWorkspaceRecord{ID: wsID, IsDefault: isDefault, Delegation: edges}
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal workspace record: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wsDir, wsID+".json"), data, 0o644); err != nil {
		t.Fatalf("write workspace file: %v", err)
	}
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
	// Graph: Mia→Ray allowed in background mode, edge depth cap 3.
	seedWorkspaceGraph(t, testWS, true, []graphEdge{
		edge("mia", "ray", []string{"background"}, intPtr(3)),
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
		edge("mia", "ray", []string{"background"}, nil),
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
		edge("mia", "ray", []string{"background"}, intPtr(2)),
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
		edge("mia", "ray", []string{"background"}, intPtr(0)),
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
		edge("mia", "ray", []string{"background"}, nil),
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
		edge("mia", "ray", []string{"await"}, intPtr(3)),
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

func TestSubagentDelegationDenyChecker_DeniedWhenModeForbidden(t *testing.T) {
	// Edge exists but does not permit the "await" mode.
	seedWorkspaceGraph(t, testWS, true, []graphEdge{
		edge("mia", "ray", []string{"background"}, nil), // await forbidden
	})
	check := buildDelegationDenyCheckerForDelegate("mia", config.AgentDefaults{}, config.DelegationModeAwait)

	denial := check(ctxWS(testWS, 0), "")
	if denial == nil || denial.Policy != tools.DenyMode {
		t.Fatalf("expected mode denial for await, got: %+v", denial)
	}
}

func TestSubagentDelegationDenyChecker_DeniedWhenDepthExceeded(t *testing.T) {
	seedWorkspaceGraph(t, testWS, true, []graphEdge{
		edge("mia", "ray", []string{"await"}, intPtr(1)),
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
		edge("jim", "ava", []string{"background"}, nil), // initially: jim→ava only
	})
	check := buildDelegationDenyCheckerForDelegate("jim", config.AgentDefaults{}, config.DelegationModeBackground)

	// Before the edit: jim→ray denied (no edge).
	if denial := check(ctxWS(testWS, 0), "ray"); denial == nil {
		t.Fatal("expected jim→ray denied before edge added")
	}

	// Edit the graph on disk to add jim→ray (no checker rebuild).
	rewriteWorkspaceGraph(t, home, testWS, true, []graphEdge{
		edge("jim", "ava", []string{"background"}, nil),
		edge("jim", "ray", []string{"background"}, nil),
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
		edge("mia", "ray", []string{"background"}, nil),
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
			Modes:     []workspace.DelegationMode{"background"},
			Depth:     intPtr(neg),
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
		FromAgent: "mia", ToAgent: "ray", Modes: []workspace.DelegationMode{"background"}, Depth: intPtr(3),
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

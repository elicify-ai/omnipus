package agent

import (
	"context"
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/tools"
)

// intPtr is a local helper for building *int policy/depth fields.
func intPtr(n int) *int { return &n }

// ctxAtDepth returns a context carrying a turnState at the given delegation depth.
func ctxAtDepth(depth int) context.Context {
	return withTurnState(context.Background(), &turnState{depth: depth})
}

// agentWithPolicy builds an AgentConfig carrying the given delegation policy.
// Retained for the (now seed-only) config tests in other files; the runtime
// delegation gate no longer reads DelegationPolicy.
func agentWithPolicy(id string, p *config.DelegationPolicy) *config.AgentConfig {
	return &config.AgentConfig{ID: id, DelegationPolicy: p}
}

// Per-workspace, graph-authoritative delegation enforcement.
//
// MIGRATED from the prior config.DelegationPolicy model: the per-workspace
// delegation graph (workspaces/<id>.json → Delegation[]) is now the SOLE runtime
// authority. Each test seeds the governing workspace's edge set on disk (via
// seedWorkspaceGraph, which also points OMNIPUS_HOME at a temp dir) and binds the
// turn to that workspace through the context (ctxWS). The per-agent AgentConfig
// passed to buildDelegationDenyChecker is now nil at runtime — the graph decides.

const testWS = "01JWMYWORKSPACE0000000001"

// --- targeted delegation gate (spawn = "background", task = "task") ---

func TestDelegationDenyChecker_AllowedWhenModeTrustDepthPermit(t *testing.T) {
	// Graph: Mia→Ray allowed in background mode, edge depth cap 3.
	seedWorkspaceGraph(t, testWS, true, []graphEdge{
		edge("mia", "ray", []string{"background"}, intPtr(3)),
	})
	check := buildDelegationDenyChecker("mia", nil, config.AgentDefaults{},
		config.DelegationModeBackground, nil)

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
	check := buildDelegationDenyChecker("mia", nil, config.AgentDefaults{},
		config.DelegationModeBackground, nil)

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
	check := buildDelegationDenyChecker("mia", nil, config.AgentDefaults{},
		config.DelegationModeBackground, nil)

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
		check := buildDelegationDenyChecker("mia", nil, config.AgentDefaults{}, mode, nil)
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
	check := buildDelegationDenyChecker("mia", nil, config.AgentDefaults{},
		config.DelegationModeBackground, nil)

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
	check := buildDelegationDenyChecker("mia", nil, config.AgentDefaults{},
		config.DelegationModeBackground, nil)

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
	check := buildDelegationDenyChecker("mia", nil, defaults,
		config.DelegationModeBackground, nil)

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
	check := buildDelegationDenyChecker("mia", nil, config.AgentDefaults{},
		config.DelegationModeBackground, nil)

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
	check := buildDelegationDenyChecker("mia", nil, config.AgentDefaults{},
		config.DelegationModeTask, nil)

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
	check := buildDelegationDenyChecker("mia", nil, config.AgentDefaults{},
		config.DelegationModeTask, nil)

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
	check := buildDelegationDenyChecker("mia", nil, config.AgentDefaults{},
		config.DelegationModeTask, nil)

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
	check := buildDelegationDenyChecker("mia", nil, config.AgentDefaults{},
		config.DelegationModeTask, nil)

	denial := check(ctxWS("01JWMISSINGWORKSPACE00001", 0), "worker")
	if denial == nil {
		t.Fatal("expected fail-closed DENY when the bound workspace is unreadable, got allow")
	}
	if denial.Policy != tools.DenyTrustSet {
		t.Fatalf("expected trust_set on unreadable-graph denial, got: %q", denial.Policy)
	}
}

// TestDelegationDenyChecker_ConfigPolicyNoLongerAffectsRuntime proves the
// per-agent config.DelegationPolicy is NO LONGER consulted: a config that would
// permit Mia→ava is irrelevant — only the graph (which has no Mia→ava edge)
// decides, so the delegation is DENIED.
func TestDelegationDenyChecker_ConfigPolicyNoLongerAffectsRuntime(t *testing.T) {
	seedWorkspaceGraph(t, testWS, true, []graphEdge{
		edge("mia", "worker", nil, nil), // graph: Mia→worker only
	})
	// A config that WOULD allow Mia→ava under the old model.
	cfg := agentWithPolicy("mia", &config.DelegationPolicy{
		To:    []config.AgentRef{{Kind: "local", ID: "ava"}},
		Modes: []config.DelegationMode{config.DelegationModeTask},
	})
	check := buildDelegationDenyChecker("mia", cfg, config.AgentDefaults{},
		config.DelegationModeTask, nil)

	// Despite the permissive config, the graph has no Mia→ava edge → DENY.
	if denial := check(ctxWS(testWS, 0), "ava"); denial == nil {
		t.Fatal("config.DelegationPolicy must NOT widen runtime delegation; graph has no Mia→ava edge")
	}
	// And the graph edge (Mia→worker) is honored regardless of config.
	if denial := check(ctxWS(testWS, 0), "worker"); denial != nil {
		t.Fatalf("graph edge Mia→worker must be honored, got deny: %+v", denial)
	}
}

// --- synchronous subagent gate (mode = "await") ---

func TestSubagentDelegationDenyChecker_AllowedWhenPermitted(t *testing.T) {
	seedWorkspaceGraph(t, testWS, true, []graphEdge{
		edge("mia", "ray", []string{"await"}, intPtr(3)),
	})
	check := buildSubagentDelegationDenyChecker(&config.AgentConfig{ID: "mia"}, config.AgentDefaults{})

	if denial := check(ctxWS(testWS, 0)); denial != nil {
		t.Fatalf("expected sync delegation allowed, got deny: %+v", denial)
	}
}

func TestSubagentDelegationDenyChecker_DeniedWhenNoEdges(t *testing.T) {
	// Caller has no outgoing edge in the graph → cannot delegate at all.
	seedWorkspaceGraph(t, testWS, true, []graphEdge{
		edge("jim", "ray", nil, nil), // an edge, but not FROM mia
	})
	check := buildSubagentDelegationDenyChecker(&config.AgentConfig{ID: "mia"}, config.AgentDefaults{})

	denial := check(ctxWS(testWS, 0))
	if denial == nil || denial.Policy != tools.DenyTrustSet {
		t.Fatalf("expected trust_set denial with no outgoing edge, got: %+v", denial)
	}
}

func TestSubagentDelegationDenyChecker_DeniedWhenModeForbidden(t *testing.T) {
	// Edge exists but does not permit the "await" mode.
	seedWorkspaceGraph(t, testWS, true, []graphEdge{
		edge("mia", "ray", []string{"background"}, nil), // await forbidden
	})
	check := buildSubagentDelegationDenyChecker(&config.AgentConfig{ID: "mia"}, config.AgentDefaults{})

	denial := check(ctxWS(testWS, 0))
	if denial == nil || denial.Policy != tools.DenyMode {
		t.Fatalf("expected mode denial for await, got: %+v", denial)
	}
}

func TestSubagentDelegationDenyChecker_DeniedWhenDepthExceeded(t *testing.T) {
	seedWorkspaceGraph(t, testWS, true, []graphEdge{
		edge("mia", "ray", []string{"await"}, intPtr(1)),
	})
	check := buildSubagentDelegationDenyChecker(&config.AgentConfig{ID: "mia"}, config.AgentDefaults{})

	denial := check(ctxWS(testWS, 1)) // depth 1 >= edge cap 1
	if denial == nil || denial.Policy != tools.DenyDepth {
		t.Fatalf("expected depth denial, got: %+v", denial)
	}
}

func TestSubagentDelegationDenyChecker_FailsClosedWhenNoWorkspace(t *testing.T) {
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	check := buildSubagentDelegationDenyChecker(&config.AgentConfig{ID: "mia"}, config.AgentDefaults{})

	if denial := check(ctxAtDepth(0)); denial == nil {
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
	check := buildDelegationDenyChecker("jim", nil, config.AgentDefaults{},
		config.DelegationModeBackground, nil)

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

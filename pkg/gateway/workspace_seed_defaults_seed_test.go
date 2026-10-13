// workspace_seed_defaults_seed_test.go — RED pack, session-core U5a condition
// 1: the self-edge seed is default CONFIG DATA consumed by the shared
// workspace-graph seed computation, and every ordinary built-in and every new
// agent carries a self-row while the two hidden type:system seed records do
// not.
//
// The expected self-row SET below is an INDEPENDENT FROZEN ORACLE: the ids are
// written here by hand from the spec (docs/internal/specs/session-core-spec.md
// architect c4b574b9c ::C-DELEGATE "Seed config data" / BDD-05.7 and the
// seed-owner-decision) — never derived by calling the producer under test.
//
// Current code (branch cut from 6ffb2eb20): coreagent.coreAgentDelegation seeds
// self-rows for jim and worker only, via the hardcoded workspace
// PermittedSelfDelegationID predicate. mia/ava/admin/planner/researcher have no
// self-row, and the exclusion is the IsSystemAgentID predicate, not config
// data. Every test below fails on the pre-change code for those reasons.

package gateway

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// selfRowIDs returns the from_agent of every self-row (from == to) in edges,
// sorted for a stable failure message.
func selfRowIDs(edges []storedDelegationEdge) []string {
	var ids []string
	for _, e := range edges {
		if e.FromAgent == e.ToAgent {
			ids = append(ids, e.FromAgent)
		}
	}
	return ids
}

// selfRowByAgent indexes the self-rows in edges by agent id.
func selfRowByAgent(edges []storedDelegationEdge) map[string]storedDelegationEdge {
	out := make(map[string]storedDelegationEdge)
	for _, e := range edges {
		if e.FromAgent == e.ToAgent {
			out[e.FromAgent] = e
		}
	}
	return out
}

// loadGatewayConfigWithWorkspaceSeedDefaults writes a minimal loadable
// config.json carrying the operator-facing workspace_seed_defaults key, loads
// it, and returns the resulting config. The roster is set by the caller
// afterwards (LoadConfig strips agents.list post-ADR-054). The key must survive
// the load — that preservation is itself a U5a condition-1 requirement.
func loadGatewayConfigWithWorkspaceSeedDefaults(t *testing.T, excludeIDs []any) *config.Config {
	t.Helper()
	dir := t.TempDir()
	m := map[string]any{
		"version": config.CurrentVersion,
		"agents": map[string]any{
			"defaults": map[string]any{"model_name": "test-model", "workspace": dir, "max_tokens": 4096},
			"list":     []any{},
		},
		"gateway":   map[string]any{"host": "127.0.0.1", "port": 5000},
		"providers": []any{},
		workspaceSeedDefaultsKey: map[string]any{
			"self_edge": map[string]any{"exclude_agent_ids": excludeIDs},
		},
	}
	body, err := json.Marshal(m)
	require.NoError(t, err)
	path := filepath.Join(dir, "config.json")
	require.NoError(t, os.WriteFile(path, body, 0o600))
	cfg, err := config.LoadConfig(path)
	require.NoError(t, err)
	return cfg
}

// TestDefaultWorkspaceDelegationEdges_FrozenSelfEdgeOracle is the independent
// oracle for the install/built-in seed writer: every ordinary built-in
// (mia, jim, ava, admin, planner, researcher, worker) carries exactly one
// self-row with ordinary direct/task modes and a pinned depth of 3; the two
// hidden type:system seed records (judge, plansupervisor) carry none.
func TestDefaultWorkspaceDelegationEdges_FrozenSelfEdgeOracle(t *testing.T) {
	cfg := &config.Config{}
	require.True(t, coreagent.SeedConfig(cfg), "SeedConfig on an empty config must modify")

	edges := defaultWorkspaceDelegationEdges(cfg)
	self := selfRowByAgent(edges)

	// FROZEN expected self-row set — hardcoded from the spec, NOT computed by
	// defaultWorkspaceDelegationEdges / coreagent.SeedDelegationEdges.
	wantSelf := []string{"mia", "jim", "ava", "admin", "planner", "researcher", "worker"}
	for _, id := range wantSelf {
		e, ok := self[id]
		if !ok {
			t.Fatalf("FROZEN oracle: ordinary agent %q must have a seeded self-row (BDD-05.7); "+
				"present self-rows = %v", id, selfRowIDs(edges))
		}
		assert.ElementsMatch(t,
			[]workspace.DelegationMode{workspace.ModeDirect, workspace.ModeTask}, e.Modes,
			"self-row %s→%s must carry ordinary direct/task modes (C-DELEGATE ::Depth/editable policy)", id, id)
		if e.Depth == nil {
			t.Fatalf("self-row %s→%s must carry an explicit depth of min(3, ceiling), got nil (inherit)", id, id)
		}
		assert.Equal(t, 3, *e.Depth, "fresh self-row %s→%s must pin depth 3 (FreshSelfDelegationMaxDepth)", id, id)
	}

	// The two hidden type:system seed records explicitly lack a self default.
	for _, id := range []string{"judge", "plansupervisor"} {
		if _, ok := self[id]; ok {
			t.Fatalf("FROZEN oracle: %q (type system) must NOT have a seeded self-row "+
				"(seed-owner-decision ::the two hidden seed records explicitly omit theirs)", id)
		}
	}
}

// TestEnsureDefaultWorkspace_SeedsSelfRowForEveryOrdinaryMember exercises the
// full install seed pipeline (defaultWorkspaceTeam -> defaultWorkspaceDelegationEdges
// -> seedEdgesForTeam -> persist) end-to-end: every on-team member has a
// PERSISTED self-row; judge/plansupervisor/admin have none.
func TestEnsureDefaultWorkspace_SeedsSelfRowForEveryOrdinaryMember(t *testing.T) {
	home := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			List: []config.AgentConfig{
				{ID: "mia", Type: config.AgentTypeCore},
				{ID: "jim", Type: config.AgentTypeCore},
				{ID: "ava", Type: config.AgentTypeCore},
				{ID: "admin", Type: config.AgentTypeCore},
				{ID: "worker", Type: config.AgentTypeWorker},
				{ID: "planner", Type: config.AgentTypeWorker},
				{ID: "researcher", Type: config.AgentTypeWorker},
				{ID: "judge", Type: config.AgentTypeSystem},
				{ID: "plansupervisor", Type: config.AgentTypeSystem},
			},
		},
	}
	require.NoError(t, ensureDefaultWorkspace(home, "alice", cfg))

	wss, err := listWorkspaceFiles(home)
	require.NoError(t, err)
	require.Len(t, wss, 1)
	ws := wss[0]

	self := selfRowByAgent(loadStoredDelegationEdges(t, home, ws.ID))
	for _, id := range ws.CoreTeam {
		if _, ok := self[id]; !ok {
			t.Fatalf("team member %q has no PERSISTED self-row; persisted self-rows = %v (BDD-05.7)",
				id, selfRowIDs(loadStoredDelegationEdges(t, home, ws.ID)))
		}
	}
	for _, id := range []string{"judge", "plansupervisor", "admin"} {
		if _, ok := self[id]; ok {
			t.Fatalf("%q must have no persisted self-row (off-team / excluded by seed data)", id)
		}
	}
}

// TestDefaultWorkspaceDelegationEdges_EmptyExcludeListSeedsSystemAgents is the
// F3 oracle half 1: the exclusion is config DATA, not the IsSystemAgentID
// predicate. With the operator's list emptied, judge and plansupervisor MUST
// receive self-rows — a Go identity predicate would still exclude them.
func TestDefaultWorkspaceDelegationEdges_EmptyExcludeListSeedsSystemAgents(t *testing.T) {
	cfg := loadGatewayConfigWithWorkspaceSeedDefaults(t, []any{})
	cfg.Agents.List = []config.AgentConfig{
		{ID: "mia", Type: config.AgentTypeCore},
		{ID: "judge", Type: config.AgentTypeSystem},
		{ID: "plansupervisor", Type: config.AgentTypeSystem},
	}

	self := selfRowByAgent(defaultWorkspaceDelegationEdges(cfg))
	for _, id := range []string{"mia", "judge", "plansupervisor"} {
		if _, ok := self[id]; !ok {
			t.Fatalf("with exclude_agent_ids=[], %q must get a self-row — proving the exclusion is "+
				"config DATA, not the IsSystemAgentID predicate (F3); present self-rows = %v",
				id, selfRowIDs(defaultWorkspaceDelegationEdges(cfg)))
		}
	}
}

// TestDefaultWorkspaceDelegationEdges_OperatorAddedExclusionIsHonoured is the
// F3 oracle half 2: an operator who adds an ordinary id to exclude_agent_ids
// takes effect as data (mia excluded though she is no system agent), while a
// sibling ordinary id keeps its self-row.
func TestDefaultWorkspaceDelegationEdges_OperatorAddedExclusionIsHonoured(t *testing.T) {
	cfg := loadGatewayConfigWithWorkspaceSeedDefaults(t, []any{"mia"})
	cfg.Agents.List = []config.AgentConfig{
		{ID: "mia", Type: config.AgentTypeCore},
		{ID: "planner", Type: config.AgentTypeWorker},
		{ID: "jim", Type: config.AgentTypeCore},
	}

	edges := defaultWorkspaceDelegationEdges(cfg)
	self := selfRowByAgent(edges)
	if _, ok := self["mia"]; ok {
		t.Fatalf("mia is in the operator's exclude_agent_ids — she must have NO self-row "+
			"(seed-owner-decision ::Adding a third excluded ID is data edit); self-rows = %v", selfRowIDs(edges))
	}
	for _, id := range []string{"planner", "jim"} {
		if _, ok := self[id]; !ok {
			t.Fatalf("%q must keep its self-row (the operator excluded only mia) — proving the list is "+
				"config DATA (F3); self-rows = %v", id, selfRowIDs(edges))
		}
	}
}

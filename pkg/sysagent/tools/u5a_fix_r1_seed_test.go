// Omnipus — System Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// U5A-FIX-R1 regression pack (security re-verification of 60e299a86):
//
//	F3  — the tool seed producers ignored the operator exclusion DATA
//	      (workspace_seed_defaults.self_edge.exclude_agent_ids). Each producer
//	      must pass the operator set from the one resolver; an explicit [] means
//	      "exclude nobody", absent means the shipped default.
//	N1  — create_agent's membership write and self-row seed are ONE workspace
//	      critical section, and the seed re-reads authoritative membership: a
//	      member no longer on the team is never seeded.
//	N3  — a seed failure AFTER membership landed returns the truthful
//	      partial-failure result, never a normal "metadata_only" success.
//
// This file is package systools (internal) so it can drive the unexported
// resolver, seed helper and test hook directly.
package systools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
	workspacepkg "github.com/elicify-ai/omnipus/pkg/workspace"
)

// u5aDeps builds a minimal Deps over an isolated temp home whose live config
// carries the given roster and (when exclude != nil) the operator self-edge
// exclusion list. A nil exclude leaves WorkspaceSeedDefaults at its shipped
// default.
func u5aDeps(t *testing.T, exclude []string, roster ...string) (*Deps, *config.Config, string) {
	t.Helper()
	home := t.TempDir()
	cfg := config.DefaultConfig()
	if exclude != nil {
		cfg.WorkspaceSeedDefaults = &config.WorkspaceSeedDefaultsConfig{
			SelfEdge: config.SelfEdgeSeedDefaults{ExcludeAgentIDs: exclude},
		}
	}
	for _, id := range roster {
		cfg.Agents.List = append(cfg.Agents.List, config.AgentConfig{ID: id})
	}
	deps := &Deps{
		Home:             home,
		ConfigPath:       filepath.Join(home, "config.json"),
		GetCfg:           func() *config.Config { return cfg },
		MutateConfig:     func(fn func(*config.Config) error) error { return fn(cfg) },
		SaveConfigLocked: func(*config.Config) error { return nil },
	}
	return deps, cfg, home
}

// u5aWriteWorkspace writes a minimal on-disk workspace record under home.
func u5aWriteWorkspace(t *testing.T, home, wsID string, coreTeam []string) {
	t.Helper()
	dir := filepath.Join(home, "workspaces")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir workspaces: %v", err)
	}
	team := ""
	for i, id := range coreTeam {
		if i > 0 {
			team += ","
		}
		team += `"` + id + `"`
	}
	body := `{"id":"` + wsID + `","name":"ws","status":"active","core_team":[` + team + `]}`
	if err := os.WriteFile(filepath.Join(dir, wsID+".json"), []byte(body), 0o644); err != nil {
		t.Fatalf("write workspace %s: %v", wsID, err)
	}
}

// u5aParseJSON decodes a tool result body.
func u5aParseJSON(t *testing.T, body string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("result body is not valid JSON: %v\nbody: %s", err, body)
	}
	return m
}

func u5aSelfEdge(t *testing.T, home, wsID, id string) (workspacepkg.DelegationEdge, bool) {
	t.Helper()
	edges, _ := workspacepkg.LoadDelegation(home, wsID)
	for _, e := range edges {
		if e.FromAgent == id && e.ToAgent == id {
			return e, true
		}
	}
	return workspacepkg.DelegationEdge{}, false
}

// ---- F3: the resolver ----

// TestSelfEdgeExcludedAgentIDs_Resolver pins the ONE resolver every tool seed
// writer reads: absent ⇒ shipped default (judge/plansupervisor); an explicit
// [] ⇒ exclude nobody; an operator list ⇒ exactly those ids.
func TestSelfEdgeExcludedAgentIDs_Resolver(t *testing.T) {
	// GetCfg nil ⇒ "unset" ⇒ the shipped default, NOT an empty (exclude-nobody)
	// set: an empty set would seed the very agents the shipped default bars.
	shipped := selfEdgeExcludedAgentIDs(&Deps{})
	if !shipped["judge"] || !shipped["plansupervisor"] {
		t.Fatalf("an unwired config must resolve to the shipped exclusions judge/plansupervisor; got %v", shipped)
	}
	if shipped["mia"] {
		t.Fatalf("mia is not a shipped exclusion; got %v", shipped)
	}

	emptyDeps, _, _ := u5aDeps(t, []string{})
	if got := selfEdgeExcludedAgentIDs(emptyDeps); len(got) != 0 {
		t.Fatalf("an explicit [] exclude_agent_ids must exclude nobody; got %v", got)
	}

	opDeps, _, _ := u5aDeps(t, []string{"mia"})
	op := selfEdgeExcludedAgentIDs(opDeps)
	if !op["mia"] {
		t.Fatalf("the operator's exclusion list must contain mia; got %v", op)
	}
	if op["judge"] {
		t.Fatalf("the operator's list REPLACES the shipped default (mia only); judge must not be excluded, got %v", op)
	}
}

// ---- F3: create_workspace producer ----

// TestCreateWorkspace_OperatorExclusionIsHonoured proves the create_workspace
// producer honours an operator-added exclusion such as `mia` (an ordinary
// agent), while a sibling member keeps its self-row.
func TestCreateWorkspace_OperatorExclusionIsHonoured(t *testing.T) {
	deps, _, home := u5aDeps(t, []string{"mia"}, "mia", "jim")

	res := NewWorkspaceCreateTool(deps).Execute(context.Background(), map[string]any{
		"name":      "ws",
		"core_team": []any{"mia", "jim"},
	})
	if res.IsError {
		t.Fatalf("create_workspace failed: %s", res.ForLLM)
	}
	id, _ := u5aParseJSON(t, res.ForLLM)["id"].(string)
	if id == "" {
		t.Fatal("create_workspace returned no id")
	}
	if _, ok := u5aSelfEdge(t, home, id, "mia"); ok {
		t.Fatalf("mia is in the operator's exclude_agent_ids — create_workspace must NOT seed her self-row " +
			"(F3: the exclusion is operator DATA, not a Go identity predicate)")
	}
	if _, ok := u5aSelfEdge(t, home, id, "jim"); !ok {
		t.Fatal("jim (not excluded) must keep his seeded self-row")
	}
}

// TestCreateWorkspace_ExplicitEmptyExclusionSeeds proves explicit [] means
// "exclude nobody": with the list emptied, an agent still gets its self-row.
func TestCreateWorkspace_ExplicitEmptyExclusionSeeds(t *testing.T) {
	deps, _, home := u5aDeps(t, []string{}, "mia")
	res := NewWorkspaceCreateTool(deps).Execute(context.Background(), map[string]any{
		"name":      "ws",
		"core_team": []any{"mia"},
	})
	if res.IsError {
		t.Fatalf("create_workspace failed: %s", res.ForLLM)
	}
	id, _ := u5aParseJSON(t, res.ForLLM)["id"].(string)
	if _, ok := u5aSelfEdge(t, home, id, "mia"); !ok {
		t.Fatal("with exclude_agent_ids=[] (exclude nobody), mia must receive a seeded self-row")
	}
}

// ---- F3: update_workspace producer ----

// TestUpdateWorkspace_OperatorExclusionIsHonoured proves the update_workspace
// team-growth producer honours the same operator exclusion when an excluded
// agent is added to an existing team.
func TestUpdateWorkspace_OperatorExclusionIsHonoured(t *testing.T) {
	deps, _, home := u5aDeps(t, []string{"mia"}, "mia", "jim")
	wsID := "wu-ws"
	u5aWriteWorkspace(t, home, wsID, []string{"jim"})

	// The operator's CURRENT graph already carries jim's self-row; mia is the
	// NEW addition. (Written before the revision is read, because the revision
	// covers the delegation store too.)
	unlock := workspacepkg.LockID(wsID)
	seedErr := workspacepkg.SaveDelegation(home, wsID, []workspacepkg.DelegationEdge{{
		FromAgent: "jim", ToAgent: "jim",
		Modes: []workspacepkg.DelegationMode{workspacepkg.ModeDirect, workspacepkg.ModeTask},
	}})
	unlock()
	if seedErr != nil {
		t.Fatalf("seed jim's current self-row: %v", seedErr)
	}

	state, err := workspacepkg.ReadState(home, wsID)
	if err != nil {
		t.Fatalf("read workspace revision: %v", err)
	}
	res := NewWorkspaceUpdateTool(deps).Execute(context.Background(), map[string]any{
		"id":        wsID,
		"revision":  state.Revision,
		"core_team": []any{"jim", "mia"},
	})
	if res.IsError {
		t.Fatalf("update_workspace failed: %s", res.ForLLM)
	}
	if _, ok := u5aSelfEdge(t, home, wsID, "mia"); ok {
		t.Fatalf("adding an EXCLUDED agent via update_workspace must NOT seed its self-row (F3)")
	}
	if _, ok := u5aSelfEdge(t, home, wsID, "jim"); !ok {
		t.Fatal("jim (not excluded) must keep his current self-row across the update")
	}
}

// ---- F3: create_agent producer ----

func u5aCreateAgent(t *testing.T, deps *Deps, ctx context.Context, name string) (string, map[string]any) {
	t.Helper()
	res := NewAgentCreateTool(deps).Execute(ctx, map[string]any{
		"name":        name,
		"description": "created in a workspace context",
		"soul":        "You help.",
		"model":       "test/model",
		"color":       "#22C55E",
	})
	body := u5aParseJSON(t, res.ForLLM)
	if res.IsError {
		t.Fatalf("create_agent(%q) failed: %s", name, res.ForLLM)
	}
	id, _ := body["id"].(string)
	if id == "" {
		t.Fatalf("create_agent(%q) returned no id", name)
	}
	return id, body
}

// TestCreateAgent_InContext_ExcludedAgentGetsNoSelfRow proves the create_agent
// join producer honours the operator exclusion: a newly created agent whose id
// the operator excluded gets NO self-row, while one that is not excluded does.
func TestCreateAgent_InContext_ExcludedAgentGetsNoSelfRow(t *testing.T) {
	deps, _, home := u5aDeps(t, []string{"ctx-bot"}, "mia")
	wsID := "ctx-ws"
	u5aWriteWorkspace(t, home, wsID, nil)

	id, _ := u5aCreateAgent(t, deps, tools.WithWorkspaceID(context.Background(), wsID), "Ctx Bot")
	if id != "ctx-bot" {
		t.Fatalf("expected slug id ctx-bot, got %q", id)
	}
	// Membership must still land — the exclusion governs the self-row only.
	w, err := readWorkspaceFromDisk(home, wsID)
	if err != nil {
		t.Fatalf("read workspace after join: %v", err)
	}
	if !teamContains(w.CoreTeam, id) {
		t.Fatalf("ctx-bot must still JOIN the team (the exclusion only bars its self-row); team=%v", w.CoreTeam)
	}
	if _, ok := u5aSelfEdge(t, home, wsID, id); ok {
		t.Fatalf("ctx-bot is in the operator's exclude_agent_ids — its self-row must NOT be seeded (F3)")
	}

	// Control: a fresh deps with no added exclusion seeds the same create.
	ctlDeps, _, ctlHome := u5aDeps(t, nil, "mia")
	ctlWS := "ctl-ws"
	u5aWriteWorkspace(t, ctlHome, ctlWS, nil)
	ctlID, _ := u5aCreateAgent(t, ctlDeps, tools.WithWorkspaceID(context.Background(), ctlWS), "Ctx Bot")
	if _, ok := u5aSelfEdge(t, ctlHome, ctlWS, ctlID); !ok {
		t.Fatalf("control: an excluded-nobody create must seed the new agent's self-row")
	}
}

// ---- N1: one transaction; never seed a removed member ----

// TestCreateAgent_JoinIsOneTransaction_NeverSeedsRemovedMember forces the
// interleaving deterministically through the test seam: a competing membership
// removal lands AFTER the membership write and BEFORE the seed's authoritative
// membership re-read. The seed must NOT resurrect a self-row for an agent that
// is no longer on the team.
func TestCreateAgent_JoinIsOneTransaction_NeverSeedsRemovedMember(t *testing.T) {
	deps, _, home := u5aDeps(t, nil, "mia")
	wsID := "tx-ws"
	u5aWriteWorkspace(t, home, wsID, nil)

	fired := false
	joinWorkspaceMembershipTestHook = func(hookWS, agentID string) {
		fired = true
		// Simulate a competing membership removal landing at exactly this point.
		w, err := readWorkspaceFromDisk(home, hookWS)
		if err != nil {
			t.Errorf("hook read workspace: %v", err)
			return
		}
		kept := make([]string, 0, len(w.CoreTeam))
		for _, id := range w.CoreTeam {
			if id != agentID {
				kept = append(kept, id)
			}
		}
		w.CoreTeam = kept
		w.UpdatedAt = nowISO()
		if err := writeEntity(workspacesDir(home), hookWS, w); err != nil {
			t.Errorf("hook write workspace: %v", err)
		}
	}
	defer func() { joinWorkspaceMembershipTestHook = nil }()

	id, _ := u5aCreateAgent(t, deps, tools.WithWorkspaceID(context.Background(), wsID), "Tx Bot")
	if !fired {
		t.Fatal("the join test hook never fired — the test cannot prove the interleaving")
	}
	if _, ok := u5aSelfEdge(t, home, wsID, id); ok {
		t.Fatalf("a member removed before the seed's authoritative re-read must NOT be seeded a self-row (N1)")
	}
	w, err := readWorkspaceFromDisk(home, wsID)
	if err != nil {
		t.Fatalf("read workspace: %v", err)
	}
	if teamContains(w.CoreTeam, id) {
		t.Fatalf("the competing removal should have removed %q from the team", id)
	}
}

// ---- N3: a seed failure after membership is a truthful partial failure ----

// TestCreateAgent_SeedFailureReturnsPartialNotMetadataOnly forces the self-row
// write to fail (an unreadable delegation store) AFTER the membership landed,
// and requires the truthful partial-failure result: never the ordinary
// "metadata_only" success, and never a claim that the agent joined nothing.
func TestCreateAgent_SeedFailureReturnsPartialNotMetadataOnly(t *testing.T) {
	deps, _, home := u5aDeps(t, nil, "mia")
	wsID := "seedfail-ws"
	u5aWriteWorkspace(t, home, wsID, nil)

	// Make the workspace's delegation store unreadable so the seed read fails.
	storePath, err := workspacepkg.DelegationStorePath(home, wsID)
	if err != nil {
		t.Fatalf("delegation store path: %v", err)
	}
	if mkErr := os.MkdirAll(filepath.Dir(storePath), 0o700); mkErr != nil {
		t.Fatalf("mkdir delegation store: %v", mkErr)
	}
	if wErr := os.WriteFile(storePath, []byte(`{"workspace_id":"some-other-workspace","delegation":[]}`), 0o600); wErr != nil {
		t.Fatalf("write corrupt delegation store: %v", wErr)
	}

	res := NewAgentCreateTool(deps).Execute(tools.WithWorkspaceID(context.Background(), wsID), map[string]any{
		"name":        "Seed Fail Bot",
		"description": "created in a workspace context",
		"soul":        "You help.",
		"model":       "test/model",
		"color":       "#22C55E",
	})
	if !res.IsError {
		t.Fatalf("a seed failure after membership landed must NOT be reported as a normal success: %s", res.ForLLM)
	}
	body := u5aParseJSON(t, res.ForLLM)
	if body["status"] == "metadata_only" {
		t.Fatalf("a joined-but-unseeded agent must NOT be labelled metadata_only: %s", res.ForLLM)
	}
	if joined, _ := body["joined_workspace"].(bool); !joined {
		t.Fatalf("the partial result must state the membership landed (joined_workspace=true): %s", res.ForLLM)
	}
	if seeded, ok := body["self_edge_seeded"].(bool); !ok || seeded {
		t.Fatalf("the partial result must state the self-row was NOT seeded (self_edge_seeded=false): %s", res.ForLLM)
	}
	if stage, _ := body["error_stage"].(string); stage != "seed_self_edge" {
		t.Fatalf("error_stage = %q, want seed_self_edge: %s", stage, res.ForLLM)
	}
	if ps, _ := body["persistence_status"].(string); ps != "partial" {
		t.Fatalf("persistence_status = %q, want partial: %s", ps, res.ForLLM)
	}
	// The membership DID land — that is exactly why metadata_only would be a lie.
	w, readErr := readWorkspaceFromDisk(home, wsID)
	if readErr != nil {
		t.Fatalf("read workspace: %v", readErr)
	}
	found := false
	for _, id := range w.CoreTeam {
		if id == "seed-fail-bot" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the membership must have landed before the seed failed; team=%v", w.CoreTeam)
	}
}

// ---- N1: barrier-driven ordering proof ----

// TestCreateAgent_JoinSerializesWithExplicitGraphWrite is the barrier-driven
// membership/graph ordering proof. While the join holds the workspace critical
// section, a competing explicit graph replacement cannot land, so the explicit
// decision (issued after the join) stays authoritative and the implicit seed
// never overwrites it. No sleeps: the two goroutines are sequenced ONLY by the
// hook channel.
//
// The hook fires INSIDE the join's critical section (with LockID held), so the
// competing writer's LockID acquisition cannot complete until the join's seed
// has been published and the lock released — the explicit replacement therefore
// lands strictly after the join and remains the final, authoritative graph.
func TestCreateAgent_JoinSerializesWithExplicitGraphWrite(t *testing.T) {
	deps, _, home := u5aDeps(t, nil, "mia")
	wsID := "order-ws"
	u5aWriteWorkspace(t, home, wsID, nil)

	inCritical := make(chan struct{})
	joinWorkspaceMembershipTestHook = func(string, string) { close(inCritical) }
	defer func() { joinWorkspaceMembershipTestHook = nil }()

	resCh := make(chan *tools.ToolResult, 1)
	go func() {
		resCh <- NewAgentCreateTool(deps).Execute(
			tools.WithWorkspaceID(context.Background(), wsID),
			map[string]any{
				"name":        "Order Bot",
				"description": "created in a workspace context",
				"soul":        "You help.",
				"model":       "test/model",
				"color":       "#22C55E",
			})
	}()

	<-inCritical

	// The competing explicit graph replacement (an EMPTY graph — every self-row
	// excluded). It must wait for the join's critical section to finish.
	putDone := make(chan struct{})
	go func() {
		defer close(putDone)
		unlock := workspacepkg.LockID(wsID)
		defer unlock()
		_ = workspacepkg.SaveDelegation(home, wsID, nil)
	}()

	res := <-resCh
	if res.IsError {
		t.Fatalf("create_agent failed: %s", res.ForLLM)
	}
	<-putDone

	edges, ok := workspacepkg.LoadDelegation(home, wsID)
	if ok && len(edges) != 0 {
		t.Fatalf("the explicit replacement issued after the join must remain authoritative — the "+
			"implicit self-row seed must not overwrite it; got %+v", edges)
	}
}

// TestCreateAgent_JoinNeverResurrectsContinuingMemberRow is the continuing-
// membership removed-row case: an operator's deliberate removal of a continuing
// member's self-row stays removed across an unrelated join, the newly
// introduced member IS seeded, and no off-team/foreign grant appears.
func TestCreateAgent_JoinNeverResurrectsContinuingMemberRow(t *testing.T) {
	deps, _, home := u5aDeps(t, nil, "mia", "jim")
	wsID := "cont-ws"
	u5aWriteWorkspace(t, home, wsID, []string{"jim", "mia"})

	// The operator's explicit graph: a self-row for jim, mia's deliberately
	// removed (she is a continuing member).
	unlock := workspacepkg.LockID(wsID)
	err := workspacepkg.SaveDelegation(home, wsID, []workspacepkg.DelegationEdge{{
		FromAgent: "jim", ToAgent: "jim",
		Modes: []workspacepkg.DelegationMode{workspacepkg.ModeDirect, workspacepkg.ModeTask},
	}})
	unlock()
	if err != nil {
		t.Fatalf("seed explicit graph: %v", err)
	}

	newID, _ := u5aCreateAgent(t, deps, tools.WithWorkspaceID(context.Background(), wsID), "Newbie")

	if _, ok := u5aSelfEdge(t, home, wsID, "mia"); ok {
		t.Fatal("a continuing member's deliberately removed self-row must NOT be resurrected by an unrelated join")
	}
	if _, ok := u5aSelfEdge(t, home, wsID, newID); !ok {
		t.Fatalf("the newly introduced member %q must receive its self-row", newID)
	}
	if _, ok := u5aSelfEdge(t, home, wsID, "jim"); !ok {
		t.Fatal("jim's explicit self-row must stand")
	}
	// No foreign / off-team grant: every edge endpoint must be a team member.
	w, readErr := readWorkspaceFromDisk(home, wsID)
	if readErr != nil {
		t.Fatalf("read workspace: %v", readErr)
	}
	edges, _ := workspacepkg.LoadDelegation(home, wsID)
	for _, e := range edges {
		if !teamContains(w.CoreTeam, e.FromAgent) || !teamContains(w.CoreTeam, e.ToAgent) {
			t.Fatalf("edge %s→%s has an off-team endpoint; team=%v", e.FromAgent, e.ToAgent, w.CoreTeam)
		}
	}
}

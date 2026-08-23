// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// wireTestLoopWithGraph builds a minimal AgentLoop for agentID. Unlike the
// old wireTestLoop, it does NOT set a per-agent config DelegationPolicy (since
// the new injector ignores config and reads the workspace graph). The caller is
// responsible for seeding the workspace graph via seedWorkspaceGraph before
// exercising the injector. Caller owns al.Close().
func wireTestLoopWithGraph(t *testing.T, agentID string) (*AgentLoop, *ContextBuilder) {
	t.Helper()
	cfg := minimalTestConfig(t)
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "test-model"}
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

// ---------------------------------------------------------------------------
// TestDelegationWiring_GraphSource
//
// Proves the injector reads the workspace graph, NOT the config policy.
// Seeds a workspace with a mia→ray edge; expects ray in the block.
// ---------------------------------------------------------------------------

func TestDelegationWiring_GraphSource(t *testing.T) {
	// seedWorkspaceGraph sets OMNIPUS_HOME to a fresh temp dir.
	const wsID = "01JWWIRINGTEST0000000001"
	home := seedWorkspaceGraph(t, wsID, true, []graphEdge{
		edge("ava", "ray", nil, nil), // ava→ray in default workspace
	})
	_ = home

	const agentID = "ava"
	al, cb := wireTestLoopWithGraph(t, agentID)
	_ = al

	// workspaceID="" → injector resolves the default workspace (wsID flagged is_default).
	got := cb.buildDynamicContext("", "", "", "", "")
	if !strings.Contains(got, "## Delegation") {
		t.Fatalf("Delegation block missing.\ngot: %s", got)
	}
	if !strings.Contains(got, "ray") {
		t.Fatalf("expected 'ray' in Delegation block (from graph edge ava→ray).\ngot: %s", got)
	}
}

// ---------------------------------------------------------------------------
// TestDelegationWiring_RuntimeGraphRefresh (P1 replacement)
//
// Proves the injector re-reads the graph per-call: edit the workspace file on
// disk without rebuilding the checker, and the NEXT call reflects the new graph.
// ---------------------------------------------------------------------------

func TestDelegationWiring_RuntimeGraphRefresh(t *testing.T) {
	const wsID = "01JWWIRINGRR000000000001"
	// Phase A: ava→ray edge present.
	home := seedWorkspaceGraph(t, wsID, true, []graphEdge{
		edge("ava", "ray", nil, nil),
	})

	const agentID = "ava"
	al, cb := wireTestLoopWithGraph(t, agentID)
	_ = al

	// Phase A: ray must appear.
	got1 := cb.buildDynamicContext("", "", "", "", "")
	if !strings.Contains(got1, "ray") {
		t.Fatalf("Phase A: expected 'ray' in Delegation block.\ngot: %s", got1)
	}

	// Phase B: rewrite the workspace graph to REMOVE the ava→ray edge.
	rewriteWorkspaceGraph(t, home, wsID, true, nil)

	// The SAME ContextBuilder — no rebuild — now reads the updated graph.
	got2 := cb.buildDynamicContext("", "", "", "", "")
	if strings.Contains(got2, "ray") {
		t.Fatalf("Phase B: 'ray' must be gone after graph edit.\ngot: %s", got2)
	}
	if !strings.Contains(got2, "cannot delegate") {
		t.Fatalf("Phase B: empty graph must render 'cannot delegate'.\ngot: %s", got2)
	}
}

// ---------------------------------------------------------------------------
// TestDelegationWiring_RuntimeGraphRefresh_AddEdge
//
// Inverse: an agent that initially cannot delegate gains a target after the
// workspace graph is edited (no checker rebuild).
// ---------------------------------------------------------------------------

func TestDelegationWiring_RuntimeGraphRefresh_AddEdge(t *testing.T) {
	const wsID = "01JWWIRINGRA000000000001"
	// Phase A: no delegation edges.
	home := seedWorkspaceGraph(t, wsID, true, nil)

	const agentID = "ava"
	al, cb := wireTestLoopWithGraph(t, agentID)
	_ = al

	got1 := cb.buildDynamicContext("", "", "", "", "")
	// Without edges, the injector renders cannot-delegate.
	if !strings.Contains(got1, "cannot delegate") {
		t.Logf("Phase A: got: %s", got1)
	}

	// Phase B: add ava→ray edge without rebuilding the loop.
	rewriteWorkspaceGraph(t, home, wsID, true, []graphEdge{
		edge("ava", "ray", nil, nil),
	})

	got2 := cb.buildDynamicContext("", "", "", "", "")
	if !strings.Contains(got2, "## Delegation") {
		t.Fatalf("Phase B: Delegation block missing after adding edge.\ngot: %s", got2)
	}
	if !strings.Contains(got2, "ray") {
		t.Fatalf("Phase B: 'ray' must appear after adding edge.\ngot: %s", got2)
	}
}

// ---------------------------------------------------------------------------
// TestDelegationWiring_BootPath
//
// Proves wireEnvProviders (boot path) installs the delegation injector:
// immediately after NewAgentLoop returns, an agent renders the Delegation block
// consistent with the on-disk workspace graph.
// ---------------------------------------------------------------------------

func TestDelegationWiring_BootPath(t *testing.T) {
	const wsID = "01JWWIRINGBOOT00000000001"
	seedWorkspaceGraph(t, wsID, true, []graphEdge{
		edge("jim", "ava", nil, nil),
		edge("jim", "ray", nil, nil),
	})

	const agentID = "jim"
	al, _ := wireTestLoopWithGraph(t, agentID)
	_ = al

	inst, ok := al.GetRegistry().GetAgent(agentID)
	if !ok || inst == nil || inst.ContextBuilder == nil {
		t.Fatalf("agent %q not in registry at boot", agentID)
	}

	got := inst.ContextBuilder.buildDynamicContext("", "", "", "", "")
	if !strings.Contains(got, "## Delegation") {
		t.Fatalf("boot path: Delegation block missing for %q.\ngot: %s", agentID, got)
	}
	if !strings.Contains(got, "ava") {
		t.Fatalf("boot path: 'ava' must appear (graph edge jim→ava).\ngot: %s", got)
	}
}

// ---------------------------------------------------------------------------
// TestDelegationWiring_AgentAddedOnReload
//
// An agent that did not exist at boot receives a delegation injector after
// being added via ReloadProviderAndConfig.
// ---------------------------------------------------------------------------

func TestDelegationWiring_AgentAddedOnReload(t *testing.T) {
	const wsID = "01JWWIRINGNEW0000000001"
	seedWorkspaceGraph(t, wsID, true, []graphEdge{
		edge("custom-helper", "ray", nil, nil),
	})

	al, _ := wireTestLoopWithGraph(t, "ava")

	const newAgentID = "custom-helper"
	liveCfg := al.GetConfig()
	newCfg := &config.Config{}
	newCfg.Agents.Defaults = liveCfg.Agents.Defaults
	newCfg.Agents.List = make([]config.AgentConfig, len(liveCfg.Agents.List))
	copy(newCfg.Agents.List, liveCfg.Agents.List)
	newCfg.Agents.List = append(newCfg.Agents.List, config.AgentConfig{
		ID:   newAgentID,
		Name: "Custom Helper",
	})
	if err := al.ReloadProviderAndConfig(context.Background(), &mockProvider{}, newCfg); err != nil {
		t.Fatalf("ReloadProviderAndConfig adding new agent: %v", err)
	}

	inst, ok := al.GetRegistry().GetAgent(newAgentID)
	if !ok || inst == nil || inst.ContextBuilder == nil {
		t.Fatalf("new agent %q not in registry after reload", newAgentID)
	}

	got := inst.ContextBuilder.buildDynamicContext("", "", "", "", "")
	if !strings.Contains(got, "## Delegation") {
		t.Fatalf("new agent added on reload: Delegation block missing.\ngot: %s", got)
	}
	if !strings.Contains(got, "ray") {
		t.Fatalf("new agent added on reload: 'ray' must appear (graph edge custom-helper→ray).\ngot: %s", got)
	}
}

// ---------------------------------------------------------------------------
// TestDelegationWiring_Race_ConcurrentRenderAndSwap
//
// Race-detector test: concurrent buildDynamicContext renders on one goroutine
// while ReloadProviderAndConfig swaps the registry on another. Must be race-free.
// ---------------------------------------------------------------------------

func TestDelegationWiring_Race_ConcurrentRenderAndSwap(t *testing.T) {
	const wsID = "01JWWIRINGRACE00000000001"
	home := seedWorkspaceGraph(t, wsID, true, []graphEdge{
		edge("ava", "ray", nil, nil),
	})

	al, _ := wireTestLoopWithGraph(t, "ava")

	const agentID = "ava"
	inst, ok := al.GetRegistry().GetAgent(agentID)
	if !ok || inst == nil || inst.ContextBuilder == nil {
		t.Fatalf("agent %q not in registry", agentID)
	}
	cb := inst.ContextBuilder

	// Render goroutine.
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
				_ = cb.buildDynamicContext("", "", "", "", "")
			}
		}
	}()

	// Graph-edit goroutine: alternate between adding and removing the edge.
	edgeSets := [][]graphEdge{
		{edge("ava", "ray", nil, nil)},
		nil,
		{edge("ava", "ray", []string{"await"}, nil)},
		nil,
	}
	for _, es := range edgeSets {
		rewriteWorkspaceGraph(t, home, wsID, true, es)
		// Also exercise hot-reload path.
		liveCfg := al.GetConfig()
		newCfg := &config.Config{}
		newCfg.Agents.Defaults = liveCfg.Agents.Defaults
		newCfg.Agents.List = make([]config.AgentConfig, len(liveCfg.Agents.List))
		copy(newCfg.Agents.List, liveCfg.Agents.List)
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
// TestDelegationWiring_NilInjectorNoBlock
//
// When no delegationInjector is set, buildDynamicContext must not emit any
// delegation section (no panic, no spurious output).
// ---------------------------------------------------------------------------

func TestDelegationWiring_NilInjectorNoBlock(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	cb := NewContextBuilder(dir) // no WithDelegationInjector call

	got := cb.buildDynamicContext("", "", "", "", "")

	if strings.Contains(got, "## Delegation") {
		t.Errorf("with no injector set, Delegation block must not appear.\ngot: %s", got)
	}
}

// ---------------------------------------------------------------------------
// TestDelegationWiring_BlockInDynamicNotCachedSection
//
// The delegation block must appear in the DYNAMIC section (the part of the
// system message that is rebuilt each turn), NOT in the cached static prompt.
// ---------------------------------------------------------------------------

func TestDelegationWiring_BlockInDynamicNotCachedSection(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	delegationBlock := "## Delegation\nSentinel delegation text"

	cb := NewContextBuilder(dir)
	cb.WithDelegationInjector(func(_ string) string { return delegationBlock })

	// The static cached prompt must NOT contain the delegation block.
	staticPrompt := cb.BuildSystemPromptWithCache()
	if strings.Contains(staticPrompt, "Sentinel delegation text") {
		t.Errorf("delegation block must NOT appear in the cached static system prompt; got it in:\n%s", staticPrompt)
	}

	// The dynamic context MUST contain the delegation block.
	dynamic := cb.buildDynamicContext("", "", "", "", "")
	if !strings.Contains(dynamic, "Sentinel delegation text") {
		t.Errorf("delegation block must appear in buildDynamicContext output; got:\n%s", dynamic)
	}
}

// ---------------------------------------------------------------------------
// TestDelegationWiring_FailClosedWhenNoDefaultWorkspace
//
// When OMNIPUS_HOME has no default workspace and the turn carries no workspaceID,
// the injector must render fail-closed "cannot delegate".
// ---------------------------------------------------------------------------

func TestDelegationWiring_FailClosedWhenNoDefaultWorkspace(t *testing.T) {
	// Use a fresh empty home with no workspace files.
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	const agentID = "ava"
	al, cb := wireTestLoopWithGraph(t, agentID)
	_ = al

	got := cb.buildDynamicContext("", "", "", "", "")
	// Must fail-closed: no workspace ⇒ "cannot delegate".
	if !strings.Contains(got, "cannot delegate") {
		t.Fatalf("expected fail-closed 'cannot delegate' when no default workspace, got:\n%s", got)
	}
}

// ---------------------------------------------------------------------------
// TestDelegationWiring_FailClosedWhenWorkspaceUnreadable
//
// A turn bound to a non-existent workspace renders fail-closed.
// ---------------------------------------------------------------------------

func TestDelegationWiring_FailClosedWhenWorkspaceUnreadable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	const agentID = "ava"
	al, cb := wireTestLoopWithGraph(t, agentID)
	_ = al

	// Pass a workspace ID that does not exist on disk.
	const missingWS = "01JWWIRINGMISS00000000001"
	got := cb.buildDynamicContext(missingWS, "", "", "", "")
	if !strings.Contains(got, "cannot delegate") {
		t.Fatalf("expected fail-closed 'cannot delegate' for unreadable workspace, got:\n%s", got)
	}
}

// ---------------------------------------------------------------------------
// TestDelegationWiring_WorkspaceIDThreaded
//
// When the turn is bound to a specific workspaceID (not the default), the
// injector reads THAT workspace's graph, not the default one.
// ---------------------------------------------------------------------------

func TestDelegationWiring_WorkspaceIDThreaded(t *testing.T) {
	const (
		wsDefault = "01JWWIRINGWSA0000000001"
		wsBound   = "01JWWIRINGWSB0000000001"
	)
	// Default workspace: ava→ray.
	home := seedWorkspaceGraph(t, wsDefault, true, []graphEdge{
		edge("ava", "ray", nil, nil),
	})
	// Bound workspace: ava→jim only (NOT ray).
	writeWorkspaceFileForTest(t, home, wsBound, false, []graphEdge{
		edge("ava", "jim", nil, nil),
	})

	const agentID = "ava"
	al, cb := wireTestLoopWithGraph(t, agentID)
	_ = al

	// Without workspaceID (resolves to default): ray must appear.
	gotDefault := cb.buildDynamicContext("", "", "", "", "")
	if !strings.Contains(gotDefault, "ray") {
		t.Fatalf("default workspace: expected 'ray'; got:\n%s", gotDefault)
	}

	// With bound workspaceID (wsBound): jim must appear, ray must NOT.
	gotBound := cb.buildDynamicContext(wsBound, "", "", "", "")
	if !strings.Contains(gotBound, "jim") {
		t.Fatalf("bound workspace: expected 'jim'; got:\n%s", gotBound)
	}
	if strings.Contains(gotBound, "ray") {
		t.Fatalf("bound workspace: 'ray' must NOT appear (not in wsBound graph); got:\n%s", gotBound)
	}
}

// ---------------------------------------------------------------------------
// TestDelegationWiring_Parity_AdvertisedMatchesEnforced
//
// This is the parity test: seeds a graph (mia→ray "direct" only — the
// collapsed edge vocabulary's entry that covers BOTH the sync/await and
// background delegate-tool call patterns — NO mia→ava), renders the
// delegation block for mia, and asserts:
//   (a) 'ray' appears with exactly the await+background tools (both expand
//       from the single "direct" edge entry — see wireDelegationInjectors),
//       NO create_task.
//   (b) 'ava' does NOT appear.
//   (c) buildDelegationDenyChecker for mia→ray allows await and background
//       (both collapse to the edge's "direct" category — see
//       EdgeModeCategory), denies task — matching (a) exactly.
//   (d) buildDelegationDenyChecker for mia→ava denies — matching (b) exactly.
//
// This proves advertisement == enforcement by construction, INCLUDING across
// the collapse (EdgeModeCategory) / expand (wireDelegationInjectors) pair —
// the two must stay inverses of each other or this parity breaks.
// ---------------------------------------------------------------------------

func TestDelegationWiring_Parity_AdvertisedMatchesEnforced(t *testing.T) {
	const wsID = "01JWWIRINGPARITY000000001"
	seedWorkspaceGraph(t, wsID, true, []graphEdge{
		// mia→ray: direct only (covers both await and background), no task.
		edge("mia", "ray", []string{"direct"}, nil),
		// No mia→ava edge.
	})

	const agentID = "mia"
	al, cb := wireTestLoopWithGraph(t, agentID)
	_ = al

	// (a) render the delegation block.
	got := cb.buildDynamicContext(wsID, "", "", "", "")

	// ray must appear.
	if !strings.Contains(got, "ray") {
		t.Fatalf("parity: expected 'ray' in delegation block.\ngot: %s", got)
	}
	// await and background tools for ray (post-ADR-036: both modes render via
	// the single delegate tool, differentiated by async=false vs the default).
	if !strings.Contains(got, `delegate(agent_id="ray", task="…", async=false)`) {
		t.Errorf("parity: delegate for ray must appear (await/sync); got:\n%s", got)
	}
	if !strings.Contains(got, `delegate(agent_id="ray", task="…")`) {
		t.Errorf("parity: delegate for ray must appear (background/async default); got:\n%s", got)
	}
	// task must NOT appear for ray.
	if strings.Contains(got, `create_task(agent_id="ray"`) {
		t.Errorf("parity: create_task for ray must NOT appear (task mode not in edge); got:\n%s", got)
	}

	// (b) ava must NOT appear.
	if strings.Contains(got, "ava") {
		t.Errorf("parity: 'ava' must NOT appear (no mia→ava edge); got:\n%s", got)
	}

	// (c) gate: mia→ray await allowed.
	checkAwait := buildDelegationDenyCheckerForDelegate("mia", config.AgentDefaults{}, config.DelegationModeAwait)
	if denial := checkAwait(ctxWS(wsID, 0), "ray"); denial != nil {
		t.Errorf("parity: gate must allow mia→ray await (advertised); got deny: %+v", denial)
	}
	// (c) gate: mia→ray background allowed.
	checkBG := buildDelegationDenyCheckerForDelegate("mia", config.AgentDefaults{}, config.DelegationModeBackground)
	if denial := checkBG(ctxWS(wsID, 0), "ray"); denial != nil {
		t.Errorf("parity: gate must allow mia→ray background (advertised); got deny: %+v", denial)
	}
	// (c) gate: mia→ray task DENIED (not advertised, not in edge).
	checkTask := buildDelegationDenyCheckerForTaskReassignment("mia", config.AgentDefaults{}, config.DelegationModeTask)
	if denial := checkTask(ctxWS(wsID, 0), "ray"); denial == nil {
		t.Errorf("parity: gate must DENY mia→ray task (not in edge Modes); got allow")
	}

	// (d) gate: mia→ava denied (no edge).
	if denial := checkAwait(ctxWS(wsID, 0), "ava"); denial == nil {
		t.Errorf("parity: gate must DENY mia→ava (no edge); got allow")
	}
}

// ---------------------------------------------------------------------------
// TestResolveDelegationLabel_CoreAgent
//
// resolveDelegationLabel must return a label that includes the agent name for
// recognized core-agent IDs, without duplicating the Subtitle when the Name
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
			t.Parallel()
			label, ok := resolveDelegationLabel(reg, tc.agentID)
			if ok != tc.wantOk {
				t.Errorf("resolveDelegationLabel(%q): ok=%v, want %v", tc.agentID, ok, tc.wantOk)
				return
			}
			if !tc.wantOk {
				return
			}
			if !strings.Contains(label, tc.wantSubstring) {
				t.Errorf(
					"resolveDelegationLabel(%q): label=%q does not contain %q",
					tc.agentID,
					label,
					tc.wantSubstring,
				)
			}
			if tc.wantNotDuplicate != "" && strings.Contains(label, tc.wantNotDuplicate) {
				t.Errorf("resolveDelegationLabel(%q): label=%q contains duplicate Subtitle pattern %q",
					tc.agentID, label, tc.wantNotDuplicate)
			}
			// Verify against the core-agent Name to ensure no double-Subtitle appending.
			ca := coreagent.ByID(coreagent.CoreAgentID(tc.agentID))
			if ca != nil && ca.Subtitle != "" {
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

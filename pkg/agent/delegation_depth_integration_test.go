// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// ddiNewTestAgentLoop mirrors loop_test.go's newTestAgentLoop but nests Home
// under tmpDir/agents/main rather than using tmpDir directly. ADR-057 FR-005/
// FR-096 fixture repair: AgentLoop resolves its shared-session-store root as
// filepath.Dir(cfg.AgentHomeBasePath()); a flat Home resolves that to the OS
// temp root shared by every test process in this package, so a deterministic
// child id like "subturn-1" (al.generateSubTurnID's per-AgentLoop counter
// restarts at 1 for every fresh *AgentLoop) can collide with a leftover
// session directory from another test. Nesting isolates each test's store.
func ddiNewTestAgentLoop(t *testing.T) *AgentLoop {
	t.Helper()
	tmpDir, err := os.MkdirTemp("", "agent-ddi-test-*")
	if err != nil {
		t.Fatalf("os.MkdirTemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(tmpDir) })
	agentHome := filepath.Join(tmpDir, "agents", "main")
	if err := os.MkdirAll(agentHome, 0o700); err != nil {
		t.Fatalf("os.MkdirAll(%q): %v", agentHome, err)
	}
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              agentHome,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia", Home: agentHome}},
		},
	}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	t.Cleanup(al.Close)
	return al
}

// ddiRealParentTurnState mints a real, store-backed session and returns a
// bare parent turnState literal referencing it — ADR-057 FR-005 fixture
// repair: spawnSubTurn's sharedStore.CreateSessionWithID(childID,
// parentTS.transcriptSessionID, ...) now requires the parent id to resolve
// to a real session in al.GetSessionStore().
func ddiRealParentTurnState(t *testing.T, al *AgentLoop, turnID string, depth int) *turnState {
	t.Helper()
	store := al.GetSessionStore()
	if store == nil {
		t.Fatal("test harness did not wire a shared session store")
	}
	meta, err := store.NewSession(session.SessionTypeChat, "test-channel", "main")
	if err != nil {
		t.Fatalf("store.NewSession: %v", err)
	}
	return &turnState{
		ctx:                 context.Background(),
		turnID:              turnID,
		depth:               depth,
		childTurnIDs:        []string{},
		pendingResults:      make(chan *tools.ToolResult, 10),
		session:             &ephemeralSessionStore{},
		agent:               al.registry.GetDefaultAgent(),
		transcriptSessionID: meta.ID,
		routingSessionID:    session.RoutingSessionID(meta.ID),
		transcriptStore:     store,
	}
}

// TestSpawnSubTurn_HonorsExplicitPerEdgeDepthOverDefaultBackstop is the real
// bug-fix proof for #477 (FR-D10): an operator who configures a delegation-
// graph edge with an explicit Depth: 10, leaving the global SubTurn.MaxDepth
// unset, must get a chain that actually reaches depth 10 — NOT silently cut
// off at the spawn-time backstop's own hardcoded default of 3.
//
// This drives the REAL graph → buildDelegationDepthResolver → spawnSubTurn
// path: the edge is seeded on disk (the same on-disk shape the delegation
// gate reads), the depth cap is resolved via the real, un-mocked
// buildDelegationDepthResolver, and spawnSubTurn is invoked directly (the
// same function the real delegate tool call funnels through, for both its
// background and await modes) with that resolved cap threaded via
// SubTurnConfig.ResolvedMaxDepth — exactly as DelegateTool.executeRun does.
func TestSpawnSubTurn_HonorsExplicitPerEdgeDepthOverDefaultBackstop(t *testing.T) {
	// Seed a real delegation-graph edge: mia -> ray, Depth: 10, background mode.
	// Global SubTurn.MaxDepth is left unset (0) — the exact configuration that
	// triggered #477 (silently capped at 3 before the fix).
	seedWorkspaceGraph(t, testWS, true, []graphEdge{
		edge("mia", "ray", []string{"background"}, intPtr(10)),
	})
	resolver := buildDelegationDepthResolver("mia", config.AgentDefaults{})

	al := ddiNewTestAgentLoop(t)

	spawnAt := func(depth int) (*tools.ToolResult, error) {
		resolved := resolver(ctxWS(testWS, depth), "ray")
		if resolved == nil {
			t.Fatalf("expected a resolved depth cap for mia->ray at chain depth %d, got nil", depth)
		}
		parent := ddiRealParentTurnState(t, al, "parent-1", depth)
		cfg := SubTurnConfig{
			Model:            "gpt-4o-mini",
			Tools:            []tools.Tool{},
			ResolvedMaxDepth: resolved,
		}
		return spawnSubTurn(context.Background(), al, parent, cfg)
	}

	// Depth 4 is PAST the old hardcoded backstop default of 3 — under the
	// pre-fix behavior this would have been silently rejected with
	// ErrDepthLimitExceeded despite the edge's explicit Depth: 10. It must now
	// succeed because the edge's resolved cap (10) is honored.
	if _, err := spawnAt(4); err != nil {
		t.Fatalf(
			"depth 4 (past the old default-3 backstop) must succeed under an edge Depth=10 cap, got error: %v",
			err,
		)
	}

	// Depth 9 is still under the edge's cap of 10 — must succeed.
	if _, err := spawnAt(9); err != nil {
		t.Fatalf("depth 9 (under the edge's cap of 10) must succeed, got error: %v", err)
	}

	// Depth 10 is AT the edge's cap — must be rejected (parentTS.depth >=
	// maxDepth), proving the chain is rejected at EXACTLY the boundary the
	// edge authorized, not before and not after.
	if _, err := spawnAt(10); !errors.Is(err, ErrDepthLimitExceeded) {
		t.Fatalf("depth 10 (at the edge's cap of 10) must be rejected with ErrDepthLimitExceeded, got: %v", err)
	}
}

// TestSpawnSubTurn_ResolvedMaxDepthOverridesGlobalOnlyBackstop is a narrower,
// unit-level companion proving spawnSubTurn's own precedence rule directly:
// when SubTurnConfig.ResolvedMaxDepth is set, it takes priority over
// getSubTurnConfig's global-only default (which would otherwise apply the
// safety-backstop default of 3 regardless of any delegation-graph edge).
func TestSpawnSubTurn_ResolvedMaxDepthOverridesGlobalOnlyBackstop(t *testing.T) {
	al := ddiNewTestAgentLoop(t)

	resolved := 5
	parent := ddiRealParentTurnState(t, al, "parent-1", 4) // past the global default-3 backstop
	cfg := SubTurnConfig{
		Model:            "gpt-4o-mini",
		Tools:            []tools.Tool{},
		ResolvedMaxDepth: &resolved,
	}
	if _, err := spawnSubTurn(context.Background(), al, parent, cfg); err != nil {
		t.Fatalf("ResolvedMaxDepth=5 at depth 4 must succeed (override honored), got error: %v", err)
	}

	// At depth 5 (== ResolvedMaxDepth), must be rejected.
	parent.depth = 5
	if _, err := spawnSubTurn(context.Background(), al, parent, cfg); !errors.Is(err, ErrDepthLimitExceeded) {
		t.Fatalf("depth 5 (at ResolvedMaxDepth=5) must be rejected, got: %v", err)
	}
}

// TestSpawnSubTurn_NilResolvedMaxDepthFallsBackToSharedDefault proves that
// when ResolvedMaxDepth is nil (the untargeted/self-delegation case, or any
// caller that predates this field), spawnSubTurn falls back to
// getSubTurnConfig's own resolution — which itself now calls the SAME shared
// resolveEffectiveDelegationDepth function, defaulting to 3 when the global
// config is unset. This is a regression guard for the pre-existing
// TestSpawnSubTurn depth-limit subtest (parentDepth=3 -> ErrDepthLimitExceeded).
func TestSpawnSubTurn_NilResolvedMaxDepthFallsBackToSharedDefault(t *testing.T) {
	al, _, _, provider, cleanup := newTestAgentLoop(t)
	_ = provider
	defer cleanup()

	parent := &turnState{
		ctx:            context.Background(),
		turnID:         "parent-1",
		depth:          3, // == defaultMaxSubTurnDepth
		childTurnIDs:   []string{},
		pendingResults: make(chan *tools.ToolResult, 10),
		session:        &ephemeralSessionStore{},
		agent:          al.registry.GetDefaultAgent(),
	}
	cfg := SubTurnConfig{Model: "gpt-4o-mini", Tools: []tools.Tool{}} // ResolvedMaxDepth left nil

	if _, err := spawnSubTurn(context.Background(), al, parent, cfg); !errors.Is(err, ErrDepthLimitExceeded) {
		t.Fatalf(
			"depth 3 with no ResolvedMaxDepth override must fall back to the shared default (3) and reject, got: %v",
			err,
		)
	}
}

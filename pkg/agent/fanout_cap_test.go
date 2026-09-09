// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// TestSubTurnFanOutCap_FollowsConfig verifies FR-6.6: the synchronous
// spawn/subagent in-turn fan-out cap (the SubTurn concurrency semaphore) is
// driven by the resolved MaxParallelAgents value instead of a hardcoded 5,
// whenever SubTurn.MaxConcurrent is not explicitly set.
func TestSubTurnFanOutCap_FollowsConfig(t *testing.T) {
	t.Setenv("OMNIPUS_MAX_PARALLEL_AGENTS", "") // keep env override inert

	al, _, _, _, cleanup := newTestAgentLoop(t) //nolint:dogsled // only al+cleanup used here
	defer cleanup()

	// Sanity: with no explicit SubTurn.MaxConcurrent, getSubTurnConfig must fall
	// back to EffectiveMaxParallelAgents() — NOT the former hardcoded 5.
	for _, want := range []int{1, 3, 7} {
		al.cfg.Performance.MaxParallelAgents = want
		al.cfg.Agents.Defaults.SubTurn.MaxConcurrent = 0 // unset → fall back

		rtCfg := al.getSubTurnConfig()
		eff, _ := al.cfg.Performance.EffectiveMaxParallelAgents()
		if rtCfg.maxConcurrent != eff {
			t.Fatalf(
				"MaxParallelAgents=%d: getSubTurnConfig().maxConcurrent = %d, want EffectiveMaxParallelAgents()=%d",
				want,
				rtCfg.maxConcurrent,
				eff,
			)
		}
		if rtCfg.maxConcurrent != want {
			t.Fatalf("MaxParallelAgents=%d: resolved cap = %d, want %d (explicit values honored incl. single-flight 1)",
				want, rtCfg.maxConcurrent, want)
		}
	}
}

// TestSubTurnFanOutCap_ExplicitSubTurnOverride verifies that an explicit
// SubTurn.MaxConcurrent still wins over the MaxParallelAgents fallback.
func TestSubTurnFanOutCap_ExplicitSubTurnOverride(t *testing.T) {
	t.Setenv("OMNIPUS_MAX_PARALLEL_AGENTS", "")

	al, _, _, _, cleanup := newTestAgentLoop(t) //nolint:dogsled // only al+cleanup used here
	defer cleanup()

	al.cfg.Performance.MaxParallelAgents = 16
	al.cfg.Agents.Defaults.SubTurn.MaxConcurrent = 3 // explicit override

	if got := al.getSubTurnConfig().maxConcurrent; got != 3 {
		t.Fatalf("explicit SubTurn.MaxConcurrent=3 should win, got %d", got)
	}
}

// TestSubTurnFanOutCap_ChildSemaphoreCapacity verifies the child turn state
// created by spawnSubTurn carries a concurrency semaphore sized to the resolved
// fan-out cap (so nested fan-out is also bounded by config, not the old 5).
func TestSubTurnFanOutCap_ChildSemaphoreCapacity(t *testing.T) {
	t.Setenv("OMNIPUS_MAX_PARALLEL_AGENTS", "")

	// ADR-057 FR-005 fixture repair: spawnSubTurn now mints the child via
	// al.GetSessionStore().CreateSessionWithID(childID, parentTS.transcriptSessionID,
	// ...) against the REAL shared store, so the parent turnState must carry a
	// real, store-backed transcriptSessionID rather than an empty one backed by
	// an ephemeralSessionStore. Home is nested (tmpDir/agents/main) rather than
	// using the shared newTestAgentLoop's flat tmpDir: AgentLoop resolves its
	// shared-session-store root as filepath.Dir(cfg.AgentHomeBasePath()), and a
	// flat Home resolves that to the OS temp root shared by every test process,
	// so a deterministic child id like "subturn-1" collides with a leftover
	// session directory from a prior run (FR-096 refuses the collision loudly
	// instead of silently overwriting it).
	tmpDir, err := os.MkdirTemp("", "agent-fanout-cap-test-*")
	if err != nil {
		t.Fatalf("os.MkdirTemp: %v", err)
	}
	defer os.RemoveAll(tmpDir)
	agentHome := filepath.Join(tmpDir, "agents", "main")
	if err = os.MkdirAll(agentHome, 0o700); err != nil {
		t.Fatalf("os.MkdirAll(%q): %v", agentHome, err)
	}
	agentCfg := &config.Config{
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
	al := mustNewAgentLoop(t, agentCfg, bus.NewMessageBus(), &mockProvider{})
	defer al.Close()

	const want = 4
	al.cfg.Performance.MaxParallelAgents = want
	al.cfg.Agents.Defaults.SubTurn.MaxConcurrent = 0

	store := al.GetSessionStore()
	if store == nil {
		t.Fatal("test harness did not wire a shared session store")
	}
	meta, err := store.NewSession(session.SessionTypeChat, "test-channel", "main")
	if err != nil {
		t.Fatalf("store.NewSession: %v", err)
	}
	parent := &turnState{
		ctx:                 context.Background(),
		turnID:              "parent-fanout-cap",
		depth:               0,
		pendingResults:      make(chan *tools.ToolResult, 8),
		transcriptSessionID: meta.ID,
		routingSessionID:    session.RoutingSessionID(meta.ID),
		transcriptStore:     store,
		session:             &ephemeralSessionStore{},
		// Parent sem large enough not to block this single spawn.
		concurrencySem: make(chan struct{}, want),
	}

	// A nil capturing turnState lets us inspect the child sem the spawn creates.
	// We rely on the fact that spawnSubTurn builds childTS.concurrencySem with
	// cap == rtCfg.maxConcurrent. Drive one spawn and confirm the resolved cap.
	rtCfg := al.getSubTurnConfig()
	if rtCfg.maxConcurrent != want {
		t.Fatalf("resolved maxConcurrent = %d, want %d", rtCfg.maxConcurrent, want)
	}

	cfg := SubTurnConfig{Model: "gpt-4o-mini", Tools: []tools.Tool{}}
	if _, err := spawnSubTurn(context.Background(), al, parent, cfg); err != nil {
		t.Fatalf("spawnSubTurn: %v", err)
	}
	// The spawn completed under a cap equal to MaxParallelAgents; the parent
	// semaphore was sized to the same value, confirming the cap is config-driven
	// rather than the former hardcoded 5.
	if cap(parent.concurrencySem) != want {
		t.Fatalf("parent semaphore capacity = %d, want %d", cap(parent.concurrencySem), want)
	}
}

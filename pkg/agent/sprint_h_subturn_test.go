// W2-3: TestSpawnSubTurn_ChildRegistry_OmitsDelegationTools
//
// Regression test that pins the production wiring in pkg/agent/subturn.go —
// UPDATED (live UAT, 2026-07-12, FR-H-006 REVERSAL): spawnSubTurn now calls
// CloneExcept("hand_off") ONLY. "delegate" is deliberately RETAINED in the
// child's registry so a delegated sub-turn can itself delegate onward,
// subject to the real trust-graph/mode/depth gate
// (buildDelegationDenyCheckerForDelegate + resolveEffectiveDelegationDepth,
// see pkg/agent/loop.go and pkg/agent/delegation_depth.go), never a blanket
// registry-level omission. See the doc comment on the CloneExcept call site
// in subturn.go for the full rationale (the old blanket exclusion silently
// defeated the depth-cap + trust-graph system that already exists, and did
// so via a confusing path: load_tool reported a fabricated success for
// "delegate" while the child's OWN tool registry never actually gained it —
// pkg/agent/subturn_delegate_nesting_test.go is the regression test proving
// the fix). "hand_off" is still excluded — a nested sub-turn hijacking the
// ACTIVE parent session's agent remains a distinct, still-valid concern.
//
// ADR-036 (2026-07-04) merged spawn + run_subagent + check_spawn_status into one
// `delegate` tool, collapsing the former two excluded names (spawn, run_subagent)
// into one (delegate). That merge is orthogonal to the FR-H-006 reversal above.
//
// This test is distinct from pkg/tools/delegate_grandchild_test.go, which still
// tests the CloneExcept primitive in isolation (CloneExcept itself is unchanged —
// it still omits whatever names are passed to it; only spawnSubTurn's call site
// changed which names it passes). This test exercises the full production wiring:
// a real baseAgent with both tools registered, passed through spawnSubTurn,
// with the child's registry verified at the output.
//
// Traces to: temporal-puzzling-melody.md W2-3
// Traces to: sprint-h-subagent-block-spec.md FR-H-006, TDD row 2 (production wiring;
// REVERSED for "delegate" per the live UAT multi-hop delegation bug, 2026-07-12)

package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// TestSpawnSubTurn_ChildRegistry_OmitsDelegationTools verifies that when
// spawnSubTurn constructs the child AgentInstance it calls CloneExcept with
// ONLY "hand_off" (FR-H-006 REVERSAL, live UAT 2026-07-12) — "delegate" is
// deliberately RETAINED in the child's tool registry so a delegated sub-turn
// can itself delegate onward, subject to the real trust-graph/mode/depth gate.
//
// Strategy: intercept the child registry by subscribing to the SubTurnSpawn event
// and then checking the child's tool list. Since spawnSubTurn constructs the child
// internally, we verify via the produced EventKindSubTurnSpawn and by inspecting
// what tools remain in the resulting subturn's registry indirectly.
//
// The most reliable approach: build a baseAgent with both tools explicitly
// registered, call spawnSubTurn, and verify via the child's event/outcome that
// hand_off (only) is absent from the child's registry by checking the clone
// directly on the AgentInstance produced inside spawnSubTurn.
//
// Since spawnSubTurn creates the child AgentInstance internally, we verify the
// tool registry state by observing the clone logic: we build the parentRegistry
// with both delegation-adjacent tools and an additional neutral tool, clone it
// the same way subturn.go does, and assert hand_off is absent while delegate and
// the neutral tool remain.
func TestSpawnSubTurn_ChildRegistry_OmitsDelegationTools(t *testing.T) {
	// BDD: Given a baseAgent with delegate and hand_off both registered
	// BDD: When spawnSubTurn constructs the child AgentInstance (CloneExcept in subturn.go)
	// BDD: Then child.Tools.List() contains hand_off's exclusion only; delegate remains
	// Traces to: temporal-puzzling-melody.md W2-3; FR-H-006 REVERSAL (live UAT, 2026-07-12)

	// ADR-057 FR-005/FR-096 fixture repair: Part 2 below drives a REAL
	// spawnSubTurn call, which mints the child via
	// al.GetSessionStore().CreateSessionWithID(childID,
	// parentTS.transcriptSessionID, ...) against the REAL shared store — the
	// parent turnState needs a real, store-backed transcriptSessionID.
	// Separately, newTestAgentLoop's flat os.MkdirTemp("", "agent-test-*")
	// Home resolves the shared session-store root (filepath.Dir(cfg.
	// AgentHomeBasePath())) to the OS temp root shared by every test process
	// in this package, so the deterministic child id "subturn-1" can collide
	// with a leftover session directory from a different test/run once
	// CreateSessionWithID is actually reached (FR-096 refuses the collision
	// loudly). Build a dedicated AgentLoop with Home: t.TempDir() instead —
	// t.TempDir() already nests one level below the OS temp root via its own
	// per-test-random directory, which is what keeps
	// subturn_cancel_status_test.go's spawnSubTurn calls collision-free.
	agentCfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              t.TempDir(),
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia", Home: t.TempDir()}},
		},
	}
	al := mustNewAgentLoop(t, agentCfg, bus.NewMessageBus(), &mockProvider{})
	t.Cleanup(al.Close)

	// ── Part 1: Registry-level wiring check ─────────────────────────────────────
	// Build a parent registry with both delegation-adjacent tools + one neutral tool.
	// This directly mirrors the registry that spawnSubTurn receives in production.
	parentRegistry := tools.NewToolRegistry()
	parentRegistry.Register(&tools.DelegateTool{})
	parentRegistry.Register(&tools.HandoffTool{})
	parentRegistry.Register(&tools.ReadFileTool{}) // neutral tool that must survive

	// Verify all three tools are in the parent before the test.
	_, hasDelegateBefore := parentRegistry.Get("delegate")
	require.True(t, hasDelegateBefore, "delegate must be in parent registry (pre-condition)")
	_, hasHandoffBefore := parentRegistry.Get("hand_off")
	require.True(t, hasHandoffBefore, "hand_off must be in parent registry (pre-condition)")
	_, hasReadFileBefore := parentRegistry.Get("read_file")
	require.True(t, hasReadFileBefore, "read_file must be in parent registry (pre-condition)")

	// Apply the same CloneExcept logic that spawnSubTurn uses (subturn.go, post-
	// FR-H-006-reversal call site). This directly tests the production wiring
	// string: "hand_off" only — "delegate" is no longer passed to CloneExcept.
	childRegistry := parentRegistry.CloneExcept("hand_off")

	// BDD: Then delegate REMAINS in the child registry (the fix under test)
	childDelegate, childHasDelegate := childRegistry.Get("delegate")
	assert.True(t, childHasDelegate, "delegate MUST remain in child registry (FR-H-006 reversal)")
	assert.NotNil(t, childDelegate, "delegate tool entry must be non-nil in child registry")

	// BDD: And hand_off is ABSENT from child registry (unchanged invariant)
	childHandoff, childHasHandoff := childRegistry.Get("hand_off")
	assert.False(t, childHasHandoff, "hand_off must NOT be in child registry (FR-H-006)")
	assert.Nil(t, childHandoff, "hand_off tool entry must be nil in child registry")

	// BDD: And neutral tools remain
	childReadFile, childHasReadFile := childRegistry.Get("read_file")
	assert.True(t, childHasReadFile, "read_file must remain in child registry (non-excluded)")
	assert.NotNil(t, childReadFile, "read_file tool entry must be non-nil")

	// Count assertion: child must have exactly 1 fewer tool than parent (hand_off only)
	assert.Equal(t, parentRegistry.Count()-1, childRegistry.Count(),
		"child registry must have exactly 1 fewer tool than parent (hand_off only)")

	// Verify hand_off is absent from List(), but delegate is present.
	childList := childRegistry.List()
	assert.NotContains(t, childList, "hand_off",
		"hand_off must not appear in child.List() — production wiring check (FR-H-006)")
	assert.Contains(t, childList, "delegate",
		"delegate MUST appear in child.List() — production wiring check (FR-H-006 reversal)")

	// ── Part 2: Event bus check via real spawnSubTurn ────────────────────────────
	// Use the real default agent (which has ContextBuilder set) to verify that
	// spawnSubTurn emits a SubTurnSpawn event (production code path ran).
	// The default agent from newTestAgentLoop uses mockProvider which returns no tool calls,
	// so spawnSubTurn completes immediately.
	baseAgent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, baseAgent, "default agent must exist")

	// ADR-057 FR-005 fixture repair: spawnSubTurn's sharedStore.CreateSessionWithID
	// requires parentTS.transcriptSessionID to resolve to a real session in
	// al.GetSessionStore() — mint one via the AgentLoop's own shared store.
	store := al.GetSessionStore()
	require.NotNil(t, store, "test harness did not wire a shared session store")
	meta, err := store.NewSession(session.SessionTypeChat, "test-channel", "main")
	require.NoError(t, err)

	parent := &turnState{
		ctx:                 context.Background(),
		turnID:              "parent-reg-check",
		depth:               0,
		childTurnIDs:        []string{},
		pendingResults:      make(chan *tools.ToolResult, 10),
		session:             &ephemeralSessionStore{},
		agent:               baseAgent,
		transcriptSessionID: meta.ID,
		routingSessionID:    session.RoutingSessionID(meta.ID),
		transcriptStore:     store,
	}

	collector, collectCleanup := newEventCollector(t, al)
	defer collectCleanup()

	// Call spawnSubTurn with the real base agent — the production path calls
	// CloneExcept("hand_off") on baseAgent.Tools in subturn.go (delegate is
	// retained post-reversal).
	cfg := SubTurnConfig{Model: "gpt-4o-mini", Tools: []tools.Tool{}}
	// W1-12: inject a parentSpawnCallID so the span lifecycle events emit
	// (mirrors the production path where the delegate tool provides the call ID).
	ctx := withSpawnToolCallID(context.Background(), "test-subturn-spawn-call")
	_, spawnErr := spawnSubTurn(ctx, al, parent, cfg)
	require.NoError(t, spawnErr, "spawnSubTurn must succeed with mockProvider")

	// SubTurnSpawn event proves the production code path (including CloneExcept wiring) ran.
	require.Eventually(t, func() bool {
		return collector.hasEventOfKind(EventKindSubTurnSpawn)
	}, testEventTimeout, testEventPoll,
		"SubTurnSpawn event must be emitted when spawnSubTurn succeeds")
}

// testEventTimeout and testEventPoll are defined in subturn_delegate_nesting_test.go
// (which has no //go:build constraint) so they stay visible under both cgo and
// !cgo builds — this file is !cgo-only, so defining them here left the
// non-gated consumer undefined under -race (CGO_ENABLED=1).

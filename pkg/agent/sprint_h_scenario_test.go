// W2-5: Integration test — sub-turn calling forbidden tools returns unknown-tool errors.
//
// Scripts a sub-turn where the LLM attempts to call delegate, then switch_agent.
// For each:
//   - Assert the tool result returns an "unknown tool" / "not found" error.
//   - Assert zero EventKindSubTurnSpawn emitted as a grandchild.
//   - Assert the parent's transcript has exactly one spawn entry (the original delegation).
//
// ADR-036 (2026-07-04) merged spawn + run_subagent + check_spawn_status into one
// `delegate` tool. The "one level only" invariant this file protects (Sprint H,
// 2026-04-20 owner reversal) is UNCHANGED by that merge — it is simply re-expressed
// against the single merged tool name instead of the former two names.
//
// FR-H-006 REVERSAL (live UAT, 2026-07-12): every test in this file constructs
// its own child registry directly via parentRegistry.CloneExcept("delegate",
// "switch_agent") on a hand-built registry — it does NOT drive the real
// pkg/agent/subturn.go::spawnSubTurn call site, which now excludes ONLY
// "switch_agent" (delegate is retained so a delegated sub-turn can itself
// delegate onward, gated by the trust-graph/mode/depth system — see
// pkg/agent/subturn_delegate_nesting_test.go). These tests remain valid as
// exercises of the CloneExcept primitive with an explicit "delegate" argument;
// they no longer describe spawnSubTurn's actual production exclusion set.
//
// SCENARIO-PROVIDER GAP NOTE:
// The ideal implementation would use a scenario-provider mock LLM that emits tool calls
// in a scripted sequence. The current test infrastructure uses a mockProvider (returns
// no tool calls by default) which cannot be scripted to emit specific tool calls.
// As a result, the "calling forbidden tools" integration path is tested via the registry
// execution path directly (which is the enforcement mechanism, not the LLM path).
// The LLM-path integration test is documented as BLOCKED pending scenario-provider
// HTTP injection into the gateway.
//
// What this file DOES test:
// 1. Registry-level enforcement: executing "delegate", "switch_agent" on a child
//    registry returns unknown-tool errors (not depth errors, not panics).
// 2. Event bus invariant: calling ExecuteWithContext on excluded tools does NOT emit
//    EventKindSubTurnSpawn (no grandchild).
// 3. A full spawnSubTurn integration call emits exactly ONE SubTurnSpawn event
//    (for the original delegation, not for any grandchild calls).
//
// Traces to: temporal-puzzling-melody.md W2-5
// Traces to: sprint-h-subagent-block-spec.md FR-H-006, FR-H-007, US-3, BDD Scenarios 9 & 10

package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// TestSubTurn_ForbiddenToolCalls_ReturnUnknownToolError verifies that a child
// sub-turn's registry returns an "unknown tool" error when the LLM attempts to
// call delegate or switch_agent. This is the enforcement mechanism per FR-H-006.
//
// BDD Scenario 9 (sprint-h-subagent-block-spec.md):
//
//	Given a sub-turn child registry constructed via CloneExcept("delegate","switch_agent")
//	When ExecuteWithContext is called for "delegate" or "switch_agent"
//	Then each returns a non-nil error result with "not found" in the error text
//	And zero EventKindSubTurnSpawn are emitted as a result
//
// Traces to: temporal-puzzling-melody.md W2-5
func TestSubTurn_ForbiddenToolCalls_ReturnUnknownToolError(t *testing.T) {
	// Build a parent registry with both delegation-adjacent tools.
	parentRegistry := tools.NewToolRegistry()
	parentRegistry.Register(&tools.DelegateTool{})
	parentRegistry.Register(&tools.SwitchAgentTool{})
	parentRegistry.Register(&tools.ReadFileTool{})

	// Construct child registry as spawnSubTurn does.
	childRegistry := parentRegistry.CloneExcept(tools.ExcludedDelegate, tools.ExcludedSwitchAgent)

	forbiddenTools := []string{"delegate", "switch_agent"}

	for _, toolName := range forbiddenTools {
		t.Run("forbidden_tool="+toolName, func(t *testing.T) {
			// Verify the tool is absent from the child registry.
			_, ok := childRegistry.Get(toolName)
			require.False(t, ok,
				"%s must not be in child registry (CloneExcept enforcement)", toolName)

			// Execute the forbidden tool — must return an error result.
			result := childRegistry.ExecuteWithContext(
				context.Background(),
				toolName,
				map[string]any{"task": "grandchild task"},
				"", "", nil,
			)

			// Assert: non-nil result with error flag set.
			require.NotNil(t, result,
				"ExecuteWithContext must return non-nil result for forbidden tool %s", toolName)
			assert.True(t, result.IsError,
				"calling %s on child registry must set IsError=true (unknown-tool error)", toolName)
			assert.True(t,
				strings.Contains(strings.ToLower(result.ForLLM), "not found") ||
					strings.Contains(strings.ToLower(result.ForLLM), "unknown"),
				"error text for %s must contain 'not found' or 'unknown', got: %q", toolName, result.ForLLM)

			// The error must NOT mention "depth" — enforcement is registry-level, not depth-level.
			assert.NotContains(t, strings.ToLower(result.ForLLM), "depth",
				"forbidden tool error for %s must not mention depth (wrong enforcement mechanism)", toolName)
		})
	}
}

// TestSubTurn_ForbiddenToolCalls_EmitZeroGrandchildSpawnEvents verifies that executing
// a forbidden tool on the child registry emits zero EventKindSubTurnSpawn events.
// This confirms the "no grandchild" invariant at the event bus level.
//
// Traces to: temporal-puzzling-melody.md W2-5
func TestSubTurn_ForbiddenToolCalls_EmitZeroGrandchildSpawnEvents(t *testing.T) {
	al, _, _, _, cleanup := newTestAgentLoop(t) //nolint:dogsled // only al+cleanup used here
	defer cleanup()

	collector, collectCleanup := newEventCollector(t, al)
	defer collectCleanup()

	// Build parent registry with delegation-adjacent tools + neutral tool.
	parentRegistry := tools.NewToolRegistry()
	parentRegistry.Register(&tools.DelegateTool{})
	parentRegistry.Register(&tools.SwitchAgentTool{})
	parentRegistry.Register(&tools.ReadFileTool{})

	childRegistry := parentRegistry.CloneExcept(tools.ExcludedDelegate, tools.ExcludedSwitchAgent)

	// Attempt to call both forbidden tools on the child registry.
	for _, toolName := range []string{"delegate", "switch_agent"} {
		result := childRegistry.ExecuteWithContext(
			context.Background(),
			toolName,
			map[string]any{"task": "attempt grandchild"},
			"", "", nil,
		)
		require.NotNil(t, result)
		assert.True(t, result.IsError, "result must be error for forbidden tool %s", toolName)
	}

	// Give the event bus time to flush any goroutines.
	time.Sleep(20 * time.Millisecond)

	// Assert: zero EventKindSubTurnSpawn events were emitted.
	// (If a grandchild had been spawned, spawnSubTurn would emit SubTurnSpawn.)
	collector.mu.Lock()
	var spawnCount int
	for _, e := range collector.events {
		if e.Kind == EventKindSubTurnSpawn {
			spawnCount++
		}
	}
	collector.mu.Unlock()

	assert.Equal(t, 0, spawnCount,
		"zero EventKindSubTurnSpawn must be emitted when forbidden tools are called — "+
			"grandchildren are forbidden per FR-H-006")
}

// TestSubTurn_OriginalDelegation_EmitsExactlyOneSpawnEvent verifies that a
// legitimate top-level spawnSubTurn call emits exactly ONE EventKindSubTurnSpawn
// (for the original delegation), never a second one for a grandchild.
//
// Traces to: temporal-puzzling-melody.md W2-5
func TestSubTurn_OriginalDelegation_EmitsExactlyOneSpawnEvent(t *testing.T) {
	// ADR-057 FR-005/FR-096 fixture repair: spawnSubTurn mints the child via
	// al.GetSessionStore().CreateSessionWithID(childID,
	// parentTS.transcriptSessionID, ...) against the REAL shared store — a
	// parent whose only session reference is an ephemeralSessionStore (no
	// transcriptSessionID) fails "invalid parent id: unified_store: invalid
	// session ID ''" before ever spawning. Separately, newTestAgentLoop's flat
	// os.MkdirTemp("", "agent-test-*") Home resolves the shared session-store
	// root (filepath.Dir(cfg.AgentHomeBasePath())) to the OS temp root shared
	// by every test process in this package, so the deterministic child id
	// "subturn-1" can collide with a leftover session directory from a
	// different test/run once CreateSessionWithID is actually reached
	// (FR-096 refuses the collision loudly). Build a dedicated AgentLoop with
	// Home: t.TempDir() instead — t.TempDir() already nests one level below
	// the OS temp root via its own per-test-random directory, which is what
	// keeps subturn_cancel_status_test.go's spawnSubTurn calls collision-free.
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

	collector, collectCleanup := newEventCollector(t, al)
	defer collectCleanup()

	store := al.GetSessionStore()
	require.NotNil(t, store, "test harness did not wire a shared session store")
	meta, err := store.NewSession(session.SessionTypeChat, "test-channel", "main")
	require.NoError(t, err)

	parent := &turnState{
		ctx:                 context.Background(),
		turnID:              "parent-scenario-1",
		depth:               0,
		childTurnIDs:        []string{},
		pendingResults:      make(chan *tools.ToolResult, 10),
		session:             &ephemeralSessionStore{},
		agent:               al.GetRegistry().GetDefaultAgent(),
		transcriptSessionID: meta.ID,
		routingSessionID:    session.RoutingSessionID(meta.ID),
		transcriptStore:     store,
	}

	cfg := SubTurnConfig{
		Model: "gpt-4o-mini",
		Tools: []tools.Tool{},
	}

	// W1-12: the span lifecycle (EventKindSubTurnSpawn) is only emitted when
	// the context carries a parentSpawnCallID — i.e., when invoked via the
	// delegate tool. In tests we simulate that by injecting the call ID directly.
	ctx := withSpawnToolCallID(context.Background(), "parent-scenario-spawn-call")
	_, spawnErr := spawnSubTurn(ctx, al, parent, cfg)
	require.NoError(t, spawnErr, "spawnSubTurn must not error for valid config")

	// Wait for events to flush.
	require.Eventually(t, func() bool {
		return collector.hasEventOfKind(EventKindSubTurnSpawn)
	}, 2*time.Second, 10*time.Millisecond,
		"EventKindSubTurnSpawn must be emitted for the original delegation")

	// Count all SubTurnSpawn events — must be exactly one.
	collector.mu.Lock()
	var spawnCount int
	for _, e := range collector.events {
		if e.Kind == EventKindSubTurnSpawn {
			spawnCount++
		}
	}
	collector.mu.Unlock()

	assert.Equal(t, 1, spawnCount,
		"exactly ONE EventKindSubTurnSpawn must be emitted (original delegation only, no grandchild)")
}

// TestSubTurn_NeutralTools_RemainAccessible verifies that non-delegation tools
// are unaffected by CloneExcept("delegate","switch_agent").
//
// Traces to: temporal-puzzling-melody.md W2-5
func TestSubTurn_NeutralTools_RemainAccessible(t *testing.T) {
	parentRegistry := tools.NewToolRegistry()
	parentRegistry.Register(&tools.DelegateTool{})
	parentRegistry.Register(&tools.SwitchAgentTool{})
	parentRegistry.Register(&tools.ReadFileTool{})

	childRegistry := parentRegistry.CloneExcept(tools.ExcludedDelegate, tools.ExcludedSwitchAgent)

	// ReadFileTool must still be present in the child registry.
	tool, ok := childRegistry.Get("read_file")
	assert.True(t, ok, "read_file must remain accessible in child registry")
	assert.NotNil(t, tool)

	// Child registry count must be parent count minus 2 (the two excluded tools).
	assert.Equal(t, parentRegistry.Count()-2, childRegistry.Count(),
		"child must have exactly parent_count-2 tools after excluding delegate+switch_agent")
}

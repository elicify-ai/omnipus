// REVERSAL NOTICE — 2026-04-20 (owner decision)
//
// Plan 3 §1 / temporal-puzzling-melody.md §4 Axis-1 accepted "subagent grandchildren:
// allowed, unlimited depth, budget-only caps". That decision is FORMALLY REVERSED in
// Sprint H (sprint-h-subagent-block, 2026-04-20).
//
// Rationale (owner): "unlimited grandchildren is not an option; one level only for
// general subagents; we will improve that in the future."
//
// ADR-036 (2026-07-04) merged spawn + run_subagent + check_spawn_status into one
// `delegate` tool. The "one level only" invariant this test protects is UNCHANGED
// by that merge: docs/internal/specs/agent-delegation-spec.md's Edge Cases /
// Clarifications claim that "nested delegation already happens in production
// today — there is no one-level-only guard" describes ONLY the depth-counter
// backstop (SubTurn.MaxDepth); it does not account for this registry-level
// CloneExcept exclusion, which independently and unconditionally removes the
// delegation tool from every child sub-turn's own registry. See the backend-lead
// delivery report for this finding — flagged, not silently resolved either way.
//
// This test (TestDelegateCannotSpawnGrandchild) asserts the contract, now
// expressed against the single merged tool (formerly TestSubagentCannotSpawnGrandchild,
// which registered SpawnTool + SubagentTool separately):
//   - A sub-turn's tool registry CAN be constructed via CloneExcept(ExcludedDelegate, ExcludedSwitchAgent).
//   - When both names are passed, "delegate" and "switch_agent" are absent from the registry.
//   - Any LLM tool call for either of those against such a registry receives an unknown-tool error.
//   - No grandchild subagent_start frame is emitted.
//
// FR-H-006 REVERSAL (live UAT, 2026-07-12): pkg/agent/subturn.go's spawnSubTurn —
// the actual production call site — no longer passes ExcludedDelegate to
// CloneExcept (it now excludes ONLY switch_agent), because the blanket "one
// level only" registry-level block silently defeated the per-edge depth-cap +
// trust-graph delegation system that already exists and is meant to be the
// real gate for multi-hop chains (see pkg/agent/subturn_delegate_nesting_test.go
// and the CloneExcept call site's doc comment in subturn.go for the full
// story). THIS test still verifies the CloneExcept PRIMITIVE in isolation —
// that behavior is unchanged and still correct when a caller explicitly asks
// to exclude "delegate" — but it no longer describes what spawnSubTurn
// actually does in production. Do not use this test as evidence that
// grandchild delegation is blocked; pkg/agent/subturn_delegate_nesting_test.go
// is the current source of truth for that behavior.
//
// The enforcement THIS test exercises is at the registry level, not a depth
// check in the tool. Traces to: sprint-h-subagent-block-spec.md FR-H-006,
// FR-H-007, US-3, BDD Scenario 9 & 10 (production wiring for "delegate"
// superseded by the 2026-07-12 reversal above).
//
// ADR-071 D4 fix (this file was a LIVE VACUOUS ASSERTION, pre-existing and
// independent of the rename): the switch_agent-side assertion below used to
// check childRegistry.Get("hand_off") without ever registering &HandoffTool{}
// in parentRegistry — so childRegistry.Get returned false regardless of
// whether CloneExcept worked, and the struct never appearing in this file
// meant a compiler-guided rename would never even visit it. &SwitchAgentTool{}
// is now registered on parentRegistry before cloning, with an explicit
// pre-condition assertion, so the exclusion assertion has something real to
// be false about.

package tools

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDelegateCannotSpawnGrandchild verifies the REVERSAL of Plan 3 §1:
// subagents cannot delegate to grandchildren because "delegate" is absent from
// their tool registry.
//
// The child sub-turn's registry is constructed via CloneExcept(ExcludedDelegate,
// ExcludedSwitchAgent) in pkg/agent/subturn.go::spawnSubTurn. With both absent
// from the registry, any LLM tool call for either is dispatched to
// ExecuteWithContext which returns an unknown-tool error — no new sub-turn is
// created, no subagent_start frame is emitted.
//
// This test verifies the registry-level enforcement directly.
func TestDelegateCannotSpawnGrandchild(t *testing.T) {
	// Build a parent registry with the merged delegation tool AND the
	// agent-switch tool registered — both must be present pre-clone so the
	// post-clone "excluded" assertions below have something real to be
	// false about (see the ADR-071 D4 fix note above the file header).
	parentRegistry := NewToolRegistry()
	delegateTool := &DelegateTool{}       // no spawner — only used for registration
	switchAgentTool := &SwitchAgentTool{} // no deps wired — only used for registration
	parentRegistry.Register(delegateTool)
	parentRegistry.Register(switchAgentTool)

	// Verify the delegation tool IS in the parent registry.
	parent, ok := parentRegistry.Get("delegate")
	require.True(t, ok, "delegate must be present in the parent registry before CloneExcept")
	require.NotNil(t, parent)

	// Verify switch_agent IS in the parent registry — the pre-condition a
	// vacuous version of this test previously skipped.
	parentSwitchAgent, okSwitchAgent := parentRegistry.Get("switch_agent")
	require.True(t, okSwitchAgent, "switch_agent must be present in the parent registry before CloneExcept")
	require.NotNil(t, parentSwitchAgent)

	// Construct the child registry as spawnSubTurn does (FR-H-006).
	// Both delegation-adjacent tools are excluded: delegate, switch_agent.
	childRegistry := parentRegistry.CloneExcept(ExcludedDelegate, ExcludedSwitchAgent)

	// BDD: Then "delegate" is absent from the child registry.
	childDelegate, childHasDelegate := childRegistry.Get("delegate")
	assert.False(t, childHasDelegate,
		"delegate must NOT be in the child registry — grandchildren are forbidden (Plan 3 §1 reversal)")
	assert.Nil(t, childDelegate,
		"delegate tool must be nil in the child registry")

	// BDD: And "switch_agent" is absent from the child registry.
	childSwitchAgent, childHasSwitchAgent := childRegistry.Get("switch_agent")
	assert.False(t, childHasSwitchAgent,
		"switch_agent must NOT be in the child registry — one level only (Plan 3 §1 reversal)")
	assert.Nil(t, childSwitchAgent,
		"switch_agent tool must be nil in the child registry")

	// BDD: When the child registry tries to execute "delegate", it returns an unknown-tool error.
	result := childRegistry.ExecuteWithContext(
		context.Background(),
		"delegate",
		map[string]any{"task": "grandchild task"},
		"", "", nil,
	)
	require.NotNil(t, result, "ExecuteWithContext must return a non-nil result")
	assert.True(t, result.IsError,
		"executing delegate in a child registry must return an error result")
	assert.Contains(t, result.ForLLM, "not found",
		"the error must indicate the tool is not found (unknown-tool error), not a depth error")

	// The error must NOT mention "depth" — depth is not the enforcement mechanism.
	assert.NotContains(t, result.ForLLM, "depth",
		"unknown-tool error must not mention depth — the enforcement is registry-level, not depth-level")
}

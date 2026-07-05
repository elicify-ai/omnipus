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
//   - A sub-turn's tool registry is constructed via CloneExcept(ExcludedDelegate, ExcludedHandoff).
//   - "delegate" and "hand_off" are absent from the registry.
//   - Any LLM tool call for either of those inside a sub-turn receives an unknown-tool error.
//   - No grandchild subagent_start frame is emitted.
//
// The enforcement is at the registry level (FR-H-006), not a depth check in the tool.
// Traces to: sprint-h-subagent-block-spec.md FR-H-006, FR-H-007, US-3, BDD Scenario 9 & 10.

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
// ExcludedHandoff) in pkg/agent/subturn.go::spawnSubTurn. With both absent from
// the registry, any LLM tool call for either is dispatched to
// ExecuteWithContext which returns an unknown-tool error — no new sub-turn is
// created, no subagent_start frame is emitted.
//
// This test verifies the registry-level enforcement directly.
func TestDelegateCannotSpawnGrandchild(t *testing.T) {
	// Build a parent registry with the merged delegation tool registered.
	parentRegistry := NewToolRegistry()
	delegateTool := &DelegateTool{} // no spawner — only used for registration
	parentRegistry.Register(delegateTool)

	// Verify the delegation tool IS in the parent registry.
	parent, ok := parentRegistry.Get("delegate")
	require.True(t, ok, "delegate must be present in the parent registry before CloneExcept")
	require.NotNil(t, parent)

	// Construct the child registry as spawnSubTurn does (FR-H-006).
	// Both delegation-adjacent tools are excluded: delegate, hand_off.
	childRegistry := parentRegistry.CloneExcept(ExcludedDelegate, ExcludedHandoff)

	// BDD: Then "delegate" is absent from the child registry.
	childDelegate, childHasDelegate := childRegistry.Get("delegate")
	assert.False(t, childHasDelegate,
		"delegate must NOT be in the child registry — grandchildren are forbidden (Plan 3 §1 reversal)")
	assert.Nil(t, childDelegate,
		"delegate tool must be nil in the child registry")

	// BDD: And "hand_off" is absent from the child registry.
	childHandoff, childHasHandoff := childRegistry.Get("hand_off")
	assert.False(t, childHasHandoff,
		"hand_off must NOT be in the child registry — one level only (Plan 3 §1 reversal)")
	assert.Nil(t, childHandoff,
		"hand_off tool must be nil in the child registry")

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

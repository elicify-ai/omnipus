// Sprint H registry tests — FR-H-006
// Traces to: sprint-h-subagent-block-spec.md TDD rows 2 & 3, BDD Scenario 9.
//
// FR-H-006 originally specified delegation-adjacent tools excluded from child
// sub-turn registries: delegate (the ADR-036 merge of
// spawn/run_subagent/check_spawn_status) and the agent-switch tool
// (hand_off, renamed switch_agent by ADR-071 D4), via:
//
//	CloneExcept(ExcludedDelegate, ExcludedSwitchAgent)
//
// This file tests that CloneExcept PRIMITIVE directly (unchanged — it still
// omits whatever names are passed to it). FR-H-006 REVERSAL (live UAT,
// 2026-07-12): the actual production call site, pkg/agent/subturn.go's
// spawnSubTurn, no longer passes ExcludedDelegate — only ExcludedSwitchAgent —
// so a delegated sub-turn can itself delegate onward, gated by the real
// trust-graph/mode/depth system instead of a blanket registry-level omission.
// See pkg/agent/subturn_delegate_nesting_test.go for the current production
// regression coverage.

package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestToolRegistry_CloneExcept_OmitsNamed verifies FR-H-006:
// CloneExcept(ExcludedDelegate, ExcludedSwitchAgent) produces a registry
// without those two delegation-adjacent tools but with all other tools intact.
// Traces to: sprint-h-subagent-block-spec.md TDD row 2, BDD Scenario 9.
func TestToolRegistry_CloneExcept_OmitsNamed(t *testing.T) {
	r := NewToolRegistry()

	// Register three tools: delegate, switch_agent, and a neutral one (read_file).
	delegateTool := &DelegateTool{}
	switchAgentTool := &SwitchAgentTool{}
	otherTool := &ReadFileTool{} // a non-excluded tool

	r.Register(delegateTool)
	r.Register(switchAgentTool)
	r.Register(otherTool)

	// Verify all three are in the parent before cloning.
	_, hasDelegate := r.Get("delegate")
	_, hasSwitchAgent := r.Get("switch_agent")
	_, hasReadFile := r.Get("read_file")
	require.True(t, hasDelegate, "delegate must be in the parent registry")
	require.True(t, hasSwitchAgent, "switch_agent must be in the parent registry")
	require.True(t, hasReadFile, "read_file must be in the parent registry")

	// Construct the child registry as spawnSubTurn does (2-arg canonical call).
	child := r.CloneExcept(ExcludedDelegate, ExcludedSwitchAgent)

	// FR-H-006: "delegate" must be absent.
	childDelegate, childHasDelegate := child.Get("delegate")
	assert.False(t, childHasDelegate, "delegate must not be in the child registry after CloneExcept")
	assert.Nil(t, childDelegate)

	// FR-H-006: "switch_agent" must be absent.
	childSwitchAgent, childHasSwitchAgent := child.Get("switch_agent")
	assert.False(t, childHasSwitchAgent, "switch_agent must not be in the child registry after CloneExcept")
	assert.Nil(t, childSwitchAgent)

	// Non-excluded tools must be present.
	childReadFile, childHasReadFile := child.Get("read_file")
	assert.True(t, childHasReadFile, "read_file must remain in the child registry (not excluded)")
	assert.NotNil(t, childReadFile)

	// Verify clone is independent: registering a new tool on child does not affect parent.
	child.Register(&MessageTool{})
	_, parentHasSendMessage := r.Get("send_message")
	assert.False(t, parentHasSendMessage,
		"registering on child must not pollute parent registry (independent copy)")
}

// TestToolRegistry_CloneExcept_EmptyNames verifies that CloneExcept() with no names
// behaves like Clone() — all tools are present.
func TestToolRegistry_CloneExcept_EmptyNames(t *testing.T) {
	r := NewToolRegistry()
	r.Register(&DelegateTool{})

	clone := r.CloneExcept() // no exclusions

	_, hasDelegate := clone.Get("delegate")
	assert.True(t, hasDelegate, "CloneExcept with no names must keep all tools (same as Clone)")
}

// TestSubTurn_ChildRegistry_OmitsDelegationTools verifies the registry used in
// sub-turns does not contain "delegate" or "switch_agent" — the enforcement
// point is CloneExcept in spawnSubTurn (FR-H-006).
// This is a structural test complementing the functional grandchild test.
// Cross-reference: TestSpawnSubTurn_ChildRegistry_OmitsDelegationTools in
// pkg/agent/sprint_h_subturn_test.go validates the production wiring end-to-end.
// Traces to: sprint-h-subagent-block-spec.md TDD row 3, BDD Scenario 9.
func TestSubTurn_ChildRegistry_OmitsDelegationTools(t *testing.T) {
	// Build a registry that contains both excluded tools plus extras.
	r := NewToolRegistry()
	r.Register(&DelegateTool{})
	r.Register(&SwitchAgentTool{})
	r.Register(&ReadFileTool{})

	child := r.CloneExcept(ExcludedDelegate, ExcludedSwitchAgent)

	childNames := child.List()

	hasDelegateInList := false
	hasSwitchAgentInList := false
	hasReadFileInList := false
	for _, name := range childNames {
		switch name {
		case "delegate":
			hasDelegateInList = true
		case "switch_agent":
			hasSwitchAgentInList = true
		case "read_file":
			hasReadFileInList = true
		}
	}

	assert.False(t, hasDelegateInList,
		"delegate must not appear in child.List() — grandchildren are forbidden")
	assert.False(t, hasSwitchAgentInList,
		"switch_agent must not appear in child.List()")
	assert.True(t, hasReadFileInList,
		"read_file must appear in child.List() — non-excluded tools are kept")

	assert.Equal(t, r.Count()-2, child.Count(),
		"child registry must have exactly 2 fewer tools than parent (delegate, switch_agent excluded)")
}

// TestToolRegistry_CloneExcept_UnknownToolNameWarns verifies W4-3 behavior:
// calling CloneExcept with a tool name not in the base registry emits slog.Warn
// and proceeds — the other named exclusions are still applied, and the unknown
// name is a no-op (not a panic).
// This documents the post-W4-3 existence-check guard.
func TestToolRegistry_CloneExcept_UnknownToolNameWarns(t *testing.T) {
	r := NewToolRegistry()
	r.Register(&DelegateTool{})
	r.Register(&ReadFileTool{})

	// "nonexistent_tool" is not in the registry. CloneExcept must warn (via slog.Warn)
	// and proceed. The known exclusion (ExcludedDelegate) must still be applied.
	// We verify behavior (no panic) and that delegate is excluded despite the invalid name.
	child := r.CloneExcept(ExcludedDelegate, "nonexistent_tool")

	// delegate must still be excluded.
	_, hasDelegate := child.Get("delegate")
	assert.False(t, hasDelegate, "delegate must still be excluded even when another name is invalid")

	// read_file must still be present.
	_, hasReadFile := child.Get("read_file")
	assert.True(t, hasReadFile, "read_file must remain in child registry")

	// No panic — test reaching here proves CloneExcept is non-fatal on unknown names.
}

// Sprint H registry tests — FR-H-006
// Traces to: sprint-h-subagent-block-spec.md TDD rows 2 & 3, BDD Scenario 9.
//
// FR-H-006 specifies three delegation tools excluded from child sub-turn registries:
// spawn, run_subagent, and hand_off. The canonical call is:
//   CloneExcept(ExcludedSpawn, ExcludedSubagent, ExcludedHandoff)

package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestToolRegistry_CloneExcept_OmitsNamed verifies FR-H-006:
// CloneExcept(ExcludedSpawn, ExcludedSubagent, ExcludedHandoff) produces a registry
// without those three delegation tools but with all other tools intact.
// Traces to: sprint-h-subagent-block-spec.md TDD row 2, BDD Scenario 9.
func TestToolRegistry_CloneExcept_OmitsNamed(t *testing.T) {
	r := NewToolRegistry()

	// Register four tools: spawn, run_subagent, hand_off, and a neutral one (read_file).
	spawnTool := &SpawnTool{}
	subagentTool := &SubagentTool{}
	handoffTool := &HandoffTool{}
	otherTool := &ReadFileTool{} // a non-excluded tool

	r.Register(spawnTool)
	r.Register(subagentTool)
	r.Register(handoffTool)
	r.Register(otherTool)

	// Verify all four are in the parent before cloning.
	_, hasSpawn := r.Get("spawn")
	_, hasSubagent := r.Get("run_subagent")
	_, hasHandoff := r.Get("hand_off")
	_, hasReadFile := r.Get("read_file")
	require.True(t, hasSpawn, "spawn must be in the parent registry")
	require.True(t, hasSubagent, "run_subagent must be in the parent registry")
	require.True(t, hasHandoff, "hand_off must be in the parent registry")
	require.True(t, hasReadFile, "read_file must be in the parent registry")

	// Construct the child registry as spawnSubTurn does (3-arg canonical call).
	child := r.CloneExcept(ExcludedSpawn, ExcludedSubagent, ExcludedHandoff)

	// FR-H-006: "spawn" must be absent.
	childSpawn, childHasSpawn := child.Get("spawn")
	assert.False(t, childHasSpawn, "spawn must not be in the child registry after CloneExcept")
	assert.Nil(t, childSpawn)

	// FR-H-006: "run_subagent" must be absent.
	childSubagent, childHasSubagent := child.Get("run_subagent")
	assert.False(t, childHasSubagent, "run_subagent must not be in the child registry after CloneExcept")
	assert.Nil(t, childSubagent)

	// FR-H-006: "hand_off" must be absent.
	childHandoff, childHasHandoff := child.Get("hand_off")
	assert.False(t, childHasHandoff, "hand_off must not be in the child registry after CloneExcept")
	assert.Nil(t, childHandoff)

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
	r.Register(&SpawnTool{})

	clone := r.CloneExcept() // no exclusions

	_, hasSpawn := clone.Get("spawn")
	assert.True(t, hasSpawn, "CloneExcept with no names must keep all tools (same as Clone)")
}

// TestSubTurn_ChildRegistry_OmitsThreeDelegationTools verifies the registry used in
// sub-turns does not contain "spawn", "subagent", or "handoff" — the enforcement point
// is CloneExcept in spawnSubTurn (FR-H-006).
// This is a structural test complementing the functional grandchild test.
// Cross-reference: TestSpawnSubTurn_ChildRegistry_OmitsThreeDelegationTools in
// pkg/agent/sprint_h_subturn_test.go validates the production wiring end-to-end.
// Traces to: sprint-h-subagent-block-spec.md TDD row 3, BDD Scenario 9.
func TestSubTurn_ChildRegistry_OmitsThreeDelegationTools(t *testing.T) {
	// Build a registry that contains all three excluded tools plus extras.
	r := NewToolRegistry()
	r.Register(&SpawnTool{})
	r.Register(&SubagentTool{})
	r.Register(&HandoffTool{})
	r.Register(&ReadFileTool{})

	child := r.CloneExcept(ExcludedSpawn, ExcludedSubagent, ExcludedHandoff)

	childNames := child.List()

	hasSpawnInList := false
	hasSubagentInList := false
	hasHandoffInList := false
	hasReadFileInList := false
	for _, name := range childNames {
		switch name {
		case "spawn":
			hasSpawnInList = true
		case "run_subagent":
			hasSubagentInList = true
		case "hand_off":
			hasHandoffInList = true
		case "read_file":
			hasReadFileInList = true
		}
	}

	assert.False(t, hasSpawnInList,
		"spawn must not appear in child.List() — grandchildren are forbidden")
	assert.False(t, hasSubagentInList,
		"run_subagent must not appear in child.List() — nested subagent-in-subagent is forbidden")
	assert.False(t, hasHandoffInList,
		"hand_off must not appear in child.List()")
	assert.True(t, hasReadFileInList,
		"read_file must appear in child.List() — non-excluded tools are kept")

	assert.Equal(t, r.Count()-3, child.Count(),
		"child registry must have exactly 3 fewer tools than parent (spawn, run_subagent, hand_off excluded)")
}

// TestToolRegistry_CloneExcept_UnknownToolNameWarns verifies W4-3 behavior:
// calling CloneExcept with a tool name not in the base registry emits slog.Warn
// and proceeds — the other named exclusions are still applied, and the unknown
// name is a no-op (not a panic).
// This documents the post-W4-3 existence-check guard.
func TestToolRegistry_CloneExcept_UnknownToolNameWarns(t *testing.T) {
	r := NewToolRegistry()
	r.Register(&SpawnTool{})
	r.Register(&ReadFileTool{})

	// "nonexistent_tool" is not in the registry. CloneExcept must warn (via slog.Warn)
	// and proceed. The known exclusion (ExcludedSpawn) must still be applied.
	// We verify behavior (no panic) and that spawn is excluded despite the invalid name.
	child := r.CloneExcept(ExcludedSpawn, "nonexistent_tool")

	// spawn must still be excluded.
	_, hasSpawn := child.Get("spawn")
	assert.False(t, hasSpawn, "spawn must still be excluded even when another name is invalid")

	// read_file must still be present.
	_, hasReadFile := child.Get("read_file")
	assert.True(t, hasReadFile, "read_file must remain in child registry")

	// No panic — test reaching here proves CloneExcept is non-fatal on unknown names.
}

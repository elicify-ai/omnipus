// loop_policy_test.go: tests for tool policy and approval at exec time

package agent

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- moved from loop.go tests 2026-09-15 ---

// ---------------------------------------------------------------------
// W10b — CheckGrantOrRequestApproval's grant read (FR-031/FR-080)
// ---------------------------------------------------------------------

// TestCheckGrantOrRequestApproval_ReadsTheActingAgentsAllowance replaces
// TestCheckGrantOrRequestApproval_UsesActingSessionKey.
//
// WHY THE EXPECTATIONS CHANGED. The old test guarded a session-keyed read (the
// cross-unit obligation FROM U17a: "IsAllowed under the child's own session
// key"). The child's own grant had to be created by an InheritFrom copy at
// spawn, and the discriminating negative control said that a read under the
// ROOT's session id (the value turnState.routingSessionID holds for this same
// child, FR-011) must miss, because routingSessionID must never be an
// approval-grant key (FR-014). C-26 (Skills & Tools redesign, founder R2-Q1,
// 2026-11-22) replaces that key. An allowance is keyed by (agent id, kind,
// subject) and "The session id is no longer part of the key" (row Key); it
// covers "every session of that agent" (row Covers). The code-path rows say
// ApprovalGrantStore.InheritFrom "copies no grants (self-helpers share the key;
// other agents get nothing)" and loop_policy.go::checkStandingGrant runs "the
// same checks keyed by agent". Under that contract a read under any session of
// the acting agent resolves, so the old root-session control is wrong and a
// correct GREEN fails it.
//
// DISPOSITION (old expectation -> new)
//
//	KEPT: the real CheckGrantOrRequestApproval call; the child's own session
//	resolves the agent's allowance with an empty reason; a miss falls through to
//	the fail-closed nopPolicyApprover (approved==true is only reachable through
//	a real grant-store hit, see below); the fixture's distinct ids.
//	FLIPPED (row Key): the read under the root's session id now RESOLVES.
//	RETIRED: the spawn-time InheritFrom call and its "SETUP: InheritFrom must
//	have copied the grant into the child's own key" precondition; they describe
//	a copy C-26 deletes. The same agent holds the allowance in the child's
//	session with no copy, and that is now asserted first, on the store itself.
//	The spawn path is driven by TestTimedAllowance_HelpersAndOtherAgents.
//	ADDED (row Covers; agent isolation): ANOTHER agent reading the same child
//	session must miss and fall through to the nop approver. It takes over the
//	discriminating power of the retired root-session control with the isolation
//	C-26 keeps.
//
// No PolicyApprover is wired on this AgentLoop, so a query that misses the
// grant-store short-circuit reaches loadToolApprover's nopPolicyApprover
// fallback, which ALWAYS denies with reason "no_approver_configured"
// (tool_approver.go's V2.B fail-closed default, never the pre-V2.B
// auto-approve). approved==true is therefore only reachable via a real
// grant-store hit — a genuine production fallback proves the read, not a spy
// recording its argument.
func TestCheckGrantOrRequestApproval_ReadsTheActingAgentsAllowance(t *testing.T) {
	al, cleanup := newAL(t)
	defer cleanup()

	const (
		rootSessionID   = "u9-root-session-grant-uninvolved"
		parentSessionID = "u9-parent-session-grant"
		childSessionID  = "u9-child-session-grant-distinct"
		agentID         = "u9-grant-agent"
		otherAgentID    = "u9-other-grant-agent"
		toolName        = "u9_grant_tool"
	)
	require.NotEqual(t, parentSessionID, childSessionID, "fixture defect: parent and child ids must be distinct")
	require.NotEqual(t, rootSessionID, parentSessionID, "fixture defect: root and parent ids must be distinct")
	require.NotEqual(t, rootSessionID, childSessionID, "fixture defect: root and child ids must be distinct")
	require.NotEqual(t, agentID, otherAgentID, "fixture defect: the two agent ids must be distinct")

	grants := security.NewApprovalGrantStore()
	// The agent holds an allowance, recorded while it ran in the PARENT's chat.
	// The child's session and the root never recorded anything, and nothing is
	// copied to them below.
	require.True(t, grants.Record(parentSessionID, agentID, toolName, nil))
	al.approvalGrants = grants

	// Instrument, flipped (rows Key and Covers): the store itself holds the
	// agent's allowance in the child's session with no copy at spawn, before the
	// code under test runs. Today's session-keyed store misses here; that miss is
	// the root cause the two loop-level checks below report as well, so this one
	// asserts instead of aborting.
	assert.True(t, grants.IsAllowed(childSessionID, agentID, toolName, nil),
		"C-26 Covers: the agent's allowance must be held in the child's own session with no copy at spawn")

	approved, reason, _ := al.CheckGrantOrRequestApproval(
		context.Background(), childSessionID, agentID, toolName, "u9-tc-1", "u9-turn-1", nil)
	assert.True(t, approved, "a read in the child's own session must resolve the acting agent's allowance")
	assert.Empty(t, reason)

	// FLIPPED control (row Key): the old test read here under the ROOT's session
	// id and demanded a miss. The session id is no longer part of the key, so the
	// same agent's read under that id resolves too.
	approvedRoot, reasonRoot, _ := al.CheckGrantOrRequestApproval(
		context.Background(), rootSessionID, agentID, toolName, "u9-tc-2", "u9-turn-2", nil)
	assert.True(t, approvedRoot,
		"C-26 Key: the session id is no longer part of the key, so the same agent's read under the root's session id must resolve")
	assert.Empty(t, reasonRoot)

	// ADDED control (row Covers; agent isolation): another agent reading the very
	// same child session must NOT find this agent's allowance.
	approvedOther, reasonOther, _ := al.CheckGrantOrRequestApproval(
		context.Background(), childSessionID, otherAgentID, toolName, "u9-tc-3", "u9-turn-3", nil)
	assert.False(t, approvedOther, "another agent reading the same session must NOT find this agent's allowance")
	assert.Equal(t, nopApproverDenialReason, reasonOther,
		"a miss must fall through to the fail-closed nopPolicyApprover, never silently approve")
}

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

// TestCheckGrantOrRequestApproval_UsesActingSessionKey is this unit's
// regression guard for the cross-unit obligation FROM U17a (Integration
// order, cross-unit requests table): "the two-key grant read — IsAllowed
// under the child's own session key". U17a's InheritFrom (Wave A,
// pkg/security/approvalgrants.go) copies a spawn-time grant from a SOURCE
// {sessionID, agentID} into a DESTINATION {sessionID, agentID} — never into
// a shared/routing key. This test proves loop.go's READ side
// (CheckGrantOrRequestApproval, which every ask-policy tool call in runTurn
// goes through — see its own doc comment's "Identity" paragraph, corrected
// by this unit) resolves an inherited grant when queried under the CHILD's
// own (acting) session id, and — the discriminating negative control — does
// NOT resolve it under the ROOT's session id, the value
// turnState.routingSessionID would hold for this same child (FR-011:
// "inherited verbatim from the root of its delegation subtree"). This is
// the exact mistake FR-014's closed-consumer-set rule exists to prevent:
// routingSessionID must never be read as an approval-grant key.
//
// The three ids are deliberately NOT a simple two-hop parent/child pair: the
// ROOT never records or receives any grant of its own (unlike the immediate
// PARENT, whose own direct "Always Allow" is legitimately valid for the
// parent's own future calls and would make a parent-keyed read succeed for
// an unrelated, correct reason — that would NOT discriminate anything). The
// ROOT is the only id in this fixture with zero grants under it by
// construction, so approved==true there could only mean the read leaked
// across keys.
//
// No PolicyApprover is wired on this AgentLoop, so a query that misses the
// grant-store short-circuit reaches loadToolApprover's nopPolicyApprover
// fallback, which ALWAYS denies with reason "no_approver_configured"
// (tool_approver.go's V2.B fail-closed default, never the pre-V2.B
// auto-approve). approved==true is therefore only reachable via a real
// grant-store hit — a genuine production fallback proves the read, not a
// spy recording its argument.
func TestCheckGrantOrRequestApproval_UsesActingSessionKey(t *testing.T) {
	al, cleanup := newAL(t)
	defer cleanup()

	const (
		rootSessionID   = "u9-root-session-grant-uninvolved"
		parentSessionID = "u9-parent-session-grant"
		childSessionID  = "u9-child-session-grant-distinct"
		agentID         = "u9-grant-agent"
		toolName        = "u9_grant_tool"
	)
	require.NotEqual(t, parentSessionID, childSessionID, "fixture defect: parent and child ids must be distinct")
	require.NotEqual(t, rootSessionID, parentSessionID, "fixture defect: root and parent ids must be distinct")
	require.NotEqual(t, rootSessionID, childSessionID, "fixture defect: root and child ids must be distinct")

	grants := security.NewApprovalGrantStore()
	// The immediate PARENT recorded "Always Allow" under its OWN session
	// id — the root itself never did...
	require.True(t, grants.Record(parentSessionID, agentID, toolName, nil))
	// ...and InheritFrom (U17a) copies it into the CHILD's OWN session id at
	// spawn — mirroring what U7's spawnSubTurn call site (Wave F) will do.
	// routingSessionID for this same child would be the ROOT's id (FR-011),
	// never the parent's — InheritFrom's source here is the immediate
	// parent, which is a DIFFERENT value from routing the moment delegation
	// is two levels deep, exactly the case this test's negative control
	// exercises against the root.
	grants.InheritFrom(parentSessionID, agentID, childSessionID, agentID)
	al.approvalGrants = grants

	// Positive lower bound (Rule 4): the grant genuinely exists under the
	// child's key, via the store's own read, before the code under test runs.
	require.True(t, grants.IsAllowed(childSessionID, agentID, toolName, nil),
		"SETUP: InheritFrom must have copied the grant into the child's own key")

	approved, reason, _ := al.CheckGrantOrRequestApproval(
		context.Background(), childSessionID, agentID, toolName, "u9-tc-1", "u9-turn-1", nil)
	assert.True(t, approved, "grant read keyed on the child's own (acting) session id must resolve the inherited grant")
	assert.Empty(t, reason)

	// Discriminating negative control: reading under the ROOT's session id —
	// the value a buggy caller would pass if it read routingSessionID
	// instead of transcriptSessionID for this child — must NOT resolve. The
	// root was never granted anything, directly or via inheritance, so any
	// approved==true here could only mean the read crossed keys.
	approvedWrongKey, reasonWrongKey, _ := al.CheckGrantOrRequestApproval(
		context.Background(), rootSessionID, agentID, toolName, "u9-tc-2", "u9-turn-2", nil)
	assert.False(t, approvedWrongKey, "reading under the root's (routing-shaped) session id must NOT find the child's inherited grant")
	assert.Equal(t, nopApproverDenialReason, reasonWrongKey,
		"a miss must fall through to the fail-closed nopPolicyApprover, never silently approve")
}

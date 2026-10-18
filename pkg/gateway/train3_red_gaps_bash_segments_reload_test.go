package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/require"
)

// Gate round 1 CHECK F1 (MEDIUM oracle gap, brief train3-red-gaps-r1): every
// Train 3 approval fixture was a non-bash tool, so the live-vs-snapshot
// segments equality could never fail — a regression that dropped or corrupted
// bash segments on the reload snapshot (ws_tool_approval.go::
// pendingApprovalSnapshot) passed the whole pack (check.md r1, mutant 13a,
// "Segments: nil", exit=0). These tests drive one REAL bash approval through
// the reload path: the segments builder (ws_tool_approval_segments.go::
// bashApprovalSegments) is real and unmocked; only the provider is scripted.
//
// Expected values derive from ADR-092 D3/D4, not from observed output: a bash
// approval carries one card entry per chained command segment, whose
// command_text is that segment's raw text, and a chained command NEVER offers
// a prefix grant (the grant would have to cover the whole chain). Bundle B3 +
// amendment §1.5: the reload snapshot is complete and equal to the live card.

// train3GapsBashSegments asserts the live card's segment shape for
// "echo hello && echo world" and returns the frame for the reload comparison.
func train3GapsBashSegments(t *testing.T, frame gen.ToolApprovalRequiredFrame) {
	t.Helper()
	require.Equal(t, "bash", frame.ToolName, "fixture: the card under test is a bash approval")
	require.Len(t, frame.Segments, 2, "D4: one card entry per &&-chained segment")
	require.Equal(t, "echo hello", frame.Segments[0].CommandText)
	require.Equal(t, "echo world", frame.Segments[1].CommandText)
	for i, seg := range frame.Segments {
		require.NotEmpty(t, seg.CommandText, "segment %d carries its command text", i)
		require.NotNil(t, seg.PrefixAvailable, "D4: prefix_available is always present")
		require.False(t, *seg.PrefixAvailable,
			"D4: a chained command never offers a prefix grant on any segment")
	}
}

// train3GapsAssertSnapshotSegments reloads the reconnect snapshot on a fresh
// connection and requires the pending card's segments to be present and equal
// to the live frame's.
func train3GapsAssertSnapshotSegments(t *testing.T, snap gen.SessionStatePendingApproval, frame gen.ToolApprovalRequiredFrame) {
	t.Helper()
	require.NotEmpty(t, snap.Segments,
		"F1: the reload snapshot carries the bash segments — the Segments:nil mutant dies here")
	require.Equal(t, frame.Segments, snap.Segments,
		"§1.5/B3: snapshot segments equal the live card's, entry for entry")
}

// TestTrain3Fix1_HelperBashApprovalReloadCarriesSegments drives the helper
// case: a delegated helper's bash ask, its live card, then the reload
// snapshot — the exact path mutant 13a survived.
func TestTrain3Fix1_HelperBashApprovalReloadCarriesSegments(t *testing.T) {
	h := newL11263Harness(t, false, config.ToolPolicyAsk, nil)
	parent := l11263Script(l11263Delegate(l11263Leaf))
	leaf := l11263Script(l11263Call("bash-segments", "bash", map[string]any{"command": "echo hello && echo world"}))
	inst, ok := h.api.agentLoop.GetRegistry().GetAgent(l11263Agent)
	require.True(t, ok)
	inst.Provider = parent
	leafInst, ok := h.api.agentLoop.GetRegistry().GetAgent(l11263Leaf)
	require.True(t, ok)
	leafInst.Provider = leaf
	done := make(chan error, 1)
	go func() {
		_, err := h.api.agentLoop.ProcessDirectAs(context.Background(), l11263Agent, "Delegate the shell probe", "train3-bash-segments")
		done <- err
	}()
	t.Cleanup(func() {
		for _, e := range h.reg.pendingApprovals() {
			h.reg.resolve(e.ApprovalID, ApprovalActionCancel, false)
		}
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Error("helper parent cleanup did not join")
		}
	})
	entry := h.awaitInteractiveApproval(t)
	frame := wu8TypedFrame[gen.ToolApprovalRequiredFrame](t, apprResReadFrame(t, h.tab, "tool_approval_required"))
	train3GapsBashSegments(t, frame)

	snap, _ := wu8SnapshotPending(t, h, entry.ApprovalID)
	train3GapsAssertSnapshotSegments(t, snap, frame)

	// The card stays approvable: the real approval registry (what the REST
	// endpoint fronts) answers it.
	resolved, gone := h.reg.resolve(entry.ApprovalID, ApprovalActionApprove, false)
	require.True(t, resolved && !gone, "the reload-restored bash card is approvable")
}

// TestWU15Rooms_HelperBashApprovalReloadCarriesSegments adds the rooms
// receiver-helper case: jim's helper bash ask inside a room-requested
// delegation, reloaded from the root chat's reconnect snapshot.
func TestWU15Rooms_HelperBashApprovalReloadCarriesSegments(t *testing.T) {
	h, room, _ := l1WU8RoomsSetup(t)
	// jim's bash ask through the live per-agent store (tighten-only; ask is
	// bash's shipped default) so the helper call prompts instead of riding an
	// unspecified ceiling entry.
	inst, ok := h.al.GetRegistry().GetAgent("jim")
	require.True(t, ok)
	policy := *inst.LoadToolPolicy()
	policy.Policies["bash"] = config.ToolPolicyAsk
	inst.StoreToolPolicy(&policy)

	mia := &wu15ScriptProvider{answer: "delegated."}
	mia.tool = "delegate"
	mia.toolArgs = map[string]any{"agent_id": "jim", "task": "run the shell probe", "critical": true}
	h.provider("mia", mia)
	jim := &wu15ScriptProvider{tool: "bash", toolArgs: map[string]any{"command": "echo hello && echo world"}}
	h.provider("jim", jim)

	conn := h.connect()
	h.attach(conn, room.Id, 0)
	h.startLoop()
	h.post(room.Id, "bash-delegate", "@Mia delegate the shell probe")

	frame := l1WU8RoomsFrame[gen.ToolApprovalRequiredFrame](t, wu15ReadFrame(t, conn, "tool_approval_required"))
	train3GapsBashSegments(t, frame)

	// Reload: a fresh connection attaches the ROOT chat (the receiver main)
	// and reads the reconnect snapshot, as the production SPA does.
	reloaded := h.connect()
	require.NoError(t, reloaded.WriteJSON(gen.AttachSessionFrame{Type: "attach_session", SessionId: h.main("mia")}))
	var state gen.SessionStateFrame
	require.NoError(t, json.Unmarshal(wu15ReadFrame(t, reloaded, "session_state"), &state))
	var snap gen.SessionStatePendingApproval
	found := false
	for _, pending := range state.PendingApprovals {
		if pending.ApprovalId == frame.ApprovalId {
			snap = pending
			found = true
		}
	}
	require.True(t, found, "B3: the reload snapshot still lists the helper's bash approval")
	train3GapsAssertSnapshotSegments(t, snap, frame)

	status, body := h.http(http.MethodPost, "/api/v1/tool-approvals/"+frame.ApprovalId,
		gen.ToolApprovalActionRequest{Action: gen.ToolApprovalActionRequestActionAllowOnce})
	require.Equal(t, http.StatusOK, status, "%s", body)
}

package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/require"
)

// WU-8 × rooms: replaces the three t.Fatal BLOCKED placeholders of
// wu15_rooms_needs_wu8_test.go — that file is deleted by this commit, fully
// superseded (same three oracles, now that WU-8's server half and this
// pack's stimulus exist) — and adds the room member-turn label.
// Oracles: amendment §1.2 R2/R3/R6, §1.3 bundle B3, §1.5 (live and snapshot
// fields equal), FR-169/FR-251, spec K7/§6.18 ("for group <name>"). Real
// boundary: the wu15 gateway boot, room runtime, approval registry and REST
// approval endpoint; only the paid provider is scripted.
//
// Expected state at c3b1dd1ee: the chief's dispatch says the room member-turn
// label is NOT written yet (the developer waits for these tests), so the
// label and the helper-card room_ref are expected RED; the receiver-main
// rooting cases may already hold — the pack reports each honestly.

// l1WU8RoomsFrame decodes one raw wire frame into its generated type.
func l1WU8RoomsFrame[T any](t *testing.T, raw json.RawMessage) T {
	t.Helper()
	var out T
	require.NoError(t, json.Unmarshal(raw, &out))
	return out
}

// l1WU8RoomsSetup boots the rooms harness with delegation and the ask probe
// wired: mia may delegate to jim, jim's helper turns hit the ask probe.
func l1WU8RoomsSetup(t *testing.T) (*wu15RoomHarness, gen.Room, *askProbeTool) {
	t.Helper()
	h := wu15NewHarness(t)
	// The wu15 boot wires no delegation mesh: mia→jim must exist for the
	// receiver main's real delegate call (ADR-037 workspace graph).
	require.NoError(t, workspace.SaveDelegation(h.home, wu15Workspace, []workspace.DelegationEdge{
		{FromAgent: "mia", ToAgent: "jim"},
	}))
	// Per-agent policies through the live store (the compositor reads the
	// boot-time global snapshot, so post-boot cfg mutation never reaches
	// resolution): mia may delegate and report, and both agents' probe calls
	// are Ask — the approval stimulus. Nothing else is loosened.
	probe := &askProbeTool{}
	h.al.RegisterTool(probe)
	for _, agentPolicies := range []struct {
		id       string
		policies map[string]config.ToolPolicy
	}{
		{"mia", map[string]config.ToolPolicy{"delegate": config.ToolPolicyAllow, "message_parent": config.ToolPolicyAllow, askProbeToolName: config.ToolPolicyAsk}},
		{"jim", map[string]config.ToolPolicy{"message_parent": config.ToolPolicyAllow, askProbeToolName: config.ToolPolicyAsk}},
	} {
		inst, ok := h.al.GetRegistry().GetAgent(agentPolicies.id)
		require.True(t, ok)
		policy := *inst.LoadToolPolicy()
		policy.Policies = agentPolicies.policies
		inst.StoreToolPolicy(&policy)
	}
	room := h.create("squad", "Launch", "mia", "jim")
	return h, room, probe
}

// l1WU8RoomsDelegateHelper makes the receiver main delegate the probe to jim
// and returns the pending helper approval's live frame.
func l1WU8RoomsDelegateHelper(t *testing.T, h *wu15RoomHarness, room gen.Room) gen.ToolApprovalRequiredFrame {
	t.Helper()
	mia := &wu15ScriptProvider{answer: "delegated."}
	miaTool := "delegate"
	mia.tool = miaTool
	mia.toolArgs = map[string]any{"agent_id": "jim", "task": "write the report", "critical": true}
	h.provider("mia", mia)
	jim := &wu15ScriptProvider{tool: askProbeToolName, toolArgs: map[string]any{}}
	h.provider("jim", jim)
	conn := h.connect()
	h.attach(conn, room.Id, 0)
	h.startLoop()
	h.post(room.Id, "delegate-1", "@Mia delegate the report")
	return l1WU8RoomsFrame[gen.ToolApprovalRequiredFrame](t, wu15ReadFrame(t, conn, "tool_approval_required"))
}

func TestWU15Rooms_InteractiveReceiverHelperApprovalRootsInReceiverMain(t *testing.T) {
	h, room, _ := l1WU8RoomsSetup(t)
	frame := l1WU8RoomsDelegateHelper(t, h, room)

	require.NotEqual(t, room.Id, frame.SessionId, "the card lives on the acting helper session, never the room")
	require.NotEqual(t, h.main("mia"), frame.SessionId)
	require.Equal(t, "jim", frame.AgentId, "the helper is the acting agent")
	require.NotNil(t, frame.RootSessionId, "R2/R3: the helper's card names the chain root")
	require.Equal(t, h.main("mia"), *frame.RootSessionId,
		"a helper of the room receiver's interactive main roots in that main, never the room")
	meta, err := h.al.GetSessionStore().GetMeta(frame.SessionId)
	require.NoError(t, err)
	require.Equal(t, h.main("mia"), meta.ParentSessionID, "fixture: the helper's chain reaches the receiver main")

	// Positive control: the real approval endpoint answers the helper's card.
	status, body := h.http(http.MethodPost, "/api/v1/tool-approvals/"+frame.ApprovalId,
		gen.ToolApprovalActionRequest{Action: gen.ToolApprovalActionRequestActionAllowOnce})
	require.Equal(t, http.StatusOK, status, "the receiver main's person approves the helper: %s", body)
}

func TestWU15Rooms_TaskSenderDoesNotMakeReceiverMainUnattended(t *testing.T) {
	h, room, probe := l1WU8RoomsSetup(t)
	conn := h.connect()
	h.attach(conn, room.Id, 0)
	h.startLoop()

	// A real task-rooted sender: jim runs under a task (task-origin session)
	// and answers a room request from that session through the real reply
	// router — the exact production path an agent's answer_request drives.
	h.post(room.Id, "ask-jim", "@Jim check the launch")
	var R1 gen.RoomEntry
	require.Eventually(t, func() bool {
		snap := h.snapshot(room.Id)
		for _, entry := range snap.Entries {
			if entry.ClientMessageId != nil && *entry.ClientMessageId == "ask-jim" {
				R1 = entry
				return true
			}
		}
		return false
	}, 30*time.Second, 10*time.Millisecond)
	deliveries := wu15Delivery(t, R1, "jim")

	taskBody := fmt.Sprintf(`{"title":"task sender","action":"llm","agent_id":"jim","workspace_id":%q,"criteria":[{"text":"reported","author":{"kind":"user","id":"wu15-tester"},"status":"pending"}],"dod":[{"text":"the work meets the team quality bar","author":{"kind":"user","id":"wu15-tester"},"status":"pending"}]}`, wu15Workspace)
	status, data := h.http(http.MethodPost, "/api/v1/tasks", json.RawMessage(taskBody))
	require.Equal(t, http.StatusCreated, status, "real task creation: %s", data)
	var created struct {
		Id string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(data, &created))
	status, data = h.http(http.MethodPatch, "/api/v1/tasks/"+created.Id, map[string]any{"status": "in_progress"})
	require.Equal(t, http.StatusOK, status, "real task start: %s", data)

	// The real dispatch binds a task-origin session — the sender chain under
	// test — as a MAIN-mode child of jim's main.
	var taskSession string
	require.Eventually(t, func() bool {
		current, err := h.api.taskStore.Get(created.Id)
		return err == nil && current.SessionID != ""
	}, 30*time.Second, 10*time.Millisecond, "the real task dispatch binds a task-origin session")
	current, err := h.api.taskStore.Get(created.Id)
	require.NoError(t, err)
	taskSession = current.SessionID
	meta, metaErr := h.al.GetSessionStore().GetMeta(taskSession)
	require.NoError(t, metaErr)
	require.Equal(t, h.main("jim"), meta.ParentSessionID, "fixture: MAIN-mode task child of jim's main")
	rec, loadErr := h.al.GetSessionLifecycleStore().Load(taskSession)
	require.NoError(t, loadErr)
	require.NotNil(t, rec.Origin)
	require.Equal(t, session.OriginKindTask, rec.Origin.Kind, "fixture: the sender session is task-rooted")

	// Room scoping fact (why a "task sender" cannot post into a room at
	// all): the reply router refuses a session that is not the conversation
	// the request waits in — a task-origin session is never the room
	// request's responder, so no sender chain can cross into the room.
	_, replyErr := h.al.NewAddressRouter().Reply(h.ctx, tools.ReplyRequest{
		ReplyTo: deliveries.RequestId, Content: "checked from the task run",
		ResponderSessionID: taskSession, Author: tools.SendOrigin{WorkspaceID: wu15Workspace, AgentID: "jim"},
	})
	require.Error(t, replyErr, "the reply router must refuse a task-origin session as a room responder")

	// R2: with that real task run live in the room's workspace, the receiver
	// main's own ask stays interactive — rooted at its own main, answered by
	// its person, never flipped unattended by the task run's existence.
	mia := &wu15ScriptProvider{tool: askProbeToolName, toolArgs: map[string]any{}}
	h.provider("mia", mia)
	h.post(room.Id, "ask-mia", "@Mia write the report")
	frame := l1WU8RoomsFrame[gen.ToolApprovalRequiredFrame](t, wu15ReadFrame(t, conn, "tool_approval_required"))
	require.Equal(t, h.main("mia"), frame.SessionId, "R2: the receiver main owns the approval")
	require.Nil(t, frame.RootSessionId,
		"K7: the receiver main is its own chain root, so root_session_id is omitted when equal to session_id")
	require.Equal(t, 0, probe.runs(), "the interactive approval has not executed before its person answers")
	status, body := h.http(http.MethodPost, "/api/v1/tool-approvals/"+frame.ApprovalId,
		gen.ToolApprovalActionRequest{Action: gen.ToolApprovalActionRequestActionAllowOnce})
	require.Equal(t, http.StatusOK, status, "the interactive approval is answerable: %s", body)
	require.Eventually(t, func() bool { return probe.runs() == 1 }, 30*time.Second, 10*time.Millisecond,
		"R2: the receiver main stayed interactive — the approved ask really runs (an auto-denied call never would)")
}

func TestWU15Rooms_ReceiverHelperApprovalReloadRestoresCompleteApprovableCard(t *testing.T) {
	h, room, _ := l1WU8RoomsSetup(t)
	frame := l1WU8RoomsDelegateHelper(t, h, room)

	// Reload: a fresh connection attaches the ROOT chat (the receiver main)
	// and reads the reconnect snapshot.
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
	require.True(t, found, "B3/§1.5: the reload snapshot still lists the helper's approval")
	require.NotNil(t, snap.RootSessionId)
	require.Equal(t, h.main("mia"), *snap.RootSessionId)
	require.NotNil(t, snap.ActingAgentName)
	require.Equal(t, "Jim", *snap.ActingAgentName)
	// B3 (required on this branch): the snapshot fields are plain strings.
	require.NotEmpty(t, snap.ToolCallId, "B3: the snapshot carries the tool call id")
	require.Equal(t, frame.ToolCallId, snap.ToolCallId)
	require.NotEmpty(t, snap.TurnId, "B3: the snapshot carries the turn id")
	require.Equal(t, frame.TurnId, snap.TurnId)
	require.NotNil(t, snap.Args, "B3: args present, never null")
	require.Equal(t, frame.Args, snap.Args)
	require.Equal(t, frame.Segments, snap.Segments)
	require.Equal(t, frame.OriginLabel, snap.OriginLabel)
	// Chief ruling 2026-10-18 (option C): room_ref belongs to the room
	// pre-admission card and to the room-received member's OWN turn — never
	// to a helper's card — so no room_ref assertion is made here.

	// Complete AND approvable: approving through the real endpoint runs the
	// helper's probe.
	status, body := h.http(http.MethodPost, "/api/v1/tool-approvals/"+frame.ApprovalId,
		gen.ToolApprovalActionRequest{Action: gen.ToolApprovalActionRequestActionAllowOnce})
	require.Equal(t, http.StatusOK, status, "%s", body)

	// Resolution is learned from the resolved frame, never from "the next
	// session_state on this connection": the server pushes no session_state
	// on resolution (mechanism probe 2026-10-18), so that next frame is the
	// STALE attach-time frame — built before the approval — whenever the
	// attach answer beat the approval, and absent otherwise. Intermittent
	// full-suite failure go-packs2.log 2026-10-18. K7: the resolved frame
	// still names the chain root so the root chat's card clears without a
	// lookup.
	resolved := l1WU8RoomsFrame[gen.ToolApprovalResolvedFrame](t, wu15ReadFrame(t, reloaded, "tool_approval_resolved"))
	require.Equal(t, frame.ApprovalId, resolved.ApprovalId)
	require.Equal(t, "approved", resolved.State)
	require.NotNil(t, resolved.RootSessionId, "K7: the resolved frame names the chain root")
	require.Equal(t, h.main("mia"), *resolved.RootSessionId)

	// A FRESH attach's session_state is built after the resolution, so it
	// cannot be stale: the resolved approval must have left the snapshot.
	after := h.connect()
	require.NoError(t, after.WriteJSON(gen.AttachSessionFrame{Type: "attach_session", SessionId: h.main("mia")}))
	var post gen.SessionStateFrame
	require.NoError(t, json.Unmarshal(wu15ReadFrame(t, after, "session_state"), &post))
	for _, pending := range post.PendingApprovals {
		require.NotEqual(t, frame.ApprovalId, pending.ApprovalId,
			"a fresh attach's snapshot no longer lists the resolved approval")
	}
}

func TestWU15Rooms_MemberTurnApprovalLabelledForGroup(t *testing.T) {
	h, room, _ := l1WU8RoomsSetup(t)
	mia := &wu15ScriptProvider{tool: askProbeToolName, toolArgs: map[string]any{}}
	h.provider("mia", mia)
	conn := h.connect()
	h.attach(conn, room.Id, 0)
	h.startLoop()
	h.post(room.Id, "turn-ask", "@Mia write the report")

	frame := l1WU8RoomsFrame[gen.ToolApprovalRequiredFrame](t, wu15ReadFrame(t, conn, "tool_approval_required"))
	require.Equal(t, h.main("mia"), frame.SessionId, "the member turn's card lives in the receiver main")
	require.NotNil(t, frame.OriginLabel, "K7/§6.18: a room member-turn ask is labelled for its group")
	require.Equal(t, "for group "+room.Name, *frame.OriginLabel)
	require.NotNil(t, frame.RoomRef, "K7/FR-251: the room ref names the originating room and request")
	require.Equal(t, room.Id, frame.RoomRef.RoomId)

	// Reload: the same label on the complete snapshot card.
	reloaded := h.connect()
	require.NoError(t, reloaded.WriteJSON(gen.AttachSessionFrame{Type: "attach_session", SessionId: h.main("mia")}))
	var state gen.SessionStateFrame
	require.NoError(t, json.Unmarshal(wu15ReadFrame(t, reloaded, "session_state"), &state))
	for _, pending := range state.PendingApprovals {
		if pending.ApprovalId != frame.ApprovalId {
			continue
		}
		require.NotNil(t, pending.OriginLabel, "the label survives the reload")
		require.Equal(t, "for group "+room.Name, *pending.OriginLabel)
		require.NotNil(t, pending.RoomRef)
		require.Equal(t, room.Id, pending.RoomRef.RoomId)
		require.True(t, pending.RootSessionId == nil || *pending.RootSessionId == h.main("mia"),
			"K7: the snapshot's root is the receiver main itself (omitted when equal)")
	}
}

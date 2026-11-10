package gateway

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/require"
)

// WU-8 server RED pack (#1263 part A + bundle B3). Oracles: amendment
// §1.2 R6, §1.3, §1.5 and spec K7/§6.18 — the card names the ROOT chat of
// the session chain (root_session_id, omitted when equal to session_id),
// names the acting agent, and the RELOAD SNAPSHOT is complete and equal to
// the live card (tool_call_id, turn_id, args never null, segments,
// root_session_id, acting_agent_name, origin_label) so the SPA never builds
// Deny-only stubs. The peer ask is labelled "asked by <Agent>".
//
// Expected RED at both the release base and 2811dab68: at 2811dab68 the live
// frame carries args/turn/tool-call/segments, but neither carrier names the
// root chat or the acting agent, the snapshot carries none of bundle B3,
// OriginLabel is never populated and the resolved frame carries no root.

// wu8TypedFrame decodes one raw wire frame into its generated type.
func wu8TypedFrame[T any](t *testing.T, raw map[string]any) T {
	t.Helper()
	blob, err := json.Marshal(raw)
	require.NoError(t, err)
	var out T
	require.NoError(t, json.Unmarshal(blob, &out))
	return out
}

// wu8RootOf walks the persisted chain to its root — the same walk rule the
// server derivation must implement (amendment §1.2 R1) — so the expected
// root comes from the chain itself, never from the wire.
func wu8RootOf(t *testing.T, h *l11263Harness, sessionID string) string {
	t.Helper()
	current := sessionID
	for hop := 0; hop < 16; hop++ {
		meta, err := h.api.agentLoop.GetSessionStore().GetMeta(current)
		require.NoError(t, err)
		if meta.ParentSessionID == "" {
			return current
		}
		current = meta.ParentSessionID
	}
	t.Fatal("chain walk exceeded the 16-hop bound")
	return ""
}

// wu8SnapshotPending reloads the snapshot on a fresh connection and returns
// the typed pending entry for approvalID.
func wu8SnapshotPending(t *testing.T, h *l11263Harness, approvalID string) (gen.SessionStatePendingApproval, map[string]any) {
	t.Helper()
	wc := apprResAttachConns(t, h.ws, 1)[0]
	h.ws.emitSessionState(wc, "")
	snapshot := apprResReadFrame(t, wc, "session_state")
	pending, ok := snapshot["pending_approvals"].([]any)
	require.True(t, ok, "pending_approvals must be an array, never null")
	for _, raw := range pending {
		item, ok := raw.(map[string]any)
		require.True(t, ok)
		if item["approval_id"] == approvalID {
			return wu8TypedFrame[gen.SessionStatePendingApproval](t, item), item
		}
	}
	t.Fatalf("approval %s missing from the reconnect snapshot", approvalID)
	return gen.SessionStatePendingApproval{}, nil
}

// wu8AwaitHelperApproval drives a real interactive chat that delegates the
// ask-probe to a helper and returns the pending entry, its live wire frame
// (raw + typed) and the chain root the frame must name.
func wu8AwaitHelperApproval(t *testing.T, h *l11263Harness) (*approvalEntry, map[string]any, gen.ToolApprovalRequiredFrame, string) {
	t.Helper()
	gate := newL11263Latch()
	parent := l11263Script(l11263Delegate(l11263Leaf), l11263Call("read-after-child", "list_tasks", map[string]any{}))
	parent.steps[1].wait = gate.ch
	leaf := l11263Script(l11263Write())
	inst, ok := h.api.agentLoop.GetRegistry().GetAgent(l11263Agent)
	require.True(t, ok)
	inst.Provider = parent
	leafInst, ok := h.api.agentLoop.GetRegistry().GetAgent(l11263Leaf)
	require.True(t, ok)
	leafInst.Provider = leaf
	done := make(chan error, 1)
	go func() {
		_, err := h.api.agentLoop.ProcessDirectAs(context.Background(), l11263Agent, "Delegate the file write", "wu8-helper-parent")
		done <- err
	}()
	t.Cleanup(func() {
		gate.release()
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
	frameRaw := apprResReadFrame(t, h.tab, "tool_approval_required")
	frame := wu8TypedFrame[gen.ToolApprovalRequiredFrame](t, frameRaw)
	root := wu8RootOf(t, h, entry.SessionID)
	return entry, frameRaw, frame, root
}

func TestWU8_HelperApproval_LiveFrameNamesRootChatAndActingAgent(t *testing.T) {
	h := newL11263Harness(t, false, config.ToolPolicyAsk, nil)
	entry, _, frame, root := wu8AwaitHelperApproval(t, h)

	require.NotNil(t, frame.RootSessionId,
		"R6: a helper's card must name the root chat it renders in")
	require.Equal(t, root, *frame.RootSessionId,
		"root_session_id must be the persisted chain root")
	require.NotEqual(t, entry.SessionID, *frame.RootSessionId,
		"the acting helper session is not its own root here")
	require.NotNil(t, frame.ActingAgentName, "K7: the card names the acting agent")
	require.Equal(t, "Approval Leaf", *frame.ActingAgentName)
	require.Equal(t, entry.SessionID, frame.SessionId,
		"session_id stays the acting session (the Open target)")
	require.Nil(t, frame.OriginLabel, "a web-origin chat carries no origin label")
}

func TestWU8_HelperApproval_ReloadSnapshotCarriesFullRequest(t *testing.T) {
	h := newL11263Harness(t, false, config.ToolPolicyAsk, nil)
	entry, frameRaw, frame, root := wu8AwaitHelperApproval(t, h)

	snap, snapRaw := wu8SnapshotPending(t, h, entry.ApprovalID)

	// Bundle B3: the snapshot carries the full request, equal to the live
	// card, so a reload restores a complete, approvable card.
	require.NotEmpty(t, snap.ToolCallId, "B3: tool_call_id required in the snapshot")
	require.Equal(t, frame.ToolCallId, snap.ToolCallId)
	require.NotEmpty(t, snap.TurnId, "B3: turn_id required in the snapshot")
	require.Equal(t, frame.TurnId, snap.TurnId)
	require.NotNil(t, snap.Args, "B3: args required and never null in the snapshot")
	require.Equal(t, frameRaw["args"], snapRaw["args"],
		"the snapshot's args equal the live card's request arguments")
	require.Equal(t, l11263Write().Arguments, snap.Args,
		"the snapshot's args are the real request arguments")
	require.Equal(t, frameRaw["segments"], snapRaw["segments"],
		"the snapshot's segments equal the live card's segments (same item schema)")
	require.NotNil(t, snap.RootSessionId, "B3: the snapshot names the root chat too")
	require.Equal(t, root, *snap.RootSessionId)
	require.NotNil(t, snap.ActingAgentName, "B3: the snapshot names the acting agent")
	require.Equal(t, "Approval Leaf", *snap.ActingAgentName)
	require.Equal(t, frame.OriginLabel, snap.OriginLabel,
		"§1.5: live and snapshot origin label are equal")
}

func TestWU8_DirectChatApproval_RootSessionOmittedWhenSelf(t *testing.T) {
	h := newL11263Harness(t, false, config.ToolPolicyAsk, nil)
	leaf := l11263Script(l11263Write())
	inst, ok := h.api.agentLoop.GetRegistry().GetAgent(l11263Leaf)
	require.True(t, ok)
	inst.Provider = leaf
	done := make(chan error, 1)
	go func() {
		_, err := h.api.agentLoop.ProcessDirectAs(context.Background(), l11263Leaf, "Write the file", "wu8-direct-chat")
		done <- err
	}()
	t.Cleanup(func() {
		for _, e := range h.reg.pendingApprovals() {
			h.reg.resolve(e.ApprovalID, ApprovalActionCancel, false)
		}
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Error("direct chat cleanup did not join")
		}
	})
	entry := h.awaitInteractiveApproval(t)
	frame := wu8TypedFrame[gen.ToolApprovalRequiredFrame](t, apprResReadFrame(t, h.tab, "tool_approval_required"))

	require.Nil(t, frame.RootSessionId,
		"K7: root_session_id is omitted when the chain root IS the acting session")
	require.Equal(t, entry.SessionID, frame.SessionId)
	require.NotNil(t, frame.ActingAgentName)
	require.Equal(t, "Approval Leaf", *frame.ActingAgentName)

	snap, _ := wu8SnapshotPending(t, h, entry.ApprovalID)
	require.Nil(t, snap.RootSessionId, "the omission holds on the snapshot too")
	require.NotEmpty(t, snap.ToolCallId, "B3 completeness holds for a direct chat")
	require.NotNil(t, snap.Args)
}

func TestWU8_PeerApproval_OriginLabelNamesTheSenderAgent(t *testing.T) {
	h := newL11263Harness(t, false, config.ToolPolicyAsk, nil)
	h.ws.home = h.api.homePath
	h.api.agentLoop.SetAddressDeps(gatewayAddressDeps{h: h.ws})
	gate := newL11263Latch()
	sender := l11263Script(l11263Call("peer-request", "send_message", map[string]any{
		"workspace_id": testHarnessWorkspaceMembershipID, "agent_id": l11263Leaf, "content": "Write the probe file",
	}), l11263Claim())
	sender.steps[1].wait = gate.ch
	receiver := l11263Script(l11263Write())
	inst, ok := h.api.agentLoop.GetRegistry().GetAgent(l11263Agent)
	require.True(t, ok)
	inst.Provider = sender
	receiverInst, ok := h.api.agentLoop.GetRegistry().GetAgent(l11263Leaf)
	require.True(t, ok)
	receiverInst.Provider = receiver
	h.startPump(t)
	t.Cleanup(gate.release)
	h.startPlan(t)

	entry := h.awaitInteractiveApproval(t)
	frame := wu8TypedFrame[gen.ToolApprovalRequiredFrame](t, apprResReadFrame(t, h.tab, "tool_approval_required"))

	require.NotNil(t, frame.OriginLabel,
		"§6.18/K7: a peer-origin ask names the agent that asked")
	require.Equal(t, "asked by Approval Worker", *frame.OriginLabel)
	require.Nil(t, frame.RoomRef, "a peer request is not a room ask")

	snap, _ := wu8SnapshotPending(t, h, entry.ApprovalID)
	require.NotNil(t, snap.OriginLabel, "the label survives the reload")
	require.Equal(t, "asked by Approval Worker", *snap.OriginLabel)
	require.Equal(t, frame.OriginLabel, snap.OriginLabel)
}

func TestWU8_ResolvedFrame_CarriesRootSession(t *testing.T) {
	h := newL11263Harness(t, false, config.ToolPolicyAsk, nil)
	entry, _, _, root := wu8AwaitHelperApproval(t, h)

	resolved, gone := h.reg.resolve(entry.ApprovalID, ApprovalActionApprove, false)
	require.True(t, resolved && !gone)
	frame := wu8TypedFrame[gen.ToolApprovalResolvedFrame](t, apprResReadFrame(t, h.tab, "tool_approval_resolved"))

	require.NotNil(t, frame.RootSessionId,
		"K7: the resolved frame lets the root chat's card clear without a lookup")
	require.Equal(t, root, *frame.RootSessionId)
}

// TestWU8_ChannelApproval_OriginLabelNamesTheConversation closes the
// channel-origin gap the map named: an approval inside a turn that answers a
// person on a channel connection is labelled "while answering <person> on
// <connection>" (spec K7, §6.18 channel row; amendment §1.2 R6), live and on
// the reload snapshot. Per the spec's own BDD ("while answering Anna on
// tg-1") and the chief's 2026-10-18 ruling: <person> is the inbound
// message's sender display name and <connection> is the configured channel
// connection name (the WU-4 P0-d oracle string), not the chat id and not the
// channel kind. The fixture publishes the exact adapter/bus input shape.
func TestWU8_ChannelApproval_OriginLabelNamesTheConversation(t *testing.T) {
	h := newL11263Harness(t, false, config.ToolPolicyAsk, nil)
	leaf := l11263Script(l11263Write())
	inst, ok := h.api.agentLoop.GetRegistry().GetAgent(l11263Leaf)
	require.True(t, ok)
	inst.Provider = leaf
	h.startPump(t)
	require.NoError(t, h.ws.msgBus.PublishInbound(context.Background(), bus.InboundMessage{
		Channel: "telegram", InstanceID: "telegram-ops", ChatID: "origin-chat",
		Content:  "Write the file",
		Sender:   bus.SenderInfo{DisplayName: "Anna", CanonicalID: "telegram:anna"},
		Metadata: map[string]string{"agent_id": l11263Leaf, "workspace_id": testHarnessWorkspaceMembershipID},
	}))
	t.Cleanup(func() {
		for _, e := range h.reg.pendingApprovals() {
			h.reg.resolve(e.ApprovalID, ApprovalActionCancel, false)
		}
	})
	entry := h.awaitInteractiveApproval(t)
	frame := wu8TypedFrame[gen.ToolApprovalRequiredFrame](t, apprResReadFrame(t, h.tab, "tool_approval_required"))
	snap, _ := wu8SnapshotPending(t, h, entry.ApprovalID)
	resolved, gone := h.reg.resolve(entry.ApprovalID, ApprovalActionApprove, false)
	require.True(t, resolved && !gone)
	h.awaitTurnSettled(t, entry.SessionID)

	require.NotNil(t, frame.OriginLabel,
		"§6.18/K7: a channel-origin ask names the person and the connection")
	require.Equal(t, "while answering Anna on telegram-ops", *frame.OriginLabel)

	require.NotNil(t, snap.OriginLabel, "the channel label survives the reload")
	require.Equal(t, "while answering Anna on telegram-ops", *snap.OriginLabel)
	require.Equal(t, frame.OriginLabel, snap.OriginLabel)
}

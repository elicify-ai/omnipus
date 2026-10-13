// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/addressing"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// Gap 1: the answer BODY must be in the requester's next model request the
// moment the answer arrives, with nothing to fetch.
func TestOwnerWake_CarriesTheAnswerBodyIntoTheRequestersNextRequest(t *testing.T) {
	h := newD2bRoot(t)
	h.seedState(t, session.LifecycleCompleted)
	meta, err := h.al.GetSessionStore().GetMeta(h.id)
	require.NoError(t, err)
	wake := ownerWakeInbound(addressing.Capture{RequestID: "req-42", Source: addressing.Source{SessionID: h.id}},
		meta.AgentID, meta.WorkspaceID,
		session.TranscriptEntry{ID: "ans-1", AgentID: "ava", Content: "ANSWER-BODY-XYZ the budget is 12"})

	_, _, err = h.al.processMessage(context.Background(), wake)
	require.NoError(t, err)

	calls := h.provider.calls()
	require.Len(t, calls, 1)
	last := calls[0].lastUser
	assert.Contains(t, last, "ANSWER-BODY-XYZ the budget is 12", "the answer text is in the model's next input")
	assert.Contains(t, last, "ava", "labelled with who answered")
	assert.Contains(t, last, "req-42", "labelled with the request id")
	assert.Equal(t, 1, strings.Count(last, "ANSWER-BODY-XYZ"), "the text is in context exactly once")
	assert.False(t, reviveInboundIsHumanTurn(wake), "the wake stays non-human (F8)")
}

// answerFixtureRequest admits a request from agent "ray" (in a real conversation
// of its own, so the answer can be delivered back) to the receiver's main.
func answerFixtureRequest(t *testing.T, f *addrFixture) (requestID, receiverSession, askerSession string) {
	t.Helper()
	store := f.al.GetSessionStore()
	asker, err := store.NewSession(session.SessionTypeChat, "webchat", "ray")
	require.NoError(t, err)
	ws := addrWS
	require.NoError(t, store.SetMeta(asker.ID, session.MetaPatch{WorkspaceID: &ws}))
	id, sid, err := f.al.AdmitRequest(context.Background(), RequestAdmission{
		Receiver: addressing.Pair{WorkspaceID: addrWS, AgentID: addrReceiver}, Content: "what is the budget?",
		Sender: addressing.Sender{Agent: addressing.Pair{WorkspaceID: addrWS, AgentID: "ray"}}, SenderLabel: "agent ray",
		Source: addressing.Source{Kind: addressing.SourceConversation, SessionID: asker.ID,
			Owner: addressing.Pair{WorkspaceID: addrWS, AgentID: "ray"}},
	})
	require.NoError(t, err)
	_, ok := addrDrainInbound(t, f.bus) // the receiver's request wake
	require.True(t, ok, "the request wake is published")
	return id, sid, asker.ID
}

// Gap 5: the request the receiver sees tells it to answer with the tool.
func TestRequestEnvelope_NamesTheAnswerTool(t *testing.T) {
	f := newAddrFixture(t)
	id, sid, _ := answerFixtureRequest(t, f)
	entries, err := f.al.GetSessionStore().ReadTranscript(sid)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Contains(t, entries[0].Content, "answer_request")
	assert.Contains(t, entries[0].Content, id)
	assert.Contains(t, entries[0].Content, "does not reach the sender")
}

// Gap 5: a turn that ends with its request unanswered gets ONE non-human
// reminder; an answer through the tool's router clears it.
func TestUnansweredRequest_GetsOneNonHumanReminder(t *testing.T) {
	f := newAddrFixture(t)
	id, sid, _ := answerFixtureRequest(t, f)

	// The receiver's turn has been given the request and ends without answering.
	f.al.markRequestDelivered(sid, id)
	f.al.remindUnansweredRequests(sid)

	reminder, ok := addrDrainInbound(t, f.bus)
	require.True(t, ok, "an unanswered request must produce a reminder, never silence")
	assert.Contains(t, reminder.Content, id)
	assert.Contains(t, reminder.Content, "answer_request")
	assert.Equal(t, sid, reminder.SessionID)
	assert.NotEmpty(t, reminder.Metadata[ownerWakeMetadataKey], "non-human marker")
	assert.NotEmpty(t, reminder.Metadata[requestReminderMetadataKey])
	assert.False(t, reviveInboundIsHumanTurn(reminder))

	// Once: the next turn end does not repeat it.
	f.al.remindUnansweredRequests(sid)
	_, again := addrDrainInbound(t, f.bus)
	assert.False(t, again, "a request is reminded about once")
}

func TestReminder_NotSentBeforeTheRequestWasDelivered(t *testing.T) {
	f := newAddrFixture(t)
	_, sid, _ := answerFixtureRequest(t, f)
	f.al.remindUnansweredRequests(sid) // another turn ended; this request's turn has not run yet
	_, sent := addrDrainInbound(t, f.bus)
	assert.False(t, sent, "no reminder for a request no turn has seen")
}

func TestReminder_AnsweredOrDiscardedRequestsOweNone(t *testing.T) {
	f := newAddrFixture(t)
	id, sid, _ := answerFixtureRequest(t, f)
	f.al.markRequestDelivered(sid, id)

	// A real answer through the router (the tool's seam).
	msg := tools.NewMessageTool()
	msg.SetReplyRouter(f.r)
	ctx := tools.WithAgentID(tools.WithWorkspaceID(context.Background(), addrWS), addrReceiver)
	ctx = tools.WithTranscriptSessionID(ctx, sid)
	res := tools.NewAnswerRequestTool(msg).Execute(ctx, map[string]any{"request_id": id, "text": "done"})
	require.False(t, res.IsError, res.ForLLM)

	f.al.remindUnansweredRequests(sid)
	for {
		m, ok := addrDrainInbound(t, f.bus)
		if !ok {
			break
		}
		assert.Empty(t, m.Metadata[requestReminderMetadataKey], "an answered request owes no reminder")
	}

	id2, sid2, _ := answerFixtureRequest(t, f)
	f.al.markRequestDelivered(sid2, id2)
	require.NoError(t, f.al.DiscardRequest(sid2, id2))
	f.al.remindUnansweredRequests(sid2)
	if m, ok := addrDrainInbound(t, f.bus); ok {
		assert.Empty(t, m.Metadata[requestReminderMetadataKey], "a discarded request owes no reminder")
	}
}

// The answer tool's reply reaches the asker's conversation and wakes it WITH the body.
func TestAnswerTool_WakeCarriesTheBody(t *testing.T) {
	f := newAddrFixture(t)
	id, sid, askerSession := answerFixtureRequest(t, f)

	msg := tools.NewMessageTool()
	msg.SetReplyRouter(f.r)
	ctx := tools.WithAgentID(tools.WithWorkspaceID(context.Background(), addrWS), addrReceiver)
	ctx = tools.WithTranscriptSessionID(ctx, sid)
	res := tools.NewAnswerRequestTool(msg).Execute(ctx, map[string]any{"request_id": id, "text": "BUDGET-IS-FORTY"})
	require.False(t, res.IsError, res.ForLLM)

	var wake bus.InboundMessage
	var ok bool
	for i := 0; i < 5; i++ { // skip anything else queued; find the owner wake
		wake, ok = addrDrainInbound(t, f.bus)
		if !ok || wake.Metadata[ownerWakeMetadataKey] != "" {
			break
		}
	}
	require.True(t, ok, "the asker is woken")
	assert.Equal(t, askerSession, wake.SessionID)
	assert.Contains(t, wake.Content, "BUDGET-IS-FORTY")
	assert.Contains(t, wake.Content, id)
}

// Gap 5 through the real session worker: a turn the request was delivered to
// that ends with a plain-text answer produces the one non-human reminder; a turn
// that answered (pending cleared) produces none.
func TestWorkerTurn_PlainTextAnswerToARequestGetsTheReminder(t *testing.T) {
	prov := newStoppableProviderReplying("a plain answer that reaches nobody")
	f := newBusyClearFixture(t, prov)
	f.al.pendingRequests.Store("req-9", f.sid)

	wake := f.human("Request req-9 from agent ray. To answer it, call the answer_request tool.\n\nwhat is the budget?")
	wake.TranscriptEntryID = "req-9"
	wake.Metadata = map[string]string{"agent_id": testDefaultAgentID, ownerWakeMetadataKey: "1"}
	require.True(t, f.w.enqueue(wake))

	select {
	case m := <-f.al.bus.InboundChan():
		assert.Contains(t, m.Content, "req-9")
		assert.Contains(t, m.Content, "answer_request")
		assert.NotEmpty(t, m.Metadata[requestReminderMetadataKey])
		assert.NotEmpty(t, m.Metadata[ownerWakeMetadataKey], "non-human")
		assert.Equal(t, f.sid, m.SessionID)
	case <-time.After(15 * time.Second):
		t.Fatal("a turn ended with its request unanswered and no reminder was sent")
	}
}

func TestWorkerTurn_AnsweredRequestGetsNoReminder(t *testing.T) {
	prov := newStoppableProviderReplying("done")
	f := newBusyClearFixture(t, prov)
	f.al.pendingRequests.Store("req-10", f.sid)
	f.al.pendingRequests.Delete("req-10") // the answer was accepted during the turn

	wake := f.human("Request req-10 ...")
	wake.TranscriptEntryID = "req-10"
	wake.Metadata = map[string]string{"agent_id": testDefaultAgentID, ownerWakeMetadataKey: "1"}
	require.True(t, f.w.enqueue(wake))
	waitUntilClear(t, "the turn", 15*time.Second, func() bool { return prov.callCount() >= 1 })
	waitUntilClear(t, "idle", 15*time.Second, func() bool { return !f.w.inTurn.Load() })

	select {
	case m := <-f.al.bus.InboundChan():
		t.Fatalf("an answered request must owe no reminder; got %q", m.Content)
	case <-time.After(500 * time.Millisecond):
	}
}

// Reachability (CLAUDE.md Definition of Done): the answer tool is registered on
// every agent that can send messages (the policy side is pinned for the seeded
// roles in pkg/coreagent).
func TestAnswerRequest_IsRegisteredAndAllowed(t *testing.T) {
	f := newAddrFixture(t)
	checked := 0
	for _, id := range f.al.GetRegistry().ListAgentIDs() {
		inst, ok := f.al.GetRegistry().GetAgent(id)
		require.True(t, ok)
		if _, has := inst.Tools.Get("send_message"); !has {
			continue // not a messaging agent
		}
		checked++
		_, has := inst.Tools.Get("answer_request")
		assert.True(t, has, "agent %s can send messages but has no way to answer a request", id)
	}
	require.NotZero(t, checked, "instrument check: at least one messaging agent was examined")
}

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// Delta-review C2 (decision note ARCHITECT-C2-contract.md): a refused @-recipient
// send is MESSAGE-SCOPED. The refusal reaches the sender's connection tagged with
// the sender's chat (session_id) and the message's client_message_id, and is
// followed by message_status "failed" for that row. It ends no turn in the
// sender's chat, so the connection must never see a "done" frame for it.
//
// Oracles: contracts/asyncapi.yaml ErrorFrame (message-scoped refusal rule) and
// MessageStatusFrame (failed state); ordering is "error first, then the failed
// tick", as in websocket_chat.go::sendAppendFailureError. Every case enters
// through the production decoder (wsHandlerReadLoop.dispatchFrame).

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/addressing"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// scopeIndexOf returns the stream index of the first frame of the given type, or -1.
func scopeIndexOf(frames []map[string]any, typ string) int {
	for i, f := range frames {
		if f["type"] == typ {
			return i
		}
	}
	return -1
}

func scopeOfType(frames []map[string]any, typ string) []map[string]any {
	var out []map[string]any
	for _, f := range frames {
		if f["type"] == typ {
			out = append(out, f)
		}
	}
	return out
}

// scopeMintBoundOwner mints ann's chat in ws-1 and binds the connection to it,
// draining the owner's own turn from the bus.
func scopeMintBoundOwner(t *testing.T) (*WSHandler, *wsHandlerReadLoop, string, *recipientDeps) {
	t.Helper()
	h, b, deps := newRecipientHandler(t)
	wc := makeTestConn()
	t.Cleanup(wc.close)
	sid := mintOwnerChat(t, h, b, wc)
	bindTestConnToSession(h, "chat-owner", sid, wc)
	_ = recipientDecodeDrain(t, wc) // discard the mint-time frames
	reader := &wsHandlerReadLoop{h: h, wc: wc, ctx: context.Background(), chatID: "chat-owner"}
	return h, reader, sid, deps
}

func scopeSend(t *testing.T, reader *wsHandlerReadLoop, frameJSON string) []map[string]any {
	t.Helper()
	return recipientDecodeSend(t, reader, frameJSON)
}

// assertMessageScopedRefusal checks the whole C2 wire contract for one refusal.
func assertMessageScopedRefusal(t *testing.T, frames []map[string]any, sid, cid, wantText string) {
	t.Helper()
	errs := scopeOfType(frames, "error")
	require.Len(t, errs, 1, "exactly one visible refusal: %v", frames)
	assert.Contains(t, errs[0]["message"], wantText)
	assert.Equal(t, sid, errs[0]["session_id"], "the refusal must carry the sender's chat")
	assert.Equal(t, cid, errs[0]["client_message_id"], "the refusal must name the refused message")
	assert.NotContains(t, errs[0], "first_message_error", "a message-scoped refusal is not a first-send outcome")

	sts := scopeOfType(frames, "message_status")
	require.Len(t, sts, 1, "exactly one failed tick for the refused row: %v", frames)
	assert.Equal(t, "failed", sts[0]["state"])
	assert.Equal(t, sid, sts[0]["session_id"])
	assert.Equal(t, cid, sts[0]["client_message_id"])

	assert.Less(t, scopeIndexOf(frames, "error"), scopeIndexOf(frames, "message_status"),
		"error first, then the failed tick (sendAppendFailureError ordering): %v", frames)
	assert.Equal(t, -1, scopeIndexOf(frames, "done"),
		"a refusal ends no turn in the sender's chat, so no done frame: %v", frames)
}

func TestRecipientRefusalIsMessageScoped(t *testing.T) {
	cases := []struct {
		name  string
		frame func(sid string) string
		want  string
	}{
		{
			name: "own agent",
			frame: func(sid string) string {
				return `{"type":"message","session_id":"` + sid + `","agent_id":"ann","content":"@ann hi",` +
					`"client_message_id":"c2-self","recipient":{"workspace_id":"ws-1","agent_id":"ann"}}`
			},
			want: "already belongs to that agent",
		},
		{
			name: "ineligible receiver",
			frame: func(sid string) string {
				return `{"type":"message","session_id":"` + sid + `","agent_id":"ann","content":"@nobody hi",` +
					`"client_message_id":"c2-inelig","recipient":{"workspace_id":"ws-1","agent_id":"nobody"}}`
			},
			want: "cannot be reached",
		},
		{
			name: "unknown owning chat",
			frame: func(string) string {
				return `{"type":"message","session_id":"missing-session","agent_id":"ann","content":"@ray hi",` +
					`"client_message_id":"c2-missing","recipient":{"workspace_id":"ws-1","agent_id":"ray"}}`
			},
			want: "session not found",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, reader, sid, _ := scopeMintBoundOwner(t)
			wantSid := sid
			if tc.name == "unknown owning chat" {
				wantSid = "missing-session"
			}
			frames := scopeSend(t, reader, tc.frame(sid))
			cid := map[string]string{"own agent": "c2-self", "ineligible receiver": "c2-inelig", "unknown owning chat": "c2-missing"}[tc.name]
			assertMessageScopedRefusal(t, frames, wantSid, cid, tc.want)
		})
	}
}

// A refusal with no owning chat is the "needs an existing chat" refusal itself:
// nothing to tag, so it stays global and ticks nothing (decision note).
func TestRecipientRefusalWithoutOwningChatStaysGlobal(t *testing.T) {
	_, reader, _, _ := scopeMintBoundOwner(t)
	frames := scopeSend(t, reader,
		`{"type":"message","agent_id":"ann","content":"@ray hi","client_message_id":"c2-nochat",`+
			`"recipient":{"workspace_id":"ws-1","agent_id":"ray"}}`)
	errs := scopeOfType(frames, "error")
	require.Len(t, errs, 1, "%v", frames)
	assert.Contains(t, errs[0]["message"], "needs an existing chat")
	assert.NotContains(t, errs[0], "session_id")
	assert.NotContains(t, errs[0], "client_message_id")
	assert.Empty(t, scopeOfType(frames, "message_status"))
}

// A recipient send without a client_message_id has no row to reconcile: the
// error is tagged with the chat only, and no tick is sent.
func TestRecipientRefusalWithoutClientIDTagsChatOnly(t *testing.T) {
	_, reader, sid, _ := scopeMintBoundOwner(t)
	frames := scopeSend(t, reader,
		`{"type":"message","session_id":"`+sid+`","agent_id":"ann","content":"@ann hi",`+
			`"recipient":{"workspace_id":"ws-1","agent_id":"ann"}}`)
	errs := scopeOfType(frames, "error")
	require.Len(t, errs, 1, "%v", frames)
	assert.Equal(t, sid, errs[0]["session_id"])
	assert.NotContains(t, errs[0], "client_message_id")
	assert.Empty(t, scopeOfType(frames, "message_status"))
}

// The over-long id is by definition invalid, so only the chat is tagged (and no
// tick can be keyed on it). Only a recipient send is retagged; an ordinary
// over-long refusal keeps its untagged shape.
func TestOverlongClientIDRefusalTagsChatOnlyForRecipientSend(t *testing.T) {
	_, reader, sid, _ := scopeMintBoundOwner(t)
	long := strings.Repeat("x", maxClientMessageIDChars+1)

	frames := scopeSend(t, reader,
		`{"type":"message","session_id":"`+sid+`","agent_id":"ann","content":"@ray hi","client_message_id":"`+long+`",`+
			`"recipient":{"workspace_id":"ws-1","agent_id":"ray"}}`)
	errs := scopeOfType(frames, "error")
	require.Len(t, errs, 1, "%v", frames)
	assert.Contains(t, errs[0]["message"], "client_message_id must be at most")
	assert.Equal(t, sid, errs[0]["session_id"])
	assert.NotContains(t, errs[0], "client_message_id")
	assert.Empty(t, scopeOfType(frames, "message_status"))

	frames = scopeSend(t, reader,
		`{"type":"message","session_id":"`+sid+`","agent_id":"ann","content":"hi","client_message_id":"`+long+`"}`)
	errs = scopeOfType(frames, "error")
	require.Len(t, errs, 1, "%v", frames)
	assert.NotContains(t, errs[0], "session_id", "an ordinary over-long refusal is unchanged")
}

// flipDeps admits the receiver for the first `okCalls` eligibility reads and
// refuses afterwards, so the preflight passes and AdmitRequest's own recheck
// (F1) is what fails: the late failure frame in admitRecipientRequest.
type flipDeps struct {
	mu      sync.Mutex
	calls   int
	okCalls int
}

func (d *flipDeps) PairEligible(ws, a string) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls++
	return ws == "ws-1" && a == "ray" && d.calls <= d.okCalls, nil
}

func (d *flipDeps) PublishGuestReply(string, session.TranscriptEntry) {}

func TestLateAdmitFailureIsMessageScoped(t *testing.T) {
	h, reader, sid, _ := scopeMintBoundOwner(t)
	fd := &flipDeps{okCalls: 1 << 30}
	h.agentLoop.SetAddressDeps(fd)

	// Instrument: count the eligibility reads of the preflight alone.
	_, err := h.agentLoop.CheckPeerAdmissionDecision(context.Background(), addressing.Pair{WorkspaceID: "ws-1", AgentID: "ray"}, sid)
	require.NoError(t, err, "instrument: the receiver must be admitted before the flip")
	fd.mu.Lock()
	fd.okCalls, fd.calls = fd.calls, 0
	require.Positive(t, fd.okCalls)
	fd.mu.Unlock()

	frames := scopeSend(t, reader,
		`{"type":"message","session_id":"`+sid+`","agent_id":"ann","content":"@ray hi","client_message_id":"c2-late",`+
			`"recipient":{"workspace_id":"ws-1","agent_id":"ray"}}`)
	assertMessageScopedRefusal(t, frames, sid, "c2-late", "could not be delivered")
}

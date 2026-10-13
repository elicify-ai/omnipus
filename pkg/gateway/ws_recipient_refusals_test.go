// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// S2 M13 and M14. The existing recipient refusal tests assert only "nothing
// was saved and nothing started" — never WHAT THE SENDER IS TOLD — and the test
// connection never carries a user id, so the sender principal on an @recipient
// request was never exercised. Both matter: a refusal the user cannot read is
// an invisible failure, and a principal that silently becomes a human would
// forge authority on the receiver's side.
//
// Oracles: ws_recipient.go::refuseRecipient's rule list (C-ADDRESS / FR-045,
// contract MessageFrame.recipient) and its doc comment "the sender is the
// authenticated connection principal (empty stays empty — anonymous/shared auth
// is never turned into a human)".

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/addressing"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// recipientRefusalFrames drains the connection and returns the messages of the
// visible error frames it received.
func recipientRefusalFrames(t *testing.T, wc *wsConn) []string {
	t.Helper()
	var out []string
	for {
		select {
		case data := <-wc.sendCh:
			var f map[string]any
			require.NoError(t, json.Unmarshal(data, &f), "server frame must be JSON: %s", data)
			if f["type"] == "error" {
				msg, _ := f["message"].(string)
				out = append(out, msg)
			}
		case <-time.After(300 * time.Millisecond):
			return out
		}
	}
}

// TestRecipient_EveryRefusalTellsTheSenderWhy pins the user-facing sentence for
// each refusal rule, so a refusal can never become invisible or generic.
func TestRecipient_EveryRefusalTellsTheSenderWhy(t *testing.T) {
	cases := []struct {
		name      string
		sessionID func(owner string) string
		to        addressing.Pair
		kickoff   bool
		setup     func(*recipientDeps)
		want      string
	}{
		{
			name: "no owning chat", sessionID: func(string) string { return "" },
			to:   addressing.Pair{WorkspaceID: "ws-1", AgentID: "ray"},
			want: "needs an existing chat (session_id)",
		},
		{
			name: "workspace setup kickoff", sessionID: func(o string) string { return o },
			to: addressing.Pair{WorkspaceID: "ws-1", AgentID: "ray"}, kickoff: true,
			want: "workspace setup message cannot be addressed to another agent",
		},
		{
			name: "malformed pair", sessionID: func(o string) string { return o },
			to:   addressing.Pair{WorkspaceID: "ws-1"},
			want: "not a valid {workspace_id, agent_id} pair",
		},
		{
			name: "unknown owning chat", sessionID: func(string) string { return "missing-session" },
			to:   addressing.Pair{WorkspaceID: "ws-1", AgentID: "ray"},
			want: "session not found",
		},
		{
			name: "the chat's own agent", sessionID: func(o string) string { return o },
			to: addressing.Pair{WorkspaceID: "ws-1", AgentID: "ann"},
			setup: func(d *recipientDeps) {
				d.eligible["ws-1/ann"] = true
			},
			want: "already belongs to that agent",
		},
		{
			name: "ineligible receiver", sessionID: func(o string) string { return o },
			to: addressing.Pair{WorkspaceID: "ws-1", AgentID: "ray"},
			setup: func(d *recipientDeps) {
				d.eligible = map[string]bool{}
			},
			want: "cannot be reached",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, b, deps := newRecipientHandler(t)
			wc := makeTestConn()
			t.Cleanup(wc.close)
			sid := mintOwnerChat(t, h, b, wc)
			if tc.setup != nil {
				tc.setup(deps)
			}
			store := h.agentLoop.ResolveSessionStore(sid)
			before, err := store.ReadTranscript(sid)
			require.NoError(t, err)

			to := tc.to
			h.handleChatMessageToRecipient(context.Background(), "chat-owner", tc.sessionID(sid),
				"@x do it", "ann", nil, "", "ws-1", tc.kickoff, "", nil, &to, wc)

			frames := recipientRefusalFrames(t, wc)
			require.Len(t, frames, 1, "a refusal must reach the sender exactly once: %v", frames)
			assert.Contains(t, frames[0], tc.want,
				"the refusal must tell the sender why, in the curated sentence for this rule")
			_, started := nextInbound(t, b, 300*time.Millisecond)
			assert.False(t, started, "a refused recipient starts no turn")
			after, err := store.ReadTranscript(sid)
			require.NoError(t, err)
			assert.Len(t, after, len(before), "a refused recipient saves nothing")
		})
	}
}

// NOTE (S2 M13, honestly scoped): admitRecipientRequest's own fail-closed
// branch — "the source chat's owner cannot be read" — cannot be reached from a
// test today. refuseRecipient reads the SAME store and the SAME session id
// immediately before it, so a request that passes the preflight always finds a
// readable meta a few lines later; only a store that changes between the two
// reads would reach it, and there is no seam to interleave one. Its sibling
// branch (the receiver delivery error) is covered by
// TestRecipient_EveryRefusalTellsTheSenderWhy's "ineligible receiver" case and
// by ws_recipient_test.go's refusal table. Reported as an unreachable-without-
// seam branch rather than pinned by an invented fixture.

// TestRecipient_SenderPrincipalIsTheConnectionPrincipal is S2 M14: the capture
// the receiver's side later authorises against must carry the authenticated
// connection principal — and an unauthenticated connection must stay empty,
// never be turned into a human.
func TestRecipient_SenderPrincipalIsTheConnectionPrincipal(t *testing.T) {
	for _, tc := range []struct {
		name   string
		userID string
	}{
		{"authenticated connection", "u-42"},
		{"anonymous connection stays empty", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, b, _ := newRecipientHandler(t)
			wc := makeTestConn()
			t.Cleanup(wc.close)
			wc.userID = tc.userID
			// mintOwnerChat already drains the owner's own turn from the bus.
			sid := mintOwnerChat(t, h, b, wc)

			to := addressing.Pair{WorkspaceID: "ws-1", AgentID: "ray"}
			h.handleChatMessageToRecipient(context.Background(), "chat-owner", sid,
				"@ray please review X", "ann", nil, "", "ws-1", false, "", nil, &to, wc)

			in, ok := nextInbound(t, b, 2*time.Second)
			require.True(t, ok, "the recipient's turn must be started")
			mainID, err := session.MainSessionID("ws-1", "ray")
			require.NoError(t, err)
			require.Equal(t, mainID, in.SessionID)

			capture, err := h.agentLoop.RequestLedger().Resolve(mainID, to, in.TranscriptEntryID)
			require.NoError(t, err, "the admitted request must be recorded in the addressing ledger")
			assert.Equal(t, tc.userID, capture.Sender.Principal,
				"the capture must carry the authenticated connection principal verbatim")
			assert.Equal(t, addressing.SourceConversation, capture.Source.Kind,
				"an @-request's source is the owning conversation")
			assert.Equal(t, sid, capture.Source.SessionID,
				"the capture must name the owning chat the answer returns to")
		})
	}
}

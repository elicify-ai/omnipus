// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// RED pack for parent-wake visibility, Test 2.
//
// Oracle: docs/internal/specs/parent-wake-visibility-spec.md
//   - BDD "Scenario Outline: Stream channel follows the parent's real destination"
//   - BDD "A web wake does not rewrite the stored channel"
//   - Dataset: Stream channel
//   - FR-001, FR-002
//   - §9.4 channel rules
//
// main and empty, with no peer, stream on webchat with the parent session id.
// A telegram peer stays telegram. A webchat peer stays webchat plus that peer.
// Stored metadata channel is not rewritten. A steered child's own turn stays webchat.
//
// Step 4 items 2–3 are deferred to CHECK.
package agent

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

func TestWakeChannel_MainBecomesWebchatStoredUntouched(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()

	t.Run("a steered child still streams on webchat", func(t *testing.T) {
		parentID := newTestSteeringSession(t, al, "ws-pw-child")
		launched, err := NewSteerLauncher(al).Launch(t.Context(), steer.LaunchRequest{
			SteeringSessionID: parentID,
			TargetAgentID:     testDefaultAgentID,
			Task:              "child channel control",
			Origin:            steer.Origin{Kind: steer.OriginKindDelegate, CallID: "call-pw-channel"},
		})
		if err != nil {
			t.Fatalf("Launch: %v", err)
		}
		rec, err := al.GetSessionLifecycleStore().Load(launched.SessionID)
		if err != nil {
			t.Fatalf("Load child: %v", err)
		}
		ts, err := al.reconstructSteeredTurn(rec, nil)
		if err != nil {
			t.Fatalf("reconstruct child: %v", err)
		}
		if ts.channel != "webchat" {
			t.Fatalf("child stream channel = %q, want webchat — Test 2 must not change the helper's own live view", ts.channel)
		}
	})

	cases := []struct {
		name          string
		storedChannel string
		peer          string
		channelSess   bool
		wantStream    string
		wantChatIsSID bool
		wantChat      string
	}{
		{name: "main and empty peer", storedChannel: "main", wantStream: "webchat", wantChatIsSID: true},
		{name: "empty channel and empty peer", storedChannel: "", wantStream: "webchat", wantChatIsSID: true},
		{name: "telegram peer", storedChannel: "telegram", peer: "peer-9", channelSess: true, wantStream: "telegram", wantChat: "peer-9"},
		{name: "webchat peer", storedChannel: "webchat", peer: "peer-web", channelSess: true, wantStream: "webchat", wantChat: "peer-web"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			meta := pwChannelSession(t, al, tc.storedChannel, tc.peer, tc.channelSess)
			before, err := al.GetSessionStore().GetMeta(meta.ID)
			if err != nil {
				t.Fatalf("GetMeta before: %v", err)
			}
			rec, err := al.ensureOrdinaryRootRecord(al.GetSessionLifecycleStore(), meta.ID, al.GetSessionStore())
			if err != nil {
				t.Fatalf("ensureOrdinaryRootRecord: %v", err)
			}
			ts, err := al.reconstructSteeredTurn(rec, nil)
			if err != nil {
				t.Fatalf("reconstructSteeredTurn: %v", err)
			}
			if ts.channel != tc.wantStream {
				t.Fatalf("stream channel = %q, want %q — stored channel %q must follow the stream-channel table (FR-001 / FR-002 / §9.4)", ts.channel, tc.wantStream, tc.storedChannel)
			}
			wantChat := tc.wantChat
			if tc.wantChatIsSID {
				wantChat = meta.ID
			}
			if ts.chatID != wantChat {
				t.Fatalf("stream chat id = %q, want %q (FR-001 / dataset Stream channel)", ts.chatID, wantChat)
			}
			after, err := al.GetSessionStore().GetMeta(meta.ID)
			if err != nil {
				t.Fatalf("GetMeta after: %v", err)
			}
			if after.Channel != before.Channel {
				t.Fatalf("stored channel changed from %q to %q — a wake must not rewrite metadata (FR-001)", before.Channel, after.Channel)
			}
		})
	}
}

func pwChannelSession(t *testing.T, al *AgentLoop, channel, peer string, channelSession bool) *session.UnifiedMeta {
	t.Helper()
	store := al.GetSessionStore()
	if channelSession {
		meta, err := store.NewChannelSession(channel, channel+".1", peer, testDefaultAgentID, "pw channel")
		if err != nil {
			t.Fatalf("NewChannelSession(%s): %v", channel, err)
		}
		return meta
	}
	meta, err := store.NewSession(session.SessionTypeChat, channel, testDefaultAgentID)
	if err != nil {
		t.Fatalf("NewSession(channel %q): %v", channel, err)
	}
	return meta
}

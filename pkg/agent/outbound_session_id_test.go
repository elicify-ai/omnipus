// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// outbound_session_id_test.go — ADR-082 D6/FR-011: the agent loop's
// post-turn PublishOutbound must carry the transcript session id so
// webchatChannel.Send (pkg/gateway/webchat_channel.go) can resolve delivery
// targets by session id first, chat id second. Before this fix,
// bus.OutboundMessage.SessionID was never set by runAgentLoop's post-turn
// publish, which made collectSessionConnsLocked's session-id fallback dead
// code — the confirmed cause of E5 (keeper-originated turns whose stale
// ChatID no longer resolves to a live connection failing to deliver twice
// per fire, even when the session's CURRENT connection is perfectly live).

package agent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// TestRunAgentLoop_PublishOutboundCarriesSessionID drives a REAL
// system-channel-dispatched turn (processSystemMessage — the exact path
// AsyncNotifier.Notify uses for keeper follow-ups, delegate-completion
// continuations, etc., which is the only processOptions constructor setting
// SendResponse:true) and asserts the resulting bus.OutboundMessage carries
// the originating transcript session id.
func TestRunAgentLoop_PublishOutboundCarriesSessionID(t *testing.T) {
	al, msgBus, delegate, _ := newAsyncResultTestLoop(t, &mockProvider{})

	store := al.GetSessionStore()
	require.NotNil(t, store, "session store must exist")
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", delegate.ID)
	require.NoError(t, err, "create session")
	t.Cleanup(func() { _ = store.DeleteSession(meta.ID) })

	// No StreamDelegate registered on this bus — mirrors a closed/absent WS
	// connection, so the response can ONLY reach webchatChannel.Send via
	// PublishOutbound (never via the streaming path). mockProvider returns
	// non-empty content ("Mock response"), so SendResponse:true's
	// PublishOutbound branch fires.
	msg := drainNotify(t, al, msgBus, AsyncNotifyEvent{
		Channel:             "webchat",
		ChatID:              "webchat:some-stale-chat-id",
		AgentID:             delegate.ID,
		TranscriptSessionID: meta.ID,
		SourceKind:          "bash",
		Content:             "Background build finished: 0 errors.",
	})

	outboundCh := msgBus.OutboundChan()

	_, err = al.processSystemMessage(context.Background(), msg)
	require.NoError(t, err)

	select {
	case out := <-outboundCh:
		require.Equal(t, "webchat", out.Channel)
		require.Equal(t, meta.ID, out.SessionID,
			"ADR-082 D6/FR-011: PublishOutbound must carry the transcript session id so "+
				"webchatChannel.Send can resolve by session id even when ChatID no longer "+
				"points at a live connection")
		require.Equal(t, "Mock response", out.Content)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for PublishOutbound")
	}
}

// TestRunAgentLoop_PublishOutbound_EmptySessionWhenNoTranscriptSession proves
// the negative case: when the reconstructed turn has no resolvable
// transcript session (msg.AsyncTranscriptSessionID unset or unresolvable),
// OutboundMessage.SessionID is simply empty — never a stale/wrong value —
// and webchatChannel.Send's chatID fallback still applies.
func TestRunAgentLoop_PublishOutbound_EmptySessionWhenNoTranscriptSession(t *testing.T) {
	al, msgBus, delegate, _ := newAsyncResultTestLoop(t, &mockProvider{})

	msg := drainNotify(t, al, msgBus, AsyncNotifyEvent{
		Channel:    "webchat",
		ChatID:     "webchat:no-session-chat",
		AgentID:    delegate.ID,
		SourceKind: "bash",
		Content:    "no transcript session known",
	})
	require.Empty(t, msg.AsyncTranscriptSessionID, "test setup: no transcript session id supplied")

	outboundCh := msgBus.OutboundChan()

	_, err := al.processSystemMessage(context.Background(), msg)
	require.NoError(t, err)

	select {
	case out := <-outboundCh:
		require.Equal(t, "", out.SessionID, "no transcript session was ever resolved — SessionID must stay empty")
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for PublishOutbound")
	}
}

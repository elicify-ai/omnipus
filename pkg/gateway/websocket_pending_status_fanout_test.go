// websocket_pending_status_fanout_test.go — regression coverage for review
// finding 12 (SQUAD-BRIEF-AZ / REVIEW-OPUS-823.md #12): the "working" message
// status tick was delivered only to the connection that SENT the message
// (pendingMessageStatus.wc), not to every connection currently bound to the
// session. After a reconnect the sender's old (dead) connection kept the
// queued entry, so the reconnected tab never saw "working" and stayed on
// "Received" forever. Separately, an entry that never got consumed by a
// GetStreamer call (e.g. a non-streaming round) was never cleared, so a
// LATER, unrelated turn on the same session could pop it and mislabel the
// wrong message as "working".
package gateway

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
)

// TestQueueWorkingStatus_FansOutToAllSessionConnections proves the fix for
// finding 12's primary scenario: after a reconnect, a SECOND connection is
// now bound to the same session (the original sender's connection may be
// dead/stale but is still in the map). The "working" status must reach
// EVERY connection bound to the session, not just the one that sent the
// message.
func TestQueueWorkingStatus_FansOutToAllSessionConnections(t *testing.T) {
	msgBus := bus.NewMessageBus()
	handler, _ := newTestWSHandlerForModelName(t, msgBus)

	wcSender := makeTestConn()
	handler.handleChatMessageWithClientID(
		context.Background(), "chat-fanout", "", "hello", "", nil,
		"", "", false, "client-fanout-1", wcSender,
	)

	receivedFrames := readMessageStatusFrames(t, wcSender, 1)
	sessionID := receivedFrames[0].SessionId
	if sessionID == "" {
		t.Fatalf("expected a session id on the 'received' status frame")
	}

	// This lightweight harness drives handleChatMessageWithClientID directly
	// (no real WS accept loop, see ws_test_helpers_test.go), so the sender's
	// own chatID is never registered in h.sessions/h.sessionIDs the way the
	// production accept loop (websocket.go's handleWS) would register it.
	// Bind it explicitly here to mirror that real registration — otherwise
	// resolveSessionConnsLocked (which the fan-out fix relies on) would not
	// resolve the sender as a delivery target at all, independent of the
	// bug under test.
	bindTestConnToSession(handler, "chat-fanout", sessionID, wcSender)

	// A second connection (e.g. a reconnected tab) is now bound to the SAME
	// session, under a different chatID.
	wcReconnected := makeTestConn()
	bindTestConnToSession(handler, "chat-fanout-reconnected", sessionID, wcReconnected)

	// Simulate the turn's first round starting — this is what triggers the
	// queued "working" status to be sent.
	if _, ok := handler.GetStreamer(context.Background(), "webchat", "chat-fanout", sessionID); !ok {
		t.Fatalf("expected GetStreamer to succeed for a valid session id")
	}

	// The sender's own connection must still get "working" (pre-existing,
	// already covered by TestHandleChatMessage_AcknowledgesPersistenceBeforeTurnStart).
	senderFrames := readMessageStatusFrames(t, wcSender, 1)
	if senderFrames[0].State != "working" {
		t.Fatalf("expected sender connection to receive 'working', got %q", senderFrames[0].State)
	}

	// BUG REGRESSION: the reconnected connection — bound to the SAME
	// session, but not the one that originally sent the message — must ALSO
	// receive the "working" status. Before the fix, sendPendingMessageWorking
	// sent only to pending.wc (the original sender), so this connection
	// would never see anything and time out here.
	select {
	case raw := <-wcReconnected.sendCh:
		var envelope struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			t.Fatalf("failed to decode frame: %v", err)
		}
		if envelope.Type != string(generated.WsFrameTypeMessageStatus) {
			t.Fatalf("expected a message_status frame on the reconnected connection, got %q", envelope.Type)
		}
		var frame generated.MessageStatusFrame
		if err := json.Unmarshal(raw, &frame); err != nil {
			t.Fatalf("failed to decode message_status frame: %v", err)
		}
		if frame.State != "working" {
			t.Fatalf("expected 'working' on the reconnected connection, got %q", frame.State)
		}
		if frame.ClientMessageId != "client-fanout-1" {
			t.Fatalf("expected client_message_id 'client-fanout-1', got %q", frame.ClientMessageId)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("BUG REGRESSION: the reconnected connection bound to the same session never received the 'working' status — the tick is still tied to the sending connection (finding 12)")
	}
}

// TestPendingMessageStatus_LeftoverEntry_FlushedAndClearedAtTurnEnd proves
// the second half of finding 12: a queued status entry that never got
// consumed by a mid-turn GetStreamer call (e.g. a round that opened no
// streamer at all — a non-streaming reply, or a message that steered into
// an already-active turn without ever winning its own GetStreamer call)
// must not survive past the turn's own "done". Before the fix, such an
// entry sat in WSHandler.pendingMessageStatuses forever, keeping a
// (possibly dead) connection reference alive and available to be popped by
// a LATER, UNRELATED turn on the same session — mislabeling the wrong
// message as "working".
func TestPendingMessageStatus_LeftoverEntry_FlushedAndClearedAtTurnEnd(t *testing.T) {
	msgBus := bus.NewMessageBus()
	handler, _ := newTestWSHandlerForModelName(t, msgBus)
	wch := newWebchatChannel(handler)

	wc := makeTestConn()
	handler.handleChatMessageWithClientID(
		context.Background(), "chat-leftover", "", "hello", "", nil,
		"", "", false, "client-leftover-1", wc,
	)

	receivedFrames := readMessageStatusFrames(t, wc, 1)
	sessionID := receivedFrames[0].SessionId
	if sessionID == "" {
		t.Fatalf("expected a session id on the 'received' status frame")
	}
	// Mirror the real accept-loop registration (see the fan-out test above
	// for why this is needed in this lightweight harness).
	bindTestConnToSession(handler, "chat-leftover", sessionID, wc)

	// Deliberately do NOT call GetStreamer — this round opens no streamer,
	// so the queued "working" entry is never consumed the normal way. The
	// turn ends anyway (Finalize is called once per turn regardless of how
	// many rounds opened a streamer — see wsStreamerFinalize.sendDone's own
	// doc comment).
	streamer := &wsStreamer{sessionID: sessionID, chatID: "chat-leftover", channel: wch}
	streamer.SetTurnID("turn-leftover")
	if err := streamer.Finalize(context.Background(), "the answer"); err != nil {
		t.Fatalf("Finalize failed: %v", err)
	}

	// The leftover entry must have been flushed as "working" BEFORE the
	// done frame — read frames until we see both, in order.
	var sawWorking, sawDone bool
	for i := 0; i < 4 && (!sawWorking || !sawDone); i++ {
		select {
		case raw := <-wc.sendCh:
			var envelope struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal(raw, &envelope); err != nil {
				t.Fatalf("failed to decode frame: %v", err)
			}
			switch envelope.Type {
			case string(generated.WsFrameTypeMessageStatus):
				var frame generated.MessageStatusFrame
				if err := json.Unmarshal(raw, &frame); err != nil {
					t.Fatalf("failed to decode message_status frame: %v", err)
				}
				if frame.State == "working" && frame.ClientMessageId == "client-leftover-1" {
					if sawDone {
						t.Fatalf("BUG REGRESSION: 'working' status arrived AFTER 'done' — a done must imply working, not race it")
					}
					sawWorking = true
				}
			case string(generated.WsFrameTypeDone):
				sawDone = true
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for frames; sawWorking=%v sawDone=%v", sawWorking, sawDone)
		}
	}
	if !sawWorking {
		t.Fatalf("BUG REGRESSION: the leftover status entry (never consumed by GetStreamer) was never flushed to 'working' at turn end (finding 12)")
	}
	if !sawDone {
		t.Fatalf("expected a 'done' frame at turn end")
	}

	// The entry must be CLEARED, not just flushed — otherwise a later,
	// unrelated turn on this same session could still pop a stale copy.
	if _, ok := handler.takePendingMessageStatus(sessionID); ok {
		t.Fatalf("BUG REGRESSION: a pending status entry survived turn end — it can leak into a later, unrelated turn on the same session (finding 12)")
	}
}

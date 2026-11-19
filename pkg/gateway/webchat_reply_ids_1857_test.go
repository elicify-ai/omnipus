package gateway

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
)

// #1857 X1 (diagnosis fix B). The no-stream fallback in webchatChannel.Send
// builds the token and done frames of a turn that has no streamer (a refusal
// before any stream, such as the LLM-call limit, or an ordinary non-streamed
// reply). Those frames must never carry the turn id or the message id of an
// EARLIER, already streamed turn: the SPA treats a token that carries a
// finished bubble's ids as a late token for that bubble and drops it, so the
// reply never shows.
//
// The streamer of a finished turn hands the channel its identity and marks it
// streamed (recordTurnIdentity + markStreamed, exactly what
// wsStreamerFinalize does); the channel's Send for that turn is then a no-op.

const (
	x1Turn1ID    = "turn-1"
	x1Message1ID = "02943d61-turn-1-message"
	x1ChatID     = "progress-notice-chat" // chat id used by newDirectWebchatSend
)

// x1FinishStreamedTurn replays what a streamed turn leaves behind: the
// identity and the streamed flag, then the channel Send for that turn.
func x1FinishStreamedTurn(t *testing.T, ch *webchatChannel, wc *wsConn, turnID, messageID, reply string) {
	t.Helper()
	seedWebchatSlots(ch, x1ChatID, true, turnID, messageID)
	if err := ch.Send(context.Background(), bus.OutboundMessage{Channel: "webchat", ChatID: x1ChatID, Content: reply}); err != nil {
		t.Fatalf("Send for the streamed turn %q: %v", turnID, err)
	}
	if frames := drainDirectFrames(t, wc); len(frames) != 0 {
		t.Fatalf("the streamed turn %q must not be delivered twice, saw frames %s", turnID, framePreview(frames))
	}
}

// x1FallbackFrames sends a reply for a turn that has no streamer and returns
// the decoded token and done frames.
func x1FallbackFrames(t *testing.T, ch *webchatChannel, wc *wsConn, content string) (generated.TokenFrame, generated.DoneFrame) {
	t.Helper()
	if err := ch.Send(context.Background(), bus.OutboundMessage{Channel: "webchat", ChatID: x1ChatID, Content: content}); err != nil {
		t.Fatalf("fallback Send: %v", err)
	}
	frames := drainDirectFrames(t, wc)
	if len(frames) != 2 {
		t.Fatalf("fallback delivery must be one token then one done frame, saw %d: %s", len(frames), framePreview(frames))
	}
	var token generated.TokenFrame
	if err := json.Unmarshal(frames[0], &token); err != nil {
		t.Fatalf("unmarshal token frame: %v body %s", err, frames[0])
	}
	var done generated.DoneFrame
	if err := json.Unmarshal(frames[1], &done); err != nil {
		t.Fatalf("unmarshal done frame: %v body %s", err, frames[1])
	}
	if token.Type != string(generated.WsFrameTypeToken) || done.Type != string(generated.WsFrameTypeDone) {
		t.Fatalf("frame types = %q then %q, want token then done: %s", token.Type, done.Type, framePreview(frames))
	}
	if token.Content != content {
		t.Fatalf("token content = %q, want %q", token.Content, content)
	}
	return token, done
}

func x1RequireNoEarlierTurnIDs(t *testing.T, token generated.TokenFrame, done generated.DoneFrame) {
	t.Helper()
	str := func(p *string) string {
		if p == nil {
			return "<absent>"
		}
		return *p
	}
	if token.TurnId != nil && *token.TurnId == x1Turn1ID {
		t.Errorf("fix B: the second turn's token frame carries turn 1's turn_id %q (the SPA drops it as a late token of the finished bubble); token turn_id=%s message_id=%s",
			x1Turn1ID, str(token.TurnId), str(token.MessageId))
	}
	if token.MessageId != nil && *token.MessageId == x1Message1ID {
		t.Errorf("fix B: the second turn's token frame carries turn 1's message_id %q; token turn_id=%s message_id=%s",
			x1Message1ID, str(token.TurnId), str(token.MessageId))
	}
	if done.TurnId != nil && *done.TurnId == x1Turn1ID {
		t.Errorf("fix B: the second turn's done frame carries turn 1's turn_id %q; done turn_id=%s message_id=%s",
			x1Turn1ID, str(done.TurnId), str(done.MessageId))
	}
	if done.MessageId != nil && *done.MessageId == x1Message1ID {
		t.Errorf("fix B: the second turn's done frame carries turn 1's message_id %q; done turn_id=%s message_id=%s",
			x1Message1ID, str(done.TurnId), str(done.MessageId))
	}
}

// Test 1: turn 1 streams, turn 2 is refused by the LLM-call limit before any
// stream exists, so its W11 reply goes out through the fallback. The chat is
// bound to a session, so the frames go through the session hub (the path the
// hands-on run took).
func TestWebchatSend_RefusedTurnAfterStreamedTurn_FramesCarryNoEarlierTurnIDs_1857(t *testing.T) {
	handler, _, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)
	wc := makeTestConn()
	wc.sendCh = make(chan []byte, 64)
	bindTestConnToSession(handler, x1ChatID, "sess-x1-refusal", wc)
	ch := newWebchatChannel(handler)
	seedWebchatSlots(ch, x1ChatID, true, x1Turn1ID, x1Message1ID)
	if err := ch.Send(context.Background(), bus.OutboundMessage{Channel: "webchat", ChatID: x1ChatID, SessionID: "sess-x1-refusal", Content: "first reply"}); err != nil {
		t.Fatalf("Send for turn 1: %v", err)
	}

	w11 := generated.LLMErrorUserMessages["agent_request_limit"]
	if w11 == "" {
		t.Fatal("SETUP: the generated catalogue must carry agent_request_limit")
	}
	if err := ch.Send(context.Background(), bus.OutboundMessage{Channel: "webchat", ChatID: x1ChatID, SessionID: "sess-x1-refusal", Content: w11}); err != nil {
		t.Fatalf("fallback Send: %v", err)
	}
	var token generated.TokenFrame
	var done generated.DoneFrame
	var gotToken, gotDone bool
	deadline := time.After(3 * time.Second)
	for !gotToken || !gotDone {
		select {
		case raw := <-wc.sendCh:
			var m struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal(raw, &m); err != nil {
				t.Fatalf("unmarshal frame: %v", err)
			}
			switch m.Type {
			case "token":
				if err := json.Unmarshal(raw, &token); err != nil {
					t.Fatalf("unmarshal token: %v", err)
				}
				gotToken = true
			case "done":
				if err := json.Unmarshal(raw, &done); err != nil {
					t.Fatalf("unmarshal done: %v", err)
				}
				gotDone = true
			}
		case <-deadline:
			t.Fatalf("no token+done frames for the refused turn (token=%v done=%v)", gotToken, gotDone)
		}
	}
	if token.Content != w11 {
		t.Fatalf("token content = %q, want the W11 sentence %q", token.Content, w11)
	}
	x1RequireNoEarlierTurnIDs(t, token, done)
}

// Test 2: the same shape with an ordinary non-streamed reply (no error).
func TestWebchatSend_PlainReplyAfterStreamedTurn_FramesCarryNoEarlierTurnIDs_1857(t *testing.T) {
	ch, wc, _ := newDirectWebchatSend(t)
	x1FinishStreamedTurn(t, ch, wc, x1Turn1ID, x1Message1ID, "first reply")

	token, done := x1FallbackFrames(t, ch, wc, "second reply, not streamed")
	x1RequireNoEarlierTurnIDs(t, token, done)
}

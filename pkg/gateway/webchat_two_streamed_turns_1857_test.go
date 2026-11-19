package gateway

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
)

// x1CollectTokenDone reads frames from wc until it has seen wantDone done
// frames, then waits briefly and drains, so a wrong duplicate delivery is
// counted instead of missed.
func x1CollectTokenDone(t *testing.T, wc *wsConn, wantDone int) ([]generated.TokenFrame, []generated.DoneFrame) {
	t.Helper()
	var tokens []generated.TokenFrame
	var dones []generated.DoneFrame
	take := func(raw []byte) {
		var m struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("unmarshal frame: %v", err)
		}
		switch m.Type {
		case "token":
			var f generated.TokenFrame
			if err := json.Unmarshal(raw, &f); err != nil {
				t.Fatalf("unmarshal token: %v", err)
			}
			tokens = append(tokens, f)
		case "done":
			var f generated.DoneFrame
			if err := json.Unmarshal(raw, &f); err != nil {
				t.Fatalf("unmarshal done: %v", err)
			}
			dones = append(dones, f)
		}
	}
	deadline := time.After(3 * time.Second)
	for len(dones) < wantDone {
		select {
		case raw := <-wc.sendCh:
			take(raw)
		case <-deadline:
			t.Fatalf("saw %d done frames, want %d (tokens=%d)", len(dones), wantDone, len(tokens))
		}
	}
	time.Sleep(200 * time.Millisecond)
	for {
		select {
		case raw := <-wc.sendCh:
			take(raw)
		default:
			return tokens, dones
		}
	}
}

// Guard for fix B (may pass before the fix): two streamed turns in a row,
// driven through the REAL wsStreamer (GetStreamer, SetTurnID, SetMessageID,
// Update, Finalize) and the channel's Send, as the agent loop and the channel
// manager do. Each turn's token and done frames carry that turn's own ids;
// turn 1's ids are still on its done frame (the fix must not clear them
// before it); no id of turn 1 shows on turn 2's frames; and Send never
// delivers a streamed turn a second time. The expected ids are the ones this
// test sets on each streamer.
func TestWebchatSend_TwoStreamedTurns_EachKeepsItsOwnIdentity_1857(t *testing.T) {
	h, _ := newTestWSHandlerForModelName(t, bus.NewMessageBus())
	h.webchatCh = newWebchatChannel(h)
	wc := makeTestConn()
	wc.sendCh = make(chan []byte, 256)
	const chatID = "chat-x1-two-streamed"
	sid := mintSessionFor(t, h, chatID, wc)

	type ids struct{ turn, msg, reply string }
	turns := []ids{
		{"turn-1", "msg-1", "reply of turn one"},
		{"turn-2", "msg-2", "reply of turn two"},
	}
	for _, want := range turns {
		st, ok := h.GetStreamer(context.Background(), "webchat", chatID, sid)
		if !ok {
			t.Fatalf("GetStreamer refused for %s", want.turn)
		}
		ws, ok := st.(*wsStreamer)
		if !ok {
			t.Fatalf("streamer type %T, want *wsStreamer", st)
		}
		ws.SetTurnID(want.turn)
		ws.SetMessageID(want.msg)
		if err := ws.Update(context.Background(), want.reply); err != nil {
			t.Fatalf("Update %s: %v", want.turn, err)
		}
		if err := ws.Finalize(context.Background(), want.reply); err != nil {
			t.Fatalf("Finalize %s: %v", want.turn, err)
		}
		if err := h.webchatCh.Send(context.Background(), bus.OutboundMessage{
			Channel: "webchat", ChatID: chatID, SessionID: sid, Content: want.reply,
		}); err != nil {
			t.Fatalf("Send %s: %v", want.turn, err)
		}
	}

	tokens, dones := x1CollectTokenDone(t, wc, 2)
	if len(dones) != 2 {
		t.Fatalf("done frames = %d, want exactly 2 (one per streamed turn, none added by Send)", len(dones))
	}
	val := func(p *string) string {
		if p == nil {
			return "<absent>"
		}
		return *p
	}
	for i, want := range turns {
		if got := val(dones[i].TurnId); got != want.turn {
			t.Errorf("done[%d] turn_id = %s, want %q", i, got, want.turn)
		}
		if got := val(dones[i].MessageId); got != want.msg {
			t.Errorf("done[%d] message_id = %s, want %q", i, got, want.msg)
		}
	}
	if len(tokens) == 0 {
		t.Fatal("no token frames streamed")
	}
	for i, f := range tokens {
		want := turns[1]
		if f.Content == turns[0].reply {
			want = turns[0]
		}
		if got := val(f.TurnId); got != want.turn {
			t.Errorf("token[%d] (%q) turn_id = %s, want %q", i, f.Content, got, want.turn)
		}
		if got := val(f.MessageId); got != want.msg {
			t.Errorf("token[%d] (%q) message_id = %s, want %q", i, f.Content, got, want.msg)
		}
	}
}

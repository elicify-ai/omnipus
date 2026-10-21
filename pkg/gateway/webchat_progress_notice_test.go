// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
)

// Progress-notice oracles are the T24b delivery decision
// (retry-shape-REPORT.md Decision 1 and 3, RED proof table) and
// RETRY-FIX-PLAN.md. They are not copied from a run of Send.
//
// The direct-send fixture registers the origin chat with no bound session
// so webchatChannel.Send takes the synchronous origin branch (empty session
// id) instead of the session hub. makeTestConn's sendCh is the socket edge.

const (
	// progressNoticeText is retryTimeout's whole published sentence
	// (retry-shape-REPORT.md Decision 2). The unmarked control uses the same
	// bytes so a text match cannot be what suppresses the frame.
	progressNoticeText = "Retrying — please wait..."
	// realAnswerText is the decision's RED-proof completion content.
	realAnswerText = "real answer"
	// Preseeded identity the unmarked completion must carry unchanged.
	progressKeptTurnID    = "turn-real-answer"
	progressKeptMessageID = "msg-real-answer"
)

func TestWebchatSend_ProgressNotice(t *testing.T) {
	t.Run("json flag is not a wire key", testProgressNoticeJSONNotWire)
	t.Run("unmarked retry sentence still completes", testUnmarkedRetrySentenceCompletes)
	t.Run("flagged notice then real answer, no slots", func(t *testing.T) {
		testProgressNoticeThenCompletion(t, false, false)
	})
	t.Run("flagged notice preserves turn identity", func(t *testing.T) {
		testProgressNoticeThenCompletion(t, false, true)
	})
	t.Run("flagged notice preserves streamed skip", func(t *testing.T) {
		testProgressNoticeThenCompletion(t, true, false)
	})
	t.Run("flagged notice preserves streamed skip and turn identity", func(t *testing.T) {
		testProgressNoticeThenCompletion(t, true, true)
	})
	t.Run("flagged notice on a bound session is not a completion", testProgressNoticeBoundSession)
}

// testProgressNoticeJSONNotWire locks Decision 1: ProgressNotice is the same
// class as OwnershipChecked. Flagged and unflagged envelopes marshal to the
// same bytes, and a progress_notice key on the wire does not set the field.
func testProgressNoticeJSONNotWire(t *testing.T) {
	plain := bus.OutboundMessage{
		Channel:   "webchat",
		ChatID:    "chat-json",
		SessionID: "sess-json",
		Content:   progressNoticeText,
	}
	flagged := plain
	flagged.ProgressNotice = true

	plainJSON, err := json.Marshal(plain)
	if err != nil {
		t.Fatalf("marshal unflagged envelope: %v", err)
	}
	flaggedJSON, err := json.Marshal(flagged)
	if err != nil {
		t.Fatalf("marshal flagged envelope: %v", err)
	}
	if string(plainJSON) != string(flaggedJSON) {
		t.Errorf("flagged JSON\n%s\n!= unflagged JSON\n%s\nProgressNotice must be json:\"-\"", flaggedJSON, plainJSON)
	}
	encoded := string(flaggedJSON)
	if strings.Contains(encoded, "progress_notice") || strings.Contains(encoded, "ProgressNotice") {
		t.Errorf("marshalled envelope contains a progress-notice key: %s", encoded)
	}

	var roundTrip bus.OutboundMessage
	if err := json.Unmarshal(flaggedJSON, &roundTrip); err != nil {
		t.Fatalf("unmarshal flagged envelope: %v", err)
	}
	if roundTrip.ProgressNotice {
		t.Errorf("round-trip ProgressNotice = true, want false")
	}
	if roundTrip.Content != progressNoticeText || roundTrip.Channel != "webchat" || roundTrip.SessionID != "sess-json" {
		t.Errorf("round-trip content/channel/session = %q/%q/%q, want %q/webchat/sess-json",
			roundTrip.Content, roundTrip.Channel, roundTrip.SessionID, progressNoticeText)
	}

	var wire map[string]any
	if err := json.Unmarshal(plainJSON, &wire); err != nil {
		t.Fatalf("unmarshal envelope object: %v", err)
	}
	wire["progress_notice"] = true
	injected, err := json.Marshal(wire)
	if err != nil {
		t.Fatalf("marshal injected wire key: %v", err)
	}
	var fromWire bus.OutboundMessage
	if err := json.Unmarshal(injected, &fromWire); err != nil {
		t.Fatalf("unmarshal injected wire key: %v", err)
	}
	if fromWire.ProgressNotice {
		t.Errorf("ProgressNotice = true after a progress_notice wire key, want false")
	}
	if fromWire.Content != progressNoticeText {
		t.Errorf("content after injected key = %q, want %q", fromWire.Content, progressNoticeText)
	}
}

// testUnmarkedRetrySentenceCompletes is the sink control and the text-match
// control. The same sentence the notice uses, without the flag, must still
// be a real completion. An empty drain is not evidence unless this passes.
func testUnmarkedRetrySentenceCompletes(t *testing.T) {
	ch, wc, chatID := newDirectWebchatSend(t)
	msg := bus.OutboundMessage{Channel: "webchat", ChatID: chatID, Content: progressNoticeText}
	if err := ch.Send(context.Background(), msg); err != nil {
		t.Fatalf("Send unmarked retry sentence: %v", err)
	}
	assertDirectCompletion(t, wc, progressNoticeText, "", "")
}

// testProgressNoticeThenCompletion checks one streamed/identity combination.
// The flagged notice must emit nothing and leave both slots. The following
// unmarked message then takes today's skip or today's token+done fallback.
func testProgressNoticeThenCompletion(t *testing.T, streamed, withIdentity bool) {
	ch, wc, chatID := newDirectWebchatSend(t)
	turnID, messageID := "", ""
	if withIdentity {
		turnID, messageID = progressKeptTurnID, progressKeptMessageID
	}
	seedWebchatSlots(ch, chatID, streamed, turnID, messageID)

	notice := bus.OutboundMessage{
		Channel:        "webchat",
		ChatID:         chatID,
		Content:        progressNoticeText,
		ProgressNotice: true,
	}
	if err := ch.Send(context.Background(), notice); err != nil {
		t.Fatalf("Send progress notice: %v", err)
	}
	if frames := drainDirectFrames(t, wc); len(frames) != 0 {
		t.Errorf("progress notice emitted %d frame(s), want 0: %s", len(frames), framePreview(frames))
	}
	assertWebchatSlots(t, ch, chatID, streamed, turnID, messageID)

	completion := bus.OutboundMessage{Channel: "webchat", ChatID: chatID, Content: realAnswerText}
	if err := ch.Send(context.Background(), completion); err != nil {
		t.Fatalf("Send unmarked completion: %v", err)
	}
	if streamed {
		frames := drainDirectFrames(t, wc)
		if len(frames) != 0 {
			t.Errorf("unmarked send after a preserved streamed slot emitted %d frame(s), want 0 (already-streamed skip): %s",
				len(frames), framePreview(frames))
		}
		return
	}
	assertDirectCompletion(t, wc, realAnswerText, turnID, messageID)
}

// testProgressNoticeBoundSession is the session-hub case the direct-origin
// cases do not cover. A guard that returns early only when the session id
// is empty would keep those cases green and still emit token+done for a
// bound chat. The oracle is retry-shape-REPORT.md Decision 3: no frame for
// the notice, slots untouched, then today's no-stream fallback.
//
// hubPublishMetaAlsoTo calls sessionHub.publishMeta, which enqueues to the
// bound connection before Send returns (it does not use the async submit
// drainer). The wait below is still a deadlock guard, not a latency check:
// collection stops at the real answer's done, so any progress frame the
// hub ordered first is in the collected prefix.
func testProgressNoticeBoundSession(t *testing.T) {
	const (
		chatID    = "progress-bound-chat"
		sessionID = "progress-bound-session"
	)
	handler, _, _ := newTestWSHandler(t)
	wc := makeTestConn()
	bindTestConnToSession(handler, chatID, sessionID, wc)
	ch := newWebchatChannel(handler)
	ch.recordTurnIdentity(chatID, progressKeptTurnID, progressKeptMessageID)

	notice := bus.OutboundMessage{
		Channel:        "webchat",
		ChatID:         chatID,
		Content:        progressNoticeText,
		ProgressNotice: true,
	}
	if err := ch.Send(context.Background(), notice); err != nil {
		t.Fatalf("Send progress notice on bound session: %v", err)
	}
	assertWebchatSlots(t, ch, chatID, false, progressKeptTurnID, progressKeptMessageID)

	completion := bus.OutboundMessage{Channel: "webchat", ChatID: chatID, Content: realAnswerText}
	if err := ch.Send(context.Background(), completion); err != nil {
		t.Fatalf("Send unmarked completion on bound session: %v", err)
	}
	frames := collectThroughTurnDone(t, wc, progressKeptTurnID)
	assertBoundCompletion(t, frames, sessionID, realAnswerText, progressKeptTurnID, progressKeptMessageID)
}

// collectThroughTurnDone reads until the done frame that closes turnID.
// busDeliveryTimeout is the package failsafe for "the frame never arrived",
// not a limit on how fast a correct publish must show up.
func collectThroughTurnDone(t *testing.T, wc *wsConn, turnID string) [][]byte {
	t.Helper()
	guard := time.NewTimer(busDeliveryTimeout)
	defer guard.Stop()
	var frames [][]byte
	for {
		select {
		case raw, ok := <-wc.sendCh:
			if !ok {
				t.Fatal("sendCh closed")
			}
			if len(raw) == 0 {
				t.Fatal("empty frame on sendCh")
			}
			frames = append(frames, raw)
			if doneClosesTurn(raw, turnID) {
				return append(frames, takeQueuedDirect(t, wc)...)
			}
		case <-guard.C:
			t.Fatalf("deadlock guard: bound session delivered no done for turn %q; collected %d: %s",
				turnID, len(frames), framePreview(frames))
		}
	}
}

func doneClosesTurn(raw []byte, turnID string) bool {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return false
	}
	if m["type"] != string(generated.WsFrameTypeDone) {
		return false
	}
	got, _ := m["turn_id"].(string)
	return got == turnID
}

func takeQueuedDirect(t *testing.T, wc *wsConn) [][]byte {
	t.Helper()
	var extra [][]byte
	for {
		select {
		case raw, ok := <-wc.sendCh:
			if !ok {
				t.Fatal("sendCh closed")
			}
			if len(raw) == 0 {
				t.Fatal("empty frame on sendCh")
			}
			extra = append(extra, raw)
		default:
			wc.qmu.Lock()
			queued, held := len(wc.q), len(wc.held)
			wc.qmu.Unlock()
			if queued != 0 || held != 0 {
				t.Fatalf("bound send left queued=%d held=%d frames outside sendCh", queued, held)
			}
			return extra
		}
	}
}

// assertBoundCompletion checks the hub fallback: one token of the real
// answer, then one stats-less done, both carrying the bound session and
// the identity the progress notice must not have consumed. It does not
// call assertTokenFrame/assertDoneFrame — those require an empty session id.
func assertBoundCompletion(t *testing.T, frames [][]byte, sessionID, content, turnID, messageID string) {
	t.Helper()
	if len(frames) != 2 {
		t.Errorf("bound frames = %d, want 2 (real token then real done, no progress): %s",
			len(frames), framePreview(frames))
		return
	}
	tokenType := frameType(t, frames[0])
	doneType := frameType(t, frames[1])
	if tokenType != string(generated.WsFrameTypeToken) || doneType != string(generated.WsFrameTypeDone) {
		t.Errorf("bound frame types = %q then %q, want token then done: %s",
			tokenType, doneType, framePreview(frames))
		return
	}
	var token generated.TokenFrame
	if err := json.Unmarshal(frames[0], &token); err != nil {
		t.Fatalf("unmarshal bound TokenFrame: %v body %s", err, frames[0])
	}
	var done generated.DoneFrame
	if err := json.Unmarshal(frames[1], &done); err != nil {
		t.Fatalf("unmarshal bound DoneFrame: %v body %s", err, frames[1])
	}
	if token.Content != content {
		t.Errorf("bound token content = %q, want %q", token.Content, content)
	}
	if strings.Contains(string(frames[0]), progressNoticeText) || strings.Contains(string(frames[1]), progressNoticeText) {
		t.Errorf("bound completion includes the progress sentence: %s", framePreview(frames))
	}
	if token.SessionId != sessionID {
		t.Errorf("bound token session_id = %q, want %q", token.SessionId, sessionID)
	}
	if done.SessionId != sessionID {
		t.Errorf("bound done session_id = %q, want %q", done.SessionId, sessionID)
	}
	if done.Stats != nil {
		t.Errorf("bound done stats = %+v, want absent", done.Stats)
	}
	assertOptionalID(t, "bound token turn_id", token.TurnId, turnID)
	assertOptionalID(t, "bound token message_id", token.MessageId, messageID)
	assertOptionalID(t, "bound done turn_id", done.TurnId, turnID)
	assertOptionalID(t, "bound done message_id", done.MessageId, messageID)
	if token.Seq == nil || done.Seq == nil {
		t.Errorf("bound frames missing seq (token %v, done %v); hub publishMeta stamps one", token.Seq, done.Seq)
		return
	}
	if *done.Seq != *token.Seq+1 {
		t.Errorf("bound done seq = %d, want token seq %d + 1", *done.Seq, *token.Seq)
	}
}

// newDirectWebchatSend builds the synchronous origin-connection fixture.
// No sessionIDs entry and no hub binding: Send must not take the async hub.
func newDirectWebchatSend(t *testing.T) (*webchatChannel, *wsConn, string) {
	t.Helper()
	handler, _, _ := newTestWSHandler(t)
	wc := makeTestConn()
	const chatID = "progress-notice-chat"
	handler.mu.Lock()
	handler.sessions[chatID] = wc
	if _, bound := handler.sessionIDs[chatID]; bound {
		handler.mu.Unlock()
		t.Fatalf("fixture bound a session for %s; direct send would not be synchronous", chatID)
	}
	handler.mu.Unlock()
	return newWebchatChannel(handler), wc, chatID
}

func seedWebchatSlots(ch *webchatChannel, chatID string, streamed bool, turnID, messageID string) {
	if streamed {
		ch.markStreamed(chatID)
	}
	if turnID != "" || messageID != "" {
		ch.recordTurnIdentity(chatID, turnID, messageID)
	}
}

func assertWebchatSlots(
	t *testing.T, ch *webchatChannel, chatID string, wantStreamed bool, wantTurn, wantMessage string,
) {
	t.Helper()
	ch.mu.Lock()
	gotStreamed := ch.streamed[chatID]
	id, hasID := ch.turnIdentity[chatID]
	ch.mu.Unlock()
	if gotStreamed != wantStreamed {
		t.Errorf("streamed[%s] = %v, want %v (progress notice must not consume or set it)", chatID, gotStreamed, wantStreamed)
	}
	if wantTurn == "" && wantMessage == "" {
		if hasID {
			t.Errorf("turn identity for %s = %+v, want none", chatID, id)
		}
		return
	}
	if !hasID || id.turnID != wantTurn || id.messageID != wantMessage {
		t.Errorf("turn identity = %+v present=%v, want turn %q message %q", id, hasID, wantTurn, wantMessage)
	}
}

// drainDirectFrames reads frames Send already queued. enqueue writes sendCh
// before Send returns when the channel has room, so this drain does not wait.
// Frames left on the overflow queue are a failure, not an empty sink.
func drainDirectFrames(t *testing.T, wc *wsConn) [][]byte {
	t.Helper()
	var out [][]byte
	for {
		select {
		case raw, ok := <-wc.sendCh:
			if !ok {
				t.Fatal("sendCh closed")
			}
			if len(raw) == 0 {
				t.Fatal("empty frame on sendCh")
			}
			out = append(out, raw)
		default:
			wc.qmu.Lock()
			queued, held := len(wc.q), len(wc.held)
			wc.qmu.Unlock()
			if queued != 0 || held != 0 {
				t.Fatalf("direct send left queued=%d held=%d frames outside sendCh", queued, held)
			}
			return out
		}
	}
}

func framePreview(frames [][]byte) string {
	parts := make([]string, 0, len(frames))
	for _, raw := range frames {
		s := string(raw)
		if len(s) > 240 {
			s = s[:240] + "..."
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, " | ")
}

// assertDirectCompletion requires today's no-stream fallback: exactly one
// token of content, then exactly one stats-less done. turnID/messageID empty
// means those generated fields stay absent. A non-empty pair must be stamped
// on both frames (the identity the progress notice must not have consumed).
func assertDirectCompletion(t *testing.T, wc *wsConn, content, turnID, messageID string) {
	t.Helper()
	frames := drainDirectFrames(t, wc)
	if len(frames) != 2 {
		t.Errorf("completion frames = %d, want 2 (token then done): %s", len(frames), framePreview(frames))
		return
	}
	tokenType := frameType(t, frames[0])
	doneType := frameType(t, frames[1])
	if tokenType != string(generated.WsFrameTypeToken) || doneType != string(generated.WsFrameTypeDone) {
		t.Errorf("frame types = %q then %q, want %q then %q; bodies %s",
			tokenType, doneType, generated.WsFrameTypeToken, generated.WsFrameTypeDone, framePreview(frames))
		return
	}
	assertTokenFrame(t, frames[0], content, turnID, messageID)
	assertDoneFrame(t, frames[1], turnID, messageID)
}

func frameType(t *testing.T, raw []byte) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("frame is not a JSON object: %v body %s", err, raw)
	}
	got, _ := m["type"].(string)
	return got
}

func assertTokenFrame(t *testing.T, raw []byte, content, turnID, messageID string) {
	t.Helper()
	var frame generated.TokenFrame
	if err := json.Unmarshal(raw, &frame); err != nil {
		t.Fatalf("unmarshal TokenFrame: %v body %s", err, raw)
	}
	if frame.Type != string(generated.WsFrameTypeToken) {
		t.Errorf("token type = %q, want %q", frame.Type, generated.WsFrameTypeToken)
	}
	if frame.Content != content {
		t.Errorf("token content = %q, want %q", frame.Content, content)
	}
	if frame.SessionId != "" {
		t.Errorf("token session_id = %q, want empty (no bound session)", frame.SessionId)
	}
	assertOptionalID(t, "token turn_id", frame.TurnId, turnID)
	assertOptionalID(t, "token message_id", frame.MessageId, messageID)
}

func assertDoneFrame(t *testing.T, raw []byte, turnID, messageID string) {
	t.Helper()
	var frame generated.DoneFrame
	if err := json.Unmarshal(raw, &frame); err != nil {
		t.Fatalf("unmarshal DoneFrame: %v body %s", err, raw)
	}
	if frame.Type != string(generated.WsFrameTypeDone) {
		t.Errorf("done type = %q, want %q", frame.Type, generated.WsFrameTypeDone)
	}
	if frame.Stats != nil {
		t.Errorf("done stats = %+v, want absent (stats-less fallback done)", frame.Stats)
	}
	if frame.SessionId != "" {
		t.Errorf("done session_id = %q, want empty (no bound session)", frame.SessionId)
	}
	assertOptionalID(t, "done turn_id", frame.TurnId, turnID)
	assertOptionalID(t, "done message_id", frame.MessageId, messageID)
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("re-read done frame: %v", err)
	}
	if _, ok := m["stats"]; ok {
		t.Errorf("done frame JSON includes stats, want the key absent: %s", raw)
	}
}

func assertOptionalID(t *testing.T, what string, got *string, want string) {
	t.Helper()
	if want == "" {
		if got != nil {
			t.Errorf("%s = %q, want absent", what, *got)
		}
		return
	}
	if got == nil || *got != want {
		shown := "<nil>"
		if got != nil {
			shown = *got
		}
		t.Errorf("%s = %s, want %q", what, shown, want)
	}
}

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/providers"
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

// retryStopResetError is the recognized transport drop the producer test
// already uses. IsTransportFailure matches it; ClassifyError does not, and
// the loop then treats it as a timeout retry. It is not a sentence observed
// from a run of this test.
const retryStopResetError = "streaming read error: http2: response body closed"

// retryStopFollowUp is the text the provider double emits on every call
// after the blocked retry. The assertion is that production delivers this
// input after the cancellation terminal, not a sentence read off the server.
const retryStopFollowUp = "follow-up answer"

// retryStopProvider is the network boundary only. Call 0 is the reset.
// Call 1 blocks on the turn context and closes retryExited only after that
// context ends. Later calls block on followUpRelease so the test can read
// the live follow-up turn id before any answer token.
type retryStopProvider struct {
	mu              sync.Mutex
	calls           int
	firstStream     bool
	firstEntered    chan struct{}
	retryEntered    chan struct{}
	retryExited     chan struct{}
	followUpEntered chan struct{}
	followUpRelease chan struct{}
}

func newRetryStopProvider() *retryStopProvider {
	return &retryStopProvider{
		firstEntered:    make(chan struct{}),
		retryEntered:    make(chan struct{}),
		retryExited:     make(chan struct{}),
		followUpEntered: make(chan struct{}),
		followUpRelease: make(chan struct{}),
	}
}

func (p *retryStopProvider) GetDefaultModel() string { return "retry-stop-provider" }

func (p *retryStopProvider) Chat(
	ctx context.Context,
	_ []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	return p.step(ctx, false, nil)
}

func (p *retryStopProvider) ChatStream(
	ctx context.Context,
	_ []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
	onChunk func(string),
	_ providers.OnToolCallProgress,
) (*providers.LLMResponse, error) {
	return p.step(ctx, true, onChunk)
}

func (p *retryStopProvider) step(
	ctx context.Context, stream bool, onChunk func(string),
) (*providers.LLMResponse, error) {
	switch n := p.note(stream); n {
	case 0:
		close(p.firstEntered)
		return nil, errors.New(retryStopResetError)
	case 1:
		close(p.retryEntered)
		<-ctx.Done()
		close(p.retryExited)
		return nil, ctx.Err()
	default:
		if n == 2 {
			close(p.followUpEntered)
		}
		select {
		case <-p.followUpRelease:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		if onChunk != nil {
			onChunk(retryStopFollowUp)
		}
		return &providers.LLMResponse{Content: retryStopFollowUp}, nil
	}
}

func (p *retryStopProvider) note(stream bool) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := p.calls
	p.calls++
	if n == 0 {
		p.firstStream = stream
	}
	return n
}

func (p *retryStopProvider) firstCallStreamed() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.firstStream
}

var (
	_ providers.LLMProvider       = (*retryStopProvider)(nil)
	_ providers.StreamingProvider = (*retryStopProvider)(nil)
)

// retryStopFrameLog is the bound socket's inbound frames. notify is a
// wake-up, not a frame queue: waitMatch always re-reads frames under mu.
type retryStopFrameLog struct {
	mu     sync.Mutex
	frames [][]byte
	notify chan struct{}
}

func newRetryStopFrameLog() *retryStopFrameLog {
	return &retryStopFrameLog{notify: make(chan struct{}, 1)}
}

func (l *retryStopFrameLog) add(raw []byte) {
	l.mu.Lock()
	l.frames = append(l.frames, append([]byte(nil), raw...))
	l.mu.Unlock()
	select {
	case l.notify <- struct{}{}:
	default:
	}
}

// retryStopMatch runs under the log lock. It must not lock or mutate.
type retryStopMatch func(frames [][]byte) (hit []byte, ok bool)

func (l *retryStopFrameLog) waitMatch(t *testing.T, what string, match retryStopMatch) []byte {
	t.Helper()
	deadline := time.NewTimer(cancelTestTurnStartDeadline)
	defer deadline.Stop()
	for {
		l.mu.Lock()
		hit, ok := match(l.frames)
		var copied []byte
		if ok {
			copied = append([]byte(nil), hit...)
		}
		l.mu.Unlock()
		if ok {
			return copied
		}
		select {
		case <-deadline.C:
			t.Fatalf("deadlock guard: %s was not delivered within %s", what, cancelTestTurnStartDeadline)
		case <-l.notify:
		}
	}
}

func (l *retryStopFrameLog) snapshot() [][]byte {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([][]byte, len(l.frames))
	for i, raw := range l.frames {
		out[i] = append([]byte(nil), raw...)
	}
	return out
}

func readRetryStopSocket(conn *websocket.Conn, log *retryStopFrameLog) {
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return
		}
		log.add(raw)
	}
}

// retryStopNotice sits only at the manager's channel registration. Every
// method is the real *webchatChannel it embeds. Send calls that value's Send
// and signals only after it returns. handler.webchatCh keeps the unwrapped
// channel, so streamer bookkeeping is not this wrapper. The signal is not a
// frame and is not the cancellation terminal.
type retryStopNotice struct {
	*webchatChannel
	once     sync.Once
	returned chan struct{}
	mu       sync.Mutex
	msg      bus.OutboundMessage
	sendErr  error
	saw      bool
}

func newRetryStopNotice(channel *webchatChannel) *retryStopNotice {
	return &retryStopNotice{webchatChannel: channel, returned: make(chan struct{})}
}

func (n *retryStopNotice) Send(ctx context.Context, msg bus.OutboundMessage) error {
	err := n.webchatChannel.Send(ctx, msg)
	if msg.ProgressNotice || msg.Content == progressNoticeText {
		n.mu.Lock()
		n.msg = msg
		n.sendErr = err
		n.saw = true
		n.mu.Unlock()
		n.once.Do(func() { close(n.returned) })
	}
	return err
}

func (n *retryStopNotice) assertRetryNotice(t *testing.T) {
	t.Helper()
	n.mu.Lock()
	defer n.mu.Unlock()
	if !n.saw {
		t.Fatal("retry notice Send was not observed")
	}
	if !n.msg.ProgressNotice {
		t.Fatalf("Send ProgressNotice = false, want true; content %q", n.msg.Content)
	}
	if n.msg.Content != progressNoticeText {
		t.Fatalf("Send content = %q, want %q", n.msg.Content, progressNoticeText)
	}
	if n.sendErr != nil {
		t.Fatalf("Send error = %v, want nil", n.sendErr)
	}
}

var _ channels.Channel = (*retryStopNotice)(nil)

type retryStopEnv struct {
	al         *agent.AgentLoop
	handler    *WSHandler
	provider   *retryStopProvider
	notice     *retryStopNotice
	mgr        *channels.Manager
	srv        *httptest.Server
	conn       *websocket.Conn
	log        *retryStopFrameLog
	cancelRun  context.CancelFunc
	runDone    chan error
	readerDone chan struct{}
}

// startRetryStopTurn wires the production webchat path around the provider
// double. The manager is the bus delegate and the handler is its stream
// fallback. The registered channel and handler.webchatCh are the same
// *webchatChannel; the registration is wrapped only to observe Send's return.
func startRetryStopTurn(t *testing.T) *retryStopEnv {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	home := t.TempDir()
	provider := newRetryStopProvider()
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 18800, DevModeBypass: true},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         home,
				DefaultModel: config.DefaultModel{Model: provider.GetDefaultModel()},
				MaxTokens:    4096,
			},
			List: []config.AgentConfig{{ID: "mia", Home: home}},
		},
	}
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	al := mustAgentLoop(t, cfg, msgBus, provider)

	runCtx, cancelRun := context.WithCancel(context.Background())
	env := &retryStopEnv{
		al: al, provider: provider, cancelRun: cancelRun,
		runDone: make(chan error, 1),
	}
	go func() {
		err := al.Run(runCtx)
		if errors.Is(err, context.Canceled) {
			err = nil
		}
		env.runDone <- err
	}()
	// Registered after mustAgentLoop, so this runs before al.Close and before
	// the temp home is removed.
	t.Cleanup(func() { env.shutdown(t) })

	handler := newWSHandler(msgBus, al, "")
	env.handler = handler
	webchat := newWebchatChannel(handler)
	handler.webchatCh = webchat
	notice := newRetryStopNotice(webchat)
	env.notice = notice
	mgr, err := channels.NewManager(cfg, credentials.SecretBundle{}, msgBus, nil)
	if err != nil {
		t.Fatalf("channels.NewManager: %v", err)
	}
	env.mgr = mgr
	mgr.SetStreamFallback(handler)
	if err := mgr.StartAll(context.Background()); err != nil {
		t.Fatalf("channels.StartAll: %v", err)
	}
	mgr.RegisterChannel("webchat", notice)

	srv := httptest.NewServer(handler)
	env.srv = srv
	conn := dialTestWS(t, srv)
	env.conn = conn
	sendWSAuthFrameDevMode(t, conn)

	env.log = newRetryStopFrameLog()
	env.readerDone = make(chan struct{})
	go func() {
		defer close(env.readerDone)
		readRetryStopSocket(conn, env.log)
	}()
	return env
}

// shutdown joins the socket reader, the agent loop, and the channel manager.
// A timeout or an unexpected error fails the test. context.Canceled from Run
// is the expected stop, not an error.
func (env *retryStopEnv) shutdown(t *testing.T) {
	t.Helper()
	if env.conn != nil {
		_ = env.conn.Close()
	}
	if env.readerDone != nil {
		select {
		case <-env.readerDone:
		case <-time.After(cancelTestTurnStartDeadline):
			t.Errorf("socket reader did not exit within %s", cancelTestTurnStartDeadline)
		}
	}
	if env.srv != nil {
		env.srv.Close()
	}
	if env.handler != nil {
		waited := make(chan struct{})
		go func() {
			env.handler.Wait()
			close(waited)
		}()
		select {
		case <-waited:
		case <-time.After(cancelTestTurnStartDeadline):
			t.Errorf("websocket handler Wait did not return within %s", cancelTestTurnStartDeadline)
		}
	}
	if env.cancelRun != nil {
		env.cancelRun()
	}
	if env.runDone != nil {
		select {
		case err := <-env.runDone:
			if err != nil {
				t.Errorf("agent loop Run: %v", err)
			}
		case <-time.After(cancelTestTurnStartDeadline):
			t.Errorf("agent loop Run did not exit within %s", cancelTestTurnStartDeadline)
		}
	}
	if env.mgr != nil {
		stopped := make(chan error, 1)
		go func() { stopped <- env.mgr.StopAll(context.Background()) }()
		select {
		case err := <-stopped:
			if err != nil {
				t.Errorf("channel manager StopAll: %v", err)
			}
		case <-time.After(cancelTestTurnStartDeadline):
			t.Errorf("channel manager StopAll did not return within %s", cancelTestTurnStartDeadline)
		}
	}
}

func writeRetryStopFrame(t *testing.T, conn *websocket.Conn, frame any) {
	t.Helper()
	data, err := json.Marshal(frame)
	if err != nil {
		t.Fatalf("marshal frame: %v", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
		t.Fatalf("write frame: %v", err)
	}
}

func waitRetryStopSignal(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(cancelTestTurnStartDeadline):
		t.Fatalf("deadlock guard: %s within %s", what, cancelTestTurnStartDeadline)
	}
}

func retryStopProjectionActive(h *WSHandler, sessionID string) bool {
	if h == nil || h.hubs == nil || sessionID == "" {
		return false
	}
	hub := h.hubs.lookup(sessionID)
	if hub == nil {
		return false
	}
	return hubProjActive(hub)
}

func retryStopFrameType(raw []byte) string {
	var head map[string]any
	if err := json.Unmarshal(raw, &head); err != nil {
		return ""
	}
	typ, _ := head["type"].(string)
	return typ
}

func retryStopHasType(typ string) retryStopMatch {
	return func(frames [][]byte) ([]byte, bool) {
		for _, raw := range frames {
			if retryStopFrameType(raw) == typ {
				return raw, true
			}
		}
		return nil, false
	}
}

func retryStopStageOK(stage string) bool {
	switch stage {
	case "graceful", "hard", "detached":
		return true
	default:
		return false
	}
}

func retryStopCancelAck(sessionID string) retryStopMatch {
	return func(frames [][]byte) ([]byte, bool) {
		for _, raw := range frames {
			if retryStopFrameType(raw) != string(generated.WsFrameTypeCancelStage) {
				continue
			}
			var frame generated.CancelStageFrame
			if json.Unmarshal(raw, &frame) != nil {
				continue
			}
			if frame.SessionId == sessionID && retryStopStageOK(frame.Stage) {
				return raw, true
			}
		}
		return nil, false
	}
}

func retryStopStatsTurn(raw []byte, sessionID string) (string, bool) {
	if retryStopFrameType(raw) != string(generated.WsFrameTypeDone) {
		return "", false
	}
	var frame generated.DoneFrame
	if json.Unmarshal(raw, &frame) != nil || frame.SessionId != sessionID || frame.Stats == nil {
		return "", false
	}
	if frame.TurnId == nil || *frame.TurnId == "" {
		return "", false
	}
	return *frame.TurnId, true
}

func retryStopTerminalDone(sessionID, turnID string) retryStopMatch {
	return func(frames [][]byte) ([]byte, bool) {
		for _, raw := range frames {
			if id, ok := retryStopStatsTurn(raw, sessionID); ok && id == turnID {
				return raw, true
			}
		}
		return nil, false
	}
}

func retryStopTokenIs(raw []byte, sessionID, content string) bool {
	if retryStopFrameType(raw) != string(generated.WsFrameTypeToken) {
		return false
	}
	var frame generated.TokenFrame
	if json.Unmarshal(raw, &frame) != nil {
		return false
	}
	return frame.SessionId == sessionID && frame.Content == content
}

func retryStopFollowUpSettled(sessionID, turnID string) retryStopMatch {
	return func(frames [][]byte) ([]byte, bool) {
		sawToken := false
		for _, raw := range frames {
			if retryStopTokenIs(raw, sessionID, retryStopFollowUp) {
				sawToken = true
				continue
			}
			if !sawToken {
				continue
			}
			if id, ok := retryStopStatsTurn(raw, sessionID); ok && id == turnID {
				return raw, true
			}
		}
		return nil, false
	}
}

func retryStopJournal(t *testing.T, h *WSHandler, sessionID string) [][]byte {
	t.Helper()
	if h == nil || h.hubs == nil {
		t.Fatal("session hub registry is nil")
	}
	hub := h.hubs.lookup(sessionID)
	if hub == nil {
		t.Fatalf("no hub for session %s at the Send-return barrier", sessionID)
	}
	hub.mu.Lock()
	defer hub.mu.Unlock()
	out := make([][]byte, 0, len(hub.journal))
	for _, entry := range hub.journal {
		out = append(out, append([]byte(nil), entry.bytes...))
	}
	return out
}

func assertRetryStopJournalQuiet(t *testing.T, frames [][]byte) {
	t.Helper()
	for _, raw := range frames {
		switch retryStopFrameType(raw) {
		case string(generated.WsFrameTypeToken), string(generated.WsFrameTypeDone):
			t.Fatalf("hub journal has a %s frame before Stop: %s", retryStopFrameType(raw), raw)
		}
	}
}

// assertRetryStopDelivery checks the socket log. The cancellation terminal is
// the stats-bearing done whose turn id is the id captured while the retry was
// held. cancel_stage is required and is not ordered against that done:
// interruptGracefully interrupts the turn before SendStageFrame. A typed
// cancel may then publish the catalogue sentence through Send. That fallback
// is allowed and is not the terminal. The follow-up done must carry the other
// live turn id.
func assertRetryStopDelivery(t *testing.T, frames [][]byte, sessionID, canceledTurnID, followTurnID string) {
	t.Helper()
	cancelText := agent.UserMessageForCode(agent.CodeTurnCanceled)
	sawAck := false
	sawTerminal := false
	sawReply := false
	sawReplyDone := false
	for _, raw := range frames {
		switch retryStopFrameType(raw) {
		case string(generated.WsFrameTypeCancelStage):
			if retryStopAcceptAck(t, raw, sessionID) {
				sawAck = true
			}
		case string(generated.WsFrameTypeToken):
			retryStopAcceptToken(t, raw, sessionID, cancelText, sawTerminal, &sawReply)
		case string(generated.WsFrameTypeDone):
			retryStopAcceptDone(t, raw, sessionID, canceledTurnID, followTurnID, &sawTerminal, sawReply, &sawReplyDone)
		}
	}
	if !sawAck {
		t.Fatal("no cancel_stage for the stopped session")
	}
	if !sawTerminal {
		t.Fatalf("cancellation terminal missing: no stats-bearing done for turn %s", canceledTurnID)
	}
	if !sawReply {
		t.Fatal("no follow-up token after the cancellation terminal")
	}
	if !sawReplyDone {
		t.Fatalf("follow-up token had no stats-bearing done for turn %s", followTurnID)
	}
}

func retryStopAcceptAck(t *testing.T, raw []byte, sessionID string) bool {
	t.Helper()
	var frame generated.CancelStageFrame
	if err := json.Unmarshal(raw, &frame); err != nil {
		t.Fatalf("cancel_stage: %v", err)
	}
	if frame.SessionId != sessionID {
		return false
	}
	if !retryStopStageOK(frame.Stage) {
		t.Fatalf("cancel_stage stage = %q, want graceful, hard, or detached", frame.Stage)
	}
	return true
}

func retryStopAcceptToken(
	t *testing.T, raw []byte, sessionID, cancelText string, sawTerminal bool, sawReply *bool,
) {
	t.Helper()
	var frame generated.TokenFrame
	if err := json.Unmarshal(raw, &frame); err != nil {
		t.Fatalf("token: %v", err)
	}
	if frame.Content == progressNoticeText {
		t.Fatalf("retry sentence was delivered as a token: %s", raw)
	}
	if frame.SessionId != sessionID {
		t.Fatalf("token session_id = %q, want %q", frame.SessionId, sessionID)
	}
	if !sawTerminal {
		t.Fatalf("token before the cancellation terminal: %s", raw)
	}
	if frame.Content == cancelText {
		return
	}
	if frame.Content == retryStopFollowUp {
		*sawReply = true
		return
	}
	t.Fatalf("unexpected token %q; want the cancel notice or %q", frame.Content, retryStopFollowUp)
}

func retryStopAcceptDone(
	t *testing.T, raw []byte, sessionID, canceledTurnID, followTurnID string,
	sawTerminal *bool, sawReply bool, sawReplyDone *bool,
) {
	t.Helper()
	var frame generated.DoneFrame
	if err := json.Unmarshal(raw, &frame); err != nil {
		t.Fatalf("done: %v", err)
	}
	if frame.SessionId != sessionID {
		t.Fatalf("done session_id = %q, want %q", frame.SessionId, sessionID)
	}
	if frame.Stats == nil {
		if !*sawTerminal {
			t.Fatalf("stats-less done before the streamer cancellation terminal: %s", raw)
		}
		return
	}
	if frame.TurnId == nil || *frame.TurnId == "" {
		t.Fatalf("stats-bearing done has no turn_id: %s", raw)
	}
	switch *frame.TurnId {
	case canceledTurnID:
		if sawReply {
			t.Fatalf("canceled turn %s completed again after the follow-up token", canceledTurnID)
		}
		*sawTerminal = true
	case followTurnID:
		if !sawReply || !*sawTerminal {
			t.Fatalf("follow-up turn %s completed before its answer and the cancellation terminal", followTurnID)
		}
		*sawReplyDone = true
	default:
		t.Fatalf("stats-bearing done turn_id = %q, want %q or %q", *frame.TurnId, canceledTurnID, followTurnID)
	}
}

func retryStopLiveTurnID(t *testing.T, al *agent.AgentLoop, sessionID, blocked string) string {
	t.Helper()
	turnID, _, _, running := al.ActiveForegroundTurnInfo(sessionID)
	if !running || turnID == "" {
		t.Fatalf("%s", blocked)
	}
	return turnID
}

// TestWebchatRetryThenStop_CancelsWithoutFalseCompletion is the combined
// sequence from the I1 coverage gap. Only the provider is fake. Stop is a
// real cancel frame. The terminal is the streamer's done for the turn id
// captured while that retry was held, not a frame built here.
func TestWebchatRetryThenStop_CancelsWithoutFalseCompletion(t *testing.T) {
	env := startRetryStopTurn(t)
	agentID := "mia"
	writeRetryStopFrame(t, env.conn, generated.MessageFrame{
		Type:    string(generated.WsFrameTypeMessage),
		Content: "hello",
		AgentId: &agentID,
	})

	startedRaw := env.log.waitMatch(t, "session_started", retryStopHasType(string(generated.WsFrameTypeSessionStarted)))
	var started generated.SessionStartedFrame
	if err := json.Unmarshal(startedRaw, &started); err != nil {
		t.Fatalf("session_started: %v", err)
	}
	if started.SessionId == "" {
		t.Fatal("session_started session_id is empty")
	}
	sessionID := started.SessionId

	waitRetryStopSignal(t, env.provider.firstEntered, "provider never entered the first call")
	if !env.provider.firstCallStreamed() {
		t.Fatal("BLOCKED: turn used Chat, not ChatStream — webchat streamer was not attached")
	}
	waitRetryStopSignal(t, env.provider.retryEntered, "provider never entered the blocked retry call")
	waitRetryStopSignal(t, env.notice.returned, "retry notice Send did not return")
	env.notice.assertRetryNotice(t)
	canceledTurnID := retryStopLiveTurnID(t, env.al, sessionID, "retry turn id is empty while the retry call is held")
	assertRetryStopJournalQuiet(t, retryStopJournal(t, env.handler, sessionID))
	if retryStopProjectionActive(env.handler, sessionID) {
		t.Fatal("hub projection is active before Stop; a token was published for the retry notice")
	}

	writeRetryStopFrame(t, env.conn, generated.CancelFrame{
		Type:      string(generated.WsFrameTypeCancel),
		SessionId: sessionID,
	})
	waitRetryStopSignal(t, env.provider.retryExited, "Stop did not cancel the blocked retry call")
	env.log.waitMatch(t, "cancellation terminal done frame", retryStopTerminalDone(sessionID, canceledTurnID))
	if _, _, _, running := env.al.ActiveForegroundTurnInfo(sessionID); running {
		t.Fatal("session still running after the cancellation terminal")
	}

	writeRetryStopFrame(t, env.conn, generated.MessageFrame{
		Type:      string(generated.WsFrameTypeMessage),
		Content:   "continue",
		AgentId:   &agentID,
		SessionId: &sessionID,
	})
	waitRetryStopSignal(t, env.provider.followUpEntered, "provider never entered the follow-up call")
	followTurnID := retryStopLiveTurnID(t, env.al, sessionID, "follow-up turn id is empty while that call is held")
	if followTurnID == canceledTurnID {
		t.Fatalf("follow-up turn id = %q, same as the canceled turn", followTurnID)
	}
	close(env.provider.followUpRelease)
	env.log.waitMatch(t, "follow-up done frame", retryStopFollowUpSettled(sessionID, followTurnID))
	assertRetryStopDelivery(t, env.log.snapshot(), sessionID, canceledTurnID, followTurnID)
	if retryStopProjectionActive(env.handler, sessionID) {
		t.Fatal("hub projection still active after the next turn finished")
	}
}

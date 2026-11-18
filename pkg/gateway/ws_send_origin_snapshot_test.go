package gateway

// Spec r9 §4.3.4 R1–R6 / §6.1 SV-19. The provider is the only scripted
// boundary; completion, snapshots, hub publication and socket queues are real.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type soSnapshotRig struct {
	s         *sendOriginSrv
	conn      *websocket.Conn
	wc        *wsConn
	sid       string
	turnID    string
	release   func()
	finalized <-chan struct{}
}

// Observe, do not replace, production Finalize. Embedding the concrete real
// streamer also preserves every optional setter interface (turn id/stats/etc.)
// that would be hidden by embedding only bus.Streamer.
type soCompletionDelegate struct {
	h    *WSHandler
	done chan struct{}
}

type soCompletionStreamer struct {
	*wsStreamer
	done chan struct{}
	once sync.Once
}

func (d *soCompletionDelegate) GetStreamer(ctx context.Context, channel, chatID, sid string) (bus.Streamer, bool) {
	streamer, ok := d.h.GetStreamer(ctx, channel, chatID, sid)
	if !ok {
		return streamer, false
	}
	actualStreamer, concrete := streamer.(*wsStreamer)
	if !concrete {
		panic("SV-19 instrument must retain the real concrete wsStreamer")
	}
	return &soCompletionStreamer{wsStreamer: actualStreamer, done: d.done}, true
}

func (s *soCompletionStreamer) Finalize(ctx context.Context, content string) error {
	err := s.wsStreamer.Finalize(ctx, content)
	s.once.Do(func() { close(s.done) }) // only after production done publication returns
	return err
}

func soPausedSnapshotRig(t *testing.T, withSuffix bool) *soSnapshotRig {
	t.Helper()
	tokens := []string{"SV19 before refusal"}
	if withSuffix {
		tokens = append(tokens, "; still streaming after refusal")
	}
	provider := newControllableStreamProvider(providerRound{tokens: tokens})
	paused, resume := provider.pauseAfter(0, 0)
	s := newSendOriginSrv(t, provider)
	finalized := make(chan struct{})
	s.msgBus.SetStreamDelegate(&soCompletionDelegate{h: s.handler, done: finalized})
	var once sync.Once
	release := func() { once.Do(func() { close(resume) }) }
	t.Cleanup(release) // unblock even if a later assertion fails
	conn := s.dialBrowserConn(t)
	drainAdmissionOneShot(t, conn)
	sid := s.mintLiveChat(t, conn, soKeyB, "SV-19 live fixture")
	select {
	case <-paused:
	case <-time.After(lifecycleEW):
		t.Fatal("SETUP: the real turn must reach its provider pause")
	}
	firstToken := readFrameOfType(t, conn, "token", lifecycleEW)
	require.Equal(t, sid, firstToken.SessionID)
	waitForLifecycleState(t, s.lc, sid, session.LifecycleRunning)
	turnID, _, _, active := s.al.ActiveForegroundTurnInfo(sid)
	require.True(t, active, "SETUP: real turn is live before refusal")
	require.NotEmpty(t, turnID)
	return &soSnapshotRig{s: s, conn: conn, wc: soRequireBound(t, s, conn, sid), sid: sid, turnID: turnID, release: release, finalized: finalized}
}

func (r *soSnapshotRig) requireSameLiveTurn(t *testing.T) {
	t.Helper()
	turnID, _, _, active := r.s.al.ActiveForegroundTurnInfo(r.sid)
	require.True(t, active, "SV-19: refusal must not cancel the live turn")
	require.Equal(t, r.turnID, turnID, "SV-19: a replacement turn must not conceal cancellation")
}

func (r *soSnapshotRig) requireSnapshotTurn(t *testing.T, frames []soFrame) {
	t.Helper()
	st := soLastSessionState(t, frames)
	require.NotNil(t, st, "SV-19: refusal must be followed by the target snapshot")
	require.Equal(t, r.sid, st["session_id"])
	active, ok := st["active_turn"].(map[string]any)
	require.True(t, ok, "SV-19: snapshot must carry the genuinely live turn")
	require.Equal(t, r.turnID, active["turn_id"])
}

func (r *soSnapshotRig) requireFreshToken(t *testing.T) {
	t.Helper()
	r.requireSameLiveTurn(t)
	r.release()
	frames := soWindow(t, r.conn, lifecycleEW, "done")
	var chunks []string
	for _, f := range frames {
		if f.Type == "token" {
			var token generated.TokenFrame
			require.NoError(t, json.Unmarshal(f.Raw, &token))
			require.Equal(t, r.sid, token.SessionId)
			require.NotNil(t, token.TurnId, "live-positive token must identify the same real turn")
			require.Equal(t, r.turnID, *token.TurnId)
			chunks = append(chunks, token.Content)
		}
	}
	require.Equal(t, []string{"; still streaming after refusal"}, chunks,
		"SV-19a live-positive: same turn must produce a fresh post-refusal wire chunk, not a cancel_stage proxy")
	require.Contains(t, soTypes(frames), "done", "instrument: resumed real provider must complete")
}

// C-4 / SV-3 / SV-19: a refused cancel is not acted on. A graceful stop
// can leave the same turn active and still emit tokens and done; inspect the
// settled lifecycle and durable stop ledger, not just those wire frames.
func (r *soSnapshotRig) requireCompletedWithoutStop(t *testing.T) {
	t.Helper()
	var rec *session.LifecycleRecord
	var err error
	require.Eventually(t, func() bool {
		rec, err = r.s.lc.Load(r.sid)
		return err != nil || (rec != nil && (rec.Terminal() || rec.State == session.LifecycleStopped))
	}, lifecycleEW, 20*time.Millisecond, "SV-19: released turn must settle after done publication")
	require.NoError(t, err, "SV-19: read the refused cancel's session lifecycle")
	require.NotNil(t, rec, "SV-19: completed turn must have a lifecycle record")
	assert.Equal(t, session.LifecycleCompleted, rec.State, "SV-19: refused cancel must let the live turn complete, not stop")
	assert.Nil(t, rec.StopNote, "SV-19: refused cancel must not record a stop note")
	effects, err := r.s.lc.AcceptedStopEffects(r.sid)
	require.NoError(t, err, "SV-19: read accepted stop controls for the refused cancel's session")
	assert.Len(t, effects, 0, "SV-19: refused cancel must not record an accepted stop control")
}

func soSV19A(t *testing.T) {
	r := soPausedSnapshotRig(t, true)
	soSend(t, r.conn, generated.CancelFrame{Type: "cancel", SessionId: r.sid})
	frames := soWindow(t, r.conn, soRequesterWindow)
	require.Equal(t, []string{"error", "session_state"}, soTypes(frames), "SV-19a: exact refusal then snapshot order")
	errs := soErrors(t, frames)
	require.Len(t, errs, 1)
	require.Equal(t, soOutdatedStop, errs[0].Message)
	require.NotNil(t, errs[0].SessionId)
	require.Equal(t, r.sid, *errs[0].SessionId, "SV-19a: target value, not string-vs-pointer")
	require.NotNil(t, errs[0].ClientMessageId)
	require.Regexp(t, `^outdated-[A-Za-z0-9_-]{12}$`, *errs[0].ClientMessageId)
	r.requireSnapshotTurn(t, frames)
	r.requireFreshToken(t)
	r.requireCompletedWithoutStop(t)
	soLiveFrameDelivered(t, r.s, r.conn, r.sid, "SV-19a cleanup")
}

func soSV19B(t *testing.T) {
	r := soPausedSnapshotRig(t, true)
	soSend(t, r.conn, generated.RedirectFrame{Type: "redirect", SessionId: r.sid, Instruction: "old tab redirect"})
	frames := soWindow(t, r.conn, soRequesterWindow)
	require.Equal(t, []string{"error", "session_state"}, soTypes(frames), "SV-19b: exact refusal then snapshot order")
	errs := soErrors(t, frames)
	require.Len(t, errs, 1)
	require.Equal(t, soOutdatedRedir, errs[0].Message, "SV-19b: OUTDATED_REDIRECT is exact")
	r.requireSnapshotTurn(t, frames)
	r.requireFreshToken(t)
	soLiveFrameDelivered(t, r.s, r.conn, r.sid, "SV-19b cleanup")
}

func soSV19C(t *testing.T) {
	r := soPausedSnapshotRig(t, true)
	// D4: create A without rebinding the requesting connection away from B.
	a, err := r.s.al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	soRequireBound(t, r.s, r.conn, r.sid)
	soSend(t, r.conn, generated.CancelFrame{Type: "cancel", SessionId: r.sid, Origin: soChatOrigin(a.ID), ClientFrameId: strPtr("so-19c1")})
	frames := soWindow(t, r.conn, soRequesterWindow)
	require.Equal(t, []string{"send_refused", "session_state"}, soTypes(frames), "SV-19c: refusal, never command receipt, then snapshot")
	refused := soRefusedFrame(t, frames, "SV-19c")
	require.Equal(t, "wrong_chat", refused.Reason)
	require.Equal(t, strPtr("so-19c1"), refused.ClientFrameId)
	r.requireSnapshotTurn(t, frames)
	r.requireFreshToken(t)
	r.requireCompletedWithoutStop(t)
	soLiveFrameDelivered(t, r.s, r.conn, r.sid, "SV-19c cleanup")
}

func soSV19D(t *testing.T) {
	s := newSendOriginSrv(t, &restMockProvider{})
	conn := s.dialBrowserConn(t)
	drainAdmissionOneShot(t, conn)
	a := s.mintChat(t, conn, soKeyA, "SV-19d other")
	b := s.mintChat(t, conn, soKeyB, "SV-19d finished turn") // B is last; its own done is drained
	soRequireBound(t, s, conn, b)
	soSend(t, conn, generated.CancelFrame{Type: "cancel", SessionId: b, Origin: soChatOrigin(a), ClientFrameId: strPtr("so-19d1")})
	frames := soWindow(t, conn, soRequesterWindow)
	require.Equal(t, []string{"send_refused", "session_state"}, soTypes(frames))
	soRefusedFrame(t, frames, "SV-19d")
	st := soLastSessionState(t, frames)
	require.NotNil(t, st, "SV-19d: the snapshot must follow")
	require.Nil(t, st["active_turn"], "SV-19d: a finished turn must NOT appear as active_turn (snapshot: %v)", st)
	soLiveFrameDelivered(t, s, conn, b, "SV-19d cleanup")
}

func soSV19G(t *testing.T) {
	r := soPausedSnapshotRig(t, true)
	conn := r.s.dialBrowserConn(t)
	drainAdmissionOneShot(t, conn)
	wc := soServerConn(t, r.s, conn)
	r.s.handler.mu.Lock()
	initialBinding := wc.boundHub
	r.s.handler.mu.Unlock()
	require.Nil(t, initialBinding, "SETUP: the requester must really be unbound")
	// §4.3.1: B/B is accepted; SV-19g must send a VALID mismatching origin.
	soSend(t, conn, generated.CancelFrame{Type: "cancel", SessionId: r.sid, Origin: soChatOrigin("SV19-other-chat"), ClientFrameId: strPtr("so-19g1")})
	frames := soWindow(t, conn, soRequesterWindow)
	require.Equal(t, []string{"send_refused"}, soTypes(frames), "SV-19g: refusal only, zero session_state on the unbound requester")
	refused := soRefusedFrame(t, frames, "SV-19g")
	require.Equal(t, "wrong_chat", refused.Reason)
	require.Equal(t, strPtr("so-19g1"), refused.ClientFrameId)
	r.s.handler.mu.Lock()
	afterBinding := wc.boundHub
	r.s.handler.mu.Unlock()
	require.Nil(t, afterBinding, "SV-19g: refusal must not bind the requester")
	require.False(t, soHolding(t, r.s, conn), "SV-19g cleanup: requester holding must be false under qmu")
	// B's own connection is the live-positive control; the unbound requester
	// cannot receive B's hub frames without violating the binding invariant.
	r.requireFreshToken(t)
	r.requireCompletedWithoutStop(t)
	soLiveFrameDelivered(t, r.s, r.conn, r.sid, "SV-19g bound control cleanup")
	probe := []byte(`{"type":"pong"}`)
	require.True(t, wc.enqueue(probe), "SV-19g: requester outbound queue remains usable")
	require.JSONEq(t, string(probe), string(soReadNext(t, conn, lifecycleEW)))
	require.False(t, soHolding(t, r.s, conn))
}

func soSV19H(t *testing.T) {
	r := soPausedSnapshotRig(t, true)
	for _, frameType := range []string{"cancel", "redirect"} {
		var frames []soFrame
		// Both malformed classes run on the SAME still-live connection. No
		// timeout may silently disable the second half of the negative check.
		soSend(t, r.conn, soRawCommandForSnapshot(frameType, r.sid, `"so-19h1"`, `null`))
		frames = soWindow(t, r.conn, soRequesterWindow)
		refused := soRefusedFrame(t, frames, "SV-19h P1a "+frameType)
		require.Equal(t, "origin_malformed", refused.Reason)
		require.Equal(t, []string{"send_refused"}, soTypes(frames), "SV-19h P1a: zero session_state and zero effects")
		soSend(t, r.conn, soRawCommandForSnapshot(frameType, r.sid, `""`, soOriginChat("so-other")))
		frames = soWindow(t, r.conn, soRequesterWindow)
		refused = soRefusedFrame(t, frames, "SV-19h P1b "+frameType)
		require.Equal(t, "frame_malformed", refused.Reason)
		require.Equal(t, []string{"send_refused"}, soTypes(frames), "SV-19h P1b: zero session_state and zero effects")
	}
	require.False(t, soHolding(t, r.s, r.conn), "SV-19h cleanup: holding must be false under qmu")
	r.requireFreshToken(t)
	r.requireCompletedWithoutStop(t)
	soLiveFrameDelivered(t, r.s, r.conn, r.sid, "SV-19h cleanup")
}

func soRawCommandForSnapshot(frameType, sid, corr, origin string) []byte {
	if frameType == "cancel" {
		return soRawCancel(soValidJSON(sid), origin, corr)
	}
	return soRawRedirect(soValidJSON(sid), origin, corr)
}

func soSV19Control(t *testing.T) {
	s := newSendOriginSrv(t, &restMockProvider{})
	conn := s.dialBrowserConn(t)
	drainAdmissionOneShot(t, conn)
	b := s.mintChat(t, conn, soKeyB, "SV-19 control idle chat")
	soSend(t, conn, generated.CancelFrame{Type: "cancel", SessionId: b, Origin: soChatOrigin(b), ClientFrameId: strPtr("so-19ctl")})
	frames := soWindow(t, conn, soRequesterWindow)
	require.Contains(t, soTypes(frames), "command_received", "SV-19 control: accepted command must actually run")
	for _, f := range frames {
		require.NotEqual(t, "session_state", f.Type, "SV-19 control: an ACCEPTED cancel gets no refusal snapshot (got %s)", f.Raw)
	}
	soLiveFrameDelivered(t, s, conn, b, "SV-19 accepted control cleanup")
}

func soSnapshotHooks(t *testing.T, before, after func(string), build func(*WSHandler, *wsConn, string) []byte) {
	t.Helper()
	oldBefore, oldAfter, oldBuild := refusalSnapshotBeforeHoldHook, refusalSnapshotAfterSampleHook, refusalSnapshotBuild
	refusalSnapshotBeforeHoldHook, refusalSnapshotAfterSampleHook = before, after
	if build != nil {
		refusalSnapshotBuild = build
	}
	t.Cleanup(func() {
		refusalSnapshotBeforeHoldHook, refusalSnapshotAfterSampleHook, refusalSnapshotBuild = oldBefore, oldAfter, oldBuild
	})
}

func soCompletePausedTurn(t *testing.T, r *soSnapshotRig) {
	t.Helper()
	r.release()
	// Wait at the actual production Finalize-return boundary, not a lifecycle
	// poll or the unrelated active-request intake counter (which does not join
	// an ordinary chat turn's runTurn).
	select {
	case <-r.finalized:
	case <-time.After(lifecycleEW):
		t.Fatal("SV-19 decisive hook: real completion and done publication did not return")
	}
	turnID, agentID, startedAt, active := r.s.al.ActiveForegroundTurnInfo(r.sid)
	require.False(t, active, "SV-19: production completion removes active turn before publishing done (turn=%s agent=%s started=%s)", turnID, agentID, startedAt)
}

func soSV19E(t *testing.T) { soDecisiveSnapshotOrder(t, false) }
func soSV19F(t *testing.T) { soDecisiveSnapshotOrder(t, true) }

func soDecisiveSnapshotOrder(t *testing.T, beforeHold bool) {
	t.Helper()
	r := soPausedSnapshotRig(t, false)
	var hookCalls atomicSnapshotCalls
	hook := func(sid string) {
		if sid == r.sid {
			hookCalls.add()
			soCompletePausedTurn(t, r)
		}
	}
	if beforeHold {
		soSnapshotHooks(t, hook, nil, nil)
	} else {
		soSnapshotHooks(t, nil, hook, nil)
	}
	soSend(t, r.conn, generated.CancelFrame{Type: "cancel", SessionId: r.sid})
	frames := soWindow(t, r.conn, soRequesterWindow)
	require.Equal(t, 1, hookCalls.load(), "instrument: decisive hook must actually execute")
	want := []string{"error", "session_state", "done"}
	if beforeHold {
		want = []string{"error", "done", "session_state"}
	}
	require.Equal(t, want, soTypes(frames), "SV-19 e/f: exact wire order, nothing between refusal/snapshot/done")
	errs := soErrors(t, frames)
	require.Len(t, errs, 1)
	require.Equal(t, soOutdatedStop, errs[0].Message)
	require.Equal(t, &r.sid, errs[0].SessionId)
	st := soLastSessionState(t, frames)
	require.NotNil(t, st)
	if beforeHold {
		require.Nil(t, st["active_turn"], "SV-19f: snapshot sampled after completed done has no active_turn")
	} else {
		r.requireSnapshotTurn(t, frames)
	}
	for _, f := range frames {
		if f.Type == "done" {
			var done generated.DoneFrame
			require.NoError(t, json.Unmarshal(f.Raw, &done))
			require.Equal(t, r.sid, done.SessionId)
			require.Equal(t, &r.turnID, done.TurnId)
		}
	}
	r.requireCompletedWithoutStop(t)
	soSaveSnapshotFixture(t, frames, beforeHold)
	soLiveFrameDelivered(t, r.s, r.conn, r.sid, "SV-19 decisive cleanup")
}

// Atomic because the read-loop hook and test goroutine communicate over a
// socket; the counter's race-freedom must not depend on a network side effect.
type atomicSnapshotCalls struct {
	mu sync.Mutex
	n  int
}

func (a *atomicSnapshotCalls) add()      { a.mu.Lock(); a.n++; a.mu.Unlock() }
func (a *atomicSnapshotCalls) load() int { a.mu.Lock(); defer a.mu.Unlock(); return a.n }

func soSaveSnapshotFixture(t *testing.T, frames []soFrame, beforeHold bool) {
	t.Helper()
	name := "sv19e-after-sample.jsonl"
	if beforeHold {
		name = "sv19f-before-hold.jsonl"
	}
	var b strings.Builder
	for _, frame := range frames {
		b.Write(frame.Raw)
		b.WriteByte('\n')
	}
	// Always expose exact bytes in the test receipt; an opt-in output directory
	// also persists them for the independent T-OLD-4b/BR-25f consumer.
	t.Logf("T-OLD-4b/BR-25f actual wire fixture %s:\n%s", name, b.String())
	if dir := os.Getenv("OMNIPUS_SV19_FIXTURE_DIR"); dir != "" {
		require.NoError(t, os.MkdirAll(dir, 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(b.String()), 0o600))
	}
}

func soSV19J(t *testing.T) {
	r := soPausedSnapshotRig(t, true)
	var calls atomicSnapshotCalls
	soSnapshotHooks(t, nil, nil, func(h *WSHandler, wc *wsConn, sid string) []byte {
		if sid != r.sid {
			return h.sessionStateBytes(wc, sid)
		}
		calls.add()
		require.Same(t, r.wc, wc, "instrument: forcing builder only for exact requester")
		return nil
	})
	soSend(t, r.conn, generated.CancelFrame{Type: "cancel", SessionId: r.sid})
	frames := soWindow(t, r.conn, soRequesterWindow)
	require.Equal(t, 1, calls.load(), "instrument: nil-builder path must execute")
	require.Equal(t, []string{"error"}, soTypes(frames), "SV-19j: nil snapshot builder still sends refusal, no snapshot")
	require.Equal(t, soOutdatedStop, soErrors(t, frames)[0].Message)
	require.False(t, soHolding(t, r.s, r.conn), "SV-19j: holding false under qmu after nil builder")
	r.requireFreshToken(t)
	r.requireCompletedWithoutStop(t)
	soLiveFrameDelivered(t, r.s, r.conn, r.sid, "SV-19j cleanup")
}

func soSV19K(t *testing.T) {
	r := soPausedSnapshotRig(t, false)
	var calls atomicSnapshotCalls
	soSnapshotHooks(t, nil, func(sid string) {
		if sid != r.sid {
			return
		}
		calls.add()
		r.wc.qmu.Lock()
		wasHolding := r.wc.holding
		// Closing without overflowLocked is intentional: overflowLocked itself
		// clears holding and would mask a missing R6 release. This is the real
		// direct() qClosed-failure condition, with holding still true.
		r.wc.qClosed = true
		r.wc.qmu.Unlock()
		require.True(t, wasHolding, "instrument: R3 is active before closing R5's queue")
	}, nil)
	soSend(t, r.conn, generated.CancelFrame{Type: "cancel", SessionId: r.sid})
	// The next ping cannot run before the refusal handler returns; its pong
	// is refused by the closed queue. Observe cleanup directly under qmu below.
	soSend(t, r.conn, generated.PingFrame{Type: "ping"})
	require.Eventually(t, func() bool {
		r.wc.qmu.Lock()
		defer r.wc.qmu.Unlock()
		return calls.load() == 1 && !r.wc.holding
	}, lifecycleEW, time.Millisecond, "SV-19k: after direct fails, R6 must clear holding under qmu")
	require.Equal(t, 1, calls.load(), "instrument: closing hook must execute")
	r.wc.qmu.Lock()
	closed, holding := r.wc.qClosed, r.wc.holding
	r.wc.qmu.Unlock()
	require.True(t, closed, "instrument: R5's real queue failure was forced")
	require.False(t, holding, "SV-19k: holding false under qmu, not inferred from empty held bytes")
	// This queue is deliberately closed, so observe production Finalize/done
	// publication returning instead of trying to read done on the dead socket.
	soCompletePausedTurn(t, r)
	r.requireCompletedWithoutStop(t)
}

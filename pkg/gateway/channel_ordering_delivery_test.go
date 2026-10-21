package gateway

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/channels"
)

// DESIGN §3 progress/cache/replay, §5 T3; R6-A01, T2-R6-S2, CHECK R6-F2.
// Real store, emitter, sync cache tap, forwarder, replay and outbound queue.
// Attempt ids and event sequences come from production entry points, not a
// fabricated event whose request/session ids accidentally imply freshness.
func TestPairingDelivery_CheckAndEnqueueOrdersWithReplay(t *testing.T) {
	t.Run("held old live cannot follow newer-attempt replay", t3HeldLiveAfterReplay)
	t.Run("held same-attempt QR cannot follow its newer rotation", t3HeldRotation)
	t.Run("held replay cannot follow newer live", t3HeldReplayAfterLive)
	t.Run("held replay cannot resurrect terminal unowned QR", t3HeldReplayAfterTerminal)
	t.Run("late subscribers see B even after a late A emission", t3LateSubscribeControl)
	t.Run("A then B is a legal final-enqueue ordering", t3LegalEnqueueOrder)
	t.Run("final-guard contention needs acquisition acknowledgement", func(t *testing.T) {
		t.Fatal("BLOCKED: final ordering guard and contended-acquisition acknowledgement are not implemented on this baseline — required by DESIGN §5 T3; never await B completion while A may hold that guard")
	})
}

type t3Delivery struct {
	t      *testing.T
	f      *wu6Fixture
	store  *channels.PairingAttempts
	h      *WSHandler
	wc     *wsConn
	frames chan []byte
	owner  channels.PairingOwner

	releases []func()
	stops    []func()
	joins    []<-chan struct{}
	finished sync.Once
}

func newT3Delivery(t *testing.T) *t3Delivery {
	t.Helper()
	f, store, _ := orderingWriterFixture(t)
	wc, output := makeForwarderTestConn(64)
	h := makeMinimalHandler()
	h.agentLoop = f.al
	s := &t3Delivery{t: t, f: f, store: store, h: h, wc: wc, frames: output,
		owner: channels.PairingOwner{SessionID: orderingChat(t, f), RequestID: "ordering-shared-request"}}
	f.al.SetEventSyncTap(h.hubSyncTap)
	t.Cleanup(s.finish)
	return s
}

// finish is also the failure-path cleanup: release first, unsubscribe/drain
// actual forwarders, join EVERY participant, and only then reset both hooks.
func (s *t3Delivery) finish() {
	s.finished.Do(func() {
		for _, release := range s.releases {
			release()
		}
		for _, stop := range s.stops {
			stop()
		}
		for _, done := range s.joins {
			t3Await(s.t, done, "participant completion")
		}
		s.f.al.SetEventSyncTap(nil)
		pairingDeliveryStageHookMu.Lock()
		pairingDeliveryAfterCheckHook = nil
		pairingDeliveryBeforeEnqueueHook = nil
		pairingDeliveryStageHookMu.Unlock()
		s.wc.close()
	})
}

func (s *t3Delivery) hooks(after func(string, *wsConn, string), before func(*wsConn, []byte)) {
	s.t.Helper()
	pairingDeliveryStageHookMu.Lock()
	afterWasNil := pairingDeliveryAfterCheckHook == nil
	beforeWasNil := pairingDeliveryBeforeEnqueueHook == nil
	pairingDeliveryAfterCheckHook = after
	pairingDeliveryBeforeEnqueueHook = before
	pairingDeliveryStageHookMu.Unlock()
	require.True(s.t, afterWasNil && beforeWasNil, "instrument: hook registry starts isolated")
}

func (s *t3Delivery) goWork(work func()) <-chan struct{} {
	done := make(chan struct{})
	s.joins = append(s.joins, done)
	go func() { defer close(done); work() }()
	return done
}

func (s *t3Delivery) forward() {
	sub := s.f.al.SubscribeEvents(64)
	done := make(chan struct{})
	s.joins = append(s.joins, done)
	s.stops = append(s.stops, func() { s.f.al.UnsubscribeEvents(sub.ID) })
	go s.h.eventForwarder(s.wc, s.owner.SessionID, sub, done)
}

// The three-second bounds diagnose a broken instrument/join only. No elapsed
// duration or absence during a sleep is a behavior oracle.
func t3Await(t *testing.T, ready <-chan struct{}, stage string) {
	t.Helper()
	select {
	case <-ready:
	case <-time.After(3 * time.Second):
		t.Fatalf("HARNESS: %s was not acknowledged; not assertion-level RED", stage)
	}
}

func (s *t3Delivery) gate() (hold func(), release func(), entered <-chan struct{}) {
	reached, resume := make(chan struct{}), make(chan struct{})
	var holdOnce, releaseOnce sync.Once
	hold = func() { holdOnce.Do(func() { close(reached); <-resume }) }
	release = func() { releaseOnce.Do(func() { close(resume) }) }
	s.releases = append(s.releases, release)
	return hold, release, reached
}

func (s *t3Delivery) holdAfter(path string) (func(), <-chan struct{}) {
	hold, release, entered := s.gate()
	s.hooks(func(gotPath string, wc *wsConn, channel string) {
		if gotPath == path && wc == s.wc && channel == "whatsapp" {
			hold()
		}
	}, nil)
	return release, entered
}

func (s *t3Delivery) start(kind channels.PairingAdmission) channels.PairingAttempt {
	s.t.Helper()
	var id string
	var err error
	switch kind {
	case channels.PairingAdmissionConnect:
		id, err = s.f.al.AdmitConnectPairing("whatsapp", s.owner.RequestID, s.owner.SessionID)
	case channels.PairingAdmissionRetry:
		id, err = s.f.al.AdmitRetryPairing("whatsapp", s.owner.RequestID, s.owner.SessionID)
	case channels.PairingAdmissionEnable:
		id, err = s.f.al.AdmitEnablePairing("whatsapp")
	default:
		s.t.Fatalf("instrument: unsupported admission %q", kind)
	}
	require.NoError(s.t, err)
	require.True(s.t, s.f.al.CommitPairing("whatsapp", id))
	rec, err := s.store.Activate("whatsapp", id, func(string) func(context.Context) error { return nil })
	require.NoError(s.t, err)
	if kind == channels.PairingAdmissionEnable {
		require.Nil(s.t, rec.Owner)
	} else {
		require.Equal(s.t, &s.owner, rec.Owner)
	}
	return rec
}

func (s *t3Delivery) successor(a channels.PairingAttempt) channels.PairingAttempt {
	s.t.Helper()
	_, ended := s.store.Complete("whatsapp", a.AttemptID, channels.PairingOutcomeTimeout)
	require.True(s.t, ended)
	b := s.start(channels.PairingAdmissionRetry)
	require.NotEqual(s.t, a.AttemptID, b.AttemptID, "same public identity, genuinely different attempt")
	require.Equal(s.t, a.Owner, b.Owner)
	return b
}

func (s *t3Delivery) code(a channels.PairingAttempt, value string) {
	s.f.al.EmitWhatsAppPairingAttempt("whatsapp", a.AttemptID, channels.PairingStatusCode, value, "")
}

func t3Code(value string, owner *channels.PairingOwner) generated.WhatsAppPairingFrame {
	frame := generated.WhatsAppPairingFrame{Type: "whatsapp_pairing", ChannelId: "whatsapp", Status: "code", Qr: &value}
	if owner != nil {
		rid, sid := owner.RequestID, owner.SessionID
		frame.RequestId, frame.SessionId = &rid, &sid
	}
	return frame
}

func t3Decode(t *testing.T, raw []byte) generated.WhatsAppPairingFrame {
	t.Helper()
	var frame generated.WhatsAppPairingFrame
	require.NoError(t, json.Unmarshal(raw, &frame))
	require.Equal(t, "whatsapp_pairing", frame.Type, "the real queue carries a generated pairing frame")
	return frame
}

func t3Take(t *testing.T, output <-chan []byte) generated.WhatsAppPairingFrame {
	t.Helper()
	select {
	case raw := <-output:
		return t3Decode(t, raw)
	case <-time.After(3 * time.Second):
		t.Fatal("HARNESS: required actual output did not arrive; not assertion-level RED")
		return generated.WhatsAppPairingFrame{}
	}
}

func t3Drain(t *testing.T, output <-chan []byte) []generated.WhatsAppPairingFrame {
	t.Helper()
	var frames []generated.WhatsAppPairingFrame
	for {
		select {
		case raw := <-output:
			frames = append(frames, t3Decode(t, raw))
		default:
			return frames
		}
	}
}

func t3OnlyCurrent(t *testing.T, first generated.WhatsAppPairingFrame, later []generated.WhatsAppPairingFrame, want generated.WhatsAppPairingFrame) {
	t.Helper()
	require.Equal(t, want, first, "positive instrument: B actually reached the real outbound queue before A resumes")
	// Equal current frames from replay/live may be duplicated; the contract
	// specifies monotonic progress, not exactly-once delivery of equal (g,s).
	for i, frame := range later {
		assert.Equal(t, want, frame, "B→A is forbidden: queued frame %d regressed after the observed current output", i+1)
	}
}

func t3HeldLiveAfterReplay(t *testing.T) {
	s := newT3Delivery(t)
	a := s.start(channels.PairingAdmissionConnect)
	s.h.subscribePairingInterest(s.wc, "whatsapp", true)
	release, entered := s.holdAfter("live")
	s.forward()
	s.code(a, "QR-A")
	t3Await(t, entered, "A after live freshness check")
	b := s.successor(a)
	s.code(b, "QR-B")
	s.h.subscribePairingInterest(s.wc, "whatsapp", true)
	first := t3Take(t, s.frames)
	release()
	s.finish()
	t3OnlyCurrent(t, first, t3Drain(t, s.frames), t3Code("QR-B", &s.owner))
}

func t3HeldRotation(t *testing.T) {
	s := newT3Delivery(t)
	a := s.start(channels.PairingAdmissionConnect)
	s.h.subscribePairingInterest(s.wc, "whatsapp", true)
	release, entered := s.holdAfter("live")
	s.forward()
	s.code(a, "QR-rotation-1")
	t3Await(t, entered, "first QR after live freshness check")
	s.code(a, "QR-rotation-2")
	s.h.subscribePairingInterest(s.wc, "whatsapp", true)
	first := t3Take(t, s.frames)
	release()
	s.finish()
	require.True(t, s.store.IsCurrent("whatsapp", a.AttemptID), "rotation did not introduce another attempt")
	t3OnlyCurrent(t, first, t3Drain(t, s.frames), t3Code("QR-rotation-2", &s.owner))
}

func t3HeldReplayAfterLive(t *testing.T) {
	s := newT3Delivery(t)
	a := s.start(channels.PairingAdmissionConnect)
	s.code(a, "QR-A")
	release, entered := s.holdAfter("replay")
	s.goWork(func() { s.h.subscribePairingInterest(s.wc, "whatsapp", true) })
	t3Await(t, entered, "A cached replay selection")
	s.forward() // only events accepted after this connection arrived
	b := s.successor(a)
	s.code(b, "QR-B")
	first := t3Take(t, s.frames)
	release()
	s.finish()
	t3OnlyCurrent(t, first, t3Drain(t, s.frames), t3Code("QR-B", &s.owner))
}

func t3HeldReplayAfterTerminal(t *testing.T) {
	s := newT3Delivery(t)
	a := s.start(channels.PairingAdmissionEnable)
	s.code(a, "QR-unowned")
	// First prove late subscription can see this exact code, before testing
	// absence after terminal eviction. Empty output alone is not evidence.
	warm, warmOutput := makeForwarderTestConn(8)
	s.h.subscribePairingInterest(warm, "whatsapp", true)
	require.Equal(t, t3Code("QR-unowned", nil), t3Take(t, warmOutput))
	release, entered := s.holdAfter("replay")
	s.goWork(func() { s.h.subscribePairingInterest(s.wc, "whatsapp", true) })
	t3Await(t, entered, "unowned replay snapshot")
	s.forward()
	s.f.al.EmitWhatsAppPairingAttempt("whatsapp", a.AttemptID, channels.PairingStatusTimeout, "", "expired")
	first := t3Take(t, s.frames)
	message := "expired"
	require.Equal(t, generated.WhatsAppPairingFrame{
		Type: "whatsapp_pairing", ChannelId: "whatsapp", Status: "timeout", Message: &message,
	}, first, "actual terminal output has neither owner nor QR")
	late, lateOutput := makeForwarderTestConn(8)
	s.h.subscribePairingInterest(late, "whatsapp", true)
	require.Empty(t, t3Drain(t, lateOutput), "late subscribe cannot load an evicted unowned QR")
	release()
	s.finish()
	for _, frame := range t3Drain(t, s.frames) {
		assert.Equal(t, first, frame, "an old selected replay cannot resurrect a QR after terminal output")
	}
}

func t3LateSubscribeControl(t *testing.T) {
	s := newT3Delivery(t)
	s.hooks(nil, nil)
	a := s.start(channels.PairingAdmissionConnect)
	s.code(a, "QR-A")
	b := s.successor(a)
	s.code(b, "QR-B")
	s.h.subscribePairingInterest(s.wc, "whatsapp", true)
	require.Equal(t, t3Code("QR-B", &s.owner), t3Take(t, s.frames))
	s.code(a, "QR-late-A") // real emitter rejects the retired attempt
	late, output := makeForwarderTestConn(8)
	s.h.subscribePairingInterest(late, "whatsapp", true)
	require.Equal(t, t3Code("QR-B", &s.owner), t3Take(t, output))
	s.finish()
	require.Empty(t, t3Drain(t, s.frames))
	require.Empty(t, t3Drain(t, output))
}

func t3LegalEnqueueOrder(t *testing.T) {
	s := newT3Delivery(t)
	a := s.start(channels.PairingAdmissionConnect)
	s.h.subscribePairingInterest(s.wc, "whatsapp", true)
	hold, release, entered := s.gate()
	s.hooks(nil, func(wc *wsConn, _ []byte) {
		if wc == s.wc {
			hold()
		}
	})
	s.forward()
	s.code(a, "QR-A")
	t3Await(t, entered, "A immediately before real enqueue")
	require.Empty(t, t3Drain(t, s.frames), "the held pre-enqueue stage has not queued A yet")
	callerReady, callB := make(chan struct{}), make(chan struct{})
	var once sync.Once
	s.releases = append(s.releases, func() { once.Do(func() { close(callB) }) })
	s.goWork(func() {
		close(callerReady)
		<-callB // test input gate, explicitly NOT an observed guard acquisition
		b := s.successor(a)
		s.code(b, "QR-B")
	})
	t3Await(t, callerReady, "B caller input gate")
	release()
	first := t3Take(t, s.frames)
	require.Equal(t, t3Code("QR-A", &s.owner), first)
	once.Do(func() { close(callB) })
	second := t3Take(t, s.frames)
	require.Equal(t, t3Code("QR-B", &s.owner), second)
	s.finish()
	require.Empty(t, t3Drain(t, s.frames), "one actual live output per accepted code in this schedule")
}

package webrtc

import (
	"context"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pion "github.com/pion/webrtc/v4"
)

func TestInputContextCancellationStopsRemainingBatch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var seen []string
	s := &Session{sink: func(_ string, raw []byte) { seen = append(seen, string(raw)); cancel() }}
	q := newInputQueue()
	first := `{"kind":"key_down","key":"a"}`
	second := `{"kind":"key_up","key":"a"}`
	q.push([]byte(first), inputQueueCapacity)
	q.push([]byte(second), inputQueueCapacity)
	q.close()
	s.runInputQueueContext(ctx, "viewer", q)
	if !reflect.DeepEqual(seen, []string{first}) {
		t.Fatalf("dispatch after source canceled = %v, want only first event", seen)
	}
}

func TestInputContextCancellationWakesIdleQueue(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &Session{}
	q := newInputQueue()
	defer q.close()
	checked := &inputReadyContext{Context: ctx, ready: make(chan struct{})}
	done := make(chan struct{})
	go func() { s.runInputQueueContext(checked, "viewer", q); close(done) }()
	// The first live Err result has already been read before this signal.
	// Cancellation now races only with entering the empty queue wait, never
	// with starting the worker (which could otherwise exit at its first guard).
	select {
	case <-checked.ready:
	case <-time.After(time.Second):
		t.Fatal("input worker never checked source lifetime")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("canceled idle input worker remained blocked")
	}
}

func TestInputContextAlreadyCanceledDiscardsQueuedInput(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	s := &Session{sink: func(string, []byte) { calls++ }}
	q := newInputQueue()
	q.push([]byte(`{"kind":"mouse_down"}`), inputQueueCapacity)
	q.close()
	s.runInputQueueContext(ctx, "viewer", q)
	if calls != 0 {
		t.Fatalf("already canceled source dispatched %d calls, want zero", calls)
	}
}

func TestInputContextReplacementCancelsBeforeRegistryChanges(t *testing.T) {
	s, _ := newLiveIngestSession(t)
	pc, err := s.buildPeerConnection(s.apiViewer, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	old := &viewerConn{pc: pc, inputCtx: ctx}
	var canceledWhileCurrent atomic.Bool
	old.inputCancel = func() {
		if ctx.Err() == nil {
			canceledWhileCurrent.Store(s.viewers["same"] == old)
		}
		cancel()
	}
	s.viewers["same"] = old
	// Publication precedes SDP application, so even an invalid replacement
	// must invalidate its predecessor before the registry points elsewhere.
	_, _, err = s.HandleViewerOfferHandleContext(context.Background(), "same", "invalid SDP")
	if err == nil {
		t.Fatal("invalid SDP unexpectedly accepted")
	}
	if !canceledWhileCurrent.Load() {
		t.Fatal("old input lifetime remained live after replacement publication")
	}
}

func TestInputContextRejectsLateDataChannelFromRetiredPeer(t *testing.T) {
	s := NewSession(Config{}, nil, nil)
	t.Cleanup(func() { _ = s.Close() })
	current, err := s.buildPeerConnection(s.apiViewer, true)
	if err != nil {
		t.Fatal(err)
	}
	old, err := s.buildPeerConnection(s.apiViewer, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = old.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	vc := &viewerConn{pc: current, inputCtx: ctx, inputCancel: cancel}
	s.viewers["same"] = vc
	dc, err := old.CreateDataChannel("input", nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.bindViewerInputChannel("[test]", "same", old, dc) {
		t.Fatal("retired peer's late channel was accepted")
	}
	if vc.dc != nil {
		t.Fatal("retired channel replaced current viewer's input channel")
	}
	if state := dc.ReadyState(); state != pion.DataChannelStateClosing && state != pion.DataChannelStateClosed {
		t.Fatalf("rejected data channel state=%v, want closing or closed", state)
	}
}

func TestInputContextIsStableAcrossEventsOfOneSource(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var seen []context.Context
	s := NewSessionWithContextInput(Config{}, func(got context.Context, _ string, _ []byte) { seen = append(seen, got) }, nil)
	t.Cleanup(func() { _ = s.Close() })
	q := newInputQueue()
	q.push([]byte(`{"kind":"key_down"}`), inputQueueCapacity)
	q.push([]byte(`{"kind":"key_up"}`), inputQueueCapacity)
	q.close()
	s.runInputQueueContext(ctx, "viewer", q)
	if len(seen) != 2 {
		t.Fatalf("source delivered %d events, want two", len(seen))
	}
	for i, got := range seen {
		if got != ctx {
			t.Fatalf("event %d did not retain the exact stable source context", i)
		}
	}
}

// inputReadyContext observes the context API boundary without replacing the
// worker or queue. Its live Err result is captured before signaling readiness.
type inputReadyContext struct {
	context.Context
	ready chan struct{}
	once  sync.Once
}

func (c *inputReadyContext) Err() error {
	err := c.Context.Err()
	if err == nil {
		c.once.Do(func() { close(c.ready) })
	}
	return err
}

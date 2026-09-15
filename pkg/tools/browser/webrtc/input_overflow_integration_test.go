package webrtc_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	relay "github.com/elicify-ai/omnipus/pkg/tools/browser/webrtc"
)

func TestInputOverflowCancelsOriginalSourceBeforeRemoval(t *testing.T) {
	entered := make(chan context.Context, 1)
	release, workerReturned := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	var calls atomic.Int64
	sess := relay.NewSessionWithContextInput(relay.Config{}, func(ctx context.Context, _ string, _ []byte) {
		if calls.Add(1) == 1 {
			entered <- ctx
			<-release
			close(workerReturned)
		}
	}, safeLogf(t))
	t.Cleanup(func() { _ = sess.Close() })
	enc := newFakeEncoder(t, true)
	enc.startPumping(t)
	answer, err := sess.HandleIngestOffer(nonTrickleOffer(t, enc.pc))
	if err != nil {
		t.Fatal(err)
	}
	setAnswer(t, enc.pc, answer)
	viewer := newFakeViewer(t, true)
	answer, handle, err := sess.HandleViewerOfferHandleContext(context.Background(), "overflow", nonTrickleOffer(t, viewer.pc))
	if err != nil {
		t.Fatal(err)
	}
	setAnswer(t, viewer.pc, answer)
	select {
	case <-viewer.dcOpen:
	case <-time.After(testWait):
		t.Fatal("input channel did not open")
	}
	if err := viewer.inputDC.SendText(`{"kind":"key_down","key":"a"}`); err != nil {
		t.Fatal(err)
	}
	var source context.Context
	select {
	case source = <-entered:
	case <-time.After(testWait):
		t.Fatal("first key-down never reached sink")
	}
	type removal struct {
		id       string
		handle   any
		canceled bool
	}
	removed := make(chan removal, 1)
	sess.SetOnViewerRemoved(func(id string, h any) {
		select {
		case removed <- removal{id, h, source.Err() == context.Canceled}:
		default:
		}
	})
	// The declared production policy is a 512-event bounded backlog. The
	// first dispatch is blocked, so event513 must overflow exactly once.
	for i := 0; i < 513; i++ {
		if err := viewer.inputDC.SendText(`{"kind":"key_up","key":"a"}`); err != nil {
			t.Fatalf("sending backlog event%d: %v", i, err)
		}
	}
	select {
	case got := <-removed:
		if got.id != "overflow" || got.handle != handle || !got.canceled {
			t.Fatalf("overflow removal=%+v want original handle and canceled original source", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("discrete overflow did not cancel and remove its original source")
	}
	got := sess.Stats()
	if got.InputDroppedDiscrete != 1 || got.InputDroppedPositional != 0 || got.InputShedPositional != 0 || got.Viewers != 0 {
		t.Errorf("overflow stats=%+v want exactly one discrete loss and no viewer", got)
	}
	once.Do(func() { close(release) })
	<-workerReturned
	// A sentinel cannot be sent on the now-closed source. Wait briefly to
	// detect any incorrect draining of its already queued 512 events.
	deadline := time.Now().Add(100 * time.Millisecond)
	for calls.Load() == 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("canceled overflow source dispatched %d calls want only first", got)
	}
}

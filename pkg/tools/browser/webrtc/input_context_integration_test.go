package webrtc_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	relay "github.com/elicify-ai/omnipus/pkg/tools/browser/webrtc"
)

type attachmentMarker struct{}

func TestInputContextRetainsOriginalAttachmentAndCancelsWithSource(t *testing.T) {
	for _, end := range []string{"attachment", "replacement", "handle", "session", "channel"} {
		t.Run(end, func(t *testing.T) {
			attachment, cancel := context.WithCancel(context.WithValue(context.Background(), attachmentMarker{}, "original"))
			defer cancel()
			entered := make(chan context.Context, 1)
			var origin atomic.Value
			var originOnce sync.Once
			removed := make(chan bool, 1)
			release := make(chan struct{})
			defer close(release)
			sess := relay.NewSessionWithContextInput(relay.Config{}, func(ctx context.Context, id string, raw []byte) {
				if id != "same-viewer" || string(raw) != `{"kind":"key_down","key":"a"}` {
					return
				}
				originOnce.Do(func() { origin.Store(ctx) })
				entered <- ctx
				select {
				case <-ctx.Done():
				case <-release:
				}
			}, safeLogf(t))
			sess.SetOnViewerRemoved(func(id string, _ any) {
				ctx, _ := origin.Load().(context.Context)
				canceled := id == "same-viewer" && ctx != nil && errors.Is(ctx.Err(), context.Canceled)
				select {
				case removed <- canceled:
				default:
				}
			})
			t.Cleanup(func() { _ = sess.Close() })
			enc := newFakeEncoder(t, true)
			enc.startPumping(t)
			answer, err := sess.HandleIngestOffer(nonTrickleOffer(t, enc.pc))
			if err != nil {
				t.Fatal(err)
			}
			setAnswer(t, enc.pc, answer)
			viewer := newFakeViewer(t, true)
			answer, handle, err := sess.HandleViewerOfferHandleContext(attachment, "same-viewer", nonTrickleOffer(t, viewer.pc))
			if err != nil {
				t.Fatal(err)
			}
			setAnswer(t, viewer.pc, answer)
			select {
			case <-viewer.dcOpen:
			case <-time.After(testWait):
				t.Fatal("input channel never opened")
			}
			if err := viewer.inputDC.SendText(`{"kind":"key_down","key":"a"}`); err != nil {
				t.Fatal(err)
			}
			var source context.Context
			select {
			case source = <-entered:
			case <-time.After(testWait):
				t.Fatal("source input never reached sink")
			}
			if got := source.Value(attachmentMarker{}); got != "original" {
				t.Errorf("sink attachment identity = %v, want original", got)
			}
			if err := source.Err(); err != nil {
				t.Fatalf("returning from negotiation canceled live peer: %v", err)
			}
			switch end {
			case "attachment":
				cancel()
			case "replacement":
				replacement := newFakeViewer(t, true)
				next := context.WithValue(context.Background(), attachmentMarker{}, "replacement")
				nextAnswer, _, err := sess.HandleViewerOfferHandleContext(next, "same-viewer", nonTrickleOffer(t, replacement.pc))
				if err != nil {
					t.Fatal(err)
				}
				setAnswer(t, replacement.pc, nextAnswer)
				select {
				case <-replacement.dcOpen:
				case <-time.After(testWait):
					t.Fatal("replacement channel never opened")
				}
				if err := replacement.inputDC.SendText(`{"kind":"key_down","key":"a"}`); err != nil {
					t.Fatal(err)
				}
				select {
				case nextSource := <-entered:
					if got := nextSource.Value(attachmentMarker{}); got != "replacement" {
						t.Errorf("replacement attachment identity = %v, want replacement", got)
					}
					cancel()
					if err := nextSource.Err(); err != nil {
						t.Fatalf("old attachment canceled replacement input: %v", err)
					}
				case <-time.After(testWait):
					t.Fatal("replacement input never reached sink")
				}
			case "handle":
				sess.CloseViewerIfCurrent(handle)
			case "session":
				if err := sess.Close(); err != nil {
					t.Fatal(err)
				}
			case "channel":
				if err := viewer.inputDC.Close(); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case <-source.Done():
				if !errors.Is(source.Err(), context.Canceled) {
					t.Fatalf("source ended with %v, want context.Canceled", source.Err())
				}
			case <-time.After(time.Second):
				t.Fatal("ended input source did not cancel the in-flight sink context")
			}
			if end == "attachment" || end == "handle" || end == "channel" {
				select {
				case canceled := <-removed:
					if !canceled {
						t.Fatal("viewer-removed callback preceded cancellation of its original source")
					}
				case <-time.After(time.Second):
					t.Fatal("ended current source did not notify held-state cleanup")
				}
			}
		})
	}
}

// input_ordering_burst_test.go — QA wave addition. Package webrtc_test
// (external), reusing session_test.go's fixtures (newFakeEncoder,
// newFakeViewer, nonTrickleOffer, setAnswer, waitCond, safeLogf, testWait)
// rather than re-deriving them.
//
// Traces to: QA wave task "TDD WAVE — live-browser WebRTC video feature"
// item 2 ("Input ordering under burst").

package webrtc_test

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	relay "github.com/elicify-ai/omnipus/pkg/tools/browser/webrtc"
)

// TestSessionInputDataChannelPreservesOrderUnderFastBurst is the FAST-BURST
// counterpart to TestSessionInputDataChannelPreservesOrderUnderSlowSink
// (session_test.go): that existing test proves ordering survives a sink with
// deliberately variable per-call latency at n=30; this test proves ordering
// survives a genuinely RAPID, back-to-back send burst — no artificial delay
// anywhere, on either the send side or the sink side — at n=40. Comfortably
// under inputQueueCapacity=64 (inputdc.go:18), so every single message is
// expected to survive; a short final count here would itself be a distinct
// regression from reordering (see enqueueInput's drop-oldest branch, tested
// separately in inputdc_backpressure_test.go).
func TestSessionInputDataChannelPreservesOrderUnderFastBurst(t *testing.T) {
	var (
		mu   sync.Mutex
		seen []int
	)
	sink := func(_ string, raw []byte) {
		var evt struct {
			Seq int `json:"seq"`
		}
		if err := json.Unmarshal(raw, &evt); err != nil {
			return
		}
		mu.Lock()
		seen = append(seen, evt.Seq)
		mu.Unlock()
	}

	sess := relay.NewSession(relay.Config{}, sink, safeLogf(t))
	t.Cleanup(func() { _ = sess.Close() })

	enc := newFakeEncoder(t, true)
	enc.startPumping(t)
	offer := nonTrickleOffer(t, enc.pc)
	answer, err := sess.HandleIngestOffer(offer)
	if err != nil {
		t.Fatalf("HandleIngestOffer: %v", err)
	}
	setAnswer(t, enc.pc, answer)

	const viewerID = "burst-order-viewer"
	viewer := newFakeViewer(t, true)
	offerV := nonTrickleOffer(t, viewer.pc)
	answerV, err := sess.HandleViewerOffer(viewerID, offerV)
	if err != nil {
		t.Fatalf("HandleViewerOffer: %v", err)
	}
	setAnswer(t, viewer.pc, answerV)

	select {
	case <-viewer.dcOpen:
	case <-time.After(testWait):
		t.Fatal("input data channel did not open")
	}

	const n = 40 // >= 32 per task instructions; well under inputQueueCapacity=64
	for i := 0; i < n; i++ {
		msg := fmt.Sprintf(`{"seq":%d}`, i)
		if sendErr := viewer.inputDC.SendText(msg); sendErr != nil {
			t.Fatalf("SendText(seq=%d): %v", i, sendErr)
		}
	}

	waitCond(t, testWait, "sink to receive all n messages", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(seen) == n
	})

	mu.Lock()
	defer mu.Unlock()
	if len(seen) != n {
		t.Fatalf(
			"expected exactly %d messages (none dropped -- a %d-message burst is well under inputQueueCapacity=64), got %d: %v",
			n,
			n,
			len(seen),
			seen,
		)
	}
	for i, s := range seen {
		if s != i {
			t.Fatalf(
				"sink received out-of-order sequence at index %d: got seq=%d, want seq=%d (full sequence: %v)",
				i, s, i, seen,
			)
		}
	}
}

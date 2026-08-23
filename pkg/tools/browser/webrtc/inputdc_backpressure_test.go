// inputdc_backpressure_test.go — in-package (package webrtc) unit test for
// the inputQueueCapacity drop-oldest backpressure mechanism in inputdc.go.
//
// QA WAVE GAP (see the parent task prompt this file was written under): the
// production drop-oldest path (enqueueInput, inputdc.go ~lines 91-104) logs
// a line via s.logf when it evicts the oldest queued event under
// backpressure, but exposes NO counter/metric anywhere this package or a
// caller could assert against — no field on Stats, no atomic counter, no
// exported accessor. This file proves the OVERFLOW SCENARIO ITSELF is real
// and deterministically reproducible (via test-only instrumentation, NOT a
// production signal), then documents via t.Skip exactly what production
// observability is missing. Per the task instructions this test was written
// under: do NOT add that counter here — production code is out of this
// agent's scope (owned by backend-lead); skip loudly instead so the gap is
// visible in the test report rather than silently absent.
//
// enqueueInput/runInputQueue are driven DIRECTLY (bypassing Pion/SCTP
// entirely) for full determinism: these two unexported methods are exactly
// what wireInputDataChannel's dc.OnMessage callback invokes per message, so
// this exercises the real mechanism, just without the (nondeterministic)
// network transport layered on top of it. Because the enqueue loop below
// runs single-threaded in this test's own goroutine and the sink is blocked
// on a gate that is only closed AFTER that loop returns, the worker
// goroutine can read AT MOST ONE item during the whole burst — this makes
// the final survivor count a provable [inputQueueCapacity,
// inputQueueCapacity+1] regardless of Go's goroutine scheduling, not a race.
//
// Traces to: QA wave task "TDD WAVE — live-browser WebRTC video feature"
// item 3 ("Input backpressure is observable, not silent").

package webrtc

import (
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"
)

func TestInputQueueDropOldest_OverflowHappensButHasNoProductionCounter(t *testing.T) {
	const viewerID = "backpressure-viewer"
	// Derived, not hardcoded (2026-07-30): this used to be a literal 100 with
	// the comment "> inputQueueCapacity (64)". When the capacity was raised
	// the burst silently stopped overflowing and the test hung waiting for
	// messages that were never dropped. Deriving it keeps the overflow
	// scenario real whatever the capacity is.
	burstSize := inputQueueCapacity + 36

	gate := make(chan struct{}) // closed once the whole burst has been enqueued
	var (
		mu   sync.Mutex
		seen []int
	)
	sink := func(_ string, raw []byte) {
		<-gate // blocks the worker's FIRST sink call until the burst is fully enqueued
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

	sess := NewSession(Config{}, sink, nil)
	t.Cleanup(func() { _ = sess.Close() })

	queue := newInputQueue()
	go sess.runInputQueue(viewerID, queue)

	for i := 0; i < burstSize; i++ {
		// kind:"mouse_move" — the flood that actually causes congestion in
		// production is the cursor stream, and it is the kind the shed policy
		// is allowed to discard. An untyped payload would now be classified
		// as DISCRETE (the safe default) and deliberately preserved instead.
		sess.enqueueInput("[test]", viewerID, queue, []byte(fmt.Sprintf(`{"kind":"mouse_move","seq":%d}`, i)))
	}
	close(gate)

	// With dequeue-time coalescing (2026-08-13, coalesceInputBatch) the
	// worker drains the WHOLE backlog per wakeup and collapses a mouse_move
	// run to its newest frame, so a pure-move overflow burst reaches the
	// sink as a HANDFUL of frames (the pre-gate in-flight batch plus the
	// coalesced remainder), not ~capacity frames. That is the fix for the
	// operator-reported progressive input lag: a full queue used to replay
	// `capacity` stale cursor positions through a slow CDP sink, adding
	// capacity x dispatch-time of latency for whatever queued behind them.
	deadline := time.Now().Add(5 * time.Second)
	var final []int
	for {
		mu.Lock()
		final = append([]int(nil), seen...)
		mu.Unlock()
		if len(final) > 0 && final[len(final)-1] == burstSize-1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for the freshest burst message to arrive; got %v", final)
		}
		time.Sleep(10 * time.Millisecond)
	}

	// --- Real, executable assertions: the overflow burst reaches the sink
	// COALESCED (far fewer dispatches than the burst), in order, and never
	// loses the freshest message. Proven via TEST-ONLY instrumentation
	// (`seen` above), NOT a production signal — see the skip below. ---
	if len(final) >= inputQueueCapacity {
		t.Fatalf(
			"expected the %d-message move burst to reach the sink coalesced to well under the queue capacity (%d), "+
				"got %d dispatches — dequeue-time coalescing is not working",
			burstSize, inputQueueCapacity, len(final),
		)
	}
	if !sort.IntsAreSorted(final) {
		t.Fatalf("surviving messages are not in send order (reordering under backpressure): %v", final)
	}
	if final[len(final)-1] != burstSize-1 {
		t.Fatalf(
			"the LAST-sent message must always survive (freshest state wins over a stale backlog), got last=%d want %d",
			final[len(final)-1],
			burstSize-1,
		)
	}
	t.Logf(
		"OBSERVED (test-only instrumentation, NOT a production signal): %d-message move burst reached the sink as "+
			"%d dispatch(es) after shed+coalesce (queue capacity=%d)",
		burstSize, len(final), inputQueueCapacity,
	)

	// --- The actual gap this test exists to surface ---
	//
	// Wished-for production API (does not exist today), e.g.:
	//
	//   gotDropped := sess.Stats().InputDropped // hypothetical field
	//   if gotDropped != int64(burstSize-len(final)) {
	//       t.Errorf("Stats().InputDropped = %d, want %d", gotDropped, burstSize-len(final))
	//   }
	//
	// There is no such field, method, or metric anywhere in this package or
	// on Stats today — enqueueInput's drop branch (inputdc.go line ~99)
	// ONLY calls s.logf(...), an unstructured log line an operator cannot
	// alert on and a caller (gateway or test) cannot assert against.
	t.Skip("BLOCKED: no production-observable counter for input-queue drop-oldest events. " +
		"pkg/tools/browser/webrtc/inputdc.go's enqueueInput (~line 99) only logs via s.logf on drop -- " +
		"no field on Stats, no atomic counter, no exported accessor exists anywhere in this package to " +
		"assert against or monitor. The assertions ABOVE this Skip already ran and passed, proving the " +
		"overflow scenario is real and reproducible via test-only instrumentation; only the counter-based " +
		"assertion is blocked. Needed: e.g. a Stats.InputDropped int64 (or per-viewer equivalent) " +
		"incremented in enqueueInput's drop branch (production code change — owned by backend-lead, not " +
		"this test file).")
}

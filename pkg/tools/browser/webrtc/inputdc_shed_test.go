package webrtc

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"
)

// TestEnqueueInput_MoveFloodNeverEvictsDiscreteEvents is the 2026-07-30 UAT
// regression. The operator could scroll a page but could not click or type:
// the gateway logged 816 "input queue full ... dropped oldest queued event".
// enqueueInput's backpressure was TYPE-BLIND, so a queued mouse_down/key_down
// was evicted by the next flood of mouse_move before the worker ever
// dispatched it — a click that never happened, while wheel (which carries
// deltas and survives decimation) still visibly moved the page.
//
// The queue here is deliberately NOT drained: this models the real failure,
// a consumer (CDP round trip) far slower than a 60-120Hz pointer stream.
func TestEnqueueInput_MoveFloodNeverEvictsDiscreteEvents(t *testing.T) {
	s := &Session{logfn: func(string, ...any) {}}
	queue := newInputQueue()

	ev := func(kind string, id int) []byte {
		b, err := json.Marshal(map[string]any{"kind": kind, "id": id})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return b
	}

	// One real click, then a sustained move flood many times the capacity.
	s.enqueueInput("[t]", "v1", queue, ev("mouse_down", 1))
	for i := 0; i < inputQueueCapacity+200; i++ {
		s.enqueueInput("[t]", "v1", queue, ev("mouse_move", i))
	}
	// And a keystroke arriving while already congested — it must still get in.
	s.enqueueInput("[t]", "v1", queue, ev("key_down", 2))

	queue.close()
	var kinds []string
	for {
		batch, ok := queue.popBatch()
		if !ok {
			break
		}
		for _, raw := range batch {
			var probe struct {
				Kind string `json:"kind"`
			}
			if err := json.Unmarshal(raw, &probe); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			kinds = append(kinds, probe.Kind)
		}
	}

	var down, keys int
	for _, k := range kinds {
		switch k {
		case "mouse_down":
			down++
		case "key_down":
			keys++
		}
	}
	if down != 1 {
		t.Errorf("mouse_down survived = %d, want 1 — a queued click was shed by the move flood: %v", down, kinds)
	}
	if keys != 1 {
		t.Errorf("key_down admitted = %d, want 1 — a discrete event was refused under congestion: %v", keys, kinds)
	}
}

// TestEnqueueInput_UnknownKindTreatedAsDiscrete pins the safe default: an
// unparseable or unrecognized payload must NOT be classified as sheddable,
// since misclassifying a discrete event is the exact failure above.
func TestEnqueueInput_UnknownKindTreatedAsDiscrete(t *testing.T) {
	for _, raw := range [][]byte{
		[]byte(`{"kind":"something_new"}`),
		[]byte(`not json at all`),
	} {
		if isCoalescableInputKind(raw) {
			t.Errorf("isCoalescableInputKind(%s) = true, want false (unknown must be treated as discrete)", raw)
		}
	}
	for _, kind := range []string{"mouse_move", "wheel"} {
		if !isCoalescableInputKind([]byte(fmt.Sprintf(`{"kind":%q}`, kind))) {
			t.Errorf("isCoalescableInputKind(%s) = false, want true", kind)
		}
	}
}

// TestInputQueue_OrderingHoldsWithConcurrentConsumer is the 2026-07-31
// reordering regression, found in review.
//
// The previous implementation used a plain `chan []byte` that TWO goroutines
// received from: the drain worker, and the producer's eviction path (which
// drained the channel into a slice, dropped one item, then re-appended the
// rest). Go does not define which of two concurrent receivers wins, so the
// worker could take an item mid-eviction while the eviction loop retained and
// re-appended items that were originally AHEAD of it — dispatching e.g. a
// mouse_up before its mouse_down.
//
// No prior test could see this: inputdc_shed_test never started a consumer at
// all, and inputdc_backpressure_test gated its sink so the worker could read
// at most one item for the whole burst. This one runs a REAL concurrent
// consumer against a producer that continuously forces the shed path, and
// asserts the discrete events arrive in send order.
func TestInputQueue_OrderingHoldsWithConcurrentConsumer(t *testing.T) {
	s := &Session{logfn: func(string, ...any) {}}
	queue := newInputQueue()

	var mu sync.Mutex
	var gotDiscrete []int

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			batch, ok := queue.popBatch()
			if !ok {
				return
			}
			// Mirror runInputQueue: the whole drained batch is consumed in
			// order. Per-item cost simulates the real consumer's CDP round
			// trip so the producer genuinely outruns it and the shed path
			// stays hot.
			for _, raw := range batch {
				var probe struct {
					Kind string `json:"kind"`
					Seq  int    `json:"seq"`
				}
				if err := json.Unmarshal(raw, &probe); err != nil {
					continue
				}
				if probe.Kind == "mouse_down" || probe.Kind == "key_down" {
					mu.Lock()
					gotDiscrete = append(gotDiscrete, probe.Seq)
					mu.Unlock()
				}
				time.Sleep(50 * time.Microsecond)
			}
		}
	}()

	// Interleave discrete events into a heavy positional flood, so nearly
	// every discrete push lands while the queue is full and must shed.
	const discreteCount = 200
	seq := 0
	for i := 0; i < discreteCount; i++ {
		for j := 0; j < 20; j++ {
			b, _ := json.Marshal(map[string]any{"kind": "mouse_move", "seq": -1})
			s.enqueueInput("[t]", "v1", queue, b)
		}
		b, _ := json.Marshal(map[string]any{"kind": "mouse_down", "seq": seq})
		s.enqueueInput("[t]", "v1", queue, b)
		seq++
	}

	// Let the consumer drain, then close and wait for it to finish.
	deadline := time.Now().Add(10 * time.Second)
	for queue.Len() != 0 && !time.Now().After(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	queue.close()
	<-done

	mu.Lock()
	defer mu.Unlock()
	if len(gotDiscrete) == 0 {
		t.Fatal("no discrete events were delivered at all — the test did not exercise the path")
	}
	// The shed policy may legitimately DROP a discrete event only when the
	// backlog is entirely discrete, which this interleaving avoids; but the
	// invariant under test is ORDER, not completeness.
	for i := 1; i < len(gotDiscrete); i++ {
		if gotDiscrete[i] < gotDiscrete[i-1] {
			t.Fatalf("discrete events dispatched OUT OF ORDER: %v (index %d went backwards) — "+
				"an eviction path racing the consumer reordered the stream; a mouse_up can precede its mouse_down",
				gotDiscrete, i)
		}
	}
	t.Logf("delivered %d/%d discrete events, all in order", len(gotDiscrete), discreteCount)
}

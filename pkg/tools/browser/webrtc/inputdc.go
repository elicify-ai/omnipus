package webrtc

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/pion/webrtc/v4"
)

// inputQueueCapacity bounds one viewer's pending input-event backlog
// (fix-wave finding 2c). 64 is generous for a live cursor/keystream under
// normal dispatch latency (Q4 spike: p95 CDP round trip ~21ms, so a
// consumer keeping pace drains this in well under a second even at a heavy
// input rate) while still bounding memory for a viewer whose sink has
// genuinely wedged.
// Raised 64 -> 512 (2026-07-30 UAT). The "~21ms p95 CDP round trip" premise
// above did not hold in the field: a live operator session logged 816
// "input queue full" drops, i.e. the queue saturated and stayed saturated.
// A trackpad/mouse emits pointermove at 60-120Hz while each dispatch costs a
// real CDP round trip, so the producer outruns the consumer by an order of
// magnitude and 64 slots fill in well under a second. Capacity alone does
// not fix that (a sustained flood overruns any finite buffer) — see
// isCoalescableInputKind for the part that actually does — but a deeper
// buffer absorbs normal bursts so shedding is reserved for real congestion.
const inputQueueCapacity = 512

// isCoalescableInputKind reports whether an input event is positional/
// continuous ("shed me first") rather than discrete and semantically
// essential.
//
// 2026-07-30 UAT — the bug this exists to fix: enqueueInput's original
// backpressure policy dropped the OLDEST queued event, whatever it was. That
// is correct ONLY if every event is equally disposable, which is exactly
// wrong for input. mouse_move dominates the stream by volume, so under
// congestion a mouse_down/key_down that had been queued got evicted by the
// next flood of moves before the worker ever reached it. The operator's
// symptom was precisely this asymmetry: scrolling still moved the page
// (wheel carries deltas, so even a decimated stream visibly works) while
// clicks and keystrokes did nothing at all — they were being shed as
// collateral, never dispatched.
//
// Discrete events (mouse_down/up, key_down/up) are unique, unrepeatable, and
// meaningless if lost: dropping one is not "degraded input", it is a click
// that never happened. Positional ones (mouse_move, wheel) are a sampled
// stream where the freshest sample supersedes older ones, so shedding them
// costs smoothness, not correctness.
//
// The package still does not depend on pkg/api/generated (see
// wireInputDataChannel's doc comment): it peeks at ONE field, `kind`, via a
// local struct, rather than decoding the frame. Any unrecognized or
// unparseable kind is treated as NON-coalescable — the safe default, since
// misclassifying a discrete event as sheddable is the failure this fixes.
func isCoalescableInputKind(raw []byte) bool {
	var probe struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return false
	}
	switch probe.Kind {
	case "mouse_move", "wheel":
		return true
	default:
		return false
	}
}

// wireInputDataChannel is called from a viewer PeerConnection's
// OnDataChannel callback when the browser's "input" data channel arrives.
// Every message is handed RAW (unparsed) to the Session's InputSink, tagged
// with viewerID -- this package never interprets the payload, so it has no
// dependency on pkg/api/generated; the gateway decodes it as a
// BrowserInputFrame (or whatever wire type it chooses) and dispatches it.
//
// Q1/Q4 gotcha (see wv1-spike-results.md): a browser's dc.send(string) is a
// TEXT-typed data-channel frame; Pion's dc.Send() emits BINARY. A text vs.
// binary mismatch is silently dropped by the browser-side onmessage handler.
// Every reply this package sends (SendToViewer) therefore uses SendText,
// never Send. Incoming binary frames are logged and dropped rather than
// forwarded, since the browser side of this protocol only ever sends text.
//
// Fix-wave finding 2c (dispatch ordering): messages are handed to a single,
// PER-VIEWER serialized queue/worker (enqueueInput/runInputQueue) rather
// than the previous "go s.sink(viewerID, raw)" spawned fresh per message.
// Pion invokes OnMessage serially per data channel, but that guarantee was
// being thrown away the instant each message got its own goroutine --
// nothing serialized WHICH goroutine's sink call actually ran first once
// more than one was in flight, so a slow dispatch for an EARLIER message
// (the gateway's real CDP round trip) could complete after a fast dispatch
// for a LATER one, delivering e.g. keydown/keyup out of order to the CDP
// layer. The queue+worker preserves order while still never letting a slow
// sink block Pion's own OnMessage callback: enqueueInput is non-blocking
// (drops the OLDEST queued event under backpressure, mirroring live.go's
// queueAck coalescing pattern -- a live cursor/key stream that has fallen
// behind is more useful caught up on the FRESHEST state than stalled
// replaying a backlog), and the worker goroutine that actually calls
// s.sink runs independently of Pion's dispatch goroutine.
// pushOutcome reports what enqueueInput's backpressure policy did.
type pushOutcome int

const (
	pushAccepted pushOutcome = iota
	pushShedOldestPositional
	pushDroppedIncomingPositional
	pushDroppedIncomingDiscrete
	pushClosed
)

// inputQueue is one viewer's pending input-event backlog: a mutex-guarded
// deque with a coalescing wakeup signal.
//
// It replaced a plain `chan []byte` on 2026-07-31 after review found a real
// reordering bug. The old design had TWO goroutines receiving from the same
// channel: the drain worker (runInputQueue) and the producer's eviction path
// (which drained the channel into a slice, dropped one item, and re-pushed
// the rest). Go guarantees buffered values are handed out in send order, but
// NOT which of two concurrent receivers gets any given value — so the worker
// could take an item mid-eviction while the eviction loop retained and
// re-appended items that were originally AHEAD of it. Result: a mouse_up
// dispatched before its mouse_down, or a key_up before its key_down — exactly
// the drag/keystroke corruption the shedding policy exists to prevent, and
// invisible to the existing tests because none of them ran a concurrent
// consumer.
//
// Here there is exactly ONE dequeuer (runInputQueue), and the shed decision
// is made under the SAME lock as the append, so the queue is never observed
// in a partially-drained state. Ordering is total and obvious.
type inputQueue struct {
	mu     sync.Mutex
	items  [][]byte
	closed bool
	// notify has capacity 1 and coalesces: it signals "there may be work",
	// never carries the work itself. pop re-checks under the lock, so a
	// spurious or missed-then-recovered wakeup is harmless.
	notify chan struct{}
}

func newInputQueue() *inputQueue {
	return &inputQueue{notify: make(chan struct{}, 1)}
}

// signalLocked wakes a waiting pop. Non-blocking, so it is safe to call with
// q.mu held.
func (q *inputQueue) signalLocked() {
	select {
	case q.notify <- struct{}{}:
	default:
	}
}

// push appends raw, applying the type-aware backpressure policy when the
// queue is at capacity. Never blocks — the caller is Pion's OnMessage
// goroutine and must not be stalled.
//
// Policy when full: shed the OLDEST POSITIONAL (mouse_move/wheel) entry to
// make room, wherever it sits in the backlog — never the head, which may be a
// discrete event. Applying this to positional arrivals too preserves the
// property that for a cursor stream the FRESHEST sample is the valuable one.
// If nothing positional exists to shed, the backlog is entirely discrete and
// the INCOMING event is dropped, which keeps the queued events a contiguous
// in-order prefix rather than punching a hole in a keystroke sequence.
func (q *inputQueue) push(raw []byte, capacity int) pushOutcome {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return pushClosed
	}
	if len(q.items) < capacity {
		q.items = append(q.items, raw)
		q.signalLocked()
		return pushAccepted
	}
	for i, it := range q.items {
		if isCoalescableInputKind(it) {
			q.items = append(q.items[:i], q.items[i+1:]...)
			q.items = append(q.items, raw)
			q.signalLocked()
			return pushShedOldestPositional
		}
	}
	if isCoalescableInputKind(raw) {
		return pushDroppedIncomingPositional
	}
	return pushDroppedIncomingDiscrete
}

// popBatch blocks until at least one item is available or the queue is
// closed, then returns the ENTIRE current backlog (ownership transferred).
// Returns (nil, false) once closed AND drained — so a viewer's final queued
// events are still delivered before the worker exits.
//
// Batch semantics (2026-08-13, operator-reported progressive input lag): the
// worker used to pop ONE event per iteration, paying one full sink dispatch
// (a real CDP round trip) per queued event. Under a sustained cursor/wheel
// stream on a busy captured tab the producer outruns that consumer, the
// queue sits pegged at capacity, and steady-state input latency becomes
// capacity x dispatch-time — tens of seconds, growing with page weight, with
// clicks queued behind hundreds of stale cursor positions ("the longer I am
// on the page the slower it gets; eventually no click hits its target").
// Shedding at push-time cannot fix that: it bounds MEMORY, not LATENCY,
// because a full-but-shedding queue still replays `capacity` events through
// the slow sink. Draining the whole backlog per wakeup and coalescing it
// (coalesceInputBatch) is what bounds latency: the sink dispatches the few
// events that still matter, not the history of how the cursor got there.
func (q *inputQueue) popBatch() ([][]byte, bool) {
	for {
		q.mu.Lock()
		if len(q.items) > 0 {
			batch := q.items
			q.items = nil
			q.mu.Unlock()
			return batch, true
		}
		closed := q.closed
		q.mu.Unlock()
		if closed {
			return nil, false
		}
		<-q.notify
	}
}

// close marks the queue closed and wakes any waiting pop. Idempotent at this
// level; callers also guard with sync.Once.
func (q *inputQueue) close() {
	q.mu.Lock()
	q.closed = true
	q.signalLocked()
	q.mu.Unlock()
}

// Len reports the current backlog depth (test/diagnostic use).
func (q *inputQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items)
}

func (s *Session) wireInputDataChannel(prefix, viewerID string, dc *webrtc.DataChannel) {
	queue := newInputQueue()
	var closeQueueOnce sync.Once
	closeQueue := func() { closeQueueOnce.Do(queue.close) }

	go s.runInputQueue(viewerID, queue)

	dc.OnOpen(func() {
		s.logf("%s input data channel OPEN (label=%s)", prefix, dc.Label())
	})
	dc.OnClose(func() {
		s.logf("%s input data channel closed", prefix)
		closeQueue()
	})
	dc.OnError(func(err error) {
		s.logf("%s input data channel error: %v", prefix, err)
	})
	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		if !msg.IsString {
			s.logf("%s WARNING: binary input frame received (want text), ignoring %d bytes", prefix, len(msg.Data))
			return
		}
		if s.sink == nil {
			return
		}
		// Copy before handing off: Pion may reuse/release the underlying
		// buffer once OnMessage returns, and the queue worker consumes this
		// asynchronously.
		raw := make([]byte, len(msg.Data))
		copy(raw, msg.Data)
		s.enqueueInput(prefix, viewerID, queue, raw)
	})
}

// enqueueInput pushes raw onto queue without ever blocking the caller (Pion's
// own OnMessage callback -- see wireInputDataChannel's doc comment): if the
// queue is full, the OLDEST queued item is dropped to make room, not the new
// one, mirroring live.go's queueAck coalescing discipline. Logged at
// Session's normal logf (the gateway's webrtcRelayLogf classifies a line
// with neither "failed" nor "warning" in it to Debug, matching the fix's
// "log drops at Debug" requirement).
// Revised 2026-07-30 (UAT): the shed decision is now TYPE-AWARE — see
// isCoalescableInputKind for the failure that forced this. A full queue no
// longer blindly evicts whatever is at the head:
//
//   - An incoming COALESCABLE event (mouse_move/wheel) is dropped outright.
//     It must never evict a queued click or keystroke; the next move is
//     along in ~10ms and supersedes it anyway.
//   - An incoming DISCRETE event (mouse_down/up, key_down/up) always gets
//     in, evicting the head to make room. Under the flood that causes
//     congestion the head is overwhelmingly a move, so in practice this
//     sheds a move to admit a click — exactly the intended trade.
//
// Order is still preserved (the queue is only ever appended to at the tail
// and consumed from the head), and the function is still non-blocking, so
// Pion's OnMessage callback is never stalled.
func (s *Session) enqueueInput(prefix, viewerID string, queue *inputQueue, raw []byte) {
	switch queue.push(raw, inputQueueCapacity) {
	case pushAccepted, pushClosed:
		return
	case pushShedOldestPositional:
		// Normal, expected backpressure under a sustained cursor stream.
		s.logf("%s input queue full for viewer %s, shed oldest positional event to admit a newer one", prefix, viewerID)
	case pushDroppedIncomingPositional:
		s.logf(
			"%s input queue full for viewer %s, dropped incoming positional event (backlog is all discrete)",
			prefix,
			viewerID,
		)
	case pushDroppedIncomingDiscrete:
		// Real input loss, not routine backpressure — WARNING so
		// webrtcRelayLogf escalates it above debug.
		s.logf("%s WARNING: input queue full for viewer %s, dropped a discrete input event", prefix, viewerID)
	}
}

// runInputQueue is the SINGLE goroutine that drains one viewer's input queue
// and invokes the Session's InputSink for each message IN ORDER, until the
// queue is closed (dc.OnClose) — draining whatever remains queued first so a
// viewer's last few events before disconnect are not discarded.
//
// It is the ONLY dequeuer. That is the whole point of the inputQueue type
// (see its doc comment): the previous implementation used a Go channel that
// this worker AND the producer's eviction path both received from, which
// could reorder events.
func (s *Session) runInputQueue(viewerID string, queue *inputQueue) {
	for {
		batch, ok := queue.popBatch()
		if !ok {
			return
		}
		if s.sink == nil {
			continue
		}
		for _, raw := range coalesceInputBatch(batch) {
			s.sink(viewerID, raw)
		}
	}
}

// coalesceInputBatch compacts one drained backlog before dispatch. Only
// CONSECUTIVE runs of the same coalescable kind are collapsed, so ordering
// relative to every discrete event (mouse_down/up, key_down/up) is preserved
// exactly — a move that precedes a click still precedes it, and a move
// between two clicks is never merged across either of them:
//
//   - a run of mouse_move frames collapses to its NEWEST frame (a cursor
//     stream is sampled state; the freshest sample supersedes the rest);
//   - a run of wheel frames collapses to its newest frame carrying the SUM
//     of the run's delta_x/delta_y (wheel is a stream of increments; the
//     merged frame preserves total scroll distance while costing one
//     dispatch instead of dozens);
//   - everything else, and anything that fails to parse, passes through
//     unchanged in place.
//
// The wheel merge round-trips the newest frame through map[string]any so
// every other field (coordinates, modifiers, capture_width/height, ...)
// rides along untouched — this package still never mirrors the
// BrowserInputFrame wire struct (see wireInputDataChannel's doc comment);
// like isCoalescableInputKind's `kind` probe it touches named fields only.
// If any frame in a wheel run fails to parse, that run is passed through
// uncoalesced — correctness over compaction.
func coalesceInputBatch(batch [][]byte) [][]byte {
	if len(batch) < 2 {
		return batch
	}
	out := make([][]byte, 0, len(batch))
	for i := 0; i < len(batch); {
		kind := inputKindOf(batch[i])
		if kind != "mouse_move" && kind != "wheel" {
			out = append(out, batch[i])
			i++
			continue
		}
		j := i + 1
		for j < len(batch) && inputKindOf(batch[j]) == kind {
			j++
		}
		run := batch[i:j]
		if kind == "mouse_move" {
			out = append(out, run[len(run)-1])
		} else {
			out = append(out, mergeWheelRun(run)...)
		}
		i = j
	}
	return out
}

// inputKindOf peeks the frame's `kind` (empty string on parse failure, which
// callers treat as non-coalescable — the safe default).
func inputKindOf(raw []byte) string {
	var probe struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return ""
	}
	return probe.Kind
}

// mergeWheelRun merges a run of consecutive wheel frames into one frame:
// the NEWEST frame's fields with the run's summed delta_x/delta_y. On any
// parse or re-marshal failure the run is returned unmerged.
func mergeWheelRun(run [][]byte) [][]byte {
	if len(run) == 1 {
		return run
	}
	var sumX, sumY float64
	for _, raw := range run {
		var probe struct {
			DeltaX *float64 `json:"delta_x"`
			DeltaY *float64 `json:"delta_y"`
		}
		if err := json.Unmarshal(raw, &probe); err != nil {
			return run
		}
		if probe.DeltaX != nil {
			sumX += *probe.DeltaX
		}
		if probe.DeltaY != nil {
			sumY += *probe.DeltaY
		}
	}
	var newest map[string]any
	if err := json.Unmarshal(run[len(run)-1], &newest); err != nil {
		return run
	}
	newest["delta_x"] = sumX
	newest["delta_y"] = sumY
	merged, err := json.Marshal(newest)
	if err != nil {
		return run
	}
	return [][]byte{merged}
}

// SendToViewer sends msg (typically a JSON ack or state frame the gateway
// built after handling an InputSink callback) to viewerID's "input" data
// channel as a TEXT frame. Returns an error if the viewer is unknown or its
// data channel has not opened yet (the channel opens asynchronously shortly
// after HandleViewerOffer returns the answer).
//
// Fix-wave comment note: this method currently has NO production caller —
// the gateway (pkg/gateway/browser_webrtc.go) chose the main
// /api/v1/browser/ws connection, not this data channel, as the path for
// surfacing input-dispatch acks/errors back to a viewer (see
// surfaceWebRTCInputError), so replies never round-trip over the same
// channel input arrived on. Kept as part of this type's public contract
// (exercised directly by this package's own Go<->Go tests) since the
// data-channel round trip it implements is still real and may gain a
// production caller later; it is not dead code to be deleted, just currently
// unused outside tests.
func (s *Session) SendToViewer(viewerID string, msg []byte) error {
	s.viewersMu.Lock()
	vc, exists := s.viewers[viewerID]
	var dc *webrtc.DataChannel
	if exists {
		dc = vc.dc
	}
	s.viewersMu.Unlock()

	if !exists {
		return fmt.Errorf("webrtc: send to viewer %q: no such viewer", viewerID)
	}
	if dc == nil {
		return fmt.Errorf("webrtc: send to viewer %q: input data channel not open yet", viewerID)
	}
	if err := dc.SendText(string(msg)); err != nil {
		return fmt.Errorf("webrtc: send to viewer %q: %w", viewerID, err)
	}
	return nil
}

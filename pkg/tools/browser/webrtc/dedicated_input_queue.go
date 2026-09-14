package webrtc

import (
	"context"
	"errors"
	"math"
	"reflect"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

const maxInputSequence = 1<<53 - 1

// Bound waiting work independently of the active browser operation deadline.
const reliableInputMaxWait = time.Second

type queuedDedicatedInput struct {
	frame                                         generated.BrowserInputFrame
	enqueued                                      time.Time
	firstReliableSeq, lastReliableSeq, inputCount int
	continuation                                  bool
}

// InputQueueTiming describes admitted inputs represented by one serial dispatch.
// EnqueuedAt always belongs to the oldest input, including when wheels merge.
type InputQueueTiming struct {
	EnqueuedAt       time.Time
	FirstReliableSeq int
	LastReliableSeq  int
	InputCount       int
}

// dedicatedInputQueue merges one reliable FIFO and one replaceable hover slot.
// Only its worker dispatches browser operations. Epoch retirement cancels that
// worker before returning; callers join done before applying a control change.
type dedicatedInputQueue struct {
	mu                               sync.Mutex
	parent                           context.Context
	peer, control                    int
	reliable, hoverSequence, barrier int
	held                             map[string]bool
	paused, closed, expired          bool
	ctx                              context.Context
	cancel                           context.CancelFunc
	done                             chan struct{}
	wake                             chan struct{}
	frames                           []queuedDedicatedInput
	hover                            *queuedDedicatedInput
	sink                             func(context.Context, generated.BrowserInputFrame)
	fail                             func(string)
	controlFailure                   func(int, string)
	activeDispatchBudget             time.Duration
	active                           *queuedDedicatedInput
	activeDeadline                   time.Time
	expiry                           *time.Timer
	observeQueue                     func(generated.BrowserInputFrame, InputQueueTiming)
}

func newDedicatedInputQueue(parent context.Context, peer, control int, sink func(context.Context, generated.BrowserInputFrame), fail func(string)) *dedicatedInputQueue {
	q := &dedicatedInputQueue{parent: parent, peer: peer, control: control, held: make(map[string]bool), sink: sink, fail: fail}
	q.startLocked()
	return q
}
func (q *dedicatedInputQueue) setActiveDispatchBudget(budget time.Duration) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.activeDispatchBudget = budget
}

func (q *dedicatedInputQueue) startLocked() {
	q.ctx, q.cancel = context.WithCancel(q.parent)
	q.done = make(chan struct{})
	q.wake = make(chan struct{}, 1)
	go q.run(q.ctx, q.done, q.wake)
}
func (q *dedicatedInputQueue) run(ctx context.Context, done chan struct{}, wake <-chan struct{}) {
	defer close(done)
	for {
		q.mu.Lock()
		if ctx.Err() != nil || q.ctx != ctx {
			q.mu.Unlock()
			return
		}
		var queued queuedDedicatedInput
		found := false
		if deadline := q.expiryDeadlineLocked(); !deadline.IsZero() && !time.Now().Before(deadline) {
			control, handler := q.control, q.controlFailure
			q.expireLocked()
			q.mu.Unlock()
			q.reportExpiry(control, handler)
			return
		}
		if len(q.frames) > 0 {
			queued = q.frames[0]
			q.frames[0] = queuedDedicatedInput{}
			q.frames = q.frames[1:]
			found = true
		} else if q.hover != nil {
			queued = *q.hover
			q.hover = nil
			found = true
		}
		if found {
			q.active = &queued
			q.activeDeadline = time.Time{}
			if queued.frame.Kind == "wheel" && q.activeDispatchBudget > 0 {
				q.activeDeadline = time.Now().Add(q.activeDispatchBudget)
			}
			q.armExpiryLocked()
		}
		observer := q.observeQueue
		q.mu.Unlock()
		if found {
			if ctx.Err() == nil {
				if observer != nil {
					observer(queued.frame, InputQueueTiming{EnqueuedAt: queued.enqueued, FirstReliableSeq: queued.firstReliableSeq, LastReliableSeq: queued.lastReliableSeq, InputCount: queued.inputCount})
				}
				if ctx.Err() == nil {
					q.sink(ctx, queued.frame)
				}
			}
			q.mu.Lock()
			if q.ctx != ctx || ctx.Err() != nil {
				q.mu.Unlock()
				return
			}
			if !q.activeDeadline.IsZero() && !time.Now().Before(q.activeDeadline) {
				control, handler := q.control, q.controlFailure
				q.expireLocked()
				q.mu.Unlock()
				q.reportExpiry(control, handler)
				return
			}
			if q.compatibleContinuationLocked() {
				q.frames[0].continuation = true
			}
			q.active, q.activeDeadline = nil, time.Time{}
			q.armExpiryLocked()
			q.mu.Unlock()
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-wake:
		}
	}
}

// A single losslessly merged wheel continuation may wait for its active
// wheel's original deadline. Mixed actions retain the oldest-entry deadline.
func (q *dedicatedInputQueue) compatibleContinuationLocked() bool {
	if q.active == nil || q.activeDeadline.IsZero() || len(q.frames) != 1 || len(q.held) != 0 {
		return false
	}
	active := *q.active
	return mergePendingWheel(&active, q.frames[0].frame)
}

func (q *dedicatedInputQueue) expiryDeadlineLocked() time.Time {
	deadline := q.activeDeadline
	if len(q.frames) == 0 || q.compatibleContinuationLocked() {
		return deadline
	}
	if len(q.frames) == 1 && q.frames[0].continuation {
		return deadline
	}
	waiting := q.frames[0].enqueued.Add(reliableInputMaxWait)
	if deadline.IsZero() || waiting.Before(deadline) {
		return waiting
	}
	return deadline
}

// This timer also bounds a hung active wheel sink, independently of whether
// its implementation cooperates with its own browser-operation timeout.
func (q *dedicatedInputQueue) armExpiryLocked() {
	q.stopExpiryLocked()
	deadline := q.expiryDeadlineLocked()
	if deadline.IsZero() {
		return
	}
	source := q.ctx
	q.expiry = time.AfterFunc(time.Until(deadline), func() {
		q.mu.Lock()
		if q.closed || q.paused || q.ctx != source || source.Err() != nil || q.expiryDeadlineLocked() != deadline {
			q.mu.Unlock()
			return
		}
		control, handler := q.control, q.controlFailure
		q.expireLocked()
		q.mu.Unlock()
		q.reportExpiry(control, handler)
	})
}
func (q *dedicatedInputQueue) stopExpiryLocked() {
	if q.expiry != nil {
		q.expiry.Stop()
		q.expiry = nil
	}
}
func (q *dedicatedInputQueue) reportExpiry(control int, handler func(int, string)) {
	if handler != nil {
		handler(control, "reliable input queue expired")
	} else if q.fail != nil {
		q.fail("reliable input queue expired")
	}
}
func (q *dedicatedInputQueue) expireLocked() {
	q.paused, q.expired = true, true
	q.cancel()
	q.frames = nil
	q.active, q.activeDeadline = nil, time.Time{}
	q.hover = nil
	q.stopExpiryLocked()
}
func (q *dedicatedInputQueue) setTimingObserver(observer func(generated.BrowserInputFrame, InputQueueTiming)) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.observeQueue = observer
}

// Only a waiting tail can merge. Comparing the remaining complete frame also
// fences optional geometry and any otherwise irrelevant claims; nothing crosses
// an action, capture change, direction reversal, or held-input transition.
func mergePendingWheel(tail *queuedDedicatedInput, next generated.BrowserInputFrame) bool {
	previous := tail.frame
	if previous.Kind != "wheel" || next.Kind != "wheel" || previous.X == nil || previous.Y == nil || previous.CaptureId == nil || *previous.CaptureId == "" || previous.CaptureGeneration == nil || *previous.CaptureGeneration <= 0 || previous.DeltaX == nil || previous.DeltaY == nil || next.DeltaX == nil || next.DeltaY == nil || (previous.Modifiers != nil && *previous.Modifiers != 0) || (previous.Button != nil && *previous.Button != "none") {
		return false
	}
	dx, dy := *previous.DeltaX+*next.DeltaX, *previous.DeltaY+*next.DeltaY
	for _, axis := range [][2]float64{{*previous.DeltaX, *next.DeltaX}, {*previous.DeltaY, *next.DeltaY}} {
		if math.IsNaN(axis[0]) || math.IsNaN(axis[1]) || math.IsInf(axis[0], 0) || math.IsInf(axis[1], 0) || (axis[0] < 0 && axis[1] > 0) || (axis[0] > 0 && axis[1] < 0) {
			return false
		}
	}
	if math.IsInf(dx, 0) || math.IsInf(dy, 0) || math.IsNaN(dx) || math.IsNaN(dy) {
		return false
	}
	a, b := previous, next
	a.ReliableSeq, b.ReliableSeq = nil, nil
	a.DeltaX, b.DeltaX, a.DeltaY, b.DeltaY = nil, nil, nil, nil
	if !reflect.DeepEqual(a, b) {
		return false
	}
	tail.frame.DeltaX, tail.frame.DeltaY = &dx, &dy
	tail.lastReliableSeq = *next.ReliableSeq
	tail.inputCount++
	return true
}
func validInputCounter(v *int, minimum int) bool {
	return v != nil && *v >= minimum && *v <= maxInputSequence
}
func inputTransition(kind string) bool {
	return kind == "mouse_down" || kind == "mouse_up" || kind == "key_down" || kind == "key_up"
}
func (q *dedicatedInputQueue) submit(hover bool, f generated.BrowserInputFrame) {
	q.mu.Lock()
	reason := q.enqueueLocked(hover, f)
	if reason != "" {
		q.closed = true
		q.cancel()
		q.frames = nil
		q.active, q.activeDeadline = nil, time.Time{}
		q.stopExpiryLocked()
		q.hover = nil
	}
	q.mu.Unlock()
	if reason != "" && q.fail != nil {
		q.fail(reason)
	}
}
func (q *dedicatedInputQueue) enqueueLocked(hover bool, f generated.BrowserInputFrame) string {
	if q.closed || q.paused || q.ctx.Err() != nil {
		return ""
	}
	if !validInputCounter(f.InputEpoch, 1) || !validInputCounter(f.ControlEpoch, 0) || !validInputCounter(f.GestureBarrier, 0) {
		return "invalid input identity"
	}
	if *f.InputEpoch != q.peer || *f.ControlEpoch != q.control {
		return ""
	}
	if hover {
		if f.Kind != "mouse_move" || f.ReliableSeq != nil || !validInputCounter(f.HoverSeq, 1) || (f.Modifiers != nil && *f.Modifiers != 0) || (f.Button != nil && *f.Button != "none") {
			return "invalid hover payload"
		}
		if *f.GestureBarrier != q.barrier || len(q.held) > 0 || *f.HoverSeq <= q.hoverSequence {
			return ""
		}
		q.hoverSequence = *f.HoverSeq
		q.hover = &queuedDedicatedInput{frame: f, enqueued: time.Now(), inputCount: 1}
	} else {
		switch f.Kind {
		case "mouse_move", "mouse_down", "mouse_up", "wheel", "key_down", "key_up", "text":
		default:
			return "invalid reliable payload"
		}
		if f.HoverSeq != nil || !validInputCounter(f.ReliableSeq, 1) {
			return "invalid reliable sequence"
		}
		if *f.ReliableSeq <= q.reliable {
			return ""
		}
		if *f.ReliableSeq != q.reliable+1 {
			return "invalid reliable sequence"
		}
		nextBarrier := q.barrier
		if inputTransition(f.Kind) {
			nextBarrier++
		}
		if nextBarrier > maxInputSequence || *f.GestureBarrier != nextBarrier {
			return "invalid gesture barrier"
		}
		// A compatible pending wheel adds no waiting work, even at capacity.
		// Validate sequence/barrier first and retain the oldest queue deadline.
		if len(q.frames) > 0 && len(q.held) == 0 && mergePendingWheel(&q.frames[len(q.frames)-1], f) {
			q.reliable = *f.ReliableSeq
			q.armExpiryLocked()
			return ""
		}
		if len(q.frames) >= inputQueueCapacity {
			return "reliable input queue full"
		}
		q.reliable = *f.ReliableSeq
		q.barrier = nextBarrier
		if inputTransition(f.Kind) {
			q.hover = nil
			key := "key:"
			if f.Code != nil {
				key += *f.Code
			} else if f.Key != nil {
				key += *f.Key
			}
			if f.Kind == "mouse_down" || f.Kind == "mouse_up" {
				key = "button:"
				if f.Button != nil {
					key += *f.Button
				}
			}
			if f.Kind == "key_down" || f.Kind == "mouse_down" {
				q.held[key] = true
			} else {
				delete(q.held, key)
			}
		}
		q.frames = append(q.frames, queuedDedicatedInput{frame: f, enqueued: time.Now(), firstReliableSeq: *f.ReliableSeq, lastReliableSeq: *f.ReliableSeq, inputCount: 1})
		q.armExpiryLocked()
	}
	select {
	case q.wake <- struct{}{}:
	default:
	}
	return ""
}
func (q *dedicatedInputQueue) close() {
	q.mu.Lock()
	q.closed = true
	q.cancel()
	q.frames = nil
	q.active, q.activeDeadline = nil, time.Time{}
	q.stopExpiryLocked()
	q.hover = nil
	q.mu.Unlock()
}
func (q *dedicatedInputQueue) pause(next int) (context.Context, <-chan struct{}, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed || q.parent.Err() != nil || next != q.control+1 || next > maxInputSequence {
		return nil, nil, errors.New("invalid control epoch")
	}
	q.control = next
	q.expired = false
	// Control can overtake packets on the separate data connection. Its new
	// epoch starts an independent sequence, so abandoned packets leave no gaps.
	q.reliable, q.hoverSequence, q.barrier = 0, 0, 0
	q.paused = true
	q.cancel()
	q.frames = nil
	q.active, q.activeDeadline = nil, time.Time{}
	q.stopExpiryLocked()
	q.hover = nil
	q.held = make(map[string]bool)
	return q.ctx, q.done, nil
}
func (q *dedicatedInputQueue) resume(next int) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed || q.expired || q.parent.Err() != nil || !q.paused || next != q.control {
		return errors.New("input epoch is not resumable")
	}
	select {
	case <-q.done:
	default:
		return errors.New("previous input dispatch still active")
	}
	q.paused = false
	q.startLocked()
	return nil
}

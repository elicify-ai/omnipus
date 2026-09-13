package webrtc

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

const maxInputSequence = 1<<53 - 1

// Bound waiting work independently of the active browser operation deadline.
const reliableInputMaxWait = time.Second

type queuedDedicatedInput struct {
	frame    generated.BrowserInputFrame
	enqueued time.Time
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
	paused, closed                   bool
	ctx                              context.Context
	cancel                           context.CancelFunc
	done                             chan struct{}
	wake                             chan struct{}
	frames                           []queuedDedicatedInput
	hover                            *queuedDedicatedInput
	sink                             func(context.Context, generated.BrowserInputFrame)
	fail                             func(string)
	expiry                           *time.Timer
	observeQueue                     func(generated.BrowserInputFrame, time.Time)
}

func newDedicatedInputQueue(parent context.Context, peer, control int, sink func(context.Context, generated.BrowserInputFrame), fail func(string)) *dedicatedInputQueue {
	q := &dedicatedInputQueue{parent: parent, peer: peer, control: control, held: make(map[string]bool), sink: sink, fail: fail}
	q.startLocked()
	return q
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
		if len(q.frames) > 0 && time.Since(q.frames[0].enqueued) >= reliableInputMaxWait {
			q.expireLocked()
			q.mu.Unlock()
			if q.fail != nil {
				q.fail("reliable input queue expired")
			}
			return
		}
		if len(q.frames) > 0 {
			queued = q.frames[0]
			q.frames[0] = queuedDedicatedInput{}
			q.frames = q.frames[1:]
			q.armExpiryLocked()
			found = true
		} else if q.hover != nil {
			queued = *q.hover
			q.hover = nil
			found = true
		}
		observer := q.observeQueue
		q.mu.Unlock()
		if found {
			if ctx.Err() == nil {
				if observer != nil {
					observer(queued.frame, queued.enqueued)
				}
				if ctx.Err() == nil {
					q.sink(ctx, queued.frame)
				}
			}
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-wake:
		}
	}
}

// The timer can fail a backlog even while the serial sink is blocked. Its
// source/oldest-entry checks fence callbacks already racing Stop or retirement.
func (q *dedicatedInputQueue) armExpiryLocked() {
	q.stopExpiryLocked()
	if len(q.frames) == 0 {
		return
	}
	source, oldest := q.ctx, q.frames[0].enqueued
	q.expiry = time.AfterFunc(time.Until(oldest.Add(reliableInputMaxWait)), func() {
		q.mu.Lock()
		if q.closed || q.paused || q.ctx != source || source.Err() != nil || len(q.frames) == 0 || q.frames[0].enqueued != oldest {
			q.mu.Unlock()
			return
		}
		q.expireLocked()
		q.mu.Unlock()
		if q.fail != nil {
			q.fail("reliable input queue expired")
		}
	})
}
func (q *dedicatedInputQueue) stopExpiryLocked() {
	if q.expiry != nil {
		q.expiry.Stop()
		q.expiry = nil
	}
}
func (q *dedicatedInputQueue) expireLocked() {
	q.closed = true
	q.cancel()
	q.frames = nil
	q.hover = nil
	q.stopExpiryLocked()
}
func (q *dedicatedInputQueue) setTimingObserver(observer func(generated.BrowserInputFrame, time.Time)) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.observeQueue = observer
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
		q.hover = &queuedDedicatedInput{frame: f, enqueued: time.Now()}
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
		q.frames = append(q.frames, queuedDedicatedInput{frame: f, enqueued: time.Now()})
		if len(q.frames) == 1 {
			q.armExpiryLocked()
		}
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
	// Control can overtake packets on the separate data connection. Its new
	// epoch starts an independent sequence, so abandoned packets leave no gaps.
	q.reliable, q.hoverSequence, q.barrier = 0, 0, 0
	q.paused = true
	q.cancel()
	q.frames = nil
	q.stopExpiryLocked()
	q.hover = nil
	q.held = make(map[string]bool)
	return q.ctx, q.done, nil
}
func (q *dedicatedInputQueue) resume(next int) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed || q.parent.Err() != nil || !q.paused || next != q.control {
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

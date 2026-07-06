package agent

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/elicify-ai/omnipus/pkg/logger"
)

const defaultEventSubscriberBuffer = 16

// EventSubscription identifies a subscriber channel returned by EventBus.Subscribe.
type EventSubscription struct {
	ID uint64
	C  <-chan Event
}

type eventSubscriber struct {
	ch chan Event
}

// EventBus is a lightweight multi-subscriber broadcaster for agent-loop events.
type EventBus struct {
	mu      sync.RWMutex
	subs    map[uint64]eventSubscriber
	nextID  uint64
	closed  bool
	dropped [eventKindCount]atomic.Int64
}

// NewEventBus creates a new in-process event broadcaster.
func NewEventBus() *EventBus {
	return &EventBus{
		subs: make(map[uint64]eventSubscriber),
	}
}

// Subscribe registers a new subscriber with the requested channel buffer size.
// A non-positive buffer uses the default size.
func (b *EventBus) Subscribe(buffer int) EventSubscription {
	if buffer <= 0 {
		buffer = defaultEventSubscriberBuffer
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		ch := make(chan Event)
		close(ch)
		return EventSubscription{C: ch}
	}

	b.nextID++
	id := b.nextID
	ch := make(chan Event, buffer)
	b.subs[id] = eventSubscriber{ch: ch}
	return EventSubscription{ID: id, C: ch}
}

// Unsubscribe removes a subscriber and closes its channel.
func (b *EventBus) Unsubscribe(id uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()

	sub, ok := b.subs[id]
	if !ok {
		return
	}

	delete(b.subs, id)
	close(sub.ch)
}

// Emit broadcasts an event to all current subscribers without blocking.
// When a subscriber channel is full, the event is dropped for that subscriber.
func (b *EventBus) Emit(evt Event) {
	if evt.Time.IsZero() {
		evt.Time = time.Now()
	}

	b.mu.RLock()
	defer b.mu.RUnlock()

	if b.closed {
		return
	}

	for _, sub := range b.subs {
		select {
		case sub.ch <- evt:
		default:
			if evt.Kind < eventKindCount {
				b.dropped[evt.Kind].Add(1)
			}
			// Most kinds are high-frequency and lossy by design, so a drop is
			// debug-level. State-critical kinds (see isStateCriticalEventKind) are
			// low-frequency and a drop leaves the SPA showing stale state until the
			// next refresh, so surface those at warn.
			if isStateCriticalEventKind(evt.Kind) {
				logger.WarnCF(
					"eventbus",
					"dropped state-critical event frame (subscriber buffer full); SPA may show stale state until refresh",
					map[string]any{"event_kind": evt.Kind},
				)
			} else {
				logger.DebugCF(
					"eventbus",
					"event dropped, subscriber buffer full",
					map[string]any{"event_kind": evt.Kind},
				)
			}
		}
	}
}

// isStateCriticalEventKind reports whether a dropped event of this kind should
// be logged at WARN rather than DEBUG. These are low-frequency, state-critical
// frames whose loss leaves the SPA showing stale state until a manual refresh:
//   - EventKindWhatsAppPairing: a dropped QR leaves the pairing screen blank (#283).
//   - EventKindNotification: a dropped schedule_failed alert means the live bell
//     never updates (M1) — same class as #283.
func isStateCriticalEventKind(kind EventKind) bool {
	switch kind {
	case EventKindWhatsAppPairing, EventKindNotification, EventKindTaskStatusChanged:
		return true
	default:
		return false
	}
}

// Dropped returns the number of dropped events for a given kind.
func (b *EventBus) Dropped(kind EventKind) int64 {
	if kind >= eventKindCount {
		return 0
	}
	return b.dropped[kind].Load()
}

// Close closes all subscriber channels and stops future broadcasts.
func (b *EventBus) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return
	}

	b.closed = true
	for id, sub := range b.subs {
		close(sub.ch)
		delete(b.subs, id)
	}
}

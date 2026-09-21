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

// mustNotDropEventKindTimeout bounds the RETRY WINDOW FOR THE WHOLE Emit CALL
// (not per-subscriber — see the shared deadline in Emit) that
// state-critical one-shot events get when a subscriber's channel is full.
// Emit runs synchronously on the caller's own goroutine, so this cannot be
// long or unbounded: a slow, or
// disconnected-but-not-yet-cleaned-up, WebSocket subscriber must never be
// able to stall a live turn. A low-hundreds-of-ms budget is enough for the
// forwarder goroutine to catch up on a transient burst without risking a
// user-visible hang; unlike sendRawFrameBytes's 5s critical-frame wait
// (pkg/gateway/websocket.go), which blocks only an isolated per-connection
// forwarder goroutine, never the turn itself.
const mustNotDropEventKindTimeout = 250 * time.Millisecond

// Emit broadcasts an event to all current subscribers. Most event kinds are
// best-effort: a full subscriber channel drops the event for that subscriber
// with no retry. mustNotDropEvent identifies one-shot events that instead get
// one bounded blocking retry, because nothing else resends them live. This
// includes sub-turn span boundaries and identified delegated-task limit
// notices; losing either leaves the live SPA incomplete until transcript
// replay.
//
// The retry itself happens OUTSIDE b.mu (2026-07-31 review finding): holding
// RLock across a blocking per-subscriber wait would block any pending
// Subscribe/Unsubscribe/Close (Go's RWMutex blocks new readers once a writer
// is waiting, to prevent writer starvation) — which in turn blocks every
// OTHER concurrent Emit call, for every event kind, on this bus. That is
// exactly the "stall a live turn" failure this mechanism exists to prevent,
// just moved to a different caller. So: snapshot the subscriber list and
// classify each under the lock (cheap, non-blocking), release the lock, then
// do the retries. The retry budget is ONE deadline shared across every
// subscriber needing it, not mustNotDropEventKindTimeout per subscriber —
// otherwise N full subscribers cost N*timeout serially on the emitting
// goroutine instead of a single bounded window.
func (b *EventBus) Emit(evt Event) {
	if evt.Time.IsZero() {
		evt.Time = time.Now()
	}
	mustRetry := mustNotDropEvent(evt)

	b.mu.RLock()
	if b.closed {
		b.mu.RUnlock()
		// No subscribers remain to notify (Close closes and clears them all),
		// but a must-not-drop kind emitted during/after shutdown (e.g. a
		// straggling sub-turn's deferred cleanup racing AgentLoop.Close) must
		// still be observable rather than vanishing with zero trace — the
		// same "must be observable" invariant as every other drop path below.
		if mustRetry {
			if evt.Kind < eventKindCount {
				b.dropped[evt.Kind].Add(1)
			}
			logger.WarnCF(
				"eventbus",
				"dropped must-not-drop event: bus already closed",
				eventDropDetails(evt),
			)
		}
		return
	}

	type pendingRetry struct {
		ch chan Event
	}
	var needsRetry []pendingRetry

	for _, sub := range b.subs {
		select {
		case sub.ch <- evt:
			continue
		default:
		}
		if mustRetry {
			needsRetry = append(needsRetry, pendingRetry(sub))
			continue
		}
		b.recordDrop(evt)
	}
	b.mu.RUnlock()

	if len(needsRetry) == 0 {
		return
	}

	// One shared deadline for every subscriber needing a retry, computed
	// once, so the total added latency on this goroutine is bounded by
	// mustNotDropEventKindTimeout regardless of how many subscribers are
	// full — not that timeout multiplied by len(needsRetry).
	deadline := time.Now().Add(mustNotDropEventKindTimeout)
	for _, p := range needsRetry {
		if trySendUntil(p.ch, evt, deadline) {
			continue
		}
		b.recordDrop(evt)
	}
}

// trySendUntil attempts to send evt on ch until deadline, recovering from a
// send on a channel that Unsubscribe/Close closed concurrently after Emit
// released b.mu (send-on-closed-channel panics; a select cannot distinguish
// "closed" from "ready to send" ahead of time, so recover is the only safe
// way to detect it post hoc). A deadline already in the past makes
// time.After fire immediately — a non-blocking best-effort try, not an error.
func trySendUntil(ch chan Event, evt Event, deadline time.Time) (delivered bool) {
	defer func() {
		if recover() != nil {
			delivered = false
		}
	}()
	select {
	case ch <- evt:
		return true
	case <-time.After(time.Until(deadline)):
		return false
	}
}

// recordDrop increments the per-kind counter and logs at the severity this
// event kind's drop warrants. Most kinds are high-frequency and lossy by
// design, so a drop is debug-level. State-critical kinds (see
// isStateCriticalEventKind) are low-frequency and a drop leaves the SPA
// showing stale state until the next refresh, so surface those at warn.
// mustNotDropEvent events reach here only after the bounded blocking
// retry in Emit also failed, so they always warn too — same "must be
// observable" reasoning as isStateCriticalEventKind, but the retry means the
// buffer was full for a sustained period, not just a single instant.
func (b *EventBus) recordDrop(evt Event) {
	kind := evt.Kind
	if kind < eventKindCount {
		b.dropped[kind].Add(1)
	}
	switch {
	case mustNotDropEvent(evt):
		logger.WarnCF(
			"eventbus",
			"dropped must-not-drop event after bounded blocking retry (subscriber buffer still full)",
			eventDropDetails(evt),
		)
	case isStateCriticalEventKind(kind):
		logger.WarnCF(
			"eventbus",
			"dropped state-critical event frame (subscriber buffer full); SPA may show stale state until refresh",
			eventDropDetails(evt),
		)
	default:
		logger.DebugCF(
			"eventbus",
			"event dropped, subscriber buffer full",
			eventDropDetails(evt),
		)
	}
}

func eventDropDetails(evt Event) map[string]any {
	details := map[string]any{"event_kind": evt.Kind}
	if payload, ok := evt.Payload.(ErrorPayload); ok && payload.Code != "" {
		details["error_code"] = payload.Code
	}
	return details
}

// mustNotDropEvent reports whether Emit should give an event a bounded
// blocking retry (mustNotDropEventKindTimeout) before dropping it on a full
// subscriber channel:
//   - EventKindSubTurnSpawn / EventKindSubTurnEnd: the one-shot announcement
//     that opens/closes a subagent's span in the SPA (FR-H-004). Unlike
//     high-frequency kinds (tool exec, LLM deltas), losing one isn't just a
//     missed intermediate update — there is no later event that repeats it.
//   - EventKindError carrying CodeDelegatedTaskLimit: the sole identified
//     operator notice for a delegated task's timeout or iteration limit. It
//     is payload-specific so ordinary error events retain their existing
//     best-effort behavior.
func mustNotDropEvent(evt Event) bool {
	switch evt.Kind {
	case EventKindSubTurnSpawn, EventKindSubTurnEnd:
		return true
	case EventKindError:
		payload, ok := evt.Payload.(ErrorPayload)
		return ok && payload.Code == string(CodeDelegatedTaskLimit)
	default:
		return false
	}
}

// isStateCriticalEventKind reports whether a dropped event of this kind should
// be logged at WARN rather than DEBUG. These are low-frequency, state-critical
// frames whose loss leaves the SPA showing stale state until a manual refresh:
//   - EventKindWhatsAppPairing: a dropped QR leaves the pairing screen blank (#283).
//   - EventKindNotification: a dropped schedule_failed alert means the live bell
//     never updates (M1) — same class as #283.
//   - EventKindTaskRunStatus: a dropped open/close transition (ADR-050 §3.8)
//     leaves a calendar occurrence chip stuck at its previous state (e.g.
//     "in progress" after the run actually finished) until a manual refresh —
//     same class as EventKindTaskStatusChanged.
func isStateCriticalEventKind(kind EventKind) bool {
	switch kind {
	case EventKindWhatsAppPairing, EventKindNotification, EventKindTaskStatusChanged, EventKindTaskRunStatus:
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

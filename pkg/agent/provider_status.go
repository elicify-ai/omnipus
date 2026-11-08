package agent

// Issue #1478 — "the model hasn't responded for N seconds, still waiting…".
//
// A provider can go silent mid-call for up to its silence limit (5 minutes by
// default, per-model stream_stall_timeout) before the call is ended. That
// limit is unchanged; what is new is that the chat is told. Each provider
// round (callProviderOnce) runs one providerSilenceWatch:
//
//   - activity comes from the streaming silence monitor itself
//     (common.WithStreamActivity: every byte of any kind, including
//     reasoning and keep-alives), from a scheduled #1280 retry (the retry
//     line replaces the notice), and from the call returning;
//   - after providerSilenceNoticeAfter without activity it publishes ONE
//     provider_status "waiting" event (silent_since, and stall_at when the
//     stream's silence limit is known);
//   - the next activity, or the end of the round, publishes "responding",
//     which clears the notice.
//
// The same event carries #1280's "recovered" note: a call that succeeded
// after automatic retries (FallbackResult.Retries).

import (
	"context"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers/common"
)

// Provider-status states (the provider_status frame's state enum).
const (
	ProviderStatusWaiting    = "waiting"
	ProviderStatusResponding = "responding"
	ProviderStatusRecovered  = "recovered"
)

// providerSilenceNoticeAfter is how long a provider may be silent before the
// chat is told (issue #1478). providerSilenceCheckEvery is the watch's poll
// interval. Package variables so tests can shorten them.
var (
	providerSilenceNoticeAfter = 30 * time.Second //nolint:gochecknoglobals // test seam
	providerSilenceCheckEvery  = time.Second      //nolint:gochecknoglobals // test seam
)

// ProviderStatusPayload is the EventKindProviderStatus payload. The hub
// builds the provider_status frame from these named fields only.
type ProviderStatusPayload struct {
	State       string    // ProviderStatusWaiting / Responding / Recovered
	SessionID   string    // the session whose turn is waiting
	SentAt      time.Time // server wall clock at emission
	SilentSince time.Time // waiting: last provider activity (or the round start)
	StallAt     time.Time // waiting: when the silence limit ends the call; zero = unknown
	Retries     int       // recovered: automatic retries the call needed
}

// providerSilenceWatch tracks one provider round's silence.
type providerSilenceWatch struct {
	rt *agentLoopRunTurn

	mu           sync.Mutex
	lastActivity time.Time
	stallLimit   time.Duration // the current stream's silence limit; 0 = not streaming
	waiting      bool          // a waiting notice is showing

	stopOnce sync.Once
	stopCh   chan struct{}
	doneCh   chan struct{}
}

// startProviderSilenceWatch starts the round's watch and returns the ctx the
// provider calls must use (it carries the stream-activity observer) and the
// stop function the round defers.
func (rt *agentLoopRunTurn) startProviderSilenceWatch(ctx context.Context) (context.Context, func()) {
	w := &providerSilenceWatch{
		rt:           rt,
		lastActivity: time.Now(),
		stopCh:       make(chan struct{}),
		doneCh:       make(chan struct{}),
	}
	rt.providerSilence = w
	go w.run()
	return common.WithStreamActivity(ctx, w.activity), func() {
		w.stop()
		if rt.providerSilence == w {
			rt.providerSilence = nil
		}
	}
}

func (w *providerSilenceWatch) run() {
	defer close(w.doneCh)
	ticker := time.NewTicker(providerSilenceCheckEvery)
	defer ticker.Stop()
	for {
		select {
		case <-w.stopCh:
			return
		case now := <-ticker.C:
			w.mu.Lock()
			due := !w.waiting && now.Sub(w.lastActivity) >= providerSilenceNoticeAfter
			since, limit := w.lastActivity, w.stallLimit
			if due {
				w.waiting = true
			}
			w.mu.Unlock()
			if due {
				var stallAt time.Time
				if limit > 0 {
					stallAt = since.Add(limit)
				}
				w.rt.emitProviderStatus(ProviderStatusPayload{State: ProviderStatusWaiting, SilentSince: since, StallAt: stallAt})
			}
		}
	}
}

// activity records provider activity (the streaming monitor's start and
// every consumed event) and clears a showing notice.
func (w *providerSilenceWatch) activity(stallLimit time.Duration) {
	w.mu.Lock()
	w.lastActivity = time.Now()
	w.stallLimit = stallLimit
	cleared := w.waiting
	w.waiting = false
	w.mu.Unlock()
	if cleared {
		w.rt.emitProviderStatus(ProviderStatusPayload{State: ProviderStatusResponding})
	}
}

// retryScheduled restarts the silence clock at the next call of a #1280
// retry: the wait itself is shown by the provider_retry line, never as
// silence. A showing notice is cleared.
func (w *providerSilenceWatch) retryScheduled(retryAt time.Time) {
	w.mu.Lock()
	w.lastActivity = retryAt
	w.stallLimit = 0
	cleared := w.waiting
	w.waiting = false
	w.mu.Unlock()
	if cleared {
		w.rt.emitProviderStatus(ProviderStatusPayload{State: ProviderStatusResponding})
	}
}

// stop ends the watch; a notice still showing is cleared (the call returned).
func (w *providerSilenceWatch) stop() {
	w.stopOnce.Do(func() {
		close(w.stopCh)
		<-w.doneCh
		w.mu.Lock()
		cleared := w.waiting
		w.waiting = false
		w.mu.Unlock()
		if cleared {
			w.rt.emitProviderStatus(ProviderStatusPayload{State: ProviderStatusResponding})
		}
	})
}

// noteProviderRecovered publishes #1280's "recovered" note when the round's
// answering call needed automatic retries.
func (rt *agentLoopRunTurn) noteProviderRecovered(retries int) {
	if retries > 0 {
		rt.emitProviderStatus(ProviderStatusPayload{State: ProviderStatusRecovered, Retries: retries})
	}
}

// emitProviderStatus stamps the session and server time and emits the event.
func (rt *agentLoopRunTurn) emitProviderStatus(p ProviderStatusPayload) {
	if rt == nil || rt.al == nil || rt.ts == nil {
		return
	}
	p.SessionID = string(rt.ts.routingSessionID)
	p.SentAt = time.Now()
	rt.al.emitEvent(EventKindProviderStatus, rt.ts.eventMeta("runTurn", "turn.llm.provider_status"), p)
}

package gateway

import (
	"context"
	"encoding/hex"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
)

const (
	browserInputTimingLimit         = 512
	browserInputCriticalTimingLimit = 64
)

// browserInputTiming is opt-in local instrumentation, never a wire payload.
// Offsets share received's Go monotonic clock. The wall timestamp is only a
// coarse correlation aid; it does not establish synchronization with Chrome.
type browserInputTiming struct {
	inputEpoch, controlEpoch, reliableSeq, hoverSeq int
	firstReliableSeq, lastReliableSeq, inputCount   int
	compact                                         bool
	stages                                          [12]float64
	seen                                            uint16
	budget                                          [2]float64
	budgetSeen                                      uint8
	sampling                                        *browserInputTimingSampling
	received                                        time.Time
	ordinal                                         uint64
	kind                                            string
	captureID                                       string
	generation                                      int
	offsets                                         map[string]float64
	outcome                                         string
	benignReason                                    string
	now                                             func() time.Time
	emit                                            func(...any)
}

func newBrowserInputTiming(enabled bool, sequence *atomic.Uint64, in generated.BrowserInputFrame, received time.Time) *browserInputTiming {
	if !enabled || !browserTimingGesture(in.Kind) {
		return nil
	}
	var admitted uint64
	for {
		ordinal := sequence.Load()
		if ordinal >= browserInputTimingLimit {
			return nil
		}
		if sequence.CompareAndSwap(ordinal, ordinal+1) {
			admitted = ordinal + 1
			break
		}
	}
	if received.IsZero() {
		received = time.Now()
	}
	p := makeBrowserInputTiming(in, received)
	p.ordinal = admitted
	p.offsets = make(map[string]float64)
	return p
}

func makeBrowserInputTiming(in generated.BrowserInputFrame, received time.Time) *browserInputTiming {
	p := &browserInputTiming{received: received, kind: in.Kind, outcome: "not_dispatched", now: time.Now, emit: func(args ...any) { slog.Info("browser input timing", args...) }}
	p.inputEpoch, p.controlEpoch = boundedTimingCounter(in.InputEpoch), boundedTimingCounter(in.ControlEpoch)
	p.reliableSeq, p.hoverSeq = boundedTimingCounter(in.ReliableSeq), boundedTimingCounter(in.HoverSeq)
	p.firstReliableSeq, p.lastReliableSeq, p.inputCount = p.reliableSeq, p.reliableSeq, 1

	// Frame identifiers are generated hex digests; never log arbitrary supplied
	// strings, text, keys, coordinates, URLs, session IDs, or error messages.
	if in.CaptureId != nil && len(*in.CaptureId) == 64 {
		if _, err := hex.DecodeString(*in.CaptureId); err == nil {
			p.captureID = *in.CaptureId
		}
	}
	if in.CaptureGeneration != nil && *in.CaptureGeneration > 0 {
		p.generation = *in.CaptureGeneration
	}
	return p
}

var browserTimingStages = [...]string{"queue_started", "live_entry", "tab_gate_wait", "tab_gate_acquired", "input_gate_wait", "input_gate_acquired", "admission_done", "mapping_start", "mapping_done", "cdp_start", "cdp_done", "finished"}

func browserTimingGesture(kind string) bool {
	switch kind {
	case "mouse_move", "mouse_down", "mouse_up", "wheel", "key_down", "key_up", "text":
		return true
	}
	return false
}

// maxBrowserCounter is JavaScript's Number.MAX_SAFE_INTEGER: the largest
// integral counter a browser can emit exactly, whether it names an input
// epoch, a control epoch, a sequence, a capture generation, or an offer.
// Typed int64 so the constant also compiles where int is 32 bits; there the
// widened comparisons are vacuously satisfied, which loses nothing — a 32-bit
// int cannot hold a value near 2^53 in the first place, and every path
// feeding these guards (JSON decoding into int fields, the binary input
// decoder's 32-bit integral cap) has already rejected anything larger before
// the bound runs.
const maxBrowserCounter int64 = 1<<53 - 1

func boundedTimingCounter(v *int) int {
	if v != nil && *v >= 0 && int64(*v) <= maxBrowserCounter {
		return *v
	}
	return 0
}

// Initial quotas reserve critical diagnostics despite routine rejections or cancellations.
// Later inputs retain fixed-size summaries for a rate-limited rolling failure window.
type browserInputTimingSampling struct {
	general, critical, ordinal atomic.Uint64
	inFlight                   atomic.Int64
	mu                         sync.Mutex
	// Fixed-size sanitized snapshots only: no callbacks, contexts, or raw inputs.
	recent            [16]browserInputTiming
	next, count       int
	lastWindow        time.Time
	completionThrough uint64
	completionPending bool
}

func takeBrowserTimingQuota(counter *atomic.Uint64, limit uint64) bool {
	for {
		n := counter.Load()
		if n >= limit {
			return false
		}
		if counter.CompareAndSwap(n, n+1) {
			return true
		}
	}
}
func (s *browserInputTimingSampling) begin(in generated.BrowserInputFrame, received time.Time) *browserInputTiming {
	if !browserTimingGesture(in.Kind) {
		return nil
	}
	p := makeBrowserInputTiming(in, received)
	p.compact, p.sampling = true, s
	p.ordinal = s.ordinal.Add(1)
	s.inFlight.Add(1)
	return p
}
func (p *browserInputTiming) mark(stage string) {
	if p == nil {
		return
	}
	offset := float64(p.now().Sub(p.received)) / float64(time.Millisecond)
	if !p.compact {
		p.offsets[stage] = offset
		return
	}
	for i, name := range browserTimingStages {
		if stage == name {
			p.stages[i] = offset
			p.seen |= 1 << i
			return
		}
	}
}
func (p *browserInputTiming) observeBudget(stage string, remaining time.Duration) {
	if p == nil {
		return
	}
	index := 0
	if stage == "cdp_start" {
		index = 1
	} else if stage != "live_budget" {
		return
	}
	p.budget[index] = max(0, float64(remaining)/float64(time.Millisecond))
	p.budgetSeen |= 1 << index
}
func browserTimingOutcome(err error) string {
	switch {
	case err == nil:
		return "completed"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case browser.IsBenignLiveInputError(err):
		return "benign_rejection"
	default:
		return "dispatch_error"
	}
}

// Only these fixed labels leave the process; the source message is never logged.
func browserTimingBenignReason(message string) string {
	switch {
	case strings.HasPrefix(message, "browser live: input rate limit exceeded"):
		return "rate_limited"
	case strings.HasPrefix(message, "browser live: page change pending"):
		return "page_pending"
	case strings.HasPrefix(message, "browser live: displayed "), strings.HasPrefix(message, "browser live: input target changed"), strings.HasPrefix(message, "browser live: no capture for this panel"):
		return "frame_changed"
	default:
		return "other"
	}
}

func (p *browserInputTiming) finish() {
	if p == nil {
		return
	}
	p.mark("finished")
	if p.sampling != nil {
		p.sampling.finish(p)
		return
	}
	p.emitRecord()
}

// finish keeps normal logging finite while retaining a small recent window for
// failures or slow dispatches later in a long connection. Emission is outside
// the mutex and uses the current caller's logger, never a retained callback.
func (s *browserInputTimingSampling) finish(p *browserInputTiming) {
	critical := p.outcome == "deadline_exceeded" || p.outcome == "dispatch_error"
	counter, limit := &s.general, uint64(browserInputTimingLimit)
	if critical {
		counter, limit = &s.critical, browserInputCriticalTimingLimit
	}
	admitted := takeBrowserTimingQuota(counter, limit)
	now := p.now()
	slow := now.Sub(p.received) >= 250*time.Millisecond
	var window []browserInputTiming
	s.mu.Lock()
	completion := s.completionPending && p.ordinal <= s.completionThrough
	if completion {
		s.completionPending = false
	}
	s.inFlight.Add(-1)
	if critical && s.lastWindow.IsZero() {
		s.lastWindow = now
	}
	if !admitted && !completion && (critical || slow) && (s.lastWindow.IsZero() || now.Sub(s.lastWindow) >= 10*time.Second) {
		window = make([]browserInputTiming, 0, s.count+1)
		for i := 0; i < s.count; i++ {
			window = append(window, s.recent[(s.next-s.count+i+len(s.recent))%len(s.recent)])
		}
		window = append(window, *p)
		s.lastWindow = now
	}
	snapshot := *p
	snapshot.now, snapshot.emit, snapshot.sampling, snapshot.offsets = nil, nil, nil, nil
	s.recent[s.next] = snapshot
	s.next = (s.next + 1) % len(s.recent)
	if s.count < len(s.recent) {
		s.count++
	}
	s.mu.Unlock()
	if admitted || completion {
		p.emitRecord()
	}
	for i := range window {
		window[i].emit = p.emit
		window[i].emitRecord()
	}
}

func (p *browserInputTiming) emitRecord() {
	if p.compact {
		p.offsets = make(map[string]float64, len(browserTimingStages))
		for i, name := range browserTimingStages {
			if p.seen&(1<<i) != 0 {
				p.offsets[name] = p.stages[i]
			}
		}
	}
	args := []any{"input_ordinal", p.ordinal, "kind", p.kind, "input_epoch", p.inputEpoch, "control_epoch", p.controlEpoch, "reliable_seq", p.reliableSeq, "hover_seq", p.hoverSeq, "first_reliable_seq", p.firstReliableSeq, "last_reliable_seq", p.lastReliableSeq, "input_count", p.inputCount, "capture_id", p.captureID, "capture_generation", p.generation, "received_unix_ms", p.received.UnixMilli(), "stage_offsets_ms", p.offsets, "outcome", p.outcome}
	if p.benignReason != "" {
		args = append(args, "benign_reason", p.benignReason)
	}
	if p.budgetSeen&1 != 0 {
		args = append(args, "live_budget_ms", p.budget[0])
	}
	if p.budgetSeen&2 != 0 {
		args = append(args, "cdp_remaining_budget_ms", p.budget[1])
	}
	p.emit(args...)
}

// failure is the connection-level hook; it must also work without an in-flight input.
func (s *browserInputTimingSampling) failure(reason string, now time.Time, emit func(...any)) {
	s.mu.Lock()
	if !s.lastWindow.IsZero() && now.Sub(s.lastWindow) < 10*time.Second {
		s.mu.Unlock()
		return
	}
	window := make([]browserInputTiming, 0, s.count)
	for i := 0; i < s.count; i++ {
		window = append(window, s.recent[(s.next-s.count+i+len(s.recent))%len(s.recent)])
	}
	s.lastWindow = now
	// One completion already admitted before this callback may arrive afterward
	// when source cancellation unblocks Chrome. Keep that final measurement too.
	s.completionThrough = s.ordinal.Load()
	s.completionPending = s.inFlight.Load() > 0
	s.mu.Unlock()
	for i := range window {
		window[i].emit = emit
		window[i].emitRecord()
	}
	emit("event", "failure_window", "reason", dedicatedFailureLogReason(reason))
}

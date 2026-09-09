package gateway

import (
	"encoding/hex"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

const browserInputTimingLimit = 512

// browserInputTiming is opt-in local instrumentation, never a wire payload.
// Offsets share received's Go monotonic clock. The wall timestamp is only a
// coarse correlation aid; it does not establish synchronization with Chrome.
type browserInputTiming struct {
	received   time.Time
	ordinal    uint64
	kind       string
	captureID  string
	generation int
	offsets    map[string]float64
	outcome    string
	now        func() time.Time
	emit       func(...any)
}

func newBrowserInputTiming(enabled bool, sequence *atomic.Uint64, in generated.BrowserInputFrame, received time.Time) *browserInputTiming {
	if !enabled || (in.Kind != "mouse_down" && in.Kind != "mouse_up") {
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
	p := &browserInputTiming{received: received, ordinal: admitted, kind: in.Kind, offsets: make(map[string]float64), outcome: "not_dispatched", now: time.Now, emit: func(args ...any) { slog.Info("browser input timing", args...) }}
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

func (p *browserInputTiming) mark(stage string) {
	if p != nil {
		p.offsets[stage] = float64(p.now().Sub(p.received)) / float64(time.Millisecond)
	}
}

func (p *browserInputTiming) finish() {
	if p == nil {
		return
	}
	p.mark("finished")
	p.emit("input_ordinal", p.ordinal, "kind", p.kind, "capture_id", p.captureID, "capture_generation", p.generation, "received_unix_ms", p.received.UnixMilli(), "stage_offsets_ms", p.offsets, "outcome", p.outcome)
}

package protocoltypes

import "log/slog"

// SafeInvoke calls cb, converting a panic into a logged event instead of an
// unwind through the caller.
//
// Providers invoke the progress callback SYNCHRONOUSLY from inside their SSE
// read loop. Without this, a panic in a consumer's handler unwinds through the
// stream parser and kills the whole turn — which is strictly worse than the
// blindness the callback exists to fix. The handler is a monitoring signal; it
// must never be able to take down the work it is monitoring.
//
// This is ADR-059 AC-06, decided (A4-4) as a real guard rather than a written
// waiver. The waiver was tempting because today's only handler is in-tree and
// provably cannot panic — four atomic stores — but that argument holds only
// while it stays the sole handler, and the cost of being wrong is a dead turn.
//
// A nil cb is a no-op, so callers do not need their own nil check.
func SafeInvoke(cb OnToolCallProgress, p ToolCallProgress) {
	if cb == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			// Logged, not swallowed: a panicking handler is a real defect in
			// the consumer and must be visible. It just must not be fatal to
			// the turn.
			slog.Error("providers: tool-call progress handler panicked; progress suppressed for this delta",
				"panic", r, "tool", p.Name, "args_bytes", p.ArgsBytes)
		}
	}()
	cb(p)
}

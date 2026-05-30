// Package audit — package-level counters for audit-skip events.
//
// B1.2(e) / H4-BK: these counters track *unexpected write loss* — the
// operator-configured logger is wired but a write to it failed.  They must
// NOT be incremented when audit is explicitly disabled (AuditLog=false /
// auditLogger==nil) by operator choice.  The two signals are distinct:
//
//   - audit_logger   (in /health) — config-state flag: "is a logger wired?"
//   - audit_skipped  (this file)  — runtime health signal: "a configured
//     logger failed mid-flight and a write was silently dropped."
//
// When a write fails with AuditFailClosed=false, the tool emits slog.Error
// and calls IncSkipped so /health and /metrics can surface the degraded-audit
// state.  When auditLogger is nil (deliberately disabled), IncSkipped must
// not be called — doing so conflates "broken" with "disabled" and causes
// audit_degraded to trip permanently in explicitly-disabled deployments.
//
// Why here? The tools package can't depend on gateway's metrics
// infrastructure without an import cycle. The audit package is the
// natural home: it's the layer whose write failed, and every package
// that emits audit already imports pkg/audit. Health and gateway-metrics
// surface the value via a typed accessor (Snapshot) rather than
// re-exporting the atomic so callers can't tamper with it.

package audit

import "sync/atomic"

// auditSkippedCounters is the package-level state for B1.2(e). Stored as
// an atomic counter map keyed by "tool|decision" so we can support multiple
// labels without taking a mutex on the hot path. There are at most a handful
// of unique label combinations in practice (web_serve|allow, web_serve|deny,
// etc.), so a fixed-size struct is simpler than a sync.Map.
//
// Each tool that skips audit calls IncSkipped(tool, decision); the counter
// is read at /health and /metrics request time via SnapshotSkipped().
var auditSkippedCounters skippedCounters

// skippedCounters wraps the small set of label tuples we care about.
// Anything outside the named buckets is summed into "other" so we don't
// silently lose counts on misconfiguration.
type skippedCounters struct {
	webServeAllow atomic.Int64
	webServeDeny  atomic.Int64
	other         atomic.Int64
}

// IncSkipped increments the audit-skipped counter for the given (tool,
// decision) pair. Both labels are matched as case-sensitive strings so
// callers must pass canonical values (the audit.DecisionAllow / DecisionDeny
// constants are recommended).
//
// The counter is process-lifetime cumulative — there is no reset, by design.
// /health surfaces the absolute value; operators read deltas across two
// scrapes if they want a rate.
func IncSkipped(tool, decision string) {
	switch tool {
	case "web_serve":
		switch decision {
		case DecisionAllow:
			auditSkippedCounters.webServeAllow.Add(1)
		case DecisionDeny:
			auditSkippedCounters.webServeDeny.Add(1)
		default:
			auditSkippedCounters.other.Add(1)
		}
	default:
		auditSkippedCounters.other.Add(1)
	}
}

// SkippedSnapshot is a point-in-time read of the audit-skipped counters
// suitable for embedding in a /health or /metrics response. Field zero
// values are valid (no skips have occurred). Total is the sum of the
// labeled counters and is provided for convenience.
type SkippedSnapshot struct {
	WebServeAllow int64 `json:"web_serve_allow"`
	WebServeDeny  int64 `json:"web_serve_deny"`
	Other         int64 `json:"other"`
	Total         int64 `json:"total"`
}

// SnapshotSkipped returns the current counter values atomically. Each Load
// is independent, so a SkippedSnapshot returned during heavy concurrent
// IncSkipped calls may be slightly skewed across labels — acceptable for
// observability, never relied on for correctness.
func SnapshotSkipped() SkippedSnapshot {
	a := auditSkippedCounters.webServeAllow.Load()
	d := auditSkippedCounters.webServeDeny.Load()
	o := auditSkippedCounters.other.Load()
	return SkippedSnapshot{
		WebServeAllow: a,
		WebServeDeny:  d,
		Other:         o,
		Total:         a + d + o,
	}
}

// ResetSkippedForTest zeroes the counters. Test-only helper; production
// code must never reset (operators rely on monotonic counts).
func ResetSkippedForTest() {
	auditSkippedCounters.webServeAllow.Store(0)
	auditSkippedCounters.webServeDeny.Store(0)
	auditSkippedCounters.other.Store(0)
}

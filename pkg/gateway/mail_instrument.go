package gateway

// mail_instrument.go — the gateway's request-scoped instrument envelope core
// (w5-integration wave; w5 spec US-7/MC-18; landing-order register row 17).
//
// w6-proof §6.1 is the SOLE publisher of the record shape; this file is the
// emitter envelope it assigned to w5-integration. It adds no field and no
// second definition: MailInstrumentSample mirrors w6 §6.1's frozen fields,
// the pool sub-fields arrive from W1 (SessionsConfig.Instrument's
// PoolInstrumentSample), and every emission goes to the injected sink AND
// the dedicated structured-log key "mail.operation" so measurement receipts
// are auditable from logs (the concrete key name is w5's to decide per
// w6 §6.1's emission rules; this file records it).
//
// Fields are closed values only: operation (w6's enum), pair_ref (opaque
// pair ID + generation fragments), source/hit/outcome from the frozen
// enums, timings and counts. Nothing content-bearing ever passes through
// here — no subjects, addresses, folder names, Message-IDs or raw upstream
// error text (US-7.1/US-7.2).

import (
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/email"
)

// mailInstrumentOperationOf maps the gateway's internal budget operation
// names onto w6-proof §6.1's frozen operation enum. An operation with NO
// frozen member maps to "": no record is emitted rather than a mislabeled
// one (the §13-Q8 principle — removal's missing member is the named
// publisher request; discovery has no gateway route yet). The map lives at
// the single emission seam so the operation vocabulary has one definition.
func mailInstrumentOperationOf(op string) string {
	switch op {
	case "listMailFolders":
		return "folders"
	case "listMailMessages":
		return "list"
	case "getMailMessage", "open":
		return "open"
	case "getMailAttachment", "mintMailAttachmentPreviewPart", "serveMailAttachmentPart":
		return "attachment_read"
	case "mintMailAttachmentPreviewMeta":
		return "attachment_metadata"
	case "saveMailAttachmentToLibrary":
		return "attachment_save"
	case "seen":
		return "seen"
	case "summary":
		// w6 §6.1's frozen enum names summary (its §6.1 operation row), and
		// w5's emission obligation (US-7.6/MC-18, register row 17) names the
		// summary boundary. Dropping it silently emitted zero summary
		// records — the exact missing-record failure US-7.8 invalidates a
		// campaign over. The map carries every frozen member the gateway
		// actually emits; removal stays absent (§13 Q8, publisher-owned).
		return "summary"
	default:
		return ""
	}
}

// mailInstrumentUnknown marks an instrument member the emitting seam cannot
// truthfully supply. It is an explicit unknown, never a value that reads as a
// measurement: a count of -1 is impossible as a measurement, while 0 on a
// dialing boundary is indistinguishable from a cache hit's true 0 (the exact
// fabricated-zero case the proof-wave audit called out). The pool sub-fields
// carry it until W1 lands the context-carrying Instrument variant (R-3).
const mailInstrumentUnknown = -1

// mailInstrumentRows returns the optional rows member's value for handlers
// that hold a real row/message count.
func mailInstrumentRows(n int) *int {
	return &n
}

// mailInstrumentNoDial reports whether err is a typed PRE-dial refusal: the
// budget or the pool refused the operation before any server connection was
// attempted, so the record may state socket_count=0 as a true value. Errors
// that can follow a dial (upstream failures, superseded publications) are
// deliberately absent — after those, the socket count is unknown.
func mailInstrumentNoDial(err error) bool {
	if err == nil {
		return false
	}
	var bf *email.MailBackoffError
	if errors.As(err, &bf) {
		return true
	}
	return errors.Is(err, email.ErrMailBusy) ||
		errors.Is(err, email.ErrPoolBusy) ||
		errors.Is(err, email.ErrPoolSkipped)
}

// emitMailOperationTiming emits the one w6 §6.1 record for a completed
// gateway Mail operation with no rows member (boundaries where a row count is
// not meaningful). Boundaries that hold a real count call
// emitMailOperationTimingRows instead.
func (a *restAPI) emitMailOperationTiming(op, agentID, workspaceID string, started time.Time, err error, source string, hit bool) {
	a.emitMailOperationTimingRows(op, agentID, workspaceID, started, err, source, hit, nil)
}

// emitMailOperationTimingRows emits the one w6 §6.1 record for a completed
// gateway Mail operation — success AND failure alike — with the measured
// duration, the safe outcome class, and rows when the caller holds a real
// count (nil otherwise: an absent optional member, never an invented one).
//
// Truthfulness rules enforced here (the proof-wave audit's four findings):
//
//   - duration_ms is > 0 on every completed operation (MC-P1): the elapsed
//     time is rounded UP to the record's millisecond resolution, so
//     sub-millisecond work reports 1 — "under 1 ms" — instead of the
//     fabricated 0 the truncating Milliseconds() read produced.
//   - acquire_wait_ms/socket_count carry what THIS seam can truthfully
//     state. W1's Instrument callback has no per-operation context
//     (mail_runtime.go's Instrument paragraph), so the real acquisition wait
//     and socket count of a dialing operation cannot be joined onto its
//     record; they are the explicit mailInstrumentUnknown sentinel, never a
//     zero that reads as a measurement. Where no dial is PROVABLE (a
//     source-none boundary runs zero mail commands; a cache hit never
//     dialed; a typed pre-dial refusal connected to nothing), socket_count
//     is the true 0. The real per-operation values become writable when W1
//     lands the context-carrying Instrument variant (R-3); wiring today's
//     context-free callback would instead mis-attribute samples under
//     concurrency and double-emit joiner records.
//   - rows/revision/shared_flight stay absent (their omitempty encodings)
//     unless a true value exists: revision is captured by no boundary yet
//     (the revision counter's consumer seam is unlanded) and the joiner
//     marker needs W1's context-carrying seam, so neither can be populated
//     from here without fabricating.
func (a *restAPI) emitMailOperationTimingRows(op, agentID, workspaceID string, started time.Time, err error, source string, hit bool, rows *int) {
	member := mailInstrumentOperationOf(op)
	if member == "" {
		return
	}
	if started.IsZero() {
		// A boundary that never captured its start has no measured duration:
		// emitting any number would fabricate one. The record is skipped
		// loudly instead of completed with an invented timing. (Unreachable
		// today — every call site captures started before the operation —
		// but the emitter must not be the place a fabricated value is born.)
		slog.Warn("mail.operation record dropped: boundary captured no start time",
			"operation", member)
		return
	}
	durationMs := time.Since(started).Milliseconds()
	if durationMs < 1 {
		// A completed operation always consumed > 0 of the monotonic clock.
		// The record's millisecond resolution floors sub-millisecond work
		// onto 0, which reads as a measurement it is not (w6 §6.1: > 0 on
		// completion). Round UP to the resolution: 1 means "completed in
		// under 1 ms", never "instantaneous".
		durationMs = 1
	}
	sample := MailOperationSample{
		Operation:     member,
		PairRef:       a.mailRuntimeFor().mailPairRef(agentID, workspaceID),
		Source:        source,
		Hit:           hit,
		DurationMs:    durationMs,
		AcquireWaitMs: mailInstrumentUnknown,
		SocketCount:   mailInstrumentUnknown,
		Outcome:       "ok",
		Rows:          rows,
	}
	if source == "none" || hit || mailInstrumentNoDial(err) {
		// Provably no server connection was made or attempted (see the
		// comment above): zero sockets is the TRUE value here.
		sample.SocketCount = 0
	}
	if err != nil {
		sample.Outcome = email.ClassifyMailError(err)
	}
	emitMailOperation(sample)
}

// MailOperationSample is one complete per-operation record in w6-proof
// §6.1's frozen shape (the envelope, not a second definition). The record
// never crosses the gateway/SPA boundary — w6-proof §16.1 PUBLISHES rules
// the surface "in-process/log" and assigns W0 "no contract impact": the
// record is delivered only to the injected in-process sink and mirrored to
// the server's slog (key-value attrs, not a struct marshal), and the json
// field names exist to mirror the frozen shape, not to serialize it.
type MailOperationSample struct { // not-wire-format: w6-proof §16.1's frozen in-process/log observability record ("no contract impact" for W0) — emitted only to the injected Go sink and the slog mirror, never a REST/WS/SPA surface
	Operation     string `json:"operation"`
	PairRef       string `json:"pair_ref"`
	Source        string `json:"source"`
	Hit           bool   `json:"hit"`
	DurationMs    int64  `json:"duration_ms"`
	AcquireWaitMs int64  `json:"acquire_wait_ms"`
	SocketCount   int    `json:"socket_count"`
	Outcome       string `json:"outcome"`
	Rows          *int   `json:"rows,omitempty"`
	Revision      string `json:"revision,omitempty"`
	SharedFlight  *bool  `json:"shared_flight,omitempty"`
}

// MailInstrumentSink receives every completed Mail operation record. Tests
// inject a recording sink; production may inject none, in which case only
// the dedicated structured log carries the record (w6 §6.1: the sink is
// injected, the log mirror is unconditional).
type MailInstrumentSink interface {
	RecordMailOperation(sample MailOperationSample)
}

var (
	mailInstrumentMu       sync.Mutex
	mailInstrumentSinkImpl MailInstrumentSink
)

// SetMailInstrumentSink installs the process-wide sink. Boot calls it before
// the first Mail operation; replacing it at runtime is accepted (the next
// record goes to the new sink).
func SetMailInstrumentSink(s MailInstrumentSink) {
	mailInstrumentMu.Lock()
	mailInstrumentSinkImpl = s
	mailInstrumentMu.Unlock()
}

// emitMailOperation delivers one completed record to the injected sink and
// mirrors it to the dedicated structured log. It never fails an operation:
// instrumentation is observability, not a gate.
func emitMailOperation(sample MailOperationSample) {
	mailInstrumentMu.Lock()
	sink := mailInstrumentSinkImpl
	mailInstrumentMu.Unlock()
	if sink != nil {
		sink.RecordMailOperation(sample)
	}
	// Dedicated structured-log mirror (w6 §6.1's emission rules; the key is
	// w5's choice, recorded here: "mail.operation"). Every field is already
	// a closed safe value — see the file comment. The optional members are
	// mirrored only when a true value exists: an absent member stays absent
	// instead of degrading to a placeholder.
	attrs := []any{
		"operation", sample.Operation,
		"pair_ref", sample.PairRef,
		"source", sample.Source,
		"hit", sample.Hit,
		"duration_ms", sample.DurationMs,
		"acquire_wait_ms", sample.AcquireWaitMs,
		"socket_count", sample.SocketCount,
		"outcome", sample.Outcome,
	}
	if sample.Rows != nil {
		attrs = append(attrs, "rows", *sample.Rows)
	}
	if sample.Revision != "" {
		attrs = append(attrs, "revision", sample.Revision)
	}
	if sample.SharedFlight != nil {
		attrs = append(attrs, "shared_flight", *sample.SharedFlight)
	}
	slog.Info("mail.operation", attrs...)
}

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

// emitMailOperationTiming emits the one w6 §6.1 record for a completed
// gateway Mail operation — success AND failure alike — with the measured
// duration and the safe outcome class.
//
// duration_ms is > 0 on every completed operation (w6 §6.1 MC-P1): the
// elapsed time is rounded UP to the record's millisecond resolution, so
// sub-millisecond work reports 1 — "under 1 ms" — instead of the fabricated
// 0 the truncating Milliseconds() read produced (a zero reads as a
// measurement, and the summary boundary is sub-millisecond in the common
// case). A boundary that never captured its start time has NO measured
// duration: the record is skipped loudly rather than completed with an
// invented number.
func (a *restAPI) emitMailOperationTiming(op, agentID, workspaceID string, started time.Time, err error, source string, hit bool) {
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
		Operation:  member,
		PairRef:    a.mailRuntimeFor().mailPairRef(agentID, workspaceID),
		Source:     source,
		Hit:        hit,
		DurationMs: durationMs,
		Outcome:    "ok",
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
	// a closed safe value — see the file comment.
	slog.Info("mail.operation",
		"operation", sample.Operation,
		"pair_ref", sample.PairRef,
		"source", sample.Source,
		"hit", sample.Hit,
		"duration_ms", sample.DurationMs,
		"acquire_wait_ms", sample.AcquireWaitMs,
		"socket_count", sample.SocketCount,
		"outcome", sample.Outcome,
	)
}

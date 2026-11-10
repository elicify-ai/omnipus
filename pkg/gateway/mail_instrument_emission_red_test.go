package gateway

// RED — w5-integration claim 7: the instrument envelope is emitted for every
// gateway Mail operation, INCLUDING the frozen `summary` member.
//
// Oracles (derived from the spec BEFORE reading the implementation;
// /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/receipts/w5-red-test-plan.md):
//   - w6-proof §6.1 (the sole record definition): `operation` is a CLOSED
//     enum — folders, list, open, summary, discovery, attachment_metadata,
//     attachment_read, attachment_save, seen; `pair_ref` is opaque ("Never
//     an email address, host:port with username, or folder name");
//     `outcome` is "ok or a safe failure class from the existing closed
//     class set — never a raw upstream error string".
//   - w5 spec US-7.6/MC-18/B-35: "exactly one complete record in w6's
//     frozen shape reaches the injected sink… Preview mint, each serve
//     request, SUMMARY and removal are covered"; emission on success AND
//     failure.
//
// KNOWN PRODUCTION DEFECT UNDER TEST (check-integration-report.md F3): the
// wave's operation map drops `summary` even though w6 §6.1 freezes it and
// w5 US-7.6 names it — the summary assertions in this file are expected to
// be RED against the landed code. They are written to the spec and are
// never adjusted to the implementation; the red IS the finding.
//
// Mutations this pack must kill (check-integration-report.md §2):
//   - M12: instrument emission disabled at the emitter (emitMailOperation).
//   - F3: mailInstrumentOperationOf("summary") → "" (summary silently
//     emits nothing).

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/email"
	"github.com/stretchr/testify/require"
)

type recordingInstrumentSink struct {
	records []MailOperationSample
}

func (s *recordingInstrumentSink) RecordMailOperation(sample MailOperationSample) {
	s.records = append(s.records, sample)
}

func withRecordingSink(t *testing.T) *recordingInstrumentSink {
	t.Helper()
	sink := &recordingInstrumentSink{}
	SetMailInstrumentSink(sink)
	t.Cleanup(func() { SetMailInstrumentSink(nil) })
	return sink
}

// TestMailInstrument_OperationMapCoversTheFrozenEnum pins every gateway
// operation name onto its w6 §6.1 frozen enum member. The summary row is
// F3's: expected RED against the landed code.
func TestMailInstrument_OperationMapCoversTheFrozenEnum(t *testing.T) {
	cases := []struct {
		op   string
		want string
	}{
		{"listMailFolders", "folders"},
		{"listMailMessages", "list"},
		{"getMailMessage", "open"},
		{"summary", "summary"}, // w6 §6.1 enum row 4; w5 US-7.6 names summary (F3).
		{"getMailAttachment", "attachment_read"},
		{"mintMailAttachmentPreviewMeta", "attachment_metadata"},
		{"saveMailAttachmentToLibrary", "attachment_save"},
		{"seen", "seen"},
	}
	for _, tc := range cases {
		t.Run(tc.op, func(t *testing.T) {
			got := mailInstrumentOperationOf(tc.op)
			if got != tc.want {
				t.Fatalf("w6 §6.1/US-7.6: operation %q must map to the frozen enum member %q, got %q (an unmapped operation emits NO record — a silent instrument)", tc.op, tc.want, got)
			}
		})
	}
}

func TestMailInstrument_SummaryEmitsExactlyOneRecord(t *testing.T) {
	home := t.TempDir()
	api := &restAPI{homePath: home}
	sink := withRecordingSink(t)

	started := time.Now()
	api.emitMailOperationTiming("summary", "inst-agent", "inst-ws", started, nil, "none", false)

	if len(sink.records) != 1 {
		t.Fatalf("US-7.6/MC-18: the summary operation must emit exactly one record, got %d (F3: the operation map drops the frozen `summary` member — summary is judged against zero records, which invalidates the campaign by US-7.8)", len(sink.records))
	}
	rec := sink.records[0]
	if rec.Operation != "summary" {
		t.Fatalf("w6 §6.1: the record's operation must be the frozen member %q, got %q", "summary", rec.Operation)
	}
	if rec.PairRef == "" {
		t.Fatal("w6 §6.1: pair_ref must be the pair's opaque identity, got an empty string")
	}
	for _, forbidden := range []string{"@", "://"} {
		if strings.Contains(rec.PairRef, forbidden) {
			t.Fatalf("w6 §6.1: pair_ref must never carry an address or URL fragment (%q found in %q)", forbidden, rec.PairRef)
		}
	}
	if rec.Source != "none" || rec.Hit {
		t.Fatalf("US-7.7: a summary record reports zero mail acquisition (source=none, hit=false), got source=%q hit=%v", rec.Source, rec.Hit)
	}
	if rec.DurationMs < 0 {
		t.Fatalf("w6 §6.1: duration_ms must be a real non-negative timing, got %d", rec.DurationMs)
	}
	if rec.Outcome != "ok" {
		t.Fatalf("w6 §6.1: a successful operation's outcome is ok, got %q", rec.Outcome)
	}
}

func TestMailInstrument_FailureRecordCarriesClassNeverRawText(t *testing.T) {
	home := t.TempDir()
	api := &restAPI{homePath: home}
	sink := withRecordingSink(t)

	marker := "IMAPINSTR-RAW-3T7 raw upstream BYE"
	api.emitMailOperationTiming("listMailMessages", "inst-agent", "inst-ws", time.Now(),
		errors.New(marker), "live", false)

	require.Len(t, sink.records, 1, "US-7.6/B-35: a FAILED operation emits its record too")
	rec := sink.records[0]
	if rec.Operation != "list" {
		t.Fatalf("w6 §6.1: the record must carry the frozen member for the operation, got %q", rec.Operation)
	}
	if strings.Contains(rec.Outcome, "3T7") || strings.Contains(rec.Outcome, marker) {
		t.Fatalf("w6 §6.1: outcome must be a safe class, never the raw upstream error (%q)", rec.Outcome)
	}
	want := email.ClassifyMailError(errors.New(marker))
	if !mailUpstreamClasses[want] {
		t.Fatalf("w6 §6.1: the classified outcome %q must be a member of the existing closed class set", want)
	}
	if rec.Outcome != want {
		t.Fatalf("w6 §6.1: outcome must be the classified safe class %q, got %q", want, rec.Outcome)
	}
}

package gateway

// mail_measure_harness_test.go — the executable measurement harness for the
// w6-proof §8 campaign's GATEWAY-BOUNDARY arms (baseline-series.md procedures
// M-5 connection-failure Retry exercise, and the §6.1 instrument audit the
// proof wave owes the measurement plan).
//
// The harness is DISABLED unless OMNIPUS_MAIL_MEASURE names an output
// directory: CI and every normal test run skip it at the first line, and a
// skipped run writes nothing. When enabled it writes ONLY into the named
// output directory (receipts land outside the repository) and uses a scratch
// OMNIPUS_HOME (newMailRedEnv's t.TempDir) — it never touches the live data
// directory, and it configures its own synthetic mailbox credentials in that
// scratch home, so no credential is ever typed by hand.
//
// Arms (OMNIPUS_MAIL_MEASURE_ARMS, comma-separated, default "instrument,failure"):
//
//	instrument — drives every gateway Mail boundary through its REAL handler
//	    (rest_mail.go::handleWorkspaceMail router) with the recording sink
//	    installed, and writes instrument-audit.csv: one row per frozen w6 §6.1
//	    member, with the emitted record's fields and truth flags. Boundaries a
//	    real dial gates cannot reach a SUCCESS record in-process (pkg/email's
//	    imapDial seam is unexported — rest_mail_red_test.go line ~331), so
//	    dialing boundaries are staged to their failure record; §6.1 requires
//	    emission on failure too, and the failure record still proves the
//	    member, the safe class and the sub-field state. Rows that cannot be
//	    staged at all are written as not-stageable with the emitting site named
//	    — never silently omitted.
//	failure — baseline M-5: one benchmark mailbox per controlled failure class
//	    (connect_refused, timeout, tls, dns), each measured through the real
//	    folders handler: request clock of the failing attempt (the bound),
//	    the typed safe class in the 502/503 body, then the human-Retry
//	    round-trip (?retry=true — the marker the SPA adds, consumed by
//	    rest_mail_budget.go::mailRetryParam). A second accept on the
//	    harness-owned listener during one sample flags an automatic retry —
//	    the invalid-run condition baseline M-5 names.
//
// Honesty rules encoded here (w6 §8.3): a sample with an unexpected extra
// instrument record, an unnamed error class, or an extra upstream accept is
// marked invalid in its row and excluded from the receipt's valid counts —
// never averaged silently. The receipt carries the build SHA
// (OMNIPUS_MAIL_MEASURE_SHA, recorded by the runner; receipts without a SHA
// are invalid by §8.1). Samples run strictly sequentially with a pause
// between them (OMNIPUS_MAIL_MEASURE_PAUSE_MS) so a measurement run cannot
// overload the mail server; the pool's own 2/mailbox and 8/global ceilings
// stay in force underneath (the shared budget is respected, never bypassed).
//
// This file is a TEST HARNESS, not a benchmark result: a run's numbers are
// only comparable under the §8 conditions (same 13 mailboxes, both clocks,
// recorded host) — the fake-server arms are controlled evidence, never
// live-provider evidence (baseline M-4/M-5 like-for-like notes).

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	mailMeasureEnvDir       = "OMNIPUS_MAIL_MEASURE"
	mailMeasureEnvArms      = "OMNIPUS_MAIL_MEASURE_ARMS"
	mailMeasureEnvSHA       = "OMNIPUS_MAIL_MEASURE_SHA"
	mailMeasureEnvPause     = "OMNIPUS_MAIL_MEASURE_PAUSE_MS"
	mailMeasureEnvN         = "OMNIPUS_MAIL_MEASURE_N"
	mailMeasureTimeoutS     = 45   // the failing attempt must end by the 45 s read bound (w6 MC-P28)
	mailMeasureBoundGraceMs = 2000 // scheduling/enforcement grace on the bound check — gross overruns only
	mailMeasureDefaultN     = 5    // baseline M-5: >= 5 per class
	mailMeasureDefaultMods  = "instrument,failure"
)

// mailMeasureSample is one row of failure-samples.csv (baseline M-5 layout:
// clocks, status, class, retry round-trip, validity — never merged clocks).
type mailMeasureSample struct {
	Class       string `json:"class"`
	AttemptMs   int64  `json:"attempt_ms"`
	RetryMs     int64  `json:"retry_ms"`
	Status      int    `json:"status"`
	RetryStatus int    `json:"retry_status"`
	Accepts     int    `json:"attempt_accepts"`
	Valid       bool   `json:"valid"`
	InvalidWhy  string `json:"invalid_reason,omitempty"`
}

// mailMeasureAuditRow is one row of instrument-audit.csv: one frozen w6 §6.1
// member staged at one gateway boundary, with the emitted record and the
// truth flags the proof-wave audit reads.
type mailMeasureAuditRow struct {
	Boundary        string `json:"boundary"`
	Member          string `json:"member_expected"`
	Staged          string `json:"staged"` // success | failure | not-stageable
	HTTPStatus      int    `json:"http_status"`
	Records         int    `json:"records_in_window"`
	Operation       string `json:"rec_operation,omitempty"`
	PairRef         string `json:"rec_pair_ref,omitempty"`
	Source          string `json:"rec_source,omitempty"`
	Hit             string `json:"rec_hit,omitempty"`
	DurationMs      string `json:"rec_duration_ms,omitempty"`
	AcquireWaitMs   string `json:"rec_acquire_wait_ms,omitempty"`
	SocketCount     string `json:"rec_socket_count,omitempty"`
	Outcome         string `json:"rec_outcome,omitempty"`
	RowsPresent     bool   `json:"rec_rows_present"`
	RevisionPresent bool   `json:"rec_revision_present"`
	SharedFlight    bool   `json:"rec_shared_flight_present"`
	ZeroDuration    bool   `json:"flag_zero_duration"`
	LiveZeroSock    bool   `json:"flag_live_zero_socket"`
	Notes           string `json:"notes,omitempty"`
}

// TestMailMeasureGatewayHarness is the entry point. Without the env var it
// skips in constant time and touches nothing.
func TestMailMeasureGatewayHarness(t *testing.T) {
	outDir := os.Getenv(mailMeasureEnvDir)
	if outDir == "" {
		t.Skip("measurement harness disabled: set OMNIPUS_MAIL_MEASURE=<output-dir> to run (writes receipts only into that directory)")
	}
	mailMeasureGuardOutDir(t, outDir)
	require.NoError(t, os.MkdirAll(outDir, 0o755), "output directory must be creatable")

	sha := os.Getenv(mailMeasureEnvSHA)
	arms := mailMeasureEnvList(mailMeasureEnvArms, mailMeasureDefaultMods)
	pause := mailMeasureEnvInt(mailMeasureEnvPause, 200)
	n := mailMeasureEnvInt(mailMeasureEnvN, mailMeasureDefaultN)

	var auditRows []mailMeasureAuditRow
	var failureSamples []mailMeasureSample
	for _, arm := range arms {
		switch arm {
		case "instrument":
			auditRows = mailMeasureRunInstrumentArm(t, pause)
		case "failure":
			failureSamples = mailMeasureRunFailureArm(t, n, pause)
		default:
			t.Fatalf("unknown arm %q (known: instrument, failure)", arm)
		}
	}

	mailMeasureWriteGatewayReceipt(t, outDir, sha, arms, auditRows, failureSamples)
}

// mailMeasureGuardOutDir refuses an output directory at or inside a live
// data directory: receipts must never be written into ~/.omnipus or any
// path containing a .omnipus component (dispatch rule: never write to the
// live data directory).
func mailMeasureGuardOutDir(t *testing.T, outDir string) {
	t.Helper()
	abs, err := filepath.Abs(outDir)
	require.NoError(t, err)
	for _, seg := range strings.Split(abs, string(filepath.Separator)) {
		if seg == ".omnipus" {
			t.Fatalf("refusing output directory %s: it sits inside a .omnipus data directory", abs)
		}
	}
}

func mailMeasureEnvList(key, def string) []string {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		raw = def
	}
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func mailMeasureEnvInt(key string, def int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v <= 0 {
		return def
	}
	return v
}

// ---------------------------------------------------------------------------
// Arm: instrument — every gateway boundary, one frozen member per row
// ---------------------------------------------------------------------------

// mailMeasureRunInstrumentArm stages each boundary through its real handler
// with the recording sink installed and returns one audit row per boundary.
func mailMeasureRunInstrumentArm(t *testing.T, pauseMs int) []mailMeasureAuditRow {
	t.Helper()
	env := newMailRedEnv(t)
	sink := withRecordingSink(t)

	ref := "uid:1:1"
	type boundary struct {
		name   string
		member string
		drive  func() *httptest.ResponseRecorder
	}
	boundaries := []boundary{
		{
			name: "summary", member: "summary",
			drive: func() *httptest.ResponseRecorder {
				return mailDo(env.mux, http.MethodGet,
					"/api/v1/workspaces/"+mailRedWS+"/mail/summary", nextMailIP(), true, "")
			},
		},
		{
			name: "preview_mint", member: "open",
			drive: func() *httptest.ResponseRecorder {
				body := fmt.Sprintf(`{"workspace_id":%q,"agent_id":%q,"folder":"inbox","message_ref":%q,"load_remote":false}`,
					mailRedWS, mailRedAgent, ref)
				return mailDo(env.mux, http.MethodPost, "/api/v1/mail/html-preview-token", nextMailIP(), true, body)
			},
		},
		{
			name: "attachment_metadata_mint", member: "attachment_metadata",
			drive: func() *httptest.ResponseRecorder {
				body := fmt.Sprintf(`{"workspace_id":%q,"agent_id":%q,"folder":"inbox","message_ref":%q}`,
					mailRedWS, mailRedAgent, ref)
				return mailDo(env.mux, http.MethodPost, "/api/v1/mail/attachment-preview-token", nextMailIP(), true, body)
			},
		},
		{
			name: "folders", member: "folders",
			drive: func() *httptest.ResponseRecorder {
				return mailDo(env.mux, http.MethodGet, mailFoldersPath(), nextMailIP(), true, "")
			},
		},
		{
			name: "list", member: "list",
			drive: func() *httptest.ResponseRecorder {
				return mailDo(env.mux, http.MethodGet, mailMessagesPath("inbox")+"?limit=20", nextMailIP(), true, "")
			},
		},
		{
			name: "open_detail", member: "open",
			drive: func() *httptest.ResponseRecorder {
				return mailDo(env.mux, http.MethodGet, mailMessagesPath("inbox")+"/"+ref, nextMailIP(), true, "")
			},
		},
		{
			name: "seen", member: "seen",
			drive: func() *httptest.ResponseRecorder {
				return mailDo(env.mux, http.MethodPost, mailMessagesPath("inbox")+"/"+ref+"/seen", nextMailIP(), true, "")
			},
		},
		{
			name: "attachment_read", member: "attachment_read",
			drive: func() *httptest.ResponseRecorder {
				return mailDo(env.mux, http.MethodGet, mailMessagesPath("inbox")+"/"+ref+"/attachments/1", nextMailIP(), true, "")
			},
		},
	}

	rows := make([]mailMeasureAuditRow, 0, len(boundaries)+2)
	for _, b := range boundaries {
		before := len(sink.records)
		rec := b.drive()
		after := len(sink.records)
		row := mailMeasureAuditRowFromRecords(b.name, b.member, rec.Code, sink.records[before:after])
		rows = append(rows, row)
		time.Sleep(time.Duration(pauseMs) * time.Millisecond)
	}

	// attachment_save cannot be staged without a valid save_operation_token,
	// which only a real preview/serve flow (a real dial) mints; without the
	// token the handler refuses 400 BEFORE the operation starts, so the
	// absence of a record there is correct behavior, not a missing emitter.
	rows = append(rows, mailMeasureAuditRow{
		Boundary: "attachment_save", Member: "attachment_save", Staged: "not-stageable",
		Notes: "emission site rest_mail_attachment.go::handleMailAttachmentSave (saveMailAttachmentToLibrary); requires a minted save_operation_token which requires a real dial; envelope covered by mail_instrument_emission_red_test.go",
	})
	// discovery has no gateway route in this tree: mailInstrumentOperationOf
	// maps it to "" and no boundary emits it. Reported, never silently
	// omitted (the audit's defined-but-never-emitted case).
	rows = append(rows, mailMeasureAuditRow{
		Boundary: "discovery", Member: "discovery", Staged: "not-stageable",
		Notes: "no gateway route exists; mail_instrument.go::mailInstrumentOperationOf maps discovery to \"\" — the frozen member is defined but never emitted (publisher-owned gap)",
	})
	return rows
}

// mailMeasureAuditRowFromRecords builds the audit row for one boundary from
// the records its single request produced (the harness sends exactly one
// request per row, so >1 record in the window is itself a finding).
func mailMeasureAuditRowFromRecords(name, member string, status int, records []MailOperationSample) mailMeasureAuditRow {
	row := mailMeasureAuditRow{
		Boundary: name, Member: member,
		Staged:     "failure",
		HTTPStatus: status,
		Records:    len(records),
	}
	if len(records) == 0 {
		row.Notes = "NO RECORD EMITTED for an exercised operation — US-P1 AC-6: invalid evidence, the silent-instrument case"
		return row
	}
	rec := records[0]
	row.Operation = rec.Operation
	row.PairRef = rec.PairRef
	row.Source = rec.Source
	row.Hit = strconv.FormatBool(rec.Hit)
	row.DurationMs = strconv.FormatInt(rec.DurationMs, 10)
	row.AcquireWaitMs = strconv.FormatInt(rec.AcquireWaitMs, 10)
	row.SocketCount = strconv.Itoa(rec.SocketCount)
	row.Outcome = rec.Outcome
	row.RowsPresent = rec.Rows != nil
	row.RevisionPresent = rec.Revision != ""
	row.SharedFlight = rec.SharedFlight != nil
	if len(records) > 1 {
		row.Notes = fmt.Sprintf("%d records in a single-request window", len(records))
	}
	if rec.DurationMs == 0 {
		row.ZeroDuration = true
	}
	// A live (hit=false) record with socket_count 0 is the fabricated-zero
	// case the proof wave audits for: it is indistinguishable from a cache
	// hit's 0 (MC-P3 wants >=1 on live; MC-P4's sum needs the real count).
	if !rec.Hit && rec.SocketCount == 0 && rec.Source == "live" {
		row.LiveZeroSock = true
	}
	return row
}

// ---------------------------------------------------------------------------
// Arm: failure — baseline M-5, one controlled endpoint per class
// ---------------------------------------------------------------------------

// mailMeasureRunFailureArm measures time-to-error and the Retry round-trip
// per controlled class through the real folders handler.
func mailMeasureRunFailureArm(t *testing.T, n, pauseMs int) []mailMeasureSample {
	t.Helper()
	classes := []struct {
		name   string
		listen func(t *testing.T) (host string, port int, accepts func() int, done func())
	}{
		{"connect_refused", mailMeasureRefusedEndpoint},
		{"timeout", mailMeasureSilentEndpoint},
		{"tls", mailMeasurePlaintextEndpoint},
		{"dns", func(t *testing.T) (string, int, func() int, func()) {
			// RFC 6761 reserves .invalid so resolution can never succeed.
			return "mail-measure-invalid.invalid", 993, func() int { return 0 }, func() {}
		}},
	}
	var all []mailMeasureSample
	for _, cls := range classes {
		host, port, accepts, done := cls.listen(t)
		env := newMailRedEnv(t)
		mailMeasurePointAt(t, env, host, port)
		for i := 0; i < n; i++ {
			s := mailMeasureOneFailureSample(t, env, accepts)
			s.Class = cls.name
			all = append(all, s)
			time.Sleep(time.Duration(pauseMs) * time.Millisecond)
		}
		done()
	}
	return all
}

// mailMeasureOneFailureSample runs one failing attempt + one human Retry
// through the real handler, timing each separately (never merged). The
// accept counter is read at THREE points: before the attempt, between the
// attempt and the retry, and after the retry — so an automatic retry during
// the ATTEMPT (more than one upstream accept before the human Retry) is
// distinguishable from the retry's own accept, which the window would
// otherwise swallow and mislabel.
func mailMeasureOneFailureSample(t *testing.T, env *mailRedEnv, accepts func() int) mailMeasureSample {
	t.Helper()
	s := mailMeasureSample{}
	before := accepts()

	start := time.Now()
	rec := mailDo(env.mux, http.MethodGet, mailFoldersPath()+"?limit=1", nextMailIP(), true, "")
	s.AttemptMs = time.Since(start).Milliseconds()
	s.Status = rec.Code
	er := decodeMailErr(t, rec)
	if er.Code != nil {
		s.Class = *er.Code
	}
	attemptAccepts := accepts() - before

	retryStart := time.Now()
	retryRec := mailDo(env.mux, http.MethodGet, mailFoldersPath()+"?limit=1&retry=true", nextMailIP(), true, "")
	s.RetryMs = time.Since(retryStart).Milliseconds()
	s.RetryStatus = retryRec.Code

	// attemptAccepts > 1 = an upstream connection was opened more than once
	// by the single failing attempt — the automatic-retry invalid condition
	// (baseline M-5). The retry's own accept is counted separately and is
	// expected.
	s.Accepts = attemptAccepts
	s.Valid = true
	if s.Class == "" {
		s.Valid = false
		s.InvalidWhy = "no typed class in the error body"
	}
	if attemptAccepts > 1 {
		s.Valid = false
		s.InvalidWhy = fmt.Sprintf("automatic retry observed: %d upstream accepts during ONE failing attempt", attemptAccepts)
	}
	if s.AttemptMs > mailMeasureTimeoutS*1000+mailMeasureBoundGraceMs {
		s.Valid = false
		s.InvalidWhy = fmt.Sprintf("failing attempt exceeded the %d s read bound beyond the %d ms enforcement grace (%d ms)",
			mailMeasureTimeoutS, mailMeasureBoundGraceMs, s.AttemptMs)
	}
	return s
}

// mailMeasurePointAt points the env's benchmark mailbox at an arbitrary
// host:port (test-local: mail_fixture_red_test.go::pointMailboxAt is fixed
// to 127.0.0.1 and is another file's helper — never edited from here).
func mailMeasurePointAt(t *testing.T, env *mailRedEnv, host string, port int) {
	t.Helper()
	cfg := env.api.agentLoop.GetConfig()
	mb := cfg.Mailboxes[mailRedAgent][mailRedWS]
	mb.IMAPHost = host
	mb.IMAPPort = port
	mb.SMTPHost = host
	mb.SMTPPort = port
	cfg.Mailboxes[mailRedAgent][mailRedWS] = mb
}

// mailMeasureRefusedEndpoint returns a port with nothing listening.
func mailMeasureRefusedEndpoint(t *testing.T) (string, int, func() int, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr, ok := ln.Addr().(*net.TCPAddr)
	require.True(t, ok, "listener address is *net.TCPAddr, got %T", ln.Addr())
	port := addr.Port
	require.NoError(t, ln.Close())
	return "127.0.0.1", port, func() int { return 0 }, func() {}
}

// mailMeasureSilentEndpoint accepts TCP and never speaks — the stalled-server
// class; the TLS handshake hangs until the dial bound fires it.
func mailMeasureSilentEndpoint(t *testing.T) (string, int, func() int, func()) {
	t.Helper()
	return mailMeasureCountingEndpoint(t, func(conn net.Conn) {
		defer conn.Close()
		select {}
	})
}

// mailMeasurePlaintextEndpoint speaks plaintext IMAP — a TLS-first dial
// against it fails the handshake: the tls class.
func mailMeasurePlaintextEndpoint(t *testing.T) (string, int, func() int, func()) {
	t.Helper()
	return mailMeasureCountingEndpoint(t, func(conn net.Conn) {
		defer conn.Close()
		_, _ = conn.Write([]byte("* OK plaintext IMAP, never TLS\r\n"))
		buf := make([]byte, 512)
		for {
			if _, err := conn.Read(buf); err != nil {
				return
			}
		}
	})
}

type mailMeasureEndpoint struct {
	accepts int
	ln      net.Listener
}

func (m *mailMeasureEndpoint) count() int { return m.accepts }

// mailMeasureCountingEndpoint runs handler on every accepted connection and
// exposes the accept counter — the independent observer that flags an
// automatic retry (more than one accept per attempt+retry pair).
func mailMeasureCountingEndpoint(t *testing.T, handler func(net.Conn)) (string, int, func() int, func()) {
	t.Helper()
	ep := &mailMeasureEndpoint{}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	ep.ln = ln
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, aerr := ln.Accept()
			if aerr != nil {
				return
			}
			ep.accepts++
			go handler(conn)
		}
	}()
	addr, ok := ln.Addr().(*net.TCPAddr)
	require.True(t, ok, "listener address is *net.TCPAddr, got %T", ln.Addr())
	port := addr.Port
	stop := func() {
		_ = ln.Close()
		<-done
	}
	return "127.0.0.1", port, ep.count, stop
}

// ---------------------------------------------------------------------------
// Receipt writing
// ---------------------------------------------------------------------------

func mailMeasureWriteGatewayReceipt(t *testing.T, outDir, sha string, arms []string, audit []mailMeasureAuditRow, failure []mailMeasureSample) {
	t.Helper()
	writeCSV := func(name string, header []string, rows [][]string) {
		path := filepath.Join(outDir, name)
		var b strings.Builder
		b.WriteString(strings.Join(header, ",") + "\n")
		for _, r := range rows {
			for i, cell := range r {
				if i > 0 {
					b.WriteString(",")
				}
				b.WriteString(`"` + strings.ReplaceAll(cell, `"`, `""`) + `"`)
			}
			b.WriteString("\n")
		}
		require.NoError(t, os.WriteFile(path, []byte(b.String()), 0o644), "write %s", name)
	}

	if len(audit) > 0 {
		header := []string{"boundary", "member_expected", "staged", "http_status", "records_in_window",
			"rec_operation", "rec_pair_ref", "rec_source", "rec_hit", "rec_duration_ms",
			"rec_acquire_wait_ms", "rec_socket_count", "rec_outcome", "rec_rows_present",
			"rec_revision_present", "rec_shared_flight_present", "flag_zero_duration",
			"flag_live_zero_socket", "notes"}
		rows := make([][]string, 0, len(audit))
		for _, r := range audit {
			rows = append(rows, []string{
				r.Boundary, r.Member, r.Staged, strconv.Itoa(r.HTTPStatus), strconv.Itoa(r.Records),
				r.Operation, r.PairRef, r.Source, r.Hit, r.DurationMs,
				r.AcquireWaitMs, r.SocketCount, r.Outcome, strconv.FormatBool(r.RowsPresent),
				strconv.FormatBool(r.RevisionPresent), strconv.FormatBool(r.SharedFlight),
				strconv.FormatBool(r.ZeroDuration), strconv.FormatBool(r.LiveZeroSock), r.Notes,
			})
		}
		writeCSV("instrument-audit.csv", header, rows)
	}
	if len(failure) > 0 {
		header := []string{"class", "attempt_ms", "retry_ms", "status", "retry_status", "attempt_accepts", "valid", "invalid_reason"}
		rows := make([][]string, 0, len(failure))
		for _, s := range failure {
			rows = append(rows, []string{
				s.Class, strconv.FormatInt(s.AttemptMs, 10), strconv.FormatInt(s.RetryMs, 10),
				strconv.Itoa(s.Status), strconv.Itoa(s.RetryStatus), strconv.Itoa(s.Accepts),
				strconv.FormatBool(s.Valid), s.InvalidWhy,
			})
		}
		writeCSV("failure-samples.csv", header, rows)
	}

	validFailure := 0
	var invalids []mailMeasureSample
	for _, s := range failure {
		if s.Valid {
			validFailure++
		} else {
			invalids = append(invalids, s)
		}
	}
	receipt := map[string]any{
		"kind":              "mail-measure-gateway-harness",
		"build_sha":         sha,
		"sha_recorded":      sha != "",
		"arms":              arms,
		"audit_rows":        audit,
		"failure_samples":   failure,
		"failure_valid_n":   validFailure,
		"failure_invalid_n": len(invalids),
		"note":              "controlled fake-endpoint evidence; never comparable to live-provider numbers (baseline M-4/M-5 like-for-like rules)",
	}
	raw, err := json.MarshalIndent(receipt, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(outDir, "gateway-receipt.json"), raw, 0o644))
}

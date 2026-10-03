package email

// mail_measure_harness_test.go — the executable measurement harness for the
// w6-proof §8 campaign's EMAIL-LAYER success arms (baseline-series.md
// procedures M-3 cold/warm request-clock series and M-4 large-folder
// benchmark, at the pkg/email operation seam).
//
// This file exists because the gateway-boundary harness
// (pkg/gateway/mail_measure_harness_test.go) cannot stage a SUCCESSFUL dial:
// pkg/email's imapDial seam is unexported and only this package can swap it
// (the same constraint rest_mail_red_test.go documents). Here the seam is
// swapped for a COUNTING plaintext dial — the counter is the independent
// connection observer the instrument-truth rules demand (MC-P4: socket
// acquisitions must agree with a counter that is not the instrument).
//
// The harness is DISABLED unless OMNIPUS_MAIL_MEASURE names an output
// directory: CI and every normal test run skip it at the first line. When
// enabled it uses a scratch state dir (t.TempDir) — never the live data
// directory — and configures synthetic credentials in code, so no credential
// is ever typed by hand.
//
// Arms (OMNIPUS_MAIL_MEASURE_ARMS, comma-separated, default "series,large"):
//
//	series — cold/warm samples per baseline M-3 for three operations:
//	    folders (FolderCounts), list (ReadFolderPage), open (ReadView) — the
//	    panel operations — and read_inbox (ReadInbox), which rides the shared
//	    pool (transport.go::withMailSession) and therefore carries W1's pool
//	    instrument sub-fields. Every sample records the request clock, the
//	    row count, whether the warm sample re-used the session (dial delta
//	    over the sample window) and, where the op rides the pool, the pool's
//	    acquire_wait_ms / socket_count / outcome. A sample is INVALID only on
//	    §8.3 process violations (operation failed, extra records in a
//	    single-operation window); a warm sample that re-dialed is a VALID
//	    measurement of "reuse not eligible on this build" — recorded, never
//	    punished, never averaged into a reuse claim.
//	large — M-4's controlled large-folder benchmark: the fake server is
//	    seeded with OMNIPUS_MAIL_MEASURE_LARGE_N synthetic messages (default
//	    10000; the campaign's 100000 row is this same harness at a bigger N)
//	    and list paging is measured with row counts.
//
// Honesty rules: sequential samples with a pause
// (OMNIPUS_MAIL_MEASURE_PAUSE_MS) so a run cannot overload any server; the
// pool's 2-per-mailbox and 8-global ceilings stay in force underneath; the
// receipt names the build SHA (OMNIPUS_MAIL_MEASURE_SHA — a receipt without
// a SHA is invalid by §8.1). This harness records the REQUEST clock only:
// the click→rendered user clock needs the browser surface and stays a
// live-campaign/Playwright obligation (§8.1) — stated, never approximated.
// All numbers are CONTROLLED fake-server evidence, never live-provider
// evidence (baseline M-4 like-for-like rule).

import (
	"context"
	"crypto/tls"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/stretchr/testify/require"
)

const (
	mailMeasureEmailEnvN       = "OMNIPUS_MAIL_MEASURE_N"
	mailMeasureEmailEnvLargeN  = "OMNIPUS_MAIL_MEASURE_LARGE_N"
	mailMeasureEmailDefaultN   = 5
	mailMeasureEmailLargeN     = 10000
	mailMeasureEmailSeriesMsgs = 3
)

// mailMeasureEmailSample is one row of series-samples.csv (baseline M-3
// layout, request clock only — the user clock is a browser obligation).
type mailMeasureEmailSample struct {
	Arm           string `json:"arm"`
	Op            string `json:"op"`
	Phase         string `json:"phase"`
	Attempt       int    `json:"attempt"`
	RequestMs     int64  `json:"request_ms"`
	RequestUs     int64  `json:"request_us"`
	Rows          int    `json:"rows"`
	Reused        string `json:"session_reused"` // true | false | "" (not observable — legacy dial path)
	AcquireWaitMs string `json:"pool_acquire_wait_ms"`
	SocketCount   string `json:"pool_socket_count"`
	PoolOutcome   string `json:"pool_outcome"`
	DialDelta     int    `json:"dial_delta"`
	Valid         bool   `json:"valid"`
	InvalidWhy    string `json:"invalid_reason,omitempty"`
}

// mailMeasurePoolCapture accumulates the pool instrument sub-fields; samples
// run strictly sequentially, so a take() before each window cannot interleave.
type mailMeasurePoolCapture struct {
	samples []PoolInstrumentSample
}

func (c *mailMeasurePoolCapture) record(s PoolInstrumentSample) {
	c.samples = append(c.samples, s)
}

func (c *mailMeasurePoolCapture) take() []PoolInstrumentSample {
	out := c.samples
	c.samples = nil
	return out
}

// TestMailMeasureEmailHarness is the entry point; it skips unless enabled.
func TestMailMeasureEmailHarness(t *testing.T) {
	outDir := os.Getenv("OMNIPUS_MAIL_MEASURE")
	if outDir == "" {
		t.Skip("measurement harness disabled: set OMNIPUS_MAIL_MEASURE=<output-dir> to run (writes receipts only into that directory)")
	}
	mailMeasureGuardEmailOutDir(t, outDir)
	require.NoError(t, os.MkdirAll(outDir, 0o755), "output directory must be creatable")

	sha := os.Getenv("OMNIPUS_MAIL_MEASURE_SHA")
	arms := mailMeasureEmailEnvList("OMNIPUS_MAIL_MEASURE_ARMS", "series,large")
	n := mailMeasureEmailEnvInt(mailMeasureEmailEnvN, mailMeasureEmailDefaultN)
	largeN := mailMeasureEmailEnvInt(mailMeasureEmailEnvLargeN, mailMeasureEmailLargeN)
	pause := mailMeasureEmailEnvInt("OMNIPUS_MAIL_MEASURE_PAUSE_MS", 200)

	var samples []mailMeasureEmailSample
	for _, arm := range arms {
		switch arm {
		case "series":
			samples = append(samples, mailMeasureRunSeriesArm(t, n, pause)...)
		case "large":
			samples = append(samples, mailMeasureRunLargeArm(t, largeN, n, pause)...)
		default:
			t.Fatalf("unknown arm %q (known: series, large)", arm)
		}
	}
	mailMeasureWriteEmailReceipt(t, outDir, sha, arms, samples)
}

func mailMeasureGuardEmailOutDir(t *testing.T, outDir string) {
	t.Helper()
	abs, err := filepath.Abs(outDir)
	require.NoError(t, err)
	for _, seg := range strings.Split(abs, string(filepath.Separator)) {
		if seg == ".omnipus" {
			t.Fatalf("refusing output directory %s: it sits inside a .omnipus data directory", abs)
		}
	}
}

func mailMeasureEmailEnvList(key, def string) []string {
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

func mailMeasureEmailEnvInt(key string, def int) int {
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

// mailMeasureSwapCountingDial swaps the (already plaintext, startMemIMAP's)
// dial seam for one that counts every dial. It must be called AFTER
// startMemIMAP: cleanups run LIFO, so this wrapper is removed first and
// startMemIMAP's plaintext seam restored after it. Every dial the pool or a
// legacy path makes passes through the wrapper, so the delta over a sample
// window is the independent connection count (MC-P4's agreement oracle).
func mailMeasureSwapCountingDial(t *testing.T) *atomic.Int64 {
	t.Helper()
	var dials atomic.Int64
	prev := imapDial
	imapDial = func(ctx context.Context, addr string, tlsCfg *tls.Config) (*imapclient.Client, error) {
		dials.Add(1)
		return prev(ctx, addr, tlsCfg)
	}
	t.Cleanup(func() { imapDial = prev })
	return &dials
}

// mailMeasureSeedMessages builds n minimal synthetic RFC 822 messages with
// marker subjects — controlled fixtures, never live mail.
func mailMeasureSeedMessages(n int) [][]byte {
	msgs := make([][]byte, n)
	for i := 0; i < n; i++ {
		msgs[i] = mkMsg(
			fmt.Sprintf("synthetic measure message %06d", i),
			"sender-measure@test.local",
			fmt.Sprintf("synthetic body %06d — controlled benchmark fixture", i))
	}
	return msgs
}

// mailMeasurePoolClient wires a fresh client onto a fresh shared-pool
// manager for a synthetic pair, with the pool instrument sink capturing W1's
// sub-fields. The state dir is a scratch TempDir — nothing is written
// beside the live data directory.
func mailMeasurePoolClient(t *testing.T, base *Client, pair string, capture *mailMeasurePoolCapture) (*Client, func()) {
	t.Helper()
	stateDir := filepath.Join(t.TempDir(), "poolstate")
	sessions := SharedMailSessions(stateDir, SessionsConfig{
		Credentials: func(string) (string, string, error) { return testIMAPUser, testIMAPPass, nil },
		Instrument:  capture.record,
	})
	cl, err := NewClient(Account{
		IMAPHost: base.acct.IMAPHost,
		IMAPPort: base.acct.IMAPPort,
		SMTPHost: base.acct.SMTPHost,
		Username: testIMAPUser,
		Password: testIMAPPass,
	})
	require.NoError(t, err)
	cl.SetSessionSource(sessions)
	cl.SetSessionScope(pair, "measure-gen-0")
	return cl, sessions.Close
}

// mailMeasureTimeOp runs one operation inside its own window: request clock,
// row count, dial delta, and — where the op rides the pool — W1's instrument
// sub-fields. Validity encodes ONLY §8.3 process violations; a warm sample
// that re-dialed is a valid measurement of "reuse not eligible here".
func mailMeasureTimeOp(op, phase string, attempt int, dials *atomic.Int64, capture *mailMeasurePoolCapture, run func() (int, error)) mailMeasureEmailSample {
	s := mailMeasureEmailSample{Arm: "series", Op: op, Phase: phase, Attempt: attempt}
	before := dials.Load()
	capture.take()
	start := time.Now()
	rows, err := run()
	elapsed := time.Since(start)
	s.RequestMs = elapsed.Milliseconds()
	s.RequestUs = elapsed.Microseconds()
	s.Rows = rows
	s.DialDelta = int(dials.Load() - before)

	poolSamples := capture.take()
	switch {
	case len(poolSamples) > 1:
		s.Valid = false
		s.InvalidWhy = fmt.Sprintf("%d pool samples in a single-operation window", len(poolSamples))
		return s
	case len(poolSamples) == 1:
		s.AcquireWaitMs = strconv.FormatInt(poolSamples[0].AcquireWaitMs, 10)
		s.SocketCount = strconv.Itoa(poolSamples[0].SocketCount)
		s.PoolOutcome = poolSamples[0].Outcome
		s.Reused = strconv.FormatBool(s.DialDelta == 0)
	default:
		// Legacy dial path (no pool on this operation in this tree): the
		// sub-fields are honestly empty, never fabricated as 0-by-design.
		s.Reused = strconv.FormatBool(s.DialDelta == 0)
	}
	s.Valid = true
	if err != nil {
		s.Valid = false
		s.InvalidWhy = "operation failed: " + err.Error()
	}
	return s
}

// mailMeasureRunSeriesArm measures cold/warm folders, list, open and the
// pool-riding read_inbox per baseline M-3: cold = first use of a fresh
// pool/client, warm = immediate repeat.
//
// Wiring honesty: the panel operations (folders/list/open — view.go) are NOT
// yet rewired onto the pool in this tree; a pool-wired client refuses them
// with the typed ErrLegacyDialReached. They are therefore measured on the
// legacy per-call client — the way they actually execute here — and their
// pool sub-fields are recorded as empty, never fabricated. read_inbox
// (transport.go::withMailSession) IS pool-rewired and rides a fresh pool per
// iteration, carrying W1's instrument sub-fields. When the w2 rewiring
// lands, this arm moves the panel ops onto the pool client — a campaign
// precondition to re-check, not silent behavior drift.
func mailMeasureRunSeriesArm(t *testing.T, n, pauseMs int) []mailMeasureEmailSample {
	t.Helper()
	base := startMemIMAP(t, mailMeasureSeedMessages(mailMeasureEmailSeriesMsgs), nil)
	dials := mailMeasureSwapCountingDial(t)
	ctx := context.Background()
	legacyNoopCapture := &mailMeasurePoolCapture{}

	var out []mailMeasureEmailSample
	for i := 0; i < n; i++ {
		foldersTotal := func() (int, error) {
			stats, ferr := base.FolderCounts(ctx)
			total := 0
			for _, st := range stats {
				total += st.Total
			}
			return total, ferr
		}
		out = append(out, mailMeasureTimeOp("folders", "cold", i+1, dials, legacyNoopCapture, foldersTotal))
		out = append(out, mailMeasureTimeOp("folders", "warm", i+1, dials, legacyNoopCapture, foldersTotal))

		rows, uv, _, err := base.ReadFolderPage(ctx, "inbox", 20, 0)
		require.NoError(t, err, "list must succeed before the series continues")
		_ = uv
		if len(rows) == 0 {
			out = append(out, mailMeasureEmailSample{Arm: "series", Op: "list", Phase: "cold", Attempt: i + 1,
				Valid: true, InvalidWhy: "not applicable: empty folder — recorded as n/a, never as 0 ms (§8.3)"})
		} else {
			out = append(out, mailMeasureTimeOp("list", "cold", i+1, dials, legacyNoopCapture, func() (int, error) {
				r, _, _, ferr := base.ReadFolderPage(ctx, "inbox", 20, 0)
				return len(r), ferr
			}))
			first := rows[0]
			out = append(out, mailMeasureTimeOp("open", "cold", i+1, dials, legacyNoopCapture, func() (int, error) {
				_, ferr := base.ReadView(ctx, "inbox", fmt.Sprintf("uid:%d:%d", first.UIDValidity, first.UID))
				if ferr != nil {
					return 0, ferr
				}
				return 1, nil
			}))
		}

		capture := &mailMeasurePoolCapture{}
		cl, closePool := mailMeasurePoolClient(t, base, fmt.Sprintf("mail-measure-pair-%d", i), capture)
		out = append(out, mailMeasureTimeOp("read_inbox", "cold", i+1, dials, capture, func() (int, error) {
			msgs, ferr := cl.ReadInbox(ctx, InboxOptions{Limit: 10})
			return len(msgs), ferr
		}))
		out = append(out, mailMeasureTimeOp("read_inbox", "warm", i+1, dials, capture, func() (int, error) {
			msgs, ferr := cl.ReadInbox(ctx, InboxOptions{Limit: 10})
			return len(msgs), ferr
		}))
		closePool()

		time.Sleep(time.Duration(pauseMs) * time.Millisecond)
	}
	return out
}

// mailMeasureRunLargeArm is M-4's controlled large-folder benchmark: seed N
// synthetic messages, then measure list paging with row counts. List paging
// rides the legacy client (view.go reads are not pool-rewired in this tree —
// see the series arm's wiring note).
func mailMeasureRunLargeArm(t *testing.T, largeN, n, pauseMs int) []mailMeasureEmailSample {
	t.Helper()
	msgs := mailMeasureSeedMessages(largeN)
	base := startMemIMAP(t, msgs, nil)
	dials := mailMeasureSwapCountingDial(t)
	legacyNoopCapture := &mailMeasurePoolCapture{}

	ctx := context.Background()
	var out []mailMeasureEmailSample
	for i := 0; i < n; i++ {
		s := mailMeasureTimeOp("list", "paged", i+1, dials, legacyNoopCapture, func() (int, error) {
			r, _, _, ferr := base.ReadFolderPage(ctx, "inbox", 200, 0)
			return len(r), ferr
		})
		s.Arm = "large"
		out = append(out, s)
		time.Sleep(time.Duration(pauseMs) * time.Millisecond)
	}
	return out
}

func mailMeasureWriteEmailReceipt(t *testing.T, outDir, sha string, arms []string, samples []mailMeasureEmailSample) {
	t.Helper()
	var b strings.Builder
	b.WriteString("arm,op,phase,attempt,request_ms,request_us,rows,session_reused,pool_acquire_wait_ms,pool_socket_count,pool_outcome,dial_delta,valid,invalid_reason\n")
	for _, s := range samples {
		cells := []string{
			s.Arm, s.Op, s.Phase, strconv.Itoa(s.Attempt),
			strconv.FormatInt(s.RequestMs, 10), strconv.FormatInt(s.RequestUs, 10), strconv.Itoa(s.Rows),
			s.Reused, s.AcquireWaitMs, s.SocketCount, s.PoolOutcome,
			strconv.Itoa(s.DialDelta), strconv.FormatBool(s.Valid), s.InvalidWhy,
		}
		for i, cell := range cells {
			if i > 0 {
				b.WriteString(",")
			}
			b.WriteString(`"` + strings.ReplaceAll(cell, `"`, `""`) + `"`)
		}
		b.WriteString("\n")
	}
	require.NoError(t, os.WriteFile(filepath.Join(outDir, "series-samples.csv"), []byte(b.String()), 0o644))

	valid := 0
	reusedWarm := 0
	warmTotal := 0
	for _, s := range samples {
		if s.Valid {
			valid++
		}
		if s.Phase == "warm" {
			warmTotal++
			if s.Reused == "true" {
				reusedWarm++
			}
		}
	}
	receipt := fmt.Sprintf(`{
  "kind": "mail-measure-email-harness",
  "build_sha": %q,
  "sha_recorded": %v,
  "arms": [%s],
  "samples_total": %d,
  "samples_valid": %d,
  "samples_invalid": %d,
  "warm_samples_total": %d,
  "warm_samples_session_reused": %d,
  "clock": "request clock only — the click-to-rendered user clock is a browser/Playwright obligation (w6 §8.1), not approximated here",
  "note": "controlled fake-server evidence; never comparable to live-provider numbers (baseline M-3/M-4 like-for-like rules)"
}`,
		sha, sha != "", `"`+strings.Join(arms, `","`)+`"`,
		len(samples), valid, len(samples)-valid, warmTotal, reusedWarm)
	require.NoError(t, os.WriteFile(filepath.Join(outDir, "email-receipt.json"), []byte(receipt), 0o644))
}

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// browser_metrics.go — FR-019 observability for the live-browser video
// streaming epic (docs/internal/specs/live-browser-video-streaming-spec.md
// FR-019, O-4, Test 28; SC-012). Reuses the SAME hand-formatted Prometheus
// text-exposition system pkg/gateway/metrics.go already built for FR-039 (no
// prometheus/client_golang — footprint, Constraint #1/#3) and served at the
// SAME GET /metrics endpoint (HandleMetrics). Follows the SAME "leaf package
// holds a small interface, gateway owns the counters and registers itself at
// boot" pattern pkg/tools/compositor.go's
// toolMetricsRecorder/SetToolMetricsRecorder already established for
// pkg/tools (FR-039/C4) — see pkg/tools/browser/metrics.go for the half of
// this wiring that lives in the leaf package (the relay drop-on-full path
// and the Xvfb/PulseAudio sidecar restart hooks, which this package must
// never reach into directly per stream_relay.go's no-import-of-gateway
// contract).
//
// Metrics (FR-019):
//
//	omnipus_browser_live_stream_count           gauge     (none)
//	omnipus_browser_stream_chunks_total         counter   stream_id, kind
//	omnipus_browser_stream_bytes_total          counter   stream_id, kind
//	omnipus_browser_viewer_drop_total           counter   (none)
//	omnipus_browser_decode_error_total          counter   (none)
//	omnipus_browser_capture_restart_total       counter   (none)
//	omnipus_browser_xvfb_sidecar_restart_total  counter   (none)
//	omnipus_browser_pulse_sidecar_restart_total counter   (none)
//	omnipus_browser_ingest_auth_reject_total    counter   reason
//
// fps/bitrate are deliberately exposed as raw counters
// (omnipus_browser_stream_{chunks,bytes}_total), not as an in-process
// windowed gauge: the standard Prometheus idiom is to derive a rate from a
// counter at query time (rate(...)[1m]), which avoids in-process
// wall-clock-window bookkeeping/flakiness and matches how every other
// counter on this /metrics endpoint already works. See the O-4 block below
// for the exact derivation.
//
// stream_id cardinality is bounded, not unbounded: per-stream chunk/byte
// counters are removed when the stream tears down (teardownStreamLocked ->
// removeStream), so steady-state cardinality tracks live streams
// (relayMaxConcurrentStreams=8 ceiling in stream_relay.go), not
// total-streams-ever — keeping this well inside the <10MB Go-process budget
// (NFR-3) over a long-running gateway's uptime.
//
// O-4 (alert thresholds + one-line runbook, required at metrics-land time
// per SC-012/F-10 — "no metric lands without its alert threshold and
// runbook line"):
//
//   - omnipus_browser_live_stream_count: informational capacity gauge, no
//     alert threshold. Runbook: compare against the
//     relayMaxConcurrentStreams=8 design ceiling in stream_relay.go.
//   - fps ~= rate(omnipus_browser_stream_chunks_total{kind="video"}[1m]).
//     Alert if < 15 for > 30s on an install with an attached viewer (half
//     of SC-003's 24fps floor, for alert slack). Runbook: check the
//     "browser video: stepped down encode dimensions" log — a step-down in
//     progress is an expected, self-recovering fps dip, not an incident.
//   - bitrate ~= rate(omnipus_browser_stream_bytes_total[1m]) * 8. No hard
//     threshold (content-dependent); a sustained drop toward 0 on a still-
//     live stream is already covered by the fps alert above.
//   - omnipus_browser_viewer_drop_total: alert if rate(...[1m]) > 5 sustained
//     for > 1m. FR-004 expects OCCASIONAL isolated drops from a slow
//     viewer; a sustained high rate means a wedged viewer connection or
//     genuine capacity pressure. Runbook: check that viewer's WS
//     send-queue depth / RTT and detach it if wedged.
//   - omnipus_browser_decode_error_total: no server-side production
//     call-site exists yet — see RecordDecodeError's doc comment. Do not
//     alert on this metric until a client decode-error wire message lands
//     in contracts/asyncapi.yaml and a real call site is wired.
//   - omnipus_browser_capture_restart_total /
//     omnipus_browser_xvfb_sidecar_restart_total /
//     omnipus_browser_pulse_sidecar_restart_total: alert if
//     rate(...[5m]) > 3. FR-018/FR-021/FR-022's bounded-backoff supervisors
//     recover from an OCCASIONAL crash by design; a high rate means a
//     crash-loop that will soon exhaust the bounded retry budget
//     (xvfbMaxRestartAttempts=5 in display_linux.go,
//     defaultMaxRelaunches=3 in browser_stream.go), which classifies the
//     install not-video-capable / fails the stream. Runbook: check
//     $OMNIPUS_HOME/logs/gateway.log for the sidecar's captured stderr
//     ("xvfb sidecar: restart attempt failed to spawn Xvfb" /
//     "audio sidecar: bring-up failed") to find the underlying crash cause.
//   - omnipus_browser_ingest_auth_reject_total{reason}: alert if
//     rate(...[5m]) > 10 for any reason OTHER than "duplicate_connection"
//     (a handful of duplicate_connection rejects is expected churn around a
//     token re-mint race). FR-024 already audits every rejection, so the
//     runbook is: correlate the alert window with browser.live.ingest_rejected
//     audit events (remote_addr + reason) to find the offending source —
//     any volume of a reason other than duplicate_connection against a
//     loopback-only endpoint is either a token-lifecycle bug or a probe
//     attempt.

package gateway

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// streamChunkCounters is one active stream's FR-019 fps/bitrate source data:
// a running chunk count + byte count per media kind (video/audio), derived
// straight from the ingest path (videoRelayAdapter.Ingest) — no separate
// wall-clock windowing.
type streamChunkCounters struct {
	videoChunks atomic.Int64
	videoBytes  atomic.Int64
	audioChunks atomic.Int64
	audioBytes  atomic.Int64
}

// browserVideoMetrics holds every FR-019 counter/gauge for the live-browser
// video streaming epic. A singleton (globalBrowserVideoMetrics) is created
// at gateway init, registered against pkg/tools/browser's small recorder
// interface at RegisterBrowserVideo, and read by HandleMetrics on every
// GET /metrics scrape.
type browserVideoMetrics struct {
	// viewerDropTotal: FR-019 "per-viewer drop rate" (component D's
	// drop-on-full path, incremented via pkg/tools/browser's
	// browserMetricsRecorder — see stream_relay.go's deliverToViewer).
	viewerDropTotal atomic.Int64

	// decodeErrorTotal: FR-019 "decode-error rate". See RecordDecodeError.
	decodeErrorTotal atomic.Int64

	// captureRestartTotal: FR-019 "capture restart count" — incremented at
	// BOTH orchestrator recovery paths that actually restart the capture
	// pipeline: StepDown's capture-driver restart (browser_stream.go) and
	// handleEncoderDrop's CRIT-002 re-mint+relaunch (browser_stream.go).
	captureRestartTotal atomic.Int64

	// xvfbSidecarRestartTotal / pulseSidecarRestartTotal: FR-019 sidecar
	// restart counts (components B/C). Incremented via pkg/tools/browser's
	// browserMetricsRecorder — see display_linux.go / audiosink_linux.go.
	xvfbSidecarRestartTotal  atomic.Int64
	pulseSidecarRestartTotal atomic.Int64

	// ingestAuthRejectTotal: FR-019 "ingest-auth-reject count", labeled by
	// the same ingestRejectReason values browser_ingest.go already audits
	// (FR-024) — see auditReject.
	ingestAuthRejectMu    sync.Mutex
	ingestAuthRejectTotal map[string]int64 // reason -> count

	// streamChunks: per-stream chunk/byte counters for the fps/bitrate
	// metrics, bounded to currently-live streams (see file doc's
	// cardinality note). Keyed by streamID.
	streamChunksMu sync.Mutex
	streamChunks   map[string]*streamChunkCounters

	// streamCounterFn reads the FR-019 "live-stream count" gauge from the
	// production StreamRelay (which already tracks this — StreamCount() in
	// stream_relay.go). Set once by newOrchestrator; nil until an
	// orchestrator exists (streamCount() then reports 0).
	streamCounterMu sync.Mutex
	streamCounterFn func() int
}

// globalBrowserVideoMetricsPtr holds the package-level singleton behind an
// atomic.Pointer (PT secondary / -race hazard fix). TestObservability_MetricsEmitted
// (Test 28) swaps in a fresh, hermetic registry for its duration and restores
// the previous one afterward via swapGlobalBrowserVideoMetrics, while
// orchestrator goroutines started during that same test (StepDown's async
// capture restart, watchEncoder's async CRIT-002 relaunch — both in
// browser_stream.go) read the singleton concurrently at call time via
// globalBrowserVideoMetrics(). A plain *browserVideoMetrics package var
// reassigned by the test (the pre-fix shape) is an unsynchronized
// read/write race between the test goroutine and those background
// goroutines — real under `go test -race`, not just theoretical, since nothing
// serializes the test's `t.Cleanup` restore against a still-running
// StepDown/watchEncoder goroutine. atomic.Pointer makes both the swap and
// every read race-safe without changing any call site's method-call shape
// beyond adding the `()` this accessor requires.
var globalBrowserVideoMetricsPtr atomic.Pointer[browserVideoMetrics]

func init() {
	globalBrowserVideoMetricsPtr.Store(newBrowserVideoMetrics())
}

// globalBrowserVideoMetrics returns the current metrics singleton. Safe for
// concurrent use alongside swapGlobalBrowserVideoMetrics.
func globalBrowserVideoMetrics() *browserVideoMetrics {
	return globalBrowserVideoMetricsPtr.Load()
}

// swapGlobalBrowserVideoMetrics atomically replaces the singleton with m and
// returns the previous value. Used by TestObservability_MetricsEmitted to
// install a hermetic, per-test registry and restore the real one afterward
// without racing the background goroutines the test itself drives.
func swapGlobalBrowserVideoMetrics(m *browserVideoMetrics) *browserVideoMetrics {
	return globalBrowserVideoMetricsPtr.Swap(m)
}

func newBrowserVideoMetrics() *browserVideoMetrics {
	return &browserVideoMetrics{
		ingestAuthRejectTotal: make(map[string]int64),
		streamChunks:          make(map[string]*streamChunkCounters),
	}
}

// ---- pkg/tools/browser.browserMetricsRecorder implementation ----
// (registered via browser.SetBrowserMetricsRecorder in RegisterBrowserVideo)

func (m *browserVideoMetrics) IncViewerDrop()          { m.viewerDropTotal.Add(1) }
func (m *browserVideoMetrics) IncXvfbSidecarRestart()  { m.xvfbSidecarRestartTotal.Add(1) }
func (m *browserVideoMetrics) IncPulseSidecarRestart() { m.pulseSidecarRestartTotal.Add(1) }

// ---- gateway-package-local hooks (no cross-package interface needed —
// these call sites already live in package gateway) ----

// IncCaptureRestart records one FR-019 "capture restart count" event — see
// the captureRestartTotal field doc for the two call sites.
func (m *browserVideoMetrics) IncCaptureRestart() { m.captureRestartTotal.Add(1) }

// IncIngestAuthReject records one FR-019 "ingest-auth-reject count" event,
// labeled by reason. Called from every rejection path in browser_ingest.go
// via auditReject, so no rejection reason is ever missed (mirrors FR-024's
// "every rejection is audited" guarantee).
func (m *browserVideoMetrics) IncIngestAuthReject(reason string) {
	m.ingestAuthRejectMu.Lock()
	m.ingestAuthRejectTotal[reason]++
	m.ingestAuthRejectMu.Unlock()
}

// RecordDecodeError records one FR-019 "decode-error rate" event.
//
// IMPORTANT: as of this writing, contracts/asyncapi.yaml defines no
// client->server wire message that reports a viewer-side VideoDecoder /
// AudioDecoder decode error (the browser/ws channel's client->server
// messages are browser_attach/browser_input/browser_control/browser_detach/
// browser_tab_action only). Per FR-019/O-4 the metric must still be
// registered ahead of that contract addition, so this method — and the
// omnipus_browser_decode_error_total series it feeds — exist and are
// exercised by Test 28, but there is currently NO production call site: the
// SPA cannot yet report a decode error to the gateway. Wiring this up for
// real requires a new wire message (contracts/components/schemas +
// asyncapi.yaml regeneration per Constraint #8) — tracked as follow-up work,
// not fabricated here.
func (m *browserVideoMetrics) RecordDecodeError() { m.decodeErrorTotal.Add(1) }

// recordChunk accounts one ingested chunk (component M's
// videoRelayAdapter.Ingest, browser_stream.go) against streamID's fps/bitrate
// counters, creating the per-stream entry on first use.
func (m *browserVideoMetrics) recordChunk(streamID, kind string, payloadBytes int) {
	m.streamChunksMu.Lock()
	c, ok := m.streamChunks[streamID]
	if !ok {
		c = &streamChunkCounters{}
		m.streamChunks[streamID] = c
	}
	m.streamChunksMu.Unlock()

	if kind == "audio" {
		c.audioChunks.Add(1)
		c.audioBytes.Add(int64(payloadBytes))
		return
	}
	c.videoChunks.Add(1)
	c.videoBytes.Add(int64(payloadBytes))
}

// removeStream drops streamID's chunk/byte counters (called from
// teardownStreamLocked) so cardinality tracks live streams, not
// total-streams-ever — see the file doc's cardinality note.
func (m *browserVideoMetrics) removeStream(streamID string) {
	m.streamChunksMu.Lock()
	delete(m.streamChunks, streamID)
	m.streamChunksMu.Unlock()
}

// setStreamCounter wires the FR-019 "live-stream count" gauge to the real
// StreamRelay.StreamCount(). Called once by newOrchestrator.
func (m *browserVideoMetrics) setStreamCounter(fn func() int) {
	m.streamCounterMu.Lock()
	m.streamCounterFn = fn
	m.streamCounterMu.Unlock()
}

// streamCount reads the FR-019 "live-stream count" gauge. 0 if no
// orchestrator has registered a counter yet.
func (m *browserVideoMetrics) streamCount() int {
	m.streamCounterMu.Lock()
	fn := m.streamCounterFn
	m.streamCounterMu.Unlock()
	if fn == nil {
		return 0
	}
	return fn()
}

// writeBrowserVideoMetrics appends every FR-019 metric to sb in Prometheus
// text-exposition format, matching the style HandleMetrics already uses for
// the FR-039 tool-registry metrics above it on the same /metrics endpoint.
func writeBrowserVideoMetrics(sb *strings.Builder) {
	m := globalBrowserVideoMetrics()

	sb.WriteString("# HELP omnipus_browser_live_stream_count Active live-browser video streams.\n")
	sb.WriteString("# TYPE omnipus_browser_live_stream_count gauge\n")
	fmt.Fprintf(sb, "omnipus_browser_live_stream_count %d\n", m.streamCount())

	sb.WriteString(
		"# HELP omnipus_browser_stream_chunks_total Encoded chunks relayed per stream and media kind (fps = rate() over this).\n",
	)
	sb.WriteString("# TYPE omnipus_browser_stream_chunks_total counter\n")
	sb.WriteString(
		"# HELP omnipus_browser_stream_bytes_total Encoded bytes relayed per stream and media kind (bitrate = rate()*8 over this).\n",
	)
	sb.WriteString("# TYPE omnipus_browser_stream_bytes_total counter\n")
	m.streamChunksMu.Lock()
	streamIDs := make([]string, 0, len(m.streamChunks))
	for id := range m.streamChunks {
		streamIDs = append(streamIDs, id)
	}
	sort.Strings(streamIDs)
	for _, id := range streamIDs {
		c := m.streamChunks[id]
		if v := c.videoChunks.Load(); v > 0 {
			fmt.Fprintf(
				sb,
				"omnipus_browser_stream_chunks_total{stream_id=%q,kind=\"video\"} %d\n",
				id,
				v,
			)
		}
		if v := c.videoBytes.Load(); v > 0 {
			fmt.Fprintf(
				sb,
				"omnipus_browser_stream_bytes_total{stream_id=%q,kind=\"video\"} %d\n",
				id,
				v,
			)
		}
		if a := c.audioChunks.Load(); a > 0 {
			fmt.Fprintf(
				sb,
				"omnipus_browser_stream_chunks_total{stream_id=%q,kind=\"audio\"} %d\n",
				id,
				a,
			)
		}
		if a := c.audioBytes.Load(); a > 0 {
			fmt.Fprintf(
				sb,
				"omnipus_browser_stream_bytes_total{stream_id=%q,kind=\"audio\"} %d\n",
				id,
				a,
			)
		}
	}
	m.streamChunksMu.Unlock()

	sb.WriteString(
		"# HELP omnipus_browser_viewer_drop_total Chunks dropped in isolation to a full-queue viewer (FR-004).\n",
	)
	sb.WriteString("# TYPE omnipus_browser_viewer_drop_total counter\n")
	fmt.Fprintf(sb, "omnipus_browser_viewer_drop_total %d\n", m.viewerDropTotal.Load())

	sb.WriteString(
		"# HELP omnipus_browser_decode_error_total Viewer-reported decode errors (no wire signal yet — see code comment).\n",
	)
	sb.WriteString("# TYPE omnipus_browser_decode_error_total counter\n")
	fmt.Fprintf(sb, "omnipus_browser_decode_error_total %d\n", m.decodeErrorTotal.Load())

	sb.WriteString(
		"# HELP omnipus_browser_capture_restart_total Capture pipeline restarts (step-down + CRIT-002 encoder relaunch).\n",
	)
	sb.WriteString("# TYPE omnipus_browser_capture_restart_total counter\n")
	fmt.Fprintf(sb, "omnipus_browser_capture_restart_total %d\n", m.captureRestartTotal.Load())

	sb.WriteString(
		"# HELP omnipus_browser_xvfb_sidecar_restart_total Xvfb sidecar bounded-backoff restarts (FR-021).\n",
	)
	sb.WriteString("# TYPE omnipus_browser_xvfb_sidecar_restart_total counter\n")
	fmt.Fprintf(
		sb,
		"omnipus_browser_xvfb_sidecar_restart_total %d\n",
		m.xvfbSidecarRestartTotal.Load(),
	)

	sb.WriteString(
		"# HELP omnipus_browser_pulse_sidecar_restart_total PulseAudio sidecar bounded-backoff restarts (FR-022).\n",
	)
	sb.WriteString("# TYPE omnipus_browser_pulse_sidecar_restart_total counter\n")
	fmt.Fprintf(
		sb,
		"omnipus_browser_pulse_sidecar_restart_total %d\n",
		m.pulseSidecarRestartTotal.Load(),
	)

	sb.WriteString(
		"# HELP omnipus_browser_ingest_auth_reject_total Capture-ingest auth rejections by reason (FR-013/FR-024).\n",
	)
	sb.WriteString("# TYPE omnipus_browser_ingest_auth_reject_total counter\n")
	m.ingestAuthRejectMu.Lock()
	reasons := make([]string, 0, len(m.ingestAuthRejectTotal))
	for r := range m.ingestAuthRejectTotal {
		reasons = append(reasons, r)
	}
	sort.Strings(reasons)
	for _, r := range reasons {
		fmt.Fprintf(
			sb,
			"omnipus_browser_ingest_auth_reject_total{reason=%q} %d\n",
			r,
			m.ingestAuthRejectTotal[r],
		)
	}
	m.ingestAuthRejectMu.Unlock()
}

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// metrics.go — the pkg/tools/browser half of the FR-019 observability wiring
// for the live-browser video streaming epic
// (docs/internal/specs/live-browser-video-streaming-spec.md FR-019, O-4,
// Test 28). Two of the seven FR-019 metrics have their real hook INSIDE this
// leaf package: the stream relay's drop-on-full path (component D —
// deliverToViewer in stream_relay.go) and the Xvfb/PulseAudio sidecar
// bounded-backoff restart points (components B/C — display_linux.go /
// audiosink_linux.go). This package must never import pkg/gateway
// (stream_relay.go's Viewer doc comment: "no import of pkg/gateway so this
// package never depends on the gateway package"), so — exactly like
// pkg/tools/compositor.go's toolMetricsRecorder / SetToolMetricsRecorder
// pattern the gateway already uses for FR-039 (wired via
// tools.SetToolMetricsRecorder in gateway.go) — the counters themselves are
// OWNED by pkg/gateway (browser_metrics.go) and this package only holds a
// small interface + package-level var that the gateway registers itself
// against at boot (RegisterBrowserVideo). This is the SAME existing
// metrics/observability system, not a new one.

package browser

// browserMetricsRecorder is the minimal interface this package needs to emit
// the two FR-019 metrics whose real hooks live here. pkg/gateway's
// browserVideoMetrics type implements it; this package uses the unexported
// interface (mirrors pkg/tools/compositor.go's toolMetricsRecorder) so there
// is no import cycle.
type browserMetricsRecorder interface {
	// IncViewerDrop records one FR-019 "per-viewer drop rate" event: a chunk
	// offered to an attached viewer was dropped because its outbound queue
	// was full (FR-004's drop-in-isolation discipline) — see
	// deliverToViewer in stream_relay.go.
	IncViewerDrop()

	// IncXvfbSidecarRestart records one FR-019 "Xvfb sidecar restart count"
	// event: the Xvfb sidecar (component B, FR-021) recovered from an
	// unexpected exit via its bounded-backoff supervisor — see
	// xvfbSidecar.supervise in display_linux.go.
	IncXvfbSidecarRestart()

	// IncPulseSidecarRestart records one FR-019 "PulseAudio sidecar restart
	// count" event: the PulseAudio sidecar (component C, FR-022) recovered
	// from an unexpected exit via its bounded-backoff supervisor — see
	// pulseSidecar.runAndSupervise in audiosink_linux.go.
	IncPulseSidecarRestart()
}

// nopBrowserMetrics satisfies browserMetricsRecorder when no gateway has
// registered a real recorder yet (e.g. package-level unit tests that
// construct a StreamRelay/sidecar directly and never call
// SetBrowserMetricsRecorder).
type nopBrowserMetrics struct{}

func (nopBrowserMetrics) IncViewerDrop()          {}
func (nopBrowserMetrics) IncXvfbSidecarRestart()  {}
func (nopBrowserMetrics) IncPulseSidecarRestart() {}

// activeBrowserMetricsRecorder is swapped at gateway boot
// (RegisterBrowserVideo in pkg/gateway/browser_stream.go).
var activeBrowserMetricsRecorder browserMetricsRecorder = nopBrowserMetrics{}

// SetBrowserMetricsRecorder registers the gateway-level FR-019 metrics
// recorder. Called once at gateway boot, mirroring
// tools.SetToolMetricsRecorder (FR-039/C4).
func SetBrowserMetricsRecorder(m browserMetricsRecorder) {
	if m != nil {
		activeBrowserMetricsRecorder = m
	}
}

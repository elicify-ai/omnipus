//go:build linux

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// metrics_linux_test.go — shared FR-019 test double for display_linux_test.go
// (Test 17) and audiosink_linux_test.go (Test 31), which additively assert
// that a REAL Xvfb/PulseAudio sidecar crash+restart cycle fires the
// browserMetricsRecorder hooks added in display_linux.go/audiosink_linux.go
// — see pkg/gateway/browser_metrics_test.go's Test 28 file doc for why the
// end-to-end proof for these two metrics lives here rather than in the
// cross-platform gateway package.

package browser

import "sync/atomic"

// fakeSidecarMetricsRecorder is a hermetic browserMetricsRecorder that only
// counts the two sidecar-restart hooks these tests care about.
type fakeSidecarMetricsRecorder struct {
	viewerDrops  atomic.Int64
	xvfbRestarts atomic.Int64
	pulseRestart atomic.Int64
}

func (f *fakeSidecarMetricsRecorder) IncViewerDrop()          { f.viewerDrops.Add(1) }
func (f *fakeSidecarMetricsRecorder) IncXvfbSidecarRestart()  { f.xvfbRestarts.Add(1) }
func (f *fakeSidecarMetricsRecorder) IncPulseSidecarRestart() { f.pulseRestart.Add(1) }

// swapMetricsRecorder registers f as the active recorder for the duration of
// the calling test and restores the previous one (nopBrowserMetrics in
// practice, since production wiring only happens at gateway boot) on
// cleanup.
func swapMetricsRecorder(t interface{ Cleanup(func()) }, f *fakeSidecarMetricsRecorder) {
	prev := activeBrowserMetricsRecorder
	activeBrowserMetricsRecorder = f
	t.Cleanup(func() { activeBrowserMetricsRecorder = prev })
}

// Omnipus — Xvfb virtual-display sidecar tests (Test 17, FR-021 / US-10).
// License: MIT. Copyright (c) 2026 Omnipus contributors.
//
// These tests are hermetic: no real Xvfb binary is required. commandFunc and
// readyFunc are injection seams (see display_linux.go) that let the test
// substitute a plain `sh` process for "Xvfb" and a trivial always-ready check
// for the real X11-socket probe, so the crash/restart/kill machinery is
// exercised without depending on X11 being installed in CI.

//go:build linux

package browser

import (
	"context"
	"os/exec"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// shrinkXvfbTiming lowers the package-level backoff/poll/kill tunables for
// the duration of a test (mirrors pkg/daemon/daemon_unix.go's
// stopGracePeriod/pollInterval test-override convention) and restores them
// on cleanup.
func shrinkXvfbTiming(t *testing.T) {
	t.Helper()
	origBase, origMax, origAttempts := xvfbBaseBackoff, xvfbMaxBackoff, xvfbMaxRestartAttempts
	origReadyPoll, origReadyTimeout := xvfbReadyPollInterval, xvfbReadyTimeout
	origKillGrace, origKillPoll := xvfbKillGrace, xvfbKillPollInterval

	xvfbBaseBackoff = 5 * time.Millisecond
	xvfbMaxBackoff = 50 * time.Millisecond
	xvfbMaxRestartAttempts = 5
	xvfbReadyPollInterval = 2 * time.Millisecond
	xvfbReadyTimeout = 2 * time.Second
	xvfbKillGrace = 200 * time.Millisecond
	xvfbKillPollInterval = 2 * time.Millisecond

	t.Cleanup(func() {
		xvfbBaseBackoff, xvfbMaxBackoff, xvfbMaxRestartAttempts = origBase, origMax, origAttempts
		xvfbReadyPollInterval, xvfbReadyTimeout = origReadyPoll, origReadyTimeout
		xvfbKillGrace, xvfbKillPollInterval = origKillGrace, origKillPoll
	})
}

// TestXvfbSidecar_SpawnSuperviseRestart is Test 17 (traceability: FR-021,
// US-10/AC-1/AC-2/AC-3). It covers, with a fake "Xvfb" command:
//  1. Start publishes DISPLAY and marks the sidecar healthy.
//  2. A simulated crash (the fake process exiting on its own) triggers a
//     bounded-backoff restart, after which DISPLAY is republished and the
//     sidecar is healthy again.
//  3. Stop tears the sidecar down cleanly (idempotent, no hang, unhealthy
//     with an empty Display() afterward).
//  4. An absent binary classifies the sidecar not-healthy/not-available
//     without hanging, panicking, or otherwise touching anything else.
func TestXvfbSidecar_SpawnSuperviseRestart(t *testing.T) {
	shrinkXvfbTiming(t)

	t.Run("spawn, publish DISPLAY, survive a simulated crash via restart, clean Stop", func(t *testing.T) {
		var launches int32
		// FR-019 (Test 28/O-4): a real crash+restart cycle must fire
		// IncXvfbSidecarRestart exactly once via the registered recorder —
		// the end-to-end proof for the xvfb_sidecar_restart_total metric
		// (see pkg/gateway/browser_metrics_test.go for the registry-level
		// proof and why this specific hook is verified here instead).
		metrics := &fakeSidecarMetricsRecorder{}
		swapMetricsRecorder(t, metrics)

		sc := newXvfbSidecar(DisplayConfig{Width: 800, Height: 600, Depth: 24})
		sc.readyFunc = func(int) bool { return true } // hermetic: no real X11 socket to probe
		sc.commandFunc = func(ctx context.Context, name string, arg ...string) *exec.Cmd {
			n := atomic.AddInt32(&launches, 1)
			if n == 1 {
				// First launch "crashes" (exits on its own) almost immediately.
				return exec.CommandContext(ctx, "sh", "-c", "sleep 0.05")
			}
			// Every subsequent (restarted) launch stays alive until SIGTERM'd.
			return exec.CommandContext(ctx, "sh", "-c", "trap 'exit 0' TERM; sleep 30")
		}

		startCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		require.NoError(t, sc.Start(startCtx))
		require.True(t, sc.Healthy())
		require.NotEmpty(t, sc.Display())

		// Wait for the simulated crash to be observed and a restart to land.
		// Bounded: the test fails (not hangs) if no restart happens.
		require.Eventually(t, func() bool {
			return atomic.LoadInt32(&launches) >= 2 && sc.Healthy()
		}, 2*time.Second, 5*time.Millisecond,
			"expected a bounded-backoff restart after the simulated Xvfb crash")

		require.NotEmpty(t, sc.Display(), "DISPLAY should be republished after the restart")
		require.Equal(t, int64(1), metrics.xvfbRestarts.Load(),
			"expected exactly one FR-019 xvfb sidecar restart metric increment for the one observed restart")

		sc.Stop()
		require.False(t, sc.Healthy())
		require.Empty(t, sc.Display())

		require.GreaterOrEqual(t, atomic.LoadInt32(&launches), int32(2),
			"expected at least one restart launch beyond the initial one")

		// Stop must be idempotent — a second call must not hang or panic.
		sc.Stop()
	})

	t.Run(
		"bounded restart attempts: a permanently-crashing Xvfb gives up, not-healthy, no infinite restart",
		func(t *testing.T) {
			var launches int32
			sc := newXvfbSidecar(DisplayConfig{})
			// Only the very first launch ever becomes "ready" — every restart
			// attempt after that fails to reach ready before its process exits,
			// exactly like a real Xvfb that comes up once and then crash-loops
			// (real xvfbDisplayReady would likewise never see the X11 socket
			// appear for a process that dies instantly on every retry).
			sc.readyFunc = func(int) bool {
				return atomic.LoadInt32(&launches) == 1
			}
			sc.commandFunc = func(ctx context.Context, name string, arg ...string) *exec.Cmd {
				n := atomic.AddInt32(&launches, 1)
				if n == 1 {
					return exec.CommandContext(ctx, "sh", "-c", "sleep 0.05") // starts fine, then "crashes"
				}
				return exec.CommandContext(ctx, "sh", "-c", "exit 1") // every restart attempt fails immediately
			}

			require.NoError(t, sc.Start(context.Background()))
			require.True(t, sc.Healthy())

			// Eventually the supervisor exhausts xvfbMaxRestartAttempts and gives
			// up; the launch count converges to (1 initial + xvfbMaxRestartAttempts)
			// and stays there (proves the restart loop is bounded, not infinite).
			require.Eventually(t, func() bool {
				return atomic.LoadInt32(&launches) >= int32(1+xvfbMaxRestartAttempts) && !sc.Healthy()
			}, 3*time.Second, 5*time.Millisecond, "expected the supervisor to exhaust its bounded restart attempts and give up")

			stalled := atomic.LoadInt32(&launches)
			time.Sleep(100 * time.Millisecond)
			require.Equal(t, stalled, atomic.LoadInt32(&launches),
				"restart attempts must stop once the bound is exceeded, not continue forever")
			require.False(t, sc.Healthy())

			sc.Stop() // must not hang even though the supervisor already gave up
		},
	)

	t.Run("absent Xvfb binary classifies not-healthy/not-available, never hangs or panics", func(t *testing.T) {
		sc := newXvfbSidecar(DisplayConfig{Width: 640, Height: 480, Depth: 24})
		sc.readyFunc = func(int) bool { return true }
		sc.commandFunc = func(ctx context.Context, name string, arg ...string) *exec.Cmd {
			return exec.CommandContext(ctx, "/nonexistent/omnipus-test-xvfb-missing-binary")
		}

		err := sc.Start(context.Background())
		require.Error(t, err)
		require.False(t, sc.Healthy())
		require.Empty(t, sc.Display())

		// Stop on a sidecar that never successfully started must not hang.
		sc.Stop()
	})
}

func TestXvfbSidecar_DisplayConfigDefaults(t *testing.T) {
	cfg := DisplayConfig{}.withDefaults()
	require.Equal(t, defaultDisplayWidth, cfg.Width)
	require.Equal(t, defaultDisplayHeight, cfg.Height)
	require.Equal(t, defaultDisplayDepth, cfg.Depth)

	cfg2 := DisplayConfig{Width: 1920, Height: 1080, Depth: 30}.withDefaults()
	require.Equal(t, 1920, cfg2.Width)
	require.Equal(t, 1080, cfg2.Height)
	require.Equal(t, 30, cfg2.Depth)
}

func TestNewDisplaySidecar_ReturnsXvfbSidecarOnLinux(t *testing.T) {
	ds := NewDisplaySidecar(DisplayConfig{})
	_, ok := ds.(*xvfbSidecar)
	require.True(t, ok, "NewDisplaySidecar should return the real Xvfb sidecar on Linux")
	ds.Stop() // never started; must be a safe no-op
}

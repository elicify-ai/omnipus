// Omnipus — Xvfb virtual-display sidecar (live-browser-video-streaming spec,
// FR-021 / US-10). License: MIT. Copyright (c) 2026 Omnipus contributors.
//
// The A2 headful-capture runtime needs a framebuffer for Chrome to render
// into on a headless Linux host. This file supervises an `Xvfb` child
// process the same way the rest of Omnipus supervises long-lived local
// sidecars (spawn, watch for exit, bounded-backoff restart, clean teardown —
// see pkg/daemon/daemon.go / daemon_unix.go for the pattern this mirrors:
// SIGTERM-then-SIGKILL kill escalation with a poll loop, and package-level
// tunables so tests can shrink timing). Linux-only; display_other.go stubs
// this out (not-video-capable) everywhere else — see spec §Platform Matrix.

//go:build linux

package browser

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/elicify-ai/omnipus/pkg/logger"
)

// Default framebuffer geometry (FR-021) used when a DisplayConfig field is
// left zero-valued.
const (
	defaultDisplayWidth  = 1280
	defaultDisplayHeight = 720
	defaultDisplayDepth  = 24
)

// Bounded-backoff restart + readiness/kill timing. Package-level vars (not
// consts) so tests can shrink them, mirroring pkg/daemon/daemon_unix.go's
// stopGracePeriod/pollInterval pattern.
var (
	xvfbBaseBackoff        = 250 * time.Millisecond
	xvfbMaxBackoff         = 10 * time.Second
	xvfbMaxRestartAttempts = 5
	xvfbReadyPollInterval  = 20 * time.Millisecond
	xvfbReadyTimeout       = 5 * time.Second
	xvfbKillGrace          = 2 * time.Second
	xvfbKillPollInterval   = 20 * time.Millisecond
)

// displayScanMax bounds the free-display-number probe (pickFreeDisplayNumber)
// so a pathologically full /tmp/.X11-unix never spins forever.
const displayScanMax = 200

// DisplayConfig configures the virtual-display sidecar's framebuffer.
type DisplayConfig struct {
	Width  int
	Height int
	Depth  int
}

// withDefaults returns cfg with zero-valued fields replaced by the
// 1280x720x24 default (FR-021).
func (cfg DisplayConfig) withDefaults() DisplayConfig {
	if cfg.Width <= 0 {
		cfg.Width = defaultDisplayWidth
	}
	if cfg.Height <= 0 {
		cfg.Height = defaultDisplayHeight
	}
	if cfg.Depth <= 0 {
		cfg.Depth = defaultDisplayDepth
	}
	return cfg
}

// DisplaySidecar is the supervised virtual-display process consumed by the
// headful-Chrome launch path (managedExecAllocatorOpts, W2) to give headful
// Chrome a framebuffer. This file provides the real Linux implementation;
// display_other.go provides the non-Linux stub with an identical surface.
type DisplaySidecar interface {
	// Start launches the sidecar and blocks until the display is confirmed
	// ready, ctx is canceled, or the readiness timeout expires. A non-nil
	// error means the sidecar is NOT available — callers MUST classify the
	// install not-video-capable (US-10/AC-3) and MUST NOT crash the gateway
	// or degrade agent browsing.
	Start(ctx context.Context) error
	// Display returns the wired DISPLAY value (e.g. ":37"), or "" if the
	// sidecar never started successfully or has since stopped.
	Display() string
	// Healthy reports whether the sidecar is currently up and supervised.
	Healthy() bool
	// Stop tears the sidecar down cleanly and frees the display number.
	// Safe to call even if Start was never called or failed.
	Stop()
}

// NewDisplaySidecar constructs the platform-appropriate DisplaySidecar. On
// Linux (this file) it is a real supervised Xvfb process.
func NewDisplaySidecar(cfg DisplayConfig) DisplaySidecar {
	return newXvfbSidecar(cfg)
}

// xvfbSidecar supervises a single `Xvfb :N -screen 0 WxHxDepth -ac -nolisten
// tcp` child process (FR-021 / US-10). Lifecycle: spawn from Start, watch for
// unexpected exit in a background goroutine, restart with a capped backoff
// (bounded attempts — after which the install is left not-video-capable
// rather than restarting forever), tear down cleanly on Stop.
type xvfbSidecar struct {
	cfg DisplayConfig

	// commandFunc / readyFunc are injection seams for hermetic unit testing
	// (Test 17, TestXvfbSidecar_SpawnSuperviseRestart). Production code never
	// overrides them: commandFunc builds the *exec.Cmd for one launch
	// attempt (defaults to exec.CommandContext against the real "Xvfb"
	// binary on PATH); readyFunc reports whether a display number is up
	// (defaults to checking the X11 socket).
	commandFunc func(ctx context.Context, name string, arg ...string) *exec.Cmd
	readyFunc   func(displayNum int) bool

	mu               sync.Mutex
	started          bool // Start() has been called once (guards double-Start)
	superviseStarted bool // the supervise() goroutine was launched at least once (guards Stop's doneCh join)
	stopped          bool // Stop() has run (tells supervise() not to restart)
	healthy          bool
	cmd              *exec.Cmd
	displayNum       int
	restarts         int

	stopCh chan struct{} // closed by Stop to signal "do not restart"
	doneCh chan struct{} // closed when the supervise() goroutine exits; Stop joins on this
}

func newXvfbSidecar(cfg DisplayConfig) *xvfbSidecar {
	return &xvfbSidecar{
		cfg:         cfg.withDefaults(),
		commandFunc: exec.CommandContext,
		readyFunc:   xvfbDisplayReady,
		stopCh:      make(chan struct{}),
		doneCh:      make(chan struct{}),
	}
}

// Start launches Xvfb on a freshly-picked free display number and blocks
// until it is confirmed ready (or ctx / the readiness timeout expires). On
// success it arms the crash-restart supervisor and returns nil. On failure
// (binary absent, spawn error, never became ready) it returns a descriptive
// error and Healthy() stays false — callers classify the install
// not-video-capable and continue headless (spec Platform Matrix / US-10/AC-3).
func (s *xvfbSidecar) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return fmt.Errorf("browser: xvfb sidecar: Start called more than once")
	}
	s.started = true
	s.mu.Unlock()

	dispNum, err := pickFreeDisplayNumber()
	if err != nil {
		return fmt.Errorf("browser: xvfb sidecar: %w", err)
	}

	cmd, exitCh, err := s.launch(dispNum)
	if err != nil {
		return err
	}

	readyCtx, cancel := context.WithTimeout(ctx, xvfbReadyTimeout)
	rerr := s.waitReady(readyCtx, dispNum, exitCh)
	cancel()
	if rerr != nil {
		s.killCmd(cmd)
		return rerr
	}

	s.mu.Lock()
	s.cmd = cmd
	s.displayNum = dispNum
	s.healthy = true
	s.superviseStarted = true
	s.mu.Unlock()

	logger.InfoCF("browser", "xvfb sidecar: started and ready", map[string]any{
		"display": fmt.Sprintf(":%d", dispNum),
		"width":   s.cfg.Width,
		"height":  s.cfg.Height,
		"depth":   s.cfg.Depth,
	})

	go s.supervise(exitCh)
	return nil
}

// Display returns the wired DISPLAY value, or "" when not currently healthy.
func (s *xvfbSidecar) Display() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.healthy {
		return ""
	}
	return fmt.Sprintf(":%d", s.displayNum)
}

// Healthy reports whether Xvfb is currently up and supervised.
func (s *xvfbSidecar) Healthy() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.healthy
}

// Stop tears the sidecar down: signals the supervisor to stop restarting,
// kills the live process (graceful SIGTERM, bounded grace, then SIGKILL),
// joins the supervisor goroutine, and frees the display number. Idempotent
// and safe to call even if Start was never called or failed.
func (s *xvfbSidecar) Stop() {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	s.stopped = true
	supervising := s.superviseStarted
	cmd := s.cmd
	dispNum := s.displayNum
	s.healthy = false
	s.mu.Unlock()

	close(s.stopCh)

	if cmd != nil {
		s.killCmd(cmd)
	}

	if supervising {
		<-s.doneCh // wait for the supervisor goroutine to fully exit
	}

	s.mu.Lock()
	s.cmd = nil
	s.displayNum = 0
	s.mu.Unlock()

	logger.InfoCF("browser", "xvfb sidecar: stopped", map[string]any{
		"display": fmt.Sprintf(":%d", dispNum),
	})
}

// --- supervision -----------------------------------------------------------

// supervise watches the current Xvfb process for unexpected exit and, when
// one occurs (and Stop has not been called), retries launch+ready with a
// capped backoff up to xvfbMaxRestartAttempts. If every attempt fails it
// gives up permanently — the sidecar is left unhealthy (not-video-capable)
// rather than restarting forever.
func (s *xvfbSidecar) supervise(initialExit chan error) {
	defer close(s.doneCh)
	curExit := initialExit

	for {
		select {
		case waitErr := <-curExit:
			s.mu.Lock()
			stopped := s.stopped
			s.healthy = false
			s.mu.Unlock()
			if stopped {
				return
			}
			logger.WarnCF("browser", "xvfb sidecar: Xvfb exited unexpectedly — attempting restart", map[string]any{
				"error": fmt.Sprint(waitErr),
			})
		case <-s.stopCh:
			return
		}

		newCmd, newDisp, newExit, ok := s.restartWithBackoff()
		if !ok {
			logger.ErrorCF("browser", "xvfb sidecar: exceeded max restart attempts — giving up; install classifies not-video-capable", map[string]any{
				"max_attempts": xvfbMaxRestartAttempts,
			})
			return
		}

		s.mu.Lock()
		if s.stopped {
			s.mu.Unlock()
			s.killCmd(newCmd) // Stop() raced us; don't leak the just-launched process
			return
		}
		s.cmd = newCmd
		s.displayNum = newDisp
		s.healthy = true
		s.mu.Unlock()

		logger.InfoCF("browser", "xvfb sidecar: restarted", map[string]any{
			"display": fmt.Sprintf(":%d", newDisp),
		})
		// FR-019 "Xvfb sidecar restart count" (Test 28, O-4): count only a
		// CONFIRMED restart (ready again after the earlier successful
		// Start) — supervise() only ever runs after that initial Start
		// succeeded, so every completed pass through this loop is a real
		// recovery, not the first bring-up.
		activeBrowserMetricsRecorder.IncXvfbSidecarRestart()
		curExit = newExit
	}
}

// restartWithBackoff retries launch+ready-wait up to xvfbMaxRestartAttempts
// times with a capped exponential backoff between attempts. ok is false if
// every attempt failed, or Stop was called mid-retry.
func (s *xvfbSidecar) restartWithBackoff() (cmd *exec.Cmd, dispNum int, exitCh chan error, ok bool) {
	for attempt := 1; attempt <= xvfbMaxRestartAttempts; attempt++ {
		s.mu.Lock()
		s.restarts++
		s.mu.Unlock()

		select {
		case <-time.After(xvfbBackoffDuration(attempt)):
		case <-s.stopCh:
			return nil, 0, nil, false
		}

		disp, err := pickFreeDisplayNumber()
		if err != nil {
			logger.WarnCF("browser", "xvfb sidecar: restart: no free display number, will retry", map[string]any{
				"attempt": attempt, "error": err.Error(),
			})
			continue
		}

		c, ec, err := s.launch(disp)
		if err != nil {
			logger.WarnCF("browser", "xvfb sidecar: restart attempt failed to spawn Xvfb", map[string]any{
				"attempt": attempt, "error": err.Error(),
			})
			continue
		}

		readyCtx, cancel := context.WithTimeout(context.Background(), xvfbReadyTimeout)
		rerr := s.waitReady(readyCtx, disp, ec)
		cancel()
		if rerr != nil {
			logger.WarnCF("browser", "xvfb sidecar: restart attempt did not become ready", map[string]any{
				"attempt": attempt, "error": rerr.Error(),
			})
			s.killCmd(c)
			continue
		}

		return c, disp, ec, true
	}
	return nil, 0, nil, false
}

// xvfbBackoffDuration returns a capped exponential backoff for the given
// 1-based restart attempt number (250ms, 500ms, 1s, 2s, ... capped at
// xvfbMaxBackoff).
func xvfbBackoffDuration(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 10 { // guard against overflow from an unexpectedly large attempt count
		attempt = 10
	}
	d := xvfbBaseBackoff * time.Duration(int64(1)<<uint(attempt-1))
	if d <= 0 || d > xvfbMaxBackoff {
		return xvfbMaxBackoff
	}
	return d
}

// --- process launch / kill ---------------------------------------------------

// launch starts one Xvfb attempt on the given display number and returns the
// live *exec.Cmd plus a channel that receives exactly one value (the
// cmd.Wait() result) when the process exits. Never calls cmd.Wait() anywhere
// else — Wait must be called at most once per *exec.Cmd.
func (s *xvfbSidecar) launch(dispNum int) (*exec.Cmd, chan error, error) {
	args := []string{
		fmt.Sprintf(":%d", dispNum),
		"-screen", "0", fmt.Sprintf("%dx%dx%d", s.cfg.Width, s.cfg.Height, s.cfg.Depth),
		"-ac", "-nolisten", "tcp",
	}
	cmd := s.commandFunc(context.Background(), "Xvfb", args...)
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true              // detach from the gateway's process group
	cmd.SysProcAttr.Pdeathsig = syscall.SIGTERM // orphan safety net if the gateway dies uncleanly
	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf

	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("browser: xvfb sidecar: spawn Xvfb :%d: %w", dispNum, err)
	}

	exitCh := make(chan error, 1)
	go func() {
		waitErr := cmd.Wait()
		if waitErr != nil {
			if msg := strings.TrimSpace(stderrBuf.String()); msg != "" {
				waitErr = fmt.Errorf("%w (stderr: %s)", waitErr, msg)
			}
		}
		exitCh <- waitErr
	}()
	return cmd, exitCh, nil
}

// waitReady blocks until readyFunc reports the display up, exitCh fires
// (the process died before becoming ready), or ctx is done (timeout/cancel).
func (s *xvfbSidecar) waitReady(ctx context.Context, dispNum int, exitCh chan error) error {
	if s.readyFunc(dispNum) {
		return nil
	}
	ticker := time.NewTicker(xvfbReadyPollInterval)
	defer ticker.Stop()
	for {
		select {
		case err := <-exitCh:
			return fmt.Errorf("browser: xvfb sidecar: Xvfb :%d exited before becoming ready: %w", dispNum, err)
		case <-ctx.Done():
			return fmt.Errorf("browser: xvfb sidecar: display :%d did not become ready: %w", dispNum, ctx.Err())
		case <-ticker.C:
			if s.readyFunc(dispNum) {
				return nil
			}
		}
	}
}

// killCmd terminates cmd: SIGTERM, then polls liveness via syscall.Kill(pid,
// 0) (mirrors pkg/daemon/daemon_unix.go's killProcess escalation) for up to
// xvfbKillGrace before escalating to SIGKILL. Deliberately never calls
// cmd.Wait() (that is the exclusive job of the goroutine started in launch —
// calling Wait twice on the same *exec.Cmd is invalid).
func (s *xvfbSidecar) killCmd(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid

	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		logger.DebugCF("browser", "xvfb sidecar: SIGTERM failed (process may already be gone)", map[string]any{
			"pid": pid, "error": err.Error(),
		})
	}

	deadline := time.Now().Add(xvfbKillGrace)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err == syscall.ESRCH {
			return // exited cleanly
		}
		time.Sleep(xvfbKillPollInterval)
	}

	logger.WarnCF("browser", "xvfb sidecar: grace period expired; sending SIGKILL", map[string]any{"pid": pid})
	if err := cmd.Process.Kill(); err != nil {
		logger.DebugCF("browser", "xvfb sidecar: SIGKILL failed (process may already be gone)", map[string]any{
			"pid": pid, "error": err.Error(),
		})
	}
}

// --- display number allocation -----------------------------------------------

// xvfbSocketPath returns the X11 socket path Xvfb creates once it is
// listening for display number n — the definitive readiness signal.
func xvfbSocketPath(n int) string {
	return filepath.Join("/tmp/.X11-unix", fmt.Sprintf("X%d", n))
}

// xvfbLockPath returns the classic X11 lock-file path for display number n.
func xvfbLockPath(n int) string {
	return fmt.Sprintf("/tmp/.X%d-lock", n)
}

// displayNumberFree reports whether neither the socket nor the lock file for
// display number n exists (i.e. it is safe to try to bind it).
func displayNumberFree(n int) bool {
	if _, err := os.Stat(xvfbSocketPath(n)); err == nil {
		return false
	}
	if _, err := os.Stat(xvfbLockPath(n)); err == nil {
		return false
	}
	return true
}

// xvfbDisplayReady is the default readyFunc: it reports whether Xvfb is
// listening on display number n by checking for its X11 socket.
func xvfbDisplayReady(n int) bool {
	_, err := os.Stat(xvfbSocketPath(n))
	return err == nil
}

// pickFreeDisplayNumber probes /tmp/.X11-unix/X<N> and /tmp/.X<N>-lock for
// N in 1..displayScanMax and returns the first free one.
func pickFreeDisplayNumber() (int, error) {
	for n := 1; n <= displayScanMax; n++ {
		if displayNumberFree(n) {
			return n, nil
		}
	}
	return 0, fmt.Errorf("no free X display number found in range 1..%d", displayScanMax)
}

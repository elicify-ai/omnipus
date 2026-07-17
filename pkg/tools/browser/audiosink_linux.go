//go:build linux

package browser

// audiosink_linux.go implements the PulseAudio sidecar (FR-022, US-11,
// ADR-044-spike-results.md §S-3). Lifecycle mirrors the channel sidecar
// supervision pattern used elsewhere in Omnipus (cf. Signal -> signal-cli:
// spawn from Start, supervise, restart with bounded backoff, teardown via
// Stop) and the BrowserCoordinator's own crash-detector (coordinator.go
// watchForCrash/ensureLaunched: block on process exit, mark unhealthy,
// restart, never silently give up).
//
// Two deliberate deviations from the literal spike recipe
// (`pulseaudio -D --exit-idle-time=-1 --disable-shm=1`):
//
//  1. Drop `-D` (daemonize). `-D` forks PulseAudio to the background and the
//     process we spawned exits immediately — which would throw away the one
//     thing a supervised sidecar needs, a live child whose exit IS the crash
//     signal. Running PulseAudio in the foreground under our own *exec.Cmd
//     means cmd.Wait() is a reliable, race-free crash detector (exactly the
//     property BrowserCoordinator.watchForCrash relies on for Chrome).
//  2. Add `-n` and load module-native-protocol-unix explicitly (see the spawn
//     site). The default `/etc/pulse/default.pa` hardware-probe modules hang
//     for many seconds on a headless VM with no sound card, so the native
//     socket does not appear before startTimeout — the bring-up failure seen
//     on the CI worker. `-n` skips default.pa; we load only what we need.
//
// `--exit-idle-time=-1` and `--disable-shm=1` are kept as-is from the recipe.
//
// Non-blocking guarantee (FR-022 "MUST NEVER block the Chrome launch"): Start
// performs only fast, synchronous preflight (creating the socket directory)
// and then hands everything else — process spawn, waiting for the socket and
// cookie to appear, the PulseAudio native-protocol handshake, and the two
// LoadModule calls — to a background goroutine. Start returns before any of
// that runs. However slow or wedged PulseAudio bring-up is, it can never
// stall whatever goroutine called Start; only Healthy() (a mutex-guarded
// bool read, never blocking) observes readiness. See
// TestPulseSidecar_SpawnLoadModules_StableSocket for the direct proof (Start
// returns near-instantly while bring-up happens against a slow fake server).

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/elicify-ai/omnipus/pkg/audio/pulse"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

// process abstracts the running PulseAudio child so tests can substitute an
// in-process fake server without a real `pulseaudio` binary (hermetic Test
// 18/31 — see audiosink_linux_test.go). execProcess is the production
// implementation, backed by a real *exec.Cmd.
type process interface {
	// Wait blocks until the process exits and returns its exit error (nil on
	// a clean exit). Mirrors exec.Cmd.Wait.
	Wait() error
	// Terminate asks the process to stop (SIGTERM), escalating to SIGKILL if
	// it hasn't exited after a short grace period. Safe to call concurrently
	// with a blocked Wait — Wait will return once the process actually exits.
	Terminate() error
}

// execProcess wraps a real *exec.Cmd. exec.Cmd permits exactly one Wait call
// for a process's lifetime (a second concurrent/duplicate call is undefined
// behavior), so `done` — closed by Wait, the ONLY caller of cmd.Wait — lets
// Terminate observe "did it already exit" without ever calling cmd.Wait
// itself.
type execProcess struct {
	cmd  *exec.Cmd
	done chan struct{}
}

func newExecProcess(cmd *exec.Cmd) *execProcess {
	return &execProcess{cmd: cmd, done: make(chan struct{})}
}

func (e *execProcess) Wait() error {
	err := e.cmd.Wait()
	close(e.done)
	return err
}

func (e *execProcess) Terminate() error {
	if e.cmd.Process == nil {
		return nil
	}
	if err := e.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		// Process may already be gone (ESRCH) — fall back to Kill. Terminate
		// is best-effort teardown for a background sidecar (FR-022: audio
		// failures never propagate to Chrome/video); the supervise loop's
		// own Wait() call remains the authoritative exit signal, so a
		// failure here is logged, not silently discarded, but doesn't need
		// to be returned.
		if killErr := e.cmd.Process.Kill(); killErr != nil {
			logger.WarnCF(
				"browser",
				"audio sidecar: terminate: signal and kill both failed (process likely already exited)",
				map[string]any{
					"signal_error": err.Error(),
					"kill_error":   killErr.Error(),
				},
			)
		}
		return nil
	}
	select {
	case <-e.done:
		// Exited on its own (from the SIGTERM) before the grace period.
	case <-time.After(3 * time.Second):
		if killErr := e.cmd.Process.Kill(); killErr != nil {
			logger.WarnCF(
				"browser",
				"audio sidecar: terminate: graceful SIGTERM timed out and kill failed",
				map[string]any{
					"kill_error": killErr.Error(),
				},
			)
		}
	}
	return nil
}

// spawner starts the PulseAudio process (or, in tests, a fake standing in
// for one) and returns a handle for supervision. Overridable per-instance so
// tests never need a real `pulseaudio` binary on PATH.
type spawner func(cfg AudioConfig) (process, error)

func defaultSpawnPulseaudio(cfg AudioConfig) (process, error) {
	execPath := cfg.ExecPath
	if execPath == "" {
		resolved, err := exec.LookPath("pulseaudio")
		if err != nil {
			return nil, fmt.Errorf("pulseaudio not found on PATH: %w", err)
		}
		execPath = resolved
	}

	// -n skips /etc/pulse/default.pa. Its hardware-probe modules (module-udev-detect,
	// module-alsa-card, …) block for many seconds in a headless VM with no sound card
	// before module-native-protocol-unix ever runs, so the socket does not appear
	// before startTimeout and bring-up fails ("socket+cookie: context deadline
	// exceeded"). Load ONLY the native-protocol socket here, with EXPLICIT socket +
	// auth-cookie paths — under -n, PulseAudio does not consult PULSE_RUNTIME_PATH/
	// PULSE_COOKIE for these (it falls back to ~/.config/pulse), so they must be
	// passed as module args. null-sink + remap-source are loaded post-connect in
	// bringUp over the native protocol; the socket+cookie appearing is all that gates
	// startup. (Verified on a headless Debian 12 VM: both files appear in <1s.)
	cmd := exec.Command(
		execPath,
		"-n",
		"--exit-idle-time=-1",
		"--disable-shm=1",
		"--load=module-native-protocol-unix socket="+socketPathFor(cfg)+" auth-cookie="+cookiePathFor(cfg),
	)
	cmd.Env = append(
		os.Environ(),
		"PULSE_RUNTIME_PATH="+cfg.SocketDir,
		"XDG_RUNTIME_DIR="+cfg.SocketDir,
		"PULSE_COOKIE="+cookiePathFor(cfg),
	)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start pulseaudio (%s): %w", execPath, err)
	}
	return newExecProcess(cmd), nil
}

func socketPathFor(cfg AudioConfig) string { return filepath.Join(cfg.SocketDir, "native") }
func cookiePathFor(cfg AudioConfig) string { return filepath.Join(cfg.SocketDir, "cookie") }

func errString(err error) string {
	if err == nil {
		return "exit status 0"
	}
	return err.Error()
}

// pulseSidecar is the Linux AudioSidecar implementation.
type pulseSidecar struct {
	cfg     AudioConfig
	spawnFn spawner

	mu      sync.Mutex
	healthy bool
	stopped bool
	proc    process

	stopCh chan struct{}
	wg     sync.WaitGroup
}

// NewAudioSidecar constructs the Linux PulseAudio sidecar. Call Start to
// begin supervision.
func NewAudioSidecar(cfg AudioConfig) AudioSidecar {
	return &pulseSidecar{
		cfg:     cfg,
		spawnFn: defaultSpawnPulseaudio,
		stopCh:  make(chan struct{}),
	}
}

func (p *pulseSidecar) PulseServer() string { return "unix:" + socketPathFor(p.cfg) }

func (p *pulseSidecar) Healthy() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.healthy
}

// Start creates the stable socket directory (fast, synchronous — a failure
// here is a real configuration problem worth surfacing immediately) and then
// launches the spawn/bring-up/supervise loop in the background. See the file
// doc for the non-blocking guarantee this embodies.
func (p *pulseSidecar) Start(_ context.Context) error {
	p.mu.Lock()
	if p.stopped {
		p.mu.Unlock()
		return fmt.Errorf("browser: audio sidecar: Start called after Stop")
	}
	p.mu.Unlock()

	if err := os.MkdirAll(p.cfg.SocketDir, 0o700); err != nil {
		return fmt.Errorf("browser: audio sidecar: create socket dir %s: %w", p.cfg.SocketDir, err)
	}

	p.wg.Add(1)
	go p.runAndSupervise()
	return nil
}

// Stop tears down the sidecar. Idempotent; safe to call even if Start failed
// or was never called.
func (p *pulseSidecar) Stop() {
	p.mu.Lock()
	if p.stopped {
		p.mu.Unlock()
		return
	}
	p.stopped = true
	proc := p.proc
	p.mu.Unlock()

	close(p.stopCh)
	if proc != nil {
		if err := proc.Terminate(); err != nil {
			logger.WarnCF("browser", "audio sidecar: stop: terminate reported an error", map[string]any{
				"error": err.Error(),
			})
		}
	}
	p.wg.Wait()

	p.mu.Lock()
	p.healthy = false
	p.mu.Unlock()
}

// runAndSupervise is the single loop covering first start, bring-up, and
// every subsequent crash-restart (Test 31: a restart reuses the exact same
// SocketDir/socket/cookie paths — nothing here ever changes them). It always
// runs off the goroutine that called Start.
func (p *pulseSidecar) runAndSupervise() {
	defer p.wg.Done()

	backoff := p.cfg.RestartBackoff
	if backoff <= 0 {
		backoff = 500 * time.Millisecond
	}

	// FR-019 "PulseAudio sidecar restart count" (Test 28, O-4): only a
	// bring-up that follows an EARLIER successful bring-up is a "restart" —
	// the very first successful bring-up in this sidecar's lifetime is not.
	everHealthy := false

	// consecutiveFailures counts spawn/bring-up failures since the last
	// successful bring-up (SF5). It resets to 0 on every success, so it only
	// bounds an unbroken run of failures — not the sidecar's total lifetime
	// restart count.
	consecutiveFailures := 0

	for {
		if p.isStopped() {
			return
		}

		proc, err := p.spawnFn(p.cfg)
		if err != nil {
			consecutiveFailures++
			logger.WarnCF(
				"browser",
				"audio sidecar: spawn failed — audio unavailable, video unaffected",
				map[string]any{
					"error":               err.Error(),
					"consecutive_failure": consecutiveFailures,
				},
			)
			if consecutiveFailures >= pulseMaxRestartAttempts {
				logger.ErrorCF(
					"browser",
					"audio sidecar: exceeded max consecutive spawn/bring-up failures — giving up; audio stays unavailable, video unaffected",
					map[string]any{"max_attempts": pulseMaxRestartAttempts},
				)
				return
			}
			if !p.sleepOrStop(backoff) {
				return
			}
			backoff = nextBackoff(backoff, p.cfg)
			continue
		}

		bringCtx, cancel := context.WithTimeout(context.Background(), p.startTimeout())
		bringErr := p.bringUp(bringCtx, proc)
		cancel()
		if bringErr != nil {
			consecutiveFailures++
			logger.WarnCF(
				"browser",
				"audio sidecar: bring-up failed — audio unavailable, video unaffected",
				map[string]any{
					"error":               bringErr.Error(),
					"consecutive_failure": consecutiveFailures,
				},
			)
			if termErr := proc.Terminate(); termErr != nil {
				logger.WarnCF("browser", "audio sidecar: bring-up cleanup: terminate reported an error", map[string]any{
					"error": termErr.Error(),
				})
			}
			if consecutiveFailures >= pulseMaxRestartAttempts {
				logger.ErrorCF(
					"browser",
					"audio sidecar: exceeded max consecutive spawn/bring-up failures — giving up; audio stays unavailable, video unaffected",
					map[string]any{"max_attempts": pulseMaxRestartAttempts},
				)
				return
			}
			if !p.sleepOrStop(backoff) {
				return
			}
			backoff = nextBackoff(backoff, p.cfg)
			continue
		}

		consecutiveFailures = 0

		p.mu.Lock()
		p.proc = proc
		p.healthy = true
		p.mu.Unlock()
		logger.InfoCF("browser", "audio sidecar: pulseaudio ready", map[string]any{
			"socket": socketPathFor(p.cfg),
			"sink":   p.cfg.SinkName,
			"source": p.cfg.SourceName,
		})
		if everHealthy {
			activeBrowserMetricsRecorder.IncPulseSidecarRestart()
		}
		everHealthy = true
		backoff = p.cfg.RestartBackoff
		if backoff <= 0 {
			backoff = 500 * time.Millisecond
		}

		waitErr := proc.Wait()

		p.mu.Lock()
		stopped := p.stopped
		p.healthy = false
		p.proc = nil
		p.mu.Unlock()
		if stopped {
			return
		}
		logger.WarnCF(
			"browser",
			"audio sidecar: pulseaudio exited unexpectedly — restarting on the same socket path",
			map[string]any{
				"error":  errString(waitErr),
				"socket": socketPathFor(p.cfg),
			},
		)
	}
}

func (p *pulseSidecar) startTimeout() time.Duration {
	if p.cfg.StartTimeout <= 0 {
		return 10 * time.Second
	}
	return p.cfg.StartTimeout
}

func (p *pulseSidecar) isStopped() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stopped
}

// sleepOrStop waits d, returning false early (and immediately) if Stop is
// called in the meantime.
func (p *pulseSidecar) sleepOrStop(d time.Duration) bool {
	select {
	case <-p.stopCh:
		return false
	case <-time.After(d):
		return true
	}
}

// pulseMaxRestartAttempts bounds a run of CONSECUTIVE spawn/bring-up
// failures — mirroring xvfbMaxRestartAttempts (display_linux.go) — so a host
// with no `pulseaudio` on PATH (or one that never manages to bring up) does
// not retry forever, WARNing every ≤15s indefinitely (SF5). The counter
// resets to zero after every successful bring-up, so a sidecar that has run
// successfully at least once still gets a fresh run of retries after a later
// crash — only a PERSISTENTLY absent/broken binary gives up for good, never
// restarting again for the lifetime of this sidecar instance.
var pulseMaxRestartAttempts = 5

func nextBackoff(cur time.Duration, cfg AudioConfig) time.Duration {
	maxBackoff := cfg.MaxRestartBackoff
	if maxBackoff <= 0 {
		maxBackoff = 15 * time.Second
	}
	next := cur * 2
	if next > maxBackoff || next <= 0 {
		next = maxBackoff
	}
	return next
}

// bringUp waits for the socket + cookie to appear, connects the pure-Go
// native-protocol client (pkg/audio/pulse), and loads module-null-sink +
// module-remap-source. Chrome hides raw ".monitor" sources, so the remap is
// required, not cosmetic (ADR-044-spike-results.md §S-3). Bounded entirely
// by ctx.
func (p *pulseSidecar) bringUp(ctx context.Context, proc process) error {
	if err := waitForFiles(ctx, socketPathFor(p.cfg), cookiePathFor(p.cfg)); err != nil {
		return fmt.Errorf("waiting for pulseaudio socket+cookie: %w", err)
	}

	client, err := pulse.Dial(ctx, socketPathFor(p.cfg), cookiePathFor(p.cfg))
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer client.Close()

	sinkName := p.cfg.SinkName
	if sinkName == "" {
		sinkName = "vspk"
	}
	sourceName := p.cfg.SourceName
	if sourceName == "" {
		sourceName = "vmic"
	}

	if _, err := client.LoadModule(ctx, "module-null-sink", "sink_name="+sinkName); err != nil {
		return fmt.Errorf("load module-null-sink: %w", err)
	}
	remapArgs := fmt.Sprintf("master=%s.monitor source_name=%s", sinkName, sourceName)
	if _, err := client.LoadModule(ctx, "module-remap-source", remapArgs); err != nil {
		return fmt.Errorf("load module-remap-source: %w", err)
	}
	return nil
}

// waitForFiles polls until both paths exist or ctx is done. PulseAudio
// creates its socket and (if PULSE_COOKIE didn't already point at an
// existing file) its cookie asynchronously after -D-less startup, so a fixed
// sleep would either race or waste time; poll instead.
func waitForFiles(ctx context.Context, paths ...string) error {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		allExist := true
		for _, p := range paths {
			if _, err := os.Stat(p); err != nil {
				allExist = false
				break
			}
		}
		if allExist {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

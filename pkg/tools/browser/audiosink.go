package browser

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// AudioConfig configures the PulseAudio sidecar (FR-022, US-11). The
// socket/cookie/state paths are STABLE across restarts (spec US-11/AC-3,
// Test 31) — a daemon crash-restart reuses the exact same paths so
// PulseServer() never changes and Chrome can (or, failing re-enumeration,
// the next Chrome launch will — see Test 31) resume audio without any other
// part of the system needing to notice the restart happened.
type AudioConfig struct {
	// SocketDir is the directory holding the PulseAudio native-protocol Unix
	// socket ("native"), auth cookie ("cookie"), and PID file ("pid").
	// Stable across restarts; defaults under $OMNIPUS_HOME (DefaultAudioConfig).
	SocketDir string
	// SinkName is the module-null-sink name (default "vspk").
	SinkName string
	// SourceName is the module-remap-source name Chrome actually enumerates
	// (default "vmic"). Chrome hides raw ".monitor" sources, so remapping the
	// null sink's monitor to a plain source is REQUIRED, not cosmetic
	// (ADR-044-spike-results.md §S-3).
	SourceName string
	// ExecPath overrides the `pulseaudio` binary; empty resolves via PATH.
	ExecPath string
	// StartTimeout bounds one spawn+bring-up attempt (socket/cookie
	// appearing, auth, and the two LoadModule calls). It does NOT bound
	// Start() itself — Start always returns immediately; see the linux
	// implementation's file doc for the non-blocking guarantee this exists
	// to support.
	StartTimeout time.Duration
	// RestartBackoff / MaxRestartBackoff bound the crash-restart retry loop
	// (exponential backoff between RestartBackoff and MaxRestartBackoff).
	RestartBackoff    time.Duration
	MaxRestartBackoff time.Duration
}

// AudioSidecar supervises the PulseAudio process backing managed-Chrome
// audio capture. It is a pure best-effort companion to video (FR-022,
// FR-023): every failure mode degrades to "audio absent", never blocks or
// fails the Chrome launch, and never affects video.
type AudioSidecar interface {
	// Start begins spawning and supervising the sidecar. On Linux it returns
	// once fast, synchronous preflight (socket-dir creation, exec resolution
	// is deferred to the background loop) succeeds — the slow parts (process
	// spawn, PulseAudio bring-up, ongoing crash-restart supervision) run in
	// the background and are observed via Healthy(), never by blocking the
	// caller. See audiosink_linux.go for the full contract.
	Start(ctx context.Context) error
	// PulseServer returns the stable "unix:<path>" PULSE_SERVER value to wire
	// into the managed-Chrome launch environment. It is a pure function of
	// configuration — it never changes across restarts and is valid to read
	// before Start is even called (the value Chrome gets is the same whether
	// or not PulseAudio ends up healthy; an absent/dead socket there simply
	// means Chrome finds no audio device, degrading silently to no audio).
	PulseServer() string
	// Healthy reports whether the sidecar currently believes PulseAudio is up
	// and has the required modules loaded. Never blocks.
	Healthy() bool
	// Stop tears down the sidecar and stops the supervision loop. Idempotent.
	Stop()
}

// DefaultAudioConfig returns an AudioConfig with spec-defined defaults,
// mirroring DefaultConfig's $OMNIPUS_HOME resolution (manager.go) so the
// sidecar's state lives alongside the rest of the browser subsystem's data.
func DefaultAudioConfig() (AudioConfig, error) {
	base := os.Getenv("OMNIPUS_HOME")
	if base == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return AudioConfig{}, fmt.Errorf("browser: audio sidecar: cannot determine home directory: %w", err)
		}
		base = filepath.Join(homeDir, ".omnipus")
	}
	return AudioConfig{
		SocketDir:         filepath.Join(base, "browser", "pulse"),
		SinkName:          "vspk",
		SourceName:        "vmic",
		StartTimeout:      10 * time.Second,
		RestartBackoff:    500 * time.Millisecond,
		MaxRestartBackoff: 15 * time.Second,
	}, nil
}

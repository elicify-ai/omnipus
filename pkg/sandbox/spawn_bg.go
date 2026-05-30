// Package sandbox — SpawnBackgroundChild: shared helper for long-running
// background processes (dev servers) that must run under sandbox hardening.
//
// Used by web_serve (dev mode) and workspace.shell_bg to spawn child
// processes through the sandbox's hardened-exec path. The two callers share
// the same spawn+harden sequence so kernel restrictions (Landlock, seccomp,
// no-new-privs) apply consistently regardless of which tool started the
// child.
//
// Callers are responsible for:
//   - Waiting on the returned *exec.Cmd (via a goroutine) so the zombie
//     clears when the child exits naturally.
//   - Killing / sending SIGTERM via the OS-level Process handle when the
//     child must be stopped (e.g., on token expiry, agent deletion).
//
// Security contract:
//   - When limits is the zero value (profile=off / god mode), sandbox
//     hardening is skipped and the child runs without Setpgid / Pdeathsig /
//     prlimit. Callers must check IsGodMode before calling SpawnBackgroundChild
//     if they wish to audit the bypass path — this function does not emit
//     audit entries; that is the caller's responsibility.
//   - When limits is non-zero, ApplyChildHardening + ApplyChildPostStartHardening
//     are applied in order. If post-start hardening fails, the child is killed
//     and an error is returned.
//   - env merging follows the same rules as sandbox.Run: proxy vars and
//     npm_config_cache are appended last and win over caller-supplied entries.
//   - An explicit PORT=<port> var is appended after mergeEnv so it always
//     takes precedence over any PORT in the supplied env slice.

package sandbox

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// isProcessDoneError reports whether err indicates the process has already
// exited. Both os.ErrProcessDone and syscall.ESRCH are "already gone" signals
// that must not be treated as a security event; every other error is novel and
// requires operator attention.
func isProcessDoneError(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH)
}

// SpawnBackgroundChild starts a long-running process under the agent's
// sandbox Limits. Returns the started *exec.Cmd. The caller is responsible
// for waiting/killing.
//
// Performs:
//   - exec.Command(parts[0], parts[1:]...)
//   - cmd.Dir = workspaceDir (when non-empty)
//   - cmd.Env = mergeEnv(env, limits) then appends PORT=<port> when port > 0
//   - ApplyChildHardening pre-start  (skipped when limits is zero-value)
//   - cmd.Start()
//   - ApplyChildPostStartHardening; on failure, kills the child and returns err
//
// The context is NOT passed to exec.CommandContext — exec.Command is used
// intentionally so that cancellation of the launch context does not SIGKILL
// the long-lived dev server. The server is managed by the DevServerRegistry
// and stopped via SIGTERM on token expiry.
//
// Note: `parts` must be pre-split (by strings.Fields or equivalent). An
// empty slice returns an error immediately without starting any process.
func SpawnBackgroundChild(
	parts []string,
	workspaceDir string,
	env []string,
	port int32,
	limits Limits,
) (*exec.Cmd, error) {
	if len(parts) == 0 {
		return nil, fmt.Errorf("SpawnBackgroundChild: empty command parts")
	}

	cmd := exec.Command(parts[0], parts[1:]...) //nolint:gosec // caller validates command
	if workspaceDir != "" {
		cmd.Dir = workspaceDir
	}

	// Merge proxy + npm-cache vars via the shared mergeEnv helper.
	// mergeEnv appends injected vars after env so they take precedence.
	merged := mergeEnv(env, limits)

	// PORT must override everything, including any PORT the caller put in env.
	// Append last so it wins under POSIX first-seen-wins lookup.
	if port > 0 {
		merged = append(merged, fmt.Sprintf("PORT=%d", port))
	}

	// HOST=127.0.0.1 forces dev servers that default to 0.0.0.0 to bind
	// loopback. The reverse proxy is the only external-access path.
	merged = append(merged, "HOST=127.0.0.1")

	cmd.Env = merged

	// Stdio: bind stdin to an empty reader, and route stdout/stderr to a
	// log file inside the workspace so operators can debug a silently-dying
	// dev server. os/exec opens /dev/null for nil streams, which is blocked
	// by the gateway's Landlock policy (no /dev access), so leaving them nil
	// is not an option. Falls back to io.Discard when the workspace is
	// unwritable (god-mode without a workspaceDir).
	//
	// B1.4-e: Open with O_APPEND only (no O_TRUNC) so that successive spawns
	// accumulate log output rather than silently discarding the previous run's
	// lines. This matches standard daemon log behavior and is critical for
	// post-mortem debugging when a dev server exits and is restarted.
	//
	// The parent-side file handle is stored in logFile so it can be closed
	// after cmd.Start() returns. The kernel duplicates the fd into the child
	// at fork time; the parent no longer needs its copy and holding it open
	// would leak an fd for the lifetime of the gateway process (B1.4-e).
	if cmd.Stdin == nil {
		cmd.Stdin = bytes.NewReader(nil)
	}
	var logFile *os.File // closed after Start; child retains its own fd
	if cmd.Stdout == nil || cmd.Stderr == nil {
		var logSink io.Writer = io.Discard
		if workspaceDir != "" {
			logPath := filepath.Join(workspaceDir, ".dev-server.log")
			if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
				logSink = f
				logFile = f
			} else {
				slog.Warn("spawn_bg: dev-server log file could not be opened — output will be discarded",
					"path", logPath, "error", err)
			}
		}
		if cmd.Stdout == nil {
			cmd.Stdout = logSink
		}
		if cmd.Stderr == nil {
			cmd.Stderr = logSink
		}
	}

	// IsGodMode (profile=off): skip ApplyChildHardening entirely.
	isZero := (limits == Limits{})
	if !isZero {
		if err := ApplyChildHardening(cmd, limits); err != nil {
			return nil, fmt.Errorf("SpawnBackgroundChild: apply hardening: %w", err)
		}
		// Background dev servers must outlive the goroutine that forked
		// them. PR_SET_PDEATHSIG fires when the parent OS *thread* exits
		// (not the process), and Go's runtime can retire OS threads at any
		// time — that would kill the dev server seconds after spawn. The
		// DevServerRegistry handles lifecycle via token expiry + explicit
		// SIGTERM, so we clear Pdeathsig here. Setpgid stays so the registry
		// can SIGTERM the whole process group on shutdown.
		clearPdeathsigForBackground(cmd)
	}

	// Thread-locked spawn: re-applies Landlock to the launching OS thread
	// so the child inherits the kernel sandbox. See StartLocked for the
	// full rationale. clearPdeathsigForBackground above ensures the dev
	// server survives the launching thread's death.
	if err := StartLocked(cmd); err != nil {
		if logFile != nil {
			_ = logFile.Close()
		}
		return nil, fmt.Errorf("SpawnBackgroundChild: start %s: %w", strings.Join(parts, " "), err)
	}

	// B1.4-e: close the parent-side log fd now that the child has been forked
	// and holds its own copy. The kernel duplicated the fd at fork time; the
	// parent's copy is redundant and leaks an open fd until the gateway exits.
	if logFile != nil {
		if err := logFile.Close(); err != nil {
			slog.Warn("SpawnBackgroundChild: failed to close parent log fd after fork",
				"path", filepath.Join(workspaceDir, ".dev-server.log"),
				"error", err)
		}
	}

	// Post-start hardening (Windows Job Object assignment, prlimit on Linux).
	if !isZero {
		if hardeningErr := ApplyChildPostStartHardening(cmd, limits); hardeningErr != nil {
			// H1-BK: capture the kill error so a loose child is never silently
			// swallowed. A partially-sandboxed child that we cannot kill is a
			// security event — log at Error so operators can investigate.
			killErr := cmd.Process.Kill()
			if killErr != nil && !isProcessDoneError(killErr) {
				slog.Error(
					"SpawnBackgroundChild: post-start hardening failed AND child kill failed; child PID may still be running",
					"pid",
					cmd.Process.Pid,
					"kill_err",
					killErr,
					"hardening_err",
					hardeningErr,
				)
				return nil, fmt.Errorf(
					"SpawnBackgroundChild: post-start hardening: %w; kill also failed: %v",
					hardeningErr,
					killErr,
				)
			}
			return nil, fmt.Errorf("SpawnBackgroundChild: post-start hardening: %w", hardeningErr)
		}
	}

	return cmd, nil
}

//go:build darwin

// Darwin platform hardening for hardened_exec children. We document this
// platform as best-effort: there is no kernel isolation
// primitive equivalent to Landlock or AppContainer that we can use without
// CGo. The child runs under the OS's normal user permissions; the only
// active controls are Setpgid (so the parent can SIGTERM the whole tree
// on shutdown) and context-cancellation timeout via exec.CommandContext.
//
// MemoryLimitBytes is NOT enforced on darwin because:
// - RLIMIT_AS is unimplemented in XNU.
// - RLIMIT_DATA only covers initialised + heap data, not mmap regions
// (which is where Node/V8 lives), so it doesn't actually cap memory.
// - prlimit does not exist; setrlimit only affects the calling
// process, so we cannot set a child's limit post-fork without
// reimplementing fork+exec via CGo (forbidden by CLAUDE.md).
//
// The Tier 2 build_static tool documents this limitation in its description
// (cross-platform Tier 2 is best-effort on darwin). Linux remains the
// primary deployment target.

package sandbox

import (
	"fmt"
	"log/slog"
	"os/exec"
	"syscall"
)

// memoryLimitSupported reports whether this platform can enforce
// Limits.MemoryLimitBytes via the post-start hardener. Darwin: no
// (RLIMIT_AS unsupported on XNU; see file header). Used by Run to populate
// Result.MemoryLimitUnsupported (HIGH-1, silent-failure-hunter) so callers
// can surface the warning to operators in the tool result.
const memoryLimitSupported = false

// applyPlatformHardening configures the child's SysProcAttr and, when a
// Seatbelt policy is installed, wraps the child so it starts inside that
// profile.
//
// This is darwin's analogue of Linux's restrictCurrentThreadIfNeeded: it is
// the single seam where a hardened-exec child acquires kernel-level
// confinement. On Linux the spawning THREAD enters the Landlock domain and the
// child inherits it; on macOS the child must be launched under sandbox-exec
// instead, because Seatbelt cannot confine an already-running process without
// CGo (hard constraint #2). See backend_darwin_seatbelt.go's header.
//
// Setpgid is retained regardless: darwin does not expose Pdeathsig, so the
// process group is how the parent SIGTERMs the whole tree on shutdown.
//
// Failure to apply an installed policy returns an error, which aborts the
// spawn. Falling through to an UNCONFINED child would silently downgrade the
// security boundary — the same fail-closed contract Linux uses when
// landlock_restrict_self fails.
func applyPlatformHardening(cmd *exec.Cmd, _ Limits) error {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true

	// No policy installed (e.g. sandbox mode off, or the fallback backend was
	// selected) means no wrapping — children run exactly as they did before
	// this backend existed.
	sb := CurrentSeatbeltBackend()
	if sb == nil {
		return nil
	}

	// Pass an empty policy so ApplyToCmd reuses the profile Apply already
	// rendered and validated, rather than re-deriving it per spawn.
	if err := sb.ApplyToCmd(cmd, SandboxPolicy{}); err != nil {
		return fmt.Errorf("hardened_exec/darwin: apply seatbelt profile to child: %w", err)
	}
	return nil
}

// applyPostStartHardening logs a WARN note when the caller requested a
// memory cap, then returns nil. The contract from hardened_exec.go is that
// applyPostStartHardening returning a non-nil error kills the child — we
// must NOT do that for an unsupported feature, so we degrade gracefully.
//
// HIGH-1 (silent-failure-hunter): the log level is Warn (not Info) so the
// degradation surfaces at default operator log thresholds. Run also flags
// this in Result.MemoryLimitUnsupported so tools can include the warning in
// their result preamble (the LLM/agent sees the limitation explicitly,
// rather than only operators tailing logs).
func applyPostStartHardening(_ *exec.Cmd, lim Limits) error {
	if lim.MemoryLimitBytes > 0 {
		slog.Warn(
			"hardened_exec/darwin: MemoryLimitBytes ignored (RLIMIT_AS unsupported on macOS); child runs unbounded",
			"requested_bytes",
			lim.MemoryLimitBytes,
		)
	}
	return nil
}

//go:build darwin

// Darwin platform hardening for hardened_exec children. Per we
// document this platform as best-effort: there is no kernel isolation
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

// applyPlatformHardening configures the child's SysProcAttr. Darwin does
// not expose Pdeathsig at the syscall layer; the closest analogue is
// Setpgid so the parent can SIGTERM the whole tree on shutdown.
//
// Returns nil so build_static / web_serve dev mode can run on darwin (Tier 2
// is cross-platform, Tier 3 is gated separately at the tool layer).
func applyPlatformHardening(cmd *exec.Cmd, _ Limits) error {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
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

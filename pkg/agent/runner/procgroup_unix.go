//go:build !windows

// procgroup_unix.go — ADR-057 U22 (W9c, FR-029): a 3P external-CLI child's
// process GROUP must die with the child.
//
// THE GAP THIS CLOSES. All three drivers (driver_claude.go, driver_codex.go,
// driver_opencode.go) spawn their child via
// `exec.CommandContext(runCtx, binary, args...)` and rely on the driver's own
// Cancel() (which cancels runCtx) to end the run. Go's os/exec default
// behavior on context cancellation is to signal ONLY the direct child PID
// (cmd.Process.Kill()) — verified: `rg 'SysProcAttr|Setpgid|cmd\.Process\.Kill|
// Signal\(' pkg/agent/runner/` returned zero matches before this file. A
// delegated CLI that itself shells out (a lint run, a dev server, a build
// tool it launches as a subprocess) leaves that subprocess tree running,
// undetected and unbilled, the moment the parent CLI process is killed —
// exactly the silent-orphan mechanism this migration exists to close, on the
// process side rather than the file side.
//
// THE FIX mirrors two in-house precedents named by the spec (FR-029):
//   - pkg/tools/shell_process_unix.go:14 (owned by U16) — the same
//     `SysProcAttr{Setpgid: true}` baseline for this repo's OTHER
//     long-running child-process path (background shells).
//   - pkg/sandbox/hardened_exec_cancel_unix.go — the group-kill-on-cancel
//     shape (SIGTERM the group, brief grace, then SIGKILL the group,
//     ESRCH-tolerant, with a direct-PID fallback). u22InstallGroupCancel
//     below is the same shape, adapted to this package's cmd.Cancel wiring
//     (each driver already relies on context cancellation via
//     exec.CommandContext, so overriding cmd.Cancel is the correct
//     integration point here too).
//
// FR-029 is explicitly POSIX-only (Setpgid has no Windows equivalent in this
// codebase) — see procgroup_windows.go for the documented no-op half.

package runner

import (
	"os/exec"
	"syscall"
	"time"
)

// u22ProcGroupKillGrace is how long a 3P child's process group is given to
// react to SIGTERM before being force-killed with SIGKILL. Mirrors
// sandbox.cancelGraceDuration's rationale (pkg/sandbox/hardened_exec_cancel_unix.go):
// short, because this only fires on cancellation/timeout — the run is already
// ending and the caller is waiting on Wait() to return.
const u22ProcGroupKillGrace = 1 * time.Second

// u22SetupProcessGroup puts cmd in its own process group (Setpgid) so its
// entire subprocess tree can later be signaled as a single unit. MUST be
// called before cmd.Start() — Setpgid is applied by the kernel at fork time.
func u22SetupProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// u22InstallGroupCancel overrides cmd.Cancel so that context
// cancellation/timeout (the mechanism all three drivers already use to end a
// run — see each Run's runCtx/cancelFn and Cancel()) signals the child's
// whole process GROUP (negative pid), not just the direct child PID
// (FR-029). SIGTERM first so a well-behaved subtree can flush and exit
// cleanly, then SIGKILL after a short grace window for stragglers — the
// SIGKILL is unconditional so a hung grandchild can never block the run
// past its cancellation.
//
// MUST be called after u22SetupProcessGroup and before cmd.Start(); the
// closure reads cmd.Process lazily (only once ctx is actually canceled), so
// it is safe to install before Start assigns cmd.Process.
func u22InstallGroupCancel(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		pid := cmd.Process.Pid
		if pid <= 0 {
			return nil
		}
		// SIGTERM the whole group first (negative pid == process group).
		// u22SetupProcessGroup made the child its own group leader, so
		// pgid == pid. ESRCH means the group is already gone — not an error.
		if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil && err != syscall.ESRCH {
			// Fall back to signaling just the direct child if the group
			// signal failed for a reason other than "already gone".
			_ = cmd.Process.Signal(syscall.SIGTERM)
		}

		time.Sleep(u22ProcGroupKillGrace)

		if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil && err != syscall.ESRCH {
			// Last resort: kill the direct child via the safe OS handle.
			return cmd.Process.Kill()
		}
		return nil
	}
}

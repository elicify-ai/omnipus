//go:build linux

// Linux platform hardening for hardened_exec children. Per
// + : children inherit the gateway's existing Landlock +
// seccomp profiles unchanged (no narrowing in v4); we add Setpgid +
// Pdeathsig=SIGTERM for clean shutdown and prlimit RLIMIT_AS for memory
// caps.

package sandbox

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

// procReadFallbackWarnOnce ensures the /proc-unreadable degradation is
// logged exactly once per process lifetime. Without the gate, an operator
// running a stripped container or a kernel with /proc masked would see
// the same WARN on every spawn — so the signal would be drowned out and
// likely filtered. Once-per-boot keeps it visible without spamming.
var procReadFallbackWarnOnce sync.Once

// applyPlatformHardening configures the child's SysProcAttr and applies
// pre-start prlimit when supported. RLIMIT_AS is set via SysProcAttr.Rlimits
// when the runtime supports it; otherwise we fall back to a post-start
// prlimit on the child PID (small race window, but the limit takes effect
// before any nontrivial allocation in practice).
func applyPlatformHardening(cmd *exec.Cmd, lim Limits) error {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	// Setpgid: put the child in a new process group so we can signal the
	// whole subtree (npm spawns children) on timeout.
	cmd.SysProcAttr.Setpgid = true
	// Pdeathsig: kernel sends SIGTERM to the child if the gateway dies.
	// Defends against orphaned npm/node processes when the gateway crashes.
	cmd.SysProcAttr.Pdeathsig = syscall.SIGTERM
	return nil
}

// memoryLimitSupported reports whether this platform can enforce
// Limits.MemoryLimitBytes via the post-start hardener. Linux: yes
// (RLIMIT_AS via prlimit). Used by Run to populate
// Result.MemoryLimitUnsupported (HIGH-1, silent-failure-hunter).
const memoryLimitSupported = true

// childNProcSlack caps the number of NEW user-level processes a hardened-exec
// subtree can spawn beyond the current per-UID baseline (RLIMIT_NPROC, v0.2
// #155 item 5). The cap is inherited by every fork() the child performs, so
// a fork-bomb that slips past the shell-guard regex (e.g. via `sh fork.sh`
// indirection) hits the kernel limit before saturating the host.
//
// Sizing rationale: 128 is generous enough for a realistic build pipeline
// (npm install commonly spawns 8-16 concurrent worker subprocesses; nx /
// turborepo can spawn slightly more; CI runners doing `go test -p N` can
// spike the per-UID count by dozens between baseline snapshot and child
// fork) but tight enough that an exponential fork-bomb still saturates
// within microseconds — 2^7 = 128, so a doubling fork-bomb hits the cap
// after seven cycles, taking under a millisecond.
//
// The original value of 32 was chosen for a quiet single-user host but
// mis-fired on ubuntu-24.04-arm CI runners where parallel test packages
// pushed the per-UID nproc above baseline+32 between snapshot and child
// exec, producing `sh: Cannot fork` even for innocuous one-shot commands.
// 128 closes that race without materially weakening the bomb defense.
//
// Why relative, not absolute: RLIMIT_NPROC is per-UID, not per-process tree.
// On a multi-user host the gateway's UID may already own dozens or hundreds
// of legitimate processes (tmux, IDE servers, other gateways). An absolute
// cap of N would refuse every spawn whenever currentNProc > N, breaking
// production. Setting cap = baseline + slack contains the BLAST RADIUS
// without falsely throttling normal operation. The value is hard-coded
// rather than configurable because operator-supplied values defeat the
// protection.
const childNProcSlack uint64 = 128

// readCurrentUserNProc returns the number of TASKS (threads) currently owned
// by the gateway's UID, for use as the RLIMIT_NPROC baseline. On read failure
// it returns 0 — the caller falls back to a conservative absolute cap.
//
// Why tasks and not processes: the kernel enforces RLIMIT_NPROC in
// copy_process() against the per-UID task_struct counter, which is
// incremented for EVERY task — including every thread created with
// CLONE_THREAD. That is the same quantity `ulimit -u` reports. Counting only
// the top-level /proc/<pid> entries (which are thread-group leaders)
// undercounts by the entire thread multiplier: a Go gateway with the runtime
// scheduler, chromedp and ~14 channels routinely holds 250+ threads across
// fewer than 100 processes.
//
// Getting this wrong is not a tuning issue, it is a total outage of the
// `bash` tool: if baseline+slack lands below the live task count, the cap is
// already exceeded at the moment it is applied and EVERY fork() by the child
// returns EAGAIN ("sh: Cannot fork") regardless of free memory or load. That
// regression shipped once — see TestReadCurrentUserNProc_CountsThreadsNotProcesses.
//
// Implementation: scans /proc, and for each PID owned by us sums the entries
// in /proc/<pid>/task. Linux only; called on the hot path of every
// hardened-exec spawn so kept allocation-light. Tolerant of races: a process
// or thread exiting mid-walk is skipped (counted as 1 for the leader).
func readCurrentUserNProc() uint64 {
	uid := uint64(os.Getuid()) // #nosec G115 -- os.Getuid() on Linux wraps getuid(2), which returns the kernel's uid_t (32-bit unsigned) and never fails or returns negative; converting a value bounded to [0, 4294967295] into uint64 cannot overflow.
	dir, err := os.Open("/proc")
	if err != nil {
		return 0
	}
	defer dir.Close()

	var count uint64
	for {
		names, err := dir.Readdirnames(256)
		if len(names) == 0 && err != nil {
			break
		}
		for _, name := range names {
			if _, atoiErr := strconv.Atoi(name); atoiErr != nil {
				continue
			}
			var st unix.Stat_t
			if statErr := unix.Stat("/proc/"+name, &st); statErr != nil {
				continue
			}
			if uint64(st.Uid) != uid {
				continue
			}
			// Sum this process's threads. A read error means it exited
			// between the stat and the readdir — count the leader only.
			taskEntries, taskErr := os.ReadDir("/proc/" + name + "/task")
			if taskErr != nil {
				count++
				continue
			}
			count += uint64(len(taskEntries))
		}
		if err != nil {
			break
		}
	}
	return count
}

// applyPostStartHardening installs RLIMIT_AS and RLIMIT_NPROC via prlimit
// on the child PID. We do this AFTER Start (rather than via
// SysProcAttr.Rlimits) because the SysProcAttr.Rlimits field is not
// available in all Go toolchain versions we target; prlimit is a stable
// Linux 2.6+ syscall.
//
// A small window exists between Start and Prlimit during which the child
// has no caps. In practice this is a few hundred microseconds — before any
// user code in npm/node has executed. The exec.Cmd contract gives us no
// earlier hook (PreExec is unsafe), so this is the best available without
// re-implementing fork+exec.
//
// RLIMIT_NPROC is set unconditionally (v0.2 #155 item 5). RLIMIT_AS is
// gated on a non-zero Limits.MemoryLimitBytes per the existing contract.
func applyPostStartHardening(cmd *exec.Cmd, lim Limits) error {
	if cmd.Process == nil {
		return nil
	}

	// RLIMIT_NPROC — fork-bomb defense. Applied unconditionally so even
	// callers that don't bother to set MemoryLimitBytes still get fork-
	// bomb containment. The cap is per-UID and inherited by every fork()
	// the child performs. Compute as baseline + slack so existing user
	// processes (tmux, IDE, sibling gateways) don't immediately trip the
	// limit.
	baseline := readCurrentUserNProc()
	if baseline == 0 {
		// /proc unreadable — fall back to a conservative absolute cap
		// large enough that a typical multi-user system isn't broken
		// but tight enough that a runaway fork-bomb saturates fast.
		// HIGH (silent-failure-hunter, #155): one-shot warn so an
		// operator triaging a fork-bomb-related deny on a /proc-masked
		// host (k8s with securityContext.procMount, stripped container)
		// has a breadcrumb. Per-spawn warn would drown the signal.
		procReadFallbackWarnOnce.Do(func() {
			slog.Warn("sandbox: /proc unreadable; using conservative absolute RLIMIT_NPROC fallback",
				"fallback_baseline", uint64(1024),
				"slack", childNProcSlack,
				"effective_cap", uint64(1024)+childNProcSlack)
		})
		baseline = 1024
	}
	// Close the snapshot->apply race: the gateway may have spawned threads
	// between the baseline read above and this point (Go's scheduler parks
	// and creates Ms continuously). Re-read and take the higher value so the
	// cap can never be applied already-exceeded, which would EAGAIN every
	// fork the child attempts.
	if live := readCurrentUserNProc(); live > baseline {
		baseline = live
	}
	nprocCap := baseline + childNProcSlack

	// Pin the HARD limit to the cap as well — do NOT inherit the child's
	// existing Max.
	//
	// An unprivileged process may raise its own SOFT limit up to its HARD
	// limit at will via setrlimit(2), and setrlimit/prlimit64 are not in the
	// seccomp denylist (pkg/sandbox/seccomp_linux.go blocks ptrace, mount,
	// module, kexec, bpf, perf_event_open — not resource limits). So leaving
	// Max at the inherited value (commonly max_user_processes, or
	// RLIM_INFINITY) would let a hostile child defeat the whole fork-bomb cap
	// with one line:
	//
	//	ulimit -u unlimited; :(){ :|:& };:
	//
	// "The soft limit contains a fork-bomb" is true only for a COOPERATING
	// child, and this control exists precisely for the adversarial one — see
	// redteam_forkbomb_test.go, whose bomb never calls ulimit and so cannot
	// observe the difference.
	//
	// We still clamp DOWN to any inherited hard limit: raising Max requires
	// CAP_SYS_RESOURCE, so asking for more than we inherited would EINVAL.
	// Clamping keeps that fix while restoring containment.
	// Clamp DOWNWARD only, against both inherited limits:
	//   - against Max, because raising a hard limit needs CAP_SYS_RESOURCE
	//     and asking for more than we inherited would EINVAL;
	//   - against Cur, because a HARDENING routine must never loosen an
	//     operator-configured limit. Without this, a unit file specifying
	//     LimitNPROC=512:4096 would be silently widened to baseline+128 for
	//     every sandboxed child — a policy inversion, and a silent one.
	nprocCapFinal := nprocCap
	var existing unix.Rlimit
	if err := unix.Prlimit(cmd.Process.Pid, unix.RLIMIT_NPROC, nil, &existing); err == nil {
		if existing.Max != unix.RLIM_INFINITY && nprocCapFinal > existing.Max {
			nprocCapFinal = existing.Max
		}
		if existing.Cur != unix.RLIM_INFINITY && nprocCapFinal > existing.Cur {
			nprocCapFinal = existing.Cur
		}
	}
	nprocLim := &unix.Rlimit{Cur: nprocCapFinal, Max: nprocCapFinal}

	slog.Debug("sandbox: applying RLIMIT_NPROC to child",
		"pid", cmd.Process.Pid,
		"baseline_tasks", baseline,
		"slack", childNProcSlack,
		"soft_cap", nprocLim.Cur,
		"hard_cap", nprocLim.Max)

	// FAIL CLOSED: if the cap we are about to apply is at or below the live
	// task count, every fork() by this child will return EAGAIN — the child
	// is born unusable. Return an error so the caller kills it and surfaces
	// the reason (hardened_exec.go does exactly that on a hardening error).
	//
	// This must NOT be a mere log line. Before the clamp above existed, this
	// situation surfaced as an EPERM from Prlimit and was therefore
	// fail-closed and loud; clamping made Prlimit succeed, which would have
	// converted it into a silent, fork-dead child whose only trace is a
	// warning in a gateway log the operator may never read. That is the same
	// silent-outage shape this file exists to prevent, reached through a
	// different door.
	//
	// Note this can only trigger via the downward clamps (an operator limit
	// genuinely lower than current usage). The unclamped cap is
	// baseline+slack, so it can never trip this by construction — the real
	// guard against a mis-measured baseline is
	// TestReadCurrentUserNProc_CountsThreadsNotProcesses, which checks the
	// counter against an independent kernel-sourced number.
	if nprocLim.Cur <= baseline {
		return fmt.Errorf(
			"RLIMIT_NPROC cap %d is at or below the current per-UID task count %d: "+
				"the child could not fork at all (inherited soft=%d hard=%d); "+
				"raise the process limit for this user",
			nprocLim.Cur, baseline, existing.Cur, existing.Max)
	}

	if err := unix.Prlimit(cmd.Process.Pid, unix.RLIMIT_NPROC, nprocLim, nil); err != nil {
		// EPERM here means the calling process lacks CAP_SYS_RESOURCE to
		// raise (or even SET) the limit. On a non-root gateway that would
		// only fire if the OS-level user nproc soft limit is below 32 —
		// in which case the OS's own limit is already containing the bomb.
		// We log via the returned error and let the caller decide; we do
		// NOT abort the spawn purely on RLIMIT_NPROC failure because the
		// other layers (regex guard + OS user limit) still apply.
		return fmt.Errorf("prlimit RLIMIT_NPROC: %w", err)
	}

	// RLIMIT_AS — memory cap (existing behavior, v0.1).
	if lim.MemoryLimitBytes == 0 {
		return nil
	}
	rlim := &unix.Rlimit{
		Cur: lim.MemoryLimitBytes,
		Max: lim.MemoryLimitBytes,
	}
	if err := unix.Prlimit(cmd.Process.Pid, unix.RLIMIT_AS, rlim, nil); err != nil {
		// A prlimit failure does not kill the child — we report the
		// error so the caller can decide whether to abort. In practice
		// the only way prlimit fails on a child we just forked is
		// EPERM (different user namespace), which is itself a
		// configuration bug worth surfacing.
		return fmt.Errorf("prlimit RLIMIT_AS: %w", err)
	}
	return nil
}

//go:build linux

package sandbox

import (
	"fmt"
	"log/slog"
	"sort"
	"sync/atomic"
	"unsafe"

	"golang.org/x/sys/unix"
)

// processSeccompInstalled latches to true the first time Install succeeds
// in this process. Paired with processLandlockApplied in sandbox_linux.go:
// both guards exist so repeated in-process boots (test harness) don't stack
// filters or fail with EEXIST. Production gateways boot once so the guard
// is a no-op there.
var processSeccompInstalled atomic.Bool

// syscallNrByName maps syscall names to Linux syscall numbers for the current
// architecture. Entries that do not exist on every arch (create_module,
// kexec_file_load, etc.) are populated at init() via the optional constants
// below. If a syscall is not available on the current arch, it is silently
// omitted from the deny list — the kernel never exposes it, so there is
// nothing to block.
var syscallNrByName = map[string]uint32{
	"ptrace":          unix.SYS_PTRACE,
	"mount":           unix.SYS_MOUNT,
	"umount2":         unix.SYS_UMOUNT2,
	"init_module":     unix.SYS_INIT_MODULE,
	"finit_module":    unix.SYS_FINIT_MODULE,
	"delete_module":   unix.SYS_DELETE_MODULE,
	"reboot":          unix.SYS_REBOOT,
	"swapon":          unix.SYS_SWAPON,
	"swapoff":         unix.SYS_SWAPOFF,
	"pivot_root":      unix.SYS_PIVOT_ROOT,
	"kexec_load":      unix.SYS_KEXEC_LOAD,
	"bpf":             unix.SYS_BPF,
	"perf_event_open": unix.SYS_PERF_EVENT_OPEN,
}

// blockedSyscallNrs derives numeric syscall IDs from the canonical blockedSyscallNames list.
// Syscall names that are not available on the current architecture are silently skipped.
func blockedSyscallNrs() []uint32 {
	nrs := make([]uint32, 0, len(blockedSyscallNames))
	for _, name := range blockedSyscallNames {
		nr, ok := syscallNrByName[name]
		if !ok {
			slog.Debug("seccomp: syscall not available on this architecture, skipping",
				"syscall", name)
			continue
		}
		nrs = append(nrs, nr)
	}
	return nrs
}

// Install loads the seccomp BPF filter into the kernel.
// Sets PR_SET_NO_NEW_PRIVS first, then installs the filter with
// SECCOMP_FILTER_FLAG_TSYNC for child-process inheritance (SEC-03).
// In ModeEnforce, blocked syscalls return EPERM (not SIGKILL). In
// ModePermissive, blocked syscalls return SECCOMP_RET_LOG — the call
// proceeds but an audit-log entry is written.
func (sp *SeccompProgram) Install() error {
	if processSeccompInstalled.Load() {
		// Process-wide idempotency: the first boot in this process
		// already installed a filter. Re-installing would stack a
		// second (identical) filter — harmless but wasteful — or fail
		// if ptrace is blocked by the first filter and the kernel
		// relies on it for SECCOMP_MODE_FILTER_CHECK. Either way the
		// process is already protected; skip.
		slog.Info("seccomp: install skipped — already installed in this process",
			"mode", string(sp.Mode()))
		return nil
	}
	nrs := blockedSyscallNrs()
	if len(nrs) == 0 {
		return nil
	}
	sort.Slice(nrs, func(i, j int) bool { return nrs[i] < nrs[j] })

	filter := assembleBPFMode(nrs, sp.Mode())
	if len(filter) == 0 {
		return fmt.Errorf("seccomp: empty BPF program")
	}

	// PR_SET_NO_NEW_PRIVS is required before installing a seccomp filter
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("seccomp: prctl(PR_SET_NO_NEW_PRIVS): %w", err)
	}

	prog := unix.SockFprog{
		Len:    uint16(len(filter)),
		Filter: &filter[0],
	}

	// Install filter with TSYNC so all threads get the filter (SEC-03)
	_, _, errno := unix.Syscall(
		unix.SYS_SECCOMP,
		uintptr(unix.SECCOMP_SET_MODE_FILTER),
		uintptr(unix.SECCOMP_FILTER_FLAG_TSYNC),
		uintptr(unsafe.Pointer(&prog)),
	)
	if errno != 0 {
		return fmt.Errorf("seccomp: SYS_SECCOMP install failed: %w", errno)
	}

	processSeccompInstalled.Store(true)
	slog.Info("Seccomp BPF filter installed",
		"blocked_syscalls", len(nrs), "tsync", sp.useTSync, "mode", string(sp.Mode()))
	return nil
}

// assembleBPFMode builds a classic BPF program that:
//  1. Loads the syscall architecture from seccomp_data.arch (offset 4) and
//     verifies it equals nativeAuditArch — the AUDIT_ARCH_* constant for the
//     GOARCH this binary was built for (defined per-arch: see
//     seccomp_linux_amd64.go, seccomp_linux_arm64.go, seccomp_linux_arm.go,
//     seccomp_linux_mipsle.go, seccomp_linux_riscv64.go,
//     seccomp_linux_loong64.go). This closes a real bypass: syscall numbers
//     are NOT stable across ABIs on the same CPU. On amd64, the legacy
//     32-bit compat entry point (`int $0x80` / vDSO sysenter, arch ==
//     AUDIT_ARCH_I386) hands the kernel a DIFFERENT numbering table than
//     the native x86_64 table this file's blockedNrs were resolved against
//     (e.g. native nr 101 is ptrace, but ia32 nr 101 is fork). Without this
//     check, every JEQ comparison below is checking the wrong table for a
//     compat-mode call, so an attacker can pick a 32-bit nr that collides
//     with an ALLOWED native syscall number and reach ptrace/mount/bpf/etc.
//     completely unfiltered. Any arch mismatch is therefore routed to the
//     SAME deny target as a matched blocked syscall — no new disposition.
//  2. Loads the syscall number from seccomp_data.nr (offset 0)
//  3. Compares against each blocked syscall number
//  4. In ModeEnforce: returns SECCOMP_RET_ERRNO(EPERM) for blocked syscalls
//     or an arch mismatch. In ModePermissive: returns SECCOMP_RET_LOG — the
//     syscall proceeds but the kernel writes an audit-log entry.
//  5. Returns SECCOMP_RET_ALLOW for everything else.
func assembleBPFMode(blockedNrs []uint32, mode Mode) []unix.SockFilter {
	// seccomp_data layout: offset 0 = nr (uint32), offset 4 = arch (uint32)
	const (
		offsetNr   = 0
		offsetArch = 4
	)

	// BPF constants from golang.org/x/sys/unix
	const (
		bpfLD  = unix.BPF_LD
		bpfW   = unix.BPF_W
		bpfABS = unix.BPF_ABS
		bpfJMP = unix.BPF_JMP
		bpfJEQ = unix.BPF_JEQ
		bpfK   = unix.BPF_K
		bpfRET = unix.BPF_RET

		seccompRetAllow = unix.SECCOMP_RET_ALLOW
		seccompRetErrno = unix.SECCOMP_RET_ERRNO
		seccompRetLog   = unix.SECCOMP_RET_LOG
	)

	// Deny target: in enforce mode, return errno EPERM; in permissive mode,
	// return RET_LOG so the call proceeds but the kernel logs it. This is
	// the core Sprint-J FR-J-012 behavior. RET_LOG has been in the kernel
	// since 4.14, well before our 5.13 Landlock floor, so it is always
	// available on any kernel that also has Landlock.
	// SECCOMP_RET_ERRNO encodes errno in the low 16 bits.
	var denyAction uint32
	if mode == ModePermissive {
		denyAction = uint32(seccompRetLog)
	} else {
		denyAction = uint32(seccompRetErrno) | uint32(unix.EPERM&0xFFFF)
	}

	n := len(blockedNrs)
	// Program structure (arch-check prepended ahead of the nr load/compares):
	// [0]         LD  [offsetArch]     — load syscall arch
	// [1]         JEQ nativeAuditArch  — mismatch falls through to deny (n+4)
	// [2]         LD  [offsetNr]       — load syscall number
	// [3..n+2]    JEQ nr, goto_deny    — one JEQ per blocked syscall
	// [n+3]       RET ALLOW            — default: allow
	// [n+4]       RET deny             — deny target (arch mismatch OR blocked nr)

	prog := make([]unix.SockFilter, 0, n+5)

	// Instruction 0: Load syscall arch
	prog = append(prog, unix.SockFilter{
		Code: uint16(bpfLD | bpfW | bpfABS),
		K:    offsetArch,
	})

	// Instruction 1: Verify arch matches nativeAuditArch. Jt=0 (fall through
	// to instruction 2, the nr load) on a match. Jf jumps to the deny target
	// at index n+4 on any mismatch. The jump is relative to the instruction
	// AFTER this one (index 2), so Jf = (n+4) - 2 = n+2.
	prog = append(prog, unix.SockFilter{
		Code: uint16(bpfJMP | bpfJEQ | bpfK),
		Jt:   0,
		Jf:   uint8(n + 2),
		K:    nativeAuditArch,
	})

	// Instruction 2: Load syscall number
	prog = append(prog, unix.SockFilter{
		Code: uint16(bpfLD | bpfW | bpfABS),
		K:    offsetNr,
	})

	// Instructions 3..n+2: Jump to deny if syscall matches.
	// Jump targets are relative: Jt/Jf are number of instructions to skip.
	// deny is at index n+4, allow is at index n+3. The gap between each
	// JEQ's own fall-through successor and the deny target is (n-i)
	// regardless of the two arch-check instructions prepended above, since
	// both the JEQ instructions and the deny target shifted by the same
	// +2 offset.
	for i, nr := range blockedNrs {
		remaining := n - i // instructions left after this one before allow
		prog = append(prog, unix.SockFilter{
			Code: uint16(bpfJMP | bpfJEQ | bpfK),
			Jt:   uint8(remaining), // jump to deny (skip remaining JEQs + allow)
			Jf:   0,                // fall through to next JEQ
			K:    nr,
		})
	}

	// Instruction n+3: RET ALLOW (default)
	prog = append(prog, unix.SockFilter{
		Code: uint16(bpfRET | bpfK),
		K:    seccompRetAllow,
	})

	// Instruction n+4: RET (deny). EPERM in enforce mode, RET_LOG in
	// permissive. Reached via a blocked-syscall JEQ match OR an arch
	// mismatch (instruction 1's Jf branch) — same disposition either way.
	prog = append(prog, unix.SockFilter{
		Code: uint16(bpfRET | bpfK),
		K:    denyAction,
	})

	return prog
}

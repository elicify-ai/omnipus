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
		Len:    uint16(len(filter)), // #nosec G115 -- len(filter) tracks blockedSyscallNames (sandbox.go), a fixed compile-time list currently 15 entries plus a handful of fixed arch/x32-check instructions (~20 total); assembleBPFMode's own uint8-jump bounds check above already fails closed long before this could approach uint16's 65535 range.
		Filter: &filter[0],
	}

	// Install filter with TSYNC so all threads get the filter (SEC-03)
	_, _, errno := unix.Syscall(
		unix.SYS_SECCOMP,
		uintptr(unix.SECCOMP_SET_MODE_FILTER),
		uintptr(unix.SECCOMP_FILTER_FLAG_TSYNC),
		uintptr(unsafe.Pointer(&prog)), // #nosec G103 -- seccomp(2), SECCOMP_SET_MODE_FILTER: prog is a stack-allocated unix.SockFprog passed by pointer only for this synchronous syscall; its Filter slice stays reachable via the still-in-scope `filter` local for the syscall's duration, and the kernel copies the program rather than retaining either pointer.
	)
	if errno != 0 {
		return fmt.Errorf("seccomp: SYS_SECCOMP install failed: %w", errno)
	}

	processSeccompInstalled.Store(true)
	slog.Info("Seccomp BPF filter installed",
		"blocked_syscalls", len(nrs), "tsync", sp.useTSync, "mode", string(sp.Mode()))
	return nil
}

// x32SyscallBit is the Linux kernel's __X32_SYSCALL_BIT (bit 30 of the raw
// syscall number, 0x40000000; see arch/x86/include/asm/unistd.h). It is a
// fixed ABI constant, not an architecture-varying one — it only matters on
// amd64, but its value is defined here (not in seccomp_linux_amd64.go)
// because it is used unconditionally, gated at runtime by nativeAuditArch,
// from assembleBPFMode below, which is compiled for every Linux GOARCH.
//
// amd64's x32 ABI (ILP32 userspace running on the x86_64 kernel) reports
// seccomp_data.arch == AUDIT_ARCH_X86_64 — the SAME value a native 64-bit
// syscall reports, so the arch check in assembleBPFMode does NOT by itself
// distinguish x32 from native — but the kernel ORs this bit into
// seccomp_data.nr for every x32 syscall. Since blockedNrs holds native
// x86_64 numbers (all well under 1000, no high bits set), a blocked
// syscall issued through the x32 ABI (e.g. ptrace = 101 natively,
// 0x40000065 under x32) does not numerically match any blockedNrs entry
// and would otherwise fall through to the default RET ALLOW: a second,
// independent kernel-sandbox bypass distinct from the ia32 compat-mode
// case handled by the arch check (which reports a different arch value
// entirely).
//
// Rather than maintaining a parallel x32 syscall-number blocklist,
// assembleBPFMode rejects the entire x32 ABI outright on amd64 — the same
// approach Docker's default seccomp profile and systemd's
// SystemCallArchitectures=native take. x32 is rarely used in practice
// (glibc does not enable it by default and most distributions ship no x32
// userspace at all), so blocking it entirely trades negligible
// compatibility loss for closing this bypass completely instead of
// chasing a second numbering table.
const x32SyscallBit = 0x40000000

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
//     (e.g. native nr 101 is ptrace, but ia32 nr 101 is ioperm). Without this
//     check, every JEQ comparison below is checking the wrong table for a
//     compat-mode call, so an attacker can pick a 32-bit nr that collides
//     with an ALLOWED native syscall number and reach ptrace/mount/bpf/etc.
//     completely unfiltered. Any arch mismatch is therefore routed to the
//     SAME deny target as a matched blocked syscall — no new disposition.
//  2. On amd64 only (nativeAuditArch == AUDIT_ARCH_X86_64): loads the
//     syscall number and rejects it outright if __X32_SYSCALL_BIT
//     (x32SyscallBit) is set. x32-mode syscalls report the SAME arch value
//     as native 64-bit syscalls (step 1's check does not catch them) but
//     carry a tagged, differently-numbered nr — see x32SyscallBit's doc
//     comment. Routed to the same deny target as everything else.
//  3. Loads (or, on amd64, reloads) the syscall number from seccomp_data.nr
//     (offset 0) and compares against each blocked syscall number.
//  4. In ModeEnforce: returns SECCOMP_RET_ERRNO(EPERM) for blocked syscalls,
//     an arch mismatch, or a tagged x32 syscall. In ModePermissive: returns
//     SECCOMP_RET_LOG — the syscall proceeds but the kernel writes an
//     audit-log entry.
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
		bpfALU = unix.BPF_ALU
		bpfAND = unix.BPF_AND

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

	// x32Check: only amd64 has an x32 ABI. nativeAuditArch is a per-arch
	// constant (see seccomp_linux_amd64.go et al.); comparing it against the
	// fixed AUDIT_ARCH_X86_64 value is a compile-time-safe, arch-agnostic
	// way to gate the extra instructions below without needing an
	// amd64-only build tag on this shared file.
	x32Check := nativeAuditArch == uint32(unix.AUDIT_ARCH_X86_64)
	x32Len := 0
	if x32Check {
		x32Len = 3 // LD nr, AND x32SyscallBit, JEQ x32SyscallBit
	}

	n := len(blockedNrs)
	// Program structure (arch-check prepended ahead of the nr load/compares;
	// the x32 sub-block, present only on amd64, is prepended ahead of the
	// nr load that feeds the blocked-nr chain):
	// [0]            LD  [offsetArch]        — load syscall arch
	// [1]            JEQ nativeAuditArch      — mismatch falls through to deny
	// amd64 only:
	// [2]            LD  [offsetNr]           — load nr to test the x32 tag
	// [3]            AND x32SyscallBit        — isolate the tag bit
	// [4]            JEQ x32SyscallBit        — tag set: falls through to deny
	// [base]         LD  [offsetNr]           — (re)load nr for the blocked-nr
	//                                            chain (base = 2 + x32Len)
	// [base+1..base+n]  JEQ nr, goto_deny     — one JEQ per blocked syscall
	// [allowIdx]     RET ALLOW                — default: allow
	// [denyIdx]      RET deny                 — deny target (arch mismatch,
	//                                            x32 tag, OR blocked nr)
	base := 2 + x32Len
	allowIdx := base + n + 1
	denyIdx := allowIdx + 1

	// Classic BPF's sock_filter.{Jt,Jf} fields are a single byte each
	// (uint8) — see struct sock_filter in linux/filter.h; the kernel ABI
	// cannot express a jump distance above 255. Every Jt/Jf value computed
	// below (denyIdx-2, and on amd64 also denyIdx-base, plus each
	// per-syscall "remaining" jump whose largest value is n) is a
	// truncating int -> uint8 conversion. blockedSyscallNames
	// (pkg/sandbox/sandbox.go) is a fixed, hand-maintained list — 15
	// entries today — but nothing previously stopped it from growing past
	// this program's jump-encoding ceiling. If it ever did, these
	// conversions would silently wrap and produce a MALFORMED-BUT-ACCEPTED
	// BPF program: a jump could land on the wrong instruction and turn a
	// syscall meant to be blocked into one that falls through to RET
	// ALLOW, weakening seccomp confinement with no error anywhere in the
	// boot path. Fail closed instead: return an empty program, which
	// Install already treats as a hard boot error ("seccomp: empty BPF
	// program").
	const maxBPFJump = 255 // math.MaxUint8, per struct sock_filter's Jt/Jf width
	if denyIdx-2 > maxBPFJump || (x32Check && denyIdx-base > maxBPFJump) || n > maxBPFJump {
		slog.Error("seccomp: blocked syscall list too large for classic BPF's uint8 jump encoding",
			"blocked_count", n, "deny_idx", denyIdx, "max_jump", maxBPFJump)
		return nil
	}

	prog := make([]unix.SockFilter, 0, denyIdx+1)

	// Instruction 0: Load syscall arch
	prog = append(prog, unix.SockFilter{
		Code: uint16(bpfLD | bpfW | bpfABS),
		K:    offsetArch,
	})

	// Instruction 1: Verify arch matches nativeAuditArch. Jt=0 (fall through
	// to instruction 2) on a match. Jf jumps to the deny target on any
	// mismatch. The jump is relative to the instruction AFTER this one
	// (index 2), so Jf = denyIdx - 2.
	prog = append(prog, unix.SockFilter{
		Code: uint16(bpfJMP | bpfJEQ | bpfK),
		Jt:   0,
		Jf:   uint8(denyIdx - 2), // #nosec G115 -- bounds-checked above: assembleBPFMode returns early (empty program) unless denyIdx-2 <= maxBPFJump (255), the classic-BPF Jf width.
		K:    nativeAuditArch,
	})

	if x32Check {
		// Instruction 2: Load nr to test for the x32 tag bit. arch==native
		// is already established by instruction 1, and on amd64 native ==
		// AUDIT_ARCH_X86_64 covers BOTH true native 64-bit syscalls and x32
		// syscalls (they are indistinguishable by arch alone), so this
		// check must run before the syscall is allowed through.
		prog = append(prog, unix.SockFilter{
			Code: uint16(bpfLD | bpfW | bpfABS),
			K:    offsetNr,
		})
		// Instruction 3: A &= x32SyscallBit — isolates the tag bit into A
		// (0 if clear, x32SyscallBit if set). This clobbers the raw nr, so
		// it must be reloaded afterward for the blocked-nr comparison chain.
		prog = append(prog, unix.SockFilter{
			Code: uint16(bpfALU | bpfAND | bpfK),
			K:    x32SyscallBit,
		})
		// Instruction 4: A == x32SyscallBit means the tag bit was set (x32
		// syscall) — deny unconditionally, regardless of the raw nr, since
		// x32 is rejected outright. A == 0 (Jf, fall through) means the tag
		// was clear (a true native 64-bit syscall) — proceed to reload nr
		// and evaluate it against the ordinary blocked-list chain. The jump
		// is relative to the instruction after this one (index base, the nr
		// reload), so Jt = denyIdx - base.
		prog = append(prog, unix.SockFilter{
			Code: uint16(bpfJMP | bpfJEQ | bpfK),
			Jt:   uint8(denyIdx - base), // #nosec G115 -- bounds-checked above: assembleBPFMode returns early (empty program) unless denyIdx-base <= maxBPFJump (255), the classic-BPF Jt width.
			Jf:   0,
			K:    x32SyscallBit,
		})
	}

	// Instruction `base`: (re)load syscall number for the blocked-nr chain.
	prog = append(prog, unix.SockFilter{
		Code: uint16(bpfLD | bpfW | bpfABS),
		K:    offsetNr,
	})

	// Instructions base+1..base+n: Jump to deny if syscall matches.
	// Jump targets are relative: Jt/Jf are number of instructions to skip.
	// deny is at denyIdx, allow is at allowIdx. The gap between each JEQ's
	// own fall-through successor and the deny target is (n-i) regardless of
	// how many instructions precede the chain, since both the JEQ
	// instructions and the deny target shift by the same offset together.
	for i, nr := range blockedNrs {
		remaining := n - i // instructions left after this one before allow
		prog = append(prog, unix.SockFilter{
			Code: uint16(bpfJMP | bpfJEQ | bpfK),
			Jt:   uint8(remaining), // #nosec G115 -- jump to deny (skip remaining JEQs + allow); bounds-checked above: remaining <= n, and assembleBPFMode returns early (empty program) unless n <= maxBPFJump (255), the classic-BPF Jt width. The marker must lead the comment: gosec only honours #nosec when it is the first thing in the comment text.
			Jf:   0,                // fall through to next JEQ
			K:    nr,
		})
	}

	// Instruction allowIdx: RET ALLOW (default)
	prog = append(prog, unix.SockFilter{
		Code: uint16(bpfRET | bpfK),
		K:    seccompRetAllow,
	})

	// Instruction denyIdx: RET (deny). EPERM in enforce mode, RET_LOG in
	// permissive. Reached via a blocked-syscall JEQ match, an arch
	// mismatch, or (amd64 only) a tagged x32 syscall — same disposition
	// either way.
	prog = append(prog, unix.SockFilter{
		Code: uint16(bpfRET | bpfK),
		K:    denyAction,
	})

	return prog
}

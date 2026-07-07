// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build linux

package sandbox

import (
	"testing"

	"golang.org/x/sys/unix"
)

// This file proves assembleBPFMode's arch-check gate (SEC-02/SEC-03): a
// syscall arriving through a non-native ABI entry point (e.g. the amd64
// 32-bit compat/int-$0x80 path, reported as seccomp_data.arch ==
// AUDIT_ARCH_I386) must be denied outright, never evaluated against the
// native blockedNrs table. Without the check, a compat-mode syscall number
// that happens to numerically collide with an ALLOWED native syscall would
// sail through every JEQ comparison unfiltered — defeating ptrace/mount/
// bpf/kexec_load blocking entirely.
//
// We do not spawn a real 32-bit process to trigger int $0x80 here: that
// requires either CGo/asm or an actual 32-bit compat-mode kernel, which CI
// runners are not guaranteed to have (this would be flaky). Instead we
// interpret the exact 3-opcode subset of classic BPF that assembleBPFMode
// emits (BPF_LD|W|ABS, BPF_JMP|JEQ|K, BPF_RET|K) against synthetic
// seccomp_data values. This proves the program's actual jump semantics —
// not just that the right opcodes appear — without touching the kernel.

// bpfExec is a minimal classic-BPF interpreter supporting exactly the
// instruction subset assembleBPFMode produces. It intentionally panics on
// any opcode outside that subset so a future change to the generator that
// this test hasn't been updated for fails loudly instead of silently
// mis-simulating.
func bpfExec(t *testing.T, prog []unix.SockFilter, nr, arch uint32) uint32 {
	t.Helper()
	pc := 0
	var a uint32 // accumulator
	steps := 0
	for {
		steps++
		if steps > len(prog)+1 {
			t.Fatalf("bpfExec: exceeded instruction budget (infinite loop?) at pc=%d", pc)
		}
		if pc < 0 || pc >= len(prog) {
			t.Fatalf("bpfExec: pc %d out of range (program has %d instructions)", pc, len(prog))
		}
		ins := prog[pc]
		switch ins.Code {
		case uint16(unix.BPF_LD | unix.BPF_W | unix.BPF_ABS):
			switch ins.K {
			case 0:
				a = nr
			case 4:
				a = arch
			default:
				t.Fatalf("bpfExec: unsupported LD_ABS offset %d at pc=%d", ins.K, pc)
			}
			pc++
		case uint16(unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K):
			if a == ins.K {
				pc += 1 + int(ins.Jt)
			} else {
				pc += 1 + int(ins.Jf)
			}
		case uint16(unix.BPF_RET | unix.BPF_K):
			return ins.K
		default:
			t.Fatalf("bpfExec: unsupported opcode 0x%x at pc=%d", ins.Code, pc)
		}
	}
}

// foreignAuditArch returns an AUDIT_ARCH_* constant that is NOT
// nativeAuditArch for the arch this test binary was built for, so tests can
// simulate a syscall arriving through a different ABI entry point. amd64's
// foreign arch is AUDIT_ARCH_I386 (the real 32-bit compat scenario this bug
// fix targets); every other arch just needs any distinct AUDIT_ARCH_*
// value, so ARM is used as the generic "other" arch (arm64's own compat
// path already covers the ARM case directly).
func foreignAuditArch() uint32 {
	if nativeAuditArch == uint32(unix.AUDIT_ARCH_ARM) {
		return uint32(unix.AUDIT_ARCH_X86_64)
	}
	return uint32(unix.AUDIT_ARCH_ARM)
}

// TestAssembleBPFMode_ArchCheckIsFirst asserts the program's first two
// instructions load seccomp_data.arch (offset 4) and compare it against
// nativeAuditArch — BEFORE the nr load/compare block. This is a structural
// check that the fix actually landed at the front of the program (an arch
// check appended after the nr comparisons would be too late: BPF has no
// backward re-evaluation, but more importantly a check placed after the
// nr JEQs wouldn't prevent them from already having matched/allowed).
func TestAssembleBPFMode_ArchCheckIsFirst(t *testing.T) {
	blocked := []uint32{unix.SYS_PTRACE, unix.SYS_MOUNT, unix.SYS_BPF}
	prog := assembleBPFMode(blocked, ModeEnforce)

	if len(prog) < 2 {
		t.Fatalf("program too short to contain an arch check: %d instructions", len(prog))
	}

	ld := prog[0]
	wantLD := uint16(unix.BPF_LD | unix.BPF_W | unix.BPF_ABS)
	if ld.Code != wantLD {
		t.Fatalf("instruction 0: got Code=0x%x, want LD|W|ABS=0x%x", ld.Code, wantLD)
	}
	if ld.K != 4 {
		t.Fatalf("instruction 0: got K(offset)=%d, want 4 (seccomp_data.arch)", ld.K)
	}

	cmp := prog[1]
	wantJEQ := uint16(unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K)
	if cmp.Code != wantJEQ {
		t.Fatalf("instruction 1: got Code=0x%x, want JMP|JEQ|K=0x%x", cmp.Code, wantJEQ)
	}
	if cmp.K != nativeAuditArch {
		t.Fatalf("instruction 1: got K=0x%x, want nativeAuditArch=0x%x", cmp.K, nativeAuditArch)
	}
	// Jt=0 means "fall through to the nr load on a match" — the arch check
	// must not itself jump anywhere on the expected/native arch.
	if cmp.Jt != 0 {
		t.Fatalf("instruction 1: got Jt=%d, want 0 (fall through on arch match)", cmp.Jt)
	}
	// Jf must jump forward (mismatch => deny), never 0/fall-through, or a
	// mismatched arch would silently proceed into the nr comparisons.
	if cmp.Jf == 0 {
		t.Fatalf("instruction 1: got Jf=0 — an arch mismatch would fall through instead of being denied")
	}

	// Instruction 2 must be the nr load, confirming the arch check didn't
	// clobber or reorder the pre-existing nr-load/compare block.
	if len(prog) < 3 {
		t.Fatalf("program too short to contain the nr load after the arch check")
	}
	nrLoad := prog[2]
	if nrLoad.Code != wantLD || nrLoad.K != 0 {
		t.Fatalf("instruction 2: got Code=0x%x K=%d, want LD|W|ABS of offset 0 (seccomp_data.nr)",
			nrLoad.Code, nrLoad.K)
	}
}

// TestAssembleBPFMode_ArchMismatchDeniesRegardlessOfNr is the core
// bypass-prevention proof. It picks an nr that is explicitly ALLOWED under
// the native arch (i.e. not in the blocked list) and confirms that same nr,
// presented under a foreign arch, is still denied — simulating the exact
// compat-ABI collision this bug allowed: a foreign-ABI syscall number that
// numerically matches an allowed native syscall must not slip through.
func TestAssembleBPFMode_ArchMismatchDeniesRegardlessOfNr(t *testing.T) {
	blocked := blockedSyscallNrs()
	if len(blocked) == 0 {
		t.Fatal("blockedSyscallNrs() returned no syscalls on this arch — cannot construct test")
	}
	prog := assembleBPFMode(blocked, ModeEnforce)

	// Find an nr that is NOT in the blocked set: allowed under the native
	// arch. getpid is never in the deny list.
	allowedNr := uint32(unix.SYS_GETPID)
	for _, b := range blocked {
		if b == allowedNr {
			t.Fatalf("test setup invariant violated: SYS_GETPID (%d) is unexpectedly in the blocked list", allowedNr)
		}
	}

	wantDeny := uint32(unix.SECCOMP_RET_ERRNO) | (uint32(unix.EPERM) & 0xFFFF)

	// Sanity: native arch + allowed nr => ALLOW. Establishes the baseline
	// so the next assertion is meaningful (not just "everything denies").
	verdict := bpfExec(t, prog, allowedNr, nativeAuditArch)
	if verdict != uint32(unix.SECCOMP_RET_ALLOW) {
		t.Fatalf("native arch + allowed nr: got verdict 0x%x, want SECCOMP_RET_ALLOW", verdict)
	}

	// Sanity: native arch + blocked nr => the deny disposition.
	verdict = bpfExec(t, prog, blocked[0], nativeAuditArch)
	if verdict != wantDeny {
		t.Fatalf("native arch + blocked nr %d: got verdict 0x%x, want deny 0x%x", blocked[0], verdict, wantDeny)
	}

	// THE BUG: foreign arch + the SAME allowedNr that passed above must now
	// be denied, because under a different ABI that nr does not mean what
	// the native table assumes it means.
	foreign := foreignAuditArch()
	verdict = bpfExec(t, prog, allowedNr, foreign)
	if verdict != wantDeny {
		t.Fatalf("foreign arch (0x%x) + nr %d (allowed on native arch): got verdict 0x%x, want deny 0x%x — "+
			"an arch mismatch must be denied even when the raw nr collides with an allowed native syscall",
			foreign, allowedNr, verdict, wantDeny)
	}

	// Also confirm a blocked nr is still denied under a foreign arch (not
	// specifically informative about the bug, but guards against a future
	// refactor accidentally ALLOWing on the mismatch path for blocked nrs).
	verdict = bpfExec(t, prog, blocked[0], foreign)
	if verdict != wantDeny {
		t.Fatalf("foreign arch + blocked nr %d: got verdict 0x%x, want deny 0x%x", blocked[0], verdict, wantDeny)
	}
}

// TestAssembleBPFMode_ArchMismatchPermissiveModeLogs confirms the arch
// mismatch path reuses the SAME per-mode disposition as a matched blocked
// syscall (RET_LOG in ModePermissive) rather than inventing a new one.
func TestAssembleBPFMode_ArchMismatchPermissiveModeLogs(t *testing.T) {
	blocked := blockedSyscallNrs()
	if len(blocked) == 0 {
		t.Fatal("blockedSyscallNrs() returned no syscalls on this arch — cannot construct test")
	}
	prog := assembleBPFMode(blocked, ModePermissive)

	foreign := foreignAuditArch()
	verdict := bpfExec(t, prog, uint32(unix.SYS_GETPID), foreign)
	if verdict != uint32(unix.SECCOMP_RET_LOG) {
		t.Fatalf("permissive mode, arch mismatch: got verdict 0x%x, want SECCOMP_RET_LOG 0x%x",
			verdict, uint32(unix.SECCOMP_RET_LOG))
	}
}

// TestAssembleBPFMode_JumpTargetsInternallyConsistent walks every
// instruction and asserts every JEQ's Jt/Jf lands on a valid in-range
// index, and that both terminal RET instructions are reachable and are the
// last two instructions (ALLOW then deny), matching the documented layout
// in assembleBPFMode's doc comment. This guards the "recompute jump offsets
// carefully" requirement independent of the behavioral tests above.
func TestAssembleBPFMode_JumpTargetsInternallyConsistent(t *testing.T) {
	blocked := blockedSyscallNrs()
	if len(blocked) == 0 {
		t.Fatal("blockedSyscallNrs() returned no syscalls on this arch — cannot construct test")
	}
	prog := assembleBPFMode(blocked, ModeEnforce)
	n := len(blocked)

	wantLen := n + 5 // LD arch, JEQ arch, LD nr, n*JEQ nr, RET allow, RET deny
	if len(prog) != wantLen {
		t.Fatalf("got %d instructions, want %d (n=%d blocked syscalls)", len(prog), wantLen, n)
	}

	denyIdx := n + 4
	allowIdx := n + 3

	for i, ins := range prog {
		switch ins.Code {
		case uint16(unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K):
			trueNext := i + 1 + int(ins.Jt)
			falseNext := i + 1 + int(ins.Jf)
			if trueNext < 0 || trueNext >= len(prog) {
				t.Fatalf("instruction %d: Jt branch targets out-of-range index %d (program len %d)",
					i, trueNext, len(prog))
			}
			if falseNext < 0 || falseNext >= len(prog) {
				t.Fatalf("instruction %d: Jf branch targets out-of-range index %d (program len %d)",
					i, falseNext, len(prog))
			}
		case uint16(unix.BPF_LD | unix.BPF_W | unix.BPF_ABS):
			if ins.K != 0 && ins.K != 4 {
				t.Fatalf("instruction %d: LD_ABS at unexpected offset %d", i, ins.K)
			}
		case uint16(unix.BPF_RET | unix.BPF_K):
			if i != allowIdx && i != denyIdx {
				t.Fatalf("instruction %d: unexpected RET instruction (expected only at %d and %d)",
					i, allowIdx, denyIdx)
			}
		default:
			t.Fatalf("instruction %d: unrecognized opcode 0x%x", i, ins.Code)
		}
	}

	if prog[allowIdx].K != uint32(unix.SECCOMP_RET_ALLOW) {
		t.Fatalf("instruction %d (expected ALLOW): got K=0x%x, want SECCOMP_RET_ALLOW=0x%x",
			allowIdx, prog[allowIdx].K, uint32(unix.SECCOMP_RET_ALLOW))
	}
	wantDeny := uint32(unix.SECCOMP_RET_ERRNO) | (uint32(unix.EPERM) & 0xFFFF)
	if prog[denyIdx].K != wantDeny {
		t.Fatalf("instruction %d (expected deny): got K=0x%x, want 0x%x", denyIdx, prog[denyIdx].K, wantDeny)
	}

	// The arch-check's Jf must land exactly on denyIdx (not merely
	// in-range) — this is the load-bearing offset the fix computes as n+2.
	archCheck := prog[1]
	archMismatchTarget := 1 + 1 + int(archCheck.Jf)
	if archMismatchTarget != denyIdx {
		t.Fatalf("arch-check Jf targets instruction %d, want deny target %d", archMismatchTarget, denyIdx)
	}
}

//go:build linux

package sandbox

import "testing"

// TestAssembleBPFMode_RefusesOversizedJumpEncoding pins the fail-closed guard
// on classic BPF's single-byte jump fields.
//
// struct sock_filter's Jt/Jf are uint8 (linux/filter.h), so a jump offset above
// 255 cannot be encoded. assembleBPFMode computes those offsets from the number
// of blocked syscalls. Before the guard, an oversized list would silently
// TRUNCATE the int->uint8 conversions and emit a filter the kernel ACCEPTS but
// whose jumps land on the wrong instructions — a syscall meant to be denied
// could fall through to RET ALLOW. That is a weakened sandbox with no error
// anywhere in the boot path, which is the worst possible failure mode for the
// layer whose whole job is containing a compromised agent.
//
// The contract is therefore: refuse, do not truncate. assembleBPFMode returns
// an empty program, and Install turns that into a hard boot error
// ("seccomp: empty BPF program") rather than installing a partial filter.
//
// blockedSyscallNames is 15 entries today, far below the limit; this test
// exists so that growing it can never reintroduce silent truncation.
func TestAssembleBPFMode_RefusesOversizedJumpEncoding(t *testing.T) {
	// Sanity: the real list must assemble to a NON-empty program, otherwise a
	// trivially-broken assembler would pass the oversized case for free.
	if got := assembleBPFMode(blockedSyscallNrs(), ModeEnforce); len(got) == 0 {
		t.Fatalf("the real blocked-syscall list produced an EMPTY program — either the "+
			"bounds guard is firing when it must not, or the assembler is broken; "+
			"blocked count = %d", len(blockedSyscallNrs()))
	}

	// 300 distinct syscall numbers pushes every jump offset past uint8's 255.
	oversized := make([]uint32, 0, 300)
	for i := uint32(0); i < 300; i++ {
		oversized = append(oversized, i)
	}

	if got := assembleBPFMode(oversized, ModeEnforce); len(got) != 0 {
		t.Fatalf("assembleBPFMode returned a %d-instruction program for %d blocked "+
			"syscalls — the uint8 jump offsets MUST have truncated, so this filter is "+
			"malformed-but-installable and could let a denied syscall reach RET ALLOW. "+
			"It must fail closed (empty program) instead.", len(got), len(oversized))
	}
}

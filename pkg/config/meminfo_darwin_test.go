//go:build darwin

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

import (
	"os"
	"strings"
	"testing"
)

// NOTE ON EXECUTABILITY, and why it is different from meminfo_other_test.go's:
// this file RUNS. It is gated `//go:build darwin` and macOS is the primary
// development platform for this project, so unlike the BSD stub's tests these
// assertions actually execute against a real kernel on a real machine. That
// is the entire reason the Darwin reader was written rather than left to the
// no-signal stub: the memory mechanism is now testable by the people most
// likely to notice it misbehaving.
//
// Linux-only CI does NOT run this file. A Darwin regression is caught by a
// developer on a Mac, or not at all.

// TestReadMemTotalBytes_Darwin_MatchesHwMemsize pins the ORACLE for total
// memory to hw.memsize — the hardware figure — and not to a reconstruction
// from the vm.page_* counters times hw.pagesize.
//
// The distinction matters: the page counters are a live partition of the
// memory the VM subsystem is currently tracking, and they do not sum to
// physical RAM (wired, compressor and reserved pages are accounted
// differently). A reader that reconstructed the total from them would drift
// against the machine's actual hardware, and every ratio computed against
// that total would drift with it.
func TestReadMemTotalBytes_Darwin_MatchesHwMemsize(t *testing.T) {
	got, ok := readMemTotalBytes()
	if !ok {
		t.Fatal("readMemTotalBytes() on Darwin reported undeterminable — hw.memsize is present on every Mac")
	}

	// Independent oracle: read hw.memsize through a completely different
	// path (the sysctl(8) binary) rather than through the same x/sys call the
	// implementation uses, so this cannot pass by both sides making the same
	// mistake.
	want := sysctlViaBinary(t, "hw.memsize")
	if got != want {
		t.Fatalf("readMemTotalBytes() = %d, want %d (hw.memsize as reported by the sysctl binary)", got, want)
	}

	// A Mac with under 1 GiB of RAM has not shipped this century; a reading
	// far below that means the reader is summing the wrong thing.
	if got < 1<<30 {
		t.Fatalf("readMemTotalBytes() = %d, which is under 1 GiB — that is not a real Mac's physical memory", got)
	}
}

// TestReadMemAvailableBytes_Darwin_StrictlyInsideZeroAndTotal asserts the
// property that actually matters about an approximation: it must land inside
// the physical envelope. Nothing can pin the exact value — it moves between
// two consecutive sysctl calls — but a reader that double-counts a term, or
// mixes up pages and bytes, escapes this bracket immediately.
func TestReadMemAvailableBytes_Darwin_StrictlyInsideZeroAndTotal(t *testing.T) {
	total, ok := readMemTotalBytes()
	if !ok {
		t.Fatal("readMemTotalBytes() on Darwin reported undeterminable")
	}
	avail, ok := readMemAvailableBytes()
	if !ok {
		t.Fatal("readMemAvailableBytes() on Darwin reported undeterminable — the vm.page_* sysctls are present on every Mac")
	}
	if avail == 0 {
		t.Fatal("readMemAvailableBytes() = 0 on a running Mac — a machine with literally zero available memory cannot have run this test")
	}
	if avail >= total {
		t.Fatalf("readMemAvailableBytes() = %d >= readMemTotalBytes() = %d — more memory is reported available than the machine physically has, which is the signature of a double-counted term (vm.page_speculative_count is contained in vm.page_pageable_external_count; see the reader's doc comment)", avail, total)
	}
}

// TestReadMemAvailableBytes_Darwin_MatchesVmStatWithinTolerance is the
// cross-check FR-064 asks for: the assembled figure is validated against
// vm_stat's independent accounting of the same page classes, read through the
// Mach host_statistics64 call rather than through sysctl.
//
// WHAT THIS CATCHES: a missing term, a term read from the wrong OID, a
// pages-versus-bytes unit error, an endianness or width mistake. All of those
// move the answer by a factor, not a percent, and all of them escape a 12%
// bracket immediately.
//
// WHAT THIS DOES NOT CATCH, stated plainly because a test whose limits are
// undocumented gets trusted for things it cannot do: it cannot detect the
// speculative/external DOUBLE-COUNT. On the host this was derived on, the
// speculative term is ~2.7% of the assembled figure, while the free-page
// count alone swings by a comparable amount between two samples seconds apart
// (7,432 to 52,248 pages observed). A tolerance tight enough to catch the
// double-count would be flaky on ordinary page movement. The overlap is
// instead established by TestVmStatOracle_ConfirmsSpeculativeIsInsideExternal
// (the kernel identity that makes it a double-count at all) and enforced by
// TestMeminfoDarwin_FormulaOmitsTheSpeculativeTerm (a structural assertion
// that the reader does not read that OID). This was verified by mutation:
// re-adding the speculative term leaves THIS test green and the structural
// one red.
func TestReadMemAvailableBytes_Darwin_MatchesVmStatWithinTolerance(t *testing.T) {
	got, ok := readMemAvailableBytes()
	if !ok {
		t.Fatal("readMemAvailableBytes() on Darwin reported undeterminable")
	}

	pageSize := sysctlViaBinary(t, "hw.pagesize")
	if pageSize == 0 {
		t.Fatal("hw.pagesize reported 0")
	}
	stats := vmStatPages(t)

	// Rebuild the reader's three terms from the independent oracle. vm_stat
	// publishes free and purgeable directly; it publishes no internal/external
	// split, so the external term is derived from the identity
	// active+inactive+speculative == internal+external, with internal read
	// through the sysctl BINARY (again, not through the x/sys call the
	// implementation uses).
	internal := sysctlViaBinary(t, "vm.page_pageable_internal_count")
	partition := stats["active"] + stats["inactive"] + stats["speculative"]
	if partition <= internal {
		t.Fatalf("degenerate oracle: vm_stat partition %d <= pageable_internal %d", partition, internal)
	}
	external := partition - internal

	want := float64(stats["free"]+stats["purgeable"]+external) * float64(pageSize)
	if want == 0 {
		t.Fatal("oracle computed 0 available bytes")
	}

	const tolerance = 0.12
	spread := (float64(got) - want) / want
	if spread < 0 {
		spread = -spread
	}
	t.Logf("readMemAvailableBytes() = %d bytes; vm_stat-derived oracle = %.0f bytes; spread = %.2f%%", got, want, spread*100)
	if spread > tolerance {
		t.Fatalf("readMemAvailableBytes() = %d differs from the vm_stat-derived oracle %.0f by %.2f%%, above the %.0f%% tolerance — a term is missing, mis-named, or in the wrong unit",
			got, want, spread*100, tolerance*100)
	}
}

// TestMeminfoDarwin_FormulaOmitsTheSpeculativeTerm is the structural guard the
// value cross-check above cannot be: it asserts the reader never reads
// vm.page_speculative_count.
//
// The omission is a measured decision (speculative pages are contained in
// vm.page_pageable_external_count — see
// TestVmStatOracle_ConfirmsSpeculativeIsInsideExternal), and it is the kind of
// decision a later reader of the code is very likely to "fix" by adding the
// obviously-missing term back. Re-added, it over-states available memory by
// roughly 190 MB on a 32 GiB host — small enough that every other test here
// stays green and the resulting figure still looks entirely plausible.
//
// So the guard has to be structural. This asserts on the FUNCTION BODY, not
// on the file, so the doc comment is still free to name the term (it must —
// an unexplained omission reads as an oversight).
func TestMeminfoDarwin_FormulaOmitsTheSpeculativeTerm(t *testing.T) {
	src, err := os.ReadFile("meminfo_darwin.go")
	if err != nil {
		t.Fatalf("read meminfo_darwin.go: %v", err)
	}
	text := string(src)

	const marker = "func readMemAvailableBytes() (uint64, bool) {"
	start := strings.Index(text, marker)
	if start < 0 {
		t.Fatalf("could not find readMemAvailableBytes's declaration — this guard has silently stopped guarding anything")
	}
	body := text[start:]
	if end := strings.Index(body, "\n}\n"); end >= 0 {
		body = body[:end]
	}

	if strings.Contains(body, "speculative") {
		t.Fatalf("readMemAvailableBytes's body reads a speculative page count.\n"+
			"vm.page_speculative_count is CONTAINED IN vm.page_pageable_external_count "+
			"(proved by TestVmStatOracle_ConfirmsSpeculativeIsInsideExternal), so adding it double-counts and over-states "+
			"available memory. Body:\n%s", body)
	}
}

// TestMeminfoDarwin_FormulaIsDocumentedAtCallSite asserts the reader's own
// doc comment states the formula, names the term it deliberately OMITS, and
// carries the two caveats. This is not decoration: the value is an
// approximation of a different measurement, and an operator or a future
// implementer who reads it as "macOS MemAvailable" will draw wrong
// conclusions from it. The documentation is part of the requirement.
func TestMeminfoDarwin_FormulaIsDocumentedAtCallSite(t *testing.T) {
	src, err := os.ReadFile("meminfo_darwin.go")
	if err != nil {
		t.Fatalf("read meminfo_darwin.go: %v", err)
	}
	doc := string(src)

	required := map[string]string{
		"vm.page_free_count":              "the free term",
		"vm.page_purgeable_count":         "the purgeable term",
		"vm.page_pageable_external_count": "the file-backed term",
		"vm.page_speculative_count":       "the deliberately omitted term (it must be NAMED, so the omission reads as a decision rather than an oversight)",
		"APPROXIMATION":                   "the statement that this is not the same measurement as Linux MemAvailable",
		"COMPRESSION":                     "the compression caveat",
		"ACTIVE FILE PAGES":               "the active-file-pages caveat",
	}
	for needle, why := range required {
		if !strings.Contains(doc, needle) {
			t.Errorf("meminfo_darwin.go does not mention %q — %s", needle, why)
		}
	}
}

// TestReadMemAvailableBytes_Darwin_UnreadableSysctlIsUndeterminable proves the
// ok=false branch is REACHABLE on Darwin rather than dead code. Without it
// nothing on this platform ever exercises the undeterminable path, and a
// reader that had quietly started returning a fabricated figure on error
// would look identical from the outside.
func TestReadMemAvailableBytes_Darwin_UnreadableSysctlIsUndeterminable(t *testing.T) {
	old := darwinPageExtSysctl
	darwinPageExtSysctl = "vm.page_this_oid_does_not_exist"
	t.Cleanup(func() { darwinPageExtSysctl = old })

	got, ok := readMemAvailableBytes()
	if ok {
		t.Fatalf("readMemAvailableBytes() with an unreadable sysctl = (%d, true), want ok=false", got)
	}
	if got != 0 {
		t.Fatalf("readMemAvailableBytes() with an unreadable sysctl = %d, want 0 alongside ok=false", got)
	}
}

// TestReadCgroupMemoryBudgetBytes_Darwin_AlwaysAbsent: cgroups are a Linux
// kernel feature. If this ever reported ok=true on Darwin, availableRAMBytes
// would be taking a minimum against a signal that cannot exist.
func TestReadCgroupMemoryBudgetBytes_Darwin_AlwaysAbsent(t *testing.T) {
	if _, _, ok := readCgroupMemoryBudgetBytes(); ok {
		t.Fatal("readCgroupMemoryBudgetBytes() ok = true on Darwin, want false (cgroups are Linux-only)")
	}
	if _, ok := readCgroupMemoryAvailableBytes(); ok {
		t.Fatal("readCgroupMemoryAvailableBytes() ok = true on Darwin, want false (cgroups are Linux-only)")
	}
}

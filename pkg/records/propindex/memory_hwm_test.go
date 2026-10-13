// Omnipus — the instrument behind TestMemory_TheQueryPathStaysInsideTheBudget.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !records_no_sqlite && !mipsle && !netbsd && !windows && !(freebsd && arm)

package propindex

import (
	"runtime"
	"testing"
)

// The budget test is only as good as its reading of the child's peak. These pin
// the reading: it rejects a missing or zero value (so the budget test can never
// pass on nothing) and it moves when the process really holds memory (so a
// breach is visible).

func TestChildVmHWM_RejectsMissingAndZeroValues(t *testing.T) {
	cases := map[string]string{
		"no line":         "MEASURED: idle, index open\nsome other output\n",
		"zero":            "MEASURED: x\nVMHWM: 0\n",
		"negative":        "VMHWM: -5\n",
		"not a number":    "VMHWM: lots\n",
		"empty value":     "VMHWM:\n",
		"empty output":    "",
		"wrong key shape": "VmHWM: 12345\n",
	}
	for name, out := range cases {
		t.Run(name, func(t *testing.T) {
			if v, ok := childVmHWM(out); ok {
				t.Fatalf("childVmHWM(%q) = (%d, true); a missing or unusable value must be rejected", out, v)
			}
		})
	}
	if v, ok := childVmHWM("noise\nVMHWM: 52428800\ntrailing\n"); !ok || v != 52428800 {
		t.Fatalf("childVmHWM on a good line = (%d, %v), want (52428800, true)", v, ok)
	}
}

func TestReadOwnVmHWM_SeesMemoryTheProcessHolds(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skipf("VmHWM comes from /proc/self/status, which %s does not have; the budget test keeps the wait4 figure there", runtime.GOOS)
	}
	// VmHWM is a PEAK. In a shared test binary an earlier, heavier test may have
	// pushed it far above what the process holds now, and new memory then moves
	// the peak only by the part that lands ABOVE the old peak. The ballast must
	// therefore be measured against the CURRENT resident set, not against the old
	// peak: after holding the ballast the peak is at least what was resident
	// before plus (most of) the ballast, whatever ran earlier and however the
	// garbage collector is timed. (PR 1260 CI: the peak was already 61.7 MiB, the
	// process held far less, and a delta-against-the-old-peak check missed by
	// 256 KiB.)
	rssBefore, err := readOwnVmRSS()
	if err != nil || rssBefore <= 0 {
		t.Fatalf("readOwnVmRSS() = (%d, %v), want a positive reading", rssBefore, err)
	}
	// Touch every page so the memory is resident, not merely reserved.
	const ballast = 96 << 20
	b := make([]byte, ballast)
	for i := 0; i < len(b); i += 4096 {
		b[i] = 1
	}
	after, err := readOwnVmHWM()
	if err != nil {
		t.Fatalf("readOwnVmHWM: %v", err)
	}
	runtime.KeepAlive(b)
	if want := rssBefore + ballast*3/4; after < want {
		t.Fatalf("VmHWM is %d after holding %d bytes resident on top of %d already resident; it should be at least %d, so the instrument cannot see a breach",
			after, ballast, rssBefore, want)
	}
}

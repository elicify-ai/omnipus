// Omnipus — the instrument behind TestMemory_TheQueryPathStaysInsideTheBudget.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !records_no_sqlite && !mipsle && !windows

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
	before, err := readOwnVmHWM()
	if err != nil || before <= 0 {
		t.Fatalf("readOwnVmHWM() = (%d, %v), want a positive reading", before, err)
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
	if after < before+ballast*3/4 {
		t.Fatalf("VmHWM moved from %d to %d after holding %d bytes resident; the instrument cannot see a breach", before, after, ballast)
	}
}

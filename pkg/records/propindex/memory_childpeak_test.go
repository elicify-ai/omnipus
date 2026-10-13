// Omnipus — pins childPeak, the one place that picks the child's peak RSS for
// the memory-budget tests (R3-QA-06).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !records_no_sqlite && !mipsle && !windows

package propindex

import (
	"runtime"
	"strings"
	"syscall"
	"testing"
)

// recordingTB is a testing.TB whose Fatalf records the message and stops the
// calling goroutine, exactly as a real Fatalf does, so childPeak's failure
// branch can be observed instead of failing this test.
type recordingTB struct {
	testing.TB
	fatal string
	done  chan struct{}
}

func (r *recordingTB) Helper() {}

func (r *recordingTB) Fatalf(format string, args ...any) {
	r.fatal = format
	_ = args
	close(r.done)
	runtime.Goexit()
}

// runChildPeak calls childPeak on a goroutine of its own (Goexit ends only that
// goroutine) and reports whether it called Fatalf and what it returned.
func runChildPeak(out string, usage *syscall.Rusage) (fatal string, peak int64, source string, ru int64, returned bool) {
	rec := &recordingTB{done: make(chan struct{})}
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		peak, source, ru = childPeak(rec, "idle", out, usage)
		returned = true
	}()
	<-finished
	return rec.fatal, peak, source, ru, returned
}

// On Linux a child that printed no usable VmHWM FAILS the budget test; it must
// never fall back to wait4's ru_maxrss, which carries the parent's peak.
func TestChildPeak_LinuxRefusesToFallBackToTheInheritedFigure(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skipf("only Linux reads the child's own VmHWM; %s keeps the wait4 figure (see the next test)", runtime.GOOS)
	}
	usage := &syscall.Rusage{Maxrss: 123456}
	for name, out := range map[string]string{
		"no VmHWM line": "MEASURED: idle\n",
		"zero value":    "VMHWM: 0\n",
		"empty output":  "",
	} {
		t.Run(name, func(t *testing.T) {
			fatal, _, _, _, returned := runChildPeak(out, usage)
			if returned || !strings.Contains(fatal, "reported no usable VmHWM") {
				t.Fatalf("childPeak(%q): returned=%v fatal=%q; a missing VmHWM must fail the test, not fall back to ru_maxrss", out, returned, fatal)
			}
		})
	}

	fatal, peak, source, ru, returned := runChildPeak("noise\nVMHWM: 52428800\n", usage)
	if !returned || fatal != "" {
		t.Fatalf("a good VmHWM line must not fail: returned=%v fatal=%q", returned, fatal)
	}
	if peak != 52428800 || source != "child VmHWM" {
		t.Fatalf("childPeak = (%d, %q), want the child's own 52428800 from %q", peak, source, "child VmHWM")
	}
	if ru != peakRSSBytes(usage) || ru == peak {
		t.Fatalf("ruMaxRSS = %d must be the wait4 figure %d, reported for contrast and distinct from the peak %d", ru, peakRSSBytes(usage), peak)
	}
}

// Off Linux the wait4 figure is the peak, and a missing VmHWM line is not an error.
func TestChildPeak_NonLinuxKeepsTheWait4Figure(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("on Linux the child's own VmHWM is required (previous test)")
	}
	usage := &syscall.Rusage{Maxrss: 123456}
	fatal, peak, source, _, returned := runChildPeak("no VmHWM here\n", usage)
	if !returned || fatal != "" {
		t.Fatalf("off Linux a missing VmHWM must not fail: returned=%v fatal=%q", returned, fatal)
	}
	if peak != peakRSSBytes(usage) || source != "wait4 ru_maxrss" {
		t.Fatalf("childPeak = (%d, %q), want the wait4 figure %d", peak, source, peakRSSBytes(usage))
	}
}

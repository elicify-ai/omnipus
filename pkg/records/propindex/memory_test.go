// Omnipus — ADR-068 D16.3a C-2 / D16.4 item 4 / FR-066b: the memory budget,
// MEASURED rather than inherited.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !records_no_sqlite && !mipsle && !windows

package propindex

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

// ---------------------------------------------------------------------------
// WHY THIS IS MEASURED IN A CHILD PROCESS
//
// The budget is stated in PEAK RSS, and peak RSS is a property of a process, not
// of a function. Measured in-process it would carry every allocation every other
// test in this binary made first — including the 50,000-record corpora
// bounds_test.go builds — and would report a number that has nothing to do with
// the query path.
//
// So the parent builds the corpus once and hands each measurement to a fresh
// child, whose peak RSS is read from wait4's rusage. The number therefore
// includes the Go runtime's own baseline, which is correct: an operator's 64 MB
// is spent on the whole process, not on the part of it we find interesting.
//
// ADR-068 D16.4 item 4 is explicit that the 64 MB budget is INHERITED from a
// measurement of bleve ALONE and is UNVERIFIED for this design, and that
// asserting it over an unmeasured store would be the same move the withdrawn
// latency argument made. So this test measures and REPORTS, and it also asserts
// — because a budget nobody fails is not a budget.
// ---------------------------------------------------------------------------

// budgetBytes is ADR-067's steady-state RSS budget, which ADR-068 inherits.
const budgetBytes = 64 << 20

const (
	envPhase = "PROPINDEX_MEM_PHASE"
	envDB    = "PROPINDEX_MEM_DB"
)

// peakRSSBytes normalises what the platform's rusage reports.
//
// Darwin reports ru_maxrss in BYTES; Linux reports it in KIBIBYTES. Getting this
// wrong understates the measurement by 1024x on Linux, which would turn a failed
// budget into a comfortable pass — the exact shape of false green this project
// keeps a document about.
func peakRSSBytes(r *syscall.Rusage) int64 {
	if runtime.GOOS == "darwin" {
		return int64(r.Maxrss) //nolint:unconvert // Maxrss is int32 on 32-bit platforms
	}
	return int64(r.Maxrss) * 1024 //nolint:unconvert // same
}

func mib(b int64) string { return fmt.Sprintf("%.1f MiB", float64(b)/(1<<20)) }

// ---------------------------------------------------------------------------
// WHY LINUX READS THE CHILD'S OWN VmHWM, NOT wait4's ru_maxrss
//
// On Linux the child's ru_maxrss is NOT the child's own peak. Go starts the
// child with a shared address space and exec then folds the OLD address space's
// high-water mark into the child's rusage, so wait4 reports max(the parent's
// peak so far, the child's own peak). The parent has just built the 50,000-record
// corpus, so the "child" figure was the parent's peak: it was identical across
// the idle, b2 and b1 phases (PR 1260: 66.6 MiB in all three, 50.8 MiB in all
// three on a quiet machine) whatever the phase did, and it moved with the
// parent's garbage-collection timing and the runner rather than with the query
// path. The kernel exposes the child's own peak as VmHWM in /proc/self/status,
// read by the child at the end of its phase and reported on a line the parent
// parses. ru_maxrss is still logged beside it, for the record, never asserted.
//
// A missing, unparsable or zero VmHWM is a test failure, never a pass. Platforms
// without /proc/self/status (darwin) keep the wait4 figure, which does not carry
// this quirk there.
// ---------------------------------------------------------------------------

const vmHWMPrefix = "VMHWM:"

// readOwnVmHWM returns this process's own peak resident set, in bytes, from
// /proc/self/status. It fails (rather than returning 0) on anything it cannot
// read and parse.
func readOwnVmHWM() (int64, error) { return readOwnStatusBytes("VmHWM") }

// readOwnVmRSS is the process's CURRENT resident set (the peak is VmHWM), read
// the same strict way. The instrument self-test needs the current figure because
// a peak only moves by the part of new memory that lands ABOVE the old peak.
func readOwnVmRSS() (int64, error) { return readOwnStatusBytes("VmRSS") }

// readOwnStatusBytes reads one "<key>:  <n> kB" line of /proc/self/status as
// bytes. A missing line, a different unit or a non-positive number is an error.
func readOwnStatusBytes(key string) (int64, error) {
	raw, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0, fmt.Errorf("read /proc/self/status: %w", err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		rest, ok := strings.CutPrefix(line, key+":")
		if !ok {
			continue
		}
		fields := strings.Fields(rest) // "  51234 kB"
		if len(fields) != 2 || fields[1] != "kB" {
			return 0, fmt.Errorf("unexpected %s line %q", key, line)
		}
		kb, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil || kb <= 0 {
			return 0, fmt.Errorf("unusable %s value in %q", key, line)
		}
		return kb * 1024, nil
	}
	return 0, fmt.Errorf("no %s line in /proc/self/status", key)
}

// childPeak is the one place that decides which figure is the child's peak. On
// Linux it is the child's own VmHWM, and a child that did not report a usable one
// FAILS the test (tb.Fatalf) instead of falling back to the inherited ru_maxrss;
// elsewhere it is the wait4 figure. Both phases of the single-index test and the
// two-index test use it, so the rule exists once.
func childPeak(tb testing.TB, phase, out string, usage *syscall.Rusage) (peak int64, source string, ruMaxRSS int64) {
	tb.Helper()
	peak = peakRSSBytes(usage)
	ruMaxRSS, source = peak, "wait4 ru_maxrss"
	if runtime.GOOS == "linux" {
		own, ok := childVmHWM(out)
		if !ok {
			tb.Fatalf("the %s child reported no usable VmHWM; refusing to pass on the inherited ru_maxrss figure\n%s", phase, out)
		}
		peak, source = own, "child VmHWM"
	}
	return peak, source, ruMaxRSS
}

// reportOwnPeak is the child's half: it prints its own VmHWM on the line the
// parent parses. On Linux a child that cannot read its own peak fails.
func reportOwnPeak(tb testing.TB) {
	tb.Helper()
	if runtime.GOOS != "linux" {
		return
	}
	hwm, err := readOwnVmHWM()
	if err != nil {
		tb.Fatalf("the child cannot read its own peak RSS: %v", err)
	}
	fmt.Printf("%s %d\n", vmHWMPrefix, hwm)
}

// childVmHWM extracts the VmHWM line the child printed.
func childVmHWM(out string) (int64, bool) {
	for _, line := range strings.Split(out, "\n") {
		if i := strings.Index(line, vmHWMPrefix); i >= 0 {
			v, err := strconv.ParseInt(strings.TrimSpace(line[i+len(vmHWMPrefix):]), 10, 64)
			return v, err == nil && v > 0
		}
	}
	return 0, false
}

// TestMemory_TheQueryPathStaysInsideTheBudget measures idle, B2's 10,000
// survivors and B1's 50,000 evaluated candidates.
func TestMemory_TheQueryPathStaysInsideTheBudget(t *testing.T) {
	if phase := os.Getenv(envPhase); phase != "" {
		runMemoryPhase(t, phase, os.Getenv(envDB))
		return
	}
	if testing.Short() {
		t.Skip("builds a 50,000-record corpus")
	}

	dbPath := buildMeasurementCorpus(t, BoundNarrowedCandidates)

	for _, phase := range []string{"idle", "b2", "b1"} {
		t.Run(phase, func(t *testing.T) {
			cmd := exec.Command(os.Args[0],
				"-test.run=^TestMemory_TheQueryPathStaysInsideTheBudget$", "-test.v")
			cmd.Env = append(os.Environ(), envPhase+"="+phase, envDB+"="+dbPath)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("the %s measurement failed: %v\n%s", phase, err, out)
			}
			usage, ok := cmd.ProcessState.SysUsage().(*syscall.Rusage)
			if !ok {
				t.Fatalf("this platform does not report rusage; the budget cannot be measured here")
			}
			// The child's OWN peak on Linux (see the block above); never a silent
			// fallback to the inherited ru_maxrss figure.
			peak, source, ruMaxRSS := childPeak(t, phase, string(out), usage)

			// The child prints what it actually did, so a measurement of nothing
			// cannot pass as a measurement of the query path.
			work := childWork(string(out))
			t.Logf("PEAK RSS %s = %s (%d bytes, %s), wait4 ru_maxrss %s, budget %s, %s",
				phase, mib(peak), peak, source, mib(ruMaxRSS), mib(budgetBytes), work)
			if work == "" {
				t.Fatalf("the child reported no work; this measurement proves nothing\n%s", out)
			}
			if peak > budgetBytes {
				t.Errorf("the %s phase peaked at %s, above the %s budget "+
					"(ADR-068 D16.4 item 4: the budget is a TARGET this design had not verified — "+
					"this is the verification, and it says the target is missed)",
					phase, mib(peak), mib(budgetBytes))
			}
		})
	}
}

// childWork extracts the line the child printed about what it measured.
func childWork(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if i := strings.Index(line, "MEASURED:"); i >= 0 {
			return strings.TrimSpace(line[i:])
		}
	}
	return ""
}

// buildMeasurementCorpus writes a full-fidelity corpus once, in the parent.
//
// Full fidelity matters: a corpus of one-property records would measure the cost
// of a schema nobody has. Each record carries all eight declared properties, a
// multi-value list, two relations and two checkbox rows.
func buildMeasurementCorpus(t *testing.T, n int) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "measurement.db")
	store, err := Open(context.Background(), path, Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	idx, ok := store.(*Index)
	if !ok {
		t.Fatalf("buildMeasurementCorpus: store is %T, not *Index", store)
	}
	sc := plantSchema(t)
	const batchSize = 1000
	batch := make([]NoteRows, 0, batchSize)
	for i := range n {
		src := fmt.Sprintf(`---
type: plant
id: PL-%06d
species: Monstera deliciosa
condition: %s
planted: 2026-03-%02d
height_cm: 41.25
cuttings: %d
bed: "[[Bed %d]]"
keeper: "[[Rosa]]"
labels: [indoor, humid]
---

- [ ] repot in spring
- [x] moved to the east window
`, i, []string{"seedling", "growing", "dormant"}[i%3], i%28+1, i%9, i%40)
		batch = append(batch, note(t, fmt.Sprintf("garden/bulk/p-%06d.md", i), sc, src))
		if len(batch) == batchSize {
			if err := idx.UpsertNotes(context.Background(), batch); err != nil {
				t.Fatalf("UpsertNotes: %v", err)
			}
			batch = batch[:0]
		}
	}
	if len(batch) > 0 {
		if err := idx.UpsertNotes(context.Background(), batch); err != nil {
			t.Fatalf("UpsertNotes: %v", err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if fi, err := os.Stat(path); err == nil {
		t.Logf("measurement corpus: %d records, index file %s", n, mib(fi.Size()))
	}
	// t.TempDir() is removed when the PARENT test ends, which is after every
	// child has run.
	return path
}

// runMemoryPhase is the child. It opens the prebuilt corpus and does exactly one
// thing, so the peak it leaves behind belongs to that one thing.
func runMemoryPhase(t *testing.T, phase, dbPath string) {
	ctx := context.Background()
	store, err := Open(ctx, dbPath, Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()

	switch phase {
	case "idle":
		n, err := store.CountCandidates(ctx, Selector{RecordType: "plant"})
		if err != nil {
			t.Fatalf("CountCandidates: %v", err)
		}
		fmt.Printf("MEASURED: idle, index open, %d records visible, nothing streamed\n", n)

	case "b2":
		// B2's bound: 10,000 survivors. The comparator accepts until the abort
		// fires, which is the most memory the row-returning path can be asked
		// for.
		accepted := 0
		err := store.Candidates(ctx, Selector{RecordType: "plant"}, func(Candidate) (Verdict, error) {
			accepted++
			return Accepted, nil
		})
		if err == nil || !IsBoundExceeded(err) {
			t.Fatalf("expected B2 to abort, got %v", err)
		}
		fmt.Printf("MEASURED: b2, %d candidates accepted before the streaming abort\n", accepted)

	case "b1":
		// B1's bound: the whole narrowed population evaluated and rejected —
		// FR-064a's aggregate-only shape, which retrieves and evaluates up to
		// 50,000 candidates and materialises one result row.
		evaluated, values := 0, 0
		sc := plantSchema(t)
		err := store.Candidates(ctx, Selector{RecordType: "plant"}, func(c Candidate) (Verdict, error) {
			evaluated++
			// Decode as the comparator would: measuring a stream nobody looks at
			// would measure the driver, not the query path.
			for _, name := range c.PropOrder {
				prop, ok := sc.Property(name)
				if !ok {
					continue
				}
				pv, err := c.Props[name].Typed(prop)
				if err != nil {
					return Rejected, err
				}
				values += len(pv.Values)
			}
			return Rejected, nil
		})
		if err != nil {
			t.Fatalf("Candidates: %v", err)
		}
		fmt.Printf("MEASURED: b1, %d candidates evaluated and typed, %d values decoded\n", evaluated, values)

	default:
		t.Fatalf("unknown measurement phase %q", phase)
	}

	reportOwnPeak(t)
}

// TestMemory_BothIndexesTogether (D16.4 item 4's "both indexes" half) lives in
// memory_both_test.go, in the external propindex_test package — it needs
// pkg/knowledge, which imports this package back (pkg/knowledge/author.go), so
// it cannot be a white-box test here without an import cycle. This file keeps
// only the single-index measurement; peakRSSBytes, mib, budgetBytes and
// childWork are exported for that file via export_test.go.

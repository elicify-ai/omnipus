// Omnipus — ADR-068 D16.3a C-2 / D16.4 item 4 / FR-066b: the memory budget,
// MEASURED rather than inherited.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !records_no_sqlite && !mipsle && !netbsd && !(freebsd && arm)

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
			peak := peakRSSBytes(usage)

			// The child prints what it actually did, so a measurement of nothing
			// cannot pass as a measurement of the query path.
			work := childWork(string(out))
			t.Logf("PEAK RSS %s = %s (%d bytes), budget %s, %s",
				phase, mib(peak), peak, mib(budgetBytes), work)
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
			if err := store.(*Index).UpsertNotes(context.Background(), batch); err != nil {
				t.Fatalf("UpsertNotes: %v", err)
			}
			batch = batch[:0]
		}
	}
	if len(batch) > 0 {
		if err := store.(*Index).UpsertNotes(context.Background(), batch); err != nil {
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
}

// TestMemory_BothIndexesTogether is D16.4 item 4's "both indexes" half.
//
// It is OPT-IN because it builds a real corpus of Markdown files on disk and
// syncs a real bleve index over them, which is minutes of work and hundreds of
// megabytes of disk — a cost that does not belong in every run of the package's
// tests. Run it with:
//
//	PROPINDEX_MEASURE_BLEVE=<n> go test -tags goolm,stdjson \
//	  -run '^TestMemory_BothIndexesTogether$' -timeout 60m ./pkg/records/propindex/
//
// The number it reports is the one D16.4 item 4 asks for and the one the ADR is
// careful NOT to claim: the budget is a target, and this is what says whether it
// is met on a given machine.
func TestMemory_BothIndexesTogether(t *testing.T) {
	raw := os.Getenv("PROPINDEX_MEASURE_BLEVE")
	if raw == "" {
		t.Skip("opt-in: set PROPINDEX_MEASURE_BLEVE=<record count> to build a real corpus and measure both indexes")
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		t.Fatalf("PROPINDEX_MEASURE_BLEVE must be a positive record count, got %q", raw)
	}
	t.Logf("measurement harness: writes %d notes to a temp dir, syncs a real bleve index over them, "+
		"then measures a fresh child process holding BOTH indexes (memory_both_test.go)", n)
	measureBothIndexes(t, n)
}

// Omnipus — ADR-068 D16.4 item 4: BOTH indexes, idle and at both bounds,
// against the inherited 64 MB budget.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !records_no_sqlite && !mipsle && !netbsd && !windows && !(freebsd && arm)

package propindex

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/knowledge"
)

// ---------------------------------------------------------------------------
// WHY THIS EXISTS SEPARATELY
//
// D16.4 item 4 is the ADR's most carefully worded caveat: the 64 MB budget was
// measured for BLEVE ALONE (idle 12.9-15.1 MB, 23.6-24.0 MB streamed at the
// cap), the two-index design keeps all of that and adds cost on both sides, and
// "asserting the inherited budget over an unmeasured store is the same move the
// latency argument made". The budget is a TARGET, not a property the ADR claims.
//
// Measuring it honestly means a real corpus of real Markdown files, a real bleve
// sync over them, and the properties index over the same notes — in one process,
// because the budget is spent by one process. That is minutes of work and
// hundreds of megabytes of disk, so it is opt-in rather than part of every run.
// ---------------------------------------------------------------------------

const (
	envBothPhase = "PROPINDEX_BOTH_PHASE"
	envBothHome  = "PROPINDEX_BOTH_HOME"
	envBothRoot  = "PROPINDEX_BOTH_ROOT"
	envBothDB    = "PROPINDEX_BOTH_DB"
)

func measureBothIndexes(t *testing.T, n int) {
	t.Helper()

	base := t.TempDir()
	home := filepath.Join(base, "home")
	root := filepath.Join(base, "vault")
	dbPath := filepath.Join(base, "properties.db")
	for _, d := range []string{home, filepath.Join(root, "garden")} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}

	start := time.Now()
	writeCorpus(t, root, dbPath, n)
	t.Logf("corpus of %d notes written and indexed into the properties index in %s", n, time.Since(start).Round(time.Second))

	start = time.Now()
	syncText(t, home, root)
	t.Logf("bleve sync of %d notes in %s", n, time.Since(start).Round(time.Second))

	for _, phase := range []string{"idle", "b2", "b1"} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestMemory_BothIndexesChild$", "-test.v")
		cmd.Env = append(os.Environ(),
			envBothPhase+"="+phase,
			envBothHome+"="+home,
			envBothRoot+"="+root,
			envBothDB+"="+dbPath,
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("the %s measurement failed: %v\n%s", phase, err, out)
		}
		usage, ok := cmd.ProcessState.SysUsage().(*syscall.Rusage)
		if !ok {
			t.Fatal("this platform does not report rusage; the budget cannot be measured here")
		}
		peak := peakRSSBytes(usage)
		work := childWork(string(out))
		t.Logf("BOTH INDEXES, %s: PEAK RSS %s (%d bytes), budget %s, %s",
			phase, mib(peak), peak, mib(budgetBytes), work)
		if work == "" {
			t.Fatalf("the child reported no work; the measurement proves nothing\n%s", out)
		}
		if peak > budgetBytes {
			t.Errorf("BOTH INDEXES, %s: peaked at %s, above the %s target. "+
				"D16.4 item 4 states the budget is inherited and unverified for this design; "+
				"this is the verification and it MISSES.",
				phase, mib(peak), mib(budgetBytes))
		}
	}
}

// TestMemory_BothIndexesChild is the child half. It is skipped unless the parent
// invoked it, and it exists as a named test only because that is how a test
// binary re-enters itself.
func TestMemory_BothIndexesChild(t *testing.T) {
	phase := os.Getenv(envBothPhase)
	if phase == "" {
		t.Skip("driven by TestMemory_BothIndexesTogether")
	}
	ctx := context.Background()

	text, err := knowledge.OpenIndex(os.Getenv(envBothHome), os.Getenv(envBothRoot))
	if err != nil {
		t.Fatalf("opening the text index: %v", err)
	}
	defer func() {
		if cerr := text.Close(); cerr != nil {
			t.Errorf("closing the text index: %v", cerr)
		}
	}()

	props, err := Open(ctx, os.Getenv(envBothDB), Options{})
	if err != nil {
		t.Fatalf("opening the properties index: %v", err)
	}
	defer func() {
		if err := props.Close(); err != nil {
			t.Errorf("closing the properties index: %v", err)
		}
	}()

	switch phase {
	case "idle":
		count, err := props.CountCandidates(ctx, Selector{RecordType: "plant"})
		if err != nil {
			t.Fatalf("CountCandidates: %v", err)
		}
		hits, err := text.Search("monstera", 10)
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		fmt.Printf("MEASURED: both indexes open, %d records visible, one text search returned %d hits\n",
			count, len(hits))

	case "b2":
		accepted := 0
		err := props.Candidates(ctx, Selector{RecordType: "plant"}, func(Candidate) (Verdict, error) {
			accepted++
			return Accepted, nil
		})
		if err != nil && !IsBoundExceeded(err) {
			t.Fatalf("Candidates: %v", err)
		}
		hits, herr := text.Search("monstera", 200)
		if herr != nil {
			t.Fatalf("Search: %v", herr)
		}
		fmt.Printf("MEASURED: both indexes, %d candidates accepted to the B2 bound, %d text hits\n",
			accepted, len(hits))

	case "b1":
		evaluated, values := 0, 0
		sc := plantSchema(t)
		err := props.Candidates(ctx, Selector{RecordType: "plant"}, func(c Candidate) (Verdict, error) {
			evaluated++
			for _, name := range c.PropOrder {
				prop, ok := sc.Property(name)
				if !ok {
					continue
				}
				pv, terr := c.Props[name].Typed(prop)
				if terr != nil {
					return Rejected, terr
				}
				values += len(pv.Values)
			}
			return Rejected, nil
		})
		if err != nil {
			t.Fatalf("Candidates: %v", err)
		}
		hits, herr := text.Search("monstera", 200)
		if herr != nil {
			t.Fatalf("Search: %v", herr)
		}
		fmt.Printf("MEASURED: both indexes, %d candidates evaluated and typed (%d values), %d text hits\n",
			evaluated, values, len(hits))

	default:
		t.Fatalf("unknown phase %q", phase)
	}
}

// writeCorpus writes n real notes to disk and indexes them into the properties
// index. The notes are what bleve will later walk, so both indexes see exactly
// the same bytes — which is also what makes the source_hash comparison
// meaningful rather than decorative.
func writeCorpus(t *testing.T, root, dbPath string, n int) {
	t.Helper()
	sc := plantSchema(t)
	store, err := Open(context.Background(), dbPath, Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()

	const batchSize = 1000
	batch := make([]NoteRows, 0, batchSize)
	for i := range n {
		rel := fmt.Sprintf("garden/p-%06d.md", i)
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

# Plant %d

A cutting taken in spring, kept in bright indirect light near the east window.

- [ ] repot in spring
- [x] moved to the east window
`, i, []string{"seedling", "growing", "dormant"}[i%3], i%28+1, i%9, i%40, i)
		mustWriteFile(t, filepath.Join(root, rel), src)
		batch = append(batch, note(t, rel, sc, src))
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
}

func syncText(t *testing.T, home, root string) {
	t.Helper()
	ix, err := knowledge.OpenIndex(home, root)
	if err != nil {
		t.Fatalf("OpenIndex: %v", err)
	}
	defer func() {
		if cerr := ix.Close(); cerr != nil {
			t.Errorf("closing the text index: %v", cerr)
		}
	}()
	stats, err := ix.SyncWith(context.Background(), knowledge.SyncOptions{})
	if err != nil {
		t.Fatalf("SyncWith: %v", err)
	}
	t.Logf("text index: scanned %d, indexed %d", stats.Scanned, stats.Indexed)
}

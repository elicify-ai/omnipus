// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// knowledge_drift_resync_test.go — ADR-067 FR-038/FR-038a and US-6's P0.
//
// # The defect
//
// The drift check ran on mount and on schedule, produced a correct report, and
// repaired nothing. Meanwhile knowledge_search kept answering
// SearchReport.Complete = true, because Complete is derived from the progress
// tracker — "no index run is in flight" — which is idle for a stale index just
// as it is for a fresh one.
//
// Measured end to end on a real binary before the fix: mount a vault, search a
// word, 0 hits, complete:true. Write a note containing that word into the
// mounted vault. Search again immediately, and again a minute later: still 0
// hits, still complete:true, still "Searched the whole of this knowledge base;
// its index was complete at query time." Only a process restart re-indexed.
//
// That is a confidently incomplete answer, which is the one failure US-6
// exists to make impossible.
//
// # What is asserted
//
// The repair, not the report. The drift lane's reporting behaviour (runs on
// mount and per tick, silent when healthy, one run per collection) is already
// covered in knowledge_lifecycle_test.go; nothing there observes what happens
// AFTER an unhealthy report, because nothing used to happen.

package gateway

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/knowledge"
)

// TestKnowledgeDrift_UnhealthyReportTriggersAReindex is the repair.
//
// The drift check is stubbed to report unhealthy once, so the test never waits
// on a six-hour schedule and never depends on what CheckDrift happens to
// consider drift. What it measures is the consequence: a reconcile ran after
// the report, and the note written after the first index is now in the index.
//
// DIES ON: removing kl.resyncAfterDrift(r.Root) from the notify wrapper in
// NewKnowledgeLifecycle. Under that mutation the report is still produced and
// still logged, and the index stays exactly as stale as it was found.
func TestKnowledgeDrift_UnhealthyReportTriggersAReindex(t *testing.T) {
	home := kltHome(t)
	vault := kltVault(t, map[string]string{
		"notes/first.md": "# First\n\nPresent at the first index.\n",
	})

	var (
		mu     sync.Mutex
		checks int
	)
	reported := make(chan knowledge.DriftReport, 4)

	kl := kltLifecycle(t, KnowledgeLifecycleOptions{
		Home: home,
		// One unhealthy report, then healthy for ever. A checker that reported
		// unhealthy on every run would make a resync loop look like a passing
		// test.
		DriftCheck: func(_ context.Context, ix *knowledge.Index) (knowledge.DriftReport, error) {
			mu.Lock()
			defer mu.Unlock()
			checks++
			if checks == 1 {
				// CheckedAt is when the check STARTED. The lifecycle refuses to
				// repair from a report older than the last completed index run,
				// so a test that left this zero would be asserting on a report
				// the production code correctly discards.
				return knowledge.DriftReport{
					Root:      vault,
					CheckedAt: time.Now(),
					Findings:  []knowledge.DriftFinding{{Kind: knowledge.DriftNotIndexed, Path: "notes/second.md"}},
				}, nil
			}
			return knowledge.DriftReport{Root: vault}, nil
		},
		DriftNotify: func(r knowledge.DriftReport) {
			select {
			case reported <- r:
			default:
			}
		},
	})

	require.NoError(t, kl.AttachMount(context.Background(), "ws-drift", "vault", vault))

	// A note that did not exist when the collection was first indexed. This is
	// the operator editing their own vault in Obsidian while Omnipus is running
	// — the ordinary case, not an exotic one.
	require.NoError(t, os.WriteFile(
		filepath.Join(vault, "notes", "second.md"),
		[]byte("# Second\n\nA note about brontosaurus, written after the first index.\n"), 0o600))

	select {
	case <-reported:
	case <-time.After(10 * time.Second):
		t.Fatal("no drift report arrived: the check does not run on mount (FR-038a)")
	}

	kl.WaitForAttaches()

	ix, ok := kl.IndexForRoot(vault)
	require.True(t, ok, "the collection must still be attached")

	// The oracle is the note's own content, written by this test. A search that
	// finds it can only have been served by an index that was rebuilt AFTER the
	// file appeared.
	found := false
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		hits, err := ix.Search("brontosaurus", 10)
		require.NoError(t, err)
		for _, h := range hits {
			if filepath.ToSlash(h.Path) == "notes/second.md" {
				found = true
			}
		}
		if found {
			break
		}
		kl.WaitForAttaches()
		time.Sleep(50 * time.Millisecond)
	}

	assert.True(t, found,
		"the drift check reported the index stale and the index was never rebuilt. "+
			"knowledge_search still answers 'Searched the whole of this knowledge base; "+
			"its index was complete at query time' over a collection it has not read since "+
			"boot — the confidently incomplete answer US-6 is written to prevent")
}

// TestKnowledgeDrift_ResyncIsANoOpForAnUnattachedCollection pins the two
// degraded paths, so the repair cannot become a way to index folders nothing
// mounted.
func TestKnowledgeDrift_ResyncIsANoOpForAnUnattachedCollection(t *testing.T) {
	home := kltHome(t)
	kl := kltLifecycle(t, KnowledgeLifecycleOptions{Home: home})

	// A real folder nobody mounted, and a path that does not exist at all.
	// Neither may open an index, and neither may panic.
	fresh := func(root string) knowledge.DriftReport {
		return knowledge.DriftReport{Root: root, CheckedAt: time.Now()}
	}
	assert.NotPanics(t, func() { kl.resyncAfterDrift(fresh(t.TempDir())) })
	assert.NotPanics(t, func() { kl.resyncAfterDrift(fresh(filepath.Join(home, "gone"))) })
	kl.WaitForAttaches()

	assert.Empty(t, kl.AttachedRoots(),
		"a drift resync must never attach a collection nothing mounted — the mount is the "+
			"grant, and re-indexing outside it would index a folder the operator revoked")
}

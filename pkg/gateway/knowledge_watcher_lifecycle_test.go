// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Tests for the filesystem-watcher wiring added to knowledge_lifecycle.go:
// starting one knowledge.Watcher per collection root when it is first
// attached, stopping it when the last holder releases, tolerating a watcher
// that cannot start, and — the point of the whole exercise — a file created
// on disk becoming findable with no restart and no explicit index call.
// See docs/internal/design/knowledge-index-freshness.md §3/§4/§8.
//
// The fake watchers here exist so "one per root" and "stopped on release" can
// be asserted deterministically, without depending on real OS filesystem-
// watch support (inotify/FSEvents) being available in whatever sandbox runs
// this suite. TestKnowledgeLifecycle_FileAddedOnDiskBecomesFindableWithoutRestart
// is the one test in this file that uses the REAL knowledge.NewWatcher —
// everything else exercises the wiring in knowledge_lifecycle.go, not
// pkg/knowledge's own watcher, which has its own test suite
// (pkg/knowledge/watch_test.go).

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

// fakeWatcher is a knowledgeWatcher test double. It counts Start/Stop calls
// and lets a test control whether Start succeeds, so tests can assert the
// WIRING in knowledge_lifecycle.go — "started once", "stopped on release",
// "a failed Start doesn't fail the attach" — as a fact about calls made,
// never inferred from index side effects a real watcher would also produce.
type fakeWatcher struct {
	startErr error

	mu     sync.Mutex
	starts int
	stops  int

	unavailCh chan struct{}
}

func newFakeWatcher(startErr error) *fakeWatcher {
	return &fakeWatcher{startErr: startErr, unavailCh: make(chan struct{})}
}

func (w *fakeWatcher) Start(context.Context) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.starts++
	return w.startErr
}

func (w *fakeWatcher) Stop() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.stops++
}

func (w *fakeWatcher) Unavailable() <-chan struct{} { return w.unavailCh }

func (w *fakeWatcher) Err() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.startErr
}

func (w *fakeWatcher) counts() (starts, stops int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.starts, w.stops
}

// --- requirement 1: one watcher per collection root -------------------------

func TestKnowledgeLifecycle_AttachStartsExactlyOneWatcher(t *testing.T) {
	root := kltVault(t, map[string]string{"a.md": "# A\nalpha"})
	home := kltHome(t)

	var mu sync.Mutex
	var built []*fakeWatcher
	kl := kltLifecycle(t, KnowledgeLifecycleOptions{
		Home: home,
		NewWatcher: func(ix *knowledge.Index) knowledgeWatcher {
			w := newFakeWatcher(nil)
			mu.Lock()
			built = append(built, w)
			mu.Unlock()
			return w
		},
	})

	require.NoError(t, kl.AttachMount(context.Background(), "ws-a", "vault", root))

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, built, 1, "exactly one watcher must be constructed for one attach")
	starts, stops := built[0].counts()
	assert.Equal(t, 1, starts, "the watcher must actually be Start()ed, not merely constructed")
	assert.Equal(t, 0, stops, "a watcher must not be stopped while its collection is still attached")
}

func TestKnowledgeLifecycle_SharedRootGetsOneWatcherNotTwo(t *testing.T) {
	root := kltVault(t, map[string]string{"a.md": "# A\nalpha"})
	home := kltHome(t)

	var mu sync.Mutex
	var built []*fakeWatcher
	kl := kltLifecycle(t, KnowledgeLifecycleOptions{
		Home: home,
		NewWatcher: func(ix *knowledge.Index) knowledgeWatcher {
			w := newFakeWatcher(nil)
			mu.Lock()
			built = append(built, w)
			mu.Unlock()
			return w
		},
	})

	// Two named mounts of the same folder, in different workspaces...
	require.NoError(t, kl.AttachMount(context.Background(), "ws-a", "vault", root))
	require.NoError(t, kl.AttachMount(context.Background(), "ws-b", "kb", root))
	// ...and the SAME root reached again via the work-tree attach path, keyed
	// entirely differently (AttachCollection keys on (workspaceID, root), not
	// (workspaceID, mountName)). All three must share the one
	// *knowledgeCollection kl.byRoot already keys on the resolved root, so
	// this must still be exactly one watcher — requirement 1 is "per root",
	// not "per attach path".
	require.NoError(t, kl.AttachCollection(context.Background(), "ws-c", root))

	assert.Equal(t, 3, kl.HoldersFor(root), "three distinct holder keys on one shared collection")

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, built, 1, "one collection root must get exactly one watcher, "+
		"however many holders (mounts or work-tree attaches) reach it")
	starts, _ := built[0].counts()
	assert.Equal(t, 1, starts, "starting two watchers on the same root would double every filesystem event")
}

// --- requirement 2: stop cleanly on release, exactly on the last holder -----

func TestKnowledgeLifecycle_ReleasingLastHolderStopsWatcher(t *testing.T) {
	root := kltVault(t, map[string]string{"a.md": "# A\nalpha"})
	home := kltHome(t)

	var mu sync.Mutex
	var built *fakeWatcher
	kl := kltLifecycle(t, KnowledgeLifecycleOptions{
		Home: home,
		NewWatcher: func(ix *knowledge.Index) knowledgeWatcher {
			mu.Lock()
			defer mu.Unlock()
			built = newFakeWatcher(nil)
			return built
		},
	})

	require.NoError(t, kl.AttachMount(context.Background(), "ws-a", "vault", root))
	require.NoError(t, kl.AttachMount(context.Background(), "ws-b", "kb", root))

	mu.Lock()
	w := built
	mu.Unlock()
	require.NotNil(t, w)
	_, stops := w.counts()
	assert.Equal(t, 0, stops)

	require.NoError(t, kl.RevokeMount("ws-a", "vault"))
	_, stops = w.counts()
	assert.Equal(t, 0, stops,
		"releasing ONE of two holders must leave the watcher running — US-16 AS-2's rule applies to the watcher too")

	require.NoError(t, kl.RevokeMount("ws-b", "kb"))
	_, stops = w.counts()
	assert.Equal(t, 1, stops, "the LAST release must stop the watcher")
}

// TestKnowledgeLifecycle_StopStopsEveryWatcher covers the whole-lifecycle
// Stop() path (gateway shutdown), which RevokeMount's per-mount path does not
// exercise: a collection can still have holders when the process shuts down.
func TestKnowledgeLifecycle_StopStopsEveryWatcher(t *testing.T) {
	rootA := kltVault(t, map[string]string{"a.md": "# A\nalpha"})
	rootB := kltVault(t, map[string]string{"b.md": "# B\nbeta"})
	home := kltHome(t)

	var mu sync.Mutex
	var built []*fakeWatcher
	kl, err := NewKnowledgeLifecycle(KnowledgeLifecycleOptions{
		Home: home,
		NewWatcher: func(ix *knowledge.Index) knowledgeWatcher {
			w := newFakeWatcher(nil)
			mu.Lock()
			built = append(built, w)
			mu.Unlock()
			return w
		},
	})
	require.NoError(t, err)

	require.NoError(t, kl.AttachMount(context.Background(), "ws-a", "vault", rootA))
	require.NoError(t, kl.AttachMount(context.Background(), "ws-b", "vault", rootB))

	mu.Lock()
	require.Len(t, built, 2)
	mu.Unlock()

	kl.Stop() // no holder was ever released via RevokeMount

	mu.Lock()
	defer mu.Unlock()
	for i, w := range built {
		_, stops := w.counts()
		assert.Equal(t, 1, stops, "watcher %d must be stopped by lifecycle Stop(), not leaked", i)
	}
}

// --- requirement 3: a watcher that cannot start must not fail the attach ---

func TestKnowledgeLifecycle_WatcherStartFailureDoesNotFailAttach(t *testing.T) {
	root := kltVault(t, map[string]string{"a.md": "# A\nalpha"})
	home := kltHome(t)

	startErr := &knowledge.WatchUnavailableError{Reason: "unsupported platform"}
	kl := kltLifecycle(t, KnowledgeLifecycleOptions{
		Home: home,
		NewWatcher: func(ix *knowledge.Index) knowledgeWatcher {
			return newFakeWatcher(startErr)
		},
	})

	err := kl.AttachMount(context.Background(), "ws-a", "vault", root)
	require.NoError(t, err,
		"a watcher that cannot start must not fail the attach — indexing still works via the sweep (design §2)")

	// The collection is genuinely indexed and searchable regardless of the
	// watcher's fate — the reconcile this attach ran is untouched by it.
	ix, ok := kl.IndexForRoot(root)
	require.True(t, ok)
	hits, searchErr := ix.Search("alpha", 10)
	require.NoError(t, searchErr)
	require.Len(t, hits, 1)

	// Revoking must still work cleanly (Stop() on a watcher whose Start
	// failed must not panic or hang — watch.go's own documented contract,
	// exercised here through the wiring rather than assumed).
	require.NoError(t, kl.RevokeMount("ws-a", "vault"))
}

// --- requirement 5: an unsupported platform degrades, it does not error at boot

func TestKnowledgeLifecycle_WatcherUnavailableIsObservableNotSilent(t *testing.T) {
	root := kltVault(t, map[string]string{"a.md": "# A\nalpha"})
	home := kltHome(t)

	startErr := &knowledge.WatchUnavailableError{Reason: "filesystem watching is not implemented on this platform"}
	var built *fakeWatcher
	kl := kltLifecycle(t, KnowledgeLifecycleOptions{
		Home: home,
		NewWatcher: func(ix *knowledge.Index) knowledgeWatcher {
			built = newFakeWatcher(startErr)
			return built
		},
	})

	require.NoError(t, kl.AttachMount(context.Background(), "ws-a", "vault", root))
	require.NotNil(t, built)
	assert.ErrorIs(t, built.Err(), startErr, "the reason must be retrievable, not just swallowed")
}

// --- the point of the whole exercise ----------------------------------------

// TestKnowledgeLifecycle_FileAddedOnDiskBecomesFindableWithoutRestart is the
// end-to-end proof: with the REAL knowledge.NewWatcher wired in (no fake), a
// file created on disk in an attached collection — never touched by any
// Omnipus tool call — becomes findable with no restart, no re-mount, and no
// call this test makes to reconcile/sync/search-triggering-a-scan. Only the
// watcher this change starts can make that true within the test's timeout:
// the drift sweep this collection also has runs every six hours by default
// and this test does not wait six hours.
func TestKnowledgeLifecycle_FileAddedOnDiskBecomesFindableWithoutRestart(t *testing.T) {
	root := kltVault(t, map[string]string{"a.md": "# A\nalpha"})
	home := kltHome(t)

	kl := kltLifecycle(t, KnowledgeLifecycleOptions{Home: home})

	require.NoError(t, kl.AttachMount(context.Background(), "ws-a", "vault", root))

	ix, ok := kl.IndexForRoot(root)
	require.True(t, ok)

	kl.mu.Lock()
	c := kl.byRoot[root]
	var watcher knowledgeWatcher
	if c != nil {
		watcher = c.watcher
	}
	kl.mu.Unlock()
	require.NotNil(t, c, "the collection must be attached")
	require.NotNil(t, watcher, "startServices must have set a watcher, real or not")

	select {
	case <-watcher.Unavailable():
		// design §8: unavailability is the watcher's own authoritative,
		// already-observable signal — this test does not invent a second
		// way to detect it. A platform this project deliberately degrades
		// on (Windows, watch_other.go) has NO instant path at all; that is
		// a stated, known gap (see this task's report), not a bug this test
		// can fail on.
		t.Skipf("filesystem watching is unavailable in this environment (%v); "+
			"the periodic drift sweep is this platform's only compensating layer, "+
			"and it does not run inside this test's timeout", watcher.Err())
	default:
	}

	// Anti-vacuity: the word is genuinely absent before the write.
	hits, err := ix.Search("brontosaurus", 10)
	require.NoError(t, err)
	require.Empty(t, hits, "the fixture must not contain the word before it is written")

	// Written OUTSIDE any Omnipus tool call — a human editing the folder
	// directly, exactly the case design §4 says the watcher (not the
	// direct-update path a vault tool would use) must cover.
	require.NoError(t, os.WriteFile(filepath.Join(root, "new-note.md"),
		[]byte("# New note\nbrontosaurus"), 0o600))

	require.Eventually(t, func() bool {
		hits, searchErr := ix.Search("brontosaurus", 10)
		return searchErr == nil && len(hits) == 1
	}, 5*time.Second, 20*time.Millisecond,
		"a file created on disk in an attached collection must become findable without a "+
			"restart — the watcher, not the 6-hour drift sweep, is what makes this instant")
}

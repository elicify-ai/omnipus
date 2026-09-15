package gateway

// U-58 follow-ups (2026-09-15): the release of a demoted knowledge base must
// not depend on WHICH door removed the marker. bash `rm`, Finder and a
// whole-folder delete leave the marker gone with no Library verb involved;
// the sweep must notice and release. The attach race (marker deleted between
// acquire and startServices) must end with nothing running. The drift
// schedule stopping is observed directly through HealthChecker.Runs().

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/knowledge"
	"github.com/elicify-ai/omnipus/pkg/records"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// releaseSweepFixture is attachedVaultLifecycle (rest_library_vault_marker_
// lifecycle_test.go) with a FAST drift schedule, so Runs() moves on a test
// timescale and a stopped schedule is directly observable.
func releaseSweepFixture(t *testing.T) (ws, vault, vaultReal string, kl *KnowledgeLifecycle, watchers func() []*fakeWatcher) {
	t.Helper()
	api, ws, vault := buildReservedVault(t)
	var err error
	vaultReal, err = filepath.EvalSymlinks(vault)
	require.NoError(t, err)

	var mu sync.Mutex
	var built []*fakeWatcher
	kl = kltLifecycle(t, KnowledgeLifecycleOptions{
		Home:          api.homePath,
		DriftInterval: 40 * time.Millisecond,
		NewWatcher: func(*knowledge.Index) knowledgeWatcher {
			w := newFakeWatcher(nil)
			mu.Lock()
			built = append(built, w)
			mu.Unlock()
			return w
		},
	})
	registerKnowledgeLifecycle(api.homePath, kl)
	t.Cleanup(func() { unregisterKnowledgeLifecycle(api.homePath) })

	require.NoError(t, kl.AttachCollection(context.Background(), ws, vault))
	require.Contains(t, kl.AttachedRoots(), vaultReal)
	watchers = func() []*fakeWatcher {
		mu.Lock()
		defer mu.Unlock()
		return append([]*fakeWatcher(nil), built...)
	}
	require.Len(t, watchers(), 1)
	return ws, vault, vaultReal, kl, watchers
}

// markerRemovedByBashIsReleasedByTheSweep: os.RemoveAll stands in for bash —
// no Library verb runs. The next workspace sweep must release the collection,
// stop its watcher and close its index.
func TestReleaseSweep_MarkerRemovedOutsideTheLibrary(t *testing.T) {
	ws, vault, vaultReal, kl, watchers := releaseSweepFixture(t)
	ix, ok := kl.IndexForRoot(vault)
	require.True(t, ok)

	// bash: every marker goes at once, no product code involved.
	demoteOnDisk(t, vault)

	// The sweep is the only trigger — no REST door is called.
	kl.AttachWorkspace(ws)
	require.NotContains(t, kl.AttachedRoots(), vaultReal,
		"a marker removed outside the Library must not keep the index attached until restart")
	require.Zero(t, kl.HoldersFor(vault))
	_, stops := watchers()[0].counts()
	require.Equal(t, 1, stops, "the watcher must be stopped")
	_, err := ix.DocCount()
	require.Error(t, err, "the index handle must be closed")
}

// wholeFolderRemovedIsReleased: deleting the entire collection folder leaves
// a root that no longer resolves; the release must still happen.
func TestReleaseSweep_WholeFolderRemoved(t *testing.T) {
	ws, vault, vaultReal, kl, watchers := releaseSweepFixture(t)
	ix, ok := kl.IndexForRoot(vault)
	require.True(t, ok)

	require.NoError(t, os.RemoveAll(vault))

	kl.AttachWorkspace(ws)
	require.NotContains(t, kl.AttachedRoots(), vaultReal,
		"a deleted collection folder must not keep a phantom attachment")
	require.Zero(t, kl.HoldersFor(vault))
	_, stops := watchers()[0].counts()
	require.Equal(t, 1, stops)
	_, err := ix.DocCount()
	require.Error(t, err)
}

// attachRace: the marker is gone BEFORE the attach runs; the post-attach
// demotion check releases what the attach just started, leaving nothing.
func TestReleaseSweep_AttachOfADemotedFolderReleasesImmediately(t *testing.T) {
	ws, vault, vaultReal, kl, watchers := releaseSweepFixture(t)

	// Demote while attached, WITHOUT any sweep, so the attachment is stale.
	demoteOnDisk(t, vault)

	// A fresh attach of the same root (the sweep path for a still-listed
	// folder): acquire joins the existing attachment (not fresh) — so also
	// drive the async path, and then the release sweep.
	require.NoError(t, kl.AttachCollection(context.Background(), ws, vault))
	kl.AttachWorkspace(ws)

	require.NotContains(t, kl.AttachedRoots(), vaultReal,
		"the post-attach demotion check must release a folder that stopped being a knowledge base")
	_, stops := watchers()[0].counts()
	require.Equal(t, 1, stops)
}

// driftScheduleStops: after the release, HealthChecker.Runs() stops moving —
// the schedule for that root is gone, observed directly.
func TestReleaseSweep_DriftScheduleStops(t *testing.T) {
	ws, vault, _, kl, _ := releaseSweepFixture(t)

	// The schedule runs every 40ms; wait until it has moved at least once.
	deadline := time.Now().Add(3 * time.Second)
	for kl.health.Runs() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	require.NotZero(t, kl.health.Runs(), "precondition: the drift schedule is running")
	before := kl.health.Runs()

	demoteOnDisk(t, vault)
	kl.AttachWorkspace(ws)
	require.Zero(t, kl.HoldersFor(vault))

	// The schedule would have fired ~6 more times in 250ms at 40ms cadence.
	time.Sleep(250 * time.Millisecond)
	assert.Equal(t, before, kl.health.Runs(),
		"after release no further drift run may fire for the released root")
}

// releaseIsIdempotentAndConcurrent: two sweeps and a concurrent release leave
// exactly one stop and no double-close panic.
func TestReleaseSweep_IdempotentAndConcurrent(t *testing.T) {
	ws, vault, vaultReal, kl, watchers := releaseSweepFixture(t)
	ix, ok := kl.IndexForRoot(vault)
	require.True(t, ok)

	demoteOnDisk(t, vault)

	done := make(chan struct{}, 3)
	for i := 0; i < 2; i++ {
		go func() { defer func() { done <- struct{}{} }(); kl.ReleaseStaleCollections() }()
	}
	go func() { defer func() { done <- struct{}{} }(); kl.AttachWorkspace(ws) }()
	for i := 0; i < 3; i++ {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent release deadlocked")
		}
	}

	require.NotContains(t, kl.AttachedRoots(), vaultReal)
	_, stops := watchers()[0].counts()
	require.Equal(t, 1, stops, "the watcher is stopped exactly once")
	_, err := ix.DocCount()
	require.Error(t, err)
}

// isKBOnDisk checks marker presence directly, independent of the API.
func isKBOnDisk(t *testing.T, vault string) bool {
	t.Helper()
	isKB, _ := knowledge.IsKnowledgeBase(vault)
	return isKB
}

// demoteOnDisk removes EVERY knowledge-base marker from the folder the way
// bash would — the fixture builds both .omnipus-vault and .obsidian, and the
// folder only stops being a knowledge base when the last marker goes.
func demoteOnDisk(t *testing.T, vault string) {
	t.Helper()
	for _, m := range []string{records.VaultMarkerDirName, ".obsidian"} {
		require.NoError(t, os.RemoveAll(filepath.Join(vault, m)))
	}
	require.False(t, isKBOnDisk(t, vault), "precondition: the folder is demoted")
}

package session

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// Oracles: chief I5 (missing/invalid identities and failed-write publication)
// and optional I4 (cached ineligible sessions need no disk read). These are
// real stores on real temporary directories. Only the existing filesystem
// write seam is faulted, scoped to this test's meta.json and restored at once.
// Mutation proof and a full CHECK verdict belong to the independent checker.

func TestOwnerWiringI5_AdoptionMissingSessionPreservesNotExistError(t *testing.T) {
	store := newTestStore(t)
	adopted, err := store.AdoptMainSessionOwner("session-owner-not-found", "alice")
	require.Equal(t, false, adopted, "a nonexistent main cannot report adoption")
	require.ErrorIs(t, err, fs.ErrNotExist, "callers must be able to classify a missing identity with errors.Is")
}

func TestOwnerWiringI5_AdoptionRejectsInvalidSessionIDs(t *testing.T) {
	for _, id := range []string{"", "../outside", "bad/id", "bad\\id", "."} {
		t.Run(id, func(t *testing.T) {
			store := newTestStore(t)
			adopted, err := store.AdoptMainSessionOwner(id, "alice")
			require.Equal(t, false, adopted, "an invalid ID cannot report adoption")
			require.Error(t, err, "I5 requires invalid IDs to return an error; no exact error string is specified")
		})
	}
}

func TestOwnerWiringI5_FailedWriteDoesNotPublishOwner(t *testing.T) {
	store := newTestStore(t)
	main, err := store.GetOrCreateMainSession("owner-workspace", "mia")
	require.NoError(t, err)
	require.Equal(t, "", main.Owner)
	before, err := store.GetMeta(main.ID)
	require.NoError(t, err, "populate the ownerless cache before the fault")
	metaPath := filepath.Join(store.BaseDir(), main.ID, "meta.json")
	beforeBytes, err := os.ReadFile(metaPath)
	require.NoError(t, err)
	injected := errors.New("owner-wiring injected identity write failure")
	originalWrite := writeFileAtomicFn
	t.Cleanup(func() { writeFileAtomicFn = originalWrite })
	failedWrites := 0
	writeFileAtomicFn = func(path string, data []byte, perm os.FileMode) error {
		if path == metaPath {
			failedWrites++
			return injected
		}
		return originalWrite(path, data, perm)
	}

	adopted, adoptErr := store.AdoptMainSessionOwner(main.ID, "alice")
	writeFileAtomicFn = originalWrite // Restore the process edge before assertions/readback/cleanup.
	require.Equal(t, 1, failedWrites, "the real adoption reached exactly the injected write boundary")
	require.Equal(t, false, adopted, "an unpersisted owner must never report success")
	require.ErrorIs(t, adoptErr, injected, "the original write error must remain classifiable")
	cached, err := store.GetMeta(main.ID)
	require.NoError(t, err)
	require.Equal(t, before, cached, "failed persistence must not publish any attempted owner into the cache")
	require.Equal(t, "", mainOwnerReopenMeta(t, store, main.ID).Owner, "disk must agree with the unchanged cache")
	afterBytes, err := os.ReadFile(metaPath)
	require.NoError(t, err)
	require.Equal(t, beforeBytes, afterBytes, "the failed write leaves the original identity intact")

	adopted, err = store.AdoptMainSessionOwner(main.ID, "alice")
	require.NoError(t, err, "a real retry after restoring persistence can still adopt")
	require.Equal(t, true, adopted, "a failed attempt did not poison the compare-and-set state")
	require.Equal(t, "alice", mainOwnerReopenMeta(t, store, main.ID).Owner)
}

func TestOwnerWiringI4_CachedIneligibleSessionIgnoresDeletedIdentityFile(t *testing.T) {
	for _, main := range []bool{true, false} {
		name := "cached_non_main"
		if main {
			name = "cached_owned_main"
		}
		t.Run(name, func(t *testing.T) {
			store := newTestStore(t)
			var meta *UnifiedMeta
			var err error
			if main {
				meta, err = store.GetOrCreateMainSessionOwned("owner-workspace", "mia", "alice")
			} else {
				meta, err = store.NewSession(SessionTypeChat, "webchat", "mia")
			}
			require.NoError(t, err)
			cached, err := store.GetMeta(meta.ID)
			require.NoError(t, err, "the ineligible identity must already be in the real cache")
			metaPath := filepath.Join(store.BaseDir(), meta.ID, "meta.json")
			require.NoError(t, os.Remove(metaPath), "delete only the backing identity after caching")
			_, statErr := os.Stat(metaPath)
			require.ErrorIs(t, statErr, fs.ErrNotExist, "instrument control: the disk identity really is missing")

			adopted, adoptErr := store.AdoptMainSessionOwner(meta.ID, "bob")
			require.NoError(t, adoptErr, "cached owned/non-main sessions must return without requiring disk")
			require.Equal(t, false, adopted)
			got, getErr := store.GetMeta(meta.ID)
			require.NoError(t, getErr)
			require.Equal(t, cached, got, "ineligible cached metadata remains untouched")
			_, statErr = os.Stat(metaPath)
			require.ErrorIs(t, statErr, fs.ErrNotExist, "the no-op did not recreate the missing file")
		})
	}
}

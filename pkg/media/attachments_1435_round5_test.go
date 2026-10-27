package media

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// Issue #1435 Round 5 item 2 (silent-failure hunter R4-SF-2, HIGH) and item 3
// (independent CHECK survivor M4).
//
// Item 2 — a stat failure that is NOT a confirmed absence must be SURFACED.
// The resolver verifies the stored file before it hands the path back. When
// that verification fails for any reason other than "the file is gone", the
// resolver must report the failure to the caller — it must not report a
// successful resolution of a path it could not verify. The error must be
// classifiable by the caller: distinct from the routine "the attachment is
// unavailable" cause (ErrNotFound), so a lost attachment and a broken
// filesystem are never confused with each other.
//
// Item 3 — the CONVERSE guard: a pinned mapping is released ONLY on a
// confirmed absence (fs.ErrNotExist). A temporary storage fault must leave
// the pin intact, so the user's accepted attachment survives the fault.
// This is the guard the independent CHECK's mutant M4 (release on ANY stat
// error) needs: that mutant loses a live user's attachment, and today
// nothing in the committed suite kills it.

// issue1435R5ArmNonAbsenceStatFault replaces the parent directory of path
// with a regular file, so the real filesystem rejects a stat of path with a
// "not a directory" error — a storage/access failure that is NOT confirmed
// deletion. It returns the restore function.
func issue1435R5ArmNonAbsenceStatFault(t *testing.T, path string) func() {
	t.Helper()
	parent := filepath.Dir(path)
	backup := parent + "-issue1435-r5-temporarily-offline"
	require.NoError(t, os.Rename(parent, backup))
	require.NoError(t, os.WriteFile(parent, []byte("temporary filesystem obstruction"), 0o600))
	armed := true
	restore := func() {
		if !armed {
			return
		}
		armed = false
		require.NoError(t, os.Remove(parent))
		require.NoError(t, os.Rename(backup, parent))
	}
	t.Cleanup(restore)
	// Instrument: prove the fault is a REAL non-absence failure, so a green
	// run cannot be explained by the fault never having been armed.
	_, statErr := os.Stat(path)
	var pathErr *os.PathError
	require.ErrorAs(t, statErr, &pathErr, "instrument: the real filesystem must reject this stat")
	require.False(t, errors.Is(statErr, fs.ErrNotExist), "instrument: this is a storage/access failure, NOT confirmed deletion")
	t.Logf("real non-absence stat fault: %v", statErr)
	return restore
}

// issue1435R5StorePinnedFile writes a real accepted attachment and registers
// it under the given scope, exactly as an accepted upload is registered.
func issue1435R5StorePinnedFile(t *testing.T, scope string) (*FileMediaStore, string, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	parent := filepath.Join(home, "uploads", "issue1435-r5-session")
	require.NoError(t, os.MkdirAll(parent, 0o700))
	path := filepath.Join(parent, "accepted.txt")
	want := []byte("accepted conversation attachment: preserve these exact bytes")
	require.NoError(t, os.WriteFile(path, want, 0o600))
	// A bare store: no cleaner configuration, so CleanExpired can never run
	// for it (the cleaner is disabled for this scenario by construction).
	store := NewFileMediaStore()
	t.Cleanup(store.Stop)
	ref, err := store.Store(path, MediaMeta{
		Filename:      "accepted.txt",
		ContentType:   "text/plain",
		Source:        "upload:webchat",
		CleanupPolicy: CleanupPolicyForgetOnly,
	}, scope)
	require.NoError(t, err)
	return store, ref, path
}

// Item 2 — RED against 9a340568e: the resolver currently answers a
// non-absence stat failure with a SUCCESS-shaped result.
func TestIssue1435R5_NonAbsenceStatErrorIsSurfacedNotSwallowed(t *testing.T) {
	store, ref, path := issue1435R5StorePinnedFile(t, SessionUploadScopePrefix+"issue1435-r5-session")
	restore := issue1435R5ArmNonAbsenceStatFault(t, path)

	_, _, resolveErr := store.ResolveWithMeta(ref)
	require.Error(t, resolveErr,
		"#1435 R5 item 2: a stat failure that is not a confirmed absence must NOT be reported as a successful resolution")
	require.NotErrorIs(t, resolveErr, ErrNotFound,
		"#1435 R5 item 2: the caller must be able to tell a broken filesystem apart from 'the attachment is unavailable'")
	require.NotErrorIs(t, resolveErr, fs.ErrNotExist,
		"#1435 R5 item 2: the failure must not masquerade as a confirmed deletion either")

	// The pin must survive the fault: the bytes are still on disk, only the
	// lookup is temporarily impossible.
	indexedRef, indexed := store.RefByPath(path)
	require.True(t, indexed,
		"#1435 R5 item 2: a temporary storage fault must not release the pinned mapping")
	require.Equal(t, ref, indexedRef,
		"#1435 R5 item 2: the surviving mapping must be the SAME accepted ref")

	restore()
	resolvedPath, resolvedMeta, resolveErr := store.ResolveWithMeta(ref)
	require.NoError(t, resolveErr,
		"#1435 R5 item 2: once storage recovers the SAME accepted ref must resolve again")
	require.Equal(t, path, resolvedPath)
	require.Equal(t, "accepted.txt", resolvedMeta.Filename)
}

// Item 3 — the guard the CHECK's mutant M4 needs. It is GREEN on the current
// code (the pin survives) and must FAIL when the resolver releases the pin on
// ANY stat error.
func TestIssue1435R5_DeadPinReleasedOnlyOnConfirmedAbsence(t *testing.T) {
	t.Run("non_absence_stat_fault_keeps_the_pin", func(t *testing.T) {
		store, ref, path := issue1435R5StorePinnedFile(t, SessionUploadScopePrefix+"issue1435-r5-session")
		restore := issue1435R5ArmNonAbsenceStatFault(t, path)

		// The resolve runs WHILE the fault is active — the round-3 regression
		// restored access first, so it never exercised this branch.
		_, _, _ = store.ResolveWithMeta(ref)

		indexedRef, indexed := store.RefByPath(path)
		require.True(t, indexed,
			"#1435 R5 item 3: a temporary stat fault must not release a pinned ref whose bytes are still present")
		require.Equal(t, ref, indexedRef)

		restore()
		resolvedPath, resolveErr := store.Resolve(ref)
		require.NoError(t, resolveErr,
			"#1435 R5 item 3: restoring storage access must leave the SAME accepted ref resolvable")
		require.Equal(t, path, resolvedPath)
	})

	t.Run("confirmed_absence_releases_the_pin", func(t *testing.T) {
		store, ref, path := issue1435R5StorePinnedFile(t, SessionInlineScopePrefix+"issue1435-r5-session")
		require.NoError(t, os.Remove(path), "instrument: the stored file must really be gone")

		_, resolveErr := store.Resolve(ref)
		require.ErrorIs(t, resolveErr, ErrNotFound,
			"#1435 R5 item 3: a confirmed absence is the routine 'attachment unavailable' cause")
		_, indexed := store.RefByPath(path)
		require.False(t, indexed,
			"#1435 R5 item 3: a confirmed absence MUST release the dead mapping — the guard is two-sided")
	})
}

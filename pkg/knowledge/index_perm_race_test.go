// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// vanishedEntry is a fs.DirEntry whose Info() reports the file is gone — the
// state a scorch segment is in when the background merger deleted it between
// WalkDir's ReadDir and our lazy lstat. Faked rather than raced, because the
// real race cannot be triggered on demand: it appeared once in a full-package
// run and not in 10+ isolated runs of the same test, including under load and
// -count=4. A test that waited for it would be flaky in the useless direction.
type vanishedEntry struct{ name string }

func (v vanishedEntry) Name() string      { return v.name }
func (v vanishedEntry) IsDir() bool       { return false }
func (v vanishedEntry) Type() fs.FileMode { return 0 }
func (v vanishedEntry) Info() (fs.FileInfo, error) {
	return nil, &fs.PathError{Op: "lstat", Path: v.name, Err: fs.ErrNotExist}
}

// realEntry wraps a file that IS present, so the positive half of the contract
// is asserted by the same test rather than assumed.
type realEntry struct{ fi fs.FileInfo }

func (r realEntry) Name() string               { return r.fi.Name() }
func (r realEntry) IsDir() bool                { return r.fi.IsDir() }
func (r realEntry) Type() fs.FileMode          { return r.fi.Mode().Type() }
func (r realEntry) Info() (fs.FileInfo, error) { return r.fi, nil }

func TestEnforceEntryPermissions_VanishedSegmentIsNotAnError(t *testing.T) {
	// 1. The walk itself could not stat an entry it had enumerated.
	require.NoError(t,
		enforceEntryPermissions("000000000005.zap", nil,
			&fs.PathError{Op: "lstat", Path: "000000000005.zap", Err: fs.ErrNotExist}),
		"a segment the merger deleted must not fail the whole Sync")

	// 2. DirEntry.Info() lstats lazily and finds the file gone.
	require.NoError(t,
		enforceEntryPermissions("000000000005.zap", vanishedEntry{name: "000000000005.zap"}, nil),
		"a lazily-lstat'ed entry that vanished must not fail the whole Sync")

	// 3. Gone between Info() and Chmod: a real file, stat'ed, then deleted.
	dir := t.TempDir()
	path := filepath.Join(dir, "000000000006.zap")
	require.NoError(t, os.WriteFile(path, []byte("x"), 0o644)) // wrong perms on purpose
	fi, err := os.Lstat(path)
	require.NoError(t, err)
	require.NoError(t, os.Remove(path))
	require.NoError(t, enforceEntryPermissions(path, realEntry{fi: fi}, nil),
		"a file deleted between Info and Chmod must not fail the whole Sync")
}

// The other half: tolerating ENOENT must not have turned the permission
// enforcement itself off. Without this, deleting the chmod entirely would
// still leave the test above green.
func TestEnforceEntryPermissions_StillEnforcesOnFilesThatExist(t *testing.T) {
	dir := t.TempDir()

	// bleve's index_meta.json is created 0666 — the exact case FR-032 exists
	// for, since the index holds the full text of every note.
	path := filepath.Join(dir, "index_meta.json")
	require.NoError(t, os.WriteFile(path, []byte("{}"), 0o666))
	fi, err := os.Lstat(path)
	require.NoError(t, err)
	require.NoError(t, enforceEntryPermissions(path, realEntry{fi: fi}, nil))

	after, err := os.Lstat(path)
	require.NoError(t, err)
	require.Equal(t, indexFileMode, after.Mode().Perm(),
		"a file that still exists must still be chmod'ed to 0600")

	sub := filepath.Join(dir, "store")
	require.NoError(t, os.Mkdir(sub, 0o777))
	dfi, err := os.Lstat(sub)
	require.NoError(t, err)
	require.NoError(t, enforceEntryPermissions(sub, realEntry{fi: dfi}, nil))

	dafter, err := os.Lstat(sub)
	require.NoError(t, err)
	require.Equal(t, indexDirMode, dafter.Mode().Perm(),
		"a directory that still exists must still be chmod'ed to 0700")
}

// A genuine error is still a genuine error: only ErrNotExist is tolerated.
func TestEnforceEntryPermissions_OtherErrorsStillPropagate(t *testing.T) {
	boom := &fs.PathError{Op: "lstat", Path: "x", Err: os.ErrPermission}
	require.Error(t, enforceEntryPermissions("x", nil, boom),
		"a permission error is not a vanished file and must still fail the walk")
}

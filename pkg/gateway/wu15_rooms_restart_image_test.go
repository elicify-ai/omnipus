package gateway

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// Freeze server-produced durable bytes while a scripted provider is blocked.
// After orderly test teardown drains every writer, restoring those same bytes
// emulates a process crash without inventing a private room/lifecycle format or
// letting a live goroutine outlive its test home. This is a filesystem-edge
// fixture, not a state-machine double. The directory is the disposable HOME.
//
// The walk races the gateway's LIVE atomic writers (the snapshot is taken
// mid-test by design — the provider is blocked, but the room ledger and
// session stores still flush). Every such write is a temp file that is
// created and renamed away microseconds later: pkg/fileutil's
// os.CreateTemp(dir, ".tmp-*") pattern (WriteFileAtomic and atomic_sync_dir),
// which is also what the rooms git-evidence marker write inside .git uses.
// wu15TransientArtifact below names exactly that population, and ONLY it: a
// vanished entry whose name matches is skipped, and any other walk error —
// including a vanished entry with any other name — fails the snapshot
// loudly. (CHECK 1 finding F3, 2026-11-08: the previous blanket
// os.ErrNotExist tolerance here would also have swallowed a DURABLE record
// that vanished mid-walk — a rotated transcript renamed current→dated while
// only the old name was listed — and restored a home that had lost the
// record at every name. A vanished durable file is a defect to fail on,
// never a transient to skip.)
func wu15DurableCrashImage(t *testing.T, home string) func() {
	t.Helper()
	files, modes, err := wu15snapshotDurableHome(home)
	require.NoError(t, err, "SETUP: snapshot actual durable home bytes")
	require.NotEmpty(t, files, "instrument: durable image is not empty")
	return func() {
		t.Helper()
		require.NoError(t, os.RemoveAll(home))
		require.NoError(t, os.MkdirAll(home, 0o700))
		for rel, data := range files {
			path := filepath.Join(home, rel)
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
			require.NoError(t, os.WriteFile(path, data, modes[rel]))
		}
	}
}

// wu15snapshotDurableHome is wu15DurableCrashImage's walk, split out so the
// tolerance contract itself is testable: the conservation tests assert the
// walk FAILS on a vanished durable file and still tolerates a vanished
// .tmp-* artifact, without a require inside the walk turning that into a
// setup panic.
func wu15snapshotDurableHome(home string) (map[string][]byte, map[string]fs.FileMode, error) {
	files := map[string][]byte{}
	modes := map[string]fs.FileMode{}
	err := filepath.WalkDir(home, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, os.ErrNotExist) && wu15TransientArtifact(path) {
				return nil // known temporary artifact vanished mid-walk: not durable state
			}
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, statErr := entry.Info()
		if statErr != nil {
			if errors.Is(statErr, os.ErrNotExist) && wu15TransientArtifact(path) {
				return nil // enumerated, then renamed away before Info: same transient
			}
			return statErr
		}
		if !info.Mode().IsRegular() {
			// SETUP contract: the crash image snapshots regular files only.
			return fmt.Errorf("crash image fixture: %s is %s, want a regular file", path, info.Mode())
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			if errors.Is(readErr, os.ErrNotExist) && wu15TransientArtifact(path) {
				return nil // vanished between Info and ReadFile: same transient
			}
			return readErr
		}
		rel, relErr := filepath.Rel(home, path)
		if relErr != nil {
			return relErr
		}
		files[rel], modes[rel] = data, info.Mode().Perm()
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return files, modes, nil
}

// wu15TransientArtifact reports whether a vanished mid-walk path is one of
// the KNOWN temporary artifacts the live gateway's atomic writers create and
// rename away within microseconds: pkg/fileutil's os.CreateTemp(dir,
// ".tmp-*") — the one temp-naming pattern with code evidence under a live
// $OMNIPUS_HOME (WriteFileAtomic and atomic_sync_dir), used by the room
// ledger, the session stores, and the git-evidence marker writes inside
// .git alike. The pattern is matched on the base name at any depth. A
// vanished path with any other name is durable state that disappeared under
// the walk: that must fail the snapshot loudly (wu15snapshotDurableHome),
// because skipping it is how a rotated record would be silently lost.
func wu15TransientArtifact(path string) bool {
	ok, _ := filepath.Match(".tmp-*", filepath.Base(path))
	return ok
}

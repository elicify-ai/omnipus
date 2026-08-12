// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package library

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// buildMountedRoot builds a Root whose work tree contains one ordinary folder
// and one MOUNT pointing at a real directory outside the work tree — the shape
// the single-root Library could not express at all.
//
// It constructs Root directly rather than going through OpenRoot because
// OpenRoot loads mounts from the workspace store, and these tests are about the
// multi-root RESOLUTION rule, not about the store. The mount store has its own
// coverage in pkg/workspace.
func buildMountedRoot(t *testing.T) (r *Root, workDir, target string) {
	t.Helper()

	workDir = t.TempDir()
	target = t.TempDir()

	require.NoError(t, os.MkdirAll(filepath.Join(workDir, "drafts"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(workDir, "drafts", "note.md"), []byte("workspace file"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(target, "real.txt"), []byte("the operator's real file"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(target, "sub"), 0o700))

	// The mount as it exists on disk: a symlink inside work/ pointing out.
	require.NoError(t, os.Symlink(target, filepath.Join(workDir, "repo")))

	wr, err := os.OpenRoot(workDir)
	require.NoError(t, err)
	mr, err := os.OpenRoot(target)
	require.NoError(t, err)
	t.Cleanup(func() { _ = wr.Close(); _ = mr.Close() })

	return &Root{
		dir:  workDir,
		root: wr,
		mounts: map[string]*mountRoot{
			"repo": {root: mr, name: "repo", target: target},
		},
	}, workDir, target
}

// TestMountedFolder_IsBrowsable is the regression for the defect that blocked
// the entire mounts UI: a mount is a symlink out of the work tree, os.Root
// refuses to follow it, so the Library listed mounted folders and then errored
// on click with ErrOutsideRoot. Visible and unopenable.
func TestMountedFolder_IsBrowsable(t *testing.T) {
	r, _, _ := buildMountedRoot(t)

	fi, err := r.StatDir("repo")
	require.NoError(t, err, "a mounted folder must be openable; ErrOutsideRoot here is the pre-fix defect")
	assert.True(t, fi.IsDir())

	entries, err := r.List("repo", false)
	require.NoError(t, err)
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name)
	}
	assert.Contains(t, names, "real.txt", "listing a mount must show the real folder's contents")

	got, err := r.ReadContent("repo/real.txt")
	require.NoError(t, err)
	assert.Contains(t, got.Content, "the operator's real file")
}

// TestMountedFolder_ContainmentStillHolds is the other half, and the one that
// matters for the security review: opening a second root must not weaken the
// first. Escaping a MOUNT is still refused, exactly as escaping work/ is.
func TestMountedFolder_ContainmentStillHolds(t *testing.T) {
	r, _, target := buildMountedRoot(t)

	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	require.NoError(t, os.WriteFile(secret, []byte("must stay unreachable"), 0o600))

	// A symlink INSIDE the mount pointing back out of it.
	require.NoError(t, os.Symlink(outside, filepath.Join(target, "escape")))

	_, err := r.StatDir("repo/escape")
	require.Error(t, err, "a symlink out of a MOUNT must be refused, exactly as one out of work/ is")
	assert.ErrorIs(t, err, ErrOutsideRoot)

	_, err = r.ReadContent("repo/escape/secret.txt")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrOutsideRoot)

	// And the lexical route out is still closed.
	_, err = r.List("repo/../..", false)
	assert.Error(t, err, "walking up out of a mount must not reach the filesystem above it")
}

// TestDelete_RefusesTheMountsOwnEntry is a DATA-LOSS regression.
//
// "repo" resolves to the mount's own root at ".", so an unguarded Delete would
// call RemoveAll(".") against it — recursively emptying the operator's real
// folder. Revoking a mount is a different operation that deletes nothing.
func TestDelete_RefusesTheMountsOwnEntry(t *testing.T) {
	r, _, target := buildMountedRoot(t)

	err := r.Delete("repo")
	require.Error(t, err, "deleting a mount's own entry must be refused, not performed")
	assert.ErrorIs(t, err, ErrIsMountRoot)

	// The operator's real files are untouched — the assertion the guard exists for.
	_, statErr := os.Stat(filepath.Join(target, "real.txt"))
	require.NoError(t, statErr, "the operator's real file must survive a delete aimed at the mount")

	// Deleting something INSIDE the mount is legitimate and still works.
	require.NoError(t, r.Delete("repo/sub"))
	_, statErr = os.Stat(filepath.Join(target, "sub"))
	assert.True(t, os.IsNotExist(statErr))
}

// TestRename_RefusesCrossRootAndMountRoot pins both rename guards. A rename
// between roots is not expressible with os.Root, and must fail with a reason
// that says so rather than a containment error from an arbitrary side.
func TestRename_RefusesCrossRootAndMountRoot(t *testing.T) {
	r, _, target := buildMountedRoot(t)

	_, err := r.Rename("drafts/note.md", "repo/note.md")
	require.Error(t, err, "work tree -> mount is a cross-root move")
	assert.ErrorIs(t, err, ErrCrossRootTransfer)

	_, err = r.Rename("repo/real.txt", "drafts/real.txt")
	require.Error(t, err, "mount -> work tree is a cross-root move")
	assert.ErrorIs(t, err, ErrCrossRootTransfer)

	_, err = r.Rename("repo", "renamed")
	require.Error(t, err, "renaming the mount's own entry desynchronises it from the mount record")
	assert.ErrorIs(t, err, ErrIsMountRoot)

	// Both sources still exist: a refused rename must not have moved anything.
	_, statErr := os.Stat(filepath.Join(target, "real.txt"))
	assert.NoError(t, statErr)

	// A rename WITHIN the mount is same-root and must still work.
	_, err = r.Rename("repo/real.txt", "repo/renamed.txt")
	require.NoError(t, err)
	_, statErr = os.Stat(filepath.Join(target, "renamed.txt"))
	assert.NoError(t, statErr, "an in-mount rename must land on the operator's real disk")
}

// TestWriteAndMkdir_LandOnTheRealFolder proves the mount is genuinely writable
// rather than merely browsable — a read-only mount would be a silent
// half-feature, since the whole point of a mount is write access.
func TestWriteAndMkdir_LandOnTheRealFolder(t *testing.T) {
	r, workDir, target := buildMountedRoot(t)

	_, err := r.WriteContent("repo/created.txt", []byte("written through the mount"))
	require.NoError(t, err)
	onDisk, readErr := os.ReadFile(filepath.Join(target, "created.txt"))
	require.NoError(t, readErr, "a write inside a mount must reach the operator's real folder")
	assert.Equal(t, "written through the mount", string(onDisk))

	_, _, err = r.Mkdir("repo/newdir")
	require.NoError(t, err)
	fi, statErr := os.Stat(filepath.Join(target, "newdir"))
	require.NoError(t, statErr)
	assert.True(t, fi.IsDir())

	// Nothing leaked into the work tree under the mount's name.
	_, statErr = os.Stat(filepath.Join(workDir, "repo", "created.txt"))
	assert.NoError(t, statErr, "the symlink makes this the same file — sanity, not a second copy")
}

// TestHostPath_NamesTheRealDestination covers what the Transfer dialog needs:
// before a write lands on the operator's actual disk, the UI must be able to
// say WHERE. A wrong answer here is a confidently-worded lie in a confirmation.
func TestHostPath_NamesTheRealDestination(t *testing.T) {
	r, workDir, target := buildMountedRoot(t)

	assert.Equal(t, filepath.Join(target, "real.txt"), r.HostPath("repo/real.txt"))
	assert.Equal(t, target, r.HostPath("repo"))
	assert.Equal(t, filepath.Join(workDir, "drafts", "note.md"), r.HostPath("drafts/note.md"))
	assert.Equal(t, workDir, r.HostPath(""))

	name, tgt, broad, ok := r.MountAt("repo/anything/deeper")
	require.True(t, ok)
	assert.Equal(t, "repo", name)
	assert.Equal(t, target, tgt)
	assert.False(t, broad)

	//nolint:dogsled // only the ok flag is under test here: a work-tree path
	// belongs to no mount, so the other three returns have nothing to assert.
	_, _, _, ok = r.MountAt("drafts/note.md")
	assert.False(t, ok, "a work-tree path is not in any mount")
}

// TestResolve_OnlyTheFirstSegmentSelectsAMount guards the resolution rule
// itself. A nested directory that happens to share a mount's NAME must not be
// diverted to that mount — that would silently redirect writes to the
// operator's real disk from a path that never mentioned it.
func TestResolve_OnlyTheFirstSegmentSelectsAMount(t *testing.T) {
	r, workDir, _ := buildMountedRoot(t)

	require.NoError(t, os.MkdirAll(filepath.Join(workDir, "drafts", "repo"), 0o700))
	require.NoError(t, os.WriteFile(
		filepath.Join(workDir, "drafts", "repo", "inner.txt"), []byte("work tree, not the mount"), 0o600))

	got, err := r.ReadContent("drafts/repo/inner.txt")
	require.NoError(t, err)
	assert.Contains(t, got.Content, "work tree, not the mount",
		"a same-named directory deeper in the tree must stay in the work root")

	//nolint:dogsled // as above — only the ok flag carries the assertion.
	_, _, _, ok := r.MountAt("drafts/repo")
	assert.False(t, ok)
}

// TestMissingMountTarget_DoesNotBreakTheWorkspace covers spec FR-8.2: the
// operator's folder is theirs to move, rename, or keep on a volume that is not
// attached. A detached target must not take the whole Library offline.
func TestMissingMountTarget_DoesNotBreakTheWorkspace(t *testing.T) {
	workDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(workDir, "drafts"), 0o700))
	wr, err := os.OpenRoot(workDir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = wr.Close() })

	// openMountRoots skips an unopenable target, so the Root has no mount for it.
	r := &Root{dir: workDir, root: wr, mounts: openMountRoots(t.TempDir(), "no-such-workspace")}

	entries, listErr := r.List("", false)
	require.NoError(t, listErr, "an absent mount target must not break listing the work tree")
	assert.NotEmpty(t, entries)

	var outside *os.PathError
	_ = errors.As(listErr, &outside)
}

// TestMoveInto_CrossesTheMountBoundary is the Transfer dialog's core case:
// dragging a workspace file into a mounted folder writes it to the operator's
// real disk, and dragging one out brings it back into the workspace.
//
// Rename cannot express this (see TestRename_RefusesCrossRootAndMountRoot) —
// MoveInto detects the cross-root case and does copy-then-delete. The bug this
// guards against is subtle: MoveInto used to decide "same root?" by comparing
// the two *Root POINTERS, which is true for any same-workspace transfer. Once
// one Root holds several os.Roots that test says "same" for a move that is
// genuinely cross-root, so it delegated to Rename and a legitimate move failed.
func TestMoveInto_CrossesTheMountBoundary(t *testing.T) {
	r, workDir, target := buildMountedRoot(t)

	// work tree -> mount: lands on the operator's real disk.
	fi, err := MoveInto(r, r, "drafts/note.md", "repo/moved.md")
	require.NoError(t, err, "moving a workspace file into a mounted folder must work")
	assert.Equal(t, "moved.md", fi.Name())

	onDisk, readErr := os.ReadFile(filepath.Join(target, "moved.md"))
	require.NoError(t, readErr, "the file must exist in the operator's real folder")
	assert.Equal(t, "workspace file", string(onDisk))

	_, statErr := os.Stat(filepath.Join(workDir, "drafts", "note.md"))
	assert.True(t, os.IsNotExist(statErr), "a move must remove the source, not duplicate it")

	// mount -> work tree: the return journey.
	_, err = MoveInto(r, r, "repo/moved.md", "drafts/back.md")
	require.NoError(t, err)
	_, statErr = os.Stat(filepath.Join(workDir, "drafts", "back.md"))
	assert.NoError(t, statErr)
	_, statErr = os.Stat(filepath.Join(target, "moved.md"))
	assert.True(t, os.IsNotExist(statErr), "the operator's copy must be gone after moving it out")
}

// TestCopyInto_CrossesTheMountBoundary is the same boundary for copy, where the
// source must SURVIVE — the distinction from move that a shared code path makes
// easy to get wrong.
func TestCopyInto_CrossesTheMountBoundary(t *testing.T) {
	r, workDir, target := buildMountedRoot(t)

	_, err := CopyInto(r, r, "drafts/note.md", "repo/copied.md")
	require.NoError(t, err)

	onDisk, readErr := os.ReadFile(filepath.Join(target, "copied.md"))
	require.NoError(t, readErr)
	assert.Equal(t, "workspace file", string(onDisk))

	_, statErr := os.Stat(filepath.Join(workDir, "drafts", "note.md"))
	assert.NoError(t, statErr, "a copy must leave the source in place")
}

// TestCopyInto_RefusesTheMountsOwnEntry keeps the data-loss guard consistent
// across every verb that can write. Copying ONTO a mount's entry would write
// through into the operator's folder under a name they never chose.
func TestCopyInto_RefusesTheMountsOwnEntry(t *testing.T) {
	r, _, _ := buildMountedRoot(t)

	_, err := CopyInto(r, r, "drafts/note.md", "repo")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrIsMountRoot)

	_, err = CopyInto(r, r, "repo", "drafts/whole-mount")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrIsMountRoot)
}

// TestMoveInto_DirectoryCrossesTheBoundary covers the recursive path, which
// takes a different branch (copyDirRecursive) from the single-file case above.
func TestMoveInto_DirectoryCrossesTheBoundary(t *testing.T) {
	r, workDir, target := buildMountedRoot(t)

	require.NoError(t, os.MkdirAll(filepath.Join(workDir, "bundle", "nested"), 0o700))
	require.NoError(t, os.WriteFile(
		filepath.Join(workDir, "bundle", "nested", "deep.txt"), []byte("deep"), 0o600))

	_, err := MoveInto(r, r, "bundle", "repo/bundle")
	require.NoError(t, err, "moving a directory into a mount must copy the whole tree")

	got, readErr := os.ReadFile(filepath.Join(target, "bundle", "nested", "deep.txt"))
	require.NoError(t, readErr, "nested contents must arrive on the operator's real disk")
	assert.Equal(t, "deep", string(got))

	_, statErr := os.Stat(filepath.Join(workDir, "bundle"))
	assert.True(t, os.IsNotExist(statErr), "the source tree must be removed after a move")
}

// TestList_MarksMountsAndCorrectsTheirShape covers what the UI depends on to
// draw a mount differently from a folder, and one correction that is easy to
// miss.
//
// A mount is a SYMLINK, so ReadDir reports it with the symlink's own mode —
// IsDir false. Unannotated it would reach the client as a zero-byte FILE, and
// clicking it would try to read it rather than open it. It is a directory from
// every angle the user has.
func TestList_MarksMountsAndCorrectsTheirShape(t *testing.T) {
	r, _, target := buildMountedRoot(t)

	entries, err := r.List("", false)
	require.NoError(t, err)

	var mount, ordinary *struct {
		isDir bool
		size  int64
		mount bool
		host  string
		broad bool
	}
	for i := range entries {
		e := entries[i]
		got := &struct {
			isDir bool
			size  int64
			mount bool
			host  string
			broad bool
		}{isDir: e.IsDir, size: e.Size, mount: e.Mount != nil}
		if e.Mount != nil {
			got.host = e.Mount.HostPath
			got.broad = e.Mount.Broad
		}
		switch e.Name {
		case "repo":
			mount = got
		case "drafts":
			ordinary = got
		}
	}

	require.NotNil(t, mount, "the mount must appear in the work-tree listing")
	assert.True(t, mount.mount, "a mount must carry mount metadata so the UI can distinguish it")
	assert.True(t, mount.isDir, "a mount must be reported as a directory, not as the symlink it is made of")
	assert.Zero(t, mount.size, "a symlink's own byte length is meaningless beside a folder")
	assert.Equal(t, target, mount.host, "the real destination must be on the wire, not hidden")
	assert.False(t, mount.broad)

	require.NotNil(t, ordinary, "the ordinary folder must still be listed")
	assert.False(t, ordinary.mount, "an ordinary folder must NOT be marked as a mount")
	assert.True(t, ordinary.isDir)
}

// TestList_InsideAMount_EntriesAreNotMarked guards the other direction: only
// the mount's own entry is special. A file inside a mounted folder is an
// ordinary file, and marking it would make the UI draw a "mounted" badge on
// every row beneath the mount.
func TestList_InsideAMount_EntriesAreNotMarked(t *testing.T) {
	r, _, _ := buildMountedRoot(t)

	entries, err := r.List("repo", false)
	require.NoError(t, err)
	require.NotEmpty(t, entries)

	for _, e := range entries {
		assert.Nil(t, e.Mount,
			"entry %q is INSIDE a mount, not a mount itself — it must not be marked", e.Name)
	}
}

// TestList_BroadMountIsFlagged proves the warning survives to the wire for an
// ALREADY-STORED mount. Breadth is recomputed rather than persisted, so without
// this the badge would appear only in the create response — once, in the moment
// the operator is least likely to revisit it.
func TestList_BroadMountIsFlagged(t *testing.T) {
	r, _, target := buildMountedRoot(t)
	// Re-point the mount at a deliberately broad location.
	r.mounts["repo"].broad = true
	r.mounts["repo"].target = target

	entries, err := r.List("", false)
	require.NoError(t, err)

	for _, e := range entries {
		if e.Name == "repo" {
			require.NotNil(t, e.Mount)
			assert.True(t, e.Mount.Broad, "a broad grant must be flagged on every read, not only at creation")
			return
		}
	}
	t.Fatal("the mount was not listed")
}

// Omnipus — RED tests for spec "Library views, anywhere"
// (docs/internal/specs/library-views-anywhere-spec.md), §10 TDD Plan tests
// 33, 34, 35, 36, 37 and 38.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// TestDiscovery_SkipsControlDirsButIndexesDotFolders is TDD Plan test 33
// (US-9 AS-1, EC-2/EC-2a, MAJ-011/D-PARITY): views under .omnipus-vault,
// .obsidian, .git or .trash are never discovered; a view under a
// non-control dot-folder (.drafts) IS discovered.
//
// CHARACTERIZATION: WalkContained already implements exactly this rule,
// unchanged (F10/F17) — scanSkippedDirNames skips only the four named
// control directories at any depth, and its own doc comment states
// ordinary dotfiles/dot-folders are NOT skipped. Pinned as the discovery
// PRIMITIVE's own parity guarantee, which the future view-specific helper
// (§4 step 1) must inherit unmodified rather than re-decide.
func TestDiscovery_SkipsControlDirsButIndexesDotFolders(t *testing.T) {
	root := t.TempDir()
	for _, controlDir := range []string{".omnipus-vault", ".obsidian", ".git", ".trash"} {
		p := filepath.Join(root, controlDir, "leftover.view")
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte("name: leftover\n"), 0o644))
	}
	draftsPath := filepath.Join(root, ".drafts", "wip.view")
	require.NoError(t, os.MkdirAll(filepath.Dir(draftsPath), 0o755))
	require.NoError(t, os.WriteFile(draftsPath, []byte("name: wip\n"), 0o644))

	fsys := OSLinkFS()
	cr, err := NewCollectionRoot(fsys, root)
	require.NoError(t, err)
	res, err := WalkContained(fsys, cr)
	require.NoError(t, err)

	for _, controlDir := range []string{".omnipus-vault", ".obsidian", ".git", ".trash"} {
		for _, f := range res.Files {
			if f == controlDir+"/leftover.view" {
				t.Errorf("EC-2: %s must never be discovered inside the control directory %s", f, controlDir)
			}
		}
	}
	found := false
	for _, f := range res.Files {
		if f == ".drafts/wip.view" {
			found = true
		}
	}
	require.True(t, found, "EC-2a/D-PARITY: a view under a non-control dot-folder (.drafts) must be "+
		"discovered — found files: %v", res.Files)
}

// TestDiscovery_NeverFollowsMountSymlink is TDD Plan test 34 (US-9 AS-2,
// EC-1, MAJ-011/D-PARITY): a view inside a symlinked mount folder is never
// found by discovery.
//
// CHARACTERIZATION: WalkContained never follows a symlink at all (FR-044,
// unchanged) — this is the SAME guarantee test 71 (nested vault) and the
// existing contain_test.go suite already exercise for other content. It
// automatically extends to a `.view`-suffixed file with no view-specific
// code at all, which is exactly D-PARITY's point.
func TestDiscovery_NeverFollowsMountSymlink(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "vault")
	mountTarget := filepath.Join(parent, "mounted-elsewhere")
	require.NoError(t, os.MkdirAll(mountTarget, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(mountTarget, "mounted.view"), []byte("name: mounted\n"), 0o644))
	require.NoError(t, os.MkdirAll(root, 0o755))
	require.NoError(t, os.Symlink(mountTarget, filepath.Join(root, "mount")))

	fsys := OSLinkFS()
	cr, err := NewCollectionRoot(fsys, root)
	require.NoError(t, err)
	res, err := WalkContained(fsys, cr)
	require.NoError(t, err)

	for _, f := range res.Files {
		if f == "mount/mounted.view" {
			t.Fatalf("EC-1/US-9 AS-2: a view inside a symlinked mount folder must never be found by "+
				"discovery — found: %v", res.Files)
		}
	}
}

// TestWriteView_SwappedSymlinkReadRefused is TDD Plan test 35 (US-3 AS-4,
// MIN-001/D-SYMLINK-READ): a path discovery walked as an ordinary regular
// file, then swapped for a symlink before the subsequent read, must be
// refused — the symlink's target content must never be parsed.
//
// BLOCKED: the unified discovery-read helper this race requires (D-SYMLINK-
// READ, §4 step 1c) does not exist — there is no read operation for views
// outside pkg/records's separate, un-raced os.ReadFile in loadViewPaths, and
// no view-specific walk to race against in the first place (discovery-
// anywhere is absent, TDD tests 1-3's own finding).
func TestWriteView_SwappedSymlinkReadRefused(t *testing.T) {
	t.Fatal("BLOCKED: the pkg/knowledge discovery helper's unified walk-open-read step " +
		"(D-SYMLINK-READ, §4 step 1c) does not exist — required before a walked-regular-file-swapped-" +
		"for-a-symlink race can even be constructed against a real code path, per MIN-001 / TDD test 35.")
}

// TestDiscovery_RefusesOversizeViewFile is TDD Plan test 36 (US-3 AS-5,
// MIN-002/D-SIZECAP): a `.view` file over 256 KiB must report
// view_too_large and never be read past the cap.
//
// Today's loadViewPaths (pkg/records/view.go) has no size cap at all — it
// os.ReadFile's the whole file unconditionally. A large, otherwise
// well-formed view therefore loads SUCCESSFULLY today, rather than being
// capped/rejected.
func TestDiscovery_RefusesOversizeViewFile(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(records.ViewsDir(root), 0o755))

	// 256 KiB is D-SIZECAP's own stated floor; well past it so the assertion
	// cannot be defeated by an off-by-one in whatever cap eventually ships.
	padding := make([]byte, 300*1024)
	for i := range padding {
		padding[i] = '#'
	}
	body := "name: too-big\nlabel: \"" + string(padding) + "\"\n"
	require.NoError(t, os.WriteFile(filepath.Join(records.ViewsDir(root), "too-big.yaml"), []byte(body), 0o600))

	set, report, err := records.LoadViews(root, nil)
	require.NoError(t, err)

	tooLarge := false
	for _, rej := range report.Rejections {
		if string(rej.Code) == "view_too_large" {
			tooLarge = true
		}
	}
	_, loaded := set.Get("too-big")
	if !tooLarge {
		t.Fatalf(
			"MIN-002/D-SIZECAP: a .view file over 256 KiB must be reported view_too_large and never "+
				"read past the cap — got loaded=%v, rejections=%+v",
			loaded, report.Rejections,
		)
	}
}

// TestConcurrentLibrarySaveAndWriteView_ShareOneLock is TDD Plan test 37
// (US-3 AS-6, MIN-007/D-LOCK): a Library save and an agent write_view on
// the same file must serialize on an identical lock key.
//
// BLOCKED (test-infrastructure gap, not a missing spec symbol): F14 claims
// controlPlaneLockKey (pkg/knowledge) and resolveLibraryLock
// (pkg/gateway) already derive the same key for the same collection-
// relative path, but the two functions are unexported in different
// packages — proving their equality, or driving a real concurrent race
// between the Library save door and write_view, needs either a new
// exported seam or a cross-package integration harness that does not exist
// today. Per qa-lead's role limits (test files only, never production
// code), this test cannot add that seam itself.
func TestConcurrentLibrarySaveAndWriteView_ShareOneLock(t *testing.T) {
	t.Fatal("BLOCKED: proving controlPlaneLockKey (pkg/knowledge) and resolveLibraryLock " +
		"(pkg/gateway) derive an identical key for the same view path needs either a new exported " +
		"seam (production code this role must not add) or a cross-package integration harness that " +
		"does not exist today — required before MIN-007/D-LOCK's claim can be driven end to end, per " +
		"TDD test 37.")
}

// TestDiscovery_ReportsUnreadableSubfolder is TDD Plan test 38 (MAJ-009): a
// SkipUnreadable entry from WalkContained encountered during view discovery
// must be surfaced in the resulting view load report, never silently
// dropped.
//
// BLOCKED: records.LoadViews performs a single os.ReadDir of one fixed
// directory — it never calls WalkContained at all, so there is no
// SkipUnreadable concept anywhere in the view-loading path to surface.
func TestDiscovery_ReportsUnreadableSubfolder(t *testing.T) {
	t.Fatal("BLOCKED: records.LoadViews does not walk subfolders (no WalkContained integration) — " +
		"there is no SkipUnreadable entry produced anywhere in the view-loading path to surface in " +
		"the load report, per MAJ-009 / TDD test 38.")
}

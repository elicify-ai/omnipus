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

// --- CleanRelPath: structural (lexical) path-safety adversarial cases ---

func TestCleanRelPath_Adversarial(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantErr bool
		wantRel string
	}{
		{"empty is root", "", false, ""},
		{"dot is root", ".", false, ""},
		{"plain relative", "notes/report.md", false, "notes/report.md"},
		{"deeply nested", "a/b/c/d/e/f/g.txt", false, "a/b/c/d/e/f/g.txt"},
		{"trailing slash cleaned", "notes/", false, "notes"},
		{"redundant dot segments", "notes/./report.md", false, "notes/report.md"},
		{"leading dotdot", "../etc/passwd", true, ""},
		{"embedded dotdot", "notes/../../etc/passwd", true, ""},
		{"bare dotdot", "..", true, ""},
		{"absolute unix", "/etc/passwd", true, ""},
		{"absolute with subpath", "/notes/report.md", true, ""},
		{"backslash windows-style", "notes\\report.md", true, ""},
		{"embedded NUL", "notes/report\x00.md", true, ""},
		{"double slash collapses", "notes//report.md", false, "notes/report.md"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rel, err := CleanRelPath(tc.raw)
			if tc.wantErr {
				require.Error(t, err, "raw=%q", tc.raw)
				assert.ErrorIs(t, err, ErrInvalidPath)
				return
			}
			require.NoError(t, err, "raw=%q", tc.raw)
			assert.Equal(t, tc.wantRel, rel)
		})
	}
}

// buildTestRoot opens a *Root at a fresh temp directory standing in for a
// workspace's work/ tree (bypassing workspace.SafeWorkDir's ID validation,
// which is irrelevant to what these tests exercise).
func buildTestRoot(t *testing.T) (*Root, string) {
	t.Helper()
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	require.NoError(t, err)
	t.Cleanup(func() { root.Close() })
	return &Root{dir: dir, root: root}, dir
}

// --- Symlink escape: the adversarial case CleanRelPath CANNOT catch (it is
// lexically fine) — only os.Root's own runtime containment check can. ---

func TestRoot_SymlinkEscape_AbsoluteTarget_Rejected(t *testing.T) {
	r, dir := buildTestRoot(t)

	outsideDir := t.TempDir()
	secretPath := filepath.Join(outsideDir, "secret.txt")
	require.NoError(t, os.WriteFile(secretPath, []byte("top secret"), 0o600))

	// A symlink INSIDE the root pointing to an ABSOLUTE path OUTSIDE it.
	linkPath := filepath.Join(dir, "escape")
	require.NoError(t, os.Symlink(outsideDir, linkPath))

	_, err := r.StatDir("escape")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrOutsideRoot,
		"absolute-target symlink escape must be rejected as ErrOutsideRoot, got %v", err)

	_, err = r.ReadContent("escape/secret.txt")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrOutsideRoot)

	_, err = r.List("escape", false)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrOutsideRoot)
}

func TestRoot_SymlinkEscape_RelativeTarget_Rejected(t *testing.T) {
	r, dir := buildTestRoot(t)

	// A relative-target symlink that walks OUT of the root via "..".
	outsideDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outsideDir, "secret.txt"), []byte("shh"), 0o600))

	rel, err := filepath.Rel(dir, outsideDir)
	require.NoError(t, err)
	linkPath := filepath.Join(dir, "escape2")
	require.NoError(t, os.Symlink(rel, linkPath))

	_, err = r.StatDir("escape2")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrOutsideRoot, "relative-target symlink escape must be rejected, got %v", err)
}

func TestRoot_SymlinkEscape_ToSensitiveSiblingWorkspace_Rejected(t *testing.T) {
	// Simulates the highest-value real attack: a symlink inside workspace
	// A's work tree pointing at workspace B's directory (or the Omnipus home
	// itself, where master.key/credentials.json live).
	home := t.TempDir()
	workDirA := filepath.Join(home, "workspaces", "wsA", "work")
	require.NoError(t, os.MkdirAll(workDirA, 0o700))
	sensitiveDir := filepath.Join(home, "workspaces", "wsB")
	require.NoError(t, os.MkdirAll(sensitiveDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(sensitiveDir, "AGENT.md"), []byte("wsB secrets"), 0o600))

	root, err := os.OpenRoot(workDirA)
	require.NoError(t, err)
	defer root.Close()
	r := &Root{dir: workDirA, root: root}

	require.NoError(t, os.Symlink(sensitiveDir, filepath.Join(workDirA, "peek")))

	_, err = r.List("peek", true)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrOutsideRoot)
}

// --- Non-adversarial CRUD smoke coverage for the primitives the REST layer
// builds on (deeper HTTP-level coverage lives in
// pkg/gateway/rest_library_test.go). ---

func TestRoot_WriteReadContent_RoundTrip(t *testing.T) {
	r, _ := buildTestRoot(t)

	// WriteContent's contract requires the parent directory to already exist
	// (LibraryContentRequest's documented "parent directory must already
	// exist" — this package has no mkdir-a-new-directory operation of its
	// own, matching the contract's actual operation set).
	require.NoError(t, r.root.MkdirAll("notes", 0o700))

	fi, err := r.WriteContent("notes/report.md", []byte("# Report\n"))
	require.NoError(t, err)
	assert.False(t, fi.IsDir())

	result, err := r.ReadContent("notes/report.md")
	require.NoError(t, err)
	assert.True(t, result.IsText)
	assert.False(t, result.TooLarge)
	assert.Equal(t, "# Report\n", result.Content)
}

func TestRoot_WriteContent_MissingParent_NotFound(t *testing.T) {
	r, _ := buildTestRoot(t)
	_, err := r.WriteContent("nope/report.md", []byte("x"))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestRoot_ReadContent_Binary_NoNulLie(t *testing.T) {
	r, _ := buildTestRoot(t)
	require.NoError(t, r.root.WriteFile("blob.bin", []byte{0x00, 0x01, 0x02, 0xff, 0xfe}, 0o600))

	result, err := r.ReadContent("blob.bin")
	require.NoError(t, err)
	assert.False(t, result.IsText)
	assert.Equal(t, "", result.Content)
}

func TestRoot_Delete_DirectoryRemovesContents(t *testing.T) {
	r, dir := buildTestRoot(t)
	require.NoError(t, r.root.MkdirAll("sub", 0o700))
	_, err := r.WriteContent("sub/a.txt", []byte("a"))
	require.NoError(t, err)
	_, err = r.WriteContent("sub/b.txt", []byte("b"))
	require.NoError(t, err)

	require.NoError(t, r.Delete("sub"))
	_, statErr := os.Stat(filepath.Join(dir, "sub"))
	assert.True(t, os.IsNotExist(statErr))
}

func TestRoot_Rename_NoOpSameFromTo(t *testing.T) {
	r, _ := buildTestRoot(t)
	_, err := r.WriteContent("a.txt", []byte("a"))
	require.NoError(t, err)
	fi, err := r.Rename("a.txt", "a.txt")
	require.NoError(t, err)
	assert.Equal(t, "a.txt", fi.Name())
}

func TestRoot_Rename_DestinationExists_Conflict(t *testing.T) {
	r, _ := buildTestRoot(t)
	_, err := r.WriteContent("a.txt", []byte("a"))
	require.NoError(t, err)
	_, err = r.WriteContent("b.txt", []byte("b"))
	require.NoError(t, err)
	_, err = r.Rename("a.txt", "b.txt")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAlreadyExists)
}

func TestCopyMoveInto_CrossRoot(t *testing.T) {
	src, _ := buildTestRoot(t)
	dst, _ := buildTestRoot(t)
	_, err := src.WriteContent("doc.md", []byte("hello"))
	require.NoError(t, err)

	// Copy: source remains.
	_, err = CopyInto(src, dst, "doc.md", "doc-copy.md")
	require.NoError(t, err)
	result, err := dst.ReadContent("doc-copy.md")
	require.NoError(t, err)
	assert.Equal(t, "hello", result.Content)
	_, err = src.StatFile("doc.md")
	require.NoError(t, err, "copy must leave the source in place")

	// Move: source is gone afterward.
	_, err = MoveInto(src, dst, "doc.md", "doc-moved.md")
	require.NoError(t, err)
	_, err = src.StatFile("doc.md")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNotFound, "move must remove the source")
	result, err = dst.ReadContent("doc-moved.md")
	require.NoError(t, err)
	assert.Equal(t, "hello", result.Content)
}

func TestMoveInto_SameRoot_UsesRename(t *testing.T) {
	r, _ := buildTestRoot(t)
	_, err := r.WriteContent("a.txt", []byte("a"))
	require.NoError(t, err)
	_, err = MoveInto(r, r, "a.txt", "b.txt")
	require.NoError(t, err)
	_, err = r.StatFile("a.txt")
	assert.True(t, errors.Is(err, ErrNotFound))
	_, err = r.StatFile("b.txt")
	require.NoError(t, err)
}

func TestCopyInto_DirectoryRecursive(t *testing.T) {
	src, _ := buildTestRoot(t)
	dst, _ := buildTestRoot(t)
	require.NoError(t, src.root.MkdirAll("proj/nested", 0o700))
	_, err := src.WriteContent("proj/a.txt", []byte("a"))
	require.NoError(t, err)
	_, err = src.WriteContent("proj/nested/b.txt", []byte("b"))
	require.NoError(t, err)

	_, err = CopyInto(src, dst, "proj", "proj-copy")
	require.NoError(t, err)

	ra, err := dst.ReadContent("proj-copy/a.txt")
	require.NoError(t, err)
	assert.Equal(t, "a", ra.Content)
	rb, err := dst.ReadContent("proj-copy/nested/b.txt")
	require.NoError(t, err)
	assert.Equal(t, "b", rb.Content)
}

func TestList_HiddenFiltering(t *testing.T) {
	r, _ := buildTestRoot(t)
	_, err := r.WriteContent("visible.txt", []byte("v"))
	require.NoError(t, err)
	_, err = r.WriteContent(".hidden.txt", []byte("h"))
	require.NoError(t, err)

	entries, err := r.List("", false)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "visible.txt", entries[0].Name)

	entries, err = r.List("", true)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	var sawHidden bool
	for _, e := range entries {
		if e.Name == ".hidden.txt" {
			sawHidden = true
			assert.True(t, e.IsHidden)
		}
	}
	assert.True(t, sawHidden)
}

func TestCountVisibleRootEntries_AbsentWorkTree(t *testing.T) {
	home := t.TempDir()
	// No workspaces/<id>/work/ directory created at all.
	count, err := CountVisibleRootEntries(home, "ws-none")
	require.NoError(t, err)
	assert.Equal(t, 0, count)

	// Confirm it did NOT create the directory as a side effect.
	_, statErr := os.Stat(filepath.Join(home, "workspaces", "ws-none", "work"))
	assert.True(t, os.IsNotExist(statErr), "CountVisibleRootEntries must not mkdir the work tree")
}

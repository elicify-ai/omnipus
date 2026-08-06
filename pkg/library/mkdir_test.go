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

// --- Root.Mkdir (UAT Issue 4) ---

func TestMkdir_SingleLevel_Created(t *testing.T) {
	r, dir := buildTestRoot(t)
	fi, created, err := r.Mkdir("newfolder")
	require.NoError(t, err)
	assert.True(t, created)
	assert.True(t, fi.IsDir())
	assert.DirExists(t, filepath.Join(dir, "newfolder"))
}

func TestMkdir_NestedPath_CreatesAllIntermediates(t *testing.T) {
	r, dir := buildTestRoot(t)
	fi, created, err := r.Mkdir("a/b/c")
	require.NoError(t, err)
	assert.True(t, created)
	assert.True(t, fi.IsDir())
	assert.DirExists(t, filepath.Join(dir, "a", "b", "c"))
	assert.DirExists(t, filepath.Join(dir, "a", "b"))
	assert.DirExists(t, filepath.Join(dir, "a"))
}

func TestMkdir_AlreadyExistsAsDirectory_Idempotent(t *testing.T) {
	r, _ := buildTestRoot(t)
	_, created1, err := r.Mkdir("dir")
	require.NoError(t, err)
	assert.True(t, created1)

	fi, created2, err := r.Mkdir("dir")
	require.NoError(t, err, "re-creating an existing directory must be idempotent, not an error")
	assert.False(t, created2)
	assert.True(t, fi.IsDir())
}

func TestMkdir_AlreadyExistsAsFile_Conflict(t *testing.T) {
	r, _ := buildTestRoot(t)
	_, err := r.WriteContent("taken.txt", []byte("x"))
	require.NoError(t, err)

	_, created, err := r.Mkdir("taken.txt")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAlreadyExists)
	assert.False(t, created)
}

func TestMkdir_IntermediateComponentIsFile_NotDir(t *testing.T) {
	r, _ := buildTestRoot(t)
	_, err := r.WriteContent("notadir.txt", []byte("x"))
	require.NoError(t, err)

	_, _, err = r.Mkdir("notadir.txt/sub")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNotDir)
}

func TestMkdir_Root_InvalidPath(t *testing.T) {
	r, _ := buildTestRoot(t)
	_, _, err := r.Mkdir("")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidPath)
}

func TestMkdir_SymlinkEscape_Rejected(t *testing.T) {
	r, dir := buildTestRoot(t)
	outsideDir := t.TempDir()
	require.NoError(t, os.Symlink(outsideDir, filepath.Join(dir, "escape")))

	_, _, err := r.Mkdir("escape/newdir")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrOutsideRoot)
}

// --- CleanRelPath: UAT Issue 6 (".."-prefixed name is not a traversal, but
// silently classified as hidden and vanishes) ---

func TestCleanRelPath_DotDotPrefixedName_Rejected(t *testing.T) {
	cases := []string{
		"..dana-pwned-encoded.txt",
		"..%2fdana-pwned-encoded.txt",
		"folder/..sneaky.txt",
		"...triple-dot.txt",
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			_, err := CleanRelPath(raw)
			require.Error(t, err, "raw=%q", raw)
			assert.ErrorIs(t, err, ErrInvalidPath)
		})
	}
}

func TestCleanRelPath_SingleDotPrefixedName_StillAllowed(t *testing.T) {
	// A conventional dotfile (single leading dot) must remain allowed —
	// only a DOUBLE-dot prefix is rejected (UAT Issue 6 targets the
	// specific "..foo" shape, not ordinary hidden files like ".library").
	cases := []string{".env", ".library", ".gitignore", "folder/.hidden"}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			rel, err := CleanRelPath(raw)
			require.NoError(t, err, "raw=%q", raw)
			assert.Equal(t, raw, rel)
		})
	}
}

func TestRename_ToDotDotPrefixedName_Rejected(t *testing.T) {
	// End-to-end: the REST layer runs every "to"/"from" path through
	// CleanRelPath before it ever reaches Root.Rename, so this name is
	// rejected outright rather than succeeding and then silently vanishing
	// from the default (non-hidden) listing.
	_, err := CleanRelPath("..dana-pwned-encoded.txt")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidPath)
}

// --- requireParentDir / DestinationParentNotFoundError (UAT Issue 4) ---

func TestRename_MissingDestinationParent_NamesTheDirectory(t *testing.T) {
	r, _ := buildTestRoot(t)
	_, err := r.WriteContent("a.txt", []byte("a"))
	require.NoError(t, err)

	_, err = r.Rename("a.txt", "subfolder/a.txt")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNotFound, "must still satisfy the generic 404 sentinel")

	var destErr *DestinationParentNotFoundError
	require.ErrorAs(t, err, &destErr, "must be identifiable as a missing-destination-parent error")
	assert.Equal(t, "subfolder", destErr.Parent)
}

func TestCopyInto_MissingDestinationParent_NamesTheDirectory(t *testing.T) {
	src, _ := buildTestRoot(t)
	dst, _ := buildTestRoot(t)
	_, err := src.WriteContent("doc.md", []byte("hello"))
	require.NoError(t, err)

	_, err = CopyInto(src, dst, "doc.md", "deep/nested/doc.md")
	require.Error(t, err)
	var destErr *DestinationParentNotFoundError
	require.ErrorAs(t, err, &destErr)
	assert.Equal(t, "deep/nested", destErr.Parent)
}

// Once the missing directory is created via Mkdir, the identical Move that
// previously 404'd now succeeds — proving Mkdir actually closes the UAT-4
// gap end to end (nested Move destination "subfolder/test.txt").
func TestMkdirThenMove_NestedDestination_Succeeds(t *testing.T) {
	r, _ := buildTestRoot(t)
	_, err := r.WriteContent("test.txt", []byte("x"))
	require.NoError(t, err)

	_, err = r.Rename("test.txt", "subfolder/test.txt")
	require.Error(t, err, "must fail before the folder exists")

	_, _, err = r.Mkdir("subfolder")
	require.NoError(t, err)

	fi, err := r.Rename("test.txt", "subfolder/test.txt")
	require.NoError(t, err, "must succeed once the destination folder has been created")
	assert.Equal(t, "test.txt", fi.Name())

	_, statErr := r.StatFile("subfolder/test.txt")
	require.NoError(t, statErr)
}

func TestDestinationParentNotFoundError_ErrorMessage(t *testing.T) {
	err := &DestinationParentNotFoundError{Parent: "subfolder"}
	assert.Contains(t, err.Error(), "subfolder")
	assert.True(t, errors.Is(err, ErrNotFound))
}

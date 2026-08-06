// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// pathsafe_integration_test.go — regression tests for the app-wide
// cross-platform filename-safety fix (pkg/pathsafe): CleanRelPath now also
// rejects Windows reserved device names, NTFS-illegal characters, a
// trailing dot/space, and an over-long component or relative path — on
// every OS, not only Windows — and every collision-sensitive Root
// operation (Rename, CopyInto, CreateUnique, Mkdir, WriteContent) now
// detects a colliding sibling case-INsensitively, so the exact same
// behavior is produced whether the underlying host filesystem is
// case-sensitive (ext4) or not (NTFS, default APFS/HFS+).

package library

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- CleanRelPath: the new pathsafe-backed checks ---

func TestCleanRelPath_ReservedDeviceName_Rejected(t *testing.T) {
	cases := []string{"CON", "con", "nul.txt", "COM1.log", "notes/LPT1", "notes/con/report.txt"}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			_, err := CleanRelPath(raw)
			require.Error(t, err, "raw=%q", raw)
			assert.ErrorIs(t, err, ErrInvalidPath)
		})
	}
}

func TestCleanRelPath_IllegalCharacters_Rejected(t *testing.T) {
	cases := []string{
		"bad<name.txt", "bad>name.txt", "bad:name.txt", `bad"name.txt`,
		"bad|name.txt", "bad?name.txt", "bad*name.txt",
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			_, err := CleanRelPath(raw)
			require.Error(t, err, "raw=%q", raw)
			assert.ErrorIs(t, err, ErrInvalidPath)
		})
	}
}

func TestCleanRelPath_TrailingDotOrSpace_Rejected(t *testing.T) {
	cases := []string{"report.", "report ", "notes/report."}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			_, err := CleanRelPath(raw)
			require.Error(t, err, "raw=%q", raw)
			assert.ErrorIs(t, err, ErrInvalidPath)
		})
	}
}

func TestCleanRelPath_ComponentTooLong_Rejected(t *testing.T) {
	// A 210-rune filename is realistic (UAT precedent) and, before this
	// fix, passed every existing check.
	_, err := CleanRelPath(strings.Repeat("a", 210) + ".txt")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidPath)
}

func TestCleanRelPath_RelPathTooLong_Rejected(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 50; i++ {
		b.WriteString("dir/")
	}
	_, err := CleanRelPath(b.String() + "file.txt")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidPath)
}

func TestCleanRelPath_RealWorldUATNames_Allowed(t *testing.T) {
	cases := []string{"Copy of My Deck.pptx", "My Report (final) — résumé 测试 🎉.txt"}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			rel, err := CleanRelPath(raw)
			require.NoError(t, err, "raw=%q", raw)
			assert.Equal(t, raw, rel)
		})
	}
}

// --- Rename: case-insensitive collision ---

func TestRename_CaseInsensitiveCollision_DifferentFile_Rejected(t *testing.T) {
	r, _ := buildTestRoot(t)
	_, err := r.WriteContent("Report.txt", []byte("original"))
	require.NoError(t, err)
	_, err = r.WriteContent("draft.txt", []byte("draft"))
	require.NoError(t, err)

	// "report.txt" differs from "Report.txt" only in case — on a
	// case-sensitive host (this dev/CI box) an exact-case Stat would miss,
	// but this must still be rejected: on Windows/macOS this exact rename
	// would silently clobber Report.txt's content.
	_, err = r.Rename("draft.txt", "report.txt")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAlreadyExists)

	// The original file's content must be untouched.
	result, err := r.ReadContent("Report.txt")
	require.NoError(t, err)
	assert.Equal(t, "original", result.Content)
}

func TestRename_CaseOnlyRelabelOfSameFile_Allowed(t *testing.T) {
	r, _ := buildTestRoot(t)
	_, err := r.WriteContent("Report.txt", []byte("hello"))
	require.NoError(t, err)

	// Renaming a file to a different case OF ITSELF is a legitimate
	// relabel, not a collision — it must be allowed on every platform.
	fi, err := r.Rename("Report.txt", "report.txt")
	require.NoError(t, err, "a pure case-only rename of the SAME entry must be allowed")
	assert.Equal(t, "report.txt", fi.Name())

	result, err := r.ReadContent("report.txt")
	require.NoError(t, err)
	assert.Equal(t, "hello", result.Content)
}

func TestRename_CaseInsensitiveCollision_DifferentDirectory_Rejected(t *testing.T) {
	r, _ := buildTestRoot(t)
	require.NoError(t, r.root.MkdirAll("archive", 0o700))
	require.NoError(t, r.root.MkdirAll("notes", 0o700))
	_, err := r.WriteContent("archive/Report.txt", []byte("archived"))
	require.NoError(t, err)
	_, err = r.WriteContent("notes/Report.txt", []byte("notes"))
	require.NoError(t, err)

	// Moving notes/Report.txt into archive/ where "report.txt" (different
	// case) already exists must be rejected — this is a genuinely
	// different file, not a case-only relabel of the archive entry.
	_, err = r.Rename("notes/Report.txt", "archive/report.txt")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAlreadyExists)
}

// --- CopyInto: case-insensitive collision, including same-root ---

func TestCopyInto_CaseInsensitiveCollision_Rejected(t *testing.T) {
	r, _ := buildTestRoot(t)
	_, err := r.WriteContent("Report.txt", []byte("original"))
	require.NoError(t, err)
	_, err = r.WriteContent("draft.txt", []byte("draft"))
	require.NoError(t, err)

	_, err = CopyInto(r, r, "draft.txt", "report.txt")
	require.Error(t, err, "copy has no same-entry exception — case-different sibling is always a collision")
	assert.ErrorIs(t, err, ErrAlreadyExists)

	result, err := r.ReadContent("Report.txt")
	require.NoError(t, err)
	assert.Equal(t, "original", result.Content, "the existing file must be untouched")
}

func TestCopyInto_CrossRoot_CaseInsensitiveCollision_Rejected(t *testing.T) {
	src, _ := buildTestRoot(t)
	dst, _ := buildTestRoot(t)
	_, err := src.WriteContent("doc.md", []byte("hello"))
	require.NoError(t, err)
	_, err = dst.WriteContent("Doc.md", []byte("existing"))
	require.NoError(t, err)

	_, err = CopyInto(src, dst, "doc.md", "doc.md")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAlreadyExists)
}

// --- CreateUnique: case-insensitive de-duplication numbering ---

func TestCreateUnique_CaseInsensitiveCollision_Deduplicates(t *testing.T) {
	r, _ := buildTestRoot(t)
	_, f, err := r.CreateUnique("Report.txt")
	require.NoError(t, err)
	f.Close()

	// A different-case candidate must be treated as colliding — de-duplicated
	// (using the REQUESTED name's own casing for the numbered suffix, the
	// same convention an exact-case collision already followed) rather
	// than silently allowed to create a second file that would collide
	// with the first the moment this workspace opens on Windows or
	// default macOS.
	finalRel, f2, err := r.CreateUnique("report.txt")
	require.NoError(t, err)
	f2.Close()
	assert.Equal(t, "report (1).txt", finalRel)
}

func TestCreateUnique_SameCaseCollision_StillDeduplicates(t *testing.T) {
	r, _ := buildTestRoot(t)
	_, f, err := r.CreateUnique("doc.txt")
	require.NoError(t, err)
	f.Close()

	finalRel, f2, err := r.CreateUnique("doc.txt")
	require.NoError(t, err)
	f2.Close()
	assert.Equal(t, "doc (1).txt", finalRel)
}

// --- Mkdir: case-insensitive idempotency / conflict ---

func TestMkdir_CaseInsensitiveExistingDirectory_Idempotent(t *testing.T) {
	r, _ := buildTestRoot(t)
	_, created1, err := r.Mkdir("Folder")
	require.NoError(t, err)
	assert.True(t, created1)

	fi, created2, err := r.Mkdir("folder")
	require.NoError(t, err, "a case-different existing DIRECTORY must be treated as the same idempotent success")
	assert.False(t, created2)
	assert.True(t, fi.IsDir())
}

func TestMkdir_CaseInsensitiveExistingFile_Conflict(t *testing.T) {
	r, _ := buildTestRoot(t)
	_, err := r.WriteContent("Taken.txt", []byte("x"))
	require.NoError(t, err)

	_, created, err := r.Mkdir("taken.txt")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAlreadyExists)
	assert.False(t, created)
}

// --- WriteContent: case-insensitive collision on new-file PUT ---

func TestWriteContent_CaseInsensitiveCollision_Rejected(t *testing.T) {
	r, _ := buildTestRoot(t)
	_, err := r.WriteContent("Report.txt", []byte("original"))
	require.NoError(t, err)

	_, err = r.WriteContent("report.txt", []byte("would-be duplicate"))
	require.Error(t, err, "PUT to a case-different sibling must not silently create a duplicate/overwrite")
	assert.ErrorIs(t, err, ErrAlreadyExists)

	result, err := r.ReadContent("Report.txt")
	require.NoError(t, err)
	assert.Equal(t, "original", result.Content)
}

func TestWriteContent_ExactCaseMatch_StillOverwrites(t *testing.T) {
	r, _ := buildTestRoot(t)
	_, err := r.WriteContent("report.txt", []byte("v1"))
	require.NoError(t, err)

	_, err = r.WriteContent("report.txt", []byte("v2"))
	require.NoError(t, err, "an exact-case PUT to an existing file remains a normal in-place overwrite")

	result, err := r.ReadContent("report.txt")
	require.NoError(t, err)
	assert.Equal(t, "v2", result.Content)
}

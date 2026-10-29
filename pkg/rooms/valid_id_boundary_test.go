// Omnipus - Ultra-lightweight personal agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package rooms

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// Boundary tests for room ID validation (issue #1357).
// validID is the single entry point for room directory safety.
// It must accept only canonical UUIDs and reject path escapes, Windows ADS,
// separators, and other malformed input.

func TestValidID_AcceptsCanonicalUUID(t *testing.T) {
	// A real UUID as produced by uuid.NewString()
	id := "fec9cf22-16a1-4675-83ee-a8bdfd3357b8"
	require.True(t, validID(id))
}

func TestValidID_RejectsEmpty(t *testing.T) {
	require.False(t, validID(""))
}

func TestValidID_RejectsDotAndDotDot(t *testing.T) {
	require.False(t, validID("."))
	require.False(t, validID(".."))
}

func TestValidID_RejectsPathSeparators(t *testing.T) {
	require.False(t, validID("abc/def"))
	require.False(t, validID("abc\\def"))
	require.False(t, validID("../room"))
}

func TestValidID_RejectsWindowsADS(t *testing.T) {
	// ':' would let a caller address an NTFS alternate data stream on Windows.
	require.False(t, validID("abc:def"))
	require.False(t, validID("room:stream"))
}

func TestValidID_RejectsTooLong(t *testing.T) {
	// 37 chars is one over the UUID length; any non-36 is rejected.
	long := "fec9cf22-16a1-4675-83ee-a8bdfd3357b8X"
	require.Len(t, long, 37)
	require.False(t, validID(long))
}

func TestValidID_RejectsMalformedUUID(t *testing.T) {
	cases := []string{
		"not-a-uuid",
		"fec9cf22_16a1_4675_83ee_a8bdfd3357b8", // underscores
		"FEC9CF22-16A1-4675-83EE-A8BDFD3357B8", // uppercase (regexp is lowercase)
		"fec9cf2-16a1-4675-83ee-a8bdfd3357b8",  // short segment
		"fec9cf22-16a1-4675-83ee-a8bdfd3357b",  // short last segment
		"fec9cf22-16a1-4675-83e-a8bdfd3357b8",  // segment too short
		"fec9cf2216a1467583eea8bdfd3357b8",     // no hyphens
	}
	for _, c := range cases {
		require.False(t, validID(c), "case %q", c)
	}
}

func TestFileJournal_RejectsInvalidRoomID(t *testing.T) {
	j := NewFileJournal(t.TempDir())
	for _, bad := range []string{"", ".", "..", "a/b", "a\\b", "a:b", "notuuid"} {
		err := j.Create(bad)
		require.Error(t, err, "Create(%q)", bad)
		_, err = j.Read(bad, StreamEntries)
		require.Error(t, err, "Read(%q)", bad)
	}
}

func TestFileJournal_AcceptsValidRoomID(t *testing.T) {
	j := NewFileJournal(t.TempDir())
	id := "01234567-89ab-cdef-0123-456789abcdef"
	require.NoError(t, j.Create(id))
	require.NoError(t, j.Append(id, StreamEntries, map[string]string{"ok": "1"}))
	lines, err := j.Read(id, StreamEntries)
	require.NoError(t, err)
	require.Len(t, lines, 1)
}

func TestFileJournal_ListFiltersToValidIDsOnly(t *testing.T) {
	dir := t.TempDir()
	j := NewFileJournal(dir)

	// Create a valid room dir
	good := "11111111-2222-3333-4444-555555555555"
	require.NoError(t, j.Create(good))

	// Manually create an invalid directory that List must ignore
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "bad:dir"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "..", "outside"), 0o700)) // should not be seen anyway

	ids, err := j.List()
	require.NoError(t, err)
	require.Equal(t, []string{good}, ids)
}

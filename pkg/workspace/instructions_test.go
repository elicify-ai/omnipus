// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Tests for the workspace Project-Instructions storage layer.
//
// This file covers the security-critical path-traversal backstop — SafeWorkspaceDir,
// ReadInstructions, and WriteInstructions must all reject unsafe workspace ids before
// any filesystem I/O occurs. All cases assert on real disk state, not just return
// values, to catch no-op stubs and hardcoded responses.
//
// Build tags: goolm,stdjson (CGO_ENABLED=0).
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -run '.' -p 1 ./pkg/workspace/

package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// 1. Traversal rejection
// ---------------------------------------------------------------------------

// TestSafeWorkspaceDir_TraversalRejection verifies that SafeWorkspaceDir
// rejects every unsafe id with ErrInvalidWorkspaceID and returns an empty
// string. It then confirms ReadInstructions and WriteInstructions also
// reject the same ids without touching the filesystem.
//
// This is the security-critical path-traversal backstop: an unsafe id must
// never reach the filesystem as a directory component.
func TestSafeWorkspaceDir_TraversalRejection(t *testing.T) {
	unsafeIDs := []struct {
		name string
		id   string
	}{
		{name: "double-dot", id: ".."},
		{name: "dot-dot-slash-x", id: "../x"},
		{name: "slash-separated", id: "a/b"},
		{name: "backslash-separated", id: `a\b`},
		{name: "empty", id: ""},
		{name: "dot-dot-backslash-dot-dot", id: `..\..`},
		{name: "embedded-traversal", id: "foo/../bar"},
	}

	for _, tc := range unsafeIDs {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()

			// SafeWorkspaceDir must return empty string + ErrInvalidWorkspaceID.
			dir, err := SafeWorkspaceDir(home, tc.id)
			require.Equal(t, "", dir,
				"SafeWorkspaceDir: want empty string for unsafe id %q", tc.id)
			require.True(t, errors.Is(err, ErrInvalidWorkspaceID),
				"SafeWorkspaceDir: want ErrInvalidWorkspaceID for %q, got %v", tc.id, err)

			// ReadInstructions must return ErrInvalidWorkspaceID.
			content, readErr := ReadInstructions(home, tc.id)
			require.True(t, errors.Is(readErr, ErrInvalidWorkspaceID),
				"ReadInstructions: want ErrInvalidWorkspaceID for %q, got %v", tc.id, readErr)
			require.Equal(t, "", content,
				"ReadInstructions: want empty content for unsafe id %q", tc.id)

			// WriteInstructions must return ErrInvalidWorkspaceID.
			writeErr := WriteInstructions(home, tc.id, "malicious content")
			require.True(t, errors.Is(writeErr, ErrInvalidWorkspaceID),
				"WriteInstructions: want ErrInvalidWorkspaceID for %q, got %v", tc.id, writeErr)

			// Verify no stray entries were created under home.
			entries, statErr := os.ReadDir(home)
			require.NoError(t, statErr)
			require.Empty(t, entries,
				"WriteInstructions must not create any files in home dir for unsafe id %q", tc.id)
		})
	}
}

// ---------------------------------------------------------------------------
// 2. Valid id — SafeWorkspaceDir path shape
// ---------------------------------------------------------------------------

// TestSafeWorkspaceDir_ValidID verifies that a well-formed id produces the
// expected path (home/workspaces/<id>) with no error.
func TestSafeWorkspaceDir_ValidID(t *testing.T) {
	home := t.TempDir()

	got, err := SafeWorkspaceDir(home, "ws1")
	require.NoError(t, err)
	want := filepath.Join(home, "workspaces", "ws1")
	require.Equal(t, want, got,
		"SafeWorkspaceDir must return filepath.Join(home, workspaces, id)")
}

// TestSafeWorkspaceDir_DifferentIDsDifferentPaths is the differentiation test:
// two distinct valid ids must produce two distinct paths. A hardcoded or
// no-op implementation would return the same path for both.
func TestSafeWorkspaceDir_DifferentIDsDifferentPaths(t *testing.T) {
	home := t.TempDir()

	dir1, err1 := SafeWorkspaceDir(home, "ws-alpha")
	require.NoError(t, err1)

	dir2, err2 := SafeWorkspaceDir(home, "ws-beta")
	require.NoError(t, err2)

	require.NotEqual(t, dir1, dir2,
		"distinct ids must map to distinct workspace dirs")
}

// ---------------------------------------------------------------------------
// 3. Round-trip: write then read
// ---------------------------------------------------------------------------

// TestInstructions_RoundTrip verifies that content written by WriteInstructions
// is returned verbatim by ReadInstructions and that the file exists on disk at
// the expected path (workspaces/<id>/AGENT.md).
func TestInstructions_RoundTrip(t *testing.T) {
	home := t.TempDir()
	const id = "ws1"
	const body = "hello, workspace instructions"

	require.NoError(t, WriteInstructions(home, id, body))

	// Verify disk state: file must exist at the canonical location.
	expectedPath := filepath.Join(home, "workspaces", id, "AGENT.md")
	_, statErr := os.Stat(expectedPath)
	require.NoError(t, statErr,
		"AGENT.md must exist at workspaces/%s/AGENT.md after WriteInstructions", id)

	// ReadInstructions must return the exact content.
	got, err := ReadInstructions(home, id)
	require.NoError(t, err)
	require.Equal(t, body, got, "ReadInstructions must return the written content verbatim")
}

// TestInstructions_RoundTrip_DifferentContents is the differentiation test:
// two different ids with different content must each read back their own
// content independently. This catches hardcoded or shared-state bugs.
func TestInstructions_RoundTrip_DifferentContents(t *testing.T) {
	home := t.TempDir()

	require.NoError(t, WriteInstructions(home, "ws-a", "instructions for workspace A"))
	require.NoError(t, WriteInstructions(home, "ws-b", "instructions for workspace B"))

	gotA, err := ReadInstructions(home, "ws-a")
	require.NoError(t, err)
	gotB, err := ReadInstructions(home, "ws-b")
	require.NoError(t, err)

	require.Equal(t, "instructions for workspace A", gotA)
	require.Equal(t, "instructions for workspace B", gotB)
	require.NotEqual(t, gotA, gotB,
		"each workspace must store and return its own distinct content")
}

// ---------------------------------------------------------------------------
// 4. Empty string clears existing file
// ---------------------------------------------------------------------------

// TestInstructions_EmptyStringClearsFile verifies that writing an empty string
// after existing content removes the AGENT.md file (cleared state) and that
// a subsequent read returns ("", nil).
func TestInstructions_EmptyStringClearsFile(t *testing.T) {
	home := t.TempDir()
	const id = "ws-clear"

	// Write initial content.
	require.NoError(t, WriteInstructions(home, id, "hello"))

	// Overwrite with empty string — must remove the file.
	require.NoError(t, WriteInstructions(home, id, ""))

	// Disk state: file must be absent.
	expectedPath := filepath.Join(home, "workspaces", id, "AGENT.md")
	_, statErr := os.Stat(expectedPath)
	require.True(t, os.IsNotExist(statErr),
		"AGENT.md must be removed after writing empty content; got stat err: %v", statErr)

	// ReadInstructions must return ("", nil).
	got, err := ReadInstructions(home, id)
	require.NoError(t, err)
	require.Equal(t, "", got, "ReadInstructions must return empty after clear")
}

// ---------------------------------------------------------------------------
// 5. Whitespace-only clears existing file
// ---------------------------------------------------------------------------

// TestInstructions_WhitespaceOnlyClearsFile verifies that whitespace-only
// content (spaces, newlines, tabs) is treated as empty and removes the file.
func TestInstructions_WhitespaceOnlyClearsFile(t *testing.T) {
	home := t.TempDir()
	const id = "ws-whitespace"

	// Write initial content.
	require.NoError(t, WriteInstructions(home, id, "hello"))

	// Overwrite with whitespace-only.
	require.NoError(t, WriteInstructions(home, id, "   \n\t "))

	// Disk state: file must be absent.
	expectedPath := filepath.Join(home, "workspaces", id, "AGENT.md")
	_, statErr := os.Stat(expectedPath)
	require.True(t, os.IsNotExist(statErr),
		"AGENT.md must be removed after writing whitespace-only content")

	// ReadInstructions must return ("", nil).
	got, err := ReadInstructions(home, id)
	require.NoError(t, err)
	require.Equal(t, "", got, "ReadInstructions must return empty after whitespace clear")
}

// ---------------------------------------------------------------------------
// 6. Empty write on non-existent file
// ---------------------------------------------------------------------------

// TestInstructions_EmptyWriteOnNonExistentFile verifies that writing empty
// content to a workspace that has never had instructions succeeds (nil err)
// and creates no files. This exercises the os.IsNotExist branch in the clear
// path inside WriteInstructions.
func TestInstructions_EmptyWriteOnNonExistentFile(t *testing.T) {
	home := t.TempDir()
	const id = "ws-fresh"

	err := WriteInstructions(home, id, "")
	require.NoError(t, err,
		"WriteInstructions with empty content on a non-existent workspace must succeed")

	// Disk state: AGENT.md must not exist.
	expectedPath := filepath.Join(home, "workspaces", id, "AGENT.md")
	_, statErr := os.Stat(expectedPath)
	require.True(t, os.IsNotExist(statErr),
		"WriteInstructions with empty content must not create AGENT.md")
}

// ---------------------------------------------------------------------------
// 7. Size boundary
// ---------------------------------------------------------------------------

// TestInstructions_SizeBoundary verifies the 262144-byte limit enforced by
// WriteInstructions:
//   - Exactly maxInstructionsBytes (262144) succeeds and round-trips.
//   - maxInstructionsBytes+1 (262145) is rejected with ErrInstructionsTooLarge
//     and writes nothing to disk.
func TestInstructions_SizeBoundary(t *testing.T) {
	const limit = 262144

	t.Run("exactly-at-limit-succeeds", func(t *testing.T) {
		home := t.TempDir()
		const id = "ws-exact"
		content := strings.Repeat("a", limit)

		require.NoError(t, WriteInstructions(home, id, content),
			"content of exactly %d bytes must be accepted", limit)

		// Disk state: file must exist.
		expectedPath := filepath.Join(home, "workspaces", id, "AGENT.md")
		_, statErr := os.Stat(expectedPath)
		require.NoError(t, statErr,
			"AGENT.md must exist after writing %d-byte content", limit)

		// Round-trip: content must be returned verbatim.
		got, err := ReadInstructions(home, id)
		require.NoError(t, err)
		require.Equal(t, content, got,
			"ReadInstructions must return the full %d-byte content", limit)
	})

	t.Run("one-byte-over-limit-rejected", func(t *testing.T) {
		home := t.TempDir()
		const id = "ws-over"
		content := strings.Repeat("b", limit+1)

		err := WriteInstructions(home, id, content)
		require.True(t, errors.Is(err, ErrInstructionsTooLarge),
			"content of %d bytes must be rejected with ErrInstructionsTooLarge, got: %v",
			limit+1, err)

		// Disk state: no file must have been written.
		expectedPath := filepath.Join(home, "workspaces", id, "AGENT.md")
		_, statErr := os.Stat(expectedPath)
		require.True(t, os.IsNotExist(statErr),
			"AGENT.md must NOT be created when content exceeds the size limit")
	})
}

// ---------------------------------------------------------------------------
// 7b. Write permissions (FIX 1)
// ---------------------------------------------------------------------------

// TestInstructions_WritePerms verifies that WriteInstructions creates AGENT.md
// with mode 0o600 and its parent directory with mode 0o700.
func TestInstructions_WritePerms(t *testing.T) {
	home := t.TempDir()
	const id = "ws-perms"

	require.NoError(t, WriteInstructions(home, id, "perm check"))

	// File must be 0o600.
	agentMD := filepath.Join(home, "workspaces", id, "AGENT.md")
	fi, err := os.Stat(agentMD)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), fi.Mode().Perm(),
		"AGENT.md must be written with mode 0o600")

	// Parent directory must be 0o700.
	wsDir := filepath.Join(home, "workspaces", id)
	di, err := os.Stat(wsDir)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), di.Mode().Perm(),
		"workspace directory must be created with mode 0o700")
}

// ---------------------------------------------------------------------------
// 7c. Read-side truncation sentinel (FIX 2)
// ---------------------------------------------------------------------------

// TestInstructions_ReadTruncationSentinel verifies that ReadInstructions
// truncates files larger than maxInstructionsBytes and appends the in-band
// sentinel so the model knows the content was cut.
//
// The oversized file is written directly via os.WriteFile (bypassing the
// write-cap enforced by WriteInstructions) to simulate a file written outside
// Omnipus or pre-dating the size limit.
func TestInstructions_ReadTruncationSentinel(t *testing.T) {
	home := t.TempDir()
	const id = "ws-trunc"

	// Create the workspace dir and write an oversized AGENT.md directly.
	wsDir := filepath.Join(home, "workspaces", id)
	require.NoError(t, os.MkdirAll(wsDir, 0o700))
	oversized := make([]byte, maxInstructionsBytes+1)
	for i := range oversized {
		oversized[i] = 'x'
	}
	agentMD := filepath.Join(wsDir, "AGENT.md")
	require.NoError(t, os.WriteFile(agentMD, oversized, 0o600))

	// ReadInstructions must truncate to maxInstructionsBytes and append the sentinel.
	got, err := ReadInstructions(home, id)
	require.NoError(t, err, "ReadInstructions must not return an error on truncation")

	const sentinel = "\n\n[... workspace instructions truncated at 256 KB ...]"

	// The returned string must end with the sentinel.
	require.True(t, strings.HasSuffix(got, sentinel),
		"truncated content must end with the in-band sentinel; got suffix: %q",
		got[maxInt(0, len(got)-len(sentinel)-20):])

	// The content before the sentinel must be exactly maxInstructionsBytes of 'x'.
	contentPart := got[:len(got)-len(sentinel)]
	require.Equal(t, maxInstructionsBytes, len(contentPart),
		"content before sentinel must be exactly maxInstructionsBytes (%d) bytes, got %d",
		maxInstructionsBytes, len(contentPart))
	require.Equal(t, strings.Repeat("x", maxInstructionsBytes), contentPart,
		"content before sentinel must be the first maxInstructionsBytes bytes of the file")
}

// maxInt returns the larger of two ints (local helper; avoids Go 1.21 min/max dependency).
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// ---------------------------------------------------------------------------
// 8. Absent read — never-written workspace returns ("", nil)
// ---------------------------------------------------------------------------

// TestInstructions_AbsentRead verifies that ReadInstructions on a workspace
// whose AGENT.md file has never been created returns ("", nil) — absent file
// is the canonical empty/cleared state, not an error.
func TestInstructions_AbsentRead(t *testing.T) {
	home := t.TempDir()
	const id = "ws-never-written"

	got, err := ReadInstructions(home, id)
	require.NoError(t, err,
		"ReadInstructions on a never-written workspace must not return an error")
	require.Equal(t, "", got,
		"ReadInstructions on a never-written workspace must return empty string")
}

// ---------------------------------------------------------------------------
// Exists helper — basic coverage
// ---------------------------------------------------------------------------

// TestExists_ValidAndInvalidIDs verifies that Exists returns false for unsafe
// ids (traversal backstop on the helper) and correctly reports presence based
// on whether the <id>.json metadata file is on disk.
func TestExists_ValidAndInvalidIDs(t *testing.T) {
	home := t.TempDir()

	// Unsafe ids must always return false (no panic, no I/O).
	for _, unsafeID := range []string{"..", "../x", "a/b", `a\b`, ""} {
		require.False(t, Exists(home, unsafeID),
			"Exists must return false for unsafe id %q", unsafeID)
	}

	// A valid id without a .json file on disk → false.
	require.False(t, Exists(home, "ws-absent"),
		"Exists must return false when no workspace JSON is present")

	// Create the workspaces dir and a stub .json file → true.
	wsDir := filepath.Join(home, "workspaces")
	require.NoError(t, os.MkdirAll(wsDir, 0o755))
	jsonPath := filepath.Join(wsDir, "ws-present.json")
	require.NoError(t, os.WriteFile(jsonPath, []byte(`{"id":"ws-present"}`), 0o644))

	require.True(t, Exists(home, "ws-present"),
		"Exists must return true when the workspace JSON file exists")
}

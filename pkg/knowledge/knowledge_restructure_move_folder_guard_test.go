// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

// knowledge_restructure_move_folder_guard_test.go — Codex review 2026-09-14
// finding #1 (High): a REFUSED move must not leave a directory behind, and
// above all not one OUTSIDE the knowledge base.
//
// The D-53 folder-creation step (ensureMoveDestinationFolder) used to run
// `os.MkdirAll` on the lexical join of the collection root and the caller's
// destination folder BEFORE the renamer's FR-044 symlink guard had looked at
// the path. With a symlink inside the collection pointing outside it
// (`Inbox -> /somewhere/else`), a move into `Inbox/New/` created
// `/somewhere/else/New` — a mutation outside the permitted root — and only
// then did the rename refuse. Each case below first proves the fixture is
// the case it claims to be (the link resolves somewhere other than its
// lexical position), then asserts the refusal AND that nothing was created
// on either side of the link.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRestructureMove_RefusedMoveThroughEscapingSymlinkCreatesNothingOutside
// is the reviewer's exact scenario: the destination folder's first segment is
// a symlink whose target is OUTSIDE the collection.
func TestRestructureMove_RefusedMoveThroughEscapingSymlinkCreatesNothingOutside(t *testing.T) {
	home, ws, root := a4Fixture(t, "KB")
	deps, _ := a4Deps(home)
	tool := NewRestructureTool(deps)
	a4Note(t, root, "Real.md", "# Real\n")

	outside := t.TempDir()
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "Inbox")))
	// Prove the fixture: the link really leaves the root.
	resolved, err := filepath.EvalSymlinks(filepath.Join(root, "Inbox"))
	require.NoError(t, err)
	realRoot, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)
	require.False(t, isWithinOrEqual(realRoot, resolved), "fixture: Inbox must point outside the collection")

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"op": "move", "collection": "KB", "path": "Real.md", "new_folder": "Inbox/New",
	})
	require.True(t, res.IsError, "a move through a symlink that leaves the collection must be refused: %s", res.ForLLM)

	require.NoDirExists(t, filepath.Join(outside, "New"),
		"the refused move must not have created a directory OUTSIDE the knowledge base")
	require.NoFileExists(t, filepath.Join(outside, "New", "Real.md"))
	require.FileExists(t, filepath.Join(root, "Real.md"), "the note must still be where it was")
	entries, err := os.ReadDir(outside)
	require.NoError(t, err)
	require.Empty(t, entries, "the external target must be untouched by a refused move")
}

// TestRestructureMove_RefusedMoveThroughInRootSymlinkCreatesNothing covers the
// FR-044 half: a directory symlink whose target stays INSIDE the collection is
// still not an addressable location, so the move is refused — and the refusal
// must not have created the folder behind the link either.
func TestRestructureMove_RefusedMoveThroughInRootSymlinkCreatesNothing(t *testing.T) {
	home, ws, root := a4Fixture(t, "KB")
	deps, _ := a4Deps(home)
	tool := NewRestructureTool(deps)
	a4Note(t, root, "Real.md", "# Real\n")
	a4Note(t, root, "Archive/Keep.md", "# Keep\n")
	require.NoError(t, os.Symlink(filepath.Join(root, "Archive"), filepath.Join(root, "Inbox")))

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"op": "move", "collection": "KB", "path": "Real.md", "new_folder": "Inbox/New",
	})
	require.True(t, res.IsError, "a move through a symlinked folder must be refused (FR-044): %s", res.ForLLM)

	require.NoDirExists(t, filepath.Join(root, "Archive", "New"),
		"the refused move must not have created the folder behind the symlink")
	require.FileExists(t, filepath.Join(root, "Real.md"))
}

// TestRestructureMove_StillCreatesAnHonestMissingFolder pins D-53 alongside
// the guard: an ordinary missing destination folder (no link anywhere in its
// path) is still created, so the guard did not simply disable the feature.
func TestRestructureMove_StillCreatesAnHonestMissingFolder(t *testing.T) {
	home, ws, root := a4Fixture(t, "KB")
	deps, _ := a4Deps(home)
	tool := NewRestructureTool(deps)
	a4Note(t, root, "Real.md", "# Real\n")

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"op": "move", "collection": "KB", "path": "Real.md", "new_folder": "Deep/Er/Folder",
	})
	require.False(t, res.IsError, res.ForLLM)
	require.FileExists(t, filepath.Join(root, "Deep", "Er", "Folder", "Real.md"))
	require.Contains(t, res.ForLLM, "folder Deep/Er/Folder created")
}

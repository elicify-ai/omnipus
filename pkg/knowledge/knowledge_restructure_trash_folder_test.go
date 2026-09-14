// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

// knowledge_restructure_trash_folder_test.go - trashing and restoring a whole
// FOLDER (round-4 attachment cascade, UAT D-123 / #701). Expected values come
// from the trash convention applied to a directory: bytes untouched, original
// path preserved under the timestamp directory, links from OUTSIDE the folder
// counted as dangling, and a restore that brings back exactly what left.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTrashFolder_RestoreBringsItBackIntact(t *testing.T) {
	dir, root := a2Collection(t, map[string]string{
		"Projects/Atlas/Plan.md":       "---\nid: PRJ-1\n---\n# Plan\n\n![[Projects/Atlas/diagram.png]]\n",
		"Projects/Atlas/diagram.png":   folderFixturePNG,
		"Projects/Atlas/notes/Deep.md": "# Deep\n",
		"Home.md":                      "See [[Projects/Atlas/Plan]] and ![[Projects/Atlas/diagram.png]].\n",
	})
	before := a2Snapshot(t, filepath.Join(dir, "Projects", "Atlas"))
	tr := rtTrasher(t, root)

	res, err := tr.Trash(TrashRequest{Path: "Projects/Atlas", Folder: true})
	require.NoError(t, err)
	assert.True(t, res.Folder)
	assert.Equal(t, "Projects/Atlas", res.OriginalPath)
	assert.Equal(t, ".omnipus-vault/trash/"+res.TrashID+"/Projects/Atlas", res.TrashPath)
	assert.Equal(t, []string{"Projects/Atlas/Plan.md", "Projects/Atlas/diagram.png", "Projects/Atlas/notes/Deep.md"}, res.Members)
	assert.Equal(t, 2, res.DanglingLinkCount,
		"Home.md's link and embed dangle; the embed inside Plan.md travels with the folder and does not")
	assert.Equal(t, []string{"Home.md"}, res.DanglingNotes)
	assert.NoDirExists(t, filepath.Join(dir, "Projects", "Atlas"))
	assert.Equal(t, before, a2Snapshot(t, filepath.Join(dir, filepath.FromSlash(res.TrashPath))),
		"the trash holds exactly what the live folder held")

	raw, rerr := os.ReadFile(filepath.Join(dir, ".omnipus-vault", "trash", res.TrashID, "entry.json"))
	require.NoError(t, rerr)
	var receipt trashReceipt
	require.NoError(t, json.Unmarshal(raw, &receipt))
	assert.Equal(t, "folder", receipt.Kind)
	assert.Equal(t, "Projects/Atlas", receipt.OriginalPath)

	restored, err := tr.Restore(RestoreRequest{Path: "Projects/Atlas"})
	require.NoError(t, err, "a trashed folder is restorable by its original path")
	assert.True(t, restored.Folder)
	assert.Equal(t, "Projects/Atlas", restored.OriginalPath)
	assert.Equal(t, res.TrashID, restored.RestoredFrom)
	assert.Equal(t, 2, restored.ResolvedLinksCount, "the link and embed from Home.md resolve again")
	assert.Equal(t, before, a2Snapshot(t, filepath.Join(dir, "Projects", "Atlas")), "the folder is back intact")
	assert.NoDirExists(t, filepath.Join(dir, ".omnipus-vault", "trash", res.TrashID), "the emptied trash entry is pruned")
}

// Single-file behaviour is unchanged: without Folder, "Projects" still means
// the note "Projects.md", even with a folder of the same name beside it.
func TestTrash_WithoutTheFolderFlagTheNoteHabitIsUnchanged(t *testing.T) {
	dir, root := a2Collection(t, map[string]string{
		"Projects.md":       "# Projects index\n",
		"Projects/Atlas.md": "# Atlas\n",
	})
	tr := rtTrasher(t, root)

	res, err := tr.Trash(TrashRequest{Path: "Projects"})
	require.NoError(t, err)
	assert.Equal(t, "Projects.md", res.OriginalPath)
	assert.False(t, res.Folder)
	assert.FileExists(t, filepath.Join(dir, "Projects", "Atlas.md"), "a folder is never trashed without the Folder flag")
}

// Trashing the note "Deals/Acme.md" leaves "<trash id>/Deals/" in the trash as
// that note's parent directory. It is not a trashed folder, and restoring
// "Deals" must not treat it as one.
func TestRestore_TrashedNotesParentDirectoryIsNotAFolder(t *testing.T) {
	dir, root := a2Collection(t, map[string]string{"Deals/Acme.md": "# Acme\n"})
	tr := rtTrasher(t, root)
	_, err := tr.Trash(TrashRequest{Path: "Deals/Acme.md"})
	require.NoError(t, err)
	// Remove the now-empty live folder, so nothing but the kind check stands
	// between this restore and a wrong "success".
	require.NoError(t, os.Remove(filepath.Join(dir, "Deals")))

	_, err = tr.Restore(RestoreRequest{Path: "Deals"})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrRestoreNotFound)
	assert.NoDirExists(t, filepath.Join(dir, "Deals"))
}

// FR-038a for a folder: a note inside the trashed folder carries identifier
// P-1, and a live note has taken P-1 since. Restoring would put two records
// under one identifier, so it is refused and the folder stays in the trash.
func TestRestoreFolder_RefusesAnIdentifierALiveNoteHolds(t *testing.T) {
	dir, root := a2Collection(t, map[string]string{"Team/Pat.md": "---\nid: P-1\n---\n# Pat\n"})
	tr := rtTrasher(t, root)
	res, err := tr.Trash(TrashRequest{Path: "Team", Folder: true})
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "People"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "People", "Pat Again.md"), []byte("---\nid: P-1\n---\n# Pat again\n"), 0o644))

	_, err = tr.Restore(RestoreRequest{Path: "Team"})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrRestoreIdentifierCollision)
	assert.Contains(t, err.Error(), "People/Pat Again.md")
	assert.Contains(t, err.Error(), "Team/Pat.md")
	assert.NoDirExists(t, filepath.Join(dir, "Team"))
	assert.FileExists(t, filepath.Join(dir, ".omnipus-vault", "trash", res.TrashID, "Team", "Pat.md"), "the folder stays in the trash")
}

// Restoring one file out of a trashed folder must not delete the folder's
// receipt: the rest of the folder is still in the trash and must stay
// restorable by the folder's name.
func TestRestore_OneFileOutOfATrashedFolderKeepsTheFolderRestorable(t *testing.T) {
	dir, root := a2Collection(t, map[string]string{
		"Projects/Atlas/Plan.md":     "# Plan\n",
		"Projects/Atlas/diagram.png": folderFixturePNG,
	})
	tr := rtTrasher(t, root)
	res, err := tr.Trash(TrashRequest{Path: "Projects/Atlas", Folder: true})
	require.NoError(t, err)

	one, err := tr.Restore(RestoreRequest{Path: "Projects/Atlas/diagram.png"})
	require.NoError(t, err, "a single file inside a trashed folder is restorable by its own path")
	assert.False(t, one.Folder)
	assert.FileExists(t, filepath.Join(dir, ".omnipus-vault", "trash", res.TrashID, "entry.json"),
		"the folder's receipt still describes Plan.md, which is still in the trash")

	// Clear the way: the single-file restore recreated the live folder.
	require.NoError(t, os.Rename(filepath.Join(dir, "Projects", "Atlas", "diagram.png"), filepath.Join(dir, "diagram.png")))
	require.NoError(t, os.Remove(filepath.Join(dir, "Projects", "Atlas")))

	rest, err := tr.Restore(RestoreRequest{Path: "Projects/Atlas"})
	require.NoError(t, err, "the rest of the folder is still restorable by the folder's name")
	assert.True(t, rest.Folder)
	assert.Equal(t, "# Plan\n", a2Read(t, dir, "Projects/Atlas/Plan.md"))
}

// The trash-side twin of TestRenameFolder_SourceSpelledDifferentlyOnDiskIsRefused.
// On a case-insensitive filesystem "projects/atlas" opens the folder, but no
// file in the link graph matches that spelling, so the trash would report no
// members and no dangling links for a folder that has both. On a
// case-sensitive filesystem the wrong spelling does not exist. The assertions
// hold on both.
func TestTrashFolder_SourceSpelledDifferentlyOnDiskIsRefused(t *testing.T) {
	dir, root := a2Collection(t, map[string]string{
		"Projects/Atlas/Plan.md": "# Plan\n",
		"Home.md":                "See [[Projects/Atlas/Plan]].\n",
	})
	before := a2Snapshot(t, dir)
	tr := rtTrasher(t, root)

	_, err := tr.Trash(TrashRequest{Path: "projects/atlas", Folder: true})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrTrashSourceMissing)
	assert.Equal(t, before, a2Snapshot(t, dir), "nothing was moved into the trash")
}

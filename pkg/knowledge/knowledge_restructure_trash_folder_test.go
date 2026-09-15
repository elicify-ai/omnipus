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
	"strings"
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

// tieFixture puts BOTH a note "Projects.md" and a folder "Projects" (holding
// Atlas.md) into the trash, so the bare name "Projects" could mean either.
func tieFixture(t *testing.T) (dir string, tr *Trasher, folderTrashID, noteTrashID string) {
	t.Helper()
	dir, root := a2Collection(t, map[string]string{
		"Projects.md":       "# Projects index\n",
		"Projects/Atlas.md": "# Atlas\n",
	})
	tr = rtTrasher(t, root)
	folderRes, err := tr.Trash(TrashRequest{Path: "Projects", Folder: true})
	require.NoError(t, err)
	noteRes, err := tr.Trash(TrashRequest{Path: "Projects.md"})
	require.NoError(t, err)
	require.NotEqual(t, folderRes.TrashID, noteRes.TrashID)
	require.NoFileExists(t, filepath.Join(dir, "Projects.md"))
	require.NoDirExists(t, filepath.Join(dir, "Projects"))
	return dir, tr, folderRes.TrashID, noteRes.TrashID
}

// A trashed note "Projects.md" and a trashed folder "Projects": restoring
// "Projects" must not guess. It is refused, the refusal names both and says
// how to pick each one, and nothing on disk changes (trash included).
func TestRestore_NoteAndFolderWithTheSameNameIsRefusedNamingBoth(t *testing.T) {
	dir, tr, _, _ := tieFixture(t)
	before := a2Snapshot(t, dir)

	res, err := tr.Restore(RestoreRequest{Path: "Projects"})
	require.Error(t, err, "restoring an ambiguous name must be refused, got %+v", res)
	assert.Nil(t, res)
	assert.ErrorIs(t, err, ErrRestoreAmbiguous)
	assert.Contains(t, err.Error(), `note "Projects.md"`)
	assert.Contains(t, err.Error(), `folder "Projects"`)
	assert.Contains(t, err.Error(), `give path "Projects.md" to restore the note`)
	assert.Contains(t, err.Error(), `give path "Projects/"`)
	assert.Equal(t, before, a2Snapshot(t, dir), "a refused restore changes nothing, in the collection or the trash")
}

func TestRestore_TieExplicitNotePathRestoresOnlyTheNote(t *testing.T) {
	dir, tr, folderTrashID, _ := tieFixture(t)

	res, err := tr.Restore(RestoreRequest{Path: "Projects.md"})
	require.NoError(t, err)
	assert.False(t, res.Folder)
	assert.Equal(t, "Projects.md", res.OriginalPath)
	assert.Equal(t, "# Projects index\n", a2Read(t, dir, "Projects.md"))
	assert.NoDirExists(t, filepath.Join(dir, "Projects"))
	assert.FileExists(t, filepath.Join(dir, ".omnipus-vault", "trash", folderTrashID, "Projects", "Atlas.md"),
		"the folder stays in the trash")
}

func TestRestore_TieTrailingSlashRestoresOnlyTheFolder(t *testing.T) {
	dir, tr, folderTrashID, noteTrashID := tieFixture(t)

	res, err := tr.Restore(RestoreRequest{Path: "Projects/"})
	require.NoError(t, err)
	assert.True(t, res.Folder)
	assert.Equal(t, "Projects", res.OriginalPath)
	assert.Equal(t, folderTrashID, res.RestoredFrom)
	assert.Equal(t, "# Atlas\n", a2Read(t, dir, "Projects/Atlas.md"))
	assert.NoFileExists(t, filepath.Join(dir, "Projects.md"))
	assert.FileExists(t, filepath.Join(dir, ".omnipus-vault", "trash", noteTrashID, "Projects.md"),
		"the note stays in the trash")
}

// Without a tie every form behaves as it did before the tie refusal existed,
// and the folder form "Projects/" never falls back to a note.
func TestRestore_WithoutATieEachFormRestoresAsBefore(t *testing.T) {
	for _, tc := range []struct {
		name       string
		trashAs    TrashRequest
		restore    string
		wantFolder bool
		wantPath   string
	}{
		{"lone note by bare name", TrashRequest{Path: "Projects.md"}, "Projects", false, "Projects.md"},
		{"lone note by .md name", TrashRequest{Path: "Projects.md"}, "Projects.md", false, "Projects.md"},
		{"lone folder by bare name", TrashRequest{Path: "Projects", Folder: true}, "Projects", true, "Projects"},
		{"lone folder by trailing slash", TrashRequest{Path: "Projects", Folder: true}, "Projects/", true, "Projects"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, root := a2Collection(t, map[string]string{
				"Projects.md":       "# Projects index\n",
				"Projects/Atlas.md": "# Atlas\n",
			})
			before := a2Snapshot(t, dir)
			tr := rtTrasher(t, root)
			trashed, err := tr.Trash(tc.trashAs)
			require.NoError(t, err)

			res, err := tr.Restore(RestoreRequest{Path: tc.restore})
			require.NoError(t, err)
			assert.Equal(t, tc.wantFolder, res.Folder)
			assert.Equal(t, tc.wantPath, res.OriginalPath)
			assert.Equal(t, trashed.TrashID, res.RestoredFrom)
			after := a2Snapshot(t, dir)
			for k := range after {
				if strings.HasPrefix(k, ".omnipus-vault/") {
					delete(after, k)
				}
			}
			assert.Equal(t, before, after, "the collection is back exactly as it was")
		})
	}
}

// "Projects/" asks for a folder. With only a trashed NOTE of that name it is
// refused rather than quietly restoring the note, and nothing changes.
func TestRestore_TrailingSlashNeverRestoresANote(t *testing.T) {
	dir, root := a2Collection(t, map[string]string{"Projects.md": "# Projects index\n"})
	tr := rtTrasher(t, root)
	_, err := tr.Trash(TrashRequest{Path: "Projects.md"})
	require.NoError(t, err)
	before := a2Snapshot(t, dir)

	_, err = tr.Restore(RestoreRequest{Path: "Projects/"})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrRestoreNotFound)
	assert.Contains(t, err.Error(), "no trashed folder")
	assert.Equal(t, before, a2Snapshot(t, dir))
}

// The same tie, driven through knowledge_restructure: the agent sees both
// names and both ways out, and each way out works through the tool.
func TestRestructureTool_RestoreTieIsRefusedAndEachFormWorks(t *testing.T) {
	home, ws, root := a4Fixture(t, "KB")
	require.NoError(t, os.WriteFile(filepath.Join(root, "Projects.md"), []byte("# Projects index\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "Projects"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "Projects", "Atlas.md"), []byte("# Atlas\n"), 0o644))
	deps, _ := a4Deps(home)
	tool := NewRestructureTool(deps)
	ctx := a4Ctx("mia", ws)
	run := func(args map[string]any) string {
		t.Helper()
		args["collection"] = "KB"
		res := tool.Execute(ctx, args)
		require.False(t, res.IsError, "unexpected refusal for %v: %s", args, res.ForLLM)
		return res.ForLLM
	}
	run(map[string]any{"op": "trash", "path": "Projects", "folder": true})
	run(map[string]any{"op": "trash", "path": "Projects.md"})

	tie := tool.Execute(ctx, map[string]any{"op": "restore", "collection": "KB", "path": "Projects"})
	require.True(t, tie.IsError, "the tie must be refused, got: %s", tie.ForLLM)
	assert.Contains(t, tie.ForLLM, `give path "Projects.md" to restore the note`)
	assert.Contains(t, tie.ForLLM, `give path "Projects/"`)
	assert.NoFileExists(t, filepath.Join(root, "Projects.md"))
	assert.NoDirExists(t, filepath.Join(root, "Projects"))

	out := run(map[string]any{"op": "restore", "path": "Projects/"})
	assert.Contains(t, out, "FOLDER: restored")
	assert.FileExists(t, filepath.Join(root, "Projects", "Atlas.md"))
	assert.NoFileExists(t, filepath.Join(root, "Projects.md"))

	out = run(map[string]any{"op": "restore", "path": "Projects.md"})
	assert.Contains(t, out, "Projects.md <- restored from trash")
	assert.FileExists(t, filepath.Join(root, "Projects.md"))
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

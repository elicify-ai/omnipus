// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

// knowledge_restructure_folder_index_test.go - an AGENT's folder operations
// through knowledge_restructure must leave both indexes current, exactly as
// the Library's folder doors do (FIX4 follow-up 1, "agent restore of a
// folder: the index warning").
//
// The defect: restoring a trashed folder through the tool put every file back
// on disk, then refreshed the index for the FOLDER's path as if it were one
// note. The index refuses a directory ("is a directory, not a file"), so the
// agent was told the index could not be refreshed and search missed every
// restored file until the next full sync.
//
// The oracles are the two indexes themselves, opened directly: the text index
// through Index.Search and the properties index through its own store. Neither
// goes through a tool that might re-sync on its own, so a pass here means the
// restructure call itself made the files findable.

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/records"
	"github.com/elicify-ai/omnipus/pkg/records/propindex"
)

// rfiToken is a word that appears in exactly one file of the fixture: the
// record note inside the folder.
const rfiToken = "Qzvrfolderindextok"

// rfiFixture is a knowledge base holding a folder "Projects" with one record
// note and one attachment, plus a note OUTSIDE the folder that links the note
// and embeds the attachment. Both indexes hold all three files before the test
// begins, which the fixture proves rather than assumes.
func rfiFixture(t *testing.T) (home, ws, root string, deps AuthoringDeps) {
	t.Helper()
	home, ws, root = a4Fixture(t, "kb")
	deps, _ = a4Deps(home)
	ctx := a4Ctx("mia", ws)

	cfg := NewConfigureTool(deps).Execute(ctx, map[string]any{
		"collection": "kb", "op": "create_record_type", "type": "shipment",
		"definition": map[string]any{
			"schema_version": float64(1),
			"properties": map[string]any{
				"delivered": map[string]any{"type": string(records.TypeCheckbox)},
			},
		},
	})
	require.False(t, cfg.IsError, "fixture: record type refused: %s", cfg.ForLLM)

	rfiWrite(t, root, "Projects/Atlas.md",
		"---\ntype: shipment\ndelivered: true\n---\n# Atlas\n\nContains "+rfiToken+" here.\n")
	rfiWrite(t, root, "Projects/assets/diagram.png", "\x89PNG\r\n\x1a\nnot-really-a-png")
	rfiWrite(t, root, "Outside.md", "See [[Projects/Atlas]] and ![[Projects/assets/diagram.png]].\n")

	for _, p := range []string{"Projects/Atlas.md", "Projects/assets/diagram.png", "Outside.md"} {
		require.Empty(t, RefreshIndexesForNote(context.Background(), home, root, p),
			"fixture: indexing %s must succeed", p)
	}
	rfiRequireFolderIndexed(t, home, root, "Projects")
	return home, ws, root, deps
}

func rfiWrite(t *testing.T, root, rel, body string) {
	t.Helper()
	abs := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
	require.NoError(t, os.WriteFile(abs, []byte(body), 0o644))
}

// rfiTextHits searches the text index directly and returns the matching paths,
// sorted.
func rfiTextHits(t *testing.T, home, root, query string) []string {
	t.Helper()
	ix, err := OpenIndex(home, root)
	require.NoError(t, err)
	defer func() { require.NoError(t, ix.Close()) }()
	hits, err := ix.Search(query, 20)
	require.NoError(t, err)
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.Path)
	}
	sort.Strings(out)
	return out
}

// rfiRequireFolderIndexed asserts the folder's note is findable by its word in
// the text index, and that the properties index holds the note as a shipment
// record and the attachment as an attachment.
func rfiRequireFolderIndexed(t *testing.T, home, root, folder string) {
	t.Helper()
	note, att := folder+"/Atlas.md", folder+"/assets/diagram.png"
	require.Equal(t, []string{note}, rfiTextHits(t, home, root, rfiToken),
		"the text index must find the note at %s by its word", note)
	rows := ifxPropPaths(t, home, root)
	require.Contains(t, rows, note, "the properties index must hold %s", note)
	require.Equal(t, "shipment", rows[note].DeclaredType,
		"the properties index must hold %s as a shipment record", note)
	require.Contains(t, rows, att, "the properties index must hold %s", att)
	require.Equal(t, propindex.KindAttachment, rows[att].Kind)
}

// rfiRequireFolderNotIndexed asserts neither index still holds anything under
// folder.
func rfiRequireFolderNotIndexed(t *testing.T, home, root, folder string) {
	t.Helper()
	for _, hit := range rfiTextHits(t, home, root, rfiToken) {
		require.NotEqual(t, folder+"/Atlas.md", hit, "the text index still finds a file under %s", folder)
	}
	for p := range ifxPropPaths(t, home, root) {
		// A prefix, not a substring: after a move into Archive/, the live path
		// Archive/Projects/Atlas.md legitimately contains "Projects/".
		require.False(t, strings.HasPrefix(p, folder+"/"), "the properties index still holds %s", p)
	}
}

// TestRestructureRestore_FolderTrashedByTheLibraryIsInstantlyIndexed is the
// reported defect on its own: the folder is trashed exactly the way the
// Library's delete door trashes it (the engine, with the Folder flag), and an
// agent restores it through knowledge_restructure.
func TestRestructureRestore_FolderTrashedByTheLibraryIsInstantlyIndexed(t *testing.T) {
	home, ws, root, deps := rfiFixture(t)
	tool := NewRestructureTool(deps)

	colRoot, err := NewCollectionRoot(OSLinkFS(), root)
	require.NoError(t, err)
	trashed, err := (&Trasher{FS: OSLinkFS(), Root: colRoot}).Trash(TrashRequest{Path: "Projects", Folder: true})
	require.NoError(t, err)
	require.Empty(t, RemoveFromIndexesForFolderTrash(context.Background(), home, root, trashed))
	rfiRequireFolderNotIndexed(t, home, root, "Projects")

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"op": "restore", "collection": "kb", "path": "Projects",
	})
	require.False(t, res.IsError, "restore refused: %s", res.ForLLM)
	require.NotContains(t, res.ForLLM, "INDEX:", "the restore must refresh both indexes without a warning: %s", res.ForLLM)
	require.FileExists(t, filepath.Join(root, "Projects", "Atlas.md"))
	require.FileExists(t, filepath.Join(root, "Projects", "assets", "diagram.png"))
	rfiRequireFolderIndexed(t, home, root, "Projects")
}

// TestRestructureTool_FolderTrashAndRestoreRoundTripKeepsIndexesCurrent is the
// whole agent round trip: trash the folder and restore it, both through
// knowledge_restructure.
func TestRestructureTool_FolderTrashAndRestoreRoundTripKeepsIndexesCurrent(t *testing.T) {
	home, ws, root, deps := rfiFixture(t)
	tool := NewRestructureTool(deps)
	ctx := a4Ctx("mia", ws)

	trashRes := tool.Execute(ctx, map[string]any{
		"op": "trash", "collection": "kb", "path": "Projects", "folder": true,
	})
	require.False(t, trashRes.IsError, "folder trash refused: %s", trashRes.ForLLM)
	require.NotContains(t, trashRes.ForLLM, "INDEX:", "unexpected index problem: %s", trashRes.ForLLM)
	require.Contains(t, trashRes.ForLLM, "Projects -> trashed at")
	require.NoDirExists(t, filepath.Join(root, "Projects"))
	rfiRequireFolderNotIndexed(t, home, root, "Projects")

	restoreRes := tool.Execute(ctx, map[string]any{
		"op": "restore", "collection": "kb", "path": "Projects",
	})
	require.False(t, restoreRes.IsError, "folder restore refused: %s", restoreRes.ForLLM)
	require.NotContains(t, restoreRes.ForLLM, "INDEX:", "unexpected index problem: %s", restoreRes.ForLLM)
	require.Contains(t, restoreRes.ForLLM, "Projects <- restored from trash")
	require.Contains(t, restoreRes.ForLLM, "CASCADE: 2 inbound link(s) resolve again")
	rfiRequireFolderIndexed(t, home, root, "Projects")
}

// TestRestructureTool_FolderRenameKeepsIndexesCurrent renames the folder
// through the tool: the old paths leave both indexes, the new paths enter
// them, and the outside note whose links were rewritten is re-derived.
func TestRestructureTool_FolderRenameKeepsIndexesCurrent(t *testing.T) {
	home, ws, root, deps := rfiFixture(t)
	tool := NewRestructureTool(deps)

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"op": "rename", "collection": "kb", "path": "Projects", "new_name": "Ventures", "folder": true,
	})
	require.False(t, res.IsError, "folder rename refused: %s", res.ForLLM)
	require.NotContains(t, res.ForLLM, "INDEX:", "unexpected index problem: %s", res.ForLLM)
	require.Contains(t, res.ForLLM, "Projects -> Ventures")

	rfiRequireFolderNotIndexed(t, home, root, "Projects")
	rfiRequireFolderIndexed(t, home, root, "Ventures")

	outside, err := os.ReadFile(filepath.Join(root, "Outside.md"))
	require.NoError(t, err)
	require.Equal(t, "See [[Ventures/Atlas]] and ![[Ventures/assets/diagram.png]].\n", string(outside))
	require.Equal(t, propindex.SourceHash(outside), ifxPropPaths(t, home, root)["Outside.md"].SourceHash,
		"the outside note's properties row must be re-derived from its rewritten bytes")
}

// TestRestructureTool_FolderMoveKeepsIndexesCurrent moves the folder into a
// folder that does not exist yet.
func TestRestructureTool_FolderMoveKeepsIndexesCurrent(t *testing.T) {
	home, ws, root, deps := rfiFixture(t)
	tool := NewRestructureTool(deps)

	res := tool.Execute(a4Ctx("mia", ws), map[string]any{
		"op": "move", "collection": "kb", "path": "Projects", "new_folder": "Archive", "folder": true,
	})
	require.False(t, res.IsError, "folder move refused: %s", res.ForLLM)
	require.NotContains(t, res.ForLLM, "INDEX:", "unexpected index problem: %s", res.ForLLM)
	require.Contains(t, res.ForLLM, "Projects -> Archive/Projects")

	rfiRequireFolderNotIndexed(t, home, root, "Projects")
	rfiRequireFolderIndexed(t, home, root, "Archive/Projects")

	outside, err := os.ReadFile(filepath.Join(root, "Outside.md"))
	require.NoError(t, err)
	require.Equal(t, "See [[Archive/Projects/Atlas]] and ![[Archive/Projects/assets/diagram.png]].\n", string(outside))
}

// TestRestructureTool_WithoutTheFolderFlagAFolderIsNeverTouched pins the
// safety property the engine's Folder flag exists for, at the tool boundary:
// an agent naming "Projects" without folder=true means the note Projects.md,
// so the folder of the same name is left alone by every op.
func TestRestructureTool_WithoutTheFolderFlagAFolderIsNeverTouched(t *testing.T) {
	_, ws, root, deps := rfiFixture(t)
	tool := NewRestructureTool(deps)
	ctx := a4Ctx("mia", ws)

	for _, args := range []map[string]any{
		{"op": "trash", "collection": "kb", "path": "Projects"},
		{"op": "rename", "collection": "kb", "path": "Projects", "new_name": "Ventures"},
		{"op": "move", "collection": "kb", "path": "Projects", "new_folder": "Archive"},
	} {
		res := tool.Execute(ctx, args)
		require.True(t, res.IsError, "%s without folder=true must not reach the folder: %s", args["op"], res.ForLLM)
		require.FileExists(t, filepath.Join(root, "Projects", "Atlas.md"), "%s moved the folder", args["op"])
		require.FileExists(t, filepath.Join(root, "Projects", "assets", "diagram.png"))
	}
}

// TestRestructureTool_RestoreRefusesTheFolderFlag: restore finds a trashed
// folder by its original path on its own, and only when no trashed note
// answers that path. Accepting folder=true there would promise a choice the
// restore does not make, so it is refused with the reason.
func TestRestructureTool_RestoreRefusesTheFolderFlag(t *testing.T) {
	_, ws, _, deps := rfiFixture(t)
	res := NewRestructureTool(deps).Execute(a4Ctx("mia", ws), map[string]any{
		"op": "restore", "collection": "kb", "path": "Projects", "folder": true,
	})
	require.True(t, res.IsError, "restore must refuse folder=true: %s", res.ForLLM)
	require.Contains(t, res.ForLLM, "restore takes no 'folder'")
}

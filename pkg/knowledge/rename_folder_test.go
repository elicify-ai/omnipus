// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

// rename_folder_test.go - renaming and moving a FOLDER (round-4 attachment
// cascade, UAT D-123 / #701). Expected file contents are written out by hand
// from the link-spelling rules rename.go documents (a path-shaped wikilink
// stays path-shaped and keeps or omits ".md" as written; a markdown link is
// re-spelled relative to the note that holds it), never read back from the
// implementation.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const folderFixturePNG = "\x89PNG\r\n\x1a\nbinary"

// folderFixture: Projects/Atlas holds a note and an image; Home.md, outside
// the folder, links to the note three ways and embeds the image.
func folderFixture(t *testing.T, extra map[string]string) (string, CollectionRoot) {
	t.Helper()
	files := map[string]string{
		"Projects/Atlas/Plan.md": "# Plan\n\nDiagram: ![[Projects/Atlas/diagram.png]]\n" +
			"Beside me: [d](diagram.png)\nHome: [home](../../Home.md)\n",
		"Projects/Atlas/diagram.png": folderFixturePNG,
		"Home.md": "# Home\n\nSee [[Projects/Atlas/Plan]] and ![[Projects/Atlas/diagram.png]].\n" +
			"Also [the plan](Projects/Atlas/Plan.md) and [[Plan]].\n",
	}
	for k, v := range extra {
		files[k] = v
	}
	return a2Collection(t, files)
}

func TestRenameFolder_RewritesLinksAndEmbedsToEverythingInside(t *testing.T) {
	dir, root := folderFixture(t, nil)
	r := a2Renamer(t, root)

	res, err := r.Rename(RenameRequest{From: "Projects/Atlas", To: "Projects/Atlas-2026", Folder: true})
	require.NoError(t, err)

	assert.Equal(t, []PathMove{
		{From: "Projects/Atlas/Plan.md", To: "Projects/Atlas-2026/Plan.md"},
		{From: "Projects/Atlas/diagram.png", To: "Projects/Atlas-2026/diagram.png"},
	}, res.Moves)
	assert.NoDirExists(t, filepath.Join(dir, "Projects", "Atlas"))
	assert.Equal(t, folderFixturePNG, a2Read(t, dir, "Projects/Atlas-2026/diagram.png"))

	assert.Equal(t,
		"# Home\n\nSee [[Projects/Atlas-2026/Plan]] and ![[Projects/Atlas-2026/diagram.png]].\n"+
			"Also [the plan](Projects/Atlas-2026/Plan.md) and [[Plan]].\n",
		a2Read(t, dir, "Home.md"),
		"every link and embed into the folder is rewritten; the bare [[Plan]] still resolves and is left alone")
	assert.Equal(t,
		"# Plan\n\nDiagram: ![[Projects/Atlas-2026/diagram.png]]\n"+
			"Beside me: [d](diagram.png)\nHome: [home](../../Home.md)\n",
		a2Read(t, dir, "Projects/Atlas-2026/Plan.md"),
		"the note inside the folder has its path-shaped embed rewritten; its relative links are still correct at the same depth")
	assert.Equal(t, 4, res.LinksRewritten, "three in Home.md, one in Plan.md")

	pending, err := r.PendingJournals()
	require.NoError(t, err)
	assert.Empty(t, pending, "a completed folder rename leaves no journal behind")
}

func TestRenameFolder_MoveToAnotherDepthRespellsRelativeLinks(t *testing.T) {
	dir, root := folderFixture(t, map[string]string{"Archive/2026/Index.md": "# Index\n"})
	r := a2Renamer(t, root)

	_, err := r.Rename(RenameRequest{From: "Projects/Atlas", To: "Archive/2026/Atlas", Folder: true})
	require.NoError(t, err)

	assert.Equal(t,
		"# Home\n\nSee [[Archive/2026/Atlas/Plan]] and ![[Archive/2026/Atlas/diagram.png]].\n"+
			"Also [the plan](Archive/2026/Atlas/Plan.md) and [[Plan]].\n",
		a2Read(t, dir, "Home.md"))
	assert.Equal(t,
		"# Plan\n\nDiagram: ![[Archive/2026/Atlas/diagram.png]]\n"+
			"Beside me: [d](diagram.png)\nHome: [home](../../../Home.md)\n",
		a2Read(t, dir, "Archive/2026/Atlas/Plan.md"),
		"a link out of the folder gains one \"../\" for the extra level; a link to a sibling inside the folder is unchanged")
}

// A crash after the folder moved but before any link was rewritten must
// recover forward with the existing journal machinery. This is what pins that
// each step for a file inside the folder is recorded at its post-move path.
func TestRenameFolder_InterruptedAfterTheMoveCompletesOnRecovery(t *testing.T) {
	dir, root := folderFixture(t, nil)
	r := a2Renamer(t, root)

	plan, err := r.Plan(RenameRequest{From: "Projects/Atlas", To: "Projects/Atlas-2026", Folder: true})
	require.NoError(t, err)
	require.NoError(t, r.store().Write(plan.Journal))
	// The crash: the move happened, no rewrite did.
	require.NoError(t, os.Rename(filepath.Join(dir, "Projects", "Atlas"), filepath.Join(dir, "Projects", "Atlas-2026")))

	results, err := r.RecoverPending()
	require.NoError(t, err, "recovery must complete a folder rename interrupted after the move")
	require.Len(t, results, 1)
	assert.Equal(t, RecoverCompleted, results[0].Outcome)
	assert.Equal(t,
		"# Home\n\nSee [[Projects/Atlas-2026/Plan]] and ![[Projects/Atlas-2026/diagram.png]].\n"+
			"Also [the plan](Projects/Atlas-2026/Plan.md) and [[Plan]].\n",
		a2Read(t, dir, "Home.md"))
	assert.Contains(t, a2Read(t, dir, "Projects/Atlas-2026/Plan.md"), "![[Projects/Atlas-2026/diagram.png]]")
}

// Single-file behaviour is unchanged: without Folder a directory source is
// refused exactly as before, and nothing is touched.
func TestRename_FolderWithoutTheFolderFlagIsStillRefused(t *testing.T) {
	dir, root := folderFixture(t, nil)
	before := a2Snapshot(t, dir)
	r := a2Renamer(t, root)

	_, err := r.Rename(RenameRequest{From: "Projects/Atlas", To: "Projects/Atlas-2026"})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrRenameSourceNotAddressable)
	assert.Equal(t, before, a2Snapshot(t, dir))
}

func TestRenameFolder_FolderFlagOnAFileIsRefused(t *testing.T) {
	dir, root := folderFixture(t, nil)
	before := a2Snapshot(t, dir)
	r := a2Renamer(t, root)

	_, err := r.Rename(RenameRequest{From: "Home.md", To: "Start.md", Folder: true})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrRenameSourceNotAddressable)
	assert.Equal(t, before, a2Snapshot(t, dir))
}

// Moving a folder into itself is refused before a journal is written, so it
// cannot leave a pending journal that blocks every later rename.
func TestRenameFolder_IntoItselfIsRefusedWithoutAJournal(t *testing.T) {
	dir, root := folderFixture(t, nil)
	before := a2Snapshot(t, dir)
	r := a2Renamer(t, root)

	_, err := r.Rename(RenameRequest{From: "Projects/Atlas", To: "Projects/Atlas/Inner", Folder: true})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrRenameInvalidPath)
	assert.Equal(t, before, a2Snapshot(t, dir))
	pending, perr := r.PendingJournals()
	require.NoError(t, perr)
	assert.Empty(t, pending)
}

// A source spelled differently from the folder on disk must not move the
// folder. On a case-insensitive filesystem (macOS by default) the folder
// opens under either spelling, but no file in the link graph matches the
// wrong one, so the folder would move with every link into it left dangling.
// On a case-sensitive filesystem the wrong spelling simply does not exist.
// The assertions hold on both.
func TestRenameFolder_SourceSpelledDifferentlyOnDiskIsRefused(t *testing.T) {
	dir, root := folderFixture(t, nil)
	before := a2Snapshot(t, dir)
	r := a2Renamer(t, root)

	_, err := r.Rename(RenameRequest{From: "projects/atlas", To: "Projects/Atlas-2026", Folder: true})
	require.Error(t, err)
	assert.Equal(t, before, a2Snapshot(t, dir), "nothing moved and no link was touched")
}

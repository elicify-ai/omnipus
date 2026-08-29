// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

// rename_test.go — ADR-067 stage 3, unit A2.
//
// Spec tests covered here, by the numbering of §13.1:
//
//	43  TestRename_RewritesBodyAndFrontmatter            US-13 AS-1, AS-2, FR-103
//	44  TestRename_FrontmatterByteStableApartFromLink    US-13 AS-3, FR-105
//	45  TestRename_InterruptedIsDetectedAndCompletable   US-13 AS-4, FR-104, H-4
//
// Every expected value below is written from the spec's own words. The
// frontmatter fixtures are shaped after AC-10.3's "comments, anchors and nested
// lists", the four link forms after DS-1, and the interrupted case after H-4's
// "confirm you are told something is incomplete, that completing it fixes every
// link, and that no note was left pointing at a name that no longer exists".

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func a2Renamer(t *testing.T, root CollectionRoot) *Renamer {
	t.Helper()
	return &Renamer{Root: root, Store: NewJournalStore(filepath.Join(t.TempDir(), "journal"))}
}

// --- 43: bodies and frontmatter ------------------------------------------

func TestRename_RewritesBodyAndFrontmatter(t *testing.T) {
	files := map[string]string{
		"Old Note.md": "# Old Note\n\n## Section\n\nContent.\n",
		"body.md":     "See [[Old Note]], [[Old Note|the old one]] and [[Old Note#Section]].\n",
		"front.md":    "---\nup: \"[[Old Note]]\"\nrelated:\n  - \"[[Old Note]]\"\n---\n\nBody text mentioning nothing.\n",
		"path.md":     "A [markdown link](Old%20Note.md) and a [nested one](./Old%20Note.md#Section).\n",
		"deep/sub.md": "Up one: [[Old Note]] and [rel](../Old%20Note.md).\n",
		"nolinks.md":  "Nothing to see.\n",
	}
	dir, root := a2Collection(t, files)
	r := a2Renamer(t, root)

	res, err := r.Rename(RenameRequest{From: "Old Note.md", To: "Renamed Note.md"})
	require.NoError(t, err)
	require.NotNil(t, res)

	assert.FileExists(t, filepath.Join(dir, "Renamed Note.md"))
	assert.NoFileExists(t, filepath.Join(dir, "Old Note.md"))

	// AS-1: body links.
	assert.Equal(t,
		"See [[Renamed Note]], [[Renamed Note|the old one]] and [[Renamed Note#Section]].\n",
		a2Read(t, dir, "body.md"))

	// AS-2: frontmatter links — the ones Obsidian leaves broken.
	assert.Equal(t,
		"---\nup: \"[[Renamed Note]]\"\nrelated:\n  - \"[[Renamed Note]]\"\n---\n\nBody text mentioning nothing.\n",
		a2Read(t, dir, "front.md"))

	// Relative markdown links, including one from a subfolder.
	assert.Equal(t,
		"A [markdown link](Renamed%20Note.md) and a [nested one](Renamed%20Note.md#Section).\n",
		a2Read(t, dir, "path.md"))
	assert.Equal(t,
		"Up one: [[Renamed Note]] and [rel](../Renamed%20Note.md).\n",
		a2Read(t, dir, "deep/sub.md"))

	// A note with no inbound link is not rewritten at all.
	assert.Equal(t, files["nolinks.md"], a2Read(t, dir, "nolinks.md"))

	// The audit payload names every file, not only the renamed note.
	assert.ElementsMatch(t,
		[]string{"Old Note.md", "Renamed Note.md", "body.md", "front.md", "path.md", "deep/sub.md"},
		res.Touched)
	assert.Equal(t, 4, res.FilesRewritten)
	assert.Equal(t, 9, res.LinksRewritten)

	// The journal is gone once the operation is confirmed complete.
	pending, err := r.PendingJournals()
	require.NoError(t, err)
	assert.Empty(t, pending)
}

func TestRename_HandlesEveryLinkFormTheGraphResolves(t *testing.T) {
	const body = "" +
		"bare [[Old]]\n" +
		"aliased [[Old|display text]]\n" +
		"heading [[Old#Section]]\n" +
		"block [[Old#^abc123]]\n" +
		"path [[folder/Old]]\n" +
		"path with ext [[folder/Old.md]]\n" +
		"embed ![[Old]]\n" +
		"markdown [text](folder/Old.md)\n" +
		"markdown angle [text](<folder/Old.md>)\n" +
		"markdown titled [text](folder/Old.md \"a title\")\n" +
		"spaced [[ Old ]]\n" +
		"untouched [[Other]]\n"
	dir, root := a2Collection(t, map[string]string{
		"folder/Old.md": "# Old\n\n## Section\n",
		"Other.md":      "# Other\n",
		"notes.md":      body,
	})
	r := a2Renamer(t, root)
	_, err := r.Rename(RenameRequest{From: "folder/Old.md", To: "folder/New.md"})
	require.NoError(t, err)

	assert.Equal(t, ""+
		"bare [[New]]\n"+
		"aliased [[New|display text]]\n"+
		"heading [[New#Section]]\n"+
		"block [[New#^abc123]]\n"+
		"path [[folder/New]]\n"+
		"path with ext [[folder/New.md]]\n"+
		"embed ![[New]]\n"+
		"markdown [text](folder/New.md)\n"+
		"markdown angle [text](<folder/New.md>)\n"+
		"markdown titled [text](folder/New.md \"a title\")\n"+
		"spaced [[ New ]]\n"+
		"untouched [[Other]]\n",
		a2Read(t, dir, "notes.md"))
}

// --- 44: frontmatter byte stability --------------------------------------

// TestRename_FrontmatterByteStableApartFromLink is AC-10.3, byte-compared.
//
// The oracle is deliberately mechanical: undo the rename textually in the
// result and require the bytes to be identical to the original. Nothing else —
// not a comment, not a YAML anchor, not an alias, not the trailing whitespace
// after a value — may have moved.
func TestRename_FrontmatterByteStableApartFromLink(t *testing.T) {
	const front = "---\n" +
		"# how this note sits in the hierarchy\n" +
		"up: &parent \"[[Old Note]]\"    \n" +
		"related:\n" +
		"  - \"[[Old Note]]\"   # the same note again\n" +
		"  - plain value\n" +
		"nested:\n" +
		"  deeper:\n" +
		"    ref: \"[[Old Note|shown as this]]\"\n" +
		"aliased: *parent\n" +
		"tags: [a, b]\n" +
		"empty:\n" +
		"...\n" +
		"\nBody paragraph.\n"
	dir, root := a2Collection(t, map[string]string{
		"Old Note.md": "# Old Note\n",
		"front.md":    front,
	})
	r := a2Renamer(t, root)
	_, err := r.Rename(RenameRequest{From: "Old Note.md", To: "Renamed Note.md"})
	require.NoError(t, err)

	got := a2Read(t, dir, "front.md")
	require.NotEqual(t, front, got, "the fixture must actually contain links to rewrite")
	assert.Equal(t, 3, strings.Count(got, "Renamed Note"))
	assert.Equal(t, front, strings.ReplaceAll(got, "Renamed Note", "Old Note"),
		"only the link value may differ from the original frontmatter")
}

// --- 45 / H-4: the interrupted rename -------------------------------------

// a2LinkedCollection builds a subject note plus n notes that each link to it in
// body and frontmatter — H-4's "heavily-linked note".
func a2LinkedCollection(t *testing.T, n int) map[string]string {
	t.Helper()
	files := map[string]string{
		"Old Note.md": "# Old Note\n\n## Section\n",
	}
	for i := 1; i <= n; i++ {
		files[fmt.Sprintf("notes/note%02d.md", i)] = fmt.Sprintf(
			"---\nup: \"[[Old Note]]\"\n---\n\nNote %d refers to [[Old Note#Section]] and [rel](../Old%%20Note.md).\n", i)
	}
	return files
}

func TestRename_InterruptedIsDetectedAndCompletable(t *testing.T) {
	const inbound = 20
	files := a2LinkedCollection(t, inbound)

	// Reference run: the same rename, uninterrupted, in its own collection.
	refDir, refRoot := a2Collection(t, files)
	refRenamer := &Renamer{Root: refRoot, Store: NewJournalStore(DefaultJournalDir(refDir))}
	_, err := refRenamer.Rename(RenameRequest{From: "Old Note.md", To: "Renamed Note.md"})
	require.NoError(t, err)
	want := a2Snapshot(t, refDir)

	// Interrupted run.
	dir, root := a2Collection(t, files)
	store := NewJournalStore(DefaultJournalDir(dir))
	r := &Renamer{Root: root, Store: store}

	plan, err := r.Plan(RenameRequest{From: "Old Note.md", To: "Renamed Note.md"})
	require.NoError(t, err)
	j := plan.Journal
	require.Len(t, j.Steps, inbound, "every inbound note must be planned")

	// FR-104: the journal reaches disk before anything is touched.
	require.NoError(t, store.Write(j))
	assert.Equal(t, files["Old Note.md"], a2Read(t, dir, "Old Note.md"))
	assert.Equal(t, files["notes/note01.md"], a2Read(t, dir, "notes/note01.md"),
		"writing the journal must not have touched the collection")

	// The process gets as far as the move and 7 of the 20 rewrites, then dies.
	const done = 7
	require.NoError(t, performMove(OSLinkFS(), root, j))
	for i := 0; i < done; i++ {
		st := j.Steps[i]
		res := ApplyStep(OSLinkFS(), root, j.stepPath(st, true), st)
		require.Equal(t, StepApplied, res.Outcome, "%s: %s", st.RelPath, res.Detail)
	}

	// Positive control: the collection really is half-rewritten. Without this
	// the test could "recover" a collection that was never interrupted.
	assert.NoFileExists(t, filepath.Join(dir, "Old Note.md"))
	assert.NotContains(t, a2Read(t, dir, j.Steps[0].RelPath), "Old Note")
	lastRel := j.Steps[len(j.Steps)-1].RelPath
	assert.Contains(t, a2Read(t, dir, lastRel), "[[Old Note",
		"the un-rewritten notes must still point at a name that no longer exists")
	assert.NotEqual(t, want, a2Snapshot(t, dir))

	// A fresh process — no memory of what was in flight — checks the collection.
	fresh := &Renamer{Root: root, Store: NewJournalStore(DefaultJournalDir(dir))}
	pending, err := fresh.PendingJournals()
	require.NoError(t, err)
	require.Len(t, pending, 1, "the incomplete rename must be reported")
	assert.Equal(t, "Old Note.md", pending[0].From)
	assert.Equal(t, "Renamed Note.md", pending[0].To)

	results, err := fresh.RecoverPending()
	require.NoError(t, err)
	require.Len(t, results, 1)
	rec := results[0]
	assert.Equal(t, RecoverCompleted, rec.Outcome)
	assert.Equal(t, MoveDone, rec.MoveState)
	assert.False(t, rec.MovePerformed, "the move had already happened")

	var applied, already int
	for _, st := range rec.Steps {
		switch st.Outcome {
		case StepApplied:
			applied++
		case StepAlreadyApplied:
			already++
		default:
			t.Fatalf("unexpected outcome %q for %s: %s", st.Outcome, st.RelPath, st.Detail)
		}
	}
	assert.Equal(t, done, already)
	assert.Equal(t, inbound-done, applied)

	// H-4: completing it fixes every link, and no note is left pointing at a
	// name that no longer exists.
	assert.Equal(t, want, a2Snapshot(t, dir),
		"completing an interrupted rename must yield the same result as an uninterrupted one")

	graph, err := BuildLinkGraph(OSLinkFS(), root)
	require.NoError(t, err)
	assert.Empty(t, graph.Unresolved(), "no link may still point at the old name")

	after, err := fresh.PendingJournals()
	require.NoError(t, err)
	assert.Empty(t, after, "a completed operation leaves no journal behind")
}

// TestRename_InterruptedBeforeTheMoveIsAlsoCompletable is the other half of the
// crash window: the journal reached disk and nothing else did.
func TestRename_InterruptedBeforeTheMoveIsAlsoCompletable(t *testing.T) {
	files := a2LinkedCollection(t, 5)

	refDir, refRoot := a2Collection(t, files)
	ref := &Renamer{Root: refRoot, Store: NewJournalStore(DefaultJournalDir(refDir))}
	_, err := ref.Rename(RenameRequest{From: "Old Note.md", To: "Renamed Note.md"})
	require.NoError(t, err)
	want := a2Snapshot(t, refDir)

	dir, root := a2Collection(t, files)
	store := NewJournalStore(DefaultJournalDir(dir))
	r := &Renamer{Root: root, Store: store}
	plan, err := r.Plan(RenameRequest{From: "Old Note.md", To: "Renamed Note.md"})
	require.NoError(t, err)
	require.NoError(t, store.Write(plan.Journal))
	// ... and the process dies here.

	fresh := &Renamer{Root: root, Store: NewJournalStore(DefaultJournalDir(dir))}
	pending, err := fresh.PendingJournals()
	require.NoError(t, err)
	require.Len(t, pending, 1)

	results, err := fresh.RecoverPending()
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, MoveNotDone, results[0].MoveState)
	assert.True(t, results[0].MovePerformed)
	assert.Equal(t, want, a2Snapshot(t, dir))
}

// TestRename_JournalIsOnDiskBeforeAnyFileIsTouched is FR-104's durability half.
//
// The oracle is a rename that FAILS part-way for a reason nothing can fake
// away: one linked note sits in a directory the process cannot write to, so its
// rewrite cannot land. If the plan had only ever existed in memory, that
// failure would leave a collection with a moved file, a broken link and no
// record of what was supposed to happen. Because the plan is on disk first, the
// operation is still there to be finished — and finishing it is what the second
// half of this test does.
func TestRename_JournalIsOnDiskBeforeAnyFileIsTouched(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("a read-only directory does not stop root, so the oracle would not hold")
	}
	dir, root := a2Collection(t, map[string]string{
		"Old.md":        "# Old\n",
		"locked/ref.md": "See [[Old]].\n",
	})
	locked := filepath.Join(dir, "locked")
	require.NoError(t, os.Chmod(locked, 0o555))
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	r := &Renamer{Root: root, Store: NewJournalStore(DefaultJournalDir(dir))}
	_, err := r.Rename(RenameRequest{From: "Old.md", To: "New.md"})
	require.Error(t, err, "the rewrite cannot land, so the operation cannot report success")
	assert.ErrorIs(t, err, ErrJournalIncomplete)

	pending, listErr := r.PendingJournals()
	require.NoError(t, listErr)
	require.Len(t, pending, 1, "the plan must be on disk, or the half-done rename is unrecoverable")
	assert.Equal(t, "Old.md", pending[0].From)
	assert.Equal(t, "New.md", pending[0].To)

	// The obstacle goes away; the recorded plan finishes the job.
	require.NoError(t, os.Chmod(locked, 0o755))
	results, err := r.RecoverPending()
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, RecoverCompleted, results[0].Outcome)
	assert.Equal(t, "See [[New]].\n", a2Read(t, dir, "locked/ref.md"))

	after, err := r.PendingJournals()
	require.NoError(t, err)
	assert.Empty(t, after)
}

// --- ambiguity ------------------------------------------------------------

func TestRename_NewlyAmbiguousBasenameIsReportedAndRefused(t *testing.T) {
	dir, root := a2Collection(t, map[string]string{
		"Ideas.md":         "# Ideas at the root\n",
		"archive/Draft.md": "# A draft\n",
		"link.md":          "See [[archive/Draft]].\n",
	})
	r := a2Renamer(t, root)

	_, err := r.Rename(RenameRequest{From: "archive/Draft.md", To: "archive/Ideas.md"})
	require.Error(t, err, "an ambiguity the operator did not ask for must not be created silently")
	assert.ErrorIs(t, err, ErrRenameCreatesAmbiguity)

	var ambErr *AmbiguityError
	require.ErrorAs(t, err, &ambErr)
	assert.Equal(t, "Ideas", ambErr.Report.Basename)
	assert.Equal(t, []string{"Ideas.md", "archive/Ideas.md"}, ambErr.Report.Candidates,
		"candidates must be in the resolver's own tie-break order: shortest path first")
	assert.False(t, ambErr.Report.WasAmbiguous)

	// Refused before anything was touched.
	assert.FileExists(t, filepath.Join(dir, "archive/Draft.md"))
	assert.NoFileExists(t, filepath.Join(dir, "archive/Ideas.md"))
	assert.Equal(t, "See [[archive/Draft]].\n", a2Read(t, dir, "link.md"))
	pending, err := r.PendingJournals()
	require.NoError(t, err)
	assert.Empty(t, pending, "a refused rename writes no journal")
}

func TestRename_AllowedAmbiguityQualifiesBareLinks(t *testing.T) {
	dir, root := a2Collection(t, map[string]string{
		"Ideas.md":         "# Ideas at the root\n",
		"archive/Draft.md": "# A draft\n",
		"bare.md":          "See [[Draft]].\n",
	})
	r := a2Renamer(t, root)

	res, err := r.Rename(RenameRequest{From: "archive/Draft.md", To: "archive/Ideas.md", AllowAmbiguity: true})
	require.NoError(t, err)
	require.NotNil(t, res.Ambiguity, "the ambiguity must still be reported even when allowed")
	assert.Equal(t, "Ideas", res.Ambiguity.Basename)

	// A bare "[[Ideas]]" would resolve to the root note by tie-break, not to
	// the note just renamed — so the rewrite must qualify it with its path.
	assert.Equal(t, "See [[archive/Ideas]].\n", a2Read(t, dir, "bare.md"))

	graph, err := BuildLinkGraph(OSLinkFS(), root)
	require.NoError(t, err)
	links := graph.Links("bare.md")
	require.Len(t, links, 1)
	assert.Equal(t, "archive/Ideas.md", links[0].To, "the rewritten link must still reach the renamed note")
}

func TestRename_AlreadyAmbiguousNameIsNotRefused(t *testing.T) {
	// The name was ambiguous before the rename, so the rename is not what
	// created the ambiguity and must not be blamed for it.
	_, root := a2Collection(t, map[string]string{
		"Ideas.md":     "# one\n",
		"a/Ideas.md":   "# two\n",
		"b/Nothing.md": "# three\n",
	})
	r := a2Renamer(t, root)
	res, err := r.Rename(RenameRequest{From: "b/Nothing.md", To: "b/Ideas.md"})
	require.NoError(t, err)
	require.NotNil(t, res.Ambiguity)
	assert.True(t, res.Ambiguity.WasAmbiguous)
}

// --- no-op and case-only --------------------------------------------------

func TestRename_SamePathIsANoOpThatWritesNothing(t *testing.T) {
	dir, root := a2Collection(t, map[string]string{
		"Note.md": "# Note\n",
		"ref.md":  "See [[Note]].\n",
	})
	before := a2Snapshot(t, dir)
	r := a2Renamer(t, root)

	for _, to := range []string{"Note.md", "./Note.md"} {
		res, err := r.Rename(RenameRequest{From: "Note.md", To: to})
		require.NoError(t, err, "to=%q", to)
		assert.True(t, res.NoOp, "to=%q", to)
		assert.Empty(t, res.JournalID)
	}
	assert.Equal(t, before, a2Snapshot(t, dir), "a no-op must leave the collection byte-identical")
	pending, err := r.PendingJournals()
	require.NoError(t, err)
	assert.Empty(t, pending)
}

// TestRename_CaseOnlyRelabelIsSafe is the trap pkg/library documents: on a
// case-insensitive filesystem the destination "already exists" because it IS
// the source. It must be allowed, it must actually change the spelling on disk,
// and it must rewrite the links.
func TestRename_CaseOnlyRelabelIsSafe(t *testing.T) {
	dir, root := a2Collection(t, map[string]string{
		"Report.md": "# Report\n",
		"ref.md":    "See [[Report]] and [rel](Report.md).\n",
	})
	r := a2Renamer(t, root)

	res, err := r.Rename(RenameRequest{From: "Report.md", To: "report.md"})
	require.NoError(t, err, "a case-only relabel of the same entry must be allowed")
	assert.True(t, res.CaseOnly)

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	assert.Contains(t, names, "report.md", "the on-disk spelling must actually have changed")
	assert.NotContains(t, names, "Report.md")

	assert.Equal(t, "See [[report]] and [rel](report.md).\n", a2Read(t, dir, "ref.md"))
}

func TestRename_DestinationOccupiedByAnotherFileIsRefused(t *testing.T) {
	dir, root := a2Collection(t, map[string]string{
		"Old.md":      "# Old\n",
		"Existing.md": "# Existing, and precious\n",
	})
	r := a2Renamer(t, root)
	_, err := r.Rename(RenameRequest{From: "Old.md", To: "Existing.md"})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrRenameDestinationExists)
	assert.Equal(t, "# Existing, and precious\n", a2Read(t, dir, "Existing.md"))
	assert.FileExists(t, filepath.Join(dir, "Old.md"))
}

func TestRename_MissingDestinationDirectoryIsNamed(t *testing.T) {
	_, root := a2Collection(t, map[string]string{"Old.md": "# Old\n"})
	r := a2Renamer(t, root)
	_, err := r.Rename(RenameRequest{From: "Old.md", To: "nowhere/Old.md"})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrRenameDestinationParentMissing)
	assert.Contains(t, err.Error(), "nowhere")
}

func TestRename_MissingSourceIsRefused(t *testing.T) {
	_, root := a2Collection(t, map[string]string{"Other.md": "x"})
	r := a2Renamer(t, root)
	_, err := r.Rename(RenameRequest{From: "Absent.md", To: "New.md"})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrRenameSourceMissing)
}

func TestRename_SymlinkSourceIsRefused(t *testing.T) {
	dir, root := a2Collection(t, map[string]string{"real.md": "# real\n"})
	require.NoError(t, os.Symlink(filepath.Join(dir, "real.md"), filepath.Join(dir, "alias.md")))
	r := a2Renamer(t, root)
	_, err := r.Rename(RenameRequest{From: "alias.md", To: "moved.md"})
	require.Error(t, err, "FR-044: a symlink is skipped and reported, never followed")
	assert.ErrorIs(t, err, ErrRenameSourceNotAddressable)
	// This assertion is the correction of a test that passed for the wrong
	// reason. Until the symlink guard was wired into Plan, the refusal above
	// came from the walk-membership backstop — a symlink is never in
	// graph.Files() — and the lstat guard the test was written for could not
	// fire at all. Only ResolveContainedNoSymlink wraps ErrOutsideCollection,
	// so this is what tells the two refusals apart. The destination side, which
	// has no membership backstop and was therefore not refused at all, is
	// covered in rename_symlink_guard_test.go.
	assert.ErrorIs(t, err, ErrOutsideCollection)
	assert.NoFileExists(t, filepath.Join(dir, "moved.md"))
}

// --- containment ----------------------------------------------------------

func TestRename_RefusesPathsThatLeaveTheCollection(t *testing.T) {
	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "victim.md")
	require.NoError(t, os.WriteFile(outside, []byte("# precious\n"), 0o644))

	dir, root := a2Collection(t, map[string]string{"Old.md": "# Old\n"})
	r := a2Renamer(t, root)

	escapes := []RenameRequest{
		{From: "../" + filepath.Base(outsideDir) + "/victim.md", To: "Old.md"},
		{From: "Old.md", To: "../" + filepath.Base(outsideDir) + "/victim.md"},
		{From: "Old.md", To: "/etc/passwd"},
		{From: "/etc/passwd", To: "Old.md"},
		{From: "Old.md", To: "../../../../tmp/escaped.md"},
	}
	for _, req := range escapes {
		_, err := r.Rename(req)
		require.Error(t, err, "%q -> %q must be refused", req.From, req.To)
	}

	b, err := os.ReadFile(outside)
	require.NoError(t, err)
	assert.Equal(t, "# precious\n", string(b))
	assert.FileExists(t, filepath.Join(dir, "Old.md"))
}

// --- moving between folders ----------------------------------------------

// TestRename_MoveRespellsRelativeLinksInsideTheMovedNote is the consequence of
// a MOVE that a rename-only implementation misses: a markdown link is spelled
// relative to the note holding it, so moving that note changes the correct
// spelling of every markdown link in it — including ones pointing at notes the
// rename has nothing to do with.
func TestRename_MoveRespellsRelativeLinksInsideTheMovedNote(t *testing.T) {
	dir, root := a2Collection(t, map[string]string{
		"a/note.md":  "[to root](../Root.md)\n[to sibling](sib.md)\n[other](../b/other.md)\n[[Root]]\n[broken](gone.md)\n",
		"a/sib.md":   "# sibling\n",
		"Root.md":    "# root\n",
		"b/other.md": "# other\n",
		"inbound.md": "points at [[a/note]] and [rel](a/note.md)\n",
	})
	r := a2Renamer(t, root)
	_, err := r.Rename(RenameRequest{From: "a/note.md", To: "b/note.md"})
	require.NoError(t, err)

	assert.Equal(t,
		"[to root](../Root.md)\n[to sibling](../a/sib.md)\n[other](other.md)\n[[Root]]\n[broken](gone.md)\n",
		a2Read(t, dir, "b/note.md"))
	assert.Equal(t,
		"points at [[b/note]] and [rel](b/note.md)\n",
		a2Read(t, dir, "inbound.md"))

	// A link that was ALREADY broken before the move is left exactly as it was.
	// It cannot be respelled — nothing knows what it was meant to point at —
	// and inventing a spelling for it would turn a visible broken link into a
	// plausible-looking wrong one.
	graph, err := BuildLinkGraph(OSLinkFS(), root)
	require.NoError(t, err)
	unresolved := graph.Unresolved()
	require.Len(t, unresolved, 1, "only the link that was already broken may be unresolved")
	assert.Equal(t, "[broken](gone.md)", unresolved[0].Raw)
}

// --- audit ----------------------------------------------------------------

// TestRename_AuditsEveryOutcome is FR-090 / US-15: every mutation AND every
// refusal is on the record.
func TestRename_AuditsEveryOutcome(t *testing.T) {
	_, root := a2Collection(t, map[string]string{
		"Old.md":      "# Old\n",
		"a.md":        "[[Old]]\n",
		"Existing.md": "# taken\n",
	})
	var events []RenameAuditEvent
	r := &Renamer{
		Root:    root,
		Store:   NewJournalStore(filepath.Join(t.TempDir(), "journal")),
		AgentID: "mia",
		Audit:   func(ev RenameAuditEvent) { events = append(events, ev) },
	}

	_, err := r.Rename(RenameRequest{From: "Old.md", To: "Existing.md"})
	require.Error(t, err)
	require.Len(t, events, 1, "a refused write is audited as a refusal, not omitted")
	assert.Equal(t, RenameOutcomeRefused, events[0].Outcome)
	assert.Equal(t, "mia", events[0].AgentID)
	assert.Equal(t, root.Path(), events[0].Collection)
	assert.Equal(t, "knowledge.rename", events[0].Op)
	assert.NotEmpty(t, events[0].Reason)
	assert.False(t, events[0].At.IsZero())

	_, err = r.Rename(RenameRequest{From: "Old.md", To: "New.md"})
	require.NoError(t, err)
	require.Len(t, events, 2)
	applied := events[1]
	assert.Equal(t, RenameOutcomeApplied, applied.Outcome)
	assert.Equal(t, "Old.md", applied.From)
	assert.Equal(t, "New.md", applied.To)
	assert.NotEmpty(t, applied.JournalID)
	assert.ElementsMatch(t, []string{"Old.md", "New.md", "a.md"}, applied.Paths,
		"a multi-file rewrite records the full set of touched paths")
}

func TestRename_NilAuditHookIsNotAPanic(t *testing.T) {
	_, root := a2Collection(t, map[string]string{"Old.md": "x", "a.md": "[[Old]]\n"})
	r := a2Renamer(t, root)
	_, err := r.Rename(RenameRequest{From: "Old.md", To: "New.md"})
	require.NoError(t, err)
}

// --- pure helpers ---------------------------------------------------------

func TestRelFromDir_SpellsPathsRelativeToTheContainingNote(t *testing.T) {
	cases := []struct{ base, target, want string }{
		{"", "Root.md", "Root.md"},
		{".", "Root.md", "Root.md"},
		{"a", "a/sib.md", "sib.md"},
		{"a", "Root.md", "../Root.md"},
		{"a", "b/x.md", "../b/x.md"},
		{"a/b", "x.md", "../../x.md"},
		{"a/b", "a/c/x.md", "../c/x.md"},
		{"a/b", "a/b/x.md", "x.md"},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, relFromDir(tc.base, tc.target), "base=%q target=%q", tc.base, tc.target)
	}
}

func TestWikiTargetSpelling_PreservesTheAuthorsShape(t *testing.T) {
	cases := []struct {
		old, newRel string
		qualify     bool
		want        string
	}{
		{"Old", "folder/New.md", false, "New"},
		{"Old.md", "folder/New.md", false, "New.md"},
		{"folder/Old", "folder/New.md", false, "folder/New"},
		{"folder/Old.md", "folder/New.md", false, "folder/New.md"},
		{"Old", "folder/New.md", true, "folder/New"},
		{"diagram.png", "img/diagram-v4.png", false, "diagram-v4.png"},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, wikiTargetSpelling(tc.old, tc.newRel, tc.qualify),
			"old=%q new=%q qualify=%v", tc.old, tc.newRel, tc.qualify)
	}
}

func TestEncodeDestinationPath_EncodesOnlyWhatWouldBreakTheLink(t *testing.T) {
	assert.Equal(t, "New.md", encodeDestinationPath("New.md", "Old.md"))
	assert.Equal(t, "New%20Note.md", encodeDestinationPath("New Note.md", "Old.md"))
	assert.Equal(t, "New%28x%29.md", encodeDestinationPath("New(x).md", "Old.md"))
	// The original was percent-encoded, so the replacement stays encoded even
	// though it would not strictly need to be.
	assert.Equal(t, "New.md", encodeDestinationPath("New.md", "Old.md"))
	assert.Equal(t, "Ünïcode.md", encodeDestinationPath("Ünïcode.md", "Old.md"),
		"non-ASCII names are left readable")
}

// TestRename_CompletesAnInterruptedRenameBeforeStartingANewOne is FR-104's
// second clause on the path that actually runs.
//
// The journal-writing half was built and the recovery half was built, tested
// and never CALLED: nothing invoked RecoverPending — not at boot, not on mount
// attach, not from the drift check, and not from Rename itself. A process
// killed mid-rename therefore left the note moved, some inbound links pointing
// at a name that no longer exists, and a journal on disk no code path would
// ever read.
//
// The most damaging consequence was the next rename. Plan reads the collection
// AS IT STANDS; standing half-rewritten, the plan it produces is wrong for both
// halves, and a second journal lands beside the orphaned first. So a rename now
// finishes what it finds before planning anything, and the property asserted is
// convergence: the collection ends byte-identical to a clean run of the same
// two renames.
func TestRename_CompletesAnInterruptedRenameBeforeStartingANewOne(t *testing.T) {
	const inbound = 6
	files := a2LinkedCollection(t, inbound)

	// Reference: both renames, uninterrupted, in their own collection.
	refDir, refRoot := a2Collection(t, files)
	ref := &Renamer{Root: refRoot, Store: NewJournalStore(DefaultJournalDir(refDir))}
	_, err := ref.Rename(RenameRequest{From: "Old Note.md", To: "Renamed Note.md"})
	require.NoError(t, err)
	_, err = ref.Rename(RenameRequest{From: "Renamed Note.md", To: "Final Note.md"})
	require.NoError(t, err)
	want := a2Snapshot(t, refDir)

	// Interrupted: the first rename dies after the move and 2 of 6 rewrites.
	dir, root := a2Collection(t, files)
	store := NewJournalStore(DefaultJournalDir(dir))
	r := &Renamer{Root: root, Store: store}

	plan, err := r.Plan(RenameRequest{From: "Old Note.md", To: "Renamed Note.md"})
	require.NoError(t, err)
	j := plan.Journal
	require.NoError(t, store.Write(j))
	require.NoError(t, performMove(OSLinkFS(), root, j))
	for i := 0; i < 2; i++ {
		st := j.Steps[i]
		require.Equal(t, StepApplied, ApplyStep(OSLinkFS(), root, j.stepPath(st, true), st).Outcome)
	}

	// Positive control: the collection really is half-rewritten.
	lastRel := j.Steps[len(j.Steps)-1].RelPath
	require.Contains(t, a2Read(t, dir, lastRel), "[[Old Note",
		"the un-rewritten notes must still point at a name that no longer exists")
	pending, err := NewJournalStore(DefaultJournalDir(dir)).List()
	require.NoError(t, err)
	require.Len(t, pending, 1)

	// A fresh process issues the NEXT rename with no memory of what was in
	// flight. It must finish the first one rather than planning on top of it.
	fresh := &Renamer{Root: root, Store: NewJournalStore(DefaultJournalDir(dir))}
	_, err = fresh.Rename(RenameRequest{From: "Renamed Note.md", To: "Final Note.md"})
	require.NoError(t, err, "the second rename must complete the interrupted first one, not fail on it")

	assert.Equal(t, want, a2Snapshot(t, dir),
		"completing an interrupted rename and then renaming again must produce the same collection as two clean renames")

	left, err := NewJournalStore(DefaultJournalDir(dir)).List()
	require.NoError(t, err)
	assert.Empty(t, left, "no journal may be left behind once both operations are complete")
}

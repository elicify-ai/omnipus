// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

// journal_test.go — ADR-067 stage 3, unit A2.
//
// Expected values here come from the SPEC, not from reading the implementation:
// FR-104 (journal written before any file is touched; an interrupted rewrite is
// detectable and completable), FR-105 (only the link value changes), FR-043
// (every written path resolves inside the collection root, on the real path),
// and D14's "mtime alone is insufficient — the hash is the decision".

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- fixtures -------------------------------------------------------------

// a2Collection materialises files (collection-relative path -> contents) under
// a fresh temporary directory and returns the directory and its validated root.
func a2Collection(t *testing.T, files map[string]string) (string, CollectionRoot) {
	t.Helper()
	dir := t.TempDir()
	for rel, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte(body), 0o644))
	}
	root, err := NewCollectionRoot(OSLinkFS(), dir)
	require.NoError(t, err)
	return dir, root
}

// a2Snapshot records every file and directory under dir, so two collections can
// be compared as a whole rather than one assertion at a time.
func a2Snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	require.NoError(t, filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(dir, p)
		if relErr != nil {
			return relErr
		}
		if rel == "." {
			return nil
		}
		key := filepath.ToSlash(rel)
		if info.IsDir() {
			out[key+"/"] = "<dir>"
			return nil
		}
		b, readErr := os.ReadFile(p)
		if readErr != nil {
			return readErr
		}
		out[key] = string(b)
		return nil
	}))
	return out
}

func a2Read(t *testing.T, dir, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	require.NoError(t, err)
	return string(b)
}

// a2ValidJournal is a minimally-valid journal used as the base for the
// malformed-record table, so each row mutates exactly one thing.
func a2ValidJournal() *Journal {
	return &Journal{
		Version:   journalVersion,
		ID:        "20260823T120000000000000Z-0011223344556677",
		Op:        JournalOpRename,
		Root:      "/collection",
		From:      "Old.md",
		To:        "New.md",
		CreatedAt: time.Now().UTC(),
		Steps: []JournalStep{{
			RelPath:    "note.md",
			BeforeHash: hashBytes([]byte("before")),
			AfterHash:  hashBytes([]byte("after")),
			Edits:      []LinkEdit{{Offset: 0, Old: "[[Old]]", New: "[[New]]"}},
		}},
	}
}

// --- Validate -------------------------------------------------------------

func TestJournal_ValidateRejectsUnusableRecords(t *testing.T) {
	require.NoError(t, a2ValidJournal().Validate(), "the base record must be valid, or every row below passes vacuously")

	cases := []struct {
		name   string
		mutate func(*Journal)
	}{
		{"unknown version", func(j *Journal) { j.Version = journalVersion + 1 }},
		{"empty id", func(j *Journal) { j.ID = "" }},
		{"id carries a path separator", func(j *Journal) { j.ID = "../escape" }},
		{"unknown op", func(j *Journal) { j.Op = JournalOp("delete") }},
		{"empty root", func(j *Journal) { j.Root = "  " }},
		{"empty from", func(j *Journal) { j.From = "" }},
		{"from equals to", func(j *Journal) { j.To = j.From }},
		{"step with no path", func(j *Journal) { j.Steps[0].RelPath = "" }},
		{"two steps for one file", func(j *Journal) { j.Steps = append(j.Steps, j.Steps[0]) }},
		{"step with no edits", func(j *Journal) { j.Steps[0].Edits = nil }},
		{"step missing a hash", func(j *Journal) { j.Steps[0].AfterHash = "" }},
		{"step that changes nothing", func(j *Journal) { j.Steps[0].AfterHash = j.Steps[0].BeforeHash }},
		{"edit replacing nothing", func(j *Journal) { j.Steps[0].Edits[0].Old = "" }},
		{"overlapping edits", func(j *Journal) {
			j.Steps[0].Edits = []LinkEdit{
				{Offset: 10, Old: "[[Old]]", New: "[[New]]"},
				{Offset: 12, Old: "[[Old]]", New: "[[New]]"},
			}
		}},
		{"two subject steps", func(j *Journal) {
			j.Steps[0].Subject = true
			j.Steps = append(j.Steps, JournalStep{
				RelPath: "other.md", Subject: true,
				BeforeHash: "a", AfterHash: "b",
				Edits: []LinkEdit{{Offset: 0, Old: "x", New: "y"}},
			})
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			j := a2ValidJournal()
			tc.mutate(j)
			err := j.Validate()
			require.Error(t, err, "an unusable journal must be refused, not applied")
			assert.ErrorIs(t, err, ErrJournalInvalid)
		})
	}
}

// --- applyEdits -----------------------------------------------------------

func TestApplyEdits_SplicesOnlyTheRecordedSpans(t *testing.T) {
	src := []byte("alpha [[Old]] beta [[Old]] gamma")
	// Offsets derived by hand from the literal above, not read off a scanner.
	edits := []LinkEdit{
		{Offset: 6, Old: "[[Old]]", New: "[[New Name]]"},
		{Offset: 19, Old: "[[Old]]", New: "[[New Name]]"},
	}
	got, err := applyEdits(src, edits)
	require.NoError(t, err)
	assert.Equal(t, "alpha [[New Name]] beta [[New Name]] gamma", string(got))
}

func TestApplyEdits_RefusesWhenTheBytesMoved(t *testing.T) {
	src := []byte("alpha [[Old]] beta")
	_, err := applyEdits(src, []LinkEdit{{Offset: 5, Old: "[[Old]]", New: "[[New]]"}})
	require.Error(t, err, "an offset whose recorded text is not there must never be overwritten")
	assert.ErrorIs(t, err, ErrJournalEditMismatch)
}

func TestApplyEdits_RefusesPastEndOfFile(t *testing.T) {
	_, err := applyEdits([]byte("short"), []LinkEdit{{Offset: 3, Old: "[[Old]]", New: "[[New]]"}})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrJournalEditMismatch)
}

// --- store ----------------------------------------------------------------

func TestJournalStore_RoundTripsAndSecuresItsFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "journal")
	store := NewJournalStore(dir)

	pending, err := store.List()
	require.NoError(t, err, "no journal directory means no pending work, not an error")
	assert.Empty(t, pending)

	j := a2ValidJournal()
	var idErr error
	j.ID, idErr = newJournalID()
	require.NoError(t, idErr)
	require.NoError(t, store.Write(j))

	di, err := os.Stat(dir)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), di.Mode().Perm(), "the journal directory must be 0700")
	fi, err := os.Stat(store.path(j.ID))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm(), "a journal record must be 0600")

	loaded, err := store.Load(j.ID)
	require.NoError(t, err)
	assert.Equal(t, j.From, loaded.From)
	assert.Equal(t, j.To, loaded.To)
	assert.Equal(t, j.Steps, loaded.Steps, "the plan must survive the round trip exactly")

	require.NoError(t, store.Delete(j.ID))
	pending, err = store.List()
	require.NoError(t, err)
	assert.Empty(t, pending)
	require.NoError(t, store.Delete(j.ID), "deleting an absent journal is not an error")
}

func TestJournalStore_ListReportsUnreadableRecordsRatherThanSkippingThem(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "journal")
	store := NewJournalStore(dir)

	good := a2ValidJournal()
	var err error
	good.ID, err = newJournalID()
	require.NoError(t, err)
	require.NoError(t, store.Write(good))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "corrupt"+journalFileSuffix), []byte("{not json"), 0o600))

	list, err := store.List()
	require.Error(t, err, "a journal nobody can read still means the collection may be half-rewritten")
	assert.ErrorIs(t, err, ErrJournalInvalid)
	assert.Contains(t, err.Error(), "corrupt")
	require.Len(t, list, 1, "the readable journals must still be returned")
	assert.Equal(t, good.ID, list[0].ID)
}

func TestJournalStore_ListIsOldestFirst(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "journal")
	store := NewJournalStore(dir)
	var ids []string
	for i := 0; i < 5; i++ {
		j := a2ValidJournal()
		id, err := newJournalID()
		require.NoError(t, err)
		j.ID = id
		j.From = "Old.md"
		j.To = "New.md"
		require.NoError(t, store.Write(j))
		ids = append(ids, id)
		time.Sleep(time.Millisecond)
	}
	list, err := store.List()
	require.NoError(t, err)
	require.Len(t, list, 5)
	got := make([]string, 0, 5)
	for _, j := range list {
		got = append(got, j.ID)
	}
	assert.Equal(t, ids, got, "journals must replay in the order they were created")
	assert.True(t, sort.StringsAreSorted(got))
}

func TestNewJournalID_IsUnique(t *testing.T) {
	seen := map[string]struct{}{}
	for i := 0; i < 2000; i++ {
		id, err := newJournalID()
		require.NoError(t, err)
		_, dup := seen[id]
		require.False(t, dup, "a repeated journal id lets one plan overwrite another")
		seen[id] = struct{}{}
	}
}

// --- ApplyStep ------------------------------------------------------------

func a2Step(t *testing.T, before, after string, edits []LinkEdit) JournalStep {
	t.Helper()
	return JournalStep{
		RelPath:    "note.md",
		BeforeHash: hashBytes([]byte(before)),
		AfterHash:  hashBytes([]byte(after)),
		Edits:      edits,
	}
}

func TestApplyStep_ClassifiesFromDiskAlone(t *testing.T) {
	const before = "See [[Old]] here.\n"
	const after = "See [[New]] here.\n"
	edit := []LinkEdit{{Offset: 4, Old: "[[Old]]", New: "[[New]]"}}

	t.Run("not yet applied", func(t *testing.T) {
		dir, root := a2Collection(t, map[string]string{"note.md": before})
		res := ApplyStep(OSLinkFS(), root, "note.md", a2Step(t, before, after, edit))
		assert.Equal(t, StepApplied, res.Outcome, res.Detail)
		assert.Equal(t, after, a2Read(t, dir, "note.md"))
	})

	t.Run("already applied", func(t *testing.T) {
		dir, root := a2Collection(t, map[string]string{"note.md": after})
		res := ApplyStep(OSLinkFS(), root, "note.md", a2Step(t, before, after, edit))
		assert.Equal(t, StepAlreadyApplied, res.Outcome, res.Detail)
		assert.Equal(t, after, a2Read(t, dir, "note.md"), "an already-applied step must write nothing")
	})

	t.Run("changed by somebody else", func(t *testing.T) {
		const foreign = "Somebody else rewrote this note entirely.\n"
		dir, root := a2Collection(t, map[string]string{"note.md": foreign})
		res := ApplyStep(OSLinkFS(), root, "note.md", a2Step(t, before, after, edit))
		assert.Equal(t, StepConflict, res.Outcome)
		assert.Equal(t, foreign, a2Read(t, dir, "note.md"), "a conflicting file must be left exactly as found")
	})

	t.Run("gone", func(t *testing.T) {
		_, root := a2Collection(t, map[string]string{"other.md": "x"})
		res := ApplyStep(OSLinkFS(), root, "note.md", a2Step(t, before, after, edit))
		assert.Equal(t, StepMissing, res.Outcome)
	})
}

// TestApplyStep_MtimePreservedChangeIsStillDetected is D14's central claim,
// asserted directly: the decision is the content hash, so an external write
// that restores the modification time is still caught. An implementation that
// compared mtime and size would pass every other test in this file.
func TestApplyStep_MtimePreservedChangeIsStillDetected(t *testing.T) {
	const before = "See [[Old]] here.\n"
	const after = "See [[New]] here.\n"
	// Same byte length as `before`, so size is identical too.
	const foreign = "See [[Xyz]] here.\n"
	require.Len(t, foreign, len(before), "the fixture must keep size constant or the test proves nothing")

	dir, root := a2Collection(t, map[string]string{"note.md": before})
	full := filepath.Join(dir, "note.md")
	original, err := os.Stat(full)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(full, []byte(foreign), 0o644))
	require.NoError(t, os.Chtimes(full, original.ModTime(), original.ModTime()))
	restored, err := os.Stat(full)
	require.NoError(t, err)
	require.True(t, restored.ModTime().Equal(original.ModTime()), "the fixture must actually restore mtime")
	require.Equal(t, original.Size(), restored.Size())

	res := ApplyStep(OSLinkFS(), root, "note.md",
		a2Step(t, before, after, []LinkEdit{{Offset: 4, Old: "[[Old]]", New: "[[New]]"}}))
	assert.Equal(t, StepConflict, res.Outcome, "mtime and size are unchanged; only a hash can detect this")
	assert.Equal(t, foreign, a2Read(t, dir, "note.md"))
}

// TestApplyStep_RefusesToWriteOutsideTheCollection is FR-043 on the WRITE side.
// A journal is a file on disk and its contents are as attacker-controllable as
// a link inside a note.
func TestApplyStep_RefusesToWriteOutsideTheCollection(t *testing.T) {
	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "victim.md")
	const victim = "See [[Old]] here.\n"
	require.NoError(t, os.WriteFile(outside, []byte(victim), 0o644))

	_, root := a2Collection(t, map[string]string{"note.md": victim})
	step := a2Step(t, victim, "See [[New]] here.\n", []LinkEdit{{Offset: 4, Old: "[[Old]]", New: "[[New]]"}})

	for _, rel := range []string{
		"../" + filepath.Base(outsideDir) + "/victim.md",
		"../../etc/passwd",
		outside,
	} {
		res := ApplyStep(OSLinkFS(), root, rel, step)
		assert.Equal(t, StepConflict, res.Outcome, "step at %q must be refused", rel)
	}
	b, err := os.ReadFile(outside)
	require.NoError(t, err)
	assert.Equal(t, victim, string(b), "the file outside the collection must be untouched")
}

// TestApplyStep_RefusesToWriteThroughASymlink is FR-044 for the rewrite path.
//
// # Why this test asserts Detail, and why the in-collection case is the point
//
// The first version of this test used ONE fixture — a symlink pointing OUTSIDE
// the collection — and asserted only res.Outcome == StepConflict. That made it
// unfalsifiable: the out-of-collection link is refused by the CONTAINMENT
// check, not by the symlink guard, so deleting the symlink guard outright left
// the test green (mutation confirmed: 426 pass, and this test among them, with
// the guard gone).
//
// Worse, the behaviour it claimed to protect did not hold. ResolveContained
// resolves every symlink on the way, so an lstat of the RESOLVED path sees a
// regular file: an IN-COLLECTION symlink pointing at a real note was written
// straight through, the link's target rewritten, outcome "applied".
//
// So: three fixtures, each naming the reason it is refused, and the assertion
// is on the REASON. A guard whose test cannot distinguish "refused by me" from
// "refused by something else" is a guard nobody is testing.
func TestApplyStep_RefusesToWriteThroughASymlink(t *testing.T) {
	const victim = "See [[Old]] here.\n"
	step := func() JournalStep {
		return a2Step(t, victim, "See [[New]] here.\n", []LinkEdit{{Offset: 4, Old: "[[Old]]", New: "[[New]]"}})
	}

	t.Run("a symlink to a note INSIDE the collection", func(t *testing.T) {
		dir, root := a2Collection(t, map[string]string{"real.md": victim})
		require.NoError(t, os.Symlink(filepath.Join(dir, "real.md"), filepath.Join(dir, "link.md")))

		res := ApplyStep(OSLinkFS(), root, "link.md", step())
		assert.Equal(t, StepConflict, res.Outcome)
		assert.Contains(t, res.Detail, "path is a symbolic link",
			"FR-044: the refusal must come from the symlink guard, not from something else")
		got, err := os.ReadFile(filepath.Join(dir, "real.md"))
		require.NoError(t, err)
		assert.Equal(t, victim, string(got),
			"FR-044: writing through an in-collection symlink rewrites the note it points at")
	})

	t.Run("a dangling symlink", func(t *testing.T) {
		dir, root := a2Collection(t, map[string]string{"real.md": victim})
		require.NoError(t, os.Symlink(filepath.Join(dir, "gone.md"), filepath.Join(dir, "dangling.md")))

		res := ApplyStep(OSLinkFS(), root, "dangling.md", step())
		assert.Equal(t, StepConflict, res.Outcome)
		assert.Contains(t, res.Detail, "path is a symbolic link")
	})

	t.Run("a symlink OUT of the collection", func(t *testing.T) {
		outside := filepath.Join(t.TempDir(), "victim.md")
		require.NoError(t, os.WriteFile(outside, []byte(victim), 0o600))
		dir, root := a2Collection(t, map[string]string{"real.md": victim})
		require.NoError(t, os.Symlink(outside, filepath.Join(dir, "note.md")))

		res := ApplyStep(OSLinkFS(), root, "note.md", step())
		assert.Equal(t, StepConflict, res.Outcome)
		assert.Contains(t, res.Detail, "path is a symbolic link",
			"the symlink is the more specific reason; containment would refuse it too, "+
				"which is exactly why asserting only the outcome proved nothing")
		b, err := os.ReadFile(outside)
		require.NoError(t, err)
		assert.Equal(t, victim, string(b), "FR-044: a symlink is never written through")
	})

	// Positive control: an ORDINARY note at the same kind of path IS rewritten,
	// so the three refusals above are not simply "ApplyStep never applies".
	t.Run("positive control: a real note is rewritten", func(t *testing.T) {
		dir, root := a2Collection(t, map[string]string{"real.md": victim})
		res := ApplyStep(OSLinkFS(), root, "real.md", step())
		require.Equal(t, StepApplied, res.Outcome, res.Detail)
		got, err := os.ReadFile(filepath.Join(dir, "real.md"))
		require.NoError(t, err)
		assert.Equal(t, "See [[New]] here.\n", string(got))
	})
}

// --- MoveStateOf ----------------------------------------------------------

func TestMoveStateOf_ReadsTheMoveFromTheFilesystem(t *testing.T) {
	t.Run("not done", func(t *testing.T) {
		_, root := a2Collection(t, map[string]string{"Old.md": "x"})
		st, err := MoveStateOf(root, &Journal{From: "Old.md", To: "New.md"})
		require.NoError(t, err)
		assert.Equal(t, MoveNotDone, st)
	})
	t.Run("done", func(t *testing.T) {
		_, root := a2Collection(t, map[string]string{"New.md": "x"})
		st, err := MoveStateOf(root, &Journal{From: "Old.md", To: "New.md"})
		require.NoError(t, err)
		assert.Equal(t, MoveDone, st)
	})
	t.Run("both present is refused, not guessed", func(t *testing.T) {
		_, root := a2Collection(t, map[string]string{"Old.md": "x", "New.md": "y"})
		st, err := MoveStateOf(root, &Journal{From: "Old.md", To: "New.md"})
		require.NoError(t, err)
		assert.Equal(t, MoveIndeterminate, st)
	})
	t.Run("neither present is refused, not guessed", func(t *testing.T) {
		_, root := a2Collection(t, map[string]string{"other.md": "x"})
		st, err := MoveStateOf(root, &Journal{From: "Old.md", To: "New.md"})
		require.NoError(t, err)
		assert.Equal(t, MoveIndeterminate, st)
	})
}

// TestMoveStateOf_CaseOnlyUsesTheDirectorySpelling is the trap pkg/library
// documents: on a case-insensitive filesystem both spellings stat successfully
// whether or not the relabel has happened, so only the directory's own spelling
// of the entry distinguishes the two states. The assertion holds identically on
// a case-sensitive filesystem, which is why it is not platform-gated.
func TestMoveStateOf_CaseOnlyUsesTheDirectorySpelling(t *testing.T) {
	j := &Journal{From: "Note.md", To: "note.md", CaseOnly: true}

	_, rootBefore := a2Collection(t, map[string]string{"Note.md": "x"})
	st, err := MoveStateOf(rootBefore, j)
	require.NoError(t, err)
	assert.Equal(t, MoveNotDone, st, "the entry is still spelled Note.md")

	_, rootAfter := a2Collection(t, map[string]string{"note.md": "x"})
	st, err = MoveStateOf(rootAfter, j)
	require.NoError(t, err)
	assert.Equal(t, MoveDone, st, "the entry is spelled note.md")
}

// --- Recover --------------------------------------------------------------

func TestRecover_RefusesAJournalFromAnotherCollection(t *testing.T) {
	_, root := a2Collection(t, map[string]string{"Old.md": "x"})
	store := NewJournalStore(filepath.Join(t.TempDir(), "journal"))
	j := a2ValidJournal()
	j.Root = "/some/other/collection"
	_, err := store.Recover(OSLinkFS(), root, j)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrJournalInvalid)
}

func TestRecover_IsIdempotent(t *testing.T) {
	dir, root := a2Collection(t, map[string]string{
		"Old.md":   "# Old\n",
		"note.md":  "See [[Old]].\n",
		"other.md": "Also [[Old]].\n",
	})
	r := &Renamer{Root: root, Store: NewJournalStore(filepath.Join(t.TempDir(), "journal"))}
	plan, err := r.Plan(RenameRequest{From: "Old.md", To: "New.md"})
	require.NoError(t, err)
	require.NoError(t, r.Store.Write(plan.Journal))

	first, err := r.Store.Recover(OSLinkFS(), root, plan.Journal)
	require.NoError(t, err)
	assert.Equal(t, RecoverCompleted, first.Outcome)
	after := a2Snapshot(t, dir)

	// Re-running a completed journal must change nothing. This is the property
	// that makes forward-only recovery safe to retry after a partial failure.
	second, err := r.Store.Recover(OSLinkFS(), root, plan.Journal)
	require.NoError(t, err)
	assert.Equal(t, RecoverCompleted, second.Outcome)
	assert.Equal(t, MoveDone, second.MoveState)
	assert.False(t, second.MovePerformed)
	for _, st := range second.Steps {
		assert.Equal(t, StepAlreadyApplied, st.Outcome, st.RelPath)
	}
	assert.Equal(t, after, a2Snapshot(t, dir))
}

func TestRecover_BlockedJournalIsRetainedAndNamed(t *testing.T) {
	dir, root := a2Collection(t, map[string]string{
		"Old.md":    "# Old\n",
		"keep.md":   "See [[Old]].\n",
		"stolen.md": "Also [[Old]].\n",
	})
	store := NewJournalStore(filepath.Join(t.TempDir(), "journal"))
	r := &Renamer{Root: root, Store: store}
	plan, err := r.Plan(RenameRequest{From: "Old.md", To: "New.md"})
	require.NoError(t, err)
	require.NoError(t, store.Write(plan.Journal))

	// Another writer edits one of the planned files between plan and apply.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "stolen.md"), []byte("rewritten elsewhere\n"), 0o644))

	rec, err := store.Recover(OSLinkFS(), root, plan.Journal)
	require.Error(t, err, "a plan that cannot be completed must say so")
	assert.ErrorIs(t, err, ErrJournalIncomplete)
	require.NotNil(t, rec)
	assert.Equal(t, RecoverBlocked, rec.Outcome)
	require.Len(t, rec.Conflicts, 1)
	assert.Equal(t, "stolen.md", rec.Conflicts[0].RelPath)

	assert.Equal(t, "See [[New]].\n", a2Read(t, dir, "keep.md"), "the files that could be finished are finished")
	assert.Equal(t, "rewritten elsewhere\n", a2Read(t, dir, "stolen.md"), "the other writer's bytes are never overwritten")

	pending, err := store.List()
	require.NoError(t, err)
	require.Len(t, pending, 1, "a blocked journal stays on disk so the operation stays visible")
}

// TestRecover_RefusesWhenTheDestinationNameIsOccupied is what actually protects
// a file the rename never named. The destination here is a SYMLINK to another
// note — the shape that would let an implementation resolving paths before
// renaming write straight through it — and the refusal happens before any
// filesystem work, because a destination that exists means the move state
// cannot be decided.
func TestRecover_RefusesWhenTheDestinationNameIsOccupied(t *testing.T) {
	const victim = "# precious, and not part of this rename\n"
	dir, root := a2Collection(t, map[string]string{
		"Old.md":    "# Old\n",
		"Victim.md": victim,
	})
	require.NoError(t, os.Symlink(filepath.Join(dir, "Victim.md"), filepath.Join(dir, "New.md")))

	store := NewJournalStore(filepath.Join(t.TempDir(), "journal"))
	j := a2ValidJournal()
	id, err := newJournalID()
	require.NoError(t, err)
	j.ID = id
	j.Root = root.Path()
	j.Steps = nil

	state, err := MoveStateOf(root, j)
	require.NoError(t, err)
	assert.Equal(t, MoveIndeterminate, state)

	_, err = store.Recover(OSLinkFS(), root, j)
	require.Error(t, err, "an occupied destination must be refused, not guessed about")
	assert.ErrorIs(t, err, ErrJournalIndeterminate)
	assert.Equal(t, victim, a2Read(t, dir, "Victim.md"), "the note the link pointed at must be untouched")
	assert.FileExists(t, filepath.Join(dir, "Old.md"))
}

// TestJournal_PathsCarriesEveryTouchedFile is US-15 AS-2: the audit payload is
// the full set, "not just the renamed note".
func TestJournal_PathsCarriesEveryTouchedFile(t *testing.T) {
	_, root := a2Collection(t, map[string]string{
		"Old.md":       "# Old\n",
		"a.md":         "[[Old]]\n",
		"deep/b.md":    "[[Old]]\n",
		"unrelated.md": "nothing here\n",
	})
	r := &Renamer{Root: root, Store: NewJournalStore(filepath.Join(t.TempDir(), "journal"))}
	plan, err := r.Plan(RenameRequest{From: "Old.md", To: "New.md"})
	require.NoError(t, err)
	assert.Equal(t, []string{"New.md", "Old.md", "a.md", "deep/b.md"}, plan.Journal.Paths())
}

func TestJournal_RecordIsPlainReadableJSON(t *testing.T) {
	// A recovery may have to be understood by a human at 2am. The record is
	// therefore indented JSON with stable field names, not an opaque blob.
	j := a2ValidJournal()
	data, err := json.MarshalIndent(j, "", "  ")
	require.NoError(t, err)
	text := string(data)
	for _, want := range []string{`"from"`, `"to"`, `"steps"`, `"before_hash"`, `"after_hash"`, `"offset"`, `"old"`, `"new"`} {
		assert.Contains(t, text, want)
	}
	assert.True(t, strings.Contains(text, "\n  "), "the record must be indented")
}

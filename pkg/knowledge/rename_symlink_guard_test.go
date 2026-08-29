// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

// rename_symlink_guard_test.go — FR-044 on the rename/move path, and FR-111 on
// the bytes it reads (ADR-067 D10, D6).
//
// WHY THESE TESTS ARE SEPARATE FROM rename_test.go's OWN SYMLINK TEST. There
// already was one — TestRename_SymlinkSourceIsRefused — and it passed against
// the broken code. It asserted the OUTCOME (an error, of the right sentinel)
// and the refusal it observed came from the walk-membership backstop further
// down Plan: a symlink is never in graph.Files(), so "not addressable" was
// returned for a reason that has nothing to do with symlinks. The guard the
// test was written for could not fire at all, because it lstat'ed a path
// ResolveContained had already dereferenced.
//
// So every case below asserts the REASON, and each one first proves it is the
// case it claims to be:
//
//  1. plain ResolveContained must ACCEPT the path — otherwise the refusal
//     could be ordinary containment (FR-043) and the test would pass with the
//     symlink guard deleted; and
//  2. the resolved path must DIFFER from the lexical one — otherwise no link
//     was traversed and there is nothing here to refuse.
//
// The discriminator between the two possible refusals is
// errors.Is(err, ErrOutsideCollection): the membership backstop wraps only
// ErrRenameSourceNotAddressable, so it can never satisfy that, while
// ResolveContainedNoSymlink always does.

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// g4Fixture builds the one collection every case here uses:
//
//	Real.md              an ordinary note
//	Cites.md             an ordinary note linking to Real
//	Archive/Sub/Keep.md  an ordinary note two folders down
//	Alias.md    -> Real.md      a LEAF symlink, target inside the collection
//	Inbox       -> Archive/     a DIRECTORY symlink, target inside
//
// Everything a symlink reaches stays inside the root, which is what makes
// FR-043 accept all of it and FR-044 the only rule that can refuse.
func g4Fixture(t *testing.T) (string, CollectionRoot) {
	t.Helper()
	dir, root := a2Collection(t, map[string]string{
		"Real.md":             "# Real\n",
		"Cites.md":            "See [[Real]] for the details.\n",
		"Archive/Sub/Keep.md": "# Keep\n",
	})
	require.NoError(t, os.Symlink(filepath.Join(dir, "Real.md"), filepath.Join(dir, "Alias.md")))
	require.NoError(t, os.Symlink(filepath.Join(dir, "Archive"), filepath.Join(dir, "Inbox")))
	return dir, root
}

// g4Snapshot records every entry under dir, symlinks included and recorded as
// their target rather than dereferenced. a2Snapshot cannot be reused here: it
// os.ReadFile's every non-directory entry, which fails outright on a link to a
// directory — and a collection containing symlinks is the entire subject.
func g4Snapshot(t *testing.T, dir string) map[string]string {
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
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, lErr := os.Readlink(p)
			if lErr != nil {
				return lErr
			}
			out[key+" ->"] = target
		case info.IsDir():
			out[key+"/"] = "<dir>"
		default:
			b, readErr := os.ReadFile(p)
			if readErr != nil {
				return readErr
			}
			out[key] = string(b)
		}
		return nil
	}))
	return out
}

// g4RequireOnlySymlinkCanRefuse is the precondition both halves of the class
// need. It fails the test — rather than the assertion under it — when the
// fixture is not the one the case claims, which is the failure mode that let
// the original test pass for four months.
func g4RequireOnlySymlinkCanRefuse(t *testing.T, root CollectionRoot, rel string) {
	t.Helper()
	resolved, err := root.ResolveContained(OSLinkFS(), rel)
	require.NoError(t, err,
		"fixture check: FR-043 containment must ACCEPT %q, or this case is not the "+
			"one it claims to be and would pass with the symlink guard removed", rel)
	require.True(t, root.Contains(resolved),
		"fixture check: %q must resolve to somewhere INSIDE the collection", rel)
	lexical := filepath.Join(root.Path(), filepath.FromSlash(rel))
	require.NotEqual(t, lexical, resolved,
		"fixture check: the resolved path must differ from the named one, or no symlink "+
			"was traversed and there is nothing here to refuse")
}

// TestRename_RefusesEveryPathOnlyReachableThroughASymlink covers BOTH ends.
//
// The two ends were not equally broken and the difference is the whole reason
// this test exists. On the source side a leaf symlink was refused anyway, by
// the membership backstop. On the DESTINATION side nothing looked: a
// destination does not exist yet, so no walk can be asked whether it is a
// member, and `move` into a symlinked folder was accepted outright.
func TestRename_RefusesEveryPathOnlyReachableThroughASymlink(t *testing.T) {
	for _, tc := range []struct {
		name     string
		from, to string
		side     error
	}{
		{
			name: "source is a leaf symlink",
			from: "Alias.md", to: "Moved.md",
			side: ErrRenameSourceNotAddressable,
		},
		{
			name: "source is reached through a symlinked folder",
			from: "Inbox/Sub/Keep.md", to: "Moved.md",
			side: ErrRenameSourceNotAddressable,
		},
		{
			name: "destination is reached through a symlinked folder",
			from: "Real.md", to: "Inbox/Sub/Moved.md",
			side: ErrRenameDestinationNotAddressable,
		},
		{
			name: "destination is an existing leaf symlink",
			from: "Real.md", to: "Alias.md",
			side: ErrRenameDestinationNotAddressable,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, root := g4Fixture(t)
			named := tc.from
			if tc.side == ErrRenameDestinationNotAddressable {
				named = tc.to
			}
			g4RequireOnlySymlinkCanRefuse(t, root, named)

			before := g4Snapshot(t, dir)
			_, err := a2Renamer(t, root).Rename(RenameRequest{From: tc.from, To: tc.to})

			require.Error(t, err, "FR-044: %q -> %q traverses a symbolic link", tc.from, tc.to)
			assert.ErrorIs(t, err, tc.side)
			assert.ErrorIs(t, err, ErrOutsideCollection,
				"the refusal must come from the FR-044 symlink guard. Neither the "+
					"walk-membership backstop nor checkDestination wraps "+
					"ErrOutsideCollection, so this assertion is what distinguishes "+
					"'refused by the guard' from 'refused by something else'")
			assert.Equal(t, before, g4Snapshot(t, dir),
				"a refused rename must leave the collection byte-identical")
		})
	}
}

// TestRename_MoveIntoASymlinkedFolderDoesNotRelocateTheNote is the consequence
// the sentinel assertions above stand for, spelled out on disk.
//
// Before the fix this rename SUCCEEDED: Real.md was deleted from its own name,
// the bytes appeared at Archive/Sub/Moved.md, and the audit record and the
// tool's own reply both said the note was now at "Inbox/Sub/Moved.md" — a path
// vault_read refuses to open, because the read path has enforced FR-044 since
// c06bb051. The operator is told where the note went and cannot go there.
func TestRename_MoveIntoASymlinkedFolderDoesNotRelocateTheNote(t *testing.T) {
	dir, root := g4Fixture(t)
	g4RequireOnlySymlinkCanRefuse(t, root, "Inbox/Sub/Moved.md")

	_, err := a2Renamer(t, root).Rename(RenameRequest{From: "Real.md", To: "Inbox/Sub/Moved.md"})
	require.Error(t, err)

	assert.FileExists(t, filepath.Join(dir, "Real.md"),
		"the note must still answer to the name it had")
	assert.NoFileExists(t, filepath.Join(dir, "Archive", "Sub", "Moved.md"),
		"nothing may be created at the path the symlink actually points at — that is "+
			"the file the caller never named")
	assert.Equal(t, "See [[Real]] for the details.\n", a2Read(t, dir, "Cites.md"),
		"no link may be rewritten for a rename that did not happen")
}

// TestApplyStep_RefusesAStepThroughASymlinkedParentDirectory closes the half
// of ApplyStep's guard that its own lexical lstat could not reach.
//
// That lstat asks about the LEAF only. "Inbox/Sub/Keep.md" is not itself a
// symlink, so it passed; ResolveContained then handed back
// Archive/Sub/Keep.md, which lstats as an ordinary regular file, and the step
// wrote through it. The journal's BeforeHash comparison limits the damage —
// see the eviction test below for why "limits" is not "removes" — but a
// compare-and-swap is not a containment guard.
func TestApplyStep_RefusesAStepThroughASymlinkedParentDirectory(t *testing.T) {
	const victim = "See [[Old]] here.\n"
	dir, root := a2Collection(t, map[string]string{"Archive/Sub/Keep.md": victim})
	require.NoError(t, os.Symlink(filepath.Join(dir, "Archive"), filepath.Join(dir, "Inbox")))

	g4RequireOnlySymlinkCanRefuse(t, root, "Inbox/Sub/Keep.md")
	require.False(t,
		func() bool {
			fi, err := os.Lstat(filepath.Join(dir, "Inbox", "Sub", "Keep.md"))
			return err == nil && fi.Mode()&fs.ModeSymlink != 0
		}(),
		"fixture check: the LEAF must not be a symlink, or the pre-existing leaf "+
			"guard would refuse this and the parent hole would go untested")

	step := a2Step(t, victim, "See [[New]] here.\n", []LinkEdit{{Offset: 4, Old: "[[Old]]", New: "[[New]]"}})
	res := ApplyStep(OSLinkFS(), root, "Inbox/Sub/Keep.md", step)

	assert.Equal(t, StepConflict, res.Outcome)
	assert.Contains(t, res.Detail, "only through a symbolic link",
		"the refusal must name the traversed link, not a hash mismatch")
	assert.Equal(t, victim, a2Read(t, dir, "Archive/Sub/Keep.md"),
		"FR-044: writing through a symlinked PARENT rewrites the note it points at")
}

// TestRenameBuildStep_RefusesANoteReachedThroughASymlink tests buildStep
// DIRECTLY, and the directness is the point.
//
// buildStep is fed by graph.Notes() today, and the walk never yields a path
// that traverses a symlink, so no end-to-end fixture can reach its guard. That
// is exactly the argument that left the other four sites in this class open:
// each of them was also "protected by its caller" right up until a caller
// changed. Mutating the guard back to plain ResolveContained and running the
// whole package is green — this test is what makes that mutation fail, so the
// guard is covered rather than merely present.
func TestRenameBuildStep_RefusesANoteReachedThroughASymlink(t *testing.T) {
	const body = "See [[Old]] here.\n"
	dir, root := a2Collection(t, map[string]string{"Archive/Sub/Keep.md": body})
	require.NoError(t, os.Symlink(filepath.Join(dir, "Archive"), filepath.Join(dir, "Inbox")))
	g4RequireOnlySymlinkCanRefuse(t, root, "Inbox/Sub/Keep.md")

	r := a2Renamer(t, root)
	edits := []LinkEdit{{Offset: 4, Old: "[[Old]]", New: "[[New]]"}}

	// The same edits against the note's REAL name must succeed, so the refusal
	// below cannot be an edit that simply does not apply.
	ok, okErr := r.buildStep(OSLinkFS(), "Archive/Sub/Keep.md", false, edits)
	require.NoError(t, okErr)
	require.NotNil(t, ok)

	step, err := r.buildStep(OSLinkFS(), "Inbox/Sub/Keep.md", false, edits)
	require.Error(t, err)
	assert.Nil(t, step)
	assert.ErrorIs(t, err, ErrOutsideCollection)
	assert.Equal(t, body, a2Read(t, dir, "Archive/Sub/Keep.md"),
		"planning must not have read or altered the note the link points at")
}

// --- FR-111 on the rename path -------------------------------------------

// g4EvictingFS is the OS filesystem, except that opening one named file
// behaves the way a dematerialised iCloud Drive / OneDrive Files-On-Demand /
// rclone VFS file does: the directory entry still reports the real size and
// the read returns nothing, on a CLEAN EOF. No errno, nothing to notice.
//
// from is 1-based and says which open starts evicting, so a fixture can let
// the link graph read the note honestly and evict it before the plan re-reads
// it — the real ordering, since the two reads are separate syscalls.
type g4EvictingFS struct {
	inner  LinkFS
	victim string
	from   int
	opens  int
}

func (e *g4EvictingFS) Lstat(n string) (fs.FileInfo, error)     { return e.inner.Lstat(n) }
func (e *g4EvictingFS) ReadDir(n string) ([]fs.DirEntry, error) { return e.inner.ReadDir(n) }
func (e *g4EvictingFS) EvalSymlinks(n string) (string, error)   { return e.inner.EvalSymlinks(n) }

func (e *g4EvictingFS) Open(n string) (fs.File, error) {
	if n == e.victim {
		e.opens++
		if e.opens >= e.from {
			fi, err := e.inner.Lstat(n)
			if err != nil {
				return nil, err
			}
			return g4EmptyFile{fi}, nil
		}
	}
	return e.inner.Open(n)
}

type g4EmptyFile struct{ fi fs.FileInfo }

func (f g4EmptyFile) Stat() (fs.FileInfo, error) { return f.fi, nil }
func (f g4EmptyFile) Read([]byte) (int, error)   { return 0, io.EOF }
func (f g4EmptyFile) Close() error               { return nil }

// TestRenamePlan_NamesEvictionRatherThanBlamingThePlan is the honest scope of
// the readWholeFile removal.
//
// Nothing was ever lost here: an evicted note read as zero bytes cannot match
// the edit offsets the graph computed from its real bytes, so applyEdits
// refuses and the whole rename is refused with it. The defect is the SENTENCE,
// and it is worth a test because the sentence is what an operator acts on.
// "journal edit does not match file contents: edit 0 spans [4,11) of a 0-byte
// file" sends someone hunting a concurrent writer, or filing a bug against the
// planner. The file is 29 bytes and the planner is fine; the provider has not
// materialised it. FR-111 already knows how to say that, and now this path
// asks it.
func TestRenamePlan_NamesEvictionRatherThanBlamingThePlan(t *testing.T) {
	dir, root := a2Collection(t, map[string]string{
		"Old.md":   "# Old\n",
		"Cites.md": "See [[Old]] for the details.\n",
	})
	fsys := &g4EvictingFS{
		inner:  OSLinkFS(),
		victim: filepath.Join(root.Path(), "Cites.md"),
		from:   2, // the graph reads it honestly; the plan re-read gets nothing
	}
	r := a2Renamer(t, root)
	r.FS = fsys

	_, err := r.Rename(RenameRequest{From: "Old.md", To: "New.md"})

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNoteEvicted,
		"FR-111: a note whose stat size disagrees with what the read returned is "+
			"evicted, and must be reported as such rather than as a mismatched plan")
	assert.NotErrorIs(t, err, ErrJournalEditMismatch,
		"blaming the plan for the filesystem is the whole defect")
	require.Equal(t, 2, fsys.opens,
		"fixture check: the plan must have re-read the note, or the eviction was "+
			"never reached and this test proves nothing")

	assert.FileExists(t, filepath.Join(dir, "Old.md"))
	assert.NoFileExists(t, filepath.Join(dir, "New.md"))
	assert.Equal(t, "See [[Old]] for the details.\n", a2Read(t, dir, "Cites.md"))
}

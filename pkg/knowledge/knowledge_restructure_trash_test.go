// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

// knowledge_restructure_trash_test.go — Stage 4 wave 1: the Trasher engine
// behind knowledge_restructure's trash/restore ops.
//
// Expected values are taken from the trash convention design note
// (docs/internal/design/vault-trash-convention-2026-08-28.md) and spec Draft
// 9 §4.1.5/FR-048/FR-048a/FR-048b/FR-038a, not read off the implementation.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func rtTrasher(t *testing.T, root CollectionRoot) *Trasher {
	t.Helper()
	return &Trasher{Root: root}
}

// --- Trash: the basic move + receipt ---------------------------------------

func TestTrash_MovesFileAndWritesReceipt(t *testing.T) {
	dir, root := a2Collection(t, map[string]string{
		"Deals/Acme Corp.md": "# Acme\n\nContent.\n",
	})
	tr := rtTrasher(t, root)

	res, err := tr.Trash(TrashRequest{Path: "Deals/Acme Corp.md"})
	require.NoError(t, err)
	require.NotNil(t, res)

	// The note's bytes are untouched, and the source no longer exists.
	assert.NoFileExists(t, filepath.Join(dir, "Deals/Acme Corp.md"))
	trashedAbs := filepath.Join(dir, filepath.FromSlash(res.TrashPath))
	assert.FileExists(t, trashedAbs)
	got, rerr := os.ReadFile(trashedAbs)
	require.NoError(t, rerr)
	assert.Equal(t, "# Acme\n\nContent.\n", string(got))

	// The trash path preserves the original relative path under the
	// timestamped directory (design note §1).
	assert.Equal(t, ".omnipus-vault/trash/"+res.TrashID+"/Deals/Acme Corp.md", res.TrashPath)

	// entry.json exists and names the original path (§7).
	receiptAbs := filepath.Join(dir, ".omnipus-vault", "trash", res.TrashID, "entry.json")
	raw, rerr := os.ReadFile(receiptAbs)
	require.NoError(t, rerr)
	var receipt trashReceipt
	require.NoError(t, json.Unmarshal(raw, &receipt))
	assert.Equal(t, "Deals/Acme Corp.md", receipt.OriginalPath)
	assert.NotEmpty(t, receipt.TrashedAt)
	assert.NotEmpty(t, receipt.SourceVersion)
}

// --- Trash: dangling links are counted and named, never repaired -----------

func TestTrash_ReportsDanglingLinksWithoutRepairingThem(t *testing.T) {
	dir, root := a2Collection(t, map[string]string{
		"Acme.md":  "# Acme\n",
		"body.md":  "See [[Acme]] for details.\n",
		"front.md": "---\nclient: \"[[Acme]]\"\n---\n\nBody.\n",
	})
	tr := rtTrasher(t, root)

	res, err := tr.Trash(TrashRequest{Path: "Acme.md"})
	require.NoError(t, err)
	assert.Equal(t, 2, res.DanglingLinkCount)
	assert.ElementsMatch(t, []string{"body.md", "front.md"}, res.DanglingNotes)

	// FR-048: not repaired. The linking notes are byte-for-byte unchanged.
	body, rerr := os.ReadFile(filepath.Join(dir, "body.md"))
	require.NoError(t, rerr)
	assert.Equal(t, "See [[Acme]] for details.\n", string(body))
	front, rerr2 := os.ReadFile(filepath.Join(dir, "front.md"))
	require.NoError(t, rerr2)
	assert.Equal(t, "---\nclient: \"[[Acme]]\"\n---\n\nBody.\n", string(front))
}

// --- Trash: F6 — refuses a source already inside .omnipus-vault/ -----------

func TestTrash_RefusesSourceInsideReservedDirectory(t *testing.T) {
	_, root := a2Collection(t, map[string]string{
		".omnipus-vault/trash/20260101T000000Z/Old.md": "content",
		".obsidian/workspace.json":                     "{}",
	})
	tr := rtTrasher(t, root)

	for _, p := range []string{
		".omnipus-vault/trash/20260101T000000Z/Old.md",
		".obsidian/workspace.json",
	} {
		_, err := tr.Trash(TrashRequest{Path: p})
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrReservedLocation)
	}
}

// --- Trash: refuses a missing source -----------------------------------

func TestTrash_RefusesMissingSource(t *testing.T) {
	_, root := a2Collection(t, map[string]string{"Keep.md": "x"})
	tr := rtTrasher(t, root)

	_, err := tr.Trash(TrashRequest{Path: "Nope.md"})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrTrashSourceMissing)
}

// --- Trash: same-second collision gets a distinct directory, never a clobber

func TestTrash_SameTimestampCollisionAllocatesDistinctDirectory(t *testing.T) {
	_, root := a2Collection(t, map[string]string{
		"A.md": "a",
		"B.md": "b",
	})
	tr := &Trasher{Root: root, Now: fixedClock}

	r1, err := tr.Trash(TrashRequest{Path: "A.md"})
	require.NoError(t, err)
	r2, err := tr.Trash(TrashRequest{Path: "B.md"})
	require.NoError(t, err)

	assert.NotEqual(t, r1.TrashID, r2.TrashID)
	assert.FileExists(t, filepath.Join(root.Path(), filepath.FromSlash(r1.TrashPath)))
	assert.FileExists(t, filepath.Join(root.Path(), filepath.FromSlash(r2.TrashPath)))
}

// --- Trash: a second trash of the same original path is reported, not refused

func TestTrash_SecondTrashOfSamePathIsReportedNotRefused(t *testing.T) {
	dir, root := a2Collection(t, map[string]string{"Note.md": "v1"})
	tr := rtTrasher(t, root)

	first, err := tr.Trash(TrashRequest{Path: "Note.md"})
	require.NoError(t, err)

	// Recreate the note (a legitimate second life for the same path) and
	// trash it again.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Note.md"), []byte("v2"), 0o644))
	second, err := tr.Trash(TrashRequest{Path: "Note.md"})
	require.NoError(t, err)

	assert.NotEqual(t, first.TrashID, second.TrashID)
	assert.Equal(t, []string{first.TrashID}, second.PriorTrashings)
}

// --- Restore: the round trip ------------------------------------------------

func TestRestore_RoundTrip(t *testing.T) {
	dir, root := a2Collection(t, map[string]string{
		"Deals/Acme Corp.md": "# Acme\n",
		"body.md":            "See [[Acme Corp]].\n",
	})
	tr := rtTrasher(t, root)

	_, err := tr.Trash(TrashRequest{Path: "Deals/Acme Corp.md"})
	require.NoError(t, err)
	assert.NoFileExists(t, filepath.Join(dir, "Deals/Acme Corp.md"))

	res, err := tr.Restore(RestoreRequest{Path: "Deals/Acme Corp.md"})
	require.NoError(t, err)
	assert.Equal(t, "Deals/Acme Corp.md", res.OriginalPath)

	got, rerr := os.ReadFile(filepath.Join(dir, "Deals/Acme Corp.md"))
	require.NoError(t, rerr)
	assert.Equal(t, "# Acme\n", string(got))

	// The inbound link resolves again (reported, not rewritten — it was
	// never broken in bytes, only in resolution).
	assert.Equal(t, 1, res.ResolvedLinksCount)

	// The now-empty timestamped instance directory is pruned; the structural
	// .omnipus-vault/trash/ parent is left in place (it is not this note's
	// to remove — other trash entries, present or future, live there too).
	assert.NoDirExists(t, filepath.Join(dir, ".omnipus-vault", "trash", res.RestoredFrom))
}

// --- Restore: no trashed note at that path ----------------------------

func TestRestore_RefusesWhenNothingTrashed(t *testing.T) {
	_, root := a2Collection(t, map[string]string{"Keep.md": "x"})
	tr := rtTrasher(t, root)

	_, err := tr.Restore(RestoreRequest{Path: "Never/Trashed.md"})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrRestoreNotFound)
}

// --- Restore: refuses to overwrite a live note at the destination ----------

func TestRestore_RefusesWhenDestinationAlreadyExists(t *testing.T) {
	dir, root := a2Collection(t, map[string]string{"Note.md": "trashed-version"})
	tr := rtTrasher(t, root)

	_, err := tr.Trash(TrashRequest{Path: "Note.md"})
	require.NoError(t, err)

	// A new, unrelated note now occupies the original path.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Note.md"), []byte("new-live-note"), 0o644))

	_, err = tr.Restore(RestoreRequest{Path: "Note.md"})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrRestoreDestinationExists)

	// The live note is untouched by the refused restore.
	got, rerr := os.ReadFile(filepath.Join(dir, "Note.md"))
	require.NoError(t, rerr)
	assert.Equal(t, "new-live-note", string(got))
}

// --- Restore: FR-038a — a live record already holding the identifier -------

func TestRestore_RefusesIdentifierCollisionWithLiveRecord(t *testing.T) {
	dir, root := a2Collection(t, map[string]string{
		"Acme.md": "---\ntype: company\nid: CO-0142\n---\n\nOriginal.\n",
	})
	tr := rtTrasher(t, root)

	_, err := tr.Trash(TrashRequest{Path: "Acme.md"})
	require.NoError(t, err)
	assert.NoFileExists(t, filepath.Join(dir, "Acme.md"))

	// A different note is created holding the SAME identifier while the
	// original is in the trash.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Acme Reissued.md"),
		[]byte("---\ntype: company\nid: CO-0142\n---\n\nReissued.\n"), 0o644))

	_, err = tr.Restore(RestoreRequest{Path: "Acme.md"})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrRestoreIdentifierCollision)

	// Neither file was touched by the refusal.
	assert.NoFileExists(t, filepath.Join(dir, "Acme.md"))
	assert.FileExists(t, filepath.Join(dir, "Acme Reissued.md"))
}

// --- Restore: FR-048b — the reconstructed destination is contained ---------

func TestRestore_RefusesReservedDestination(t *testing.T) {
	_, root := a2Collection(t, map[string]string{"Keep.md": "x"})
	tr := rtTrasher(t, root)

	_, err := tr.Restore(RestoreRequest{Path: ".omnipus-vault/sneaky.md"})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrReservedLocation)
}

// --- Restore: trashed_at selects an explicit older copy ---------------

func TestRestore_TrashedAtSelectsExplicitCopy(t *testing.T) {
	dir, root := a2Collection(t, map[string]string{"Note.md": "v1"})
	tr := &Trasher{Root: root, Now: fixedClockSeq(0)}

	first, err := tr.Trash(TrashRequest{Path: "Note.md"})
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "Note.md"), []byte("v2"), 0o644))
	tr2 := &Trasher{Root: root, Now: fixedClockSeq(1)}
	second, err := tr2.Trash(TrashRequest{Path: "Note.md"})
	require.NoError(t, err)
	require.NotEqual(t, first.TrashID, second.TrashID)

	res, err := tr.Restore(RestoreRequest{Path: "Note.md", TrashedAt: first.TrashID})
	require.NoError(t, err)
	assert.Equal(t, first.TrashID, res.RestoredFrom)
	assert.Equal(t, []string{second.TrashID}, res.OtherAvailable)

	got, rerr := os.ReadFile(filepath.Join(dir, "Note.md"))
	require.NoError(t, rerr)
	assert.Equal(t, "v1", string(got))
}

// --- Restore: an unknown trashed_at names what IS available ---------------

func TestRestore_UnknownTrashedAtNamesAvailable(t *testing.T) {
	_, root := a2Collection(t, map[string]string{"Note.md": "v1"})
	tr := rtTrasher(t, root)

	first, err := tr.Trash(TrashRequest{Path: "Note.md"})
	require.NoError(t, err)

	_, err = tr.Restore(RestoreRequest{Path: "Note.md", TrashedAt: "20200101T000000Z"})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrRestoreNotFound)
	assert.Contains(t, err.Error(), first.TrashID)
}

// --- Audit: every outcome is recorded, including refusals -------------

func TestTrash_AuditsBothAppliedAndRefused(t *testing.T) {
	_, root := a2Collection(t, map[string]string{"Note.md": "x"})
	var events []TrashAuditEvent
	tr := &Trasher{Root: root, Audit: func(ev TrashAuditEvent) { events = append(events, ev) }}

	_, err := tr.Trash(TrashRequest{Path: "Note.md"})
	require.NoError(t, err)
	_, err = tr.Trash(TrashRequest{Path: "Note.md"}) // now missing — refused
	require.Error(t, err)

	require.Len(t, events, 2)
	assert.Equal(t, "applied", events[0].Outcome)
	assert.Equal(t, "refused", events[1].Outcome)
	assert.NotEmpty(t, events[1].Reason)
}

// --- fixedClock helpers -----------------------------------------------

// fixedClock and fixedClockSeq give Trash a deterministic Now so tests can
// assert on TrashID directly rather than parsing whatever wall-clock second
// the test happened to run in.
func fixedClock() time.Time { return time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC) }

func fixedClockSeq(offsetSeconds int) func() time.Time {
	base := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	return func() time.Time { return base.Add(time.Duration(offsetSeconds) * time.Second) }
}

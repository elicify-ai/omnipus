//go:build goolm && stdjson

package memory

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
)

// seedToolLines appends n role:"tool" lines whose tool_call_id is ids[i]
// (ids may repeat — B-29b duplicates) and returns nothing; line i of the
// archive carries ids[i].
func seedToolLines(t *testing.T, store *JSONLStore, key string, ids []string) {
	t.Helper()
	ctx := context.Background()
	for _, id := range ids {
		err := store.AddFullMessage(ctx, key, providers.Message{
			Role: "tool", ToolCallID: id, Content: "result for " + id,
		})
		if err != nil {
			t.Fatalf("AddFullMessage(%s): %v", id, err)
		}
	}
}

// TestSessionMeta_ProjectionStateCompositeKey — spec test 15 (B-12, B-29b,
// B-27): projection state is keyed (tool_call_id, archive_line) so two lines
// sharing a tool_call_id are tracked independently; the state survives a
// meta round trip (reload), and TruncateHistory prunes entries with
// archive_line < Skip (US-6.AC9) while keeping the rest (US-6.AC8).
func TestSessionMeta_ProjectionStateCompositeKey(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	const key = "proj-composite"

	// Lines 0..4; call_0 appears twice (lines 0 and 3) — B-29b.
	seedToolLines(t, store, key, []string{"call_0", "call_1", "call_2", "call_0", "call_4"})

	// Mark line 0 (call_0, first occurrence) emptied and line 3 (call_0,
	// second occurrence) capped — same id, different lines, different states.
	if err := store.SetProjectionState(ctx, key, ProjectionKey{ToolCallID: "call_0", ArchiveLine: 0}, ProjectionEmptied); err != nil {
		t.Fatalf("SetProjectionState(line 0): %v", err)
	}
	if err := store.SetProjectionState(ctx, key, ProjectionKey{ToolCallID: "call_0", ArchiveLine: 3}, ProjectionCapped); err != nil {
		t.Fatalf("SetProjectionState(line 3): %v", err)
	}
	if err := store.SetProjectionState(ctx, key, ProjectionKey{ToolCallID: "call_4", ArchiveLine: 4}, ProjectionCapped); err != nil {
		t.Fatalf("SetProjectionState(line 4): %v", err)
	}

	// Reload through a fresh store instance — the state must come back
	// byte-for-byte from the meta file, not from process memory (B-12,
	// US-6.AC3 reload half).
	reopened, err := NewJSONLStore(store.dir)
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}
	pm, err := reopened.GetProjection(ctx, key)
	if err != nil {
		t.Fatalf("GetProjection: %v", err)
	}
	want := ProjectionSet{
		{ToolCallID: "call_0", ArchiveLine: 0}: ProjectionEmptied,
		{ToolCallID: "call_0", ArchiveLine: 3}: ProjectionCapped,
		{ToolCallID: "call_4", ArchiveLine: 4}: ProjectionCapped,
	}
	assertProjectionEqual(t, "after reload", pm.Entries, want)

	// Re-marking the same composite key overwrites (capped → emptied), it
	// does not add a second entry.
	if err = reopened.SetProjectionState(ctx, key, ProjectionKey{ToolCallID: "call_0", ArchiveLine: 3}, ProjectionEmptied); err != nil {
		t.Fatalf("SetProjectionState(overwrite): %v", err)
	}
	pm, err = reopened.GetProjection(ctx, key)
	if err != nil {
		t.Fatalf("GetProjection: %v", err)
	}
	want[ProjectionKey{ToolCallID: "call_0", ArchiveLine: 3}] = ProjectionEmptied
	assertProjectionEqual(t, "after overwrite", pm.Entries, want)

	// Advance Skip to 3 (keep last 2 of 5 lines): entries with
	// archive_line < 3 are pruned; lines 3 and 4 stay (B-27).
	if err = reopened.TruncateHistory(ctx, key, 2); err != nil {
		t.Fatalf("TruncateHistory: %v", err)
	}
	meta, err := reopened.readMeta(key)
	if err != nil {
		t.Fatalf("readMeta: %v", err)
	}
	if meta.Skip != 3 {
		t.Fatalf("skip = %d, want 3", meta.Skip)
	}
	pm, err = reopened.GetProjection(ctx, key)
	if err != nil {
		t.Fatalf("GetProjection: %v", err)
	}
	assertProjectionEqual(t, "after prune", pm.Entries, ProjectionSet{
		{ToolCallID: "call_0", ArchiveLine: 3}: ProjectionEmptied,
		{ToolCallID: "call_4", ArchiveLine: 4}: ProjectionCapped,
	})

	// Invalid inputs are refused, never silently stored.
	if err = reopened.SetProjectionState(ctx, key, ProjectionKey{ToolCallID: "", ArchiveLine: 4}, ProjectionCapped); err == nil {
		t.Error("empty tool_call_id accepted, want error")
	}
	if err = reopened.SetProjectionState(ctx, key, ProjectionKey{ToolCallID: "x", ArchiveLine: -1}, ProjectionCapped); err == nil {
		t.Error("negative archive_line accepted, want error")
	}
	if err = reopened.SetProjectionState(ctx, key, ProjectionKey{ToolCallID: "x", ArchiveLine: 4}, ProjectionState("full")); err == nil {
		t.Error("unknown state accepted, want error")
	}
	pm, err = reopened.GetProjection(ctx, key)
	if err != nil {
		t.Fatalf("GetProjection: %v", err)
	}
	if len(pm.Entries) != 2 {
		t.Errorf("invalid writes changed the set: %v", pm.Entries)
	}

	// The ProjectionMeta.Hydrated one-way flag and JSONLStore.MarkHydrated are
	// DELETED with the model-content store surface (session-core DEL-10/DEL-12;
	// the recall refusal is now keyed on a record's model_origin == conv_rebuilt,
	// CONV-P A). There is no flag left to assert here.
}

// TestRollbackWindow_RestoresTurnStartProjectionSet — spec test 16 (B-24,
// US-6.AC5), ported to the non-destructive primitive (FR-006 / DEL-12): on
// abort the Skip AND the projection set return to their turn-start values in
// one write, while the appended archive bytes are RETAINED (never truncated).
//
// A mid-turn empty of a pre-turn line is undone by restoring the WHOLE
// turn-start projection set; entries whose archive_line ≥ the turn-start
// archive length go with it.
func TestRollbackWindow_RestoresTurnStartProjectionSet(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	const key = "rollback-emptied"

	// Pre-turn archive: lines 0..3. Line 1 emptied by an earlier (committed)
	// turn, line 2 capped at append time.
	seedToolLines(t, store, key, []string{"c0", "c1", "c2", "c3"})
	mustSetProjection(t, store, key, "c1", 1, ProjectionEmptied)
	mustSetProjection(t, store, key, "c2", 2, ProjectionCapped)
	// Skip = 1 at turn start (line 0 evicted earlier).
	if err := store.TruncateHistory(ctx, key, 3); err != nil {
		t.Fatalf("TruncateHistory: %v", err)
	}
	snap, err := store.SnapshotWindow(ctx, key)
	if err != nil {
		t.Fatalf("SnapshotWindow: %v", err)
	}
	start := snap.State.Clone()
	if start.Count != 4 || start.Skip != 1 || len(start.Projection.Entries) != 2 {
		t.Fatalf("turn-start snapshot = %+v, want Count=4 Skip=1 and 2 projection entries", start)
	}

	// Mid-turn: append lines 4 and 5, cap line 4, empty line 5, AND empty the
	// pre-turn capped line 2 and pre-turn line 3; windowTrim advances Skip.
	seedToolLines(t, store, key, []string{"c4", "c5"})
	mustSetProjection(t, store, key, "c4", 4, ProjectionCapped)
	mustSetProjection(t, store, key, "c5", 5, ProjectionEmptied)
	mustSetProjection(t, store, key, "c2", 2, ProjectionEmptied)
	mustSetProjection(t, store, key, "c3", 3, ProjectionEmptied)
	if err = store.TruncateHistory(ctx, key, 2); err != nil { // Skip → 4
		t.Fatalf("TruncateHistory(mid-turn): %v", err)
	}
	meta, err := store.readMeta(key)
	if err != nil {
		t.Fatalf("readMeta: %v", err)
	}
	if meta.Skip != 4 {
		t.Fatalf("mid-turn skip = %d, want 4 (the intermediate state the rollback must not keep)", meta.Skip)
	}

	// Abort → rollback to the turn-start window snapshot (FR-006).
	if err = store.RollbackWindow(ctx, key, start); err != nil {
		t.Fatalf("RollbackWindow: %v", err)
	}

	meta, err = store.readMeta(key)
	if err != nil {
		t.Fatalf("readMeta: %v", err)
	}
	if meta.Skip != 1 {
		t.Errorf("after rollback Skip = %d, want 1", meta.Skip)
	}
	// FR-006: the archive is NOT truncated — it keeps all 6 lines; Count
	// re-syncs to the physical length while Skip returns to turn start.
	if n := countFileLines(t, store.jsonlPath(key)); n != 6 {
		t.Errorf("FR-006: archive lines = %d, want 6 (retained, not truncated)", n)
	}
	pm, err := store.GetProjection(ctx, key)
	if err != nil {
		t.Fatalf("GetProjection: %v", err)
	}
	assertProjectionEqual(t, "after rollback", pm.Entries, ProjectionSet{
		{ToolCallID: "c1", ArchiveLine: 1}: ProjectionEmptied, // turn-start state kept
		{ToolCallID: "c2", ArchiveLine: 2}: ProjectionCapped,  // mid-turn empty undone
		// c3 (emptied mid-turn, not in the turn-start set) is gone; c4/c5
		// (archive_line ≥ the turn-start count) go with the restored set.
	})

	// The model view excludes the appended span (lines 4, 5) while ReadArchive
	// still returns every retained byte.
	archived, err := store.ReadArchive(ctx, key)
	if err != nil {
		t.Fatalf("ReadArchive: %v", err)
	}
	if len(archived) != 6 {
		t.Fatalf("FR-006: ReadArchive = %d messages, want 6 retained", len(archived))
	}
	snapAfter, err := store.SnapshotWindow(ctx, key)
	if err != nil {
		t.Fatalf("SnapshotWindow after rollback: %v", err)
	}
	history, lines := WindowHistory(snapAfter)
	if len(history) == 0 {
		t.Error("model view is empty after rollback; expected the retained turn-start window")
	}
	for _, l := range lines {
		if l >= start.Count {
			t.Errorf("FR-006: model view leaked aborted archive line %d (>= turn-start Count %d)", l, start.Count)
		}
	}
}

// TestSetHistory_RefusesNonEmptyArchive — spec test 57 (B-53c, US-15.AC5,
// DS-10 #8): SetHistory on a 1-line archive returns an error, the file bytes
// and Skip are untouched; on an empty archive it fills the file without
// resetting Skip (FR-047).
func TestSetHistory_RefusesNonEmptyArchive(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	const key = "set-history-refuse"

	// 1-line archive with Skip advanced to 1.
	seedToolLines(t, store, key, []string{"c0"})
	if err := store.TruncateHistory(ctx, key, 0); err != nil {
		t.Fatalf("TruncateHistory: %v", err)
	}
	before, err := os.ReadFile(store.jsonlPath(key))
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}
	metaBefore, err := store.readMeta(key)
	if err != nil {
		t.Fatalf("readMeta: %v", err)
	}
	if metaBefore.Skip != 1 {
		t.Fatalf("precondition skip = %d, want 1", metaBefore.Skip)
	}

	err = store.SetHistory(ctx, key, []providers.Message{{Role: "user", Content: "rebuilt"}})
	if err == nil {
		t.Fatal("SetHistory on a non-empty archive returned nil, want refusal")
	}
	if !errors.Is(err, ErrArchiveNotEmpty) {
		t.Errorf("error = %v, want errors.Is(ErrArchiveNotEmpty)", err)
	}

	after, err := os.ReadFile(store.jsonlPath(key))
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}
	if string(after) != string(before) {
		t.Errorf("archive bytes changed:\nbefore=%q\nafter=%q", before, after)
	}
	metaAfter, err := store.readMeta(key)
	if err != nil {
		t.Fatalf("readMeta: %v", err)
	}
	if metaAfter.Skip != 1 || metaAfter.Count != 1 {
		t.Errorf("meta after refusal skip/count = %d/%d, want 1/1", metaAfter.Skip, metaAfter.Count)
	}

	// Empty archive: SetHistory fills it and leaves Skip alone (0 here).
	const fresh = "set-history-empty"
	hist := []providers.Message{{Role: "user", Content: "a"}, {Role: "assistant", Content: "b"}}
	if err = store.SetHistory(ctx, fresh, hist); err != nil {
		t.Fatalf("SetHistory(empty): %v", err)
	}
	got, err := store.GetHistory(ctx, fresh)
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	if len(got) != 2 || got[0].Content != "a" || got[1].Content != "b" {
		t.Errorf("history = %+v, want the 2 supplied messages", got)
	}
	// A second SetHistory on the now non-empty archive is refused too.
	if err := store.SetHistory(ctx, fresh, hist); !errors.Is(err, ErrArchiveNotEmpty) {
		t.Errorf("second SetHistory error = %v, want ErrArchiveNotEmpty", err)
	}
}

func mustSetProjection(t *testing.T, store *JSONLStore, key, id string, line int, st ProjectionState) {
	t.Helper()
	if err := store.SetProjectionState(context.Background(), key, ProjectionKey{ToolCallID: id, ArchiveLine: line}, st); err != nil {
		t.Fatalf("SetProjectionState(%s,%d,%s): %v", id, line, st, err)
	}
}

func assertProjectionEqual(t *testing.T, label string, got, want ProjectionSet) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s: projection = %v, want %v", label, got, want)
		return
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: projection[%+v] = %q, want %q (full: %v)", label, k, got[k], v, got)
		}
	}
}

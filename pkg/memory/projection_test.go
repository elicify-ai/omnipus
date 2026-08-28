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
	if pm.Hydrated {
		t.Errorf("hydrated = true, want false on a never-hydrated archive")
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

	// Hydrated is a one-way flag persisted beside the set (FR-019 meta half,
	// read by T066-14's recall-by-id answer).
	if err = reopened.MarkHydrated(ctx, key); err != nil {
		t.Fatalf("MarkHydrated: %v", err)
	}
	pm, err = reopened.GetProjection(ctx, key)
	if err != nil {
		t.Fatalf("GetProjection: %v", err)
	}
	if !pm.Hydrated {
		t.Error("hydrated = false after MarkHydrated")
	}
	if len(pm.Entries) != 2 {
		t.Errorf("MarkHydrated changed the set: %v", pm.Entries)
	}
}

// TestRollbackAppended_RestoresTurnStartEmptiedSet — spec test 16 (B-24,
// US-6.AC5): on abort the archive length, Skip AND the emptied-set return to
// their turn-start values in one write; entries whose archive_line ≥ the
// turn-start archive length are dropped, and a mid-turn empty of a
// pre-turn line is undone (restored to whatever the turn-start set said —
// here, capped).
func TestRollbackAppended_RestoresTurnStartEmptiedSet(t *testing.T) {
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

	// Capture the turn-start triple exactly as newTurnState will.
	turnStartLines := 4
	turnStartSkip := 1
	pm, err := store.GetProjection(ctx, key)
	if err != nil {
		t.Fatalf("GetProjection: %v", err)
	}
	turnStartSet := pm.Entries.Clone()
	if len(turnStartSet) != 2 {
		t.Fatalf("turn-start set = %v, want 2 entries", turnStartSet)
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
	// The mid-turn prune removed lines 1..3's entries; mutate turnStartSet's
	// caller copy afterwards to prove the store does not alias it.

	// Abort → rollback to the turn-start triple.
	if err = store.RollbackAppended(ctx, key, turnStartLines, turnStartSkip, turnStartSet); err != nil {
		t.Fatalf("RollbackAppended: %v", err)
	}
	turnStartSet[ProjectionKey{ToolCallID: "zzz", ArchiveLine: 0}] = ProjectionEmptied // caller-side mutation after the call

	meta, err = store.readMeta(key)
	if err != nil {
		t.Fatalf("readMeta: %v", err)
	}
	if meta.Count != 4 || meta.Skip != 1 {
		t.Errorf("after rollback count/skip = %d/%d, want 4/1", meta.Count, meta.Skip)
	}
	if n := countFileLines(t, store.jsonlPath(key)); n != 4 {
		t.Errorf("archive lines = %d, want 4", n)
	}
	pm, err = store.GetProjection(ctx, key)
	if err != nil {
		t.Fatalf("GetProjection: %v", err)
	}
	assertProjectionEqual(t, "after rollback", pm.Entries, ProjectionSet{
		{ToolCallID: "c1", ArchiveLine: 1}: ProjectionEmptied, // turn-start state kept
		{ToolCallID: "c2", ArchiveLine: 2}: ProjectionCapped,  // mid-turn empty undone
		// c3 (emptied mid-turn, not in the turn-start set) is gone.
		// c4/c5 (archive_line ≥ 4) are dropped.
	})

	// Rollback is idempotent: a second call with the same triple changes
	// nothing (US-6.AC5 "never an intermediate state").
	if err = store.RollbackAppended(ctx, key, turnStartLines, turnStartSkip, pm.Entries); err != nil {
		t.Fatalf("RollbackAppended(2nd): %v", err)
	}
	pm2, err := store.GetProjection(ctx, key)
	if err != nil {
		t.Fatalf("GetProjection: %v", err)
	}
	assertProjectionEqual(t, "after 2nd rollback", pm2.Entries, pm.Entries)

	// A nil turn-start set means "nothing was emptied at turn start": every
	// emptied entry goes, capped pre-turn entries stay.
	mustSetProjection(t, store, key, "c1", 1, ProjectionEmptied)
	if err = store.RollbackAppended(ctx, key, turnStartLines, turnStartSkip, nil); err != nil {
		t.Fatalf("RollbackAppended(nil set): %v", err)
	}
	pm, err = store.GetProjection(ctx, key)
	if err != nil {
		t.Fatalf("GetProjection: %v", err)
	}
	assertProjectionEqual(t, "after nil-set rollback", pm.Entries, ProjectionSet{
		{ToolCallID: "c2", ArchiveLine: 2}: ProjectionCapped,
	})
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

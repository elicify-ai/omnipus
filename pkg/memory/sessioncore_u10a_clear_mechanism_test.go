package memory

import (
	"context"
	"testing"
)

// U10a/U10b — /clear semantics (founder ruling 2026-10-09).
//
// Founder ruling: /new is retired; /clear is the replacement and its meaning is
// "clear the chat UI and the model's context, but PRESERVE the transcript and
// start NO new session — it MOVES/advances the context window (the compaction
// mechanism). It does not delete conversation history."
//
// FR-006 (session-core-spec.md): "Clear/correction/rollback/projection MUST
// append effects or move view/window metadata, never rewrite retained bytes."
// FR-030: /clear "preserves session/pending input/archive/search/recall".
//
// This is a MECHANISM-level assertion (the context window + the retained
// transcript), NOT command-table presence — per the X3 interim caveat. It is
// U10b-bound: the real /clear server command (main/extra-only, safe point,
// marker) lands in U10b; U10a is the command-table cut. A test here that stays
// red until U10b is a U10b target, not a U10a GREEN target.
//
// The mechanism under test is the window store's clear cursor move
// (JSONLStore.TruncateHistory advancing WindowState.Skip) on a REAL store, with
// the retained archive read back through ReadArchive (the recall path).

func TestSessionCoreU10a_ClearMechanismMovesWindowAndPreservesTranscript(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()
	ctx := context.Background()
	const key = "sess-clear-mechanism"

	seeded := []struct{ role, content string }{
		{"user", "first question"},
		{"assistant", "first answer"},
		{"user", "second question"},
		{"assistant", "second answer"},
	}
	for _, m := range seeded {
		if err := store.AddMessage(ctx, key, m.role, m.content); err != nil {
			t.Fatalf("AddMessage(%q): %v", m.content, err)
		}
	}

	before, err := store.SnapshotWindow(ctx, key)
	if err != nil {
		t.Fatalf("SnapshotWindow before: %v", err)
	}
	if before.State.Skip != 0 {
		t.Fatalf("precondition: initial window Skip = %d, want 0", before.State.Skip)
	}
	if len(before.Archive) != len(seeded) {
		t.Fatalf("precondition: archive has %d records, want %d", len(before.Archive), len(seeded))
	}

	// The /clear mechanism: move the context window without touching bytes.
	if err := store.TruncateHistory(ctx, key, 0); err != nil {
		t.Fatalf("TruncateHistory (clear mechanism): %v", err)
	}

	after, err := store.SnapshotWindow(ctx, key)
	if err != nil {
		t.Fatalf("SnapshotWindow after: %v", err)
	}

	// (a) the context window MOVED/advanced forward.
	if after.State.Skip <= before.State.Skip {
		t.Errorf("clear must move the context window forward: Skip %d -> %d (want a larger Skip)", before.State.Skip, after.State.Skip)
	}

	// (b) the transcript is PRESERVED — no history deletion.
	preserved, err := store.ReadArchive(ctx, key)
	if err != nil {
		t.Fatalf("ReadArchive: %v", err)
	}
	if len(preserved) != len(seeded) {
		t.Errorf("clear deleted transcript bytes: archive %d -> %d records, want all %d retained", len(before.Archive), len(preserved), len(seeded))
	}
	for i, want := range seeded {
		if i >= len(preserved) {
			break
		}
		if preserved[i].Role != want.role || preserved[i].Content != want.content {
			t.Errorf("clear altered archived transcript record %d: got %q/%q, want %q/%q (FR-006: never rewrite retained bytes)",
				i, preserved[i].Role, preserved[i].Content, want.role, want.content)
		}
	}
}

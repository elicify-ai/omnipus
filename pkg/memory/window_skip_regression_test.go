package memory

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
)

// Gap 6 (R1 coverage-gaps brief): commitWindow's atomic commit/rollback
// cursor check — `after.Count != before.Count || after.Count != len(archive)
// || after.Skip < 0 || (!restore && after.Skip < before.Skip) || after.Skip
// > after.Count` (window.go) — includes a Skip-regression guard that applies
// only to a real commit (restore=false): a commit must never move Skip
// backward. RestoreWindow is deliberately exempt (its own doc comment:
// "unlike turn rollback, it never removes archive appends" — undoing a prior
// advance IS a legitimate backward move). This test drives a genuine
// CommitWindow regression attempt and proves the guard actually refuses it,
// then proves RestoreWindow is correctly NOT subject to the same guard — so
// a test asserting "any error" on both paths would not actually pin the
// commit-specific clause the way this does.
func TestCommitWindow_RefusesSkipRegression(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	key := "cw-gap6-skip-regression"

	seed := []providers.Message{
		{Role: "user", Content: "one"},
		{Role: "assistant", Content: "two"},
		{Role: "user", Content: "three"},
		{Role: "assistant", Content: "four"},
	}
	for _, m := range seed {
		if err := store.AddFullMessage(ctx, key, m); err != nil {
			t.Fatalf("seed AddFullMessage: %v", err)
		}
	}

	snap0, err := store.SnapshotWindow(ctx, key)
	if err != nil {
		t.Fatalf("SnapshotWindow: %v", err)
	}
	if snap0.State.Skip != 0 || snap0.State.Count != len(seed) {
		t.Fatalf("precondition: expected Skip=0 Count=%d, got Skip=%d Count=%d", len(seed), snap0.State.Skip, snap0.State.Count)
	}

	// A legitimate forward slide: Skip 0 -> 2.
	advanced := snap0.State.Clone()
	advanced.Skip = 2
	if err := store.CommitWindow(ctx, key, snap0.State, advanced); err != nil {
		t.Fatalf("legitimate forward CommitWindow (Skip 0 -> 2) must succeed: %v", err)
	}

	snap1, err := store.SnapshotWindow(ctx, key)
	if err != nil {
		t.Fatalf("SnapshotWindow after advance: %v", err)
	}
	if snap1.State.Skip != 2 {
		t.Fatalf("precondition: expected Skip=2 after the legitimate advance, got %d", snap1.State.Skip)
	}

	// The regression attempt: Skip 2 -> 1 via a real commit (restore=false).
	// Absent the guard, nothing else in the validation chain rejects this —
	// Count/archive length/AnchorLine/Projection are all still consistent.
	regressed := snap1.State.Clone()
	regressed.Skip = 1
	err = store.CommitWindow(ctx, key, snap1.State, regressed)
	if err == nil {
		t.Fatal("CommitWindow must refuse a Skip regression (after.Skip=1 < before.Skip=2) on a real commit — " +
			"retained archive the window already evicted must never come back")
	}

	final, err := store.SnapshotWindow(ctx, key)
	if err != nil {
		t.Fatalf("SnapshotWindow after refused regression: %v", err)
	}
	if final.State.Skip != 2 {
		t.Fatalf("a refused commit must not move Skip backward: expected Skip=2 (unchanged), got %d", final.State.Skip)
	}

	// Contract control: RestoreWindow (restore=true) is deliberately exempt
	// from this same guard — undoing a prior advance is a legitimate
	// backward move, and the guard's `!restore` condition exists precisely
	// to let this succeed while the commit above is refused.
	if err := store.RestoreWindow(ctx, key, final.State, snap0.State); err != nil {
		t.Fatalf("RestoreWindow must be allowed to move Skip backward (restore=true is exempt from the "+
			"commit-only regression guard): %v", err)
	}
	restored, err := store.SnapshotWindow(ctx, key)
	if err != nil {
		t.Fatalf("SnapshotWindow after restore: %v", err)
	}
	if restored.State.Skip != 0 {
		t.Fatalf("expected the restore to move Skip back to 0, got %d", restored.State.Skip)
	}
}

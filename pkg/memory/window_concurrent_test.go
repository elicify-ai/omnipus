package memory

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
)

// Gap 1 (R1 coverage-gaps brief): commitWindow (window.go) compares the
// current on-disk meta against the caller's `before` snapshot with
// reflect.DeepEqual before applying a restore. RestoreWindow's own doc
// comment states the contract: "it never removes archive appends and
// refuses a concurrent change." These two tests drive that refusal for
// real, through genuine goroutines racing a real AddFullMessage append
// against a RestoreWindow built from a now-stale snapshot.
//
// Why assert the specific ErrWindowChanged sentinel and not merely "some
// error": commitWindow has a SECOND, independent guard a few lines later
// (`after.Count != before.Count || after.Count != len(archive) || ...`)
// that also rejects a stale restore whenever Count itself disagrees with
// the real archive length. A neutered DeepEqual therefore does not open a
// data-corruption hole for THIS race — the second guard still refuses the
// write — but it changes WHICH error comes back: the generic "invalid
// checkpoint cursor" (a bare errors.New, not the sentinel) instead of the
// documented ErrWindowChanged. A caller doing `errors.Is(err,
// ErrWindowChanged)` — the only documented way to detect "the window
// changed concurrently" — would silently misclassify the failure once
// DeepEqual stops contributing its half of the check. Pinning the sentinel,
// not just non-nil-ness, is what makes these tests die when DeepEqual is
// neutered.
func TestRestoreWindow_ConcurrentAppend_AppendWins_RefusesWithErrWindowChanged(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	key := "cw-restore-race-append-wins"

	seed := []providers.Message{
		{Role: "user", Content: "one"},
		{Role: "assistant", Content: "two"},
		{Role: "user", Content: "three"},
	}
	for _, m := range seed {
		if err := store.AddFullMessage(ctx, key, m); err != nil {
			t.Fatalf("seed AddFullMessage: %v", err)
		}
	}

	before, err := store.SnapshotWindow(ctx, key)
	if err != nil {
		t.Fatalf("SnapshotWindow: %v", err)
	}
	if before.State.Count != len(seed) || before.State.Skip != 0 {
		t.Fatalf("precondition: expected Count=%d Skip=0, got Count=%d Skip=%d",
			len(seed), before.State.Count, before.State.Skip)
	}

	// restoreTarget is the plausible metadata-only slide (Skip 0 -> 1) a
	// caller would compute from the now-stale `before` snapshot, unaware
	// that a concurrent append is about to land.
	restoreTarget := before.State.Clone()
	restoreTarget.Skip = 1

	// Run the real append in its own goroutine, and only release the
	// restore attempt once that append's full critical section (archive
	// write + meta resync) has actually committed — this deterministically
	// reproduces "a real concurrent append landed between this caller's
	// snapshot and its restore attempt" while still exercising the append
	// through a genuinely separate goroutine, not an inline call.
	appendDone := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(appendDone)
		if aerr := store.AddFullMessage(ctx, key, providers.Message{Role: "assistant", Content: "concurrent append"}); aerr != nil {
			t.Errorf("concurrent AddFullMessage: %v", aerr)
		}
	}()
	<-appendDone
	wg.Wait()

	restoreErr := store.RestoreWindow(ctx, key, before.State, restoreTarget)
	if restoreErr == nil {
		t.Fatal("RestoreWindow succeeded against a stale `before` snapshot after a concurrent append landed — " +
			"the window genuinely changed underneath it and the restore must be refused")
	}
	if !errors.Is(restoreErr, ErrWindowChanged) {
		t.Fatalf("RestoreWindow must refuse a stale concurrent-change restore with the specific "+
			"ErrWindowChanged sentinel (RestoreWindow's own doc comment: %q); got a different error instead: %v",
			"refuses a concurrent change", restoreErr)
	}

	// No corruption: the concurrent append is never lost, and the refused
	// restore must not have partially applied its Skip change.
	after, err := store.SnapshotWindow(ctx, key)
	if err != nil {
		t.Fatalf("SnapshotWindow after race: %v", err)
	}
	if after.State.Count != len(seed)+1 {
		t.Fatalf("concurrent append must not be lost: expected Count=%d, got %d", len(seed)+1, after.State.Count)
	}
	if len(after.Archive) != len(seed)+1 {
		t.Fatalf("archive must retain the concurrent append: expected %d entries, got %d", len(seed)+1, len(after.Archive))
	}
	if after.State.Skip != 0 {
		t.Fatalf("a refused restore must not apply its Skip change: expected Skip=0 (unchanged), got %d", after.State.Skip)
	}
}

// TestRestoreWindow_ConcurrentAppend_RestoreWins_AppendStillLandsAfter is the
// companion ordering: the restore's `before` snapshot is still accurate when
// it runs (nothing changed it yet), so it must succeed, and the concurrent
// append — released only once the restore's own critical section has fully
// committed — must still land cleanly afterward on the restored state. Read
// together with the append-wins test above, this is the "serializes
// correctly, no corruption, in either order" half of the brief's
// requirement.
func TestRestoreWindow_ConcurrentAppend_RestoreWins_AppendStillLandsAfter(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	key := "cw-restore-race-restore-wins"

	seed := []providers.Message{
		{Role: "user", Content: "one"},
		{Role: "assistant", Content: "two"},
		{Role: "user", Content: "three"},
	}
	for _, m := range seed {
		if err := store.AddFullMessage(ctx, key, m); err != nil {
			t.Fatalf("seed AddFullMessage: %v", err)
		}
	}

	before, err := store.SnapshotWindow(ctx, key)
	if err != nil {
		t.Fatalf("SnapshotWindow: %v", err)
	}
	restoreTarget := before.State.Clone()
	restoreTarget.Skip = 1

	restoreDone := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(restoreDone)
		if rerr := store.RestoreWindow(ctx, key, before.State, restoreTarget); rerr != nil {
			t.Errorf("RestoreWindow (accurate `before`, no concurrent change yet) must succeed: %v", rerr)
		}
	}()
	<-restoreDone
	wg.Wait()

	if aerr := store.AddFullMessage(ctx, key, providers.Message{Role: "assistant", Content: "append after restore"}); aerr != nil {
		t.Fatalf("append after restore: %v", aerr)
	}

	after, err := store.SnapshotWindow(ctx, key)
	if err != nil {
		t.Fatalf("SnapshotWindow after race: %v", err)
	}
	if after.State.Skip != 1 {
		t.Fatalf("the won restore's Skip change must be applied: expected Skip=1, got %d", after.State.Skip)
	}
	if after.State.Count != len(seed)+1 {
		t.Fatalf("the append after the restore must land: expected Count=%d, got %d", len(seed)+1, after.State.Count)
	}
	if len(after.Archive) != len(seed)+1 {
		t.Fatalf("archive must contain seed + the post-restore append: expected %d entries, got %d", len(seed)+1, len(after.Archive))
	}
}

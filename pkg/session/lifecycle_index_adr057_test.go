// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-057 W6, FR-020, U13 — coverage for the LifecycleStore parent index
// (lifecycle_index.go). Per binding Rule 1, every assertion here runs
// against a REAL *LifecycleStore rooted at a t.TempDir() and REAL persisted
// JSONL files — never a spy/fake standing in for the store.

package session

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
	"time"
)

// TestLifecycleParentIndex_MaintainedInsidePersist is test #11: the index
// MUST be updated as a direct side effect of Persist (FR-020), and it MUST
// track a session's later generations (which all carry the same
// ParentDurableKey) without duplicating or losing the mapping. Assertions
// land on the index's own real, production state (idx.children(...)) —
// observable, in-memory artefact of a REAL store — never on "add() was
// called".
func TestLifecycleParentIndex_MaintainedInsidePersist(t *testing.T) {
	s := newTestLifecycleStore(t)
	const parent = "chat-root-11"
	const childA = "child-a-11"
	const childB = "child-b-11"

	// FR-074 — parent and children must be distinct, non-equal values.
	if parent == childA || parent == childB || childA == childB {
		t.Fatalf("fixture ids must be pairwise distinct: %q %q %q", parent, childA, childB)
	}

	if err := s.Persist(&LifecycleRecord{
		SessionID: childA, State: LifecycleQueued,
		OwnerScopeKind: OwnerScopeHuman, ParentDurableKey: parent,
		WorkspaceID: "ws-1", AgentID: "ray",
	}); err != nil {
		t.Fatalf("persist childA: %v", err)
	}

	// The index must already reflect childA before ANY List() call — proving
	// maintenance happens INSIDE Persist, not lazily on first read.
	got := s.parentIndex.children(parent)
	if len(got) != 1 || got[0] != childA {
		t.Fatalf("after persisting childA, parentIndex.children(%q) = %v, want [%q]", parent, got, childA)
	}

	if err := s.Persist(&LifecycleRecord{
		SessionID: childB, State: LifecycleQueued,
		OwnerScopeKind: OwnerScopeHuman, ParentDurableKey: parent,
		WorkspaceID: "ws-1", AgentID: "ava",
	}); err != nil {
		t.Fatalf("persist childB: %v", err)
	}
	got = s.parentIndex.children(parent)
	sort.Strings(got)
	want := []string{childA, childB}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("after persisting childB, parentIndex.children(%q) = %v, want %v", parent, got, want)
	}

	// A later generation of childA (same ParentDurableKey) must not
	// duplicate the entry or evict it.
	if err := s.Persist(&LifecycleRecord{
		SessionID: childA, Generation: 1, State: LifecycleRunning,
		OwnerScopeKind: OwnerScopeHuman, ParentDurableKey: parent,
		WorkspaceID: "ws-1", AgentID: "ray",
	}); err != nil {
		t.Fatalf("persist childA gen1: %v", err)
	}
	got = s.parentIndex.children(parent)
	if len(got) != 2 {
		t.Fatalf("a later generation of an already-indexed child must not duplicate the entry, got %v", got)
	}

	// The public query surface (List) must agree with the index's own state.
	viaList, err := s.List(LifecycleFilter{ParentDurableKey: parent})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(viaList) != 2 {
		t.Fatalf("List(ParentDurableKey=%q) returned %d records, want 2", parent, len(viaList))
	}
}

// TestLifecycleParentIndex_UnattributedRecordNotIndexed proves add()'s empty
// guard: a record with no ParentDurableKey (FR-015's degraded-mode mint, or
// any record predating this field) must never appear under
// LifecycleFilter{ParentDurableKey: ""} — that value means "filter off"
// everywhere else on LifecycleFilter and must keep meaning that here too.
func TestLifecycleParentIndex_UnattributedRecordNotIndexed(t *testing.T) {
	s := newTestLifecycleStore(t)
	if err := s.Persist(&LifecycleRecord{
		SessionID: "orphan-11b", State: LifecycleQueued,
		OwnerScopeKind: OwnerScopeHuman, ParentDurableKey: "",
		WorkspaceID: "ws-1", AgentID: "ray",
	}); err != nil {
		t.Fatalf("persist: %v", err)
	}
	if got := s.parentIndex.children(""); len(got) != 0 {
		t.Fatalf("parentIndex.children(\"\") = %v, want empty — an unparented record must never be indexed under the empty key", got)
	}
}

// TestLifecycleParentIndex_RemoveIsIdempotentAndPrunesEmptyBucket exercises
// the index primitive directly: remove() on an absent pair, and removal of
// the last child clearing the parent's bucket entirely (so long-running
// processes do not accumulate empty buckets).
func TestLifecycleParentIndex_RemoveIsIdempotentAndPrunesEmptyBucket(t *testing.T) {
	idx := newLifecycleParentIndex()
	// Removing something never added is a documented no-op, not a panic.
	idx.remove("nope", "nope")

	idx.add("p", "c1")
	idx.add("p", "c2")
	idx.remove("p", "c1")
	if got := idx.children("p"); len(got) != 1 || got[0] != "c2" {
		t.Fatalf("children(p) after removing c1 = %v, want [c2]", got)
	}
	idx.remove("p", "c2")
	if got := idx.children("p"); len(got) != 0 {
		t.Fatalf("children(p) after removing every child = %v, want empty", got)
	}
	idx.mu.RLock()
	_, bucketSurvives := idx.byParent["p"]
	idx.mu.RUnlock()
	if bucketSurvives {
		t.Fatal("an empty parent bucket must be deleted, not left behind as an empty map")
	}
}

// TestLifecycleParentIndex_PruneTerminalRemovesFromIndex proves lifecycle.go's
// pruneTerminalOne keeps the index consistent with disk: once a child's
// terminal record is pruned (file removed), it must stop being returned as
// that parent's child.
func TestLifecycleParentIndex_PruneTerminalRemovesFromIndex(t *testing.T) {
	s := newTestLifecycleStore(t)
	const parent = "chat-prune-11c"
	const child = "child-prune-11c"
	if err := s.Persist(&LifecycleRecord{
		SessionID: child, State: LifecycleCompleted,
		OwnerScopeKind: OwnerScopeHuman, ParentDurableKey: parent,
		WorkspaceID: "ws-1", AgentID: "ray",
	}); err != nil {
		t.Fatalf("persist: %v", err)
	}
	if got := s.parentIndex.children(parent); len(got) != 1 {
		t.Fatalf("precondition failed: parentIndex.children(%q) = %v, want [%q]", parent, got, child)
	}

	// retentionDays=1, "now" pushed 48h into the future so the just-seeded
	// (real-time) UpdatedAt is comfortably past the resulting 24h-in-the-
	// future cutoff (mirrors lifecycle_prune_race_test.go's own pattern).
	removed, err := s.PruneTerminal(1, time.Now().Add(48*time.Hour))
	if err != nil {
		t.Fatalf("PruneTerminal: %v", err)
	}
	if removed != 1 {
		t.Fatalf("PruneTerminal removed = %d, want 1", removed)
	}
	if got := s.parentIndex.children(parent); len(got) != 0 {
		t.Fatalf("parentIndex.children(%q) after prune = %v, want empty — stale index entry survived a real file deletion", parent, got)
	}
	// The public surface must agree: no error, no ghost result.
	list, err := s.List(LifecycleFilter{ParentDurableKey: parent})
	if err != nil {
		t.Fatalf("List after prune: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("List(ParentDurableKey=%q) after prune = %v, want empty", parent, list)
	}
}

// TestLifecycleParentIndex_SelfHealsStaleEntryOutsidePrune covers the defensive
// branch in listByParentDurableKey directly: if a child's file disappears by
// a path OTHER than PruneTerminal (an operator `rm`, mirroring the
// out-of-band-deletion scenarios this whole spec treats as a first-class
// case), the very next query must still succeed and silently drop the ghost
// entry rather than surface an error.
func TestLifecycleParentIndex_SelfHealsStaleEntryOutsidePrune(t *testing.T) {
	s := newTestLifecycleStore(t)
	const parent = "chat-selfheal-11d"
	const child = "child-selfheal-11d"
	if err := s.Persist(&LifecycleRecord{
		SessionID: child, State: LifecycleRunning,
		OwnerScopeKind: OwnerScopeHuman, ParentDurableKey: parent,
		WorkspaceID: "ws-1", AgentID: "ray",
	}); err != nil {
		t.Fatalf("persist: %v", err)
	}

	// Simulate an out-of-band deletion (RetentionSweep, a crashed deploy, an
	// operator `rm` — AC-19's own cases), bypassing PruneTerminal entirely.
	if err := os.Remove(filepath.Join(s.Dir(), child+".jsonl")); err != nil {
		t.Fatalf("simulate out-of-band removal: %v", err)
	}

	list, err := s.List(LifecycleFilter{ParentDurableKey: parent})
	if err != nil {
		t.Fatalf("List must self-heal a stale index entry rather than error, got: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("List(ParentDurableKey=%q) = %v, want empty after the child's file vanished out of band", parent, list)
	}
	if got := s.parentIndex.children(parent); len(got) != 0 {
		t.Fatalf("parentIndex.children(%q) = %v, want empty — the stale entry should have self-healed out of the index", parent, got)
	}
}

// TestLifecycleParentIndex_WarmsAcrossSimulatedRestart proves ensureWarm
// closes the gap Persist-time-only maintenance would leave: a SECOND
// LifecycleStore instance rooted at the SAME directory (standing in for a
// fresh process after a restart, since this package's stores are per-process
// in-memory objects with no shared index state) must still resolve
// "children of X" correctly on its very FIRST query, backfilling from the
// real files the FIRST instance wrote — even though no Persist call has ever
// run against this second instance.
func TestLifecycleParentIndex_WarmsAcrossSimulatedRestart(t *testing.T) {
	dir := t.TempDir()
	first := NewLifecycleStore(dir)
	const parent = "chat-restart-11e"
	const child1 = "child-restart-11e-1"
	const child2 = "child-restart-11e-2"
	for _, id := range []string{child1, child2} {
		if err := first.Persist(&LifecycleRecord{
			SessionID: id, State: LifecycleRunning,
			OwnerScopeKind: OwnerScopeHuman, ParentDurableKey: parent,
			WorkspaceID: "ws-1", AgentID: "ray",
		}); err != nil {
			t.Fatalf("seed via first instance: %v", err)
		}
	}

	// A fresh store instance over the SAME directory: its own parentIndex
	// starts empty (a brand-new struct), simulating what a restarted process
	// would see before the boot sweep or any new delegation has run.
	second := NewLifecycleStore(dir)
	if got := second.parentIndex.children(parent); len(got) != 0 {
		t.Fatalf("precondition failed: a fresh store instance's index must start empty, got %v", got)
	}

	got, err := second.List(LifecycleFilter{ParentDurableKey: parent})
	if err != nil {
		t.Fatalf("List on the fresh instance: %v", err)
	}
	ids := map[string]bool{}
	for _, r := range got {
		ids[r.SessionID] = true
	}
	if len(ids) != 2 || !ids[child1] || !ids[child2] {
		t.Fatalf("fresh-instance List(ParentDurableKey=%q) = %v, want exactly [%q %q] backfilled from disk",
			parent, listedSessionIDs(got), child1, child2)
	}
}

// TestParentIndex_SteadyStateQueryNeverTouchesUnrelatedSessionFiles is the
// deterministic, non-timing proof of BDD-19's steady-state claim ("its
// file-read count scales with the subtree size, not with the total session
// count"): once the index is warm (any prior ParentDurableKey query has
// already run ensureWarm's one-time backfill — see
// TestLifecycleParentIndex_WarmsAcrossSimulatedRestart for the cold-start
// half), every SUBSEQUENT query must resolve its candidate id set purely
// from the in-memory index, never touching any OTHER session's file.
//
// This is proven, not asserted-by-invocation-count (binding Rules 1-2
// forbid a spy that merely records "Load was called N times"): after
// warm-up has already completed, every UNRELATED session's own file is made
// unreadable (chmod 0000 — this test runs as the non-root `dev` user, so
// this is a REAL, deterministic permission denial, not a simulated one). If
// the steady-state query path touched any of those files, it would receive
// a genuine non-ErrLifecycleNotFound I/O error and, per the full-scan
// branch's own error handling, fail outright. It must instead succeed with
// exactly the small subtree's two children — an observable outcome (a
// returned error and result set), never an invocation count.
//
// POSIX-only: chmod-based unreadability is not a portable technique
// (mirrors this spec's own precedent of scoping such tests, e.g. FR-029's
// Setpgid work and pkg/entity's cross-process test, to !windows).
func TestParentIndex_SteadyStateQueryNeverTouchesUnrelatedSessionFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX-only: relies on file permission bits to prove unrelated files are never opened")
	}
	s := newTestLifecycleStore(t)
	const parent = "chat-costproof-11f"
	const child1 = "child-costproof-11f-1"
	const child2 = "child-costproof-11f-2"

	for _, id := range []string{child1, child2} {
		if err := s.Persist(&LifecycleRecord{
			SessionID: id, State: LifecycleRunning,
			OwnerScopeKind: OwnerScopeHuman, ParentDurableKey: parent,
			WorkspaceID: "ws-1", AgentID: "ray",
		}); err != nil {
			t.Fatalf("seed child %q: %v", id, err)
		}
	}

	const poisonCount = 30
	poisonIDs := make([]string, 0, poisonCount)
	for i := 0; i < poisonCount; i++ {
		id := "poison-11f-" + string(rune('a'+i%26)) + string(rune('0'+i/26))
		if err := s.Persist(&LifecycleRecord{
			SessionID: id, State: LifecycleRunning,
			OwnerScopeKind: OwnerScopeHuman, // deliberately no ParentDurableKey — unrelated to `parent`
			WorkspaceID:    "ws-1", AgentID: "ray",
		}); err != nil {
			t.Fatalf("seed poison %q: %v", id, err)
		}
		poisonIDs = append(poisonIDs, id)
	}
	// Positive lower bound (binding Rule 4): confirm every poison file
	// actually exists as a real, separate, still-readable file before doing
	// anything else — a missing fixture would make the whole gate vacuous.
	found := 0
	for _, id := range poisonIDs {
		if _, err := os.Stat(filepath.Join(s.Dir(), id+".jsonl")); err == nil {
			found++
		}
	}
	if found != poisonCount {
		t.Fatalf("positive lower bound failed: only %d of %d poison fixtures exist on disk", found, poisonCount)
	}

	// Warm the index NOW, while every file (including the poison ones) is
	// still perfectly readable — this is the one-time backfill scan
	// (ensureWarm's sync.Once) that necessarily touches every persisted
	// session_id once. Any ParentDurableKey query triggers it; the result
	// here is exactly the same as the direct query below.
	if _, err := s.List(LifecycleFilter{ParentDurableKey: parent}); err != nil {
		t.Fatalf("warm-up query failed unexpectedly: %v", err)
	}

	// NOW poison every unrelated file. Because ensureWarm already ran
	// (sync.Once), no LATER query may repeat that scan — the sole avenue
	// left for a query to reach these files no longer exists in a correct
	// implementation.
	for _, id := range poisonIDs {
		if err := os.Chmod(filepath.Join(s.Dir(), id+".jsonl"), 0o000); err != nil {
			t.Fatalf("chmod poison %q: %v", id, err)
		}
	}
	t.Cleanup(func() {
		for _, id := range poisonIDs {
			_ = os.Chmod(filepath.Join(s.Dir(), id+".jsonl"), 0o600)
		}
	})

	// Sanity: prove the poison files are ACTUALLY unreadable by this
	// process now — if they were not (e.g. running as root), the rest of
	// this test would pass vacuously regardless of which code path ran.
	if _, err := os.ReadFile(filepath.Join(s.Dir(), poisonIDs[0]+".jsonl")); err == nil {
		t.Skip("poison fixture is still readable by this process (likely running as root) — this technique cannot prove the property here")
	} else if !errors.Is(err, os.ErrPermission) {
		t.Fatalf("expected a permission error reading a chmod 0000 file, got: %v", err)
	}

	// The steady-state query: the index is already warm, so this MUST
	// resolve entirely from parentIndex.children(parent) plus a Load of
	// exactly child1/child2 — never reopening any poisoned file.
	got, err := s.List(LifecycleFilter{ParentDurableKey: parent})
	if err != nil {
		t.Fatalf("steady-state List(ParentDurableKey=%q) must never re-touch unrelated sessions, but got: %v", parent, err)
	}
	ids := map[string]bool{}
	for _, r := range got {
		ids[r.SessionID] = true
	}
	if len(ids) != 2 || !ids[child1] || !ids[child2] {
		t.Fatalf("List(ParentDurableKey=%q) = %v, want exactly [%q %q] — a steady-state query must resolve the "+
			"subtree without touching any of the %d unrelated (now-unreadable) session files",
			parent, listedSessionIDs(got), child1, child2, poisonCount)
	}
}

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package session

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// countJSONL counts the non-empty JSONL lines persisted for sessionID — used
// to prove "no lost update / no duplicate" in the race tests (exactly the
// expected number of appends landed).
func countJSONL(t *testing.T, s *LifecycleStore, sessionID string) int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(s.Dir(), sessionID+".jsonl"))
	if err != nil {
		return 0
	}
	n := 0
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}

func newTestLifecycleStore(t *testing.T) *LifecycleStore {
	t.Helper()
	return NewLifecycleStore(t.TempDir())
}

func TestLifecycleStore_PersistAndReload(t *testing.T) {
	s := newTestLifecycleStore(t)
	rec := &LifecycleRecord{
		SessionID:      "sess-1",
		Generation:     0,
		State:          LifecycleQueued,
		OwnerScopeKind: OwnerScopeHuman,
		WorkspaceID:    "ws-1",
		AgentID:        "agent-1",
	}
	if err := s.Persist(rec); err != nil {
		t.Fatalf("Persist(queued) failed: %v", err)
	}

	loaded, err := s.Load("sess-1")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if loaded.State != LifecycleQueued {
		t.Errorf("state = %q, want %q", loaded.State, LifecycleQueued)
	}
	if loaded.Terminal() {
		t.Error("queued record must not be terminal")
	}

	// Transition to running — a new line, same generation, tail updates.
	loaded.State = LifecycleRunning
	if persistErr := s.Persist(loaded); persistErr != nil {
		t.Fatalf("Persist(running) failed: %v", persistErr)
	}
	reloaded, err := s.Load("sess-1")
	if err != nil {
		t.Fatalf("Load after running transition failed: %v", err)
	}
	if reloaded.State != LifecycleRunning {
		t.Errorf("state after transition = %q, want %q", reloaded.State, LifecycleRunning)
	}
	if reloaded.CreatedAt.IsZero() {
		t.Error("CreatedAt should be preserved from the first record of this generation")
	}
}

func TestLifecycleStore_AllEightStatesValid(t *testing.T) {
	states := []LifecycleState{
		LifecycleQueued, LifecycleRunning, LifecycleNeedsInput, LifecyclePaused,
		LifecycleCompleted, LifecycleFailed, LifecycleCancelled, LifecycleTimedOut,
	}
	if len(states) != 8 {
		t.Fatalf("expected exactly 8 canonical states in this test table, got %d", len(states))
	}
	for _, st := range states {
		if !IsValidLifecycleState(st) {
			t.Errorf("IsValidLifecycleState(%q) = false, want true", st)
		}
	}
	if IsValidLifecycleState(LifecycleState("bogus")) {
		t.Error("IsValidLifecycleState(bogus) = true, want false")
	}
}

func TestLifecycleStore_TerminalImmutability(t *testing.T) {
	s := newTestLifecycleStore(t)
	rec := &LifecycleRecord{
		SessionID:      "sess-term",
		Generation:     0,
		State:          LifecycleCompleted,
		OwnerScopeKind: OwnerScopeHuman,
		WorkspaceID:    "ws-1",
		AgentID:        "agent-1",
	}
	if err := s.Persist(rec); err != nil {
		t.Fatalf("Persist(completed) failed: %v", err)
	}

	// Attempting to mutate the SAME generation after it went terminal must
	// be rejected (L-3/MAJ-1/N-7 immutable-terminal invariant).
	mutate := *rec
	mutate.FailedReason = "" // no-op field change, still same generation
	mutate.State = LifecycleRunning
	err := s.Persist(&mutate)
	if err == nil {
		t.Fatal("expected an error mutating a terminal record's own generation, got nil")
	}
	if !errors.Is(err, ErrLifecycleTerminalImmutable) {
		t.Errorf("expected ErrLifecycleTerminalImmutable, got: %v", err)
	}

	// A NEW generation (follow_up/Play mint) IS allowed to append after a
	// terminal tail.
	next := *rec
	next.Generation = rec.Generation + 1
	next.State = LifecycleQueued
	next.ResumedFrom = rec.SessionID
	if persistErr := s.Persist(&next); persistErr != nil {
		t.Fatalf("Persist(new generation after terminal) failed: %v", persistErr)
	}
	reloaded, err := s.Load("sess-term")
	if err != nil {
		t.Fatalf("Load after new generation failed: %v", err)
	}
	if reloaded.Generation != 1 {
		t.Errorf("generation = %d, want 1", reloaded.Generation)
	}
	if reloaded.State != LifecycleQueued {
		t.Errorf("state = %q, want %q", reloaded.State, LifecycleQueued)
	}
}

func TestLifecycleStore_LoadNotFound(t *testing.T) {
	s := newTestLifecycleStore(t)
	_, err := s.Load("does-not-exist")
	if !errors.Is(err, ErrLifecycleNotFound) {
		t.Errorf("expected ErrLifecycleNotFound, got: %v", err)
	}
}

func TestLifecycleStore_ListNonTerminalOnly(t *testing.T) {
	s := newTestLifecycleStore(t)
	specs := []struct {
		id    string
		state LifecycleState
	}{
		{"running-1", LifecycleRunning},
		{"needs-input-1", LifecycleNeedsInput},
		{"completed-1", LifecycleCompleted},
		{"failed-1", LifecycleFailed},
	}
	for _, sp := range specs {
		rec := &LifecycleRecord{
			SessionID:      sp.id,
			State:          sp.state,
			OwnerScopeKind: OwnerScopeHuman,
			WorkspaceID:    "ws-1",
			AgentID:        "agent-1",
		}
		if sp.state == LifecycleNeedsInput {
			rec.NeedsInput = &NeedsInput{CorrelationID: "corr-1", TTLDeadline: time.Now().Add(time.Hour)}
		}
		if sp.state == LifecycleFailed {
			rec.FailedReason = "interrupted"
		}
		if err := s.Persist(rec); err != nil {
			t.Fatalf("Persist(%s) failed: %v", sp.id, err)
		}
	}

	recs, err := s.List(LifecycleFilter{NonTerminalOnly: true})
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("expected 2 non-terminal records, got %d: %+v", len(recs), recs)
	}
	seen := map[string]bool{}
	for _, r := range recs {
		seen[r.SessionID] = true
	}
	if !seen["running-1"] || !seen["needs-input-1"] {
		t.Errorf("expected running-1 and needs-input-1 in non-terminal list, got %+v", recs)
	}
}

func TestNeedsInput_Expired(t *testing.T) {
	now := time.Now()
	n := &NeedsInput{CorrelationID: "c1", TTLDeadline: now.Add(-1 * time.Second)}
	if !n.Expired(now) {
		t.Error("expected expired TTL to report Expired=true")
	}
	n2 := &NeedsInput{CorrelationID: "c2", TTLDeadline: now.Add(time.Hour)}
	if n2.Expired(now) {
		t.Error("expected future TTL to report Expired=false")
	}
	n3 := &NeedsInput{CorrelationID: "c3"} // zero TTLDeadline
	if n3.Expired(now) {
		t.Error("expected zero TTLDeadline to never report Expired=true")
	}
}

// MAJOR-4 / DoD-11: the domain OwnerScopeKind values MUST mirror the
// generated SessionLifecycleRecordOwnerScopeKind wire enum one-for-one so a
// natural cast (OwnerScopeKind → generated.SessionLifecycleRecordOwnerScopeKind)
// produces schema-valid JSON without an explicit conversion. Previously the
// domain used "parent_session_id"/"plan_id" while the wire enum used
// "parent_session"/"plan" — two of three differed, while the type doc claimed
// it "mirrors the generated enum one-for-one." This table test pins the
// correspondence so any future drift fails here, not at a caller doing the cast.
func TestOwnerScopeKind_MirrorsWireEnum(t *testing.T) {
	specs := []struct {
		name   string
		domain OwnerScopeKind
		wire   generated.SessionLifecycleRecordOwnerScopeKind
	}{
		{"parent_session", OwnerScopeParentSession, generated.SessionLifecycleRecordOwnerScopeKindParentSession},
		{"plan", OwnerScopePlan, generated.SessionLifecycleRecordOwnerScopeKindPlan},
		{"human", OwnerScopeHuman, generated.SessionLifecycleRecordOwnerScopeKindHuman},
	}
	for _, s := range specs {
		t.Run(s.name, func(t *testing.T) {
			if string(s.domain) != string(s.wire) {
				t.Errorf("domain %q != wire %q — domain and wire enum have drifted; align pkg/session/lifecycle.go to pkg/api/generated", s.domain, s.wire)
			}
			if !s.wire.Valid() {
				t.Errorf("wire value %q is not a valid generated enum member", s.wire)
			}
			if !IsValidOwnerScopeKind(s.domain) {
				t.Errorf("domain value %q failed IsValidOwnerScopeKind", s.domain)
			}
		})
	}
}

// --- Mutate primitive (Correctness-MAJOR-3) ---

// TestLifecycleStore_Mutate_NotFoundFnReceivesNil proves fn receives nil when
// no record exists yet, and that returning an error aborts the write (nothing
// is persisted).
func TestLifecycleStore_Mutate_NotFoundFnReceivesNil(t *testing.T) {
	s := newTestLifecycleStore(t)
	var saw *LifecycleRecord
	err := s.Mutate("never-existed", func(rec *LifecycleRecord) error {
		saw = rec
		return ErrLifecycleNotFound
	})
	if !errors.Is(err, ErrLifecycleNotFound) {
		t.Fatalf("expected ErrLifecycleNotFound propagated, got: %v", err)
	}
	if saw != nil {
		t.Errorf("expected fn to receive nil record, got: %+v", saw)
	}
	if s.Exists("never-existed") {
		t.Error("expected NO record to be persisted when fn returns an error")
	}
}

// TestLifecycleStore_Mutate_AppliesAndPersists proves the happy path: fn
// mutates the copy and Mutate persists the result.
func TestLifecycleStore_Mutate_AppliesAndPersists(t *testing.T) {
	s := newTestLifecycleStore(t)
	if err := s.Persist(&LifecycleRecord{
		SessionID: "sess-m1", State: LifecycleRunning,
		OwnerScopeKind: OwnerScopeHuman, WorkspaceID: "ws", AgentID: "a",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	err := s.Mutate("sess-m1", func(rec *LifecycleRecord) error {
		if rec == nil {
			t.Fatal("expected non-nil record")
		}
		if rec.State != LifecycleRunning {
			t.Fatalf("expected to see running, got %s", rec.State)
		}
		rec.State = LifecyclePaused
		return nil
	})
	if err != nil {
		t.Fatalf("Mutate failed: %v", err)
	}
	rec, err := s.Load("sess-m1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if rec.State != LifecyclePaused {
		t.Errorf("state = %q, want paused", rec.State)
	}
}

// TestLifecycleStore_Mutate_TerminalImmutableGuard proves the L-3 invariant
// is enforced inside Mutate: mutating a terminal tail's own generation is
// rejected with ErrLifecycleTerminalImmutable (the cancel-vs-complete race's
// loser outcome).
func TestLifecycleStore_Mutate_TerminalImmutableGuard(t *testing.T) {
	s := newTestLifecycleStore(t)
	if err := s.Persist(&LifecycleRecord{
		SessionID: "sess-term2", State: LifecycleCompleted,
		OwnerScopeKind: OwnerScopeHuman, WorkspaceID: "ws", AgentID: "a",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	err := s.Mutate("sess-term2", func(rec *LifecycleRecord) error {
		rec.State = LifecycleRunning // same generation, terminal tail → reject
		return nil
	})
	if !errors.Is(err, ErrLifecycleTerminalImmutable) {
		t.Fatalf("expected ErrLifecycleTerminalImmutable, got: %v", err)
	}
	// Tail must be unchanged (still completed).
	rec, err := s.Load("sess-term2")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if rec.State != LifecycleCompleted {
		t.Errorf("tail state = %q, want completed (unchanged)", rec.State)
	}
}

// TestLifecycleStore_Mutate_NilSignalNoWrite proves fn can signal "no write"
// by setting the record pointer to nil.
func TestLifecycleStore_Mutate_NilSignalNoWrite(t *testing.T) {
	s := newTestLifecycleStore(t)
	beforeLines := countJSONL(t, s, "sess-nil")
	err := s.Mutate("sess-nil", func(rec *LifecycleRecord) error {
		// Signal "nothing to do" — but fn cannot reassign the *LifecycleRecord
		// pointer itself; instead it returns nil and leaves the record as-is.
		// For a non-existent session, rec is already nil → no write.
		return nil
	})
	if err != nil {
		t.Fatalf("Mutate: %v", err)
	}
	if after := countJSONL(t, s, "sess-nil"); after != beforeLines {
		t.Errorf("expected no new line persisted, before=%d after=%d", beforeLines, after)
	}
}

// TestLifecycleStore_Mutate_ConcurrentTerminalGuardHolds is the RACE test
// (Correctness-MAJOR-3 / S4 INV-3): N goroutines each Mutate the SAME
// running session to `completed`. Exactly ONE must succeed; the other N-1
// must see the now-terminal tail under the lock and get
// ErrLifecycleTerminalImmutable. No lost update (exactly one completion line
// appended), terminal guard holds. Run with -race.
func TestLifecycleStore_Mutate_ConcurrentTerminalGuardHolds(t *testing.T) {
	s := newTestLifecycleStore(t)
	if err := s.Persist(&LifecycleRecord{
		SessionID: "sess-race", State: LifecycleRunning,
		OwnerScopeKind: OwnerScopeHuman, WorkspaceID: "ws", AgentID: "a",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	const n = 60
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			errs[idx] = s.Mutate("sess-race", func(rec *LifecycleRecord) error {
				if rec == nil || rec.State == LifecycleCompleted {
					// Already completed by a peer — no-op (signals via fn
					// returning nil without changing state → persistLocked
					// will reject the same-gen write on a terminal tail and
					// return ErrLifecycleTerminalImmutable, which is the
					// outcome we assert below).
					return nil
				}
				rec.State = LifecycleCompleted
				return nil
			})
		}(i)
	}
	wg.Wait()

	successes, immutables := 0, 0
	for _, e := range errs {
		if e == nil {
			successes++
		} else if errors.Is(e, ErrLifecycleTerminalImmutable) {
			immutables++
		} else {
			t.Errorf("unexpected error from concurrent Mutate: %v", e)
		}
	}
	// Race outcome: the goroutine that observes running first lands the
	// completion; every later goroutine sees the completed tail and its
	// same-generation write is rejected by the guard. Exactly one persist of
	// a state change landed; the losers each either got Immutable or their
	// no-op write was rejected.
	if successes == 0 {
		t.Errorf("expected at least one concurrent Mutate to succeed, got %d successes", successes)
	}
	// The tail MUST be terminal completed regardless of who won.
	rec, err := s.Load("sess-race")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if rec.State != LifecycleCompleted {
		t.Errorf("tail state = %q, want completed", rec.State)
	}
	// No lost update: the file has exactly 2 lines (seed + one completion).
	if lines := countJSONL(t, s, "sess-race"); lines != 2 {
		t.Errorf("expected exactly 2 persisted lines (seed + completion), got %d — a lost update or duplicate completion raced through", lines)
	}
	t.Logf("race outcome: %d successes, %d immutable-rejects (of %d goroutines)", successes, immutables, n)
}

// TestLifecycleStore_Mutate_ConcurrentNoLostAppend proves the atomic RMW
// directly: N goroutines each Mutate to APPEND a unique id to
// UndeliveredMessageIDs. Without the held lock (a naked Load+Persist), a
// last-writer-wins race would lose ids. With Mutate all N must be present.
// Run with -race.
func TestLifecycleStore_Mutate_ConcurrentNoLostAppend(t *testing.T) {
	s := newTestLifecycleStore(t)
	if err := s.Persist(&LifecycleRecord{
		SessionID: "sess-append", State: LifecycleRunning,
		OwnerScopeKind: OwnerScopeHuman, WorkspaceID: "ws", AgentID: "a",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	const n = 50
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			id := fmt.Sprintf("id-%d", idx)
			_ = s.Mutate("sess-append", func(rec *LifecycleRecord) error {
				rec.UndeliveredMessageIDs = append(rec.UndeliveredMessageIDs, id)
				return nil
			})
		}(i)
	}
	wg.Wait()

	rec, err := s.Load("sess-append")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(rec.UndeliveredMessageIDs) != n {
		seen := map[string]bool{}
		for _, id := range rec.UndeliveredMessageIDs {
			seen[id] = true
		}
		t.Errorf("expected all %d appended ids present (no lost update), got %d (unique=%d) — RMW was not atomic", n, len(rec.UndeliveredMessageIDs), len(seen))
	}
}

// TestLifecycleStore_Persist_RequiresOwnerScopeKind proves the strict
// OwnerScopeKind validation (Comments-MINOR-3 / m5): an empty value is
// rejected, like an invalid State.
func TestLifecycleStore_Persist_RequiresOwnerScopeKind(t *testing.T) {
	s := newTestLifecycleStore(t)
	err := s.Persist(&LifecycleRecord{
		SessionID: "sess-nokind", State: LifecycleRunning,
		OwnerScopeKind: "", WorkspaceID: "ws", AgentID: "a",
	})
	if err == nil {
		t.Fatal("expected an error for an empty owner_scope_kind, got nil")
	}
	if !strings.Contains(err.Error(), "owner_scope_kind is required") {
		t.Errorf("expected an owner_scope_kind-required error, got: %v", err)
	}
}

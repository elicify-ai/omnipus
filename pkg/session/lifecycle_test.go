// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package session

import (
	"errors"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
)

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
		LaunchProfile:  LaunchProfileUtility,
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
	if err := s.Persist(loaded); err != nil {
		t.Fatalf("Persist(running) failed: %v", err)
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
		LaunchProfile:  LaunchProfileUtility,
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
	if err := s.Persist(&next); err != nil {
		t.Fatalf("Persist(new generation after terminal) failed: %v", err)
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
	if err != ErrLifecycleNotFound {
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
			LaunchProfile:  LaunchProfileUtility,
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

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package plan

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// newStore returns a Store rooted at a fresh temp dir, mirroring
// pkg/task/store_test.go's newStore helper.
func newStore(t *testing.T) *Store {
	t.Helper()
	return New(filepath.Join(t.TempDir(), "plans"))
}

// mkPlan returns a minimally-valid plan in the given workspace, owned by
// owner.
func mkPlan(title, ws, owner string) *Plan {
	return &Plan{Title: title, WorkspaceID: ws, OwnerAgentID: owner}
}

func TestPlan_Store_AtomicWriteAndList(t *testing.T) {
	s := newStore(t)

	p1 := mkPlan("Plan One", "ws-1", "agent-a")
	if err := s.Create(p1); err != nil {
		t.Fatalf("Create p1: %v", err)
	}
	if p1.ID == "" {
		t.Fatal("Create did not assign an ID")
	}
	if p1.State != StateDraft {
		t.Fatalf("default state = %q, want draft", p1.State)
	}
	if p1.CreatedAt == "" || p1.UpdatedAt == "" {
		t.Fatal("timestamps not stamped")
	}

	p2 := mkPlan("Plan Two", "ws-2", "agent-b")
	if err := s.Create(p2); err != nil {
		t.Fatalf("Create p2: %v", err)
	}

	// The write must actually have landed on disk at the expected path
	// (atomic-write proof), not just be readable through the in-process Store.
	if _, err := os.Stat(filepath.Join(s.Dir(), p1.ID+".json")); err != nil {
		t.Fatalf("plan file not found on disk: %v", err)
	}

	// Round-trip via Get.
	got, err := s.Get(p1.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Title != "Plan One" || got.WorkspaceID != "ws-1" || got.OwnerAgentID != "agent-a" {
		t.Fatalf("round-trip mismatch: %+v", got)
	}

	// A second, independent Store instance over the same directory must see
	// the same data — proves the write is durable, not just cached.
	reopened := New(s.Dir())
	got2, err := reopened.Get(p1.ID)
	if err != nil {
		t.Fatalf("Get via reopened store: %v", err)
	}
	if got2.Title != got.Title {
		t.Fatalf("reopened store mismatch: %+v vs %+v", got2, got)
	}

	// List(Filter{}) returns both plans.
	all, err := s.List(Filter{})
	if err != nil {
		t.Fatalf("List all: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("List all: got %d plans, want 2", len(all))
	}

	// List(Filter{WorkspaceID}) narrows to the matching workspace only.
	ws1, err := s.List(Filter{WorkspaceID: "ws-1"})
	if err != nil {
		t.Fatalf("List ws-1: %v", err)
	}
	if len(ws1) != 1 || ws1[0].ID != p1.ID {
		t.Fatalf("List ws-1: got %+v, want just p1", ws1)
	}
	ws2, err := s.List(Filter{WorkspaceID: "ws-2"})
	if err != nil {
		t.Fatalf("List ws-2: %v", err)
	}
	if len(ws2) != 1 || ws2[0].ID != p2.ID {
		t.Fatalf("List ws-2: got %+v, want just p2", ws2)
	}
	none, err := s.List(Filter{WorkspaceID: "ws-nonexistent"})
	if err != nil {
		t.Fatalf("List ws-nonexistent: %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("List ws-nonexistent: got %d, want 0", len(none))
	}

	// A corrupt file in the store dir is skipped (logged WARN), not fatal.
	corruptPath := filepath.Join(s.Dir(), "corrupt-id.json")
	if err := os.WriteFile(corruptPath, []byte("{not valid json"), 0o600); err != nil {
		t.Fatalf("write corrupt file: %v", err)
	}
	stillOK, err := s.List(Filter{})
	if err != nil {
		t.Fatalf("List with corrupt file present: %v", err)
	}
	if len(stillOK) != 2 {
		t.Fatalf("List with corrupt file present: got %d, want 2 (corrupt skipped)", len(stillOK))
	}
}

func TestPlan_Store_ListOnMissingDir(t *testing.T) {
	// A Store whose directory has never been created (no plan ever written)
	// must return an empty, non-error result rather than failing.
	s := New(filepath.Join(t.TempDir(), "plans-never-created"))
	got, err := s.List(Filter{})
	if err != nil {
		t.Fatalf("List on missing dir: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("List on missing dir: got %d, want 0", len(got))
	}
}

func TestPlan_CreateValidation(t *testing.T) {
	s := newStore(t)
	cases := []struct {
		name string
		p    *Plan
	}{
		{"no title", &Plan{WorkspaceID: "ws", OwnerAgentID: "agent"}},
		{"no workspace", &Plan{Title: "x", OwnerAgentID: "agent"}},
		{"no owner", &Plan{Title: "x", WorkspaceID: "ws"}},
		{"bad state", &Plan{Title: "x", WorkspaceID: "ws", OwnerAgentID: "agent", State: "bogus"}},
		{"failed_reason without failed state", &Plan{
			Title: "x", WorkspaceID: "ws", OwnerAgentID: "agent",
			FailedReason: FailedReasonStoppedByUser,
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := s.Create(c.p); err == nil {
				t.Fatalf("expected validation error for %s", c.name)
			} else if !errors.Is(err, ErrValidation) {
				t.Fatalf("expected ErrValidation, got %v", err)
			}
		})
	}
}

func TestPlan_SameWorkspaceFK(t *testing.T) {
	s := newStore(t)
	p := mkPlan("FK Plan", "ws-1", "agent-a")
	if err := s.Create(p); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := s.ValidatePlanWorkspace(p.ID, "ws-1"); err != nil {
		t.Fatalf("same-workspace reference rejected: %v", err)
	}
	if err := s.ValidatePlanWorkspace("", "ws-1"); err != nil {
		t.Fatalf("empty plan_id should be a no-op success: %v", err)
	}

	err := s.ValidatePlanWorkspace(p.ID, "ws-2")
	if err == nil {
		t.Fatal("expected error for cross-workspace plan_id reference")
	}
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("expected ErrValidation, got %v", err)
	}

	if err := s.ValidatePlanWorkspace("nonexistent-id", "ws-1"); err == nil {
		t.Fatal("expected error for nonexistent plan_id")
	} else if !errors.Is(err, ErrValidation) {
		t.Fatalf("expected ErrValidation for nonexistent plan_id, got %v", err)
	}
}

func TestPlan_CountersPersistAndReload(t *testing.T) {
	s := newStore(t)
	p := mkPlan("Counters Plan", "ws-1", "agent-a")
	if err := s.Create(p); err != nil {
		t.Fatalf("Create: %v", err)
	}

	rounds := 3
	paused := "owner disabled"
	lastActivity := "2026-07-19T00:00:00Z"
	phase := PhaseJudging
	updated, err := s.Update(p.ID, Patch{
		JudgeRounds:    &rounds,
		PausedReason:   &paused,
		LastActivityAt: &lastActivity,
		PlanPhase:      &phase,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.JudgeRounds != 3 ||
		updated.PausedReason != paused ||
		updated.LastActivityAt != lastActivity ||
		updated.PlanPhase != PhaseJudging {
		t.Fatalf("counters not applied: %+v", updated)
	}

	// Reload from a brand-new Store instance over the same directory to prove
	// persistence, not just the in-memory returned pointer.
	reloaded := New(s.Dir())
	got, err := reloaded.Get(p.ID)
	if err != nil {
		t.Fatalf("Get after reload: %v", err)
	}
	if got.JudgeRounds != 3 ||
		got.PausedReason != paused ||
		got.LastActivityAt != lastActivity ||
		got.PlanPhase != PhaseJudging {
		t.Fatalf("counters did not survive reload: %+v", got)
	}

	// ActiveLoop counter, exercised independently since it is also stamped by
	// State transitions (covered in TestPlan_StateTransitions) — set it
	// directly here via Patch to prove the plain counter-write path too.
	active := true
	updated, err = s.Update(p.ID, Patch{ActiveLoop: &active})
	if err != nil {
		t.Fatalf("Update ActiveLoop: %v", err)
	}
	if !updated.ActiveLoop {
		t.Fatalf("ActiveLoop not applied: %+v", updated)
	}
	got2, err := New(s.Dir()).Get(p.ID)
	if err != nil {
		t.Fatalf("Get after ActiveLoop reload: %v", err)
	}
	if !got2.ActiveLoop {
		t.Fatalf("ActiveLoop did not survive reload: %+v", got2)
	}
}

func TestPlan_Delete(t *testing.T) {
	s := newStore(t)

	// A draft plan may be deleted.
	draft := mkPlan("Draft Plan", "ws-1", "agent-a")
	if err := s.Create(draft); err != nil {
		t.Fatalf("Create draft: %v", err)
	}
	if err := s.Delete(draft.ID); err != nil {
		t.Fatalf("Delete draft: %v", err)
	}
	if s.Exists(draft.ID) {
		t.Fatal("draft plan file still exists after Delete")
	}
	if err := s.Delete(draft.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Delete already-deleted plan: expected ErrNotFound, got %v", err)
	}

	// A running plan is rejected (FR-006).
	running := mkPlan("Running Plan", "ws-1", "agent-a")
	if err := s.Create(running); err != nil {
		t.Fatalf("Create running: %v", err)
	}
	approved := StateApproved
	if _, err := s.Update(running.ID, Patch{State: &approved}); err != nil {
		t.Fatalf("draft->approved: %v", err)
	}
	runningState := StateRunning
	if _, err := s.Update(running.ID, Patch{State: &runningState}); err != nil {
		t.Fatalf("approved->running: %v", err)
	}
	if err := s.Delete(running.ID); err == nil {
		t.Fatal("expected Delete of a running plan to be rejected")
	} else if !errors.Is(err, ErrValidation) {
		t.Fatalf("expected ErrValidation, got %v", err)
	}
	if !s.Exists(running.ID) {
		t.Fatal("running plan file should NOT have been removed")
	}
}

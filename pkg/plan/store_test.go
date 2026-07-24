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
	if writeErr := os.WriteFile(corruptPath, []byte("{not valid json"), 0o600); writeErr != nil {
		t.Fatalf("write corrupt file: %v", writeErr)
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

// driveToFailed pushes plan id through draft->approved->running->failed
// (reason), optionally stamping a non-zero JudgeRounds and a PausedReason
// before the failing transition so the restart tests below have real,
// non-zero state to assert gets cleared (JudgeRounds) or left alone
// (PausedReason — orthogonal, ADR-052 §6.7).
func driveToFailed(t *testing.T, s *Store, id string, reason FailedReason, judgeRounds int, pausedReason string) *Plan {
	t.Helper()
	approved, running, failed := StateApproved, StateRunning, StateFailed
	if _, err := s.Update(id, Patch{State: &approved}); err != nil {
		t.Fatalf("draft->approved: %v", err)
	}
	if _, err := s.Update(id, Patch{State: &running}); err != nil {
		t.Fatalf("approved->running: %v", err)
	}
	if judgeRounds > 0 {
		rounds := judgeRounds
		if _, err := s.Update(id, Patch{JudgeRounds: &rounds}); err != nil {
			t.Fatalf("set judge_rounds: %v", err)
		}
	}
	if pausedReason != "" {
		pr := pausedReason
		if _, err := s.Update(id, Patch{PausedReason: &pr}); err != nil {
			t.Fatalf("set paused_reason: %v", err)
		}
	}
	updated, err := s.Update(id, Patch{State: &failed, FailedReason: &reason})
	if err != nil {
		t.Fatalf("running->failed(%s): %v", reason, err)
	}
	return updated
}

// TestPlan_RestartTransition_ViaStoreUpdate exercises the store-level
// reason-aware RESTART guard (ADR-052 §6.7, spec FR-016/FR-017/DS-1)
// end-to-end through the SAME Store.Update(id, Patch{State: &approved}) call
// shape already used at every other approve/stop call site
// (pkg/gateway/rest_plans.go, pkg/agent/plan_engine.go) — no new exported
// entry point, per the task's "explicit guarded path in the store's
// Update/validation flow" instruction.
func TestPlan_RestartTransition_ViaStoreUpdate(t *testing.T) {
	t.Run("stopped_by_user restart succeeds, clears reason, resets judge_rounds, leaves paused_reason alone", func(t *testing.T) {
		s := newStore(t)
		p := mkPlan("Restartable Plan", "ws-1", "agent-a")
		if err := s.Create(p); err != nil {
			t.Fatalf("Create: %v", err)
		}
		failedPlan := driveToFailed(t, s, p.ID, FailedReasonStoppedByUser, 2, "owner paused mid-run")
		if failedPlan.FailedReason != FailedReasonStoppedByUser || failedPlan.JudgeRounds != 2 {
			t.Fatalf("setup: unexpected pre-restart state: %+v", failedPlan)
		}

		approved := StateApproved
		restarted, err := s.Update(p.ID, Patch{State: &approved})
		if err != nil {
			t.Fatalf("restart transition failed[stopped_by_user]->approved: %v", err)
		}
		if restarted.State != StateApproved {
			t.Fatalf("restart: state = %q, want approved (engine promotes to running separately)", restarted.State)
		}
		if restarted.FailedReason != "" {
			t.Fatalf("restart must clear failed_reason, got %q", restarted.FailedReason)
		}
		if restarted.JudgeRounds != 0 {
			t.Fatalf("restart must reset judge_rounds to 0, got %d", restarted.JudgeRounds)
		}
		// ApprovedAt is re-stamped by the StateApproved case in the same
		// lifecycle-timestamp switch every other approve transition uses
		// (store.go); RFC3339 is second-resolution so a same-second re-stamp
		// cannot be distinguished from the original by string inequality —
		// non-empty is the assertion this test can make reliably.
		if restarted.ApprovedAt == "" {
			t.Fatalf("restart must stamp ApprovedAt, got empty")
		}
		if restarted.PausedReason != "owner paused mid-run" {
			t.Fatalf("restart must NOT touch paused_reason (orthogonal), got %q", restarted.PausedReason)
		}

		// Persistence proof: reload from a brand-new Store instance over the
		// same directory.
		reloaded, err := New(s.Dir()).Get(p.ID)
		if err != nil {
			t.Fatalf("Get after restart: %v", err)
		}
		if reloaded.State != StateApproved || reloaded.FailedReason != "" || reloaded.JudgeRounds != 0 {
			t.Fatalf("restart did not survive reload: %+v", reloaded)
		}
	})

	t.Run("genuine failures stay frozen — restart rejected", func(t *testing.T) {
		for _, reason := range []FailedReason{FailedReasonJudgeRoundsExhausted, FailedReasonIdleExpired} {
			t.Run(string(reason), func(t *testing.T) {
				s := newStore(t)
				p := mkPlan("Frozen Plan", "ws-1", "agent-a")
				if err := s.Create(p); err != nil {
					t.Fatalf("Create: %v", err)
				}
				driveToFailed(t, s, p.ID, reason, 3, "")

				approved := StateApproved
				if _, err := s.Update(p.ID, Patch{State: &approved}); !errors.Is(err, ErrRestartNotPermitted) {
					t.Fatalf("restart from failed(%s) must be rejected with ErrRestartNotPermitted, got %v", reason, err)
				}

				// The rejected restart must NOT mutate the plan on disk.
				reloaded, err := s.Get(p.ID)
				if err != nil {
					t.Fatalf("Get after rejected restart: %v", err)
				}
				if reloaded.State != StateFailed || reloaded.FailedReason != reason || reloaded.JudgeRounds != 3 {
					t.Fatalf("plan mutated by rejected restart: %+v", reloaded)
				}
			})
		}
	})

	t.Run("failed to running stays illegal even with stopped_by_user reason", func(t *testing.T) {
		s := newStore(t)
		p := mkPlan("No Direct Running Plan", "ws-1", "agent-a")
		if err := s.Create(p); err != nil {
			t.Fatalf("Create: %v", err)
		}
		driveToFailed(t, s, p.ID, FailedReasonStoppedByUser, 1, "")

		running := StateRunning
		if _, err := s.Update(p.ID, Patch{State: &running}); !errors.Is(err, ErrIllegalPlanTransition) {
			t.Fatalf("failed[stopped_by_user]->running must stay illegal, got %v", err)
		}
		reloaded, err := s.Get(p.ID)
		if err != nil {
			t.Fatalf("Get after rejected failed->running: %v", err)
		}
		if reloaded.State != StateFailed || reloaded.JudgeRounds != 1 {
			t.Fatalf("plan mutated by rejected failed->running, got %+v", reloaded)
		}
	})

	t.Run("done is not a cancel — cannot restart to approved", func(t *testing.T) {
		s := newStore(t)
		p := mkPlan("Done Plan", "ws-1", "agent-a")
		if err := s.Create(p); err != nil {
			t.Fatalf("Create: %v", err)
		}
		approved, running, done := StateApproved, StateRunning, StateDone
		if _, err := s.Update(p.ID, Patch{State: &approved}); err != nil {
			t.Fatalf("draft->approved: %v", err)
		}
		if _, err := s.Update(p.ID, Patch{State: &running}); err != nil {
			t.Fatalf("approved->running: %v", err)
		}
		if _, err := s.Update(p.ID, Patch{State: &done}); err != nil {
			t.Fatalf("running->done: %v", err)
		}
		reapprove := StateApproved
		if _, err := s.Update(p.ID, Patch{State: &reapprove}); !errors.Is(err, ErrIllegalPlanTransition) {
			t.Fatalf("done->approved must be rejected, got %v", err)
		}
	})

	t.Run("restart lands at approved, never running (engine promotes under the cap separately)", func(t *testing.T) {
		s := newStore(t)
		p := mkPlan("Cap Deferred Plan", "ws-1", "agent-a")
		if err := s.Create(p); err != nil {
			t.Fatalf("Create: %v", err)
		}
		driveToFailed(t, s, p.ID, FailedReasonStoppedByUser, 0, "")
		approved := StateApproved
		restarted, err := s.Update(p.ID, Patch{State: &approved})
		if err != nil {
			t.Fatalf("restart: %v", err)
		}
		if restarted.State != StateApproved {
			t.Fatalf("restart must land at approved (never running directly), got %q", restarted.State)
		}
	})

	t.Run("restart rejects an explicit failed_reason/judge_rounds in the same patch (fix-wave #4)", func(t *testing.T) {
		s := newStore(t)
		p := mkPlan("Explicit Patch Plan", "ws-1", "agent-a")
		if err := s.Create(p); err != nil {
			t.Fatalf("Create: %v", err)
		}
		driveToFailed(t, s, p.ID, FailedReasonStoppedByUser, 5, "")

		approved := StateApproved
		conflictingReason := FailedReasonStoppedByUser // still a valid enum value on its own
		if _, err := s.Update(p.ID, Patch{State: &approved, FailedReason: &conflictingReason}); !errors.Is(err, ErrValidation) {
			t.Fatalf("restart + explicit failed_reason in the same patch must be rejected (ErrValidation), got %v", err)
		}

		conflictingRounds := 7
		if _, err := s.Update(p.ID, Patch{State: &approved, JudgeRounds: &conflictingRounds}); !errors.Is(err, ErrValidation) {
			t.Fatalf("restart + explicit judge_rounds in the same patch must be rejected (ErrValidation), got %v", err)
		}

		// Neither rejected patch may have mutated the plan.
		reloaded, err := s.Get(p.ID)
		if err != nil {
			t.Fatalf("Get after rejected restart patches: %v", err)
		}
		if reloaded.State != StateFailed || reloaded.FailedReason != FailedReasonStoppedByUser || reloaded.JudgeRounds != 5 {
			t.Fatalf("plan mutated by a rejected restart patch: %+v", reloaded)
		}

		// A plain restart (neither field set) still succeeds and performs the
		// clean-slate reset.
		restarted, err := s.Update(p.ID, Patch{State: &approved})
		if err != nil {
			t.Fatalf("plain restart: %v", err)
		}
		if restarted.FailedReason != "" {
			t.Fatalf("restart must clear failed_reason, got %q", restarted.FailedReason)
		}
		if restarted.JudgeRounds != 0 {
			t.Fatalf("restart must reset judge_rounds to 0, got %d", restarted.JudgeRounds)
		}
	})

	t.Run("crafted-patch attack: forged failed_reason cannot restart a genuinely-failed plan (fix-wave #4)", func(t *testing.T) {
		s := newStore(t)
		p := mkPlan("Attack Target Plan", "ws-1", "agent-a")
		if err := s.Create(p); err != nil {
			t.Fatalf("Create: %v", err)
		}
		// The plan's REAL on-disk reason is judge_rounds_exhausted — never
		// restartable.
		driveToFailed(t, s, p.ID, FailedReasonJudgeRoundsExhausted, 3, "")

		approved := StateApproved
		forged := FailedReasonStoppedByUser
		if _, err := s.Update(p.ID, Patch{State: &approved, FailedReason: &forged}); !errors.Is(err, ErrValidation) {
			t.Fatalf("crafted patch (state=approved + forged failed_reason=stopped_by_user) must be rejected, got %v", err)
		}

		// The plan must remain exactly as it was: still failed, still its
		// REAL reason, never restarted.
		reloaded, err := s.Get(p.ID)
		if err != nil {
			t.Fatalf("Get after rejected crafted-patch attack: %v", err)
		}
		if reloaded.State != StateFailed || reloaded.FailedReason != FailedReasonJudgeRoundsExhausted {
			t.Fatalf("crafted-patch attack mutated the plan: %+v", reloaded)
		}
	})
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

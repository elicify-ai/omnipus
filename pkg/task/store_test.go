// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package task

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// removeFileRaw deletes a task's JSON file directly, bypassing Store.Delete (and
// thus its cascade). Used to simulate a manual deletion / crash leaving a
// dangling blocked_by edge.
func removeFileRaw(s *Store, id string) error {
	return os.Remove(filepath.Join(s.dir, id+".json"))
}

func newStore(t *testing.T) *Store {
	t.Helper()
	return New(t.TempDir())
}

// mkTask returns a minimally-valid task in the given workspace.
func mkTask(title, ws string) *Task {
	return &Task{Title: title, Action: ActionLLM, WorkspaceID: ws}
}

func TestCreateGetRoundTrip(t *testing.T) {
	s := newStore(t)
	in := mkTask("Analyze logs", "ws-1")
	in.Prompt = "do it"
	in.Priority = 2
	in.Todos = []Todo{{Text: "step one", Status: TodoPending}}
	if err := s.Create(in); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if in.ID == "" {
		t.Fatal("Create did not assign an ID")
	}
	if in.Status != StatusInbox {
		t.Fatalf("default status = %q, want inbox", in.Status)
	}
	got, err := s.Get(in.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Title != "Analyze logs" || got.WorkspaceID != "ws-1" || got.Priority != 2 {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
	if len(got.Todos) != 1 || got.Todos[0].Text != "step one" {
		t.Fatalf("todos not preserved: %+v", got.Todos)
	}
	if got.CreatedAt == "" || got.UpdatedAt == "" {
		t.Fatal("timestamps not stamped")
	}
}

func TestCreateValidation(t *testing.T) {
	s := newStore(t)
	cases := []struct {
		name string
		t    *Task
	}{
		{"no title", &Task{Action: ActionLLM, WorkspaceID: "ws"}},
		{"no workspace", &Task{Title: "x", Action: ActionLLM}},
		{"bad priority", &Task{Title: "x", Action: ActionLLM, WorkspaceID: "ws", Priority: 9}},
		{"bad status", &Task{Title: "x", Action: ActionLLM, WorkspaceID: "ws", Status: "bogus"}},
		{"bad action", &Task{Title: "x", Action: "tool", WorkspaceID: "ws"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := s.Create(c.t); err == nil {
				t.Fatalf("expected validation error for %s", c.name)
			}
		})
	}
}

func TestUpdatePartial(t *testing.T) {
	s := newStore(t)
	tk := mkTask("t", "ws")
	if err := s.Create(tk); err != nil {
		t.Fatal(err)
	}
	newStatus := StatusInProgress
	newTitle := "renamed"
	got, err := s.Update(tk.ID, Patch{Status: &newStatus, Title: &newTitle})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got.Status != StatusInProgress || got.Title != "renamed" {
		t.Fatalf("update not applied: %+v", got)
	}
	// Unspecified fields preserved.
	if got.WorkspaceID != "ws" || got.Action != ActionLLM {
		t.Fatalf("update clobbered other fields: %+v", got)
	}
}

// TestUpdate_TransitionToInProgress_StampsStartedAt is a regression test for
// the Board card bug where "Started" never populated: a plain Update() call
// that flips status into in_progress (the path the REST PATCH "Start" action
// uses — see rest_tasks.go's handleTaskUpdate, which calls Store.Update
// directly rather than ClaimForRun) must stamp StartedAt itself when the
// caller did not supply one, mirroring what ClaimForRun already does for the
// scheduler/heartbeat dispatch path.
func TestUpdate_TransitionToInProgress_StampsStartedAt(t *testing.T) {
	s := newStore(t)
	tk := mkTask("t", "ws")
	if err := s.Create(tk); err != nil {
		t.Fatal(err)
	}
	if tk.StartedAt != "" {
		t.Fatalf("newly-created task must start with empty StartedAt, got %q", tk.StartedAt)
	}

	newStatus := StatusInProgress
	got, err := s.Update(tk.ID, Patch{Status: &newStatus})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got.Status != StatusInProgress {
		t.Fatalf("status not applied: %+v", got)
	}
	if got.StartedAt == "" {
		t.Fatal("Update did not auto-stamp StartedAt on transition into in_progress")
	}
	if _, perr := time.Parse(time.RFC3339, got.StartedAt); perr != nil {
		t.Fatalf("StartedAt is not a valid RFC 3339 timestamp: %q (%v)", got.StartedAt, perr)
	}

	// Persisted, not just returned in-memory.
	reread, err := s.Get(tk.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if reread.StartedAt != got.StartedAt {
		t.Fatalf("StartedAt did not persist: got %q on reread, want %q", reread.StartedAt, got.StartedAt)
	}
}

// TestUpdate_InProgressNoOp_DoesNotRestampStartedAt verifies that a same-
// status PATCH (task already in_progress) does not overwrite the original
// StartedAt — only a genuine transition INTO in_progress stamps it.
func TestUpdate_InProgressNoOp_DoesNotRestampStartedAt(t *testing.T) {
	s := newStore(t)
	tk := mkTask("t", "ws")
	tk.Status = StatusNext
	if err := s.Create(tk); err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimForRun(tk.ID, time.Now())
	if err != nil {
		t.Fatalf("ClaimForRun: %v", err)
	}
	original := claimed.StartedAt
	if original == "" {
		t.Fatal("ClaimForRun must have stamped StartedAt")
	}

	// A no-op status PATCH (already in_progress) must not re-stamp.
	sameStatus := StatusInProgress
	got, err := s.Update(tk.ID, Patch{Status: &sameStatus})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got.StartedAt != original {
		t.Fatalf("no-op in_progress PATCH re-stamped StartedAt: got %q, want unchanged %q", got.StartedAt, original)
	}
}

// TestUpdate_ExplicitStartedAt_OverridesAutoStamp verifies that an
// explicitly-supplied patch.StartedAt (e.g. from TaskUpdateTool's own
// self-report, or a future wire-supplied value) always wins over the
// store's auto-stamp.
func TestUpdate_ExplicitStartedAt_OverridesAutoStamp(t *testing.T) {
	s := newStore(t)
	tk := mkTask("t", "ws")
	if err := s.Create(tk); err != nil {
		t.Fatal(err)
	}

	explicit := "2020-01-01T00:00:00Z"
	newStatus := StatusInProgress
	got, err := s.Update(tk.ID, Patch{Status: &newStatus, StartedAt: &explicit})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got.StartedAt != explicit {
		t.Fatalf("explicit StartedAt was overridden: got %q, want %q", got.StartedAt, explicit)
	}
}

func TestUpdateNotFound(t *testing.T) {
	s := newStore(t)
	st := StatusDone
	if _, err := s.Update("missing", Patch{Status: &st}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

// --- DAG validator ---

func TestBlockedBySelfEdgeRejected(t *testing.T) {
	s := newStore(t)
	a := mkTask("a", "ws")
	if err := s.Create(a); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Update(a.ID, Patch{BlockedBy: &[]string{a.ID}}); !errors.Is(err, ErrBlockedBySelfEdge) {
		t.Fatalf("want self-edge rejection, got %v", err)
	}
}

func TestBlockedByTwoNodeCycleRejected(t *testing.T) {
	s := newStore(t)
	a, b := mkTask("a", "ws"), mkTask("b", "ws")
	if err := s.Create(a); err != nil {
		t.Fatal(err)
	}
	if err := s.Create(b); err != nil {
		t.Fatal(err)
	}
	// a depends on b — OK.
	if _, err := s.Update(a.ID, Patch{BlockedBy: &[]string{b.ID}}); err != nil {
		t.Fatalf("a→b should be allowed: %v", err)
	}
	// b depends on a — closes a 2-node cycle, must be rejected.
	if _, err := s.Update(b.ID, Patch{BlockedBy: &[]string{a.ID}}); !errors.Is(err, ErrBlockedByCycle) {
		t.Fatalf("want cycle rejection, got %v", err)
	}
}

func TestBlockedByNNodeCycleRejected(t *testing.T) {
	s := newStore(t)
	a, b, c := mkTask("a", "ws"), mkTask("b", "ws"), mkTask("c", "ws")
	for _, tk := range []*Task{a, b, c} {
		if err := s.Create(tk); err != nil {
			t.Fatal(err)
		}
	}
	mustUpdate(t, s, a.ID, Patch{BlockedBy: &[]string{b.ID}})
	mustUpdate(t, s, b.ID, Patch{BlockedBy: &[]string{c.ID}})
	// c→a closes a→b→c→a.
	if _, err := s.Update(c.ID, Patch{BlockedBy: &[]string{a.ID}}); !errors.Is(err, ErrBlockedByCycle) {
		t.Fatalf("want N-node cycle rejection, got %v", err)
	}
}

func TestBlockedByMissingTargetRejected(t *testing.T) {
	s := newStore(t)
	a := mkTask("a", "ws")
	if err := s.Create(a); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Update(a.ID, Patch{BlockedBy: &[]string{"does-not-exist"}}); err == nil {
		t.Fatal("expected rejection of missing blocked_by target")
	}
}

// --- auto-advance ---

func TestAdvanceBlockedDependents(t *testing.T) {
	s := newStore(t)
	dep := mkTask("dep", "ws")
	if err := s.Create(dep); err != nil {
		t.Fatal(err)
	}
	blocked := mkTask("blocked", "ws")
	blocked.Status = StatusBlocked
	blocked.BlockedBy = []string{dep.ID}
	if err := s.Create(blocked); err != nil {
		t.Fatal(err)
	}
	// While dep is not done, advancing is a no-op.
	advanced, err := s.AdvanceBlockedDependents(dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(advanced) != 0 {
		t.Fatalf("should not advance while dep not done, got %v", advanced)
	}
	// Mark dep done, then advance.
	done := StatusDone
	mustUpdate(t, s, dep.ID, Patch{Status: &done})
	advanced, err = s.AdvanceBlockedDependents(dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(advanced) != 1 || advanced[0] != blocked.ID {
		t.Fatalf("expected %s advanced, got %v", blocked.ID, advanced)
	}
	got, _ := s.Get(blocked.ID)
	if got.Status != StatusNext {
		t.Fatalf("blocked task should advance to next, got %q", got.Status)
	}
}

func TestAdvanceWaitsForAllDeps(t *testing.T) {
	s := newStore(t)
	d1, d2 := mkTask("d1", "ws"), mkTask("d2", "ws")
	if err := s.Create(d1); err != nil {
		t.Fatal(err)
	}
	if err := s.Create(d2); err != nil {
		t.Fatal(err)
	}
	blocked := mkTask("blocked", "ws")
	blocked.Status = StatusBlocked
	blocked.BlockedBy = []string{d1.ID, d2.ID}
	if err := s.Create(blocked); err != nil {
		t.Fatal(err)
	}
	done := StatusDone
	mustUpdate(t, s, d1.ID, Patch{Status: &done})
	advanced, _ := s.AdvanceBlockedDependents(d1.ID)
	if len(advanced) != 0 {
		t.Fatalf("should stay blocked until ALL deps done, got %v", advanced)
	}
	mustUpdate(t, s, d2.ID, Patch{Status: &done})
	advanced, _ = s.AdvanceBlockedDependents(d2.ID)
	if len(advanced) != 1 {
		t.Fatalf("should advance once all deps done, got %v", advanced)
	}
}

// --- delete + cascade ---

func TestDeleteCascadesEdges(t *testing.T) {
	s := newStore(t)
	dep := mkTask("dep", "ws")
	if err := s.Create(dep); err != nil {
		t.Fatal(err)
	}
	blocked := mkTask("blocked", "ws")
	blocked.BlockedBy = []string{dep.ID}
	if err := s.Create(blocked); err != nil {
		t.Fatal(err)
	}
	unblocked, err := s.Delete(dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(unblocked) != 1 || unblocked[0] != blocked.ID {
		t.Fatalf("delete should report %s unblocked, got %v", blocked.ID, unblocked)
	}
	got, _ := s.Get(blocked.ID)
	if len(got.BlockedBy) != 0 {
		t.Fatalf("edge not cascade-cleaned: %v", got.BlockedBy)
	}
}

func TestDeleteNotFound(t *testing.T) {
	s := newStore(t)
	if _, err := s.Delete("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestDropOrphanEdges(t *testing.T) {
	s := newStore(t)
	dep := mkTask("dep", "ws")
	if err := s.Create(dep); err != nil {
		t.Fatal(err)
	}
	a := mkTask("a", "ws")
	a.BlockedBy = []string{dep.ID}
	if err := s.Create(a); err != nil {
		t.Fatal(err)
	}
	// No orphans yet.
	removed, err := s.DropOrphanEdges()
	if err != nil {
		t.Fatal(err)
	}
	if removed != 0 {
		t.Fatalf("no orphans expected, removed %d", removed)
	}
	// Remove dep's file out-of-band (simulate manual deletion / crash) so a's
	// edge now dangles, WITHOUT going through Delete (which would cascade-clean).
	if rerr := removeFileRaw(s, dep.ID); rerr != nil {
		t.Fatal(rerr)
	}
	removed, err = s.DropOrphanEdges()
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("expected 1 orphan edge removed, got %d", removed)
	}
	got, _ := s.Get(a.ID)
	if len(got.BlockedBy) != 0 {
		t.Fatalf("orphan edge not dropped: %v", got.BlockedBy)
	}
}

// --- subtasks (parent cycle) ---

func TestSubtaskParentCycleRejected(t *testing.T) {
	s := newStore(t)
	parent := mkTask("parent", "ws")
	if err := s.Create(parent); err != nil {
		t.Fatal(err)
	}
	child := mkTask("child", "ws")
	child.ParentTaskID = parent.ID
	if err := s.Create(child); err != nil {
		t.Fatalf("child with valid parent should be created: %v", err)
	}
	// A task cannot be its own parent.
	self := mkTask("self", "ws")
	self.ID = "self-id"
	self.ParentTaskID = "self-id"
	if err := s.Create(self); !errors.Is(err, ErrParentCycle) {
		t.Fatalf("want parent self-cycle rejection, got %v", err)
	}
}

func TestSubtaskListByParent(t *testing.T) {
	s := newStore(t)
	parent := mkTask("parent", "ws")
	if err := s.Create(parent); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		c := mkTask("child", "ws")
		c.ParentTaskID = parent.ID
		if err := s.Create(c); err != nil {
			t.Fatal(err)
		}
	}
	// A top-level task too.
	if err := s.Create(mkTask("top", "ws")); err != nil {
		t.Fatal(err)
	}
	children, err := s.List(Filter{ParentTaskID: parent.ID, ParentTaskIDSet: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(children) != 3 {
		t.Fatalf("expected 3 children, got %d", len(children))
	}
	// Top-level filter (parent == "").
	tops, err := s.List(Filter{ParentTaskIDSet: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(tops) != 2 { // parent + top
		t.Fatalf("expected 2 top-level tasks, got %d", len(tops))
	}
}

// --- triggers ---

func TestTriggerValidation(t *testing.T) {
	at := int64(1781000000000)
	every := int64(3600000)
	tooFast := int64(500)
	cron := "0 9 * * MON"
	good := []*Trigger{
		{Type: TriggerManual},
		{Type: TriggerOnce, Config: TriggerConfig{AtMs: &at}},
		{Type: TriggerEvery, Config: TriggerConfig{EveryMs: &every}},
		{Type: TriggerRecurring, Config: TriggerConfig{CronExpr: &cron}},
	}
	for _, tr := range good {
		if err := ValidateTrigger(tr); err != nil {
			t.Fatalf("trigger %q should be valid: %v", tr.Type, err)
		}
	}
	bad := []*Trigger{
		{Type: "bogus"},
		{Type: TriggerOnce},  // missing at_ms
		{Type: TriggerEvery}, // missing every_ms
		{Type: TriggerEvery, Config: TriggerConfig{EveryMs: &tooFast}}, // below min
		{Type: TriggerRecurring}, // missing cron
	}
	for _, tr := range bad {
		if err := ValidateTrigger(tr); err == nil {
			t.Fatalf("trigger %q should be invalid", tr.Type)
		}
	}
}

func TestTriggerPersisted(t *testing.T) {
	s := newStore(t)
	cron := "0 9 * * MON"
	tk := mkTask("recurring", "ws")
	tk.Trigger = &Trigger{Type: TriggerRecurring, Config: TriggerConfig{CronExpr: &cron}}
	if err := s.Create(tk); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(tk.ID)
	if got.Trigger == nil || got.Trigger.Type != TriggerRecurring {
		t.Fatalf("trigger not persisted: %+v", got.Trigger)
	}
	if got.Trigger.Config.CronExpr == nil || *got.Trigger.Config.CronExpr != cron {
		t.Fatalf("trigger config not persisted: %+v", got.Trigger.Config)
	}
}

// --- workspace scoping ---

func TestWorkspaceScopedList(t *testing.T) {
	s := newStore(t)
	for _, ws := range []string{"ws-a", "ws-a", "ws-b"} {
		if err := s.Create(mkTask("t", ws)); err != nil {
			t.Fatal(err)
		}
	}
	a, err := s.List(Filter{WorkspaceID: "ws-a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != 2 {
		t.Fatalf("ws-a should have 2 tasks, got %d", len(a))
	}
	b, _ := s.List(Filter{WorkspaceID: "ws-b"})
	if len(b) != 1 {
		t.Fatalf("ws-b should have 1 task, got %d", len(b))
	}
}

func TestSurfaceScopedList(t *testing.T) {
	s := newStore(t)
	u := mkTask("user", "ws")
	if err := s.Create(u); err != nil {
		t.Fatal(err)
	}
	hb := mkTask("hb", "ws")
	hb.Surface = SurfaceHeartbeat
	if err := s.Create(hb); err != nil {
		t.Fatal(err)
	}
	users, _ := s.List(Filter{Surface: SurfaceUser})
	if len(users) != 1 || users[0].Title != "user" {
		t.Fatalf("surface=user filter wrong: %+v", users)
	}
	hbs, _ := s.List(Filter{Surface: SurfaceHeartbeat})
	if len(hbs) != 1 || hbs[0].Title != "hb" {
		t.Fatalf("surface=heartbeat filter wrong: %+v", hbs)
	}
}

// --- claim ---

func TestClaimForRun(t *testing.T) {
	s := newStore(t)
	tk := mkTask("t", "ws")
	tk.Status = StatusNext
	if err := s.Create(tk); err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimForRun(tk.ID, time.Now())
	if err != nil {
		t.Fatalf("ClaimForRun: %v", err)
	}
	if claimed.Status != StatusInProgress || claimed.StartedAt == "" {
		t.Fatalf("claim did not transition to in_progress: %+v", claimed)
	}
	// A second claim must fail (already in_progress).
	if _, err := s.ClaimForRun(tk.ID, time.Now()); !errors.Is(err, ErrAlreadyClaimed) {
		t.Fatalf("second claim should fail, got %v", err)
	}
}

func TestClaimForRunRejectsNonNext(t *testing.T) {
	s := newStore(t)
	tk := mkTask("t", "ws") // defaults to inbox
	if err := s.Create(tk); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimForRun(tk.ID, time.Now()); !errors.Is(err, ErrAlreadyClaimed) {
		t.Fatalf("inbox task should not be claimable, got %v", err)
	}
}

func TestClaimParentFollowUpOnce(t *testing.T) {
	s := newStore(t)
	p := mkTask("parent", "ws")
	if err := s.Create(p); err != nil {
		t.Fatal(err)
	}
	first, err := s.ClaimParentFollowUp(p.ID)
	if err != nil || !first {
		t.Fatalf("first claim should win: %v %v", first, err)
	}
	second, err := s.ClaimParentFollowUp(p.ID)
	if err != nil || second {
		t.Fatalf("second claim should lose: %v %v", second, err)
	}
}

func mustUpdate(t *testing.T, s *Store, id string, p Patch) {
	t.Helper()
	if _, err := s.Update(id, p); err != nil {
		t.Fatalf("Update(%s): %v", id, err)
	}
}

// TestUpdate_BlockedByRecompute_TransitionsNextToBlocked proves the recompute
// fix: a `next` task that gains an unmet blocker via Update(patch.BlockedBy)
// transitions to `blocked` (the store re-derives the side-state from the new
// blocked_by set). Without the recompute call in updateLocked, the task would
// stay `next` (dispatchable) with an unsatisfied dependency.
func TestUpdate_BlockedByRecompute_TransitionsNextToBlocked(t *testing.T) {
	t.Parallel()
	s := newStore(t)

	blocker := mkTask("blocker", "ws")
	mustCreate(t, s, blocker)
	dependent := mkTask("dependent", "ws")
	dependent.Status = StatusNext
	mustCreate(t, s, dependent)

	if dependent.Status != StatusNext {
		t.Fatalf("dependent must start next, got %q", dependent.Status)
	}

	// Add an unmet blocker via Update → recompute must flip next→blocked.
	updated, err := s.Update(dependent.ID, Patch{BlockedBy: &[]string{blocker.ID}})
	if err != nil {
		t.Fatalf("Update BlockedBy: %v", err)
	}
	if updated.Status != StatusBlocked {
		t.Fatalf("expected dependent to transition to blocked, got %q", updated.Status)
	}
	if len(updated.BlockedBy) != 1 || updated.BlockedBy[0] != blocker.ID {
		t.Errorf("expected blocked_by=[%s], got %v", blocker.ID, updated.BlockedBy)
	}

	// Persisted state must match.
	got, err := s.Get(dependent.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != StatusBlocked {
		t.Errorf("persisted status must be blocked, got %q", got.Status)
	}
}

// TestUpdate_BlockedByClear_TransitionsBlockedToNext proves the inverse: a
// `blocked` task whose blocked_by is CLEARED via Update(patch.BlockedBy: [])
// transitions back to `next`. This is the recompute path that lets
// task_update{blocked_by:[]} unblock a task at the store layer.
func TestUpdate_BlockedByClear_TransitionsBlockedToNext(t *testing.T) {
	t.Parallel()
	s := newStore(t)

	blocker := mkTask("blocker", "ws")
	mustCreate(t, s, blocker)
	dependent := mkTask("dependent", "ws")
	dependent.Status = StatusNext
	mustCreate(t, s, dependent)

	// Wire the dep via AddDependency so the task lands in `blocked`.
	if _, _, err := s.AddDependency(dependent.ID, blocker.ID); err != nil {
		t.Fatalf("AddDependency: %v", err)
	}
	got, err := s.Get(dependent.ID)
	if err != nil {
		t.Fatalf("Get after AddDependency: %v", err)
	}
	if got.Status != StatusBlocked {
		t.Fatalf("expected dependent blocked after AddDependency, got %q", got.Status)
	}

	// Clear deps via Update → recompute must flip blocked→next.
	updated, err := s.Update(dependent.ID, Patch{BlockedBy: &[]string{}})
	if err != nil {
		t.Fatalf("Update clear BlockedBy: %v", err)
	}
	if updated.Status != StatusNext {
		t.Fatalf("expected dependent to transition back to next, got %q", updated.Status)
	}
	if len(updated.BlockedBy) != 0 {
		t.Errorf("expected blocked_by cleared, got %v", updated.BlockedBy)
	}
}

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package plan

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/task"
)

func newTestIntentLog(t *testing.T) *IntentLog {
	t.Helper()
	return NewIntentLog(filepath.Join(t.TempDir(), "plan_intents"))
}

func TestIntentLog_AppendMarkCommittedMarkDone(t *testing.T) {
	il := newTestIntentLog(t)
	rec := IntentRecord{
		IntentID: "i-1", PlanID: "p-1",
		Members:  []task.Task{{ID: "t-1", Title: "t-1", WorkspaceID: "ws", PlanID: "p-1"}},
		Revision: RevisionEntry{RevisionID: "r-1", PlanID: "p-1", Verb: RevisionAppend, Generation: 1},
	}
	if err := il.AppendIntent(rec); err != nil {
		t.Fatalf("AppendIntent: %v", err)
	}
	got, err := il.List("p-1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].Status != IntentUncommitted {
		t.Fatalf("got %+v, want one uncommitted record", got)
	}
	if len(got[0].Members) != 1 || got[0].Members[0].ID != "t-1" {
		t.Errorf("self-contained member body not preserved: %+v", got[0].Members)
	}

	if err := il.MarkCommitted("p-1", "i-1"); err != nil {
		t.Fatalf("MarkCommitted: %v", err)
	}
	got, _ = il.List("p-1")
	if got[0].Status != IntentCommitted || got[0].CommittedAt.IsZero() {
		t.Errorf("after commit: %+v", got[0])
	}

	if err := il.MarkDone("p-1", "i-1"); err != nil {
		t.Fatalf("MarkDone: %v", err)
	}
	got, _ = il.List("p-1")
	if got[0].Status != IntentDone || got[0].DoneAt.IsZero() {
		t.Errorf("after done: %+v", got[0])
	}
}

func TestIntentLog_MarkCommittedNotFound(t *testing.T) {
	il := newTestIntentLog(t)
	if err := il.MarkCommitted("p-1", "ghost"); !errors.Is(err, errIntentNotFound) {
		t.Errorf("MarkCommitted on missing intent = %v, want errIntentNotFound", err)
	}
}

func TestIntentLog_Classify_ThreeStates(t *testing.T) {
	// classifyRecords is the pure classifier — test it directly.
	c := classifyRecords([]IntentRecord{
		{IntentID: "a", Status: IntentUncommitted},
		{IntentID: "b", Status: IntentCommitted},
		{IntentID: "d", Status: IntentDone},
		{IntentID: "e", Status: IntentCommitted},
	})
	if len(c.Discard) != 1 || c.Discard[0].IntentID != "a" {
		t.Errorf("Discard = %+v, want [a]", c.Discard)
	}
	if len(c.ReplayForward) != 2 {
		t.Errorf("ReplayForward = %+v, want 2", c.ReplayForward)
	}
	if len(c.AlreadyDone) != 1 || c.AlreadyDone[0].IntentID != "d" {
		t.Errorf("AlreadyDone = %+v, want [d]", c.AlreadyDone)
	}
}

// TestIntentLog_ReplayAtBoot_DiscardUncommitted verifies INV-6 rollback: an
// uncommitted intent is discarded (no apply), and the classification reports
// it for the caller to roll back the partially-written members.
func TestIntentLog_ReplayAtBoot_DiscardUncommitted(t *testing.T) {
	il := newTestIntentLog(t)
	il.AppendIntent(IntentRecord{IntentID: "un", PlanID: "p-1", Status: IntentUncommitted})
	var applied []string
	res, err := il.ReplayAtBoot("p-1", func(rec IntentRecord) error {
		applied = append(applied, rec.IntentID)
		return nil
	})
	if err != nil {
		t.Fatalf("ReplayAtBoot: %v", err)
	}
	if len(applied) != 0 {
		t.Errorf("uncommitted intent was applied: %v (must be discarded)", applied)
	}
	if res.Discarded != 1 {
		t.Errorf("Discarded = %d, want 1", res.Discarded)
	}
	if res.Replayed != 0 {
		t.Errorf("Replayed = %d, want 0", res.Replayed)
	}
}

// TestIntentLog_ReplayAtBoot_ReplayCommittedNotDone verifies INV-6 replay:
// a committed-but-not-done intent is applied idempotently and then marked done.
func TestIntentLog_ReplayAtBoot_ReplayCommittedNotDone(t *testing.T) {
	il := newTestIntentLog(t)
	il.AppendIntent(IntentRecord{IntentID: "c", PlanID: "p-1", Status: IntentUncommitted})
	if err := il.MarkCommitted("p-1", "c"); err != nil {
		t.Fatal(err)
	}
	applyCount := 0
	res, err := il.ReplayAtBoot("p-1", func(rec IntentRecord) error {
		applyCount++
		return nil
	})
	if err != nil {
		t.Fatalf("ReplayAtBoot: %v", err)
	}
	if applyCount != 1 || res.Replayed != 1 {
		t.Fatalf("apply=%d replayed=%d, want 1/1", applyCount, res.Replayed)
	}
	// After replay the intent is marked done — a second replay is a no-op.
	applyCount = 0
	res2, err := il.ReplayAtBoot("p-1", func(rec IntentRecord) error {
		applyCount++
		return nil
	})
	if err != nil {
		t.Fatalf("second ReplayAtBoot: %v", err)
	}
	if applyCount != 0 || res2.Replayed != 0 {
		t.Errorf("second replay applied=%d (must be no-op, done-marker guards it)", applyCount)
	}
	if len(res2.Classification.AlreadyDone) != 1 {
		t.Errorf("second replay: AlreadyDone = %d, want 1", len(res2.Classification.AlreadyDone))
	}
}

// TestIntentLog_ReplayAllPlans_multiplePlans verifies per-plan isolation.
func TestIntentLog_ReplayAllPlans_multiplePlans(t *testing.T) {
	il := newTestIntentLog(t)
	for _, pid := range []string{"p-a", "p-b"} {
		il.AppendIntent(IntentRecord{IntentID: "i-" + pid, PlanID: pid})
		il.MarkCommitted(pid, "i-"+pid)
	}
	var seen []string
	results, err := il.ReplayAllPlans(func(rec IntentRecord) error {
		seen = append(seen, rec.PlanID)
		return nil
	})
	if err != nil {
		t.Fatalf("ReplayAllPlans: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2", len(results))
	}
	if len(seen) != 2 {
		t.Errorf("applied = %v, want 2 plans", seen)
	}
}

// TestIntentLog_SelfContainedRecord verifies the intent record carries full
// member bodies + edges + revision + patch (FR-148 self-contained requirement).
func TestIntentLog_SelfContainedRecord(t *testing.T) {
	il := newTestIntentLog(t)
	now := time.Now().UTC()
	rec := IntentRecord{
		IntentID: "i-sc", PlanID: "p-sc",
		Members: []task.Task{
			{ID: "m-1", Title: "member one", WorkspaceID: "ws", PlanID: "p-sc"},
			{ID: "m-2", Title: "member two", WorkspaceID: "ws", PlanID: "p-sc"},
		},
		Edges:    []IntentEdge{{FromTaskID: "m-1", ToTaskID: "m-2"}},
		Revision: RevisionEntry{RevisionID: "r-1", PlanID: "p-sc", Verb: RevisionSupersede, SupersededMemberID: "old-1", CreatedAt: now},
		Patch:    IntentRecordPatch{ClearLastUnmetTerminalSignature: true, PlanPhase: PhaseDispatching},
	}
	il.AppendIntent(rec)
	got, _ := il.List("p-sc")
	if len(got[0].Members) != 2 {
		t.Errorf("members = %d, want 2 (full bodies)", len(got[0].Members))
	}
	if len(got[0].Edges) != 1 || got[0].Edges[0].ToTaskID != "m-2" {
		t.Errorf("edges not preserved: %+v", got[0].Edges)
	}
	if got[0].Revision.SupersededMemberID != "old-1" {
		t.Errorf("revision not preserved: %+v", got[0].Revision)
	}
	if !got[0].Patch.ClearLastUnmetTerminalSignature || got[0].Patch.PlanPhase != PhaseDispatching {
		t.Errorf("patch not preserved: %+v", got[0].Patch)
	}
}

// TestIntentLog_CommitCorrection_FullFourStep verifies the CommitCorrection
// wrapper sequences AppendIntent → MarkCommitted → Apply → MarkDone and the
// intent ends up status=done with a non-zero DoneAt (FR-148/INV-6).
func TestIntentLog_CommitCorrection_FullFourStep(t *testing.T) {
	il := newTestIntentLog(t)
	rec := IntentRecord{
		IntentID: "i-cc", PlanID: "p-cc",
		Members:  []task.Task{{ID: "m-cc", Title: "member", WorkspaceID: "ws", PlanID: "p-cc"}},
		Revision: RevisionEntry{RevisionID: "r-cc", PlanID: "p-cc", Verb: RevisionAppend, Generation: 0},
	}
	var applied bool
	err := il.CommitCorrection(rec, func(r IntentRecord) error {
		applied = true
		if len(r.Members) != 1 || r.Members[0].ID != "m-cc" {
			t.Errorf("apply received wrong members: %+v", r.Members)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("CommitCorrection: %v", err)
	}
	if !applied {
		t.Error("apply function was not called")
	}
	got, _ := il.List("p-cc")
	if len(got) != 1 || got[0].Status != IntentDone {
		t.Errorf("after CommitCorrection: %+v, want status=done", got)
	}
	if got[0].DoneAt.IsZero() {
		t.Error("DoneAt not set after CommitCorrection")
	}
}

// TestIntentLog_CommitCorrection_NilApply verifies CommitCorrection with a
// nil apply function (supersede with no new members — commit-only intent).
func TestIntentLog_CommitCorrection_NilApply(t *testing.T) {
	il := newTestIntentLog(t)
	rec := IntentRecord{
		IntentID: "i-na", PlanID: "p-na",
		Revision: RevisionEntry{RevisionID: "r-na", PlanID: "p-na", Verb: RevisionSupersede, SupersededMemberID: "old-1"},
	}
	err := il.CommitCorrection(rec, nil)
	if err != nil {
		t.Fatalf("CommitCorrection with nil apply: %v", err)
	}
	got, _ := il.List("p-na")
	if got[0].Status != IntentDone {
		t.Errorf("status = %s, want done", got[0].Status)
	}
}

// TestIntentLog_CommitCorrection_ApplyErrorKeepsCommitted verifies that if
// apply fails AFTER MarkCommitted, the intent stays committed (not done) —
// boot replay will complete it. The error is returned to the caller.
func TestIntentLog_CommitCorrection_ApplyErrorKeepsCommitted(t *testing.T) {
	il := newTestIntentLog(t)
	rec := IntentRecord{
		IntentID: "i-ae", PlanID: "p-ae",
		Revision: RevisionEntry{RevisionID: "r-ae", PlanID: "p-ae", Verb: RevisionAppend},
	}
	applyErr := errors.New("simulated apply failure")
	err := il.CommitCorrection(rec, func(r IntentRecord) error {
		return applyErr
	})
	if !errors.Is(err, applyErr) {
		t.Errorf("expected apply error, got %v", err)
	}
	// The intent must be committed (not done) — boot will replay-forward.
	got, _ := il.List("p-ae")
	if len(got) != 1 {
		t.Fatalf("expected 1 record, got %d", len(got))
	}
	if got[0].Status != IntentCommitted {
		t.Errorf("status = %s, want committed (apply failed after commit; boot will replay)", got[0].Status)
	}
}

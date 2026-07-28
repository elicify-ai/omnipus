// Omnipus — stop_plan Agent Tool Tests
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// --- harness ---------------------------------------------------------------

// stopSpy stands in for PlanEngine.StopPlan. It performs the SAME terminal
// write the real engine does — plan -> failed(stopped_by_user) — so a test can
// assert that the plan actually stopped, not merely that a function was
// entered. A test that only counted calls would pass against a tool that
// stopped the wrong plan.
type stopSpy struct {
	store    *plan.Store
	planIDs  []string
	userIDs  []string
	channels []string
	err      error
}

func (s *stopSpy) fn(_ context.Context, planID, userID, channel string) (*plan.Plan, error) {
	s.planIDs = append(s.planIDs, planID)
	s.userIDs = append(s.userIDs, userID)
	s.channels = append(s.channels, channel)
	if s.err != nil {
		return nil, s.err
	}
	failed := plan.StateFailed
	reason := plan.FailedReasonStoppedByUser
	return s.store.Update(planID, plan.Patch{State: &failed, FailedReason: &reason})
}

// newStoppablePlan builds a RUNNING plan owned by "jim" plus one in-flight
// member, and returns the tool wired over a stopSpy.
func newStoppablePlan(t *testing.T) (planFixture, *stopSpy, *PlanStopTool) {
	t.Helper()
	f := newPlanInPhase(t, plan.PhaseDispatching)
	f.addMember(t, "m1", task.StatusInProgress, nil)
	spy := &stopSpy{store: f.planStore}
	tool := NewPlanStopTool(f.planStore)
	tool.SetStopPlan(spy.fn)
	return f, spy, tool
}

// --- FR-043: owner authority ----------------------------------------------

// TestStopPlan_OwnerStopsTheRunningPlan proves the OUTCOME: the plan the owner
// named is actually stopped — terminal on disk, attributed to the owner, and
// reported as stopped.
func TestStopPlan_OwnerStopsTheRunningPlan(t *testing.T) {
	t.Parallel()
	f, spy, tool := newStoppablePlan(t)

	res := tool.Execute(WithAgentID(context.Background(), "jim"), map[string]any{"plan_id": f.planID})
	if res.IsError {
		t.Fatalf("the plan's owner was denied: %s", res.ForLLM)
	}

	// Outcome 1: the plan is terminal on disk.
	got, err := f.planStore.Get(f.planID)
	if err != nil {
		t.Fatalf("get plan: %v", err)
	}
	if got.State != plan.StateFailed {
		t.Fatalf("plan state = %q, want failed — the plan did not actually stop", got.State)
	}
	if got.FailedReason != plan.FailedReasonStoppedByUser {
		t.Errorf("failed_reason = %q, want stopped_by_user", got.FailedReason)
	}

	// Outcome 2: the engine was asked to stop THIS plan, attributed to the
	// calling agent — the cancel fan-out and handover both read that actor.
	if len(spy.planIDs) != 1 || spy.planIDs[0] != f.planID {
		t.Fatalf("engine stop calls = %v, want exactly [%s]", spy.planIDs, f.planID)
	}
	if spy.userIDs[0] != "jim" {
		t.Errorf("stop attributed to %q, want jim", spy.userIDs[0])
	}

	// Outcome 3: the caller is told the plan is terminal.
	var out struct {
		PlanID       string `json:"plan_id"`
		State        string `json:"state"`
		FailedReason string `json:"failed_reason"`
		Note         string `json:"note"`
	}
	if err := json.Unmarshal([]byte(res.ForLLM), &out); err != nil {
		t.Fatalf("parse result %q: %v", res.ForLLM, err)
	}
	if out.PlanID != f.planID || out.State != string(plan.StateFailed) {
		t.Errorf("unexpected result: %+v", out)
	}
	if out.FailedReason != string(plan.FailedReasonStoppedByUser) {
		t.Errorf("failed_reason = %q", out.FailedReason)
	}
	if !strings.Contains(out.Note, "cannot be resumed") {
		t.Errorf("note does not say the stop is terminal: %q", out.Note)
	}
}

// TestStopPlan_DeniesEveryNonOwner proves the OUTCOME: a caller that is not
// the plan's owner cannot stop it — the engine is never reached and the plan
// keeps running — and the denial names neither the owner nor the plan.
//
// PlanSupervisor is in the table on purpose: the adjudicator corrects, the
// owner contains. Correction authority must not imply stop authority; the two
// gates are deliberate inverses.
func TestStopPlan_DeniesEveryNonOwner(t *testing.T) {
	t.Parallel()
	callers := []struct {
		name    string
		agentID string
	}{
		{"another core agent", "ava"},
		{"the plan supervisor", PlanSupervisorAgentID},
		{"a user-created agent", "agent-7f3c"},
		{"a caller with no identity", ""},
		{"an agent whose id is a prefix of the owner's", "ji"},
	}
	for _, c := range callers {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			f, spy, tool := newStoppablePlan(t)

			res := tool.Execute(WithAgentID(context.Background(), c.agentID), map[string]any{"plan_id": f.planID})

			// Outcome 1: denied.
			if !res.IsError {
				t.Fatalf("caller %q stopped a plan it does not own: %s", c.agentID, res.ForLLM)
			}
			// Outcome 2: the engine was never reached, so nothing was cancelled.
			if len(spy.planIDs) != 0 {
				t.Fatalf("caller %q reached the stop engine %d time(s)", c.agentID, len(spy.planIDs))
			}
			// Outcome 3: the plan is still running.
			got, err := f.planStore.Get(f.planID)
			if err != nil {
				t.Fatalf("get plan: %v", err)
			}
			if got.State != plan.StateRunning {
				t.Fatalf("plan state = %q after a denied stop, want running", got.State)
			}
			// Outcome 4: the denial leaks nothing.
			for _, secret := range []string{"jim", f.planID} {
				if strings.Contains(res.ForLLM, secret) {
					t.Errorf("denial leaked %q: %s", secret, res.ForLLM)
				}
			}
		})
	}
}

// TestStopPlan_DenialIsNotAnExistenceOracle proves "this plan is not yours"
// and "there is no such plan" are byte-identical responses. Otherwise any
// agent holding stop_plan can enumerate plan ids.
func TestStopPlan_DenialIsNotAnExistenceOracle(t *testing.T) {
	t.Parallel()
	f, spy, tool := newStoppablePlan(t)
	ctx := WithAgentID(context.Background(), "ava")

	notMine := tool.Execute(ctx, map[string]any{"plan_id": f.planID})
	missing := tool.Execute(ctx, map[string]any{"plan_id": "plan-that-does-not-exist"})
	otherMissing := tool.Execute(ctx, map[string]any{"plan_id": "another-nonexistent-plan"})

	if !notMine.IsError || !missing.IsError || !otherMissing.IsError {
		t.Fatal("expected all three calls to be denied")
	}
	if notMine.ForLLM != missing.ForLLM || missing.ForLLM != otherMissing.ForLLM {
		t.Fatalf("denials differ — the tool is an existence oracle:\n not-mine:   %q\n missing:    %q\n missing(2): %q",
			notMine.ForLLM, missing.ForLLM, otherMissing.ForLLM)
	}
	if len(spy.planIDs) != 0 {
		t.Fatalf("engine reached %d time(s) by a denied caller", len(spy.planIDs))
	}
}

// TestStopPlan_ApprovedPlanIsStoppable proves a plan queued behind the
// concurrency cap can still be stopped by its owner — an owner must not have
// to wait for their plan to be admitted before they can cancel it.
func TestStopPlan_ApprovedPlanIsStoppable(t *testing.T) {
	t.Parallel()
	planStore, taskStore := newPlanAndTaskStores(t)
	p := &plan.Plan{WorkspaceID: "ws-1", Title: "Queued", OwnerAgentID: "jim", CreatedBy: "jim"}
	if err := planStore.Create(p); err != nil {
		t.Fatalf("create plan: %v", err)
	}
	approved := plan.StateApproved
	if _, err := planStore.Update(p.ID, plan.Patch{State: &approved}); err != nil {
		t.Fatalf("approve: %v", err)
	}
	_ = taskStore
	spy := &stopSpy{store: planStore}
	tool := NewPlanStopTool(planStore)
	tool.SetStopPlan(spy.fn)

	res := tool.Execute(WithAgentID(context.Background(), "jim"), map[string]any{"plan_id": p.ID})
	if res.IsError {
		t.Fatalf("approved plan could not be stopped by its owner: %s", res.ForLLM)
	}
	got, err := planStore.Get(p.ID)
	if err != nil {
		t.Fatalf("get plan: %v", err)
	}
	if got.State != plan.StateFailed {
		t.Fatalf("plan state = %q, want failed", got.State)
	}
}

// TestStopPlan_RejectsTerminalPlan proves a plan that already ended is
// rejected with a real state error and no second terminal write — the owner
// gate has already passed, so there is nothing left to hide.
func TestStopPlan_RejectsTerminalPlan(t *testing.T) {
	t.Parallel()
	for _, state := range []plan.State{plan.StateDone, plan.StateFailed} {
		t.Run(string(state), func(t *testing.T) {
			t.Parallel()
			f, spy, tool := newStoppablePlan(t)
			target := state
			if _, err := f.planStore.Update(f.planID, plan.Patch{State: &target}); err != nil {
				t.Fatalf("move plan to %s: %v", state, err)
			}
			res := tool.Execute(WithAgentID(context.Background(), "jim"), map[string]any{"plan_id": f.planID})
			if !res.IsError {
				t.Fatalf("stop of a %s plan accepted: %s", state, res.ForLLM)
			}
			if len(spy.planIDs) != 0 {
				t.Fatalf("stop of a %s plan reached the engine", state)
			}
		})
	}

	// A draft plan has never been admitted, so there is nothing to cancel —
	// and running -> draft is not a legal transition, so this case can only be
	// reached by a plan that was never approved.
	t.Run("draft", func(t *testing.T) {
		t.Parallel()
		planStore, _ := newPlanAndTaskStores(t)
		p := &plan.Plan{WorkspaceID: "ws-1", Title: "Never approved", OwnerAgentID: "jim", CreatedBy: "jim"}
		if err := planStore.Create(p); err != nil {
			t.Fatalf("create plan: %v", err)
		}
		spy := &stopSpy{store: planStore}
		tool := NewPlanStopTool(planStore)
		tool.SetStopPlan(spy.fn)

		res := tool.Execute(WithAgentID(context.Background(), "jim"), map[string]any{"plan_id": p.ID})
		if !res.IsError {
			t.Fatalf("stop of a draft plan accepted: %s", res.ForLLM)
		}
		if len(spy.planIDs) != 0 {
			t.Fatal("stop of a draft plan reached the engine")
		}
	})
}

// TestStopPlan_UnwiredEngine_FailsClosed proves an unwired stop hook denies
// rather than reporting a stop that never happened. Reporting a phantom stop
// is the worst failure mode a kill switch has — the operator stops watching.
func TestStopPlan_UnwiredEngine_FailsClosed(t *testing.T) {
	t.Parallel()
	f := newPlanInPhase(t, plan.PhaseDispatching)
	tool := NewPlanStopTool(f.planStore) // no SetStopPlan

	res := tool.Execute(WithAgentID(context.Background(), "jim"), map[string]any{"plan_id": f.planID})
	if !res.IsError {
		t.Fatalf("unwired stop hook reported success: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "not wired") {
		t.Errorf("unexpected message: %s", res.ForLLM)
	}
	got, err := f.planStore.Get(f.planID)
	if err != nil {
		t.Fatalf("get plan: %v", err)
	}
	if got.State != plan.StateRunning {
		t.Fatalf("plan state = %q, want running (nothing should have changed)", got.State)
	}
}

// TestStopPlan_EngineFailureIsSurfaced proves an engine failure is reported as
// an error. A stop that failed must never read as a stop that succeeded.
func TestStopPlan_EngineFailureIsSurfaced(t *testing.T) {
	t.Parallel()
	f, spy, tool := newStoppablePlan(t)
	spy.err = fmt.Errorf("plan_engine: StopPlan: list member tasks: i/o error")

	res := tool.Execute(WithAgentID(context.Background(), "jim"), map[string]any{"plan_id": f.planID})
	if !res.IsError {
		t.Fatalf("engine failure reported as a successful stop: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "i/o error") {
		t.Errorf("engine error not surfaced: %s", res.ForLLM)
	}
	got, err := f.planStore.Get(f.planID)
	if err != nil {
		t.Fatalf("get plan: %v", err)
	}
	if got.State != plan.StateRunning {
		t.Errorf("plan state = %q, want running", got.State)
	}
}

// TestStopPlan_ChannelAttribution proves the stop always carries a non-empty
// channel. It flows into the per-session cancel attribution and into the
// handover text, so an empty string would produce an unattributed cancellation.
func TestStopPlan_ChannelAttribution(t *testing.T) {
	t.Parallel()

	t.Run("the calling turn's channel is used when bound", func(t *testing.T) {
		t.Parallel()
		f, spy, tool := newStoppablePlan(t)
		ctx := WithToolContext(WithAgentID(context.Background(), "jim"), "telegram", "chat-9")
		if res := tool.Execute(ctx, map[string]any{"plan_id": f.planID}); res.IsError {
			t.Fatalf("stop rejected: %s", res.ForLLM)
		}
		if spy.channels[0] != "telegram" {
			t.Errorf("channel = %q, want telegram", spy.channels[0])
		}
	})

	t.Run("an unbound channel falls back to a literal, never empty", func(t *testing.T) {
		t.Parallel()
		f, spy, tool := newStoppablePlan(t)
		if res := tool.Execute(WithAgentID(context.Background(), "jim"), map[string]any{"plan_id": f.planID}); res.IsError {
			t.Fatalf("stop rejected: %s", res.ForLLM)
		}
		if spy.channels[0] == "" {
			t.Fatal("channel is empty — the cancel attribution and handover would both be unattributed")
		}
		if spy.channels[0] != stopPlanToolChannel {
			t.Errorf("channel = %q, want %q", spy.channels[0], stopPlanToolChannel)
		}
	})
}

// TestStopPlan_RequiresPlanID proves a missing plan_id is rejected before any
// store access.
func TestStopPlan_RequiresPlanID(t *testing.T) {
	t.Parallel()
	f, spy, tool := newStoppablePlan(t)
	res := tool.Execute(WithAgentID(context.Background(), "jim"), map[string]any{})
	if !res.IsError {
		t.Fatalf("missing plan_id accepted: %s", res.ForLLM)
	}
	if len(spy.planIDs) != 0 {
		t.Fatal("reached the engine with no plan id")
	}
	_ = f
}

// TestStopPlan_NilStore_FailsClosed proves metadata-only construction cannot
// be executed against.
func TestStopPlan_NilStore_FailsClosed(t *testing.T) {
	t.Parallel()
	tool := NewPlanStopTool(nil)
	tool.SetStopPlan((&stopSpy{}).fn)
	res := tool.Execute(WithAgentID(context.Background(), "jim"), map[string]any{"plan_id": "p1"})
	if !res.IsError {
		t.Fatalf("nil store reported success: %s", res.ForLLM)
	}
}

// TestStopPlan_DescriptionWarnsAboutInFlightAdjudication proves the tool's own
// description carries the containment warning. The sibling roster surfaces an
// awaiting-supervision plan as `blocked` on the OWNER's roster, and the Owner
// is the one principal forbidden from correcting — so an Owner acting on that
// signal has exactly one tool available and it is this one. The description is
// what stops it stopping healthy work.
func TestStopPlan_DescriptionWarnsAboutInFlightAdjudication(t *testing.T) {
	t.Parallel()
	desc := strings.ToLower(NewPlanStopTool(nil).Description())
	for _, want := range []string{"blocked", "awaiting supervision", "adjudication"} {
		if !strings.Contains(desc, want) {
			t.Errorf("description does not mention %q: %s", want, desc)
		}
	}
}

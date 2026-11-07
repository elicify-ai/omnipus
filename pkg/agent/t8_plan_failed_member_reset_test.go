package agent

// Round 8, CK-8. Oracle: r17 TK-30, the wait invariant across EVERY
// automatic promotion path: a task whose time trigger has not released it
// must not be published as `next` by an automatic path; only the scheduler's
// fire at the occurrence (SpawnResetForOccurrence) releases it. A task
// waiting for its occurrence is scheduler-owned: status `inbox`, trigger kept.
//
// The path under test is the PlanSupervisor correction flow, which resets a
// failed plan member through Store.RestartReset:
//   - AppendCorrection (append / supersede) -> autoResetLiveRoundFailedMembers
//   - AppendCorrection (targeted_retry)      -> the apply func's reset
//   - boot replay of a targeted_retry intent -> applyIntentRecord
//
// Explicit person actions (Play of the plan, standalone restart) are NOT
// automatic: they stay allowed to publish `next` and are pinned as controls.
// A failed member whose trigger is already due, manual, or absent has no
// occurrence to wait for and follows the shared path (`next`).

import (
	"context"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type t8ResetTrigger int

const (
	t8TrigNone t8ResetTrigger = iota
	t8TrigManual
	t8TrigDue
	t8TrigFuture
)

func (k t8ResetTrigger) String() string {
	return [...]string{"no trigger", "manual trigger", "already-due trigger", "future once trigger"}[k]
}

// t8FailedMember seeds a failed plan member with an assignee, no unmet
// dependencies and (for the trigger kinds) a persisted trigger. The trigger
// is added in a second write: a terminal task keeps its status when a
// trigger is added. It returns the exact trigger so the caller can compare.
func t8FailedMember(t *testing.T, h *planEngineHarness, id, planID string, kind t8ResetTrigger) (*task.Trigger, int64) {
	t.Helper()
	m := failedMember(id)
	m.PlanID = planID
	m.WorkspaceID = "ws"
	m.AgentID = "worker"
	m.CreatedByAgentID = "owner"
	m.Action = task.ActionLLM
	m.Prompt = "do " + id
	mustCreateTask(t, h.tasks, m)
	var trig *task.Trigger
	var atMs int64
	switch kind {
	case t8TrigManual:
		trig = &task.Trigger{Type: task.TriggerManual}
	case t8TrigDue:
		atMs = time.Now().Add(-time.Hour).UnixMilli()
		trig = &task.Trigger{Type: task.TriggerOnce, Config: task.TriggerConfig{AtMs: &atMs}}
	case t8TrigFuture:
		atMs = time.Now().Add(time.Hour).UnixMilli()
		trig = &task.Trigger{Type: task.TriggerOnce, Config: task.TriggerConfig{AtMs: &atMs}}
	}
	if trig != nil {
		_, err := h.tasks.Update(id, task.Patch{Trigger: &trig})
		require.NoError(t, err, "instrument: persist the trigger on the failed member")
	}
	got, err := h.tasks.Get(id)
	require.NoError(t, err)
	require.Equal(t, task.StatusFailed, got.Status, "instrument: a terminal task keeps its status when a trigger is added")
	if kind == t8TrigNone {
		require.Nil(t, got.Trigger, "instrument: no trigger")
	} else {
		require.NotNil(t, got.Trigger, "instrument: the trigger persisted")
		require.Equal(t, trig.Type, got.Trigger.Type)
		if atMs != 0 {
			require.NotNil(t, got.Trigger.Config.AtMs)
			require.Equal(t, atMs, *got.Trigger.Config.AtMs, "instrument: the exact occurrence is stored")
		}
	}
	return trig, atMs
}

// t8AssertTriggerKept proves the member's trigger survived the reset exactly.
func t8AssertTriggerKept(t *testing.T, got *task.Task, want *task.Trigger, atMs int64) {
	t.Helper()
	if want == nil {
		assert.Nil(t, got.Trigger, "no trigger invented by the reset")
		return
	}
	require.NotNil(t, got.Trigger, "the reset keeps the trigger")
	assert.Equal(t, want.Type, got.Trigger.Type, "the reset keeps the trigger type")
	if atMs != 0 {
		require.NotNil(t, got.Trigger.Config.AtMs)
		assert.Equal(t, atMs, *got.Trigger.Config.AtMs, "the reset keeps the exact occurrence")
	}
}

// t8AssertStatusAfterReset is the spec result: only a future occurrence
// waits (exactly inbox). Every other member is dispatchable: the live
// correction flow runs a dispatch pass right after the reset, so a control
// member is `next` or already claimed (`in_progress`); the boot replay has
// no dispatch pass and leaves exactly `next`.
func t8AssertStatusAfterReset(t *testing.T, kind t8ResetTrigger, got task.Status, path string) {
	t.Helper()
	if kind == t8TrigFuture {
		assert.Equal(t, task.StatusInbox, got,
			"CK-8 (%s): %s must leave the future-trigger member waiting in inbox", kind, path)
		return
	}
	assert.Contains(t, []task.Status{task.StatusNext, task.StatusInProgress}, got,
		"control (%s): %s resets the member to a dispatchable state", kind, path)
}

func t8AppendTail(id string) []task.Task {
	return []task.Task{{
		ID: id, Title: id, WorkspaceID: "ws", Status: task.StatusNext,
		Criteria: []task.AcceptanceCriterion{planProseCriterion(id + " work is done")},
	}}
}

// CK-8 (RED for the future trigger, controls for the others): the append
// correction auto-resets every failed member; a member waiting for a future
// occurrence ends in the scheduler-owned waiting state, not `next`.
func TestT8PlanCorrection_AppendAutoReset_FailedMemberTrigger_TK30(t *testing.T) {
	for _, kind := range []t8ResetTrigger{t8TrigFuture, t8TrigNone, t8TrigManual, t8TrigDue} {
		t.Run(kind.String(), func(t *testing.T) {
			h := newCorrectionHarness(t)
			mustSeedAwaitingCorrection(t, h, "p-ck8-append", doneMember("m-done"))
			trig, atMs := t8FailedMember(t, h, "m-ck8", "p-ck8-append", kind)

			_, err := h.pe.AppendCorrection(context.Background(), "p-ck8-append", supervisorCaller(), CorrectionRequest{
				Verb:                CorrectionAppend,
				FalsifiedAssumption: "assumed the failed member needed no extra help",
				TailMembers:         t8AppendTail("m-ck8-tail"),
				Reason:              "add a tail member",
			})
			require.NoError(t, err, "AppendCorrection")

			got, gerr := h.tasks.Get("m-ck8")
			require.NoError(t, gerr)
			t8AssertStatusAfterReset(t, kind, got.Status, "automatic correction reset")
			t8AssertTriggerKept(t, got, trig, atMs)
			if kind == t8TrigFuture {
				assert.NotEqual(t, task.StatusNext, got.Status, "a waiting member is never dispatchable before its occurrence")
				assert.NotEqual(t, task.StatusInProgress, got.Status, "and never dispatched early")
			}
		})
	}
}

// CK-8: the supervisor's targeted_retry resets one named failed member; the
// future-trigger member waits, the others follow the shared path.
func TestT8PlanCorrection_TargetedRetry_FailedMemberTrigger_TK30(t *testing.T) {
	for _, kind := range []t8ResetTrigger{t8TrigFuture, t8TrigNone, t8TrigManual, t8TrigDue} {
		t.Run(kind.String(), func(t *testing.T) {
			h := newCorrectionHarness(t)
			mustSeedAwaitingCorrection(t, h, "p-ck8-retry", doneMember("m-done"))
			trig, atMs := t8FailedMember(t, h, "m-ck8", "p-ck8-retry", kind)

			_, err := h.pe.AppendCorrection(context.Background(), "p-ck8-retry", supervisorCaller(), CorrectionRequest{
				Verb:                CorrectionTargetedRetry,
				RetriedMemberID:     "m-ck8",
				FalsifiedAssumption: "assumed the failure was permanent",
				Reason:              "retry the failed member",
			})
			require.NoError(t, err, "AppendCorrection targeted_retry")

			got, gerr := h.tasks.Get("m-ck8")
			require.NoError(t, gerr)
			t8AssertStatusAfterReset(t, kind, got.Status, "targeted retry")
			t8AssertTriggerKept(t, got, trig, atMs)
		})
	}
}

// CK-8: boot replay of a committed targeted_retry intent is automatic too
// (nobody asked at boot), so it follows the same wait invariant.
func TestT8PlanCorrection_BootReplayTargetedRetry_FailedMemberTrigger_TK30(t *testing.T) {
	for _, kind := range []t8ResetTrigger{t8TrigFuture, t8TrigNone, t8TrigManual, t8TrigDue} {
		t.Run(kind.String(), func(t *testing.T) {
			h := newCorrectionHarness(t)
			mustSeedAwaitingCorrection(t, h, "p-ck8-replay", doneMember("m-done"))
			trig, atMs := t8FailedMember(t, h, "m-ck8", "p-ck8-replay", kind)

			require.NoError(t, h.pe.applyIntentRecord(plan.IntentRecord{
				IntentID: "rev-ck8", PlanID: "p-ck8-replay",
				Revision: plan.RevisionEntry{
					RevisionID: "rev-ck8", PlanID: "p-ck8-replay",
					Verb: plan.RevisionTargetedRetry, RetriedMemberID: "m-ck8",
					CreatedAt: time.Now().UTC(),
				},
				Patch:     plan.IntentRecordPatch{ClearLastUnmetTerminalSignature: true, PlanPhase: plan.PhaseDispatching},
				CreatedAt: time.Now().UTC(),
			}), "boot replay apply")

			got, gerr := h.tasks.Get("m-ck8")
			require.NoError(t, gerr)
			t8AssertStatusAfterReset(t, kind, got.Status, "boot replay reset")
			if kind != t8TrigFuture {
				assert.Equal(t, task.StatusNext, got.Status, "boot replay has no dispatch pass: exactly next")
			}
			t8AssertTriggerKept(t, got, trig, atMs)
		})
	}
}

// Control: a person's Play of the plan is an explicit, authorized restart. It
// resets every non-done member to `next`, a future-trigger member included
// (the person asked for it now); the trigger is kept. Must stay green.
func TestT8PlanPlay_PersonRestart_FutureTriggerMember_StaysAllowed_TK30(t *testing.T) {
	h := newCorrectionHarness(t)
	mustCreatePlan(t, h.plans, &plan.Plan{
		ID: "p-ck8-play", Title: "p-ck8-play", WorkspaceID: "ws", OwnerAgentID: "owner",
		State: plan.StateFailed, FailedReason: plan.FailedReasonStoppedByUser,
		DoD: []task.AcceptanceCriterion{planProseCriterion("DoD is met")},
	})
	trig, atMs := t8FailedMember(t, h, "m-ck8", "p-ck8-play", t8TrigFuture)

	_, err := h.pe.PlayPlan(context.Background(), "p-ck8-play")
	require.NoError(t, err, "PlayPlan")

	got, gerr := h.tasks.Get("m-ck8")
	require.NoError(t, gerr)
	assert.Equal(t, task.StatusNext, got.Status, "an explicit person's restart stays allowed to run the member now")
	t8AssertTriggerKept(t, got, trig, atMs)
}

// Control, due-time release: a member waiting in inbox for its occurrence is
// released by the scheduler's existing path (SpawnResetForOccurrence): next,
// with the delivery marker recording exactly that occurrence.
func TestT8PlanWaitingMember_DueRelease_ThroughSchedulerPath_TK30(t *testing.T) {
	h := newCorrectionHarness(t)
	mustSeedAwaitingCorrection(t, h, "p-ck8-due", doneMember("m-done"))
	atMs := time.Now().Add(time.Hour).UnixMilli()
	w := &task.Task{
		ID: "m-ck8-wait", Title: "m-ck8-wait", WorkspaceID: "ws", PlanID: "p-ck8-due",
		AgentID: "worker", CreatedByAgentID: "owner", Action: task.ActionLLM, Prompt: "wait",
		Status:  task.StatusInbox,
		Trigger: &task.Trigger{Type: task.TriggerOnce, Config: task.TriggerConfig{AtMs: &atMs}},
	}
	mustCreateTask(t, h.tasks, w)
	before, err := h.tasks.Get("m-ck8-wait")
	require.NoError(t, err)
	require.Equal(t, task.StatusInbox, before.Status, "instrument: the member waits in inbox")

	released, err := h.tasks.SpawnResetForOccurrence("m-ck8-wait", &atMs)
	require.NoError(t, err)
	assert.Equal(t, task.StatusNext, released.Status, "the occurrence releases the member through the existing path")
	require.NotNil(t, released.LastDeliveredAtMs, "the delivery marker is recorded")
	assert.Equal(t, atMs, *released.LastDeliveredAtMs, "the marker records exactly the delivered occurrence")
	require.NotNil(t, released.Trigger)
	require.NotNil(t, released.Trigger.Config.AtMs)
	assert.Equal(t, atMs, *released.Trigger.Config.AtMs, "the trigger is kept")
}

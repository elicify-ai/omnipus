package agent

import (
	"context"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #1263 R5 (founder 2026-10-13, L1-AMENDMENTS-20261013.md §1.2; amendment
// §1.5 re-aims this file's former oracle
// TestI1OrdinaryRevivalClearsUnattendedOnlyForHuman): ordinary admission
// revival keeps the chain's attendance for EVERY principal. The former
// clearUnattendedForHuman is deleted — a person reviving a task- or
// plan-rooted chain is not thereby its approval audience, because who is on
// the other end is decided by the chain's persisted root origin, never by
// who revived it. The scheduler and hand-back principals keep the posture
// they always kept.
func TestI1OrdinaryRevivalKeepsUnattendedForEveryPrincipal(t *testing.T) {
	restartStop := func(boot uint64) *session.StopNote {
		return &session.StopNote{At: time.Now().UTC(), By: session.StopActorRestart, Cause: session.StopCauseRestart, BootSeq: boot, Seq: 1}
	}
	// task_stopped and plan_completed model the two real unattended roots; a
	// stopped TASK run's own scheduler re-entry goes through the task
	// executor, not this ordinary-admission entry point, so the non-human
	// principals use the completed-plan shape this entry point really
	// revives. Every principal — a person included — must leave the posture
	// alone.
	cases := []struct {
		name      string
		origin    session.OriginKind
		taskID    string
		ownerKind session.OwnerScopeKind
		ownerID   string
		state     session.LifecycleState
		stopNote  func(boot uint64) *session.StopNote
		by        steer.Principal
	}{
		{"human_revives_stopped_task", session.OriginKindTask, "task-1263-i1", session.OwnerScopeHuman, "",
			session.LifecycleStopped, restartStop, steer.Principal{Kind: steer.PrincipalKindHuman, ID: "d2b-owner"}},
		{"human_revives_completed_plan", session.OriginKindPlan, "", session.OwnerScopePlan, "plan-1263",
			session.LifecycleCompleted, nil, steer.Principal{Kind: steer.PrincipalKindHuman, ID: "d2b-owner"}},
		{"scheduler_revives_completed_plan", session.OriginKindPlan, "", session.OwnerScopePlan, "plan-1263",
			session.LifecycleCompleted, nil, scheduledRevivalPrincipal},
		{"handback_revives_completed_plan", session.OriginKindPlan, "", session.OwnerScopePlan, "plan-1263",
			session.LifecycleCompleted, nil, handbackRevivalPrincipal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newD2bRoot(t)
			boot := h.al.bootEpochFor()
			rec := &session.LifecycleRecord{SessionID: h.id, Generation: 1, State: tc.state, WorkspaceID: testHarnessWorkspaceMembershipID,
				AgentID: testDefaultAgentID, OwnerScopeKind: tc.ownerKind, OwnerScopeID: tc.ownerID,
				Origin:      &session.Origin{Kind: tc.origin, TaskID: tc.taskID},
				ExecutionID: &session.ExecutionIdentity{RunID: "unattended-old", BootSeq: boot}, Unattended: true}
			if tc.stopNote != nil {
				rec.StopNote = tc.stopNote(boot)
			}
			require.NoError(t, h.al.GetSessionLifecycleStore().Persist(rec))
			require.True(t, h.load(t).Unattended, "SETUP: saved record must be unattended")
			by := tc.by
			prepared, err := h.al.prepareOrdinarySessionExecution(context.Background(), h.id,
				processOptions{SessionKey: "agent:" + testDefaultAgentID + ":session:" + h.id,
					TranscriptSessionID: h.id, TranscriptStore: h.al.GetSessionStore()}, &by)
			require.NoError(t, err)
			require.NotNil(t, prepared.execution, "the revival must have admitted an execution")
			t.Cleanup(func() { _ = h.al.finishExecutionDisposition(prepared.execution) })
			got := h.load(t)
			assert.True(t, got.Unattended,
				"revival by %s/%s must never hand an unattended %s chain to a person (R5: the root origin decides)", by.Kind, by.ID, tc.origin)
			assert.NotEqual(t, session.LifecycleStopped, got.State, "SETUP: the record must really have been revived")
		})
	}
}

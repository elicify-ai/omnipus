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

// #891 through I1: ordinary admission revival keeps the same rule as every
// other revival path. Only a person's revival clears Unattended; the scheduler
// and the hand-back principal revive without handing the run to a person.
func TestI1OrdinaryRevivalClearsUnattendedOnlyForHuman(t *testing.T) {
	restartStop := func(boot uint64) *session.StopNote {
		return &session.StopNote{At: time.Now().UTC(), By: session.StopActorRestart, Cause: session.StopCauseRestart, BootSeq: boot, Seq: 1}
	}
	cases := []struct {
		name         string
		state        session.LifecycleState
		stopNote     func(boot uint64) *session.StopNote
		by           steer.Principal
		wantUnattend bool
	}{
		{"human_revives_stopped", session.LifecycleStopped, restartStop, steer.Principal{Kind: steer.PrincipalKindHuman, ID: "d2b-owner"}, false},
		{"human_revives_completed", session.LifecycleCompleted, nil, steer.Principal{Kind: steer.PrincipalKindHuman, ID: "d2b-owner"}, false},
		{"scheduler_revives_restart_stopped", session.LifecycleStopped, restartStop, scheduledRevivalPrincipal, true},
		{"handback_revives_completed", session.LifecycleCompleted, nil, handbackRevivalPrincipal, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newD2bRoot(t)
			boot := h.al.bootEpochFor()
			rec := &session.LifecycleRecord{SessionID: h.id, Generation: 1, State: tc.state, WorkspaceID: testHarnessWorkspaceMembershipID,
				AgentID: testDefaultAgentID, OwnerScopeKind: session.OwnerScopeHuman, Origin: &session.Origin{Kind: session.OriginKindChat},
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
			assert.Equal(t, tc.wantUnattend, got.Unattended, "Unattended after revival by %s/%s", by.Kind, by.ID)
			assert.NotEqual(t, session.LifecycleStopped, got.State, "SETUP: the record must really have been revived")
		})
	}
}

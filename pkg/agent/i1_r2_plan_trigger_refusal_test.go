package agent

import (
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// SHOULD / already-passing control: a normal standing schedule continues after
// restart, but a task/plan owner cannot bypass its plan/task control path.
func TestI1R2TaskPlanTriggerRefusal(t *testing.T) {
	for _, kind := range []string{"task", "plan_nil_origin", "plan_chat_origin", "plan_scope"} {
		for _, trigger := range []string{"heartbeat", "scheduled"} {
			t.Run(kind+"/"+trigger, func(t *testing.T) {
				h := newD2bRoot(t)
				boot := h.al.bootEpochFor()
				rec := &session.LifecycleRecord{SessionID: h.id, Generation: 1, State: session.LifecycleStopped, WorkspaceID: testHarnessWorkspaceMembershipID,
					AgentID: testDefaultAgentID, OwnerScopeKind: session.OwnerScopeHuman, Origin: &session.Origin{Kind: session.OriginKindChat},
					ExecutionID: &session.ExecutionIdentity{RunID: "i1-task-plan-old", BootSeq: boot}, StopNote: &session.StopNote{At: time.Now().UTC(), By: session.StopActorRestart, Cause: session.StopCauseRestart, BootSeq: boot, Seq: 1}}
				switch kind {
				case "task":
					rec.Origin = &session.Origin{Kind: session.OriginKindTask, TaskID: "i1-task"}
				case "plan_nil_origin":
					rec.Origin = nil
					rec.OwnsPlanID = "i1-plan"
				case "plan_chat_origin":
					rec.OwnsPlanID = "i1-plan"
				case "plan_scope":
					rec.OwnerScopeKind = session.OwnerScopePlan
					rec.OwnerScopeID = "i1-plan"
				}
				require.NoError(t, h.al.GetSessionLifecycleStore().Persist(rec))
				before, journal := h.load(t), h.journal(t)
				assert.False(t, session.LifecycleRecordIsStandingRoot(before), "task/plan owner is not an ordinary standing trigger")
				if kind == "plan_chat_origin" || kind == "plan_nil_origin" {
					assert.True(t, standingRootExemptFromSweep(*before), "broad plan-sweep exemption is not ordinary revival authority")
				}
				response, err := h.scheduledTurn(trigger, "Do not bypass task/plan ownership.")
				require.ErrorIs(t, err, steer.ErrDispatchCancelled)
				assert.Empty(t, response)
				assert.Empty(t, h.provider.calls(), "refused trigger cannot invoke the provider")
				assert.Equal(t, before, h.load(t))
				assert.Equal(t, journal, h.journal(t), "record, generation and execution identity remain byte-identical")
			})
		}
	}
}

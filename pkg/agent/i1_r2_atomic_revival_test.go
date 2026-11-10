package agent

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Round 2 MUST 3: a persisted revival is an admission, not an idle seed.
// Every live revived snapshot must already own the new execution identity;
// otherwise a process death before the second stamp leaves it Working forever.
func TestI1R2OrdinaryRevivalStampedAtomically(t *testing.T) {
	for _, state := range []session.LifecycleState{session.LifecycleStopped, session.LifecycleCompleted} {
		t.Run(string(state), func(t *testing.T) {
			h := newD2bRoot(t)
			boot := h.al.bootEpochFor()
			rec := &session.LifecycleRecord{SessionID: h.id, Generation: 1, State: state,
				OwnerScopeKind: session.OwnerScopeHuman, OwnerScopeID: "d2b-owner", WorkspaceID: testHarnessWorkspaceMembershipID, AgentID: testDefaultAgentID,
				Origin: &session.Origin{Kind: session.OriginKindChat}, ExecutionID: &session.ExecutionIdentity{RunID: "i1-old-execution", BootSeq: boot}}
			if state == session.LifecycleStopped {
				rec.StopNote = &session.StopNote{At: time.Now().UTC(), By: session.StopActorRestart, Cause: session.StopCauseRestart, BootSeq: boot, Seq: 1}
			}
			require.NoError(t, h.al.GetSessionLifecycleStore().Persist(rec))
			before := h.journal(t)
			response, err := h.humanTurn(t, "Continue with exactly one stamped execution.")
			require.NoError(t, err)
			assert.Equal(t, d2bReply, response)
			calls := h.provider.calls()
			require.Len(t, calls, 1)
			require.NoError(t, calls[0].readErr)
			require.NotNil(t, calls[0].record.ExecutionID)
			newExecution := *calls[0].record.ExecutionID
			assert.NotEqual(t, "i1-old-execution", newExecution.RunID)
			appended := strings.TrimSpace(string(h.journal(t)[len(before):]))
			require.NotEmpty(t, appended, "actual admission must persist its history")
			liveSnapshots := 0
			for _, line := range strings.Split(appended, "\n") {
				var saved session.LifecycleRecord
				require.NoError(t, json.Unmarshal([]byte(line), &saved))
				if saved.State != session.LifecycleRunning && saved.State != session.LifecycleQueued {
					continue
				}
				liveSnapshots++
				if !assert.NotNil(t, saved.ExecutionID, "atomic revival: no durable live snapshot may have nil identity") {
					continue
				}
				assert.Equal(t, newExecution, *saved.ExecutionID, "revival and admission must publish the SAME new run/boot tuple")
			}
			assert.Greater(t, liveSnapshots, 0, "instrument must inspect at least one actual revived/live snapshot")
		})
	}
}

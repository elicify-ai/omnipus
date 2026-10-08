package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// R1: a message routed by the worker while its old turn is finishing must use
// the same atomic ordinary admission, not a nil-identity pre-revival snapshot.
func TestI1R3InTurnSteeringRevivalIsAtomic(t *testing.T) {
	for _, state := range []session.LifecycleState{session.LifecycleStopped, session.LifecycleCompleted} {
		t.Run(string(state), func(t *testing.T) {
			h := newD2bRoot(t)
			boot := h.al.bootEpochFor()
			rec := &session.LifecycleRecord{SessionID: h.id, Generation: 1, State: state,
				OwnerScopeKind: session.OwnerScopeHuman, OwnerScopeID: "d2b-owner", WorkspaceID: testHarnessWorkspaceMembershipID, AgentID: testDefaultAgentID,
				Origin: &session.Origin{Kind: session.OriginKindChat}, ExecutionID: &session.ExecutionIdentity{RunID: "i1-finished-turn", BootSeq: boot}}
			if state == session.LifecycleStopped {
				rec.StopNote = &session.StopNote{At: time.Now().UTC(), By: session.StopActorRestart, Cause: session.StopCauseRestart, BootSeq: boot, Seq: 1}
			}
			require.NoError(t, h.al.GetSessionLifecycleStore().Persist(rec))
			before := h.journal(t)
			const instruction = "Continue once through the finishing-turn steering path."
			require.NoError(t, h.al.GetSessionStore().AppendTranscript(h.id, session.TranscriptEntry{ID: "i1-steering-human", Role: "user", Content: instruction, AgentID: testDefaultAgentID, Timestamp: time.Now().UTC()}))
			w := newSessionWorker("agent:"+testDefaultAgentID+":session:"+h.id, h.al, func() {})
			t.Cleanup(w.cancel)
			w.inTurn.Store(true) // The discrete finishing-turn routing state under test.
			msg := bus.InboundMessage{Channel: "webchat", ChatID: h.id, SessionID: h.id, Content: instruction, UserInitiated: true, GatewayUserID: "d2b-owner", Sender: bus.SenderInfo{CanonicalID: "d2b-owner"}, Metadata: map[string]string{"agent_id": testDefaultAgentID, "workspace_id": testHarnessWorkspaceMembershipID}}
			require.True(t, w.trySteerIntoLiveTurn(msg), "actual worker steering entry must own this message once")
			select {
			case reply := <-h.al.bus.OutboundChan():
				assert.Equal(t, d2bReply, reply.Content)
				assert.Equal(t, h.id, reply.SessionID)
			case <-time.After(10 * time.Second):
				t.Fatal("accepted revived turn must publish its actual reply")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			require.True(t, h.al.WaitForActiveRequestsContext(ctx), "join actual detached revived turn and publication tail")
			calls := h.provider.calls()
			require.Len(t, calls, 1, "no double run or inbox fallback")
			require.NoError(t, calls[0].readErr)
			require.NotNil(t, calls[0].record.ExecutionID)
			identity := *calls[0].record.ExecutionID
			live := 0
			for _, line := range strings.Split(strings.TrimSpace(string(h.journal(t)[len(before):])), "\n") {
				var saved session.LifecycleRecord
				require.NoError(t, json.Unmarshal([]byte(line), &saved))
				if saved.State != session.LifecycleRunning && saved.State != session.LifecycleQueued {
					continue
				}
				live++
				if !assert.NotNil(t, saved.ExecutionID, "in-turn revival must publish no nil-identity live snapshot") {
					continue
				}
				assert.Equal(t, identity, *saved.ExecutionID, "the accepted instruction has exactly one fresh producing identity")
			}
			assert.Greater(t, live, 0, "journal instrument must see real admission")
		})
	}
}

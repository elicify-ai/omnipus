package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Oracle: I1 fix round 1 MUST 1. A restart is not an operator Stop. The next
// normal trigger must run, while task/plan ownership remains with the plan sweep.
func TestI1R1OriginTriggers(t *testing.T) {
	for _, kind := range []string{"chat", "channel", "heartbeat", "scheduled", "task", "plan"} {
		t.Run(kind, func(t *testing.T) {
			h := newD2bRoot(t)
			if kind != "chat" {
				var meta *session.UnifiedMeta
				var err error
				if kind == "channel" {
					meta, err = h.al.GetSessionStore().NewChannelSession("telegram", "telegram", "i1-restart-peer", testDefaultAgentID, "I1 channel")
				} else {
					sessionType := session.SessionTypeScheduled
					if kind == "heartbeat" {
						sessionType = session.SessionTypeHeartbeat
					}
					if kind == "task" || kind == "plan" {
						sessionType = session.SessionTypeTask
					}
					meta, err = h.al.GetSessionStore().NewSession(sessionType, "", testDefaultAgentID)
				}
				require.NoError(t, err)
				h.id, h.provider.id = meta.ID, meta.ID
				owner, ws := "d2b-owner", testHarnessWorkspaceMembershipID
				require.NoError(t, h.al.GetSessionStore().SetMeta(h.id, session.MetaPatch{Owner: &owner, WorkspaceID: &ws}))
				for _, e := range h.prior {
					require.NoError(t, h.al.GetSessionStore().AppendTranscript(h.id, e))
				}
			}
			h.seedState(t, session.LifecycleRunning)
			ls := h.al.GetSessionLifecycleStore()
			oldBoot := h.al.bootEpochFor()
			require.NotZero(t, oldBoot)
			require.NoError(t, ls.Mutate(h.id, func(rec *session.LifecycleRecord) error {
				rec.ExecutionID = &session.ExecutionIdentity{RunID: "i1-previous-" + kind, BootSeq: oldBoot}
				rec.Origin = &session.Origin{Kind: session.OriginKind(kind)}
				if kind == "task" {
					rec.Origin.TaskID = "i1-task"
				}
				if kind == "plan" {
					rec.Origin = nil
					rec.OwnsPlanID = "i1-owner-plan"
				}
				return nil
			}))
			before := h.load(t)
			journal := h.journal(t)
			boot := session.NewBootEpochStore(filepath.Join(h.al.GetConfig().Agents.Defaults.Home, "boot_epoch"))
			writingBoot, err := boot.Mint()
			require.NoError(t, err)
			require.Greater(t, writingBoot, oldBoot, "instrument: real next physical epoch")
			reopened := session.NewLifecycleStore(ls.Dir())
			h.al.SetSessionMessagingStores(h.al.GetMessageInboxStore(), reopened)
			h.al.SetBootEpochStore(boot)
			h.al.rebuildChannelSessionIndex()
			recovery := &SteerBootRecovery{Lifecycle: reopened, Sessions: h.al.GetSessionStore(), Inbox: h.al.GetMessageInboxStore(), Classifier: NewSteerRecordClassifier(reopened, h.al.GetSessionStore()), BootEpoch: boot}
			require.NoError(t, recovery.Run(context.Background()))
			assert.Empty(t, h.provider.calls(), "boot must not invoke the provider")
			if kind == "task" || kind == "plan" {
				assert.Equal(t, before, h.load(t), "recoverOrdinaryRoot must leave task/plan ownership to the plan sweep")
				assert.Equal(t, journal, h.journal(t), "not even an unchanged lifecycle line may be added")
				return
			}
			recovered := h.load(t)
			require.Equal(t, session.LifecycleStopped, recovered.State)
			require.NotNil(t, recovered.StopNote)
			assert.Equal(t, session.StopCauseRestart, recovered.StopNote.Cause)
			assert.Equal(t, session.LifecycleDisplayInterrupted, session.LifecycleRecordToDisplay(recovered))
			const prompt = "Run the next ordinary trigger after restart."
			var response string
			switch kind {
			case "chat":
				response, err = h.humanTurn(t, prompt)
			case "channel":
				response, _, err = h.al.processMessage(context.Background(), bus.InboundMessage{Channel: "telegram", ChatID: "i1-restart-peer", Content: prompt, UserInitiated: true, Sender: bus.SenderInfo{CanonicalID: "telegram:i1-owner"}, Metadata: map[string]string{"agent_id": testDefaultAgentID, "workspace_id": testHarnessWorkspaceMembershipID}})
			default:
				response, err = h.scheduledTurn(kind, prompt)
			}
			require.NoError(t, err, "a normal next %s trigger must not be refused as though restart were a human Stop", kind)
			assert.Equal(t, d2bReply, response)
			calls := h.provider.calls()
			require.Len(t, calls, 1, "next trigger must execute exactly one provider round")
			require.NoError(t, calls[0].readErr)
			require.NotNil(t, calls[0].record.ExecutionID)
			assert.Equal(t, before.Generation, calls[0].record.Generation, "restart continuation preserves this generation")
			assert.Equal(t, session.LifecycleRunning, calls[0].record.State)
			assert.Equal(t, writingBoot, calls[0].record.ExecutionID.BootSeq)
			assert.NotEqual(t, before.ExecutionID.RunID, calls[0].record.ExecutionID.RunID)
			if kind == "heartbeat" || kind == "scheduled" {
				assert.False(t, calls[0].userInitiated)
				assert.True(t, calls[0].autoDenyAsk, "automatic resumption must not forge a human approval context")
				assert.True(t, calls[0].jobPresent)
			}
			raw, readErr := os.ReadFile(filepath.Join(reopened.Dir(), h.id+".jsonl"))
			require.NoError(t, readErr)
			assert.Greater(t, len(raw), len(journal), "a real fresh execution must extend its durable history")
		})
	}
}

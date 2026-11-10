package agent

// Gate round 1: CR-1 / SFH-1 / PTA-F1. The archive append and pending-index
// write are separate durable boundaries. Stop suppression may skip a wake,
// never the repair that makes the report discoverable on continuation.

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

func rnReviewStoppedParent(t *testing.T, h *pwBootRecoveryHarness) string {
	t.Helper()
	parent := h.newSession(t, session.SessionTypeChat, "")
	h.persist(t, &session.LifecycleRecord{SessionID: parent, Generation: 1, State: session.LifecycleStopped,
		AgentID: "agent-1", WorkspaceID: testHarnessWorkspaceMembershipID, OwnerScopeKind: session.OwnerScopeHuman,
		Origin:   &session.Origin{Kind: session.OriginKindChat},
		StopNote: &session.StopNote{At: time.Now().UTC(), By: session.StopActorHumanUser("rn-owner"), Cause: session.StopCauseStop, Seq: 1}})
	return parent
}

func TestRNReview_HeldPartialSaveIsRepairedBeforeContinuation(t *testing.T) {
	for _, committed := range []bool{false, true} {
		for _, repairFails := range []bool{false, true} {
			t.Run(fmt.Sprintf("committed=%t/repair_failure=%t", committed, repairFails), func(t *testing.T) {
				h := newPWBootRecoveryHarness(t)
				parent := rnReviewStoppedParent(t, h)
				child := h.newSession(t, session.SessionTypeDelegate, parent)
				rec := h.steeredRecord(child, parent, session.LifecycleCompleted)
				if committed {
					rec.State = session.LifecycleRunning
				}
				h.persist(t, rec)
				const result = "RN-HELD-REPORT-ORIGINAL"
				require.NoError(t, h.sessions.AppendTranscript(child, session.TranscriptEntry{Role: "assistant", Content: result, AgentID: "agent-1"}))
				message := bootHandback(t, child, parent, child+":1:final")
				handback, err := message.AsSessionMessageHandback()
				require.NoError(t, err)
				handback.ResultSoFar = result
				require.NoError(t, message.FromSessionMessageHandback(handback))
				payload, err := message.MarshalJSON()
				require.NoError(t, err)
				if committed {
					require.NoError(t, h.lifecycle.Mutate(child, func(current *session.LifecycleRecord) error {
						current.State = session.LifecycleCompleted
						current.FinalDelivery = &session.FinalDeliveryCommit{Generation: 1, CommitID: current.ExecutionID.RunID,
							MessageID: child + ":1:final", ParentSessionID: parent, Outcome: string(steer.OutcomeFinalAnswer),
							Payload: payload, PayloadHash: fmt.Sprintf("%x", sha256.Sum256(payload))}
						return nil
					}))
				}
				fault := errors.New("injected held-input pending-index failure")
				session.SetSavedInputIndexWriteFaultForTest(func(id string) error {
					if id == parent {
						return fault
					}
					return nil
				})
				t.Cleanup(func() { session.SetSavedInputIndexWriteFaultForTest(nil) })
				_, err = h.al.getUpwardDeliverer().Deliver(context.Background(), steer.UpwardEvent{
					ChildSessionID: child, Outcome: steer.OutcomeFinalAnswer, Message: message})
				require.ErrorContains(t, err, fault.Error(), "instrument: the first delivery fails after its archive append")
				id := child + ":1:final"
				addr, placed, err := h.sessions.SavedChatInput(parent, "saved-input-"+id)
				require.NoError(t, err)
				require.NotEmpty(t, addr.EntryID, "instrument: report bytes really landed")
				require.False(t, placed)
				pending, err := h.sessions.PendingChatInputs(context.Background(), parent)
				require.NoError(t, err)
				require.Empty(t, pending, "instrument: the pending directory is really missing this report")
				r := h.recovery()
				if repairFails {
					require.NoError(t, r.Run(context.Background()))
					require.NotEmpty(t, h.notices, "a failed index repair must remain operator-visible, not become an ordinary hold")
					require.Contains(t, h.notices[0], fault.Error())
				}
				session.SetSavedInputIndexWriteFaultForTest(nil)
				h.notices = nil
				require.NoError(t, r.Run(context.Background()))
				require.Empty(t, h.notices)
				require.Empty(t, h.al.bus.InboundChan(), "repair must not wake the intentionally stopped parent")
				pending, err = h.sessions.PendingChatInputs(context.Background(), parent)
				require.NoError(t, err)
				require.Len(t, pending, 1, "boot must repair pending-index discovery before calling this report held")
				const expected = "A delegated session handed back (final): " + result
				require.Equal(t, expected, pending[0].Content)
				unread, _, _, err := h.inbox.Drain(parent, child, "", 0)
				require.NoError(t, err)
				require.Len(t, unread, 1, "repair is not acknowledgement")
				_, err = h.al.steerCanceller().Revive(context.Background(), parent, steer.Principal{Kind: steer.PrincipalKindHuman, ID: "rn-owner"})
				require.NoError(t, err)
				h.provider.open()
				_, _, err = h.al.processMessageAsPrimaryForTest(context.Background(), bus.InboundMessage{
					Channel: "webchat", ChatID: parent, SessionID: parent, Content: "Continue with the saved helper result.",
					UserInitiated: true, GatewayUserID: "rn-owner", Sender: bus.SenderInfo{CanonicalID: "webchat_user"},
					Metadata: map[string]string{"agent_id": "agent-1"}})
				require.NoError(t, err)
				require.Equal(t, 1, gate1CountInMessages(h.provider.await(t), expected), "legitimate continuation receives the original report once")
				included, err := h.al.placeSavedWakeInput(context.Background(), parent, id, "agent-1")
				require.NoError(t, err)
				require.False(t, included, "retry must not place a consumed report twice")
				archive, err := h.sessions.ReadArchive(context.Background(), parent)
				require.NoError(t, err)
				occurrences := 0
				for _, entry := range archive {
					if entry.Role == "user" && entry.Content == expected {
						occurrences++
					}
				}
				require.Equal(t, 1, occurrences)
				unread, _, _, err = h.inbox.Drain(parent, child, "", 0)
				require.NoError(t, err)
				require.Empty(t, unread)
			})
		}
	}
}

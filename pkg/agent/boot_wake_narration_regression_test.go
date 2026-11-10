package agent

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// UAT finding B: an ACKED legacy terminal outcome is not missing just because
// Drain returns only unacknowledged messages. A saved Stop-held outcome remains
// readable, but must not be sent back through delivery on each later boot.
func TestBootWakeRecovery_ConsumedAndStoppedAreNotRetried(t *testing.T) {
	for _, state := range []string{"consumed", "consumed_committed", "consumed_compacted", "parent_stopped", "parent_stopped_committed"} {
		t.Run(state, func(t *testing.T) {
			h := newPWBootRecoveryHarness(t)
			parent := h.newSession(t, session.SessionTypeChat, "")
			parentRec := &session.LifecycleRecord{SessionID: parent, Generation: 1, State: session.LifecycleCompleted,
				AgentID: "agent-1", WorkspaceID: testHarnessWorkspaceMembershipID, OwnerScopeKind: session.OwnerScopeHuman,
				Origin: &session.Origin{Kind: session.OriginKindChat}}
			if strings.HasPrefix(state, "parent_stopped") {
				parentRec.State = session.LifecycleStopped
				parentRec.StopNote = &session.StopNote{At: time.Now().UTC(), By: session.StopActorHumanUser("owner"), Cause: session.StopCauseStop, Seq: 1}
			}
			h.persist(t, parentRec)
			child := h.newSession(t, session.SessionTypeDelegate, parent)
			rec := h.steeredRecord(child, parent, session.LifecycleFailed)
			rec.FailedReason = "failed"
			committed := strings.HasSuffix(state, "_committed")
			if committed {
				rec.State = session.LifecycleRunning
			}
			h.persist(t, rec)
			finalID := fmt.Sprintf("%s:1:final", child)
			if committed {
				message, _, err := terminalErrorBootMessage(rec)
				require.NoError(t, err)
				payload, err := message.MarshalJSON()
				require.NoError(t, err)
				require.NoError(t, h.lifecycle.Mutate(child, func(current *session.LifecycleRecord) error {
					current.State = session.LifecycleFailed
					current.FailedReason = "failed"
					current.FinalDelivery = &session.FinalDeliveryCommit{Generation: 1, CommitID: current.ExecutionID.RunID,
						MessageID: finalID, ParentSessionID: parent, Outcome: string(steer.OutcomeFailed),
						Payload: payload, PayloadHash: fmt.Sprintf("%x", sha256.Sum256(payload))}
					return nil
				}))
			}
			if strings.HasPrefix(state, "consumed") {
				message, _, err := terminalErrorBootMessage(rec)
				require.NoError(t, err)
				_, err = h.inbox.Append(parent, message)
				require.NoError(t, err)
				require.NoError(t, h.inbox.Ack(parent, []string{finalID}))
				if state == "consumed_compacted" {
					// Model placement is positive evidence even after legitimate
					// inbox compaction retires the acknowledged final's entry.
					_, err := h.sessions.SaveChatInput(parent, "saved-input-"+finalID, "agent-1", deliverySummary(message))
					require.NoError(t, err)
					placed, err := h.al.placeSavedWakeInput(context.Background(), parent, finalID, "agent-1")
					require.NoError(t, err)
					require.True(t, placed)
					h.inbox.AckedRetentionMax, h.inbox.CompactionAckedTrigger = 1, 1
					_, err = h.inbox.Append(parent, bootProgress(t, child, parent, "newer-acked-report"))
					require.NoError(t, err)
					require.NoError(t, h.inbox.Ack(parent, []string{"newer-acked-report"}))
					entries, err := h.inbox.Entries(parent)
					require.NoError(t, err)
					for _, entry := range entries {
						if entry.Message != nil {
							require.NotEqual(t, finalID, messageIDOf(*entry.Message), "instrument: compaction actually removed the old final")
						}
					}
				}
			}
			readLog := captureLogFile(t, logger.ERROR)
			appends := 0
			steerUpwardAfterInboxAppendTestHook = func(owner, id string) error {
				if owner == parent && id == finalID {
					appends++
				}
				return nil
			}
			t.Cleanup(func() { steerUpwardAfterInboxAppendTestHook = nil })
			r := h.recovery()
			for boot := 0; boot < 3; boot++ {
				require.NoError(t, r.Run(context.Background()))
			}
			wantAttempts := 0
			if strings.HasPrefix(state, "parent_stopped") {
				wantAttempts = 1 // the first delivery saves the report, later boots do not re-nudge
			}
			require.Equal(t, wantAttempts, appends, "settled/held terminal records must not be delivered forever")
			require.Empty(t, h.notices, "consumption and an intentional Stop are not lost-wake failures")
			require.NotContains(t, readLog(), "recipient was NOT woken")
			require.Empty(t, h.al.bus.InboundChan(), "neither case may start a parent turn")
			pending, err := h.sessions.PendingChatInputs(context.Background(), parent)
			require.NoError(t, err)
			messages, _, _, err := h.inbox.Drain(parent, child, "", 0)
			require.NoError(t, err)
			if strings.HasPrefix(state, "consumed") {
				require.Empty(t, pending)
				require.Empty(t, messages)
			} else {
				require.Len(t, pending, 1, "a Stop must retain the exact input for a later legitimate turn, not drop it")
				require.Len(t, messages, 1, "a held report must not be marked read")
				require.Equal(t, finalID, messageIDOf(messages[0]))
				require.Contains(t, pending[0].Content, "recovered terminal outcome after restart")
			}
		})
	}
}

// PW-D14 is not an intentional Stop hold: a saved outcome must still reach
// WakeSession, which authenticates whether the fresh helper may reopen it.
func TestBootWakeRecovery_RestartHoldIsNotAHandledOutcome(t *testing.T) {
	h := newPWBootRecoveryHarness(t)
	parent := h.newSession(t, session.SessionTypeChat, "")
	h.persist(t, &session.LifecycleRecord{SessionID: parent, Generation: 1, State: session.LifecycleStopped,
		AgentID: "agent-1", WorkspaceID: testHarnessWorkspaceMembershipID, OwnerScopeKind: session.OwnerScopeHuman,
		Origin:   &session.Origin{Kind: session.OriginKindChat},
		StopNote: &session.StopNote{At: time.Now().UTC(), Cause: session.StopCauseRestart, By: session.StopActorSystem, Seq: 1}})
	child := h.newSession(t, session.SessionTypeDelegate, parent)
	message := bootHandback(t, child, parent, child+":1:final")
	_, err := h.inbox.Append(parent, message)
	require.NoError(t, err)
	_, err = h.sessions.SaveChatInput(parent, "saved-input-"+child+":1:final", "agent-1", "fresh helper outcome")
	require.NoError(t, err)
	handled, consumed, err := h.recovery().bootWakeHandled(parent, child+":1:final")
	require.NoError(t, err)
	require.False(t, handled, "only the authenticated wake entry may decide whether a restart-stopped parent can be reopened")
	require.False(t, consumed)
}

// Negative control: a durable inbox/saved input alone does NOT prove a wake.
// A failed notifier must remain visible and retryable. Repairing it must reach
// the real parent model once; subsequent recovery must not replay that outcome.
func TestBootWakeRecovery_RealLostWakeStaysVisibleAndRetries(t *testing.T) {
	h := newPWBootRecoveryHarness(t)
	parent := h.rootSession(t)
	child := h.newSession(t, session.SessionTypeDelegate, parent)
	rec := h.steeredRecord(child, parent, session.LifecycleFailed)
	rec.FailedReason = "failed"
	h.persist(t, rec)
	notifier := h.al.asyncNotifier
	h.al.asyncNotifier = nil
	t.Cleanup(func() { h.al.asyncNotifier = notifier })
	r := h.recovery()
	require.NoError(t, r.Run(context.Background()))
	require.Len(t, h.notices, 1)
	require.Contains(t, h.notices[0], "wake session: notifier is not configured")
	require.Empty(t, h.al.bus.InboundChan())
	pending, err := h.sessions.PendingChatInputs(context.Background(), parent)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	want := pending[0].Content
	h.notices = nil
	h.al.asyncNotifier = notifier
	require.NoError(t, r.Run(context.Background()))
	wake := narrationNextWake(t, h.al, parent)
	require.Equal(t, want, wake.Content)
	require.Equal(t, []string{want}, narrationDriveWake(t, h.al, h.provider, wake))
	require.NoError(t, r.Run(context.Background()))
	require.Empty(t, h.al.bus.InboundChan(), "already-consumed terminal outcome must not start another parent turn")
	require.Empty(t, h.notices)
	left, _, _, err := h.inbox.Drain(parent, child, "", 0)
	require.NoError(t, err)
	require.Empty(t, left, "the delivered outcome is acknowledged only after model placement")
}

// Test the diagnostic instrument with a genuinely unexplained no-wake result.
// This is not a log-only fix: expected reasons may be quiet, but a caller that
// cannot explain a stored-not-woken delivery must still produce an error.
func TestBootWakeRecovery_UnexplainedNoWakeIsStillAnError(t *testing.T) {
	readLog := captureLogFile(t, logger.ERROR)
	h := newPWBootRecoveryHarness(t)
	parent := h.rootSession(t)
	child := h.newSession(t, session.SessionTypeDelegate, parent)
	rec := h.steeredRecord(child, parent, session.LifecycleFailed)
	rec.FailedReason = "failed"
	h.persist(t, rec)
	message := bootHandback(t, child, parent, child+":1:final")
	reportUndeliveredWake("unexplained-wake-control", steer.UpwardEvent{ChildSessionID: child, Outcome: steer.OutcomeFinalAnswer, Message: message}, parent, 1,
		steer.Delivery{MessageID: child + ":1:final", Outcome: steer.DeliveryStoredNotWoken})
	logged := readLog()
	require.Contains(t, logged, "recipient was NOT woken")
	require.True(t, strings.Contains(logged, child) && strings.Contains(logged, parent), "failure diagnostic must identify both sessions")
}

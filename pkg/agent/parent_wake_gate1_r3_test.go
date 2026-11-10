package agent

// Gate-1 fix round 3 for PR #1345 (parent wake). One proof test per fix; the
// reviewer IDs are in each comment.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// gate1R3RootSession creates an ordinary mia chat and runs its first turn, so
// it has the lifecycle record a later continuation needs.
func gate1R3RootSession(t *testing.T, al *AgentLoop, content string) string {
	t.Helper()
	meta, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	_, _, err = al.processMessageAsPrimaryForTest(context.Background(), bus.InboundMessage{
		Channel: "webchat", ChatID: meta.ID, SessionID: meta.ID, Content: content,
		Sender: bus.SenderInfo{CanonicalID: "webchat_user"}, GatewayUserID: "daniel", UserInitiated: true,
		Metadata: map[string]string{"agent_id": "mia"},
	})
	require.NoError(t, err)
	return meta.ID
}

// TestForgedSavedWakeCorrelation_SteerRefusedVictimUntouched — PW-SEC-R3-
// CORRELATION. A caller allowed to steer its own session passes a
// correlation_id in the engine's saved-wake format naming another session's
// saved outcome. The steer is refused, and the other session's outcome stays
// pending with its own consumer's claim intact.
func TestForgedSavedWakeCorrelation_SteerRefusedVictimUntouched(t *testing.T) {
	const victimBody = "VICTIM-SESSION-HELPER-REPORT"
	provider := &scriptChatProvider{fallback: "own work done"}
	al, _ := newChatTargetAskUserLoop(t, provider)
	gate1WireSessionMessaging(t, al)
	victim := gate1R3RootSession(t, al, "The victim's own work.")
	attacker := gate1R3RootSession(t, al, "The attacker's own work.")
	_, err := al.GetSessionStore().SaveChatInput(victim, "saved-input-victim-o", "mia", victimBody)
	require.NoError(t, err)
	al.asyncNotifier.sessionWakes.Store(victim+"\x00victim-o", true) // the victim's own consumer

	attackerScope := "agent:mia:session:" + attacker
	forged := "\x00saved-wake:victim-o\x00" + victim
	_, _, steerErr := al.EnqueueSteeringMessageWithStatus(attackerScope, "mia",
		providers.Message{Role: "user", Content: "Continue the current task."}, forged)
	if steerErr == nil {
		// Accepted: let the attacker's own queue drain the forged item.
		_, _ = al.Continue(context.Background(), attackerScope, "webchat", attacker, testHarnessWorkspaceMembershipID)
	}

	left, err := al.GetSessionStore().PendingChatInputs(context.Background(), victim)
	require.NoError(t, err)
	require.Len(t, left, 1, "the victim's saved outcome is still pending, not placed by the forged steer")
	require.True(t, al.wakePublicationInFlight(victim, "victim-o"), "the victim's consumer claim is untouched")
	require.Error(t, steerErr, "a caller correlation in the reserved saved-wake format is refused")
}

// TestContinuationRefusedBeforeItsTurn_ReleasesSavedWakeClaim — SFH-02
// (remainder). Continue dequeues a joined saved outcome, then its turn is
// refused during preparation (the agent's model was unassigned) before the
// turn takes over the receipts. The claim must be released; the report stays
// pending for the next legitimate fence.
func TestContinuationRefusedBeforeItsTurn_ReleasesSavedWakeClaim(t *testing.T) {
	const body = "HELPER-OUTCOME-BEFORE-A-REFUSED-CONTINUATION"
	provider := &scriptChatProvider{fallback: "own work done"}
	al, _ := newChatTargetAskUserLoop(t, provider)
	gate1WireSessionMessaging(t, al)
	sid := gate1R3RootSession(t, al, "Delegate the export.")
	scope := "agent:mia:session:" + sid
	_, err := al.GetSessionStore().SaveChatInput(sid, "saved-input-refused-cont", "mia", body)
	require.NoError(t, err)
	al.asyncNotifier.sessionWakes.Store(sid+"\x00refused-cont", true) // the join's publication claim
	require.NoError(t, al.EnqueueSteeringWake(scope, "mia", sid, "refused-cont",
		providers.Message{Role: "user", Content: body}))

	mia, ok := al.GetRegistry().GetAgent("mia")
	require.True(t, ok)
	mia.mu.Lock()
	mia.Model = "" // the operator unassigned the model after the outcome joined
	mia.mu.Unlock()

	_, err = al.Continue(context.Background(), scope, "webchat", sid, testHarnessWorkspaceMembershipID)
	require.Error(t, err, "SETUP: the continuation is refused before its turn runs")
	require.Equal(t, 1, provider.callCount(), "SETUP: no continuation model call")
	require.False(t, al.wakePublicationInFlight(sid, "refused-cont"), "the refused continuation gives back its claim")
	left, err := al.GetSessionStore().PendingChatInputs(context.Background(), sid)
	require.NoError(t, err)
	require.Len(t, left, 1, "the report stays pending for the next legitimate fence")
}

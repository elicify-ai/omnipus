package agent

// PTA-F2 is coverage for existing immutable-input reuse. Removing a reuse
// branch must make these tests fail; correctly preserving the first saved
// bytes requires no new outcome, session, or publication format.

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

func TestRNReview_FinalReplayKeepsSnapshotAfterReportsChange(t *testing.T) {
	al, lifecycle, inbox, deliverer := newPWDeliverTestLoop(t)
	const parent, child = "rn-replay-parent", "rn-replay-child"
	pwSeedDeliverySessions(t, al, parent, child)
	tool, ctx := u4NewMessageParentTool(lifecycle, deliverer, child)
	for _, update := range []struct{ id, text string }{
		{"first-report", strings.Repeat("A", 17000)},
		{"omitted-report", strings.Repeat("B", 17000)},
	} {
		result := tool.Execute(ctx, rnReviewQuietArgs("progress", update.id, update.text))
		require.False(t, result.IsError, "%s", result.ForLLM)
	}
	notifier := al.asyncNotifier
	al.asyncNotifier = nil
	t.Cleanup(func() { al.asyncNotifier = notifier })
	event := steer.UpwardEvent{ChildSessionID: child, Outcome: steer.OutcomeFinalAnswer,
		Message: bootHandback(t, child, parent, child+":1:final")}
	_, err := deliverer.Deliver(context.Background(), event)
	require.ErrorContains(t, err, "notifier is not configured")
	pending, err := al.GetSessionStore().PendingChatInputs(context.Background(), parent)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	original := pending[0].Content
	require.Contains(t, original, strings.Repeat("A", 17000))
	require.NotContains(t, original, strings.Repeat("B", 17000))
	require.Contains(t, original, "More stored reports are available through delegate inbox")

	require.NoError(t, inbox.Ack(parent, []string{"first-report"}))
	result := tool.Execute(ctx, rnReviewQuietArgs("progress", "later-report", "REPORT-ARRIVED-AFTER-SNAPSHOT"))
	require.False(t, result.IsError, "%s", result.ForLLM)
	al.asyncNotifier = notifier
	delivery, err := deliverer.Deliver(context.Background(), event)
	require.NoError(t, err, "replay must reuse the original body, not rebuild a changed report snapshot")
	require.Equal(t, steer.DeliveryWoke, delivery.Outcome)
	wake := narrationNextWake(t, al, parent)
	require.Equal(t, original, wake.Content)
	inst, ok := al.GetRegistry().GetAgent("parent-agent")
	require.True(t, ok)
	provider, ok := inst.Provider.(*pwHeldProvider)
	require.True(t, ok)
	require.Equal(t, []string{original}, narrationDriveWake(t, al, provider, wake))
	_, placed, err := al.GetSessionStore().SavedChatInput(parent, "saved-input-"+child+":1:final")
	require.NoError(t, err)
	require.True(t, placed)
	_, err = deliverer.Deliver(context.Background(), event)
	require.NoError(t, err)
	require.Empty(t, al.bus.InboundChan(), "consumed replay must not publish a duplicate wake")
	dt, parentCtx := u4DelegateToolFor(lifecycle, inbox, parent)
	result = dt.Execute(parentCtx, map[string]any{"action": "inbox", "session_id": child})
	require.False(t, result.IsError, "%s", result.ForLLM)
	require.Contains(t, result.ForLLM, strings.Repeat("B", 17000), "omitted reports stay accessible through the real inbox tool")
	require.Contains(t, result.ForLLM, "REPORT-ARRIVED-AFTER-SNAPSHOT")
	entries, err := al.GetSessionStore().ReadTranscript(parent)
	require.NoError(t, err)
	copies := 0
	for _, entry := range entries {
		if entry.ID == "saved-input-"+child+":1:final" {
			copies++
			require.Equal(t, original, entry.Content)
		}
	}
	require.Equal(t, 1, copies, "replay retains one original saved body")
}

func TestRNReview_RestartReplayKeepsSnapshotAfterHelperStops(t *testing.T) {
	h := newPWBootRecoveryHarness(t)
	parent := h.rootSession(t)
	child := h.newSession(t, session.SessionTypeDelegate, parent)
	h.persist(t, h.steeredRecord(child, parent, session.LifecycleRunning))
	h.seedCompletedToolHistory(t, child)
	notifier := h.al.asyncNotifier
	h.al.asyncNotifier = nil
	t.Cleanup(func() { h.al.asyncNotifier = notifier })
	r := h.recovery()
	require.NoError(t, r.Run(context.Background()))
	h.provider.await(t) // automatic continuation really reached the provider
	require.NotEmpty(t, h.notices)
	require.Contains(t, strings.Join(h.notices, "\n"), "notifier is not configured")
	pending, err := h.sessions.PendingChatInputs(context.Background(), parent)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	original, savedID := pending[0].Content, pending[0].ID
	require.Equal(t, "Helper "+child+": Interrupted by a restart. Automatic continuation has started (current state: running). This is not a request to resume the helper.", original)
	_, err = h.al.steerCanceller().CancelSubtree(context.Background(), child, steer.Principal{Kind: steer.PrincipalKindHuman, ID: "rn-owner"})
	require.NoError(t, err)
	after := awaitLandedStopped(t, h.al, child, "replay test human Stop")
	require.Equal(t, session.StopCauseStop, after.StopNote.Cause)
	h.al.steerAdmission().turns.Wait()
	h.notices = nil
	h.al.asyncNotifier = notifier
	require.NoError(t, r.Run(context.Background()))
	require.Empty(t, h.notices, "a changed helper state must not conflict with the first saved restart input")
	wake := narrationNextWake(t, h.al, parent)
	require.Equal(t, original, wake.Content)
	require.Equal(t, []string{original}, narrationDriveWake(t, h.al, h.provider, wake))
	_, placed, err := h.sessions.SavedChatInput(parent, savedID)
	require.NoError(t, err)
	require.True(t, placed)
	require.NoError(t, r.Run(context.Background()))
	require.Empty(t, h.al.bus.InboundChan(), "the consumed restart outcome must not publish another wake")
	entries, err := h.sessions.ReadTranscript(parent)
	require.NoError(t, err)
	copies := 0
	for _, entry := range entries {
		if entry.ID == savedID {
			copies++
			require.Equal(t, original, entry.Content)
		}
	}
	require.Equal(t, 1, copies)
}

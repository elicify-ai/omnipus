package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/askuser"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGoalReminderContract derives its oracles from dispatch R1-R3, not from
// implementation helpers. Do not run in parallel: the keeper state is global.
func TestGoalReminderContract(t *testing.T) {
	t.Run("CompletedRootReminderLaunchesReviewer", grCompletedRootReviewer)
	t.Run("SecondReminderGetsFreshExecutionAndReviewer", grSecondReminder)
	t.Run("NormalCompletionSettlesProducingIdentity", grNormalCompletion)
	t.Run("InactiveParentStillRefusesReviewer", grInactiveParent)
	t.Run("HumanStopSuppressesKeeperAndPublishedReminder", grHumanStop)
	t.Run("UnansweredQuestionSuppressesKeeperAndPublishedReminder", grUnansweredQuestion)
	t.Run("DuplicateDuringLiveReminderDoesNotStartExecution", grDuplicateDuringLive)
	t.Run("DuplicateAfterSettlementDoesNotStartExecution", grDuplicateAfterSettlement)
	t.Run("StaleReminderAfterNewHumanRoundDoesNotStartExecution", grStaleAfterHuman)
	t.Run("ConcurrentHumanInputIsSerializedBehindReminder", grConcurrentHuman)
	t.Run("ReminderHistoryIsVisiblePlainSystemEventNotUserBubble", grReminderHistory)
}

func grCompletedRootReviewer(t *testing.T) {
	h := newGRHarness(t)
	prior := h.completedRoot(t)
	h.arm(t)
	before := h.journal(t)
	run := h.system(h.reminder(t))
	call := grAwaitCall(t, h.root, run)
	h.assertAdmission(t, call, prior)
	h.launchReviewer(t, "reminder-review-1")
	h.finish(t, run, call)
	h.assertSettled(t, call, prior, before)
}

func grSecondReminder(t *testing.T) {
	h := newGRHarness(t)
	initial := h.completedRoot(t)
	h.arm(t)
	firstRun := h.system(h.reminder(t))
	first := grAwaitCall(t, h.root, firstRun)
	h.assertAdmission(t, first, initial)
	firstChild := h.launchReviewer(t, "reminder-first-review")
	h.finish(t, firstRun, first)
	prior := h.load(t)
	// Age ONLY the clock; never manually clear the keeper's idle-settling
	// marker. A real completed reminder must re-arm its own next cycle.
	h.age(t)
	secondRun := h.system(h.reminder(t))
	second := grAwaitCall(t, h.root, secondRun)
	h.assertAdmission(t, second, prior)
	assert.Equal(t, initial.Generation+2, second.record.Generation, "R3: two reminders are two genuine new ordinary generations")
	require.NotNil(t, second.record.ExecutionID)
	assert.NotEqual(t, first.record.ExecutionID.RunID, second.record.ExecutionID.RunID, "R3: the second reminder cannot reuse the first execution identity")
	secondChild := h.launchReviewer(t, "reminder-second-review")
	if firstChild != nil && secondChild != nil {
		assert.NotEqual(t, firstChild.SessionID, secondChild.SessionID, "each reminder must really launch a distinct reviewer child")
	}
	h.finish(t, secondRun, second)
	assert.Equal(t, session.LifecycleCompleted, h.load(t).State)
}

func grNormalCompletion(t *testing.T) {
	h := newGRHarness(t)
	prior := h.completedRoot(t)
	h.arm(t)
	before := h.journal(t)
	run := h.system(h.reminder(t))
	call := grAwaitCall(t, h.root, run)
	h.assertAdmission(t, call, prior)
	h.finish(t, run, call)
	h.assertSettled(t, call, prior, before)
}

func grInactiveParent(t *testing.T) {
	h := newGRHarness(t)
	before := h.completedRoot(t)
	journal := h.journal(t)
	launched, err := NewSteerLauncher(h.al).Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: h.id, TargetAgentID: grReviewerID, Task: "Must not bypass inactive-parent safety.",
		Origin: steer.Origin{Kind: steer.OriginKindDelegate, CallID: "refusal-control"},
	})
	require.ErrorIs(t, err, steer.ErrSteeringStopped, "R1: fix admission, never remove the real inactive-parent guard")
	assert.Contains(t, err.Error(), steer.ErrSteeringStopped.Error())
	assert.Empty(t, launched.SessionID)
	children, listErr := h.al.GetSessionLifecycleStore().List(session.LifecycleFilter{SteeringSessionID: h.id})
	require.NoError(t, listErr)
	assert.Empty(t, children, "inactive-parent refusal must leave no child lifecycle")
	assert.Equal(t, before, h.load(t))
	assert.Equal(t, journal, h.journal(t))
	assert.Equal(t, 0, h.reviewer.count())
}

func grHumanStop(t *testing.T) {
	h := newGRHarness(t)
	h.completedRoot(t)
	h.arm(t)
	published := h.reminder(t) // Stop lands AFTER production publication.
	humanRun := h.human(t, "Please keep working until I press Stop.")
	humanCall := grAwaitCall(t, h.root, humanRun)
	grStop(t, h)
	h.root.release(humanCall)
	grJoin(t, humanRun)
	grAwaitOwner(t, humanCall)
	stopped := h.load(t)
	require.Equal(t, session.LifecycleStopped, stopped.State, "SETUP: real human Stop must have landed")
	require.NotNil(t, stopped.StopNote)
	assert.Equal(t, session.StopCauseStop, stopped.StopNote.Cause)
	before := h.journal(t)
	h.age(t)
	h.assertNoKeeperWake(t, "explicit human Stop")
	h.rejectReminder(t, published, "a queued reminder published before human Stop")
	assert.Equal(t, stopped, h.load(t), "automatic reminder cannot undo landed Stop or mint a replacement identity")
	assert.Equal(t, before, h.journal(t))
	assert.Equal(t, "active", string(goalRecordForSession(t, h.id).State), "Stop pauses work without ending its goal")
}

func grUnansweredQuestion(t *testing.T) {
	h := newGRHarness(t)
	before := h.completedRoot(t)
	h.arm(t)
	published := h.reminder(t) // Card opens AFTER the real reminder is queued.
	registry := askuser.NewRegistry(h.al.GetSessionStore(), nil, askuser.Options{})
	t.Cleanup(registry.Quiesce)
	h.al.SetAskUserRegistry(registry)
	pending := &askuser.PendingSet{
		CardID: "goal-reminder-question", RoutingSessionKey: "question-" + h.id,
		TranscriptSessionID: h.id, AgentID: testDefaultAgentID, Channel: "webchat", ChatID: "chat-" + h.id,
		Owner: "goal-reminder-owner", Status: askuser.StatusPending,
		Questions: []askuser.Question{{Header: "Scope", Question: "Which review scope should I use?",
			Options: []askuser.Option{{Label: "Narrow"}, {Label: "Broad"}}}},
	}
	require.NoError(t, registry.CreatePending(pending), "SETUP: real unanswered card must persist")
	h.age(t)
	h.assertNoKeeperWake(t, "an unanswered question card")
	h.rejectReminder(t, published, "a queued reminder while the question card is unanswered")
	assert.Equal(t, before, h.load(t), "a held reminder must not admit an execution")
	card, ok := registry.PendingForSession(h.id)
	require.True(t, ok)
	assert.Equal(t, pending.CardID, card.CardID)
	assert.Equal(t, askuser.StatusPending, card.Status)
	meta, err := h.al.GetSessionStore().GetMeta(h.id)
	require.NoError(t, err)
	assert.Contains(t, meta.PendingAskJSON, pending.CardID, "pending card must remain durable, not be silently answered by a reminder")
}

func grDuplicateDuringLive(t *testing.T) {
	h := newGRHarness(t)
	h.completedRoot(t)
	h.arm(t)
	published := h.reminder(t)
	run := h.system(published)
	call := grAwaitCall(t, h.root, run)
	before := h.load(t)
	h.rejectReminder(t, published, "duplicate delivery while the original reminder is running")
	assert.Equal(t, before, h.load(t), "duplicate must not replace the producing execution")
	h.assertOneLiveRoot(t)
	h.finish(t, run, call)
}

func grDuplicateAfterSettlement(t *testing.T) {
	h := newGRHarness(t)
	h.completedRoot(t)
	h.arm(t)
	published := h.reminder(t)
	run := h.system(published)
	call := grAwaitCall(t, h.root, run)
	h.finish(t, run, call)
	before, journal := h.load(t), h.journal(t)
	h.rejectReminder(t, published, "duplicate delivery after its reminder already completed")
	assert.Equal(t, before, h.load(t))
	assert.Equal(t, journal, h.journal(t), "duplicate cannot append a second lifecycle round")
}

func grStaleAfterHuman(t *testing.T) {
	h := newGRHarness(t)
	h.completedRoot(t)
	h.arm(t)
	stale := h.reminder(t)
	run := h.human(t, "Use my newer instruction instead of the old reminder.")
	call := grAwaitCall(t, h.root, run)
	h.finish(t, run, call)
	before, journal := h.load(t), h.journal(t)
	h.rejectReminder(t, stale, "old reminder delivered after a newer human round settled")
	assert.Equal(t, before, h.load(t))
	assert.Equal(t, journal, h.journal(t))
}

func grConcurrentHuman(t *testing.T) {
	h := newGRHarness(t)
	prior := h.completedRoot(t)
	h.arm(t)
	msg := h.reminder(t)
	require.True(t, h.al.dispatchSystemMessageToSessionWorker(msg, "webchat"), "actual reminder must enter the real serialized worker")
	waiting := &grPending{done: make(chan struct{})} // provider-entry watchdog; worker owns completion
	reminder := grAwaitCall(t, h.root, waiting)
	h.assertAdmission(t, reminder, prior)
	before := h.load(t)
	const humanText = "This human instruction must not run concurrently or get lost."
	human := h.humanInput(t, humanText)
	scope, _, ok := h.al.resolveSteeringTarget(human)
	require.True(t, ok)
	require.True(t, h.al.dispatchSessionWorker(scope, human))
	worker := h.worker(t)
	assert.Equal(t, 1, worker.waitingInboxCount(), "human input must be in the real worker queue, not a second executing turn")
	assert.Equal(t, 2, h.root.count(), "only the setup turn and the held reminder may have reached provider")
	h.assertOneLiveRoot(t)
	assert.Equal(t, before, h.load(t), "queued human input cannot replace the held reminder's execution")
	h.root.release(reminder)
	humanCall := grAwaitCall(t, h.root, waiting)
	assert.True(t, humanCall.human)
	assert.Equal(t, "goal-reminder-owner", humanCall.userID)
	assert.NotEqual(t, reminder.claim.RunID, humanCall.claim.RunID, "serialized human input needs its own later execution")
	var occurrences int
	for _, message := range humanCall.messages {
		if message.Role == "user" && message.Content == humanText {
			occurrences++
		}
	}
	assert.Equal(t, 1, occurrences, "queued real human instruction must be presented exactly once")
	h.assertOneLiveRoot(t)
	h.root.release(humanCall)
	grAwaitOwner(t, humanCall)
	worker.cancel()
	select {
	case <-worker.done:
	case <-h.ctx.Done():
		t.Fatal("worker cancelled unexpectedly")
	}
	assert.Equal(t, session.LifecycleCompleted, h.load(t).State)
}

func grReminderHistory(t *testing.T) {
	h := newGRHarness(t)
	h.completedRoot(t)
	h.arm(t)
	run := h.system(h.reminder(t))
	call := grAwaitCall(t, h.root, run)
	h.finish(t, run, call)
	entries := h.visibleHistory(t)
	var events, users int
	for _, entry := range entries {
		if entry.Role == "user" {
			users++
			assert.Equal(t, "Start the requested work.", entry.Content, "R2: only the actual human input may be a user bubble")
		}
		if entry.Role == "system" && strings.HasPrefix(strings.TrimSpace(entry.Content), "Goal reminder:") {
			events++
			assert.Equal(t, session.EntryTypeSystem, entry.Type)
			assert.Empty(t, entry.SystemSubtype, "R2: reuse generic system events, not a new kind or special payload")
			assert.NotContains(t, entry.Content, "[System:")
			assert.NotContains(t, entry.Content, "system:goal_loop")
			assert.NotContains(t, entry.Content, "\n", "R2: reminder is a plain system event line, not the internal model prompt")
		}
	}
	assert.Equal(t, 1, users, "R2: do not archive automatic model input as another human chat bubble")
	assert.Equal(t, 1, events, "R2: actual reminder must remain visible as one plain system row in the reopened public chat projection")
}

package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gitevidence"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// R6's exact public sentence is an independent founder oracle. Never use the
// implementation's constant to construct this expected value.
const grPauseLine = "No progress after 5 reminders — goal paused. Reply to continue."

// R6 counts assessed outcomes, NOT lifetime dispatches. The first reminder
// has zero assessed outcomes; the check following five unproductive completed
// reminders pauses before a sixth. An observed productive interval resets 0.
func TestGoalReminderBrakeContract(t *testing.T) {
	t.Run("TextOnlyReminderCountsAnUnproductiveOutcome", grBrakeTextOnly)
	t.Run("FourUnproductiveOutcomesStillAllowFifthReminder", grBrakeFourThenFifth)
	t.Run("FifthUnproductiveOutcomePausesVisiblyBeforeSixth", grBrakeFifthPauses)
	t.Run("HumanReplyResumesPausedGoal", grBrakeHumanResumes)
	t.Run("PersistedStreakSurvivesColdKeeperState", grBrakeRestart)
	t.Run("RealExecutedToolCallResetsStreak", grBrakeToolProgress)
	t.Run("MoreThanFiveProductiveRemindersAreAllowed", grBrakeNoLifetimeCap)
	t.Run("SetTodosOnlyDoesNotResetStreak", grBrakeTodosNotProgress)
	t.Run("ProseCompletionClaimDoesNotResetStreak", grBrakeProseNotProgress)
	t.Run("GoalScopedFileChangeResetsStreak", grBrakeFileProgress)
	t.Run("CompletedHelperActivityResetsStreak", grBrakeHelperProgress)
	t.Run("RunningBackgroundCommandSuppressesReminder", grBrakeBackgroundCommands)
	t.Run("UnrelatedForegroundAndFinishedCommandsDoNotSuppress", grBrakeUnrelatedBackground)
	t.Run("LiveDescendantSuppressesAfterRootReturned", grBrakeLiveHelper)
	t.Run("QueuedDescendantSuppressesWithoutLiveTurn", grBrakeQueuedHelper)
	t.Run("IndependentDispatchedTaskSuppressesItsInitiatingGoal", grBrakeIndependentTask)
	t.Run("RelatedPlanUsesInitiatorNotCoordinatorOrAgentWideOwnership", grBrakeRelatedPlan)
	t.Run("NewTaskOwnedGoalEvidenceResetsStreak", grBrakeEvidenceProgress)
	t.Run("BackgroundCompletionResetsOnlyItsOwningGoal", grBrakeBackgroundCompletion)
	t.Run("ActualGoalClaimOnlyDoesNotResetStreak", grBrakeClaimToolNotProgress)
}

func grNewRecordedBrake(t *testing.T) *grHarness {
	t.Helper()
	h := grNewR6Harness(t)
	h.completedRoot(t)
	h.arm(t)
	return h
}

func grSetStreak(t *testing.T, h *grHarness, streak int) {
	t.Helper()
	_, err := resolveGoalRecordStore().Update(h.goalID, func(g *goal.Goal) error {
		g.ZeroOutputPushes = streak
		return nil
	})
	require.NoError(t, err)
	reopened, err := goal.NewStore(config.OmnipusHomeDir()).Get(h.goalID)
	require.NoError(t, err)
	require.Equal(t, streak, reopened.ZeroOutputPushes, "SETUP: boundary streak must really be durable")
}

func grTextReminder(t *testing.T, h *grHarness, msg bus.InboundMessage) {
	t.Helper()
	run := h.system(msg)
	call := grAwaitCall(t, h.root, run)
	h.finish(t, run, call)
	grAwaitOwner(t, call)
	settled := h.load(t)
	require.Equal(t, session.LifecycleCompleted, settled.State, "an outcome must actually complete before it can enter the R6 assessment")
	require.Equal(t, call.record.ExecutionID, settled.ExecutionID, "count only this actual producing execution's completion")
}

func grBrakeTextOnly(t *testing.T) {
	h := grNewRecordedBrake(t)
	first := h.reminder(t)
	assert.Equal(t, 0, goalRecordForSession(t, h.id).ZeroOutputPushes, "R6: publishing the first reminder is not an assessed unproductive outcome")
	grTextReminder(t, h, first)
	h.age(t)
	second := h.reminder(t)
	assert.Equal(t, 1, goalRecordForSession(t, h.id).ZeroOutputPushes, "R6: a completed text-only response is one unproductive reminder outcome")
	grTextReminder(t, h, second)
}

func grBrakeFourThenFifth(t *testing.T) {
	h := grNewRecordedBrake(t)
	for dispatch := 1; dispatch <= 5; dispatch++ {
		if dispatch > 1 {
			h.age(t)
		}
		msg := h.reminder(t)
		assert.Equal(t, dispatch-1, goalRecordForSession(t, h.id).ZeroOutputPushes,
			"R6: dispatch %d must assess only the %d completed unproductive predecessors", dispatch, dispatch-1)
		assert.Equal(t, 0, grHasSystemLine(h.visibleHistory(t), grPauseLine), "R6: fewer than five completed outcomes cannot pause the goal")
		grTextReminder(t, h, msg)
	}
	assert.Equal(t, 6, h.root.count(), "one bootstrap human request plus exactly five actually executing reminders")
}

// Arrange four already-assessed failures while the actual fifth reminder is
// outstanding. This durable boundary fixture isolates the pause and restart
// oracles even when the unfixed lifetime-two cap prevents a five-cycle replay.
func grFifthOutstanding(t *testing.T) *grHarness {
	t.Helper()
	h := grNewRecordedBrake(t)
	fifth := h.reminder(t)
	grSetStreak(t, h, 4)
	grTextReminder(t, h, fifth)
	h.age(t)
	return h
}

func grAssertFifthPause(t *testing.T, h *grHarness) {
	t.Helper()
	beforeCalls := h.root.count()
	h.al.goalQuietWindowSettle(time.Now())
	select {
	case msg := <-h.al.bus.InboundChan():
		t.Errorf("R6: fifth unproductive outcome must pause before a sixth reminder, got %+v", msg)
	default:
	}
	g := goalRecordForSession(t, h.id)
	assert.Equal(t, 5, g.ZeroOutputPushes, "R6: pause records exactly five consecutive assessed unproductive outcomes")
	assert.Equal(t, "active", string(g.State), "a stuck brake pauses work; it must not falsely end or complete the goal")
	assert.Equal(t, 0, g.Round, "the idle brake must never trigger timer judging")
	assert.Equal(t, beforeCalls, h.root.count())
	assert.Equal(t, 1, grHasSystemLine(h.visibleHistory(t), grPauseLine), "R6: pause must be visible once as the exact plain system event, not hidden input or a user bubble")
}

func grBrakeFifthPauses(t *testing.T) {
	h := grFifthOutstanding(t)
	grAssertFifthPause(t, h)
	h.age(t)
	h.al.goalQuietWindowSettle(time.Now())
	assert.Equal(t, 1, grHasSystemLine(h.visibleHistory(t), grPauseLine), "repeated paused ticks must not duplicate the pause announcement")
	assert.Equal(t, 5, goalRecordForSession(t, h.id).ZeroOutputPushes)
}

func grBrakeHumanResumes(t *testing.T) {
	h := grFifthOutstanding(t)
	grAssertFifthPause(t, h)
	run := h.human(t, "Continue working on the goal.")
	call := grAwaitCall(t, h.root, run)
	assert.True(t, call.human, "only the actual new human reply authorizes this resumption")
	h.finish(t, run, call)
	h.age(t)
	next := h.reminder(t)
	assert.Equal(t, 0, goalRecordForSession(t, h.id).ZeroOutputPushes, "human resumption starts a fresh consecutive-failure streak, not the spent lifetime cap")
	grTextReminder(t, h, next)
	assert.Equal(t, h.goalID, goalRecordForSession(t, h.id).GoalID, "reply resumes the same goal, not a replacement goal")
}

func grBrakeRestart(t *testing.T) {
	h := grNewRecordedBrake(t)
	for completed := 0; completed < 4; completed++ {
		if completed > 0 {
			h.age(t)
		}
		grTextReminder(t, h, h.reminder(t))
	}
	h.age(t)
	fifth := h.reminder(t)
	require.Equal(t, 4, goalRecordForSession(t, h.id).ZeroOutputPushes, "SETUP: four actual completed reminders must be assessed before the pending fifth")
	// Discard exactly the keeper's process-local maps and reopen the real
	// persisted store while its actually published fifth reminder is pending.
	resetGoalTriggerStateForTest()
	h.al.SetGoalRouteSessionStore()
	reopened, err := goal.NewStore(config.OmnipusHomeDir()).Get(h.goalID)
	require.NoError(t, err)
	require.Equal(t, 4, reopened.ZeroOutputPushes)
	grTextReminder(t, h, fifth)
	h.age(t)
	grAssertFifthPause(t, h)
}

type grActionProvider struct {
	mu       sync.Mutex
	tool     string
	args     map[string]any
	callID   string
	final    string
	calls    int
	toolText string
}

func (*grActionProvider) GetDefaultModel() string { return "reminder-progress-contract-provider" }

func (p *grActionProvider) Chat(_ context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if p.calls == 1 && p.tool != "" {
		return &providers.LLMResponse{FinishReason: "tool_calls", ToolCalls: []providers.ToolCall{{ID: p.callID, Name: p.tool, Arguments: p.args}}}, nil
	}
	for _, m := range messages {
		if m.Role == "tool" && m.ToolCallID == p.callID {
			p.toolText = m.Content
		}
	}
	return &providers.LLMResponse{Content: p.final}, nil
}

func grActionReminder(t *testing.T, h *grHarness, msg bus.InboundMessage, name string, args map[string]any) *grActionProvider {
	t.Helper()
	return grActionRound(t, h, msg, name, args, false)
}

func grActionRound(t *testing.T, h *grHarness, msg bus.InboundMessage, name string, args map[string]any, human bool) *grActionProvider {
	t.Helper()
	inst, ok := h.al.GetRegistry().GetAgent(testDefaultAgentID)
	require.True(t, ok)
	before, err := h.al.GetSessionStore().ReadTranscript(h.id)
	require.NoError(t, err)
	p := &grActionProvider{tool: name, args: args, callID: fmt.Sprintf("brake-action-%d", len(before)), final: "I believe this work is complete."}
	inst.Provider = p
	defer func() { inst.Provider = h.root }()
	if human {
		_, _, err = h.al.processMessage(h.ctx, msg)
	} else {
		_, err = h.al.processSystemMessage(h.ctx, msg)
	}
	require.NoError(t, err)
	if name == "" {
		require.Equal(t, 1, p.calls)
		return p
	}
	require.Equal(t, 2, p.calls, "the real tool must execute before the final response")
	entries, err := h.al.GetSessionStore().ReadTranscript(h.id)
	require.NoError(t, err)
	var actual []session.ToolCall
	for _, entry := range entries {
		for _, call := range entry.ToolCalls {
			if string(call.ID) == p.callID {
				actual = append(actual, call)
			}
		}
	}
	require.Len(t, actual, 1, "SETUP: count a genuinely archived tool execution, never an invented successful row")
	require.Equal(t, name, actual[0].Tool)
	require.Equal(t, "success", actual[0].Status, "SETUP: progress/checklist fixture tool must really succeed: %s", p.toolText)
	assert.Equal(t, args, actual[0].Parameters)
	return p
}

func grReadFixture(t *testing.T) string {
	t.Helper()
	dir, err := workspace.EnsureWorkDir(config.OmnipusHomeDir(), testHarnessWorkspaceMembershipID)
	require.NoError(t, err)
	path := filepath.Join(dir, "reminder-reference.txt")
	require.NoError(t, os.WriteFile(path, []byte("Concrete reference for a real read-only tool call.\n"), 0o600))
	return path
}

func grBrakeToolProgress(t *testing.T) {
	h := grNewRecordedBrake(t)
	path := grReadFixture(t)
	first := h.reminder(t)
	grSetStreak(t, h, 3)
	grActionReminder(t, h, first, "read_file", map[string]any{"path": path})
	h.age(t)
	next := h.reminder(t)
	assert.Equal(t, 0, goalRecordForSession(t, h.id).ZeroOutputPushes, "R6: real non-checklist tool execution resets the entire streak")
	grTextReminder(t, h, next)
}

func grBrakeNoLifetimeCap(t *testing.T) {
	h := grNewRecordedBrake(t)
	path := grReadFixture(t)
	for n := 0; n < 6; n++ { // six > founder's five consecutive-unproductive threshold
		if n > 0 {
			h.age(t)
		}
		msg := h.reminder(t)
		assert.Equal(t, 0, goalRecordForSession(t, h.id).ZeroOutputPushes, "R6: productive reminder %d must reset, not spend a lifetime quota", n+1)
		grActionReminder(t, h, msg, "read_file", map[string]any{"path": path})
	}
	assert.Equal(t, 0, grHasSystemLine(h.visibleHistory(t), grPauseLine), "more than five productive reminders must never announce a no-progress pause")
}

func grBrakeTodosNotProgress(t *testing.T) {
	h := grNewRecordedBrake(t)
	msg := h.reminder(t)
	grSetStreak(t, h, 2)
	grActionReminder(t, h, msg, "set_todos", map[string]any{
		"outcome": "Only rearrange the checklist", "todos": []any{map[string]any{"text": "Do the work later", "active_form": "Completing Do the work later", "status": "pending"}},
	})
	h.age(t)
	next := h.reminder(t)
	assert.Equal(t, 3, goalRecordForSession(t, h.id).ZeroOutputPushes, "R6: a successful set_todos-only reminder remains unproductive; checklist bookkeeping is not work")
	grTextReminder(t, h, next)
}

func grBrakeProseNotProgress(t *testing.T) {
	h := grNewRecordedBrake(t)
	msg := h.reminder(t)
	grSetStreak(t, h, 2)
	grActionReminder(t, h, msg, "", nil)
	h.age(t)
	next := h.reminder(t)
	assert.Equal(t, 3, goalRecordForSession(t, h.id).ZeroOutputPushes, "R6: a text-only completion claim must not reset the streak")
	grTextReminder(t, h, next)
}

func grBrakeFileProgress(t *testing.T) {
	h := grNewRecordedBrake(t)
	dir, err := workspace.EnsureWorkDir(config.OmnipusHomeDir(), testHarnessWorkspaceMembershipID)
	require.NoError(t, err)
	repo, err := gitevidence.Open(dir, gitevidence.WithRedactor(func(text string) string { return text }))
	require.NoError(t, err)
	const filename = "goal-work.txt"
	require.NoError(t, os.WriteFile(filepath.Join(dir, filename), []byte("before\n"), 0o600))
	initial, err := repo.Commit(gitevidence.BoundaryAgent, gitevidence.CommitMeta{AgentID: testDefaultAgentID}, []string{filename})
	require.NoError(t, err)
	require.False(t, initial.Skipped, "SETUP: actual initial evidence commit must exist")
	msg := h.reminder(t) // real keeper establishes the goal's own diff bookmark
	grSetStreak(t, h, 3)
	grTextReminder(t, h, msg)
	require.NoError(t, os.WriteFile(filepath.Join(dir, filename), []byte("after: concrete new goal work\n"), 0o600))
	changed, err := repo.Commit(gitevidence.BoundaryAgent, gitevidence.CommitMeta{AgentID: testDefaultAgentID}, []string{filename})
	require.NoError(t, err)
	require.False(t, changed.Skipped)
	require.NotEqual(t, initial.Hash, changed.Hash)
	h.age(t)
	next := h.reminder(t)
	assert.Equal(t, 0, goalRecordForSession(t, h.id).ZeroOutputPushes, "R6: a real new goal-scoped file diff resets the streak")
	grTextReminder(t, h, next)
}

func grBrakeHelperProgress(t *testing.T) {
	h := grNewRecordedBrake(t)
	grTextReminder(t, h, h.reminder(t)) // establish the real preceding observation, with no stale wake left queued
	// A real human-admitted parent isolates helper activity from R1's missing
	// admission. This parent does no tool work and returns only plain text;
	// the completed child's actual work is the independent progress signal.
	run := h.human(t, "Obtain an independent helper review.")
	parent := grAwaitCall(t, h.root, run)
	child := h.launchReviewer(t, "brake-helper-review")
	require.NotNil(t, child, "SETUP: helper activity must really execute and settle")
	h.finish(t, run, parent)
	grSetStreak(t, h, 3) // isolate the NEXT keeper's observation, not a human-turn reset
	h.age(t)
	next := h.reminder(t)
	assert.Equal(t, 0, goalRecordForSession(t, h.id).ZeroOutputPushes, "R6: real activity in a delegated descendant resets the parent goal's streak")
	grTextReminder(t, h, next)
}

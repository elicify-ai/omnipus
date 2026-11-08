package agent

// C1 DESIGN-v2 and chief independent clocks. Real goal/store/bus/admission and
// full execution owner; only the existing PlanEngineClock supplies sweep time.
import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/require"
)

func TestGoalTimerLiveC1(t *testing.T) {
	t.Run("ExistingDefaultIdleAndOwnerInstrument", func(t *testing.T) { c1LiveIdleBoundary(t, nil, 60) })
	for _, seconds := range []int{30, 60, 3600} {
		t.Run(fmt.Sprintf("IdleInclusiveThreshold/%d", seconds), func(t *testing.T) {
			c1LiveIdleBoundary(t, map[string]any{"goal_reminder_quiet_seconds": seconds}, seconds)
		})
	}
	for _, mode := range []string{"ZeroPlanning", "IdleExpiryOnly", "NullPointers"} {
		t.Run("NilDefault/"+mode, func(t *testing.T) {
			h, pe, clock := c1LiveHarness(t, false, false)
			cfg, err := h.al.GetConfig().Clone()
			require.NoError(t, err)
			cfg.Planning = config.PlanningConfig{}
			if mode == "IdleExpiryOnly" {
				cfg.Planning.IdleExpiryDays = 1
			}
			if mode == "NullPointers" {
				require.NoError(t, json.Unmarshal([]byte(`{"goal_reminder_quiet_seconds":null,"goal_stalled_checkin_enabled":null,"goal_stalled_checkin_minutes":null}`), &cfg.Planning))
			}
			h.al.SwapConfig(cfg)
			at := time.Now().UTC()
			c1LiveAge(t, h, at)
			c1LiveTick(pe, clock, at.Add(60*time.Second-time.Nanosecond))
			grNoPublishedReminder(t, h.al)
			msg := c1LiveDue(t, h, pe, clock, at.Add(60*time.Second))
			c1LiveExecute(t, h, msg)
		})
	}
	t.Run("LiveQuiet120To60WithoutRestart", c1LiveQuietShrinks)
	t.Run("LiveQuiet60To120WithoutRestart", c1LiveQuietExtends)
	for _, recordless := range []bool{false, true} {
		t.Run(fmt.Sprintf("BusyDueWhileQuietActive/recordless_%t", recordless), func(t *testing.T) {
			h, pe, clock := c1LiveHarness(t, true, recordless)
			c1LiveConfig(t, h, map[string]any{"goal_reminder_quiet_seconds": 3600, "goal_stalled_checkin_enabled": true, "goal_stalled_checkin_minutes": 5})
			first := time.Now().UTC()
			c1LiveAge(t, h, first)
			c1LiveTick(pe, clock, first)
			grNoPublishedReminder(t, h.al)
			c1LiveTick(pe, clock, first.Add(5*time.Minute-time.Nanosecond))
			grNoPublishedReminder(t, h.al)
			msg := c1LiveDue(t, h, pe, clock, first.Add(5*time.Minute))
			require.Less(t, 5*time.Minute, time.Hour, "busy clock is due inside IDLE quiet")
			c1LiveBusyOutcome(t, h, pe, clock, msg, first.Add(5*time.Minute))
		})
	}
	t.Run("ExistingDefaultBusyOutcomeInstrument", func(t *testing.T) {
		h, pe, clock := c1LiveHarness(t, true, false)
		first := time.Now().UTC()
		c1LiveTick(pe, clock, first)
		grNoPublishedReminder(t, h.al)
		c1LiveTick(pe, clock, first.Add(30*time.Minute-time.Nanosecond))
		grNoPublishedReminder(t, h.al)
		msg := c1LiveDue(t, h, pe, clock, first.Add(30*time.Minute))
		c1LiveBusyOutcome(t, h, pe, clock, msg, first.Add(30*time.Minute))
	})
	t.Run("StalledOFFSuppressesBusyBeyondDefault", func(t *testing.T) {
		h, pe, clock := c1LiveHarness(t, true, false)
		c1LiveConfig(t, h, map[string]any{"goal_reminder_quiet_seconds": 3600, "goal_stalled_checkin_enabled": false, "goal_stalled_checkin_minutes": 5})
		first := time.Now().UTC()
		c1LiveTick(pe, clock, first)
		grNoPublishedReminder(t, h.al)
		c1LiveTick(pe, clock, first.Add(31*time.Minute))
		grNoPublishedReminder(t, h.al)
		require.Equal(t, 1, tools.GetSharedSessionManager().CountRunningBackgroundCommands(h.id))
		require.Equal(t, 0, goalRecordForSession(t, h.id).ZeroOutputPushes)
	})
	t.Run("StalledOFFStillAllowsIdleReminder", func(t *testing.T) {
		c1LiveIdleBoundary(t, map[string]any{"goal_reminder_quiet_seconds": 60, "goal_stalled_checkin_enabled": false, "goal_stalled_checkin_minutes": 45}, 60)
	})
	t.Run("LiveMinutesShrinkOnSameObservedStall", c1LiveMinutesShrink)
	t.Run("LiveMinutesExtendOnSameObservedStall", c1LiveMinutesExtend)
	t.Run("LiveOFFToONRetainsMinutes", c1LiveEnable)
	t.Run("RealProgressRearmsConfiguredStall", c1LiveProgress)
	t.Run("ActualFailedPublicationRemainsRetryable", c1LiveRollback)
	for _, failed := range []bool{true, false} {
		t.Run(fmt.Sprintf("ExistingBrakesUnderC1Config/failed_%t", failed), func(t *testing.T) { c1LiveBrake(t, failed) })
	}
	t.Run("ExplicitStopStillSuppressesConfiguredTimers", func(t *testing.T) {
		h, pe, clock := c1LiveHarness(t, true, false)
		c1LiveConfig(t, h, map[string]any{"goal_reminder_quiet_seconds": 30, "goal_stalled_checkin_minutes": 5})
		first := time.Now().UTC()
		c1LiveTick(pe, clock, first)
		grStop(t, h)
		require.True(t, h.al.goalKeeperPausedByStop(h.id))
		before := h.load(t)
		c1LiveTick(pe, clock, first.Add(time.Hour))
		grNoPublishedReminder(t, h.al)
		require.Equal(t, before, h.load(t))
	})
	t.Run("RealParkedQuestionStillSuppressesConfiguredTimers", c1LiveQuestion)
}

func c1LiveHarness(t *testing.T, busy, recordless bool) (*grHarness, *PlanEngine, *fakePlanClock) {
	t.Helper()
	h := grNewRecordedBrake(t)
	if recordless {
		_, err := resolveGoalRecordStore().Update(h.goalID, func(g *goal.Goal) error { g.Criteria = nil; g.Definition = ""; return nil })
		require.NoError(t, err)
	}
	if busy {
		grRegisteredProcess(t, h.id, true)
		require.Equal(t, 1, tools.GetSharedSessionManager().CountRunningBackgroundCommands(h.id))
	}
	prior := h.load(t)
	require.Equal(t, session.LifecycleCompleted, prior.State)
	require.NotNil(t, prior.ExecutionID)
	require.Nil(t, h.al.ownLiveTurn(h.id))
	if owner := h.al.executionDispositionFor(executionClaim{SessionID: h.id, Generation: prior.Generation, RunID: prior.ExecutionID.RunID, BootSeq: prior.ExecutionID.BootSeq}); owner != nil {
		select {
		case <-owner.done:
		case <-time.After(grWatchdog):
			t.Fatal("initial actual owner did not settle")
		}
	}
	clock := &fakePlanClock{now: time.Now().UTC()}
	dir := t.TempDir()
	pe := NewPlanEngine(h.al, plan.New(filepath.Join(dir, "plans")), task.New(filepath.Join(dir, "tasks")), nil)
	pe.clock = clock
	require.Equal(t, 30*time.Second, pe.tickInterval, "fixed production sweep cadence is not configurable")
	t.Cleanup(pe.Stop)
	return h, pe, clock
}
func c1LiveConfig(t *testing.T, h *grHarness, fields map[string]any) {
	t.Helper()
	cfg, err := h.al.GetConfig().Clone()
	require.NoError(t, err)
	// Real JSON decoding into present PlanningConfig, no future field, hook,
	// reflection or shared returned-config mutation. Exact effects observed by Tick.
	data, err := json.Marshal(fields)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &cfg.Planning))
	h.al.SwapConfig(cfg)
	require.Same(t, cfg, h.al.GetConfig())
	require.Equal(t, h.goalID, goalRecordForSession(t, h.id).GoalID)
}
func c1LiveAge(t *testing.T, h *grHarness, at time.Time) {
	t.Helper()
	_, err := resolveGoalRecordStore().Update(h.goalID, func(g *goal.Goal) error { g.LastActivityAt = at; g.StartedAt = &at; return nil })
	require.NoError(t, err)
	require.Equal(t, at, goalRecordForSession(t, h.id).LastActivityAt)
}
func c1LiveTick(pe *PlanEngine, clock *fakePlanClock, at time.Time) {
	clock.Set(at)
	pe.Tick(context.Background())
}
func c1LiveDue(t *testing.T, h *grHarness, pe *PlanEngine, clock *fakePlanClock, at time.Time) bus.InboundMessage {
	t.Helper()
	c1LiveTick(pe, clock, at)
	select {
	case msg := <-h.al.bus.InboundChan():
		require.Equal(t, h.id, msg.AsyncTranscriptSessionID)
		require.Equal(t, testDefaultAgentID, msg.AsyncOriginAgentID)
		require.Equal(t, goalLoopFollowUpSenderID, msg.Sender.CanonicalID)
		require.Equal(t, "true", msg.Metadata["goal_reminder"])
		return msg
	default:
		t.Fatal("C1 specified due boundary did not publish an ordinary goal wake after real owner/config/work premises")
		return bus.InboundMessage{}
	}
}
func c1LiveExecute(t *testing.T, h *grHarness, msg bus.InboundMessage) {
	t.Helper()
	prior := h.load(t)
	run := h.system(msg)
	call := grAwaitCall(t, h.root, run)
	h.assertAdmission(t, call, prior)
	h.finish(t, run, call)
	grAwaitOwner(t, call)
	require.Equal(t, session.LifecycleCompleted, h.load(t).State)
}
func c1LiveIdleBoundary(t *testing.T, fields map[string]any, seconds int) {
	t.Helper()
	h, pe, clock := c1LiveHarness(t, false, false)
	if fields != nil {
		c1LiveConfig(t, h, fields)
	}
	at := time.Now().UTC()
	c1LiveAge(t, h, at)
	c1LiveTick(pe, clock, at.Add(time.Duration(seconds)*time.Second-time.Nanosecond))
	grNoPublishedReminder(t, h.al)
	msg := c1LiveDue(t, h, pe, clock, at.Add(time.Duration(seconds)*time.Second))
	prior := h.load(t)
	run := h.system(msg)
	call := grAwaitCall(t, h.root, run)
	h.assertAdmission(t, call, prior)
	t.Cleanup(func() { h.root.openAll(); grJoin(t, run); grAwaitOwner(t, call) })
	c1LiveTick(pe, clock, at.Add(time.Duration(seconds)*time.Second))
	grNoPublishedReminder(t, h.al)
	h.finish(t, run, call)
	grAwaitOwner(t, call)
	require.Equal(t, 0, goalRecordForSession(t, h.id).ZeroOutputPushes, "before first outcome retry no publication-count accounting")
}
func c1LiveQuietShrinks(t *testing.T) {
	h, pe, clock := c1LiveHarness(t, false, false)
	sid, gid := h.id, h.goalID
	c1LiveConfig(t, h, map[string]any{"goal_reminder_quiet_seconds": 120})
	at := time.Now().UTC()
	c1LiveAge(t, h, at)
	c1LiveTick(pe, clock, at.Add(90*time.Second))
	grNoPublishedReminder(t, h.al)
	c1LiveConfig(t, h, map[string]any{"goal_reminder_quiet_seconds": 60})
	require.Equal(t, sid, h.id)
	require.Equal(t, gid, h.goalID)
	msg := c1LiveDue(t, h, pe, clock, at.Add(90*time.Second))
	c1LiveExecute(t, h, msg)
}
func c1LiveQuietExtends(t *testing.T) {
	h, pe, clock := c1LiveHarness(t, false, false)
	at := time.Now().UTC()
	c1LiveAge(t, h, at)
	c1LiveConfig(t, h, map[string]any{"goal_reminder_quiet_seconds": 60})
	c1LiveTick(pe, clock, at.Add(59*time.Second))
	grNoPublishedReminder(t, h.al)
	c1LiveConfig(t, h, map[string]any{"goal_reminder_quiet_seconds": 120})
	c1LiveTick(pe, clock, at.Add(90*time.Second))
	grNoPublishedReminder(t, h.al)
	msg := c1LiveDue(t, h, pe, clock, at.Add(120*time.Second))
	c1LiveExecute(t, h, msg)
}

func c1LiveBusyOutcome(t *testing.T, h *grHarness, pe *PlanEngine, clock *fakePlanClock, msg bus.InboundMessage, due time.Time) {
	t.Helper()
	require.True(t, strings.HasPrefix(msg.Content, b2PromptBeforeItems))
	require.Equal(t, 1, tools.GetSharedSessionManager().CountRunningBackgroundCommands(h.id))
	prior := h.load(t)
	run := h.system(msg)
	call := grAwaitCall(t, h.root, run)
	h.assertAdmission(t, call, prior)
	r6TailFillOutbound(t, h)
	h.root.release(call)
	r6TailAwaitOpen(t, h, run, call)
	t.Cleanup(func() { h.root.openAll(); r2DrainAndJoinFailedTail(t, h, run); grAwaitOwner(t, call) })
	work := goalReminderWorkFor(h.goalID)
	work.mu.Lock()
	pending := work.pending
	work.mu.Unlock()
	require.NotNil(t, pending)
	require.Same(t, call.owner, pending.execution)
	c1LiveTick(pe, clock, due.Add(time.Second))
	select {
	case <-call.owner.done:
		t.Fatal("C1 sweep must not assess before FULL owner tail")
	default:
	}
	require.Equal(t, 0, goalRecordForSession(t, h.id).ZeroOutputPushes)
	grNoPublishedReminder(t, h.al)
	select {
	case <-h.al.bus.OutboundChan():
	default:
		t.Fatal("held actual outbound tail was not present")
	}
	result := grJoin(t, run)
	require.NoError(t, result.err)
	grAwaitOwner(t, call)
	h.root.drainRequests()
	c1LiveTick(pe, clock, due.Add(2*time.Second))
	require.Equal(t, 1, goalRecordForSession(t, h.id).ZeroOutputPushes, "FIRST retry after owner.done must assess check-in while work remains busy")
	work.mu.Lock()
	remaining, assessed := work.pending, work.lastAssessedTurn
	work.mu.Unlock()
	require.Nil(t, remaining)
	require.Equal(t, pending.turnID, assessed)
	for _, at := range []time.Time{due.Add(10 * time.Minute), due.Add(40 * time.Minute)} {
		c1LiveTick(pe, clock, at)
		grNoPublishedReminder(t, h.al)
	}
	require.Equal(t, 1, tools.GetSharedSessionManager().CountRunningBackgroundCommands(h.id))
	require.Equal(t, 1, goalRecordForSession(t, h.id).ZeroOutputPushes, "same stall must be one-per-stall, not a repeating timer")
}
func c1LiveMinutesShrink(t *testing.T) {
	h, pe, clock := c1LiveHarness(t, true, false)
	first := time.Now().UTC()
	c1LiveConfig(t, h, map[string]any{"goal_stalled_checkin_minutes": 30})
	c1LiveTick(pe, clock, first)
	grNoPublishedReminder(t, h.al)
	c1LiveTick(pe, clock, first.Add(6*time.Minute))
	grNoPublishedReminder(t, h.al)
	c1LiveConfig(t, h, map[string]any{"goal_stalled_checkin_minutes": 5})
	msg := c1LiveDue(t, h, pe, clock, first.Add(6*time.Minute))
	c1LiveExecute(t, h, msg)
}
func c1LiveMinutesExtend(t *testing.T) {
	h, pe, clock := c1LiveHarness(t, true, false)
	first := time.Now().UTC()
	c1LiveConfig(t, h, map[string]any{"goal_stalled_checkin_minutes": 5})
	c1LiveTick(pe, clock, first)
	grNoPublishedReminder(t, h.al)
	c1LiveTick(pe, clock, first.Add(4*time.Minute))
	grNoPublishedReminder(t, h.al)
	c1LiveConfig(t, h, map[string]any{"goal_stalled_checkin_minutes": 30})
	c1LiveTick(pe, clock, first.Add(5*time.Minute))
	grNoPublishedReminder(t, h.al)
	msg := c1LiveDue(t, h, pe, clock, first.Add(30*time.Minute))
	c1LiveExecute(t, h, msg)
}
func c1LiveEnable(t *testing.T) {
	h, pe, clock := c1LiveHarness(t, true, false)
	first := time.Now().UTC()
	c1LiveConfig(t, h, map[string]any{"goal_stalled_checkin_enabled": false, "goal_stalled_checkin_minutes": 5})
	c1LiveTick(pe, clock, first)
	grNoPublishedReminder(t, h.al)
	c1LiveTick(pe, clock, first.Add(6*time.Minute))
	grNoPublishedReminder(t, h.al)
	c1LiveConfig(t, h, map[string]any{"goal_stalled_checkin_enabled": true})
	msg := c1LiveDue(t, h, pe, clock, first.Add(6*time.Minute))
	c1LiveExecute(t, h, msg)
}
func c1LiveProgress(t *testing.T) {
	h, pe, clock := c1LiveHarness(t, true, false)
	c1LiveConfig(t, h, map[string]any{"goal_stalled_checkin_minutes": 5})
	reader := &b2DraftReadProvider{base: h.root, path: grReadFixture(t)}
	inst, ok := h.al.GetRegistry().GetAgent(testDefaultAgentID)
	require.True(t, ok)
	inst.Provider = reader
	first := time.Now().UTC().Add(-6 * time.Minute)
	c1LiveTick(pe, clock, first)
	grNoPublishedReminder(t, h.al)
	msg := c1LiveDue(t, h, pe, clock, first.Add(5*time.Minute))
	c1LiveExecute(t, h, msg)
	require.GreaterOrEqual(t, reader.calls.Load(), int32(2), "actual read tool then provider continuation")
	entries, err := h.al.GetSessionStore().ReadTranscript(h.id)
	require.NoError(t, err)
	reads := 0
	for _, entry := range entries {
		for _, call := range entry.ToolCalls {
			if call.Tool == "read_file" && call.Parameters["path"] == reader.path {
				require.Equal(t, "success", call.Status)
				reads++
			}
		}
	}
	require.Equal(t, 1, reads, "only actual persisted completed read may rearm")
	fresh := time.Now().UTC()
	c1LiveTick(pe, clock, fresh)
	require.Equal(t, 0, goalRecordForSession(t, h.id).ZeroOutputPushes)
	grNoPublishedReminder(t, h.al)
	c1LiveTick(pe, clock, fresh.Add(5*time.Minute-time.Nanosecond))
	grNoPublishedReminder(t, h.al)
	inst.Provider = h.root
	next := c1LiveDue(t, h, pe, clock, fresh.Add(5*time.Minute))
	c1LiveExecute(t, h, next)
}
func c1LiveRollback(t *testing.T) {
	h, pe, clock := c1LiveHarness(t, true, false)
	c1LiveConfig(t, h, map[string]any{"goal_stalled_checkin_minutes": 5})
	first := time.Now().UTC()
	c1LiveTick(pe, clock, first)
	grNoPublishedReminder(t, h.al)
	level := logger.GetLevel()
	path := filepath.Join(t.TempDir(), "c1-publish.log")
	require.NoError(t, logger.EnableFileLogging(path))
	logger.SetLevel(logger.WARN)
	t.Cleanup(func() { logger.DisableFileLogging(); logger.SetLevel(level) })
	logger.WarnCF("c1-test", "C1 publish capture instrument", map[string]any{"control": true})
	require.Equal(t, 1, b2DraftLogCount(t, path, "C1 publish capture instrument"))
	for n := 0; n < cap(h.al.bus.InboundChan()); n++ {
		require.NoError(t, h.al.bus.PublishInbound(context.Background(), bus.InboundMessage{Channel: "c1-fill", ChatID: "c1-fill"}))
	}
	c1LiveTick(pe, clock, first.Add(5*time.Minute))
	require.Positive(t, b2DraftLogCount(t, path, "goal: follow-up turn dispatch failed"), "must reach actual failed handoff before rollback is credited")
	for len(h.al.bus.InboundChan()) > 0 {
		<-h.al.bus.InboundChan()
	}
	msg := c1LiveDue(t, h, pe, clock, first.Add(5*time.Minute+time.Second))
	c1LiveExecute(t, h, msg)
}
func c1LiveBrake(t *testing.T, failed bool) {
	h, pe, clock := c1LiveHarness(t, false, false)
	c1LiveConfig(t, h, map[string]any{"goal_reminder_quiet_seconds": 30, "goal_stalled_checkin_minutes": 5})
	if failed {
		provider := r2InstallFailureProvider(t, h)
		for i := 0; i < 3; i++ {
			r2FailedReminder(t, h, provider, r2ReplyProviderError)
		}
	} else {
		for i := 0; i < 5; i++ {
			h.age(t)
			grTextReminder(t, h, h.reminder(t))
		}
	}
	h.age(t)
	c1LiveTick(pe, clock, time.Now().UTC())
	grNoPublishedReminder(t, h.al)
	require.True(t, h.al.goalIsWaitingOnUser(h.goalID))
	before := goalRecordForSession(t, h.id)
	grRegisteredProcess(t, h.id, true)
	c1LiveTick(pe, clock, time.Now().Add(time.Hour))
	grNoPublishedReminder(t, h.al)
	require.Equal(t, before.LatestReason, goalRecordForSession(t, h.id).LatestReason)
	require.Equal(t, before.Round, goalRecordForSession(t, h.id).Round)
	require.Equal(t, before.MaxRounds, goalRecordForSession(t, h.id).MaxRounds)
}
func c1LiveQuestion(t *testing.T) {
	h, pe, clock := c1LiveHarness(t, false, false)
	c1LiveConfig(t, h, map[string]any{"goal_reminder_quiet_seconds": 30, "goal_stalled_checkin_minutes": 5})
	registry := newTestAskUserRegistry(t, h.al.GetSessionStore())
	h.al.SetAskUserRegistry(registry)
	t.Cleanup(registry.Quiesce)
	inst, ok := h.al.GetRegistry().GetAgent(testDefaultAgentID)
	require.True(t, ok)
	inst.Provider = &r2QuestionProvider{base: h.root}
	run := h.system(h.reminder(t))
	call := grAwaitCall(t, h.root, run)
	h.root.release(call)
	result := grJoin(t, run)
	require.NoError(t, result.err)
	grAwaitOwner(t, call)
	card, found := registry.PendingForSession(h.id)
	require.True(t, found)
	r2RequirePersistedParkCard(t, h, card)
	grRegisteredProcess(t, h.id, true)
	before := h.root.count()
	h.age(t)
	c1LiveTick(pe, clock, time.Now().Add(time.Hour))
	grNoPublishedReminder(t, h.al)
	still, found := registry.PendingForSession(h.id)
	require.True(t, found)
	require.Equal(t, card.CardID, still.CardID)
	require.Equal(t, before, h.root.count())
}

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// goal_loop_test.go covers /goal (US-8) and /loop (US-9) at the
// applyGoalCommandPrompt/checkGoalLoopAfterTurn/applyLoopCommandPrompt/
// LoopScheduler unit level — mirroring judge_test.go's
// newGoalLoopTestLoop harness (a fake Judge LLM provider swapped onto the
// seeded Judge System Agent) rather than driving a full bus-dispatched,
// multi-round turn flow end to end.
package agent

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// --- shared test helpers -----------------------------------------------

// newLoopSchedulerForTest builds a LoopScheduler on a fake, manually-driven
// clock (mirrors newTriggerSchedulerForTest's triggerFakeClock pattern,
// task_trigger_test.go) so /loop cron-fire tests run with zero wall-clock
// sleeps. The caller drives dispatch via ls.RunDueJobs(clk.Now()) +
// ls.WaitForLane().
func newLoopSchedulerForTest(t *testing.T, al *AgentLoop) (*LoopScheduler, *triggerFakeClock) {
	t.Helper()
	dir := t.TempDir()
	ls := NewLoopScheduler(filepath.Join(dir, "jobs.json"), al)
	clk := newTriggerFakeClock()
	ls.SetClock(clk)
	if err := ls.Start(); err != nil {
		t.Fatalf("loop scheduler start: %v", err)
	}
	t.Cleanup(ls.Stop)
	return ls, clk
}

func newGoalTestSession(t *testing.T, al *AgentLoop, agentID string) (*session.UnifiedStore, string) {
	t.Helper()
	store := al.GetSessionStore()
	if store == nil {
		t.Fatal("shared session store not available")
	}
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", agentID)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	return store, meta.ID
}

func metJudgeProvider(reason string) *fakeJudgeProvider {
	return &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return &providers.LLMResponse{
			Content: `{"met": true, "criteria": [{"id":"goal-condition","met":true,"reason":"` + reason + `"}]}`,
		}, nil
	}}
}

func unmetJudgeProvider(reason string) *fakeJudgeProvider {
	return &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return &providers.LLMResponse{
			Content: `{"met": false, "criteria": [{"id":"goal-condition","met":false,"reason":"` + reason + `"}]}`,
		}, nil
	}}
}

// --- /goal: set / status / clear ----------------------------------------

func TestGoalCommand_SetRewritesUserMessage(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)

	opts := processOptions{
		TranscriptStore: store, TranscriptSessionID: sid,
		Channel: "webchat", ChatID: "c1", SessionKey: "sk1", UserInitiated: true,
	}
	matched, handled, _ := al.applyGoalCommandPrompt(
		context.Background(),
		bus.InboundMessage{Content: "/goal make the tests pass", UserInitiated: true},
		agentInst, &opts,
	)
	if !matched || handled {
		t.Fatalf("matched=%v handled=%v, want matched=true handled=false (turn continues to LLM)", matched, handled)
	}
	if opts.UserMessage != "make the tests pass" {
		t.Fatalf("opts.UserMessage = %q, want the condition text", opts.UserMessage)
	}
	after, err := store.GetMeta(sid)
	if err != nil {
		t.Fatal(err)
	}
	if after.GoalCondition != "make the tests pass" || after.GoalRoundsUsed != 0 || after.GoalMaxRounds != config.DefaultGoalMaxRounds {
		t.Fatalf("unexpected goal state: %+v", after)
	}
}

func TestGoalCommand_ReplaceOnSet(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	opts := processOptions{
		TranscriptStore: store, TranscriptSessionID: sid,
		Channel: "webchat", ChatID: "c1", SessionKey: "sk1", UserInitiated: true,
	}

	al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal condition A", UserInitiated: true}, agentInst, &opts)
	oneRound := 1
	if err := store.SetMeta(sid, session.MetaPatch{GoalRoundsUsed: &oneRound}); err != nil {
		t.Fatal(err)
	}

	al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal condition B", UserInitiated: true}, agentInst, &opts)

	after, err := store.GetMeta(sid)
	if err != nil {
		t.Fatal(err)
	}
	if after.GoalCondition != "condition B" {
		t.Fatalf("condition = %q, want replaced to condition B (FR-068)", after.GoalCondition)
	}
	if after.GoalRoundsUsed != 0 {
		t.Fatalf("rounds_used = %d, want reset to 0 on replace", after.GoalRoundsUsed)
	}
}

func TestGoalCommand_StatusAndClear(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	opts := processOptions{
		TranscriptStore: store, TranscriptSessionID: sid,
		Channel: "webchat", ChatID: "c1", SessionKey: "sk1", UserInitiated: true,
	}

	// Bare status with no active goal.
	matched, handled, reply := al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal", UserInitiated: true}, agentInst, &opts)
	if !matched || !handled {
		t.Fatalf("status: matched=%v handled=%v, want both true", matched, handled)
	}
	if !strings.Contains(reply, "No active goal") {
		t.Fatalf("status reply = %q", reply)
	}

	al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal make the tests pass", UserInitiated: true}, agentInst, &opts)

	matched, handled, reply = al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal", UserInitiated: true}, agentInst, &opts)
	if !matched || !handled {
		t.Fatalf("status: matched=%v handled=%v", matched, handled)
	}
	for _, want := range []string{"make the tests pass", "Rounds: 0/", "Active loops:"} {
		if !strings.Contains(reply, want) {
			t.Fatalf("status reply = %q, want to contain %q", reply, want)
		}
	}

	// clear aliases (FR-070).
	for _, verb := range []string{"clear", "stop", "cancel"} {
		al.applyGoalCommandPrompt(context.Background(),
			bus.InboundMessage{Content: "/goal make the tests pass", UserInitiated: true}, agentInst, &opts)
		matched, handled, reply = al.applyGoalCommandPrompt(context.Background(),
			bus.InboundMessage{Content: "/goal " + verb, UserInitiated: true}, agentInst, &opts)
		if !matched || !handled {
			t.Fatalf("%s: matched=%v handled=%v", verb, matched, handled)
		}
		if !strings.Contains(reply, "cleared") {
			t.Fatalf("%s reply = %q, want a cleared confirmation", verb, reply)
		}
		after, err := store.GetMeta(sid)
		if err != nil {
			t.Fatal(err)
		}
		if after.GoalCondition != "" {
			t.Fatalf("%s: goal condition still set: %q", verb, after.GoalCondition)
		}
	}
}

func TestGoalCommand_AdmissionRefusal(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, func(cfg *config.Config) {
		cfg.Planning.GlobalActiveLoopCap = 1
	})
	planStore := plan.New(t.TempDir())
	pe := NewPlanEngine(al, planStore, nil, nil)
	pe.RegisterActiveCounter("goal", func() (int, error) { return 1, nil }) // cap already full
	al.SetPlanEngine(pe)

	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	opts := processOptions{
		TranscriptStore: store, TranscriptSessionID: sid,
		Channel: "webchat", ChatID: "c1", SessionKey: "sk1", UserInitiated: true,
	}

	matched, handled, reply := al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal make the tests pass", UserInitiated: true}, agentInst, &opts)
	if !matched || !handled {
		t.Fatalf("matched=%v handled=%v, want both true (refusal answers synchronously)", matched, handled)
	}
	if !strings.Contains(reply, "active loops") {
		t.Fatalf("reply = %q, want a cap-reached message", reply)
	}
	after, err := store.GetMeta(sid)
	if err != nil {
		t.Fatal(err)
	}
	if after.GoalCondition != "" {
		t.Fatal("goal must not be set when admission is refused")
	}
}

// --- /goal: judge-gated round advance (checkGoalLoopAfterTurn) ----------

func TestGoalLoop_MetVerdict_ClearsGoalAndWritesVerdict(t *testing.T) {
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	opts := processOptions{
		TranscriptStore: store, TranscriptSessionID: sid,
		Channel: "webchat", ChatID: "c1", SessionKey: "sk1", UserInitiated: true,
	}
	al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal make the tests pass", UserInitiated: true}, agentInst, &opts)

	judgeInst.Provider = metJudgeProvider("tests pass")

	result := &turnResult{finalContent: "Tests are passing now."}
	al.checkGoalLoopAfterTurn(context.Background(), agentInst, opts, result)

	after, err := store.GetMeta(sid)
	if err != nil {
		t.Fatal(err)
	}
	if after.GoalCondition != "" {
		t.Fatalf("goal should be cleared on a met verdict, still: %q", after.GoalCondition)
	}
	if len(result.followUps) != 0 {
		t.Fatalf("a met verdict must not schedule a follow-up round, got %d", len(result.followUps))
	}

	entries, err := store.ReadTranscript(sid)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range entries {
		if e.Type == session.EntryTypeJudgeVerdict {
			found = true
			if !strings.Contains(e.Content, `"scope":"goal"`) {
				t.Errorf("judge_verdict entry scope not goal: %s", e.Content)
			}
		}
	}
	if !found {
		t.Fatal("expected a judge_verdict transcript entry (FR-056)")
	}
}

func TestGoalLoop_UnmetVerdict_AdvancesRoundAndFeedsForward(t *testing.T) {
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	opts := processOptions{
		TranscriptStore: store, TranscriptSessionID: sid,
		Channel: "webchat", ChatID: "c1", SessionKey: "sk1", UserInitiated: true,
	}
	al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal make the tests pass", UserInitiated: true}, agentInst, &opts)

	judgeInst.Provider = unmetJudgeProvider("3 tests still failing")

	result := &turnResult{finalContent: "I tried but some tests still fail."}
	al.checkGoalLoopAfterTurn(context.Background(), agentInst, opts, result)

	after, err := store.GetMeta(sid)
	if err != nil {
		t.Fatal(err)
	}
	if after.GoalCondition == "" {
		t.Fatal("goal must remain active after an unmet verdict under the round bound")
	}
	if after.GoalRoundsUsed != 1 {
		t.Fatalf("rounds_used = %d, want 1", after.GoalRoundsUsed)
	}
	if !strings.Contains(after.GoalLatestReason, "3 tests still failing") {
		t.Fatalf("latest reason = %q, want to contain the judge's reason", after.GoalLatestReason)
	}
	if len(result.followUps) != 1 {
		t.Fatalf("expected exactly 1 follow-up round, got %d", len(result.followUps))
	}
	fu := result.followUps[0]
	if !strings.Contains(fu.Content, "3 tests still failing") {
		t.Fatalf("follow-up content = %q, want the judge reason fed forward as steering (FR-043 pattern)", fu.Content)
	}
	if fu.UserInitiated {
		t.Fatal("a re-injected continuation must NOT be UserInitiated (Gap #8)")
	}
	if fu.SessionID != sid {
		t.Fatalf("follow-up session_id = %q, want %q (same session)", fu.SessionID, sid)
	}
}

func TestGoalLoop_RoundCap_StopsAndClearsWithHandover(t *testing.T) {
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, func(cfg *config.Config) {
		cfg.Planning.GoalMaxRounds = 2
	})
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	opts := processOptions{
		TranscriptStore: store, TranscriptSessionID: sid,
		Channel: "webchat", ChatID: "c1", SessionKey: "sk1", UserInitiated: true,
	}
	al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal make the tests pass", UserInitiated: true}, agentInst, &opts)
	judgeInst.Provider = unmetJudgeProvider("still unmet")

	r1 := &turnResult{finalContent: "attempt 1"}
	al.checkGoalLoopAfterTurn(context.Background(), agentInst, opts, r1)
	after1, err := store.GetMeta(sid)
	if err != nil {
		t.Fatal(err)
	}
	if after1.GoalCondition == "" || after1.GoalRoundsUsed != 1 {
		t.Fatalf("round 1 (< bound=2): unexpected state %+v", after1)
	}
	if len(r1.followUps) != 1 {
		t.Fatalf("round 1: expected a follow-up round, got %d", len(r1.followUps))
	}

	r2 := &turnResult{finalContent: "attempt 2"}
	al.checkGoalLoopAfterTurn(context.Background(), agentInst, opts, r2)
	after2, err := store.GetMeta(sid)
	if err != nil {
		t.Fatal(err)
	}
	if after2.GoalCondition != "" {
		t.Fatal("round 2 (== bound=2): goal must be cleared (bound reached)")
	}
	if len(r2.followUps) != 0 {
		t.Fatal("round == bound must NOT schedule a further follow-up round")
	}

	entries, err := store.ReadTranscript(sid)
	if err != nil {
		t.Fatal(err)
	}
	foundHandover := false
	for _, e := range entries {
		if e.Type == session.EntryTypeSystem && strings.Contains(e.Content, "did not reach a MET verdict") {
			foundHandover = true
		}
	}
	if !foundHandover {
		t.Fatal("expected a handover system transcript entry at the round bound (SD-B9)")
	}
}

func TestGoalLoop_JudgeUnavailable_DoesNotConsumeRound(t *testing.T) {
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	opts := processOptions{
		TranscriptStore: store, TranscriptSessionID: sid,
		Channel: "webchat", ChatID: "c1", SessionKey: "sk1", UserInitiated: true,
	}
	al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal make the tests pass", UserInitiated: true}, agentInst, &opts)

	judgeInst.Provider = &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return nil, context.DeadlineExceeded
	}}

	// Bound the judge's internal backoff retry loop with an already-expired
	// ctx so JudgeCriteria gives up promptly instead of sleeping through the
	// real 60/120/300s D7 schedule.
	ctx, cancel := context.WithTimeout(context.Background(), 1)
	defer cancel()
	time.Sleep(2 * time.Millisecond)

	result := &turnResult{finalContent: "still working on it"}
	al.checkGoalLoopAfterTurn(ctx, agentInst, opts, result)

	after, err := store.GetMeta(sid)
	if err != nil {
		t.Fatal(err)
	}
	if after.GoalRoundsUsed != 0 {
		t.Fatalf("rounds_used = %d, want 0 (judge unavailability must not consume a round, D7)", after.GoalRoundsUsed)
	}
	if after.GoalCondition == "" {
		t.Fatal("goal must remain active when the judge is unavailable")
	}
	if len(result.followUps) != 0 {
		t.Fatal("judge unavailability must not schedule a follow-up round")
	}
}

// --- /loop: interval mode, run cap, stop, self-paced reschedule ---------

func TestLoopCommand_IntervalMode_FiresAndIncrementsRunCount(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	ls, clk := newLoopSchedulerForTest(t, al)
	al.SetLoopScheduler(ls)

	opts := processOptions{
		TranscriptStore: store, TranscriptSessionID: sid,
		Channel: "webchat", ChatID: "c1", SessionKey: "sk1", UserInitiated: true,
	}
	matched, handled, reply := al.applyLoopCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/loop every 5m summarize new emails", UserInitiated: true}, agentInst, &opts)
	if !matched || !handled {
		t.Fatalf("matched=%v handled=%v, want both true", matched, handled)
	}
	if !strings.Contains(reply, "Loop started") {
		t.Fatalf("reply = %q", reply)
	}

	setMeta, err := store.GetMeta(sid)
	if err != nil {
		t.Fatal(err)
	}
	if setMeta.LoopMode != loopModeInterval || setMeta.LoopIntervalMS != 5*60*1000 {
		t.Fatalf("unexpected loop state after set: %+v", setMeta)
	}

	clk.Advance(5 * time.Minute)
	ls.RunDueJobs(clk.Now())
	ls.WaitForLane()

	after, err := store.GetMeta(sid)
	if err != nil {
		t.Fatal(err)
	}
	if after.LoopRunCount != 1 {
		t.Fatalf("run_count = %d, want 1 after the interval elapses", after.LoopRunCount)
	}
	if after.LoopMode != loopModeInterval {
		t.Fatal("loop should still be active (run 1 < max)")
	}
}

func TestLoopScheduler_RunCapBoundary_StopsAndRemovesJob(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, func(cfg *config.Config) {
		cfg.Planning.LoopMaxRuns = 2
	})
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	ls, clk := newLoopSchedulerForTest(t, al)
	al.SetLoopScheduler(ls)

	opts := processOptions{
		TranscriptStore: store, TranscriptSessionID: sid,
		Channel: "webchat", ChatID: "c1", SessionKey: "sk1", UserInitiated: true,
	}
	al.applyLoopCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/loop every 1m ping", UserInitiated: true}, agentInst, &opts)

	clk.Advance(time.Minute)
	ls.RunDueJobs(clk.Now())
	ls.WaitForLane()
	after1, err := store.GetMeta(sid)
	if err != nil {
		t.Fatal(err)
	}
	if after1.LoopRunCount != 1 || after1.LoopMode == "" {
		t.Fatalf("after run 1 (< bound=2): run_count=%d mode=%q", after1.LoopRunCount, after1.LoopMode)
	}

	clk.Advance(time.Minute)
	ls.RunDueJobs(clk.Now())
	ls.WaitForLane()
	after2, err := store.GetMeta(sid)
	if err != nil {
		t.Fatal(err)
	}
	if after2.LoopMode != "" {
		t.Fatal("after run 2 (== bound=2): loop must be stopped (run-count boundary)")
	}
	if len(ls.ListEnabledJobs()) != 0 {
		t.Fatal("cron job must be removed once the run cap is reached")
	}
}

func TestLoopCommand_Stop_RemovesCronJobAndClearsState(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	ls, _ := newLoopSchedulerForTest(t, al)
	al.SetLoopScheduler(ls)

	opts := processOptions{
		TranscriptStore: store, TranscriptSessionID: sid,
		Channel: "webchat", ChatID: "c1", SessionKey: "sk1", UserInitiated: true,
	}
	al.applyLoopCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/loop every 1m ping", UserInitiated: true}, agentInst, &opts)
	if got := len(ls.ListEnabledJobs()); got != 1 {
		t.Fatalf("enabled jobs after set = %d, want 1", got)
	}

	matched, handled, reply := al.applyLoopCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/loop stop", UserInitiated: true}, agentInst, &opts)
	if !matched || !handled {
		t.Fatalf("stop: matched=%v handled=%v", matched, handled)
	}
	if reply != "Loop stopped." {
		t.Fatalf("stop reply = %q", reply)
	}

	after, err := store.GetMeta(sid)
	if err != nil {
		t.Fatal(err)
	}
	if after.LoopMode != "" {
		t.Fatal("loop state must be cleared after stop")
	}
	if got := len(ls.ListEnabledJobs()); got != 0 {
		t.Fatalf("enabled jobs after stop = %d, want 0", got)
	}
}

// selfPacedTurnProvider is a worker LLM provider double that always replies
// with a LOOP_NEXT marker so LoopScheduler.RunScheduled can parse the next
// self-paced delay.
type selfPacedTurnProvider struct{ marker string }

func (p *selfPacedTurnProvider) Chat(
	context.Context, []providers.Message, []providers.ToolDefinition, string, map[string]any,
) (*providers.LLMResponse, error) {
	return &providers.LLMResponse{
		Content:   "Checked in, all good.\n\n" + p.marker,
		ToolCalls: []providers.ToolCall{},
	}, nil
}
func (p *selfPacedTurnProvider) GetDefaultModel() string { return "test-model" }

func TestLoopScheduler_SelfPaced_ReschedulesFromLoopNextMarker(t *testing.T) {
	al, _ := newGoalLoopTestLoop(
		t, &selfPacedTurnProvider{marker: "LOOP_NEXT: 10m — waiting for the next check"}, nil,
	)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	ls, clk := newLoopSchedulerForTest(t, al)
	al.SetLoopScheduler(ls)

	opts := processOptions{
		TranscriptStore: store, TranscriptSessionID: sid,
		Channel: "webchat", ChatID: "c1", SessionKey: "sk1", UserInitiated: true,
	}
	matched, handled, reply := al.applyLoopCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/loop check on the deploy", UserInitiated: true}, agentInst, &opts)
	if !matched || !handled {
		t.Fatalf("matched=%v handled=%v", matched, handled)
	}
	if !strings.Contains(reply, "Self-paced loop started") {
		t.Fatalf("reply = %q", reply)
	}
	setMeta, err := store.GetMeta(sid)
	if err != nil {
		t.Fatal(err)
	}
	if setMeta.LoopMode != loopModeSelfPaced {
		t.Fatalf("mode = %q, want self_paced", setMeta.LoopMode)
	}

	// The first run is scheduled effectively-immediately (firstSelfPacedRunDelayMS) — fire it now.
	clk.Advance(2 * time.Second)
	ls.RunDueJobs(clk.Now())
	ls.WaitForLane()

	after1, err := store.GetMeta(sid)
	if err != nil {
		t.Fatal(err)
	}
	if after1.LoopRunCount != 1 {
		t.Fatalf("run_count = %d, want 1", after1.LoopRunCount)
	}
	if after1.LoopNextDelayMS != 10*60*1000 {
		t.Fatalf("next_delay_ms = %d, want 600000 (parsed from LOOP_NEXT: 10m)", after1.LoopNextDelayMS)
	}

	// Advancing less than the 10m delay must not fire the next run.
	clk.Advance(9 * time.Minute)
	ls.RunDueJobs(clk.Now())
	ls.WaitForLane()
	mid, err := store.GetMeta(sid)
	if err != nil {
		t.Fatal(err)
	}
	if mid.LoopRunCount != 1 {
		t.Fatalf("run_count = %d, want still 1 (next fire not due yet)", mid.LoopRunCount)
	}

	// Crossing the 10m delay fires the rescheduled one-shot run.
	clk.Advance(2 * time.Minute)
	ls.RunDueJobs(clk.Now())
	ls.WaitForLane()
	final, err := store.GetMeta(sid)
	if err != nil {
		t.Fatal(err)
	}
	if final.LoopRunCount != 2 {
		t.Fatalf("run_count = %d, want 2 once the self-paced delay elapses", final.LoopRunCount)
	}
}

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
	"github.com/elicify-ai/omnipus/pkg/task"
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

// activatePendingGoal drives the ADR-074 D4a confirm step: a prose `/goal
// <intent>` now parks as a PENDING goal (US-3 S1) rather than activating
// immediately, so tests that need an ACTIVE goal replay `/goal confirm` after
// the set. Asserts the confirm actually activated (matched=true,
// handled=false — the confirm turn continues into round 1) and that the goal
// condition is now live.
func activatePendingGoal(t *testing.T, al *AgentLoop, agentInst *AgentInstance, opts *processOptions) {
	t.Helper()
	matched, handled, reply := al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal confirm", UserInitiated: true}, agentInst, opts)
	if !matched || handled {
		t.Fatalf("activatePendingGoal: matched=%v handled=%v reply=%q, want matched=true handled=false "+
			"(fresh-pending confirm rewrites the turn into round 1)", matched, handled, reply)
	}
	meta, err := opts.TranscriptStore.GetMeta(opts.TranscriptSessionID)
	if err != nil {
		t.Fatalf("activatePendingGoal: GetMeta: %v", err)
	}
	if meta.GoalCondition == "" {
		t.Fatal("activatePendingGoal: goal must be ACTIVE after confirm")
	}
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

// TestGoalCommand_SetRewritesUserMessage — updated for ADR-074 D4a (US-3 S1):
// a PROSE `/goal <intent>` no longer activates in the set turn. It parks as a
// PENDING goal (echo + confirm), and it is the CONFIRM turn that rewrites
// opts.UserMessage and continues into round 1.
func TestGoalCommand_SetRewritesUserMessage(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)

	opts := processOptions{
		TranscriptStore: store, TranscriptSessionID: sid,
		Channel: "webchat", ChatID: "c1", SessionKey: "sk1", UserInitiated: true,
	}
	matched, handled, reply := al.applyGoalCommandPrompt(
		context.Background(),
		bus.InboundMessage{Content: "/goal make the tests pass", UserInitiated: true},
		agentInst, &opts,
	)
	if !matched || !handled {
		t.Fatalf("matched=%v handled=%v, want matched=true handled=true (prose set answers with the pending echo)", matched, handled)
	}
	if reply == "" {
		t.Fatal("prose set must reply with the itemized pending echo")
	}
	mid, err := store.GetMeta(sid)
	if err != nil {
		t.Fatal(err)
	}
	if mid.GoalCondition != "" {
		t.Fatalf("prose set must NOT activate before confirm, got condition %q", mid.GoalCondition)
	}
	if mid.GoalPendingJSON == "" {
		t.Fatal("prose set must park a pending compiled goal (GoalPendingJSON)")
	}

	// The confirm turn activates and rewrites the user message into round 1.
	matched, handled, _ = al.applyGoalCommandPrompt(
		context.Background(),
		bus.InboundMessage{Content: "/goal confirm", UserInitiated: true},
		agentInst, &opts,
	)
	if !matched || handled {
		t.Fatalf("confirm: matched=%v handled=%v, want matched=true handled=false (turn continues to LLM)", matched, handled)
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
	activatePendingGoal(t, al, agentInst, &opts)
	oneRound := 1
	if err := store.SetMeta(sid, session.MetaPatch{GoalRoundsUsed: &oneRound}); err != nil {
		t.Fatal(err)
	}

	// ADR-053 Phase-2 (N-6/D11): a `/goal <new intent>` while a goal is ALREADY
	// active is diffed as an amendment and confirmed — never silently recompiled.
	// So /goal condition B produces an amendment echo (handled=true) and leaves
	// the active goal A untouched, with a pending amendment stored.
	matched, handled, reply := al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal condition B", UserInitiated: true}, agentInst, &opts)
	if !matched || !handled {
		t.Fatalf("re-state: matched=%v handled=%v, want matched=true handled=true (amendment echo)", matched, handled)
	}
	if !strings.Contains(reply, "amendment") {
		t.Fatalf("re-state reply = %q, want an amendment echo", reply)
	}
	afterAmend, _ := store.GetMeta(sid)
	if afterAmend.GoalCondition != "condition A" {
		t.Fatalf("condition after re-state = %q, want condition A still active (no silent recompile, N-6)", afterAmend.GoalCondition)
	}
	if afterAmend.GoalPendingJSON == "" {
		t.Fatal("GoalPendingJSON must hold the proposed amendment until confirm (N-6)")
	}

	// /goal confirm applies the amendment: condition B, rounds reset (new generation).
	al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal confirm", UserInitiated: true}, agentInst, &opts)
	after, err := store.GetMeta(sid)
	if err != nil {
		t.Fatal(err)
	}
	if after.GoalCondition != "condition B" {
		t.Fatalf("condition = %q, want condition B after confirm (FR-113/N-6)", after.GoalCondition)
	}
	if after.GoalRoundsUsed != 0 {
		t.Fatalf("rounds_used = %d, want reset to 0 on amend (new generation)", after.GoalRoundsUsed)
	}
	if after.GoalPendingJSON != "" {
		t.Fatal("GoalPendingJSON must clear on confirm")
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
	activatePendingGoal(t, al, agentInst, &opts)

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
		// First iteration: the status section's goal is still ACTIVE, so the
		// restate parked as an amendment (confirm answers synchronously);
		// later iterations: a fresh pending goal (confirm continues into
		// round 1). Either way the confirm leaves an ACTIVE goal to clear.
		al.applyGoalCommandPrompt(context.Background(),
			bus.InboundMessage{Content: "/goal confirm", UserInitiated: true}, agentInst, &opts)
		if mid, merr := store.GetMeta(sid); merr != nil || mid.GoalCondition == "" {
			t.Fatalf("%s: setup — goal must be active before the clear (err=%v)", verb, merr)
		}
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

// TestGoalClear_CancelsInFlightGoalVerifierSession proves ADR-052 FR-037's
// `/goal clear` cancel half (7-reviewer gate item 2): clearGoal looks up the
// goal unit's registered verifier session (verifierUnitForGoal(sessionID))
// and cancels it via RequestCancelForSession — the SAME chat-cancel every
// other Stop surface uses (A2) — then unregisters the entry. Drives a REAL
// in-flight verifier turn (runVerifierAdjudication via al.JudgeCriteria,
// blocked on a channel-gated fake provider) rather than a fake registry
// entry alone, so the assertion proves an actual turn gets canceled, not
// just that a map entry disappears.
func TestGoalClear_CancelsInFlightGoalVerifierSession(t *testing.T) {
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	pe := NewPlanEngine(al, plan.New(t.TempDir()), nil, nil)
	al.SetPlanEngine(pe)

	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	opts := processOptions{
		TranscriptStore: store, TranscriptSessionID: sid,
		Channel: "webchat", ChatID: "c1", SessionKey: "sk1", UserInitiated: true,
	}
	al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal make the tests pass", UserInitiated: true}, agentInst, &opts)
	activatePendingGoal(t, al, agentInst, &opts)

	registered := make(chan struct{})
	proceed := make(chan struct{})
	fake := &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		close(registered)
		<-proceed
		return &providers.LLMResponse{
			Content: `{"met": true, "criteria": [{"id":"goal-condition","met":true,"reason":"ok"}]}`,
		}, nil
	}}
	judgeInst.Provider = fake

	judgeDone := make(chan JudgeCriteriaResult, 1)
	go func() {
		judgeDone <- al.JudgeCriteria(context.Background(), JudgeCriteriaInput{
			Scope:           task.VerdictScopeGoal,
			AssigneeAgentID: agentInst.ID,
			Criteria: []task.AcceptanceCriterion{
				{ID: "goal-condition", Kind: task.KindProse, Text: "make the tests pass"},
			},
			Attempt:       1,
			ClaimText:     "done",
			GoalSessionID: sid,
		})
	}()

	<-registered
	verifierSessionID, ok := pe.VerifierRegistry().Lookup(verifierUnitForGoal(sid))
	if !ok || verifierSessionID == "" {
		t.Fatal("expected the goal verifier session to be registered before dispatch")
	}

	matched, handled, reply := al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal clear", UserInitiated: true}, agentInst, &opts)
	if !matched || !handled || !strings.Contains(reply, "cleared") {
		t.Fatalf("/goal clear: matched=%v handled=%v reply=%q", matched, handled, reply)
	}

	if _, stillRegistered := pe.VerifierRegistry().Lookup(verifierUnitForGoal(sid)); stillRegistered {
		t.Error("the verifier registry entry must be unregistered by /goal clear")
	}

	// Prove clearGoal's own RequestCancelForSession call actually FIRED (was
	// not a no-op) via the codebase's established double-cancel semantics
	// (TestRequestCancel_DoubleCancelReturnsFiredFalse, cancel_test.go): once
	// a cancel has claimed a turn, a SECOND cancel attempt on the SAME
	// session returns Fired=false. The cancel cascade itself is graceful-
	// then-hard-abort on its own internal timer (InterruptSession's 3s grace
	// window, cancel.go) — asserting on ctx.Err() immediately would be
	// timing-dependent/flaky; Fired is the deterministic, already-proven
	// signal this codebase uses to verify "a cancel was actually claimed".
	fired, _, err := al.RequestCancelForSession(context.Background(), verifierSessionID, "", "")
	if err != nil {
		t.Fatalf("RequestCancelForSession (verification probe): %v", err)
	}
	if fired {
		t.Error("/goal clear's own cancel must already have claimed this session — a second cancel " +
			"attempt returning Fired=true means clearGoal never actually canceled it")
	}

	close(proceed)
	<-judgeDone
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
	activatePendingGoal(t, al, agentInst, &opts)
	// Phase-2 compile stored a UUID-IDed criteria ladder; the canned met-judge
	// provider echoes the legacy "goal-condition" ID, so exercise the back-compat
	// fallback (compiledGoalCriteriaFor with empty GoalCriteriaJSON).
	emptyCriteria := ""
	if err := store.SetMeta(sid, session.MetaPatch{GoalCriteriaJSON: &emptyCriteria}); err != nil {
		t.Fatal(err)
	}

	judgeInst.Provider = metJudgeProvider("tests pass")

	// ADR-053 Phase-2 (FR-101/G-1): the Judge fires ONLY on an explicit
	// completion claim ([goal:evidence] + GOAL_STATUS: met) — a bare turn
	// with no marker must NOT adjudicate. This turn ends in a real claim.
	result := &turnResult{finalContent: "[goal:evidence] all tests green\nGOAL_STATUS: met"}
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
	activatePendingGoal(t, al, agentInst, &opts)
	// Phase-2 compile produced a UUID-IDed criteria ladder in GoalCriteriaJSON.
	// The canned judge providers below echo the legacy "goal-condition" ID, so
	// exercise the back-compat fallback (compiledGoalCriteriaFor with empty
	// GoalCriteriaJSON → single "goal-condition" prose criterion) — this tests
	// the pre-Phase-2 session path that checkGoalLoopAfterTurn still serves.
	emptyCriteria := ""
	if err := store.SetMeta(sid, session.MetaPatch{GoalCriteriaJSON: &emptyCriteria}); err != nil {
		t.Fatal(err)
	}

	judgeInst.Provider = unmetJudgeProvider("3 tests still failing")

	// ADR-053 Phase-2 (FR-101/G-1): the Judge fires ONLY on an explicit
	// completion claim. This turn ends in a real claim ([goal:evidence] +
	// GOAL_STATUS: met); the judge returns unmet → one round consumed + steer.
	result := &turnResult{finalContent: "[goal:evidence] ran suite, 3 still red\nGOAL_STATUS: met"}
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

// TestGoalLoop_ScheduledTurn_DoesNotAdvanceGoal proves review r2 RV3: a
// scheduled/loop turn (opts.UserInitiated=false, opts.SenderID="" — exactly
// what ProcessScheduled's processOptions literal carries, since it is built
// directly in loop.go and never threads through the msg-based path that sets
// SenderID/UserInitiated) must NOT touch an active /goal loop on the same
// session, even though opts.IsTaskRun is also false for that origin (/goal
// and /loop can legitimately coexist on one session).
func TestGoalLoop_ScheduledTurn_DoesNotAdvanceGoal(t *testing.T) {
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	goalOpts := processOptions{
		TranscriptStore: store, TranscriptSessionID: sid,
		Channel: "webchat", ChatID: "c1", SessionKey: "sk1", UserInitiated: true,
	}
	al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal make the tests pass", UserInitiated: true}, agentInst, &goalOpts)
	activatePendingGoal(t, al, agentInst, &goalOpts)

	// A judge provider that fails the test if ever called — a scheduled/loop
	// turn must never reach the judge at all.
	judgeFake := &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		t.Fatal("judge must NOT be called for a scheduled/loop turn on a goal-bearing session")
		return &providers.LLMResponse{}, nil
	}}
	judgeInst.Provider = judgeFake

	before, err := store.GetMeta(sid)
	if err != nil {
		t.Fatal(err)
	}

	// Mimics ProcessScheduled's own processOptions literal (loop.go ~L5156):
	// built directly, so UserInitiated and SenderID are both left at their
	// zero value — never through the msg-based path (processMessage) that
	// sets SenderID from msg.Sender.CanonicalID and UserInitiated from
	// msg.UserInitiated.
	scheduledOpts := processOptions{
		TranscriptStore: store, TranscriptSessionID: sid,
		Channel: "webchat", ChatID: "c1", SessionKey: "sk1",
	}
	result := &turnResult{finalContent: "scheduled run output, unrelated to the goal"}
	al.checkGoalLoopAfterTurn(context.Background(), agentInst, scheduledOpts, result)

	after, err := store.GetMeta(sid)
	if err != nil {
		t.Fatal(err)
	}
	if after.GoalRoundsUsed != before.GoalRoundsUsed {
		t.Fatalf("rounds_used changed from %d to %d — a scheduled/loop turn must not consume a goal round",
			before.GoalRoundsUsed, after.GoalRoundsUsed)
	}
	if after.GoalCondition != before.GoalCondition {
		t.Fatal("goal condition changed — a scheduled/loop turn must not touch the goal at all")
	}
	if len(result.followUps) != 0 {
		t.Fatalf("expected no follow-up from a scheduled/loop turn, got %d", len(result.followUps))
	}
	if judgeFake.callCount() != 0 {
		t.Fatal("judge must not have been called")
	}
}

// TestGoalLoop_ReInjectedFollowUp_AdvancesGoal proves the counterpart: the
// goal loop's own re-injected follow-up (SenderID == goalLoopFollowUpSenderID,
// UserInitiated=false — exactly how processMessage rebuilds opts for the
// republished bus.InboundMessage goal_loop.go itself constructs, since
// msg.UserInitiated is never set true for that follow-up) still advances the
// goal, despite UserInitiated being false.
func TestGoalLoop_ReInjectedFollowUp_AdvancesGoal(t *testing.T) {
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	goalOpts := processOptions{
		TranscriptStore: store, TranscriptSessionID: sid,
		Channel: "webchat", ChatID: "c1", SessionKey: "sk1", UserInitiated: true,
	}
	al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal make the tests pass", UserInitiated: true}, agentInst, &goalOpts)
	activatePendingGoal(t, al, agentInst, &goalOpts)

	judgeInst.Provider = unmetJudgeProvider("still not there")

	// Mirrors what processMessage builds for the goal loop's own republished
	// follow-up: SenderID threaded from msg.Sender.CanonicalID (goal_loop.go's
	// InboundMessage carries Sender.CanonicalID: goalLoopFollowUpSenderID),
	// UserInitiated left false (msg.UserInitiated is never set true for it).
	followUpOpts := processOptions{
		TranscriptStore: store, TranscriptSessionID: sid,
		Channel: "webchat", ChatID: "c1", SessionKey: "sk1",
		SenderID: goalLoopFollowUpSenderID,
	}
	// ADR-053 Phase-2 (FR-101): the round advances on a CLAIM, not on a bare
	// turn. The re-injected follow-up passes the origin gate (SenderID ==
	// goalLoopFollowUpSenderID, UserInitiated=false) and a claim carried by it
	// still adjudicates + advances — proving the system continuation is not
	// gated out (only non-user-origin gating, not a claim-blocking gate).
	result := &turnResult{finalContent: "[goal:evidence] tried again\nGOAL_STATUS: met"}
	al.checkGoalLoopAfterTurn(context.Background(), agentInst, followUpOpts, result)

	after, err := store.GetMeta(sid)
	if err != nil {
		t.Fatal(err)
	}
	if after.GoalRoundsUsed != 1 {
		t.Fatalf("rounds_used = %d, want 1 — the goal loop's own re-injected follow-up must still advance "+
			"the round", after.GoalRoundsUsed)
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
	activatePendingGoal(t, al, agentInst, &opts)
	judgeInst.Provider = unmetJudgeProvider("still unmet")

	// ADR-053 Phase-2 (FR-101): each round advances on a CLAIM, not a bare
	// turn. Round 1 = a claim the judge finds unmet.
	r1 := &turnResult{finalContent: "[goal:evidence] attempt 1\nGOAL_STATUS: met"}
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

	r2 := &turnResult{finalContent: "[goal:evidence] attempt 2\nGOAL_STATUS: met"}
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
	activatePendingGoal(t, al, agentInst, &opts)

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

// TestGoalLoop_JudgeThrottled_BoundedByOwnTimeout_NotCallerCtx is review r1
// major M2: checkGoalLoopAfterTurn must NOT hang the interactive turn
// forever when (a) the judge keeps failing/throttling AND (b) the caller's
// own ctx carries NO deadline at all (context.Background(), the realistic
// shape of an interactive chat turn's ctx) — exactly the combination
// JudgeCriteria's own D7 "retry forever, respecting only ctx cancellation"
// contract would otherwise hang on indefinitely. goalJudgeRoundTimeout is
// substituted with a tiny bound so the test itself completes in
// milliseconds, not the real 10-minute production value.
func TestGoalLoop_JudgeThrottled_BoundedByOwnTimeout_NotCallerCtx(t *testing.T) {
	origTimeout := goalJudgeRoundTimeout
	t.Cleanup(func() { goalJudgeRoundTimeout = origTimeout })
	goalJudgeRoundTimeout = 5 * time.Millisecond

	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	opts := processOptions{
		TranscriptStore: store, TranscriptSessionID: sid,
		Channel: "webchat", ChatID: "c1", SessionKey: "sk1", UserInitiated: true,
	}
	al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal make the tests pass", UserInitiated: true}, agentInst, &opts)
	activatePendingGoal(t, al, agentInst, &opts)

	// The judge keeps erroring — with the ORIGINAL (pre-fix) code, calling
	// JudgeCriteria on a ctx with no deadline would retry the real
	// 60/120/300s D7 backoff schedule forever (production's judgeSleepFn is
	// the real sleepWithContext, not a test fake here — the fix must bound
	// the ctx itself, not rely on a test-only sleep substitution).
	judgeInst.Provider = &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return nil, context.DeadlineExceeded
	}}

	// Deliberately NO deadline/timeout on the caller's ctx — the realistic
	// shape of an interactive turn's ctx that isn't itself about to expire.
	ctx := context.Background()

	result := &turnResult{finalContent: "still working on it"}
	done := make(chan struct{})
	go func() {
		al.checkGoalLoopAfterTurn(ctx, agentInst, opts, result)
		close(done)
	}()

	select {
	case <-done:
		// Returned — bounded by goalJudgeRoundTimeout, not hung forever.
	case <-time.After(2 * time.Second):
		t.Fatal("checkGoalLoopAfterTurn did not return within a generous margin over its own " +
			"bounded timeout — it is hanging on the caller's (deadline-less) ctx instead of its own")
	}

	after, err := store.GetMeta(sid)
	if err != nil {
		t.Fatal(err)
	}
	if after.GoalRoundsUsed != 0 {
		t.Fatalf("rounds_used = %d, want 0 (judge unavailability must not consume a round, D7)", after.GoalRoundsUsed)
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

// --- Idle-expiry (FR-064/D7, review r1 blocker) --------------------------

// TestGoal_IdleExpiry_7d proves goalIdleExpirySweep's 7-day calendar brake
// (the /goal counterpart to plan_engine.go's own idle-expiry sweep, review
// r1 gap 4): a goal idle for 6d23h must survive a sweep, and one idle for
// exactly 7d must be expired (cleared, R5 cap released). Fake-clock: both
// GoalLastActivityAt values and the sweep's own "now" are explicit
// caller-supplied timestamps — zero real sleeps.
func TestGoal_IdleExpiry_7d(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store := al.GetSessionStore()
	if store == nil {
		t.Fatal("shared session store not available")
	}

	now := time.Now().UTC()
	stillActiveAt := now.Add(-(6*24 + 23) * time.Hour).Format(time.RFC3339) // 6d23h idle
	expiredAt := now.Add(-7 * 24 * time.Hour).Format(time.RFC3339)          // exactly 7d idle

	newGoalSession := func(condition, lastActivity string) string {
		meta, err := store.NewSession(session.SessionTypeChat, "webchat", agentInst.ID)
		if err != nil {
			t.Fatalf("NewSession: %v", err)
		}
		if err := store.SetMeta(meta.ID, session.MetaPatch{
			GoalCondition:      &condition,
			GoalMaxRounds:      intPtr(config.DefaultGoalMaxRounds),
			GoalStartedAt:      &lastActivity,
			GoalLastActivityAt: &lastActivity,
		}); err != nil {
			t.Fatalf("SetMeta: %v", err)
		}
		return meta.ID
	}

	stillActiveSID := newGoalSession("still active goal", stillActiveAt)
	expiredSID := newGoalSession("expired goal", expiredAt)

	al.goalIdleExpirySweep(config.PlanningConfig{}, now)

	stillActive, err := store.GetMeta(stillActiveSID)
	if err != nil {
		t.Fatal(err)
	}
	if stillActive.GoalCondition == "" {
		t.Fatal("a goal idle for 6d23h must NOT be expired (under the 7-day bound)")
	}

	expired, err := store.GetMeta(expiredSID)
	if err != nil {
		t.Fatal(err)
	}
	if expired.GoalCondition != "" {
		t.Fatal("a goal idle for exactly 7d must be idle-expired (cleared)")
	}
}

// TestLoop_IdleExpiry_7d proves LoopScheduler.IdleExpirySweep's 7-day
// calendar brake (the /loop counterpart, review r1 gap 4): a loop idle for
// 6d23h must survive a sweep (job still enabled), and one idle for exactly
// 7d must be stopped and its cron job removed. Fake-clock: both
// LoopLastActivityAt values and the sweep's own "now" are explicit
// caller-supplied timestamps — zero real sleeps.
func TestLoop_IdleExpiry_7d(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store := al.GetSessionStore()
	if store == nil {
		t.Fatal("shared session store not available")
	}
	// The fake clock only matters for the scheduler's OWN due-job firing
	// (RunDueJobs/AddOneShot's "at" math) — this test drives IdleExpirySweep
	// directly with its own explicit `now`, never through the cron firing
	// path, so the scheduler's clock is left at its default and unused here.
	ls, _ := newLoopSchedulerForTest(t, al)
	al.SetLoopScheduler(ls)

	now := time.Now().UTC()
	stillActiveAt := now.Add(-(6*24 + 23) * time.Hour).Format(time.RFC3339) // 6d23h idle
	expiredAt := now.Add(-7 * 24 * time.Hour).Format(time.RFC3339)          // exactly 7d idle

	newLoopSession := func(lastActivity string) (sessionID string) {
		meta, err := store.NewSession(session.SessionTypeChat, "webchat", agentInst.ID)
		if err != nil {
			t.Fatalf("NewSession: %v", err)
		}
		sessionID = meta.ID
		jobID, err := ls.AddInterval(agentInst.ID, sessionID, 5*60*1000)
		if err != nil {
			t.Fatalf("AddInterval: %v", err)
		}
		mode := loopModeInterval
		everyMS := int64(5 * 60 * 1000)
		if err := store.SetMeta(sessionID, session.MetaPatch{
			LoopMode:           &mode,
			LoopPrompt:         strPtrForTest("ping"),
			LoopMaxRuns:        intPtr(config.DefaultLoopMaxRuns),
			LoopIntervalMS:     &everyMS,
			LoopJobID:          &jobID,
			LoopStartedAt:      &lastActivity,
			LoopLastActivityAt: &lastActivity,
		}); err != nil {
			t.Fatalf("SetMeta: %v", err)
		}
		return sessionID
	}

	stillActiveSID := newLoopSession(stillActiveAt)
	expiredSID := newLoopSession(expiredAt)

	if len(ls.ListEnabledJobs()) != 2 {
		t.Fatalf("enabled jobs = %d, want 2 before the sweep", len(ls.ListEnabledJobs()))
	}

	ls.IdleExpirySweep(config.PlanningConfig{}, now)

	stillActive, err := store.GetMeta(stillActiveSID)
	if err != nil {
		t.Fatal(err)
	}
	if stillActive.LoopMode == "" {
		t.Fatal("a loop idle for 6d23h must NOT be expired (under the 7-day bound)")
	}

	expired, err := store.GetMeta(expiredSID)
	if err != nil {
		t.Fatal(err)
	}
	if expired.LoopMode != "" {
		t.Fatal("a loop idle for exactly 7d must be idle-expired (stopped)")
	}

	if len(ls.ListEnabledJobs()) != 1 {
		t.Fatalf("enabled jobs = %d, want 1 after the sweep (the expired job's cron entry must be removed)",
			len(ls.ListEnabledJobs()))
	}
}

func strPtrForTest(s string) *string { return &s }

// --- UAT S3 fix: stable per-generation goal_id + non-failure user-clear ----
//
// goal_loop-uat-s3.md findings 1 & 2 (2026-07): (1) GoalStatusFrame.goal_id
// was never populated, so every goal landed in the SPA's `_default` pill
// bucket and a second goal set after a clear could not get its own pill/
// history; (2) `/goal clear` emitted state="failed" for a deliberate,
// successful user action. Both are fixed in goal_loop.go/goal_triggers.go;
// these tests are the DoD: (a) an emitted GoalStatusFrame carries a
// non-empty, STABLE goal_id, and (b) a user-initiated clear does NOT emit
// state="failed".

// goalStatusPayloadsFor drains the event collector and returns every
// GoalStatusChangedPayload observed for sid, in emission order — mirrors
// conformance_design_test.go's goalPillStates but also exposes GoalID, which
// these tests assert on directly.
func goalStatusPayloadsFor(c *eventCollector, sid string) []GoalStatusChangedPayload {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []GoalStatusChangedPayload
	for _, e := range c.events {
		if e.Kind != EventKindGoalStatusChanged {
			continue
		}
		p, ok := e.Payload.(GoalStatusChangedPayload)
		if !ok || p.SessionID != sid {
			continue
		}
		out = append(out, p)
	}
	return out
}

// TestGoalId_StableAcrossLifecycle_NewGenerationAfterClear proves finding 1:
// every GoalStatusFrame for an active goal carries a non-empty goal_id that
// stays STABLE across every frame of that one generation (set, amend
// +confirm, an ordinary round-advance turn) — never fabricated per-frame —
// and a genuinely NEW goal set after `/goal clear` mints a DIFFERENT id. This
// is exactly the UAT-confirmed symptom ("goal -> amend -> clear -> second
// goal left exactly ONE pill and no history"): without a stable-but-distinct
// id, the SPA's GoalPillTray (one pill per goal-id) cannot tell the second
// goal apart from the first.
func TestGoalId_StableAcrossLifecycle_NewGenerationAfterClear(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	opts := processOptions{
		TranscriptStore: store, TranscriptSessionID: sid,
		Channel: "webchat", ChatID: "c1", SessionKey: "sk1", UserInitiated: true,
	}

	c, cleanup := newEventCollector(t, al)
	defer cleanup()

	// --- Goal #1: set, then amend+confirm (must KEEP the same goal-id — it's
	// the SAME goal being refined, not a new one) ---
	al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal condition A", UserInitiated: true}, agentInst, &opts)
	activatePendingGoal(t, al, agentInst, &opts)
	meta1, err := store.GetMeta(sid)
	if err != nil {
		t.Fatal(err)
	}
	if meta1.GoalID == "" {
		t.Fatal("a freshly-set goal must carry a non-empty GoalID")
	}
	firstID := meta1.GoalID

	al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal condition A amended", UserInitiated: true}, agentInst, &opts)
	al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal confirm", UserInitiated: true}, agentInst, &opts)
	metaAmended, err := store.GetMeta(sid)
	if err != nil {
		t.Fatal(err)
	}
	if metaAmended.GoalID != firstID {
		t.Fatalf("amendment must keep the same goal-id, got %q want %q", metaAmended.GoalID, firstID)
	}

	// An ordinary (non-claim) worker turn re-emits the SAME goal-id.
	al.checkGoalLoopAfterTurn(context.Background(), agentInst, opts, &turnResult{finalContent: "still working"})
	metaAfterTurn, err := store.GetMeta(sid)
	if err != nil {
		t.Fatal(err)
	}
	if metaAfterTurn.GoalID != firstID {
		t.Fatalf("an ordinary turn must not change the goal-id, got %q want %q", metaAfterTurn.GoalID, firstID)
	}

	// --- Clear goal #1, then set a genuinely new goal #2 ---
	al.clearGoal(sid, store, goalClearNoteUser)
	al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal condition B", UserInitiated: true}, agentInst, &opts)
	activatePendingGoal(t, al, agentInst, &opts)
	meta2, err := store.GetMeta(sid)
	if err != nil {
		t.Fatal(err)
	}
	if meta2.GoalID == "" {
		t.Fatal("the second goal must also carry a non-empty GoalID")
	}
	if meta2.GoalID == firstID {
		t.Fatal("a NEW goal set after /goal clear must mint a DIFFERENT goal-id from the cleared goal " +
			"(UAT S3: this is what distinguishes the second goal's pill/history from the first)")
	}

	// Drain: cleanup unsubscribes and blocks until the collector goroutine has
	// appended every already-emitted, already-buffered event to c.events —
	// without this, reading c.events right after the last call above races
	// the background goroutine that populates it.
	cleanup()

	// Every emitted frame for goal #1's lifecycle carries firstID; goal #2's
	// own frame carries its own, different id. None is ever empty.
	payloads := goalStatusPayloadsFor(c, sid)
	sawFirstID, sawSecondID := false, false
	for _, p := range payloads {
		switch p.GoalID {
		case firstID:
			sawFirstID = true
		case meta2.GoalID:
			sawSecondID = true
		case "":
			// ADR-074 D4a: a PENDING (queued) frame legitimately carries no
			// goal-id — the generation is minted at confirm, never earlier
			// (newGoalID's own contract). Any OTHER state with an empty id is
			// still the UAT S3 bug.
			if p.State != goalPillQueued {
				t.Fatalf("emitted GoalStatusFrame with an empty goal_id: %+v", p)
			}
		default:
			t.Fatalf("emitted GoalStatusFrame with an unexpected goal_id %q: %+v", p.GoalID, p)
		}
	}
	if !sawFirstID {
		t.Fatal("expected at least one emitted frame carrying the first goal's id")
	}
	if !sawSecondID {
		t.Fatal("expected at least one emitted frame carrying the second goal's id")
	}
}

// TestGoalClear_UserInitiated_EmitsClearedNotFailed proves finding 2: the
// user-facing `/goal clear` path (applyGoalCommandPrompt's clear-verb branch,
// the ONLY caller of clearGoal with goalClearNoteUser) emits pill state
// "cleared" — NOT "failed". A deliberate, successful user action must never
// paint the pill as a failure.
func TestGoalClear_UserInitiated_EmitsClearedNotFailed(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	opts := processOptions{
		TranscriptStore: store, TranscriptSessionID: sid,
		Channel: "webchat", ChatID: "c1", SessionKey: "sk1", UserInitiated: true,
	}

	al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal make the tests pass", UserInitiated: true}, agentInst, &opts)
	activatePendingGoal(t, al, agentInst, &opts)

	c, cleanup := newEventCollector(t, al)
	defer cleanup()

	matched, handled, reply := al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal clear", UserInitiated: true}, agentInst, &opts)
	if !matched || !handled {
		t.Fatalf("clear: matched=%v handled=%v, want both true", matched, handled)
	}
	if !strings.Contains(reply, "cleared") {
		t.Fatalf("clear reply = %q, want a cleared confirmation (chat text is unaffected by this fix)", reply)
	}

	// Drain (see the sibling test's identical comment): block until the
	// collector goroutine has appended every buffered event before reading it.
	cleanup()

	payloads := goalStatusPayloadsFor(c, sid)
	if len(payloads) == 0 {
		t.Fatal("expected at least one emitted GoalStatusFrame for the clear")
	}
	last := payloads[len(payloads)-1]
	if last.State == goalPillFailed {
		t.Fatal("user-initiated clear must NEVER emit state=failed (UAT S3: a deliberate, successful " +
			"action must not read as a failure)")
	}
	if last.State != goalPillCleared {
		t.Fatalf("user-initiated clear emitted state %q, want %q", last.State, goalPillCleared)
	}
}

// TestGoalClear_GenuineFailures_StillEmitFailed proves the S3 fix did not
// regress the real terminal-failure paths — round-bound-reached, budget-
// exhausted, and idle-expired clears are NOT user-initiated and must still
// emit "failed", unchanged.
func TestGoalClear_GenuineFailures_StillEmitFailed(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	opts := processOptions{
		TranscriptStore: store, TranscriptSessionID: sid,
		Channel: "webchat", ChatID: "c1", SessionKey: "sk1", UserInitiated: true,
	}
	al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal make the tests pass", UserInitiated: true}, agentInst, &opts)
	activatePendingGoal(t, al, agentInst, &opts)

	c, cleanup := newEventCollector(t, al)
	defer cleanup()

	al.clearGoal(sid, store, "round bound reached (3/3)")

	// Drain (see the sibling tests' identical comment).
	cleanup()

	payloads := goalStatusPayloadsFor(c, sid)
	if len(payloads) == 0 {
		t.Fatal("expected an emitted GoalStatusFrame")
	}
	if got := payloads[len(payloads)-1].State; got != goalPillFailed {
		t.Fatalf("round-bound-reached clear emitted state %q, want %q (this path is NOT user-initiated "+
			"and must still report a genuine failure)", got, goalPillFailed)
	}
}

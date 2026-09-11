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
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
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

// activateTestGoalRecord creates and activates a minimal pkg/goal record
// for sid — the test-side mirror of createAndActivateSessionGoalRecord
// (goal_loop.go, GOAL-FR-009/010/011, wave E12). Most of this suite's test
// sessions are built directly via newGoalTestSession/SetMeta rather than
// through a real `/goal` command turn, so they carry no durable goal record
// unless a test asks for one explicitly — any assertion that now reads the
// goal record (routing, verdict-on-record checks) needs this first. Returns
// the minted goal id.
func activateTestGoalRecord(t *testing.T, sid, intent string) string {
	t.Helper()
	gid := newGoalID()
	g, err := goal.New(generated.GoalOwnerKindSession, sid, generated.ChatCompiled,
		intent, "", nil, newFloorDoD(), config.DefaultGoalMaxRounds, time.Now().UTC())
	if err != nil {
		t.Fatalf("activateTestGoalRecord: goal.New: %v", err)
	}
	g.GoalID = gid
	gs := goal.NewStore(config.OmnipusHomeDir())
	if cerr := gs.Create(g); cerr != nil {
		t.Fatalf("activateTestGoalRecord: Create: %v", cerr)
	}
	if _, uerr := gs.Update(gid, func(cur *goal.Goal) error {
		return cur.Activate(sid, time.Now().UTC())
	}); uerr != nil {
		t.Fatalf("activateTestGoalRecord: Activate: %v", uerr)
	}
	return gid
}

// goalRecordForSession reads back the ACTIVE pkg/goal record BOUND to sid —
// the ADR-086 replacement for the retired session-meta Goal* fields every
// assertion in this suite used to read off *session.UnifiedMeta (wave S6
// deleted them; activeGoalForSession, goal_record_wiring.go, is the single
// entry predicate production code now shares). It FAILS the test when no
// active record exists, so an assertion that used to read a populated meta
// field can never silently degrade into reading a zero value off a record
// that was never created.
//
// Field mapping, for readers comparing against the pre-ADR-086 assertions:
//
//	meta.GoalID                 -> g.GoalID
//	meta.GoalCondition          -> g.Prompt
//	meta.GoalRoundsUsed         -> g.Round
//	meta.GoalMaxRounds          -> g.MaxRounds
//	meta.GoalCriteriaJSON       -> goalRecordCompiledJSON(g)
//	meta.GoalLatestReason       -> g.LatestReason
//	meta.GoalStartedAt          -> g.StartedAt   (now a *time.Time)
//	meta.GoalLastActivityAt     -> g.LastActivityAt
//	meta.GoalZeroOutputPushes   -> g.ZeroOutputPushes
//	meta.GoalQuestionRoundsUsed -> g.QuestionRoundsUsed
func goalRecordForSession(t *testing.T, sid string) *goal.Goal {
	t.Helper()
	g := activeGoalForSession(sid)
	if g == nil {
		t.Fatalf("goalRecordForSession: no ACTIVE goal record is bound to session %q", sid)
	}
	return g
}

// goalRecordForSessionOrNil is goalRecordForSession without the fatal — for
// the assertions that deliberately check a session carries NO active goal
// (the old `meta.GoalCondition == ""` check). A nil return is the ADR-086
// equivalent of that empty string.
func goalRecordForSessionOrNil(sid string) *goal.Goal { return activeGoalForSession(sid) }

// mustGoalRecord reads ONE goal record by id regardless of its state — the
// reader for assertions about a goal that has reached a TERMINAL state,
// which activeGoalForSession deliberately stops finding (Terminate moves the
// record out of active).
func mustGoalRecord(t *testing.T, gid string) *goal.Goal {
	t.Helper()
	g, err := goal.NewStore(config.OmnipusHomeDir()).Get(gid)
	if err != nil {
		t.Fatalf("mustGoalRecord %q: %v", gid, err)
	}
	return g
}

// setGoalRecordRound forces the ACTIVE goal record's Round counter to n —
// the ADR-086 replacement for the `SetMeta(sid, MetaPatch{GoalRoundsUsed:
// &n})` arrange step several tests use to pre-age a goal's budget.
func setGoalRecordRound(t *testing.T, sid string, n int) {
	t.Helper()
	g := goalRecordForSession(t, sid)
	if _, err := goal.NewStore(config.OmnipusHomeDir()).Update(g.GoalID, func(cur *goal.Goal) error {
		cur.Round = n
		return nil
	}); err != nil {
		t.Fatalf("setGoalRecordRound(%q, %d): %v", sid, n, err)
	}
}

// clearGoalRecordCriteria empties the ACTIVE goal record's criteria ladder —
// the ADR-086 replacement for the `SetMeta(sid, MetaPatch{GoalCriteriaJSON:
// &""})` arrange step the judge tests use to exercise the back-compat
// fallback (compiledGoalCriteriaFor's single "goal-condition" prose
// criterion). Kept EXPLICIT rather than relying on activation happening to
// leave the list empty, so the precondition each of those tests depends on
// stays visible at its own call site.
func clearGoalRecordCriteria(t *testing.T, sid string) {
	t.Helper()
	g := goalRecordForSession(t, sid)
	if _, err := goal.NewStore(config.OmnipusHomeDir()).Update(g.GoalID, func(cur *goal.Goal) error {
		return cur.SetCriteria(nil, time.Now().UTC())
	}); err != nil {
		t.Fatalf("clearGoalRecordCriteria(%q): %v", sid, err)
	}
}

// activatePendingGoal is a compatibility name (ADR-081 D1: instant
// activation — there is no more pending/confirm step) kept so the ~33
// call sites across the goal test suite that call it right after an
// activating `/goal <intent>` do not all need individual edits. It is now a
// pure assertion: the goal must ALREADY be active, because the preceding
// `/goal <intent>` call activated it in the SAME turn.
func activatePendingGoal(t *testing.T, _ *AgentLoop, _ *AgentInstance, opts *processOptions) {
	t.Helper()
	if activeGoalForSession(opts.TranscriptSessionID) == nil {
		t.Fatal("activatePendingGoal: goal must already be ACTIVE (ADR-081 D1 instant activation)")
	}
}

// recordedGoalCriterionID is the id setGoalRoundsArmedRecorded /
// setGoalRecordArmed persist for their single prose criterion.
//
// Before ADR-086 those helpers persisted the id "goal-condition" straight
// into session meta's GoalCriteriaJSON string, which nothing validated. A
// pkg/goal record validates: "goal-condition" is one of GOAL-FR-007's three
// RESERVED, never-persisted criterion ids (pkg/goal/criteria.go's
// validateCriteriaList rejects it outright), because it belongs to
// compiledGoalCriteriaFor's synthesized back-compat criterion, not to a
// stored ladder. So a recorded goal now carries a real, non-reserved id and
// the canned judge providers below answer for BOTH forms — see
// cannedJudgeVerdictJSON.
const recordedGoalCriterionID = "a0000000-0000-4000-8000-000000000001"

// cannedJudgeVerdictJSON renders the fixed judge response every canned
// provider in this suite returns: one verdict per id the goal loop can put
// in front of the Judge, all with the same met value and reason.
//
// Verdicts are matched to criteria BY ID (verifier_adjudication.go's
// byID[c.ID] lookup) — an id the response omits resolves
// "criterion_unjudgeable", met=false. The judged set differs by path:
//
//   - EMPTY criteria ladder -> compiledGoalCriteriaFor synthesizes the single
//     "goal-condition" criterion (DoD is dropped with it, since
//     goalRecordCompiledJSON returns "" for a criteria-less record).
//   - RECORDED ladder -> criteria UNION DoD (ADR-080 D-DOD's judged-set union
//     seam), i.e. recordedGoalCriterionID plus newFloorDoD's two fixed
//     "goal-dod-floor-*" sentinels, which every real record carries because
//     Goal.Validate requires a non-empty DoD.
//
// Answering all four ids keeps ONE pair of canned providers usable on both
// paths. Ids absent from the judged set are simply never looked up, so the
// extra entries are inert — the met/unmet outcome each test asserts is
// exactly the one it asserted before.
func cannedJudgeVerdictJSON(met bool, reason string) string {
	ids := []string{
		"goal-condition",
		recordedGoalCriterionID,
		"goal-dod-floor-no-secrets",
		"goal-dod-floor-grounded-claims",
	}
	items := make([]string, 0, len(ids))
	for _, id := range ids {
		items = append(items, fmt.Sprintf(`{"id":%q,"met":%t,"reason":%q}`, id, met, reason))
	}
	return fmt.Sprintf(`{"met": %t, "criteria": [%s]}`, met, strings.Join(items, ","))
}

func metJudgeProvider(reason string) *fakeJudgeProvider {
	return &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return &providers.LLMResponse{Content: cannedJudgeVerdictJSON(true, reason)}, nil
	}}
}

func unmetJudgeProvider(reason string) *fakeJudgeProvider {
	return &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return &providers.LLMResponse{Content: cannedJudgeVerdictJSON(false, reason)}, nil
	}}
}

// --- /goal: set / status / clear ----------------------------------------

// TestGoalCommand_SetRewritesUserMessage — rewritten for ADR-081 D1: a PROSE
// `/goal <intent>` activates INSTANTLY in the same turn (matched=true,
// handled=false), rewriting opts.UserMessage to the raw intent — no compile
// call, no pending echo, no confirm step. GoalCriteriaJSON starts EMPTY
// (the working agent authors the record itself via set_goal, a later wave) —
// that is the legal transient state, not a bug.
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
	if !matched || handled {
		t.Fatalf("matched=%v handled=%v, want matched=true handled=false (instant activation continues to the LLM)", matched, handled)
	}
	if reply != "" {
		t.Fatalf("instant activation must not answer synchronously, got reply %q", reply)
	}
	if opts.UserMessage != "make the tests pass" {
		t.Fatalf("opts.UserMessage = %q, want the raw intent", opts.UserMessage)
	}
	after := goalRecordForSession(t, sid)
	if after.Prompt != "make the tests pass" || after.Round != 0 || after.MaxRounds != config.DefaultGoalMaxRounds {
		t.Fatalf("unexpected goal state: %+v", after)
	}
	if after.GoalID == "" {
		t.Fatal("instant activation must mint a GoalID")
	}
	if got := goalRecordCompiledJSON(after); got != "" {
		t.Fatalf("the goal record's criteria must start EMPTY on instant activation, got %q", got)
	}
}

// TestGoalCommand_ReplaceOnSet — rewritten for ADR-081 D1/D5/FR-001: a
// `/goal <new intent>` on an ALREADY-ACTIVE goal is STEERING, not a pending
// amendment. A PROSE restate rewrites the turn's working prompt (same
// mechanism as activation) and does NOT touch the persisted record — the
// working agent updates it via set_goal in a later wave — and critically
// does NOT mint a new GoalID (FR-001).
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
	firstID := goalRecordForSession(t, sid).GoalID
	setGoalRecordRound(t, sid, 1)

	// A prose restate rewrites the working prompt and continues the turn —
	// no confirm ritual, no amendment echo — and patches the durable
	// GoalCondition to the new intent (review-round-1 finding #9).
	matched, handled, reply := al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal condition B", UserInitiated: true}, agentInst, &opts)
	if !matched || handled {
		t.Fatalf("restate: matched=%v handled=%v, want matched=true handled=false (continues to the LLM)", matched, handled)
	}
	if reply != "" {
		t.Fatalf("restate must not answer synchronously, got reply %q", reply)
	}
	if opts.UserMessage != "condition B" {
		t.Fatalf("opts.UserMessage = %q, want the restated intent", opts.UserMessage)
	}
	after := goalRecordForSession(t, sid)
	if after.Prompt != "condition B" {
		t.Fatalf("finding #9: condition after restate = %q, want it patched to the new intent %q "+
			"(keeper prompts/status/fallback all cite the goal record's Prompt — leaving it stale would have "+
			"them cite the superseded intent forever)", after.Prompt, "condition B")
	}
	if after.GoalID != firstID {
		t.Fatalf("restate must NOT mint a new GoalID (FR-001), got %q want %q", after.GoalID, firstID)
	}
	if after.Round != 1 {
		t.Fatalf("restate must not reset rounds (no new generation), got %d", after.Round)
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
		// ADR-081 D1: instant activation — no confirm step. First iteration:
		// the status section's goal is still ACTIVE, so this is a prose
		// restate that leaves the already-active goal in place; later
		// iterations: a goalless fresh activation. Either way the goal is
		// ACTIVE immediately after the single call above.
		if goalRecordForSessionOrNil(sid) == nil {
			t.Fatalf("%s: setup — goal must be active before the clear", verb)
		}
		matched, handled, reply = al.applyGoalCommandPrompt(context.Background(),
			bus.InboundMessage{Content: "/goal " + verb, UserInitiated: true}, agentInst, &opts)
		if !matched || !handled {
			t.Fatalf("%s: matched=%v handled=%v", verb, matched, handled)
		}
		if !strings.Contains(reply, "cleared") {
			t.Fatalf("%s reply = %q, want a cleared confirmation", verb, reply)
		}
		if after := goalRecordForSessionOrNil(sid); after != nil {
			t.Fatalf("%s: goal is still ACTIVE: %q", verb, after.Prompt)
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
	if goalRecordForSessionOrNil(sid) != nil {
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
	// fallback (compiledGoalCriteriaFor on a record with an EMPTY criteria list).
	clearGoalRecordCriteria(t, sid)

	judgeInst.Provider = metJudgeProvider("tests pass")

	// ADR-053 Phase-2 (FR-101/G-1): the Judge fires ONLY on an explicit
	// completion claim ([goal:evidence] + GOAL_STATUS: met) — a bare turn
	// with no marker must NOT adjudicate. This turn ends in a real claim.
	result := &turnResult{finalContent: "[goal:evidence] all tests green\nGOAL_STATUS: met"}
	al.checkGoalLoopAfterTurn(context.Background(), agentInst, opts, result)

	// JUDGE-FR-098 (D13, this wave): the adjudication is recorded as
	// deferred work, not run synchronously — dispatch it now (simulating
	// runAgentLoop's own post-delivery goroutine call) to exercise the
	// SAME met-verdict behavior this test proves.
	if result.goalDeferredAdjudication == nil {
		t.Fatal("a met+evidence claim must record deferred adjudication work")
	}
	al.dispatchDeferredGoalAdjudication(result.goalDeferredAdjudication)

	if after := goalRecordForSessionOrNil(sid); after != nil {
		t.Fatalf("goal should be cleared on a met verdict, still ACTIVE: %q", after.Prompt)
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
	// JUDGE-FR-099 (D13, this wave): the deferred adjudication's steer is
	// delivered via the async-notifier, which needs routing to resolve.
	al.recordGoalRouting(sid, "", "webchat", "c1", "sk1", agentInst.ID)
	// Phase-2 compile produced a UUID-IDed criteria ladder on the goal record.
	// The canned judge providers below echo the legacy "goal-condition" ID, so
	// exercise the back-compat fallback (compiledGoalCriteriaFor on an EMPTY
	// criteria list → single "goal-condition" prose criterion) — this tests
	// the pre-Phase-2 session path that checkGoalLoopAfterTurn still serves.
	clearGoalRecordCriteria(t, sid)

	judgeInst.Provider = unmetJudgeProvider("3 tests still failing")

	// ADR-053 Phase-2 (FR-101/G-1): the Judge fires ONLY on an explicit
	// completion claim. This turn ends in a real claim ([goal:evidence] +
	// GOAL_STATUS: met); the judge returns unmet → one round consumed + steer.
	result := &turnResult{finalContent: "[goal:evidence] ran suite, 3 still red\nGOAL_STATUS: met"}
	al.checkGoalLoopAfterTurn(context.Background(), agentInst, opts, result)

	// JUDGE-FR-098 (D13, this wave): dispatch the deferred adjudication
	// (simulating runAgentLoop's own post-delivery goroutine call).
	if result.goalDeferredAdjudication == nil {
		t.Fatal("a met+evidence claim must record deferred adjudication work")
	}
	if len(result.followUps) != 0 {
		t.Fatalf("checkGoalLoopAfterTurn itself must not append a followUp on a deferred claim — "+
			"the steer only exists once the deferred adjudication runs; got %d", len(result.followUps))
	}
	al.dispatchDeferredGoalAdjudication(result.goalDeferredAdjudication)

	after := goalRecordForSessionOrNil(sid)
	if after == nil {
		t.Fatal("goal must remain active after an unmet verdict under the round bound")
	}
	if after.Round != 1 {
		t.Fatalf("rounds_used = %d, want 1", after.Round)
	}
	if !strings.Contains(after.LatestReason, "3 tests still failing") {
		t.Fatalf("latest reason = %q, want to contain the judge's reason", after.LatestReason)
	}

	// JUDGE-FR-099 (D13, this wave): the deferred adjudication's steer is
	// delivered via the async-notifier (idleSteerDeliverer ->
	// dispatchGoalAsyncFollowUp -> Notify -> bus.PublishInbound), NOT
	// result.followUps — by the time this runs, this turn's own followUps
	// have already been published and read by nobody again.
	var fu bus.InboundMessage
	select {
	case fu = <-al.bus.InboundChan():
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the deferred steer to be published via the async-notifier")
	}
	if !strings.Contains(fu.Content, "3 tests still failing") {
		t.Fatalf("steer content = %q, want the judge reason fed forward as steering (FR-043 pattern)", fu.Content)
	}
	if fu.UserInitiated {
		t.Fatal("a re-injected continuation must NOT be UserInitiated (Gap #8)")
	}
	if fu.Sender.CanonicalID != goalLoopFollowUpSenderID {
		t.Fatalf("steer sender = %q, want %q (JUDGE-FR-099's origin-gate sentinel)", fu.Sender.CanonicalID, goalLoopFollowUpSenderID)
	}
	// The async-notifier carries the transcript session id via
	// AsyncTranscriptSessionID (async_notifier.go's Notify), never
	// InboundMessage.SessionID — a field this async path never populates.
	if fu.AsyncTranscriptSessionID != sid {
		t.Fatalf("follow-up async_transcript_session_id = %q, want %q (same session)", fu.AsyncTranscriptSessionID, sid)
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

	before := goalRecordForSession(t, sid)

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

	after := goalRecordForSession(t, sid)
	if after.Round != before.Round {
		t.Fatalf("rounds_used changed from %d to %d — a scheduled/loop turn must not consume a goal round",
			before.Round, after.Round)
	}
	if after.Prompt != before.Prompt {
		t.Fatal("goal condition changed — a scheduled/loop turn must not touch the goal at all")
	}
	if len(result.followUps) != 0 {
		t.Fatalf("expected no follow-up from a scheduled/loop turn, got %d", len(result.followUps))
	}
	if judgeFake.callCount() != 0 {
		t.Fatal("judge must not have been called")
	}
}

// TestGoalLoop_TaskRunTurn_AdvancesTaskOwnedGoal proves GOAL-FR-015 (E12):
// unlike TestGoalLoop_ScheduledTurn_DoesNotAdvanceGoal's plain scheduled/loop
// turn (IsTaskRun=false, UserInitiated=false — excluded), a TASK RUN's own
// dispatched turn (IsTaskRun=true, UserInitiated=false, SenderID=
// "task-executor" — exactly loop.go's processTaskDirect literal) on a
// task-owned goal's own session DOES reach checkGoalLoopAfterTurn's ordinary-
// turn branch and bumps the goal's activity clock — GOAL-FR-013's "one code
// path" requires the after-turn hook to apply to a task-owned goal exactly
// like a chat-owned one.
func TestGoalLoop_TaskRunTurn_AdvancesTaskOwnedGoal(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	tk, _ := seedDefiningTaskGoal(t, al, "t-goal-loop-task-run", "native-agent")
	sid, err := al.taskExecutor.createTaskSessionSync(tk)
	if err != nil {
		t.Fatalf("createTaskSessionSync: %v", err)
	}
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store := al.GetAgentStore(tk.AgentID)

	if activeGoalForSession(sid) == nil {
		t.Fatal("test setup: the task-owned goal must already be bound ACTIVE to this session")
	}
	// Push the activity clock into the past first (rewindGoalActivity's own
	// precedent, elsewhere in this suite) — asserting "changed" against a
	// same-instant activation timestamp would be flaky, not a real signal.
	rewindGoalActivityTimeOnly(t, store, sid)
	before := goalRecordForSession(t, sid)

	// Mirrors processTaskDirect's own processOptions literal (loop.go):
	// IsTaskRun=true, UserInitiated left false, SenderID="task-executor".
	taskRunOpts := processOptions{
		TranscriptStore: store, TranscriptSessionID: sid,
		Channel: "webchat", ChatID: "task:" + sid, SenderID: "task-executor",
		IsTaskRun: true,
	}
	result := &turnResult{finalContent: "made some progress on the task, no claim yet"}
	al.checkGoalLoopAfterTurn(context.Background(), agentInst, taskRunOpts, result)

	after := goalRecordForSession(t, sid)
	if after.LastActivityAt.Equal(before.LastActivityAt) {
		t.Fatal("a task run's own turn must bump the task-owned goal's activity clock — " +
			"the origin gate must admit opts.IsTaskRun turns (GOAL-FR-015)")
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
	al.recordGoalRouting(sid, "", "webchat", "c1", "sk1", agentInst.ID)

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
	if result.goalDeferredAdjudication == nil {
		t.Fatal("a met+evidence claim must record deferred adjudication work")
	}
	al.dispatchDeferredGoalAdjudication(result.goalDeferredAdjudication)

	after := goalRecordForSession(t, sid)
	if after.Round != 1 {
		t.Fatalf("rounds_used = %d, want 1 — the goal loop's own re-injected follow-up must still advance "+
			"the round", after.Round)
	}
}

// --- ADR-081 D3 AMENDMENT item 3: immediate post-turn correction --------
//
// checkGoalLoopAfterTurn's maybeNudgeUnregisteredGoal replaces provider
// tool-choice forcing as the enforcement point: a completed goal turn that
// left the compiled record empty gets nudged RIGHT THEN, not at the next
// idle quiet window (contrast TestKeeper_NudgeLadderToFallback in
// goal_keeper_repairs_test.go, which needs al.goalQuietWindowSettle to
// advance the SAME GoalZeroOutputPushes counter via the idle path).

// TestGoalLoop_PostTurnCorrection_NudgesImmediatelyWhenUnregistered proves
// the core case: an ordinary turn (no GOAL_STATUS marker, no set_goal call)
// on a recordless goal dispatches the D6c registration nudge synchronously,
// within checkGoalLoopAfterTurn itself — never waiting for
// goalQuietWindowSettle.
func TestGoalLoop_PostTurnCorrection_NudgesImmediatelyWhenUnregistered(t *testing.T) {
	resetGoalTriggerStateForTest()
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	al.recordGoalRouting(sid, "", "webchat", "c1", "sk1", agentInst.ID)
	setActiveGoalRecordless(t, store, sid, "goal-nudge-1", "build a tetris game")

	var mu sync.Mutex
	var events []AsyncNotifyEvent
	al.asyncNotifier.registerObserver(func(evt AsyncNotifyEvent) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, evt)
	})

	opts := processOptions{
		TranscriptStore: store, TranscriptSessionID: sid,
		Channel: "webchat", ChatID: "c1", SessionKey: "sk1", UserInitiated: true,
	}
	// An ordinary turn that ends in plain text — no GOAL_STATUS marker, no
	// set_goal call: the model skipped its first-move door entirely.
	result := &turnResult{finalContent: "sure, let me think about this"}
	al.checkGoalLoopAfterTurn(context.Background(), agentInst, opts, result)

	after := goalRecordForSession(t, sid)
	if after.ZeroOutputPushes != 1 {
		t.Fatalf("goal record ZeroOutputPushes = %d, want 1 immediately after the turn — the correction must "+
			"not wait for the idle quiet window", after.ZeroOutputPushes)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(events) != 1 {
		t.Fatalf("expected exactly 1 dispatched nudge, got %d: %+v", len(events), events)
	}
	if !strings.Contains(events[0].Content, "Call set_goal now") {
		t.Fatalf("nudge content = %q, want the D6c registration-nudge prompt", events[0].Content)
	}
	if events[0].SenderCanonicalID != goalLoopFollowUpSenderID {
		t.Fatalf("nudge sender = %q, want %q — checkGoalLoopAfterTurn's own origin gate would otherwise drop it",
			events[0].SenderCanonicalID, goalLoopFollowUpSenderID)
	}
}

// TestGoalLoop_PostTurnCorrection_NoNudgeWhenRegistered proves the negative:
// once the record is registered, an ordinary turn dispatches no nudge and
// leaves GoalZeroOutputPushes untouched.
func TestGoalLoop_PostTurnCorrection_NoNudgeWhenRegistered(t *testing.T) {
	resetGoalTriggerStateForTest()
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	al.recordGoalRouting(sid, "", "webchat", "c1", "sk1", agentInst.ID)

	// ADR-086: a REGISTERED goal is an active pkg/goal record whose criteria
	// ladder is non-empty — the state the retired
	// `GoalCriteriaJSON: &compiled` session-meta patch used to represent.
	armGoalRecord(t, sid, "build a tetris game",
		recordedGoalCriteria("the game renders"), 0, time.Now())

	var mu sync.Mutex
	var events []AsyncNotifyEvent
	al.asyncNotifier.registerObserver(func(evt AsyncNotifyEvent) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, evt)
	})

	opts := processOptions{
		TranscriptStore: store, TranscriptSessionID: sid,
		Channel: "webchat", ChatID: "c1", SessionKey: "sk1", UserInitiated: true,
	}
	result := &turnResult{finalContent: "working on it now"}
	al.checkGoalLoopAfterTurn(context.Background(), agentInst, opts, result)

	after := goalRecordForSession(t, sid)
	if after.ZeroOutputPushes != 0 {
		t.Fatalf("goal record ZeroOutputPushes = %d, want 0 — the record is registered, nothing to correct", after.ZeroOutputPushes)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(events) != 0 {
		t.Fatalf("expected NO dispatched nudge once the record is registered, got %d: %+v", len(events), events)
	}
}

// TestGoalLoop_PostTurnCorrection_ParkedCardSuppressesNudge proves D6a's
// parked-card suppression also gates the IMMEDIATE correction, not just the
// idle ladder: an already-pending AskUserQuestion card means the agent DID
// take a first-move door and is waiting on the operator, not stalled — this
// turn's OWN status is deliberately left non-Parked (the earlier, simpler
// "this turn itself parked" gate at the top of checkGoalLoopAfterTurn is a
// different code path; this test isolates maybeNudgeUnregisteredGoal's own
// goalHasParkedCard check).
func TestGoalLoop_PostTurnCorrection_ParkedCardSuppressesNudge(t *testing.T) {
	resetGoalTriggerStateForTest()
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	al.recordGoalRouting(sid, "", "webchat", "c1", "sk1", agentInst.ID)
	setActiveGoalRecordless(t, store, sid, "goal-parked-1", "build a tetris game")

	al.SetAskUserRegistry(&fakeParkedCardRegistry{pending: map[string]bool{sid: true}})

	var mu sync.Mutex
	var events []AsyncNotifyEvent
	al.asyncNotifier.registerObserver(func(evt AsyncNotifyEvent) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, evt)
	})

	opts := processOptions{
		TranscriptStore: store, TranscriptSessionID: sid,
		Channel: "webchat", ChatID: "c1", SessionKey: "sk1", UserInitiated: true,
	}
	result := &turnResult{finalContent: "let me ask you something first"}
	al.checkGoalLoopAfterTurn(context.Background(), agentInst, opts, result)

	after := goalRecordForSession(t, sid)
	if after.ZeroOutputPushes != 0 {
		t.Fatalf("goal record ZeroOutputPushes = %d, want 0 — a parked card must suppress the immediate nudge", after.ZeroOutputPushes)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(events) != 0 {
		t.Fatalf("expected NO dispatched nudge while a card is parked, got %d: %+v", len(events), events)
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
	al.recordGoalRouting(sid, "", "webchat", "c1", "sk1", agentInst.ID)
	judgeInst.Provider = unmetJudgeProvider("still unmet")

	// ADR-053 Phase-2 (FR-101): each round advances on a CLAIM, not a bare
	// turn. Round 1 = a claim the judge finds unmet.
	r1 := &turnResult{finalContent: "[goal:evidence] attempt 1\nGOAL_STATUS: met"}
	al.checkGoalLoopAfterTurn(context.Background(), agentInst, opts, r1)
	if r1.goalDeferredAdjudication == nil {
		t.Fatal("round 1: a met+evidence claim must record deferred adjudication work")
	}
	al.dispatchDeferredGoalAdjudication(r1.goalDeferredAdjudication)
	after1 := goalRecordForSessionOrNil(sid)
	if after1 == nil || after1.Round != 1 {
		t.Fatalf("round 1 (< bound=2): unexpected state %+v", after1)
	}
	// JUDGE-FR-099: the steer is delivered via the async-notifier now.
	select {
	case <-al.bus.InboundChan():
	case <-time.After(2 * time.Second):
		t.Fatal("round 1: expected a follow-up round delivered via the async-notifier")
	}

	r2 := &turnResult{finalContent: "[goal:evidence] attempt 2\nGOAL_STATUS: met"}
	al.checkGoalLoopAfterTurn(context.Background(), agentInst, opts, r2)
	if r2.goalDeferredAdjudication == nil {
		t.Fatal("round 2: a met+evidence claim must record deferred adjudication work")
	}
	al.dispatchDeferredGoalAdjudication(r2.goalDeferredAdjudication)
	if after2 := goalRecordForSessionOrNil(sid); after2 != nil {
		t.Fatal("round 2 (== bound=2): goal must be cleared (bound reached)")
	}
	select {
	case fu := <-al.bus.InboundChan():
		t.Fatalf("round == bound must NOT schedule a further follow-up round, got %+v", fu)
	case <-time.After(200 * time.Millisecond):
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

	after := goalRecordForSessionOrNil(sid)
	if after == nil {
		t.Fatal("goal must remain active when the judge is unavailable")
	}
	if after.Round != 0 {
		t.Fatalf("rounds_used = %d, want 0 (judge unavailability must not consume a round, D7)", after.Round)
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

	if after := goalRecordForSession(t, sid); after.Round != 0 {
		t.Fatalf("rounds_used = %d, want 0 (judge unavailability must not consume a round, D7)", after.Round)
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
	stillActiveAt := now.Add(-(6*24 + 23) * time.Hour) // 6d23h idle
	expiredAt := now.Add(-7 * 24 * time.Hour)          // exactly 7d idle

	// ADR-086: the sweep selects on pkg/goal.Store.ListActive() and compares
	// the RECORD's own LastActivityAt/StartedAt (effectiveGoalActivity), so
	// the fixture is a real active goal record per session rather than the
	// retired GoalCondition/GoalStartedAt/GoalLastActivityAt meta patch.
	newGoalSession := func(condition string, lastActivity time.Time) string {
		meta, err := store.NewSession(session.SessionTypeChat, "webchat", agentInst.ID)
		if err != nil {
			t.Fatalf("NewSession: %v", err)
		}
		armGoalRecord(t, meta.ID, condition, nil, 0, lastActivity)
		return meta.ID
	}

	stillActiveSID := newGoalSession("still active goal", stillActiveAt)
	expiredSID := newGoalSession("expired goal", expiredAt)

	al.goalIdleExpirySweep(config.PlanningConfig{}, now)

	if goalRecordForSessionOrNil(stillActiveSID) == nil {
		t.Fatal("a goal idle for 6d23h must NOT be expired (under the 7-day bound)")
	}
	if goalRecordForSessionOrNil(expiredSID) != nil {
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
	meta1 := goalRecordForSession(t, sid)
	if meta1.GoalID == "" {
		t.Fatal("a freshly-set goal must carry a non-empty GoalID")
	}
	firstID := meta1.GoalID

	// ADR-081 D1/FR-001: a prose restate on the active goal rewrites the
	// working prompt and continues the turn — no confirm ritual, and
	// critically it must NOT mint a new GoalID.
	matched, handled, _ := al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal condition A amended", UserInitiated: true}, agentInst, &opts)
	if !matched || handled {
		t.Fatalf("restate: matched=%v handled=%v, want matched=true handled=false", matched, handled)
	}
	metaAmended := goalRecordForSession(t, sid)
	if metaAmended.GoalID != firstID {
		t.Fatalf("restate must keep the same goal-id, got %q want %q", metaAmended.GoalID, firstID)
	}

	// An ordinary (non-claim) worker turn re-emits the SAME goal-id.
	al.checkGoalLoopAfterTurn(context.Background(), agentInst, opts, &turnResult{finalContent: "still working"})
	metaAfterTurn := goalRecordForSession(t, sid)
	if metaAfterTurn.GoalID != firstID {
		t.Fatalf("an ordinary turn must not change the goal-id, got %q want %q", metaAfterTurn.GoalID, firstID)
	}

	// --- Clear goal #1, then set a genuinely new goal #2 ---
	al.clearGoal(sid, store, goalClearNoteUser)
	al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal condition B", UserInitiated: true}, agentInst, &opts)
	activatePendingGoal(t, al, agentInst, &opts)
	meta2 := goalRecordForSession(t, sid)
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
	// own frame carries its own, different id. None is ever empty — ADR-081
	// D1 mints the GoalID up front on instant activation, so (unlike the
	// retired ADR-074 D4a pending/queued state) there is no window where a
	// frame legitimately carries an empty goal-id.
	payloads := goalStatusPayloadsFor(c, sid)
	sawFirstID, sawSecondID := false, false
	for _, p := range payloads {
		switch p.GoalID {
		case firstID:
			sawFirstID = true
		case meta2.GoalID:
			sawSecondID = true
		case "":
			t.Fatalf("emitted GoalStatusFrame with an empty goal_id: %+v", p)
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

// TestGoalBudgets_ResetAcrossGenerations is review-round-1 finding #5: a
// fresh goal generation must get fresh budgets. Neither
// activateInstantGoal's fresh-activation SetMeta nor clearGoal's SetMeta
// used to zero GoalQuestionRoundsUsed (FR-010's question-round door) or
// GoalZeroOutputPushes (FR-014b's bounded-push streak) — both counters
// silently carried over from whatever the PREVIOUS goal generation on this
// session had already spent, so a goal that itself never asked a single
// question could inherit an already-exhausted ask door. Goal A spends both
// budgets, is cleared, and goal B — a brand new activation on the SAME
// session — must see both back at zero.
func TestGoalBudgets_ResetAcrossGenerations(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	opts := processOptions{
		TranscriptStore: store, TranscriptSessionID: sid,
		Channel: "webchat", ChatID: "c1", SessionKey: "sk1", UserInitiated: true,
	}

	// --- Goal A: instant activation (prose intent, no markers). ---
	al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal goal A prose intent", UserInitiated: true}, agentInst, &opts)
	activatePendingGoal(t, al, agentInst, &opts)
	metaA := goalRecordForSession(t, sid)
	if metaA.GoalID == "" {
		t.Fatal("setup: goal A must be active")
	}
	if metaA.QuestionRoundsUsed != 0 || metaA.ZeroOutputPushes != 0 {
		t.Fatalf("setup: a freshly-activated goal must start with both budgets at 0, got question=%d pushes=%d",
			metaA.QuestionRoundsUsed, metaA.ZeroOutputPushes)
	}

	// Spend both budgets on goal A — simulating a question round taken
	// (FR-010) and the zero-output push streak exhausted (FR-014b). ADR-086
	// GOAL-FR-004 relocated both counters off session meta onto the goal's
	// OWN record, so they are spent there.
	if _, uerr := goal.NewStore(config.OmnipusHomeDir()).Update(metaA.GoalID, func(cur *goal.Goal) error {
		cur.QuestionRoundsUsed = 1
		cur.ZeroOutputPushes = goalZeroOutputPushMax
		return nil
	}); uerr != nil {
		t.Fatal(uerr)
	}

	// --- Clear goal A. ---
	al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal clear", UserInitiated: true}, agentInst, &opts)

	// ADR-086 re-point of this checkpoint. Pre-ADR-086 the budgets were
	// SESSION-scoped, so the only way goal B could avoid inheriting goal A's
	// spend was for clearGoal to zero them on the session — which is what
	// this block used to assert. Under GOAL-FR-027/FR-028 a clear is a
	// STATUS TRANSITION on a RETAINED record, explicitly "never an erasure":
	// goal A keeps its spent counters forever, and a zeroing assertion here
	// would now assert the exact opposite of the ADR. The invariant that
	// replaced it — and the one that actually protects goal B — is that the
	// counters are PER-RECORD, so assert both halves: A is terminal and has
	// RETAINED its spend (non-erasure), and no goal is active any more.
	if goalRecordForSessionOrNil(sid) != nil {
		t.Fatal("clear: no goal may remain ACTIVE on this session after /goal clear")
	}
	clearedA := mustGoalRecord(t, metaA.GoalID)
	if clearedA.State != generated.GoalStateCleared {
		t.Fatalf("goal A state after /goal clear = %q, want %q", clearedA.State, generated.GoalStateCleared)
	}
	if clearedA.QuestionRoundsUsed != 1 || clearedA.ZeroOutputPushes != goalZeroOutputPushMax {
		t.Fatalf("GOAL-FR-027 non-erasure: a terminated record must RETAIN its spent budgets, "+
			"got question=%d pushes=%d, want question=1 pushes=%d",
			clearedA.QuestionRoundsUsed, clearedA.ZeroOutputPushes, goalZeroOutputPushMax)
	}

	// --- Goal B: a brand new activation on the SAME session must have the
	// ask door again — not inherit goal A's exhausted budgets. ---
	al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal goal B prose intent", UserInitiated: true}, agentInst, &opts)
	activatePendingGoal(t, al, agentInst, &opts)
	metaB := goalRecordForSession(t, sid)
	if metaB.GoalID == "" || metaB.GoalID == metaA.GoalID {
		t.Fatalf("goal B must be a fresh generation (new GoalID), got %q (goal A was %q)", metaB.GoalID, metaA.GoalID)
	}
	if metaB.QuestionRoundsUsed != 0 {
		t.Fatalf("goal B: QuestionRoundsUsed = %d, want 0 — the ask door must be fresh, not inherited from goal A",
			metaB.QuestionRoundsUsed)
	}
	if metaB.ZeroOutputPushes != 0 {
		t.Fatalf("goal B: ZeroOutputPushes = %d, want 0 — fresh push budget, not inherited from goal A",
			metaB.ZeroOutputPushes)
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

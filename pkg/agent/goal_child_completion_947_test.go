package agent

// RED tests for GitHub #947 defect 1 ("the hang"): a goal-bearing delegated
// (steered) child whose turn has ended stays at lifecycle state `running`
// for ever — the parent never gets a completion, the child never leaves
// `running`.
//
// SPEC (the only oracle source): the approved design note at
// coordination/logs/fix890-opus/lc947-defect1-design-note.md — decisions
// (a)-(e) plus founder decisions FD1=A (a session-owned goal ends with its
// session, and the outcome records why) and FD2=A (no wall-clock reaper).
// Every expected value below derives from that note, never from the
// pre-fix implementation.
//
// RED protocol (elicify-test-writing, RED mode): each test is observed red
// on the pre-fix code for the stranding reason; the green run and the
// mutation proof belong to CHECK and are deliberately not run here.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// launchGoalBearingChild947 launches one goal-bearing delegated child of
// parentID through the production launcher with the design note's two-part
// goal spec (one criterion, one DoD), then persists its lifecycle record at
// `running` — the strand shape: the record never leaves running today, so
// the RED assertions below read it back after the turn has already ended.
func launchGoalBearingChild947(
	t *testing.T, al *AgentLoop, parentID, callID string,
) *session.LifecycleRecord {
	t.Helper()
	res, err := NewSteerLauncher(al).Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: parentID,
		TargetAgentID:     "native-agent",
		Task:              "prove the delegated goal",
		Origin:            steer.Origin{Kind: steer.OriginKindDelegate, CallID: callID},
		Goal: &steer.GoalSpec{
			Criteria: []steer.Criterion{{Text: "the work is complete"}},
			DoD:      []steer.Criterion{{Text: "the evidence is sufficient"}},
		},
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	rec, err := al.GetSessionLifecycleStore().Load(res.SessionID)
	if err != nil {
		t.Fatalf("Load(child): %v", err)
	}
	rec.State = session.LifecycleRunning
	if persistErr := al.GetSessionLifecycleStore().Persist(rec); persistErr != nil {
		t.Fatalf("Persist(running): %v", persistErr)
	}
	return rec
}

// metGoalVerdict947 builds the Judge's met verdict JSON over every criterion
// and DoD id the goal record actually carries (the same shape the verifier
// is instructed to return, applied to this goal's own ladder).
func metGoalVerdict947(t *testing.T, rec *session.LifecycleRecord) string {
	t.Helper()
	g, err := resolveGoalRecordStore().Get(rec.GoalRef)
	if err != nil {
		t.Fatalf("Get(goal %q): %v", rec.GoalRef, err)
	}
	type criterionVerdict struct {
		ID     string `json:"id"`
		Met    bool   `json:"met"`
		Reason string `json:"reason"`
	}
	verdicts := make([]criterionVerdict, 0, len(g.Criteria)+len(g.DoD))
	for _, criterion := range append(append([]task.AcceptanceCriterion{}, g.Criteria...), g.DoD...) {
		verdicts = append(verdicts, criterionVerdict{ID: criterion.ID, Met: true, Reason: "verified"})
	}
	body, err := json.Marshal(map[string]any{"met": true, "criteria": verdicts})
	if err != nil {
		t.Fatalf("Marshal(verdict): %v", err)
	}
	return string(body)
}

// TestGoalChildCompletion947_MetArmCompletesChildAndDeliversHandback
// (design note decisions (a)+(b)): when the Judge returns met, the met arm
// must run the completion tail — write the child terminal `completed`,
// pair-end the session, AND deliver the completion handback — in addition to
// the goal_status verdict. Today the met arm writes the goal record only:
// the child stays `running` and the parent never receives the handback
// (the hang).
func TestGoalChildCompletion947_MetArmCompletesChildAndDeliversHandback(t *testing.T) {
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	lifecycle := session.NewLifecycleStore(t.TempDir())
	inbox := session.NewMessageInboxStore(t.TempDir())
	al.SetSessionMessagingStores(inbox, lifecycle)
	wireSteerCompletionDeps(t, al)

	parentMeta, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "native-agent")
	if err != nil {
		t.Fatalf("NewSession(parent): %v", err)
	}
	rec := launchGoalBearingChild947(t, al, parentMeta.ID, "call-947-met")

	judgeInst.Provider = &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return &providers.LLMResponse{Content: metGoalVerdict947(t, rec)}, nil
	}}

	ts, err := al.reconstructSteeredTurn(rec, nil)
	if err != nil {
		t.Fatalf("reconstructSteeredTurn: %v", err)
	}
	if !ts.opts.UserInitiated {
		t.Fatal("first steered turn is not marked user-initiated; goal claim would be ignored")
	}
	done := make(chan string, 1)
	oldDone := goalDeferredAdjudicationDoneFn
	goalDeferredAdjudicationDoneFn = func(sessionID string) { done <- sessionID }
	t.Cleanup(func() { goalDeferredAdjudicationDoneFn = oldDone })

	result := turnResult{finalContent: "[goal:evidence] verified the work\nGOAL_STATUS: met"}
	al.finishSteeredGoalTurn(ts, rec, &result, nil)
	select {
	case got := <-done:
		if got != rec.SessionID {
			t.Fatalf("adjudicated session = %q, want %q", got, rec.SessionID)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for delegated goal adjudication")
	}

	got, err := lifecycle.Load(rec.SessionID)
	if err != nil {
		t.Fatalf("Load(child after met): %v", err)
	}
	if got.State != session.LifecycleCompleted || !got.Terminal() {
		t.Errorf("child state after met verdict = %q (terminal=%v), want completed — "+
			"the met arm leaves the goal-bearing child stranded at running",
			got.State, got.Terminal())
	}

	messages, _, _, err := inbox.Drain(parentMeta.ID, rec.SessionID, "", 10)
	if err != nil {
		t.Fatalf("Drain(parent): %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("unacknowledged parent messages after met verdict = %d, want exactly the final handback", len(messages))
	}
	var sawHandback bool
	for _, msg := range messages {
		// Kind-gate FIRST: the generated As* accessors are lenient (a
		// goal_status message carries the shared message_id field, so
		// AsSessionMessageHandback decodes it into a hollow handback) —
		// only the envelope's own kind selects the variant.
		class, cerr := session.ClassifySessionMessage(msg)
		if cerr != nil {
			t.Fatalf("ClassifySessionMessage: %v", cerr)
		}
		switch class.Kind {
		case "handback":
			v, herr := msg.AsSessionMessageHandback()
			if herr != nil {
				t.Fatalf("AsSessionMessageHandback: %v", herr)
			}
			sawHandback = true
			wantID := fmt.Sprintf("%s:%d:final", rec.SessionID, rec.Generation)
			if v.MessageId != wantID {
				t.Errorf("handback MessageId = %q, want %q (deterministic <child>:<gen>:final)", v.MessageId, wantID)
			}
			if v.Mode != generated.SessionMessageHandbackModeFinal {
				t.Errorf("handback mode = %q, want final", v.Mode)
			}
			if strings.TrimSpace(v.ResultSoFar) == "" {
				t.Errorf("handback ResultSoFar is empty, want the child's final answer")
			}
		}
	}
	if !sawHandback {
		t.Errorf("no completion handback reached the parent inbox — the parent waits on a worker whose turn already ended")
	}
}

// TestGoalChildCompletion947_JudgeUnavailableFailsChildAfterThreeAttempts
// (design note decision (c)): with the Judge unavailable on every attempt,
// the deferred adjudication is re-driven a bounded number of times — three —
// and then the child is failed visibly: terminal `failed` with a reason
// naming the judge, a wake-eligible error to the parent inbox, and the goal
// record ended. Today there is no re-drive and no delivery: one unavailable
// round leaves the child at `running` and the goal active for ever.
//
// Attempt-counting mechanism: JudgeCriteria's internal D7 retry would
// otherwise re-call the provider an implementation-defined number of times
// per attempt. Substituting a judgeSleepFn that returns a non-nil error
// makes judgeBackoffWait fail on its first wait, so runVerifierAdjudication
// returns Unavailable after EXACTLY one provider call per adjudication
// attempt — the stub's call count is then the re-drive attempt count, and
// the note's expected value is 3.
func TestGoalChildCompletion947_JudgeUnavailableFailsChildAfterThreeAttempts(t *testing.T) {
	origSleep, origBackoff := judgeSleepFn, judgeRetryBackoff
	t.Cleanup(func() { judgeSleepFn, judgeRetryBackoff = origSleep, origBackoff })
	judgeRetryBackoff = []time.Duration{time.Millisecond}
	judgeSleepFn = func(context.Context, time.Duration) error {
		return errors.New("test: no wait between judge attempts")
	}

	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	lifecycle := session.NewLifecycleStore(t.TempDir())
	inbox := session.NewMessageInboxStore(t.TempDir())
	al.SetSessionMessagingStores(inbox, lifecycle)
	wireSteerCompletionDeps(t, al)

	parentMeta, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "native-agent")
	if err != nil {
		t.Fatalf("NewSession(parent): %v", err)
	}
	rec := launchGoalBearingChild947(t, al, parentMeta.ID, "call-947-judge-unavailable")

	judge := &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return nil, errors.New("simulated judge outage: provider unreachable")
	}}
	judgeInst.Provider = judge

	ts, err := al.reconstructSteeredTurn(rec, nil)
	if err != nil {
		t.Fatalf("reconstructSteeredTurn: %v", err)
	}
	done := make(chan string, 1)
	oldDone := goalDeferredAdjudicationDoneFn
	goalDeferredAdjudicationDoneFn = func(sessionID string) { done <- sessionID }
	t.Cleanup(func() { goalDeferredAdjudicationDoneFn = oldDone })

	result := turnResult{finalContent: "[goal:evidence] verified the work\nGOAL_STATUS: met"}
	al.finishSteeredGoalTurn(ts, rec, &result, nil)
	select {
	case got := <-done:
		if got != rec.SessionID {
			t.Fatalf("adjudicated session = %q, want %q", got, rec.SessionID)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for delegated goal adjudication")
	}

	if calls := judge.callCount(); calls != 3 {
		t.Errorf("judge provider calls = %d, want 3 — the design note bounds the re-drive at "+
			"three attempts before failing the child; today there is no re-drive at all", calls)
	}

	got, err := lifecycle.Load(rec.SessionID)
	if err != nil {
		t.Fatalf("Load(child after judge outage): %v", err)
	}
	if got.State != session.LifecycleFailed || !got.Terminal() {
		t.Errorf("child state after judge outage = %q (terminal=%v), want failed — "+
			"an unavailable judge strands the goal-bearing child at running", got.State, got.Terminal())
	}
	if reason := strings.ToLower(got.FailedReason); !strings.Contains(reason, "judge unavailable") {
		t.Errorf("child FailedReason = %q, want it to name \"judge unavailable\"", got.FailedReason)
	}

	messages, _, _, err := inbox.Drain(parentMeta.ID, rec.SessionID, "", 10)
	if err != nil {
		t.Fatalf("Drain(parent): %v", err)
	}
	var sawWakeEligibleJudgeError bool
	for _, msg := range messages {
		class, cerr := session.ClassifySessionMessage(msg)
		if cerr != nil {
			t.Fatalf("ClassifySessionMessage: %v", cerr)
		}
		if class.Kind != "error" || !class.WakeEligible || !class.Fatal {
			continue
		}
		v, aerr := msg.AsSessionMessageError()
		if aerr != nil {
			t.Fatalf("AsSessionMessageError: %v", aerr)
		}
		if strings.Contains(strings.ToLower(v.Text), "judge") {
			sawWakeEligibleJudgeError = true
		}
	}
	if !sawWakeEligibleJudgeError {
		t.Errorf("no wake-eligible fatal error naming the judge reached the parent inbox "+
			"(%d messages drained) — the outage is silent to the parent", len(messages))
	}

	g, gerr := resolveGoalRecordStore().Get(rec.GoalRef)
	if gerr != nil {
		t.Fatalf("Get(goal): %v", gerr)
	}
	if !goal.IsTerminalState(g.State) {
		t.Errorf("goal record state after judge outage = %q, want terminal — "+
			"the note ends the goal record when the re-drive is exhausted", g.State)
	}
}

// TestGoalChildCompletion947_ParkedQuestionDeliveredToParent (design note
// decision (d), waiting_on_user arm): a goal-bearing child that parks on the
// user must deliver its parked-question outcome upward — the parent needs a
// wake-eligible question, not silence. Today the park arm records the claim
// and shows the pill but delivers nothing.
//
// Drive shape: the turn ends with a normal completion status and the prose
// marker GOAL_STATUS: waiting_on_user, which resolves the claim in
// handleOutcome's waiting_on_user arm — the note's own fix site. (Ending the
// turn AT TurnEndStatusParked instead would short-circuit checkEligibility
// and never reach the arm.)
//
// NOT asserted on purpose: moving the child's lifecycle record to
// NeedsInput is the note's RECOMMENDATION, not a decided behaviour — the
// decision covers the upward delivery only. A CHECK-stage decision may add
// it; RED does not assert recommendations.
func TestGoalChildCompletion947_ParkedQuestionDeliveredToParent(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	lifecycle := session.NewLifecycleStore(t.TempDir())
	inbox := session.NewMessageInboxStore(t.TempDir())
	al.SetSessionMessagingStores(inbox, lifecycle)
	wireSteerCompletionDeps(t, al)

	parentMeta, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "native-agent")
	if err != nil {
		t.Fatalf("NewSession(parent): %v", err)
	}
	rec := launchGoalBearingChild947(t, al, parentMeta.ID, "call-947-park")

	ts, err := al.reconstructSteeredTurn(rec, nil)
	if err != nil {
		t.Fatalf("reconstructSteeredTurn: %v", err)
	}
	result := turnResult{
		finalContent: "I need your call on the storage format before I can continue\nGOAL_STATUS: waiting_on_user",
	}
	al.finishSteeredGoalTurn(ts, rec, &result, nil)

	messages, _, _, err := inbox.Drain(parentMeta.ID, rec.SessionID, "", 10)
	if err != nil {
		t.Fatalf("Drain(parent): %v", err)
	}
	var sawQuestion bool
	for _, msg := range messages {
		class, cerr := session.ClassifySessionMessage(msg)
		if cerr != nil {
			t.Fatalf("ClassifySessionMessage: %v", cerr)
		}
		if class.Kind != "question" || !class.WakeEligible {
			continue
		}
		v, aerr := msg.AsSessionMessageQuestion()
		if aerr != nil {
			t.Fatalf("AsSessionMessageQuestion: %v", aerr)
		}
		if strings.TrimSpace(v.Text) != "" {
			sawQuestion = true
		}
	}
	if !sawQuestion {
		t.Errorf("no wake-eligible parked-question message reached the parent inbox "+
			"(%d messages drained) — a parked goal-bearing child is silent to its parent", len(messages))
	}
}

// TestGoalChildCompletion947_CancelEndsSessionOwnedGoal (founder decision
// FD1=A): cancelling a goal-bearing steered child ends its session-owned
// goal record — terminal, with the terminal reason recording the
// cancellation. Today the cancel cascade stamps and terminalises the
// session but never touches the goal record: the goal stays active for
// ever.
func TestGoalChildCompletion947_CancelEndsSessionOwnedGoal(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	lifecycle := session.NewLifecycleStore(t.TempDir())
	inbox := session.NewMessageInboxStore(t.TempDir())
	al.SetSessionMessagingStores(inbox, lifecycle)
	wireSteerCompletionDeps(t, al)

	parentMeta, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "native-agent")
	if err != nil {
		t.Fatalf("NewSession(parent): %v", err)
	}
	rec := launchGoalBearingChild947(t, al, parentMeta.ID, "call-947-cancel")

	// Premise (holds today): the cancel cascade reaches the never-ran
	// goal-bearing child and terminalises its record at `cancelled`.
	report, err := al.steerCanceller().CancelSubtree(context.Background(), rec.SessionID, steer.Principal{
		Kind: steer.PrincipalKindHuman, ID: "qa-red-947",
	})
	if err != nil {
		t.Fatalf("CancelSubtree: %v", err)
	}
	reached := false
	for _, id := range report.Reached {
		if id == rec.SessionID {
			reached = true
		}
	}
	if !reached {
		t.Fatalf("cancel report reached = %v, want it to include the child %q", report.Reached, rec.SessionID)
	}
	got, err := lifecycle.Load(rec.SessionID)
	if err != nil {
		t.Fatalf("Load(child after cancel): %v", err)
	}
	if got.State != session.LifecycleCancelled || !got.Terminal() {
		t.Fatalf("child state after cancel = %q (terminal=%v), want cancelled — "+
			"the cancel cascade itself is broken; this premise failure is NOT the #947 RED signal",
			got.State, got.Terminal())
	}

	// RED assertion (FD1=A): the session-owned goal record ends with its
	// session. No terminal goal-state value named "cancelled" exists in the
	// wire contract (generated.GoalState has none), so the oracle is the
	// contract-level one: terminal, with the reason recording why.
	g, gerr := resolveGoalRecordStore().Get(rec.GoalRef)
	if gerr != nil {
		t.Fatalf("Get(goal): %v", gerr)
	}
	if !goal.IsTerminalState(g.State) {
		t.Errorf("goal record state after session cancel = %q, want terminal — "+
			"FD1=A: a session-owned goal ends with its session", g.State)
	}
	if reason := strings.ToLower(g.TerminalReason); !strings.Contains(reason, "cancel") {
		t.Errorf("goal TerminalReason = %q, want it to record the cancellation (FD1=A: the outcome records why)",
			g.TerminalReason)
	}
}

// casRejectionRegistrySpy is the minimal VerifierSessionPublisher double
// whose Register ALWAYS loses the CAS (ErrVerifierSessionHeld). It does NOT
// implement the richer VerifierSessionRegistry interface, so
// runVerifierAdjudication's Lookup pre-check is skipped and the back-off is
// exercised through the atomic Register rejection itself — the residual
// check-then-act arm the pre-check cannot close.
type casRejectionRegistrySpy struct{}

func (s *casRejectionRegistrySpy) Register(unit, verifierSessionID string) error {
	return ErrVerifierSessionHeld
}

func (s *casRejectionRegistrySpy) Unregister(unit string) {}

// TestGoalChildCompletion947_ConcurrencyBackoffNeverFailsChild is #984's
// regression test (the release red on TestConcurrentDeferredClaims_
// OneJudge_corrMAJOR3): when runGoalAdjudication's Unavailable outcome is
// the verifier registry's "concurrent adjudication in flight" BACK-OFF
// (corr-MAJOR-3/G-1) — not a judge outage — the deferred re-drive must stop
// silently: no retry (the in-flight adjudication resolves the goal), no
// judge_unavailable pill, and critically NO completeSteeredTurnAfterGoal
// failure. Pre-fix, the re-drive read every Unavailable as an outage: the
// losing adjudication waited out the backoff, invoked the Judge a SECOND
// time once the winner unregistered (violating G-1 exactly-once), and — the
// false failure this test pins — after goalJudgeRedriveAttempts it failed
// the child terminal `failed` with "judge unavailable" and sent the parent
// a wake-eligible error, while the goal record stayed active with the
// winner's own verdict unmet.
//
// Deterministic forcing (no sleeps as mechanism, no goroutine race): each
// subtest pre-arms the registry so the loser's FIRST attempt is rejected —
// the Lookup pre-check arm via a held live entry, the CAS Register arm via
// a spy that always loses the compare-and-set — and judgeSleepFn is
// substituted with an erroring stub so a regressed re-drive retries
// immediately instead of waiting out the real 5s/15s/30s schedule. The
// judge provider counts calls: the loser must never reach it (0 calls),
// and the child must still be non-terminal `running` with no wake-eligible
// parent error and an ACTIVE goal record afterwards.
func TestGoalChildCompletion947_ConcurrencyBackoffNeverFailsChild(t *testing.T) {
	origSleep := judgeSleepFn
	t.Cleanup(func() { judgeSleepFn = origSleep })
	judgeSleepFn = func(context.Context, time.Duration) error {
		return errors.New("test: no wait between re-drive attempts")
	}

	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	lifecycle := session.NewLifecycleStore(t.TempDir())
	inbox := session.NewMessageInboxStore(t.TempDir())
	al.SetSessionMessagingStores(inbox, lifecycle)
	wireSteerCompletionDeps(t, al)

	// Unmet verdict: exactly the winner outcome that kept the goal record
	// active pre-fix, letting the loser's retries continue into the false
	// child failure. The provider also counts calls — the back-off must stop
	// the re-drive before the loser ever reaches the Judge (0 calls).
	judge := &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return &providers.LLMResponse{Content: `{"met": false, "criteria": []}`}, nil
	}}
	judgeInst.Provider = judge

	launch := func(t *testing.T, callID string) (*session.LifecycleRecord, string) {
		t.Helper()
		parentMeta, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "native-agent")
		if err != nil {
			t.Fatalf("NewSession(parent): %v", err)
		}
		rec := launchGoalBearingChild947(t, al, parentMeta.ID, callID)
		return rec, parentMeta.ID
	}

	driveAndAssert := func(t *testing.T, rec *session.LifecycleRecord, parentID string) {
		t.Helper()
		ts, err := al.reconstructSteeredTurn(rec, nil)
		if err != nil {
			t.Fatalf("reconstructSteeredTurn: %v", err)
		}
		done := make(chan string, 1)
		oldDone := goalDeferredAdjudicationDoneFn
		goalDeferredAdjudicationDoneFn = func(sessionID string) { done <- sessionID }
		t.Cleanup(func() { goalDeferredAdjudicationDoneFn = oldDone })

		result := turnResult{finalContent: "[goal:evidence] verified the work\nGOAL_STATUS: met"}
		al.finishSteeredGoalTurn(ts, rec, &result, nil)
		select {
		case got := <-done:
			if got != rec.SessionID {
				t.Fatalf("adjudicated session = %q, want %q", got, rec.SessionID)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("timed out waiting for the deferred adjudication's re-drive to stop")
		}

		if calls := judge.callCount(); calls != 0 {
			t.Errorf("judge provider calls = %d, want 0 — a concurrency back-off is a back-off, not an "+
				"outage: the re-drive must stop instead of re-invoking the Judge", calls)
		}
		got, err := lifecycle.Load(rec.SessionID)
		if err != nil {
			t.Fatalf("Load(child after back-off): %v", err)
		}
		if got.State != session.LifecycleRunning || got.Terminal() {
			t.Errorf("child state after concurrency back-off = %q (terminal=%v), want still running — "+
				"a back-off must never fail the child with \"judge unavailable\"", got.State, got.Terminal())
		}
		messages, _, _, err := inbox.Drain(parentID, rec.SessionID, "", 10)
		if err != nil {
			t.Fatalf("Drain(parent): %v", err)
		}
		for _, msg := range messages {
			class, cerr := session.ClassifySessionMessage(msg)
			if cerr != nil {
				t.Fatalf("ClassifySessionMessage: %v", cerr)
			}
			if class.Kind == "error" && class.WakeEligible && class.Fatal {
				t.Errorf("wake-eligible fatal error reached the parent inbox on a concurrency back-off — " +
					"the parent must not be told the judge failed; this is not an outage")
			}
		}
		g, gerr := resolveGoalRecordStore().Get(rec.GoalRef)
		if gerr != nil {
			t.Fatalf("Get(goal): %v", gerr)
		}
		if !goal.IsActiveState(g.State) {
			t.Errorf("goal record state after concurrency back-off = %q, want active — the in-flight "+
				"adjudication owns the goal's fate; the back-off must not end it", g.State)
		}
	}

	t.Run("registry lookup pre-check", func(t *testing.T) {
		rec, parentID := launch(t, "call-947-concurrent-lookup")
		// A held LIVE entry stands in for the winner adjudication in flight.
		unit := verifierUnitForGoal(rec.SessionID)
		if err := currentVerifierSessionRegistry().Register(unit, "concurrent-winner-in-flight"); err != nil {
			t.Fatalf("Register(winner placeholder): %v", err)
		}
		t.Cleanup(func() { currentVerifierSessionRegistry().Unregister(unit) })
		driveAndAssert(t, rec, parentID)
	})

	t.Run("register CAS rejection", func(t *testing.T) {
		rec, parentID := launch(t, "call-947-concurrent-cas")
		SetVerifierSessionRegistry(&casRejectionRegistrySpy{})
		t.Cleanup(func() { SetVerifierSessionRegistry(nil) })
		driveAndAssert(t, rec, parentID)
	})
}

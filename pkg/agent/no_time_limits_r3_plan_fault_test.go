package agent

// PR #1311 / RED dispatch G3 (T3). The real engine admits its round from
// real records. Its OnChange filesystem boundary makes inputs unreadable
// BEFORE the admitted goroutine runs and keeps them unreadable through
// outcome application. judgeRoundReadFault is deliberately never used.
// A broken plan file cannot be the only route for recording its own failure.
// CHECK mutations: silently return on the second read; leak claim/slot;
// restart Judge rounds after the consecutive-unavailable park bound.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/task"
)

type r3PlanHarness struct {
	al     *AgentLoop
	pe     *PlanEngine
	plans  *plan.Store
	tasks  *task.Store
	judge  *fakeJudgeProvider
	owner  string
	p      *plan.Plan
	member *task.Task
	events *eventCollector
	stop   func()
}

func newR3PlanHarness(t *testing.T) *r3PlanHarness {
	t.Helper()
	oldRegistry := currentVerifierSessionRegistry()
	t.Cleanup(func() { SetVerifierSessionRegistry(oldRegistry) })
	supervisorHome := t.TempDir()
	al, judgeInst := newGoalLoopTestLoop(t, newRecordingTurnProvider(), func(cfg *config.Config) {
		cfg.Judge.TimeoutSec = 600 // legal inner verifier bound, also used by G4
		cfg.Agents.List = append(cfg.Agents.List, config.AgentConfig{
			ID: planSupervisorAgentID, Name: "Plan Supervisor", Type: config.AgentTypeSystem, Home: supervisorHome,
		})
	})
	installTestSessionLauncher(t, al)
	ps, ts := plan.New(filepath.Join(t.TempDir(), "plans")), task.New(filepath.Join(t.TempDir(), "tasks"))
	pe := NewPlanEngine(al, ps, ts, al.taskExecutor)
	al.SetPlanEngine(pe)
	pe.SetLifecycleStore(al.GetSessionLifecycleStore())
	_, ownerID := newGoalTestSession(t, al, "native-agent")
	p := mustCreatePlan(t, ps, &plan.Plan{ID: "r3-plan", Title: "R3 verified report", WorkspaceID: "default",
		OwnerAgentID: "native-agent", OwnerSessionID: ownerID, State: plan.StateRunning,
		SourceChannel: "webchat", SourceChatID: ownerID,
		DoD: []task.AcceptanceCriterion{planProseCriterion("the final report is supported by the member's evidence")},
	})
	member := mustCreateTask(t, ts, &task.Task{Title: "report member", WorkspaceID: "default", PlanID: p.ID,
		Status: task.StatusDone, Result: "RESULT-R3-PLAN: report.md contains the completed analysis"})
	// Schema-valid provider reply over the INPUT criterion IDs, not an oracle
	// copied from the implementation's output. The met verdict is the recovery control.
	p, err := ps.Get(p.ID)
	if err != nil {
		t.Fatalf("SETUP Get(created plan): %v", err)
	}
	body, err := json.Marshal(map[string]any{"met": true, "criteria": []map[string]any{
		{"id": p.DoD[0].ID, "met": true, "reason": "the report is verified"},
	}})
	if err != nil {
		t.Fatalf("SETUP marshal provider verdict: %v", err)
	}
	judge := &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return &providers.LLMResponse{Content: string(body)}, nil
	}}
	judgeInst.Provider = judge
	events, stop := recordAgentEvents(t, al)
	t.Cleanup(func() { pe.judgeWG.Wait(); pe.wakeWG.Wait() })
	return &r3PlanHarness{al: al, pe: pe, plans: ps, tasks: ts, judge: judge, owner: ownerID,
		p: p, member: member, events: events, stop: stop}
}

func (h *r3PlanHarness) assertRoundReleased(t *testing.T) {
	t.Helper()
	if _, held := h.pe.registry().Lookup(verifierUnitForPlan(h.p.ID)); held {
		t.Error("G3 failed input read leaked the plan Judge claim")
	}
	if got := h.pe.judgeSema.InFlight(); got != 0 {
		t.Errorf("G3 failed input read leaked execution slots: in_flight=%d, want 0", got)
	}
	// Positive instrument control: the semaphore really can observe a held slot.
	ok, release := h.pe.judgeSema.TryAcquire()
	if !ok {
		t.Fatal("G3 failed input read prevented a subsequent execution-slot acquisition")
	}
	if got := h.pe.judgeSema.InFlight(); got != 1 {
		t.Errorf("CONTROL acquired slot reads %d, want 1", got)
	}
	release()
}

func r3HonestUnverifiedText(text string) bool {
	text = strings.ToLower(text)
	// G3 allows either a not-verified conclusion OR an honest undeliverable
	// report. The wording is not being designed by this RED test.
	return strings.Contains(text, "not verified") || strings.Contains(text, "could not be delivered") ||
		strings.Contains(text, "could not be saved") || strings.Contains(text, "has not been scored") ||
		strings.Contains(text, "cannot be adjudicated")
}

func (h *r3PlanHarness) assertIndependentFailureReport(t *testing.T) {
	t.Helper()
	entries, err := h.al.GetSessionStore().ReadTranscript(h.owner)
	if err != nil {
		t.Fatalf("read independent owner transcript: %v", err)
	}
	durable := false
	for _, entry := range entries {
		if r3HonestUnverifiedText(entry.Content) {
			durable = true
		}
	}
	if !durable {
		t.Error("G3 unreadable plan lost its conclusion: no durable Not verified/undeliverable outcome outside the unreadable plan record")
	}
	visible := false
	h.events.mu.Lock()
	for _, event := range h.events.events {
		if p, ok := event.Payload.(ErrorPayload); ok && r3HonestUnverifiedText(p.Message) {
			visible = true
		}
		if p, ok := event.Payload.(NotificationPayload); ok && r3HonestUnverifiedText(p.Body) {
			visible = true
		}
	}
	h.events.mu.Unlock()
	for _, msg := range drainOutbound(h.al.bus) {
		if msg.SessionID == h.owner && r3HonestUnverifiedText(msg.Content) {
			visible = true
		}
	}
	if !visible && !durable {
		t.Error("G3 unreadable plan failed silently: no user-visible Not verified/undeliverable outcome (a server log is not delivery)")
	}
}

func r3PersistentPlanReadFault(t *testing.T) {
	h := newR3PlanHarness(t)
	path := filepath.Join(h.plans.Dir(), h.p.ID+".json")
	var saved []byte
	var faultErr error
	var once sync.Once
	h.plans.OnChange = func(p *plan.Plan) {
		if p.ID == h.p.ID && p.PlanPhase == plan.PhaseJudging {
			once.Do(func() {
				saved, faultErr = os.ReadFile(path) // already persisted at judging
				if faultErr == nil {
					faultErr = os.WriteFile(path, []byte("{unreadable plan\n"), 0o600)
				}
			})
		}
	}
	h.pe.processPlan(context.Background(), h.p.ID)
	h.pe.judgeWG.Wait()
	h.pe.wakeWG.Wait()
	if faultErr != nil || len(saved) == 0 {
		t.Fatalf("SETUP persistent plan fault: saved=%d err=%v", len(saved), faultErr)
	}
	if _, err := h.plans.Get(h.p.ID); err == nil || !strings.Contains(err.Error(), "plan: parse") {
		t.Fatalf("SETUP real plan must remain unreadable through the handler's second read: err=%v", err)
	}
	if h.pe.judgeRoundReadFault != nil {
		t.Fatal("SETUP the healthy-store read-fault seam must not be used")
	}
	h.assertRoundReleased(t)
	if got := h.judge.callCount(); got != 0 {
		t.Errorf("G3 provider calls=%d, want 0 on genuinely unreadable inputs", got)
	}
	// Drain the real event recorder before asserting absence, not merely poll it.
	h.stop()
	h.assertIndependentFailureReport(t)

	// Restore the exact admitted (judging) record. The real crash/retry entry
	// must pick it up, not strand it behind an abandoned claim or execution slot.
	if err := os.WriteFile(path, saved, 0o600); err != nil {
		t.Fatalf("restore plan record: %v", err)
	}
	h.plans.OnChange = nil
	h.pe.processPlan(context.Background(), h.p.ID)
	h.pe.judgeWG.Wait()
	h.pe.wakeWG.Wait()
	after, err := h.plans.Get(h.p.ID)
	if err != nil || after.State != plan.StateDone || after.JudgeRounds != 1 || h.judge.callCount() != 1 {
		t.Errorf("G3 restored-record recovery: plan=%+v err=%v provider_calls=%d, want done, one scored round, one provider call",
			after, err, h.judge.callCount())
	}
	h.assertRoundReleased(t)
}

func r3PersistentMemberReadFault(t *testing.T) {
	h := newR3PlanHarness(t)
	dir := h.tasks.Dir()
	backup := dir + "-saved"
	var faultErr error
	var once sync.Once
	h.plans.OnChange = func(p *plan.Plan) {
		if p.ID == h.p.ID && p.PlanPhase == plan.PhaseJudging {
			once.Do(func() {
				faultErr = os.Rename(dir, backup)
				if faultErr == nil {
					faultErr = os.WriteFile(dir, []byte("not a directory"), 0o600)
				}
			})
		}
	}
	// First admission is through the normal per-plan pass. Subsequent rounds
	// use its already-admitted member snapshot at the production round entry;
	// the real directory fault remains across EVERY round and park write.
	h.pe.processPlan(context.Background(), h.p.ID)
	h.pe.judgeWG.Wait()
	if faultErr != nil {
		t.Fatalf("SETUP member directory fault: %v", faultErr)
	}
	for round := 1; round < plan.MaxConsecutiveJudgeUnavailable; round++ {
		p, err := h.plans.Get(h.p.ID)
		if err != nil || p.PlanPhase != plan.PhaseDispatching || p.JudgeRounds != 0 {
			t.Fatalf("G3 below-bound input failure should retry without a round debit: plan=%+v err=%v", p, err)
		}
		r3AssertUnreadableMemberConclusion(t, h, p)
		if _, err := h.tasks.List(task.Filter{PlanID: h.p.ID}); err == nil || !strings.Contains(err.Error(), "task: list dir") {
			t.Fatalf("SETUP member fault must persist: err=%v", err)
		}
		h.assertRoundReleased(t)
		h.pe.planDecisionMu.Lock()
		h.pe.beginPlanJudgeRound(p, []task.Task{*h.member})
		h.pe.planDecisionMu.Unlock()
		h.pe.judgeWG.Wait()
	}
	h.pe.wakeWG.Wait()
	parked, err := h.plans.Get(h.p.ID)
	if err != nil || parked.PlanPhase != plan.PhaseStalled || parked.JudgeRounds != 0 ||
		!strings.Contains(parked.HandoverText, "member tasks could not be listed") ||
		parked.Supervision == nil || parked.Supervision.WakeAt == "" {
		t.Errorf("G3 persistent member fault did not reach the visible bounded park: plan=%+v err=%v", parked, err)
	}
	if err == nil {
		r3AssertUnreadableMemberConclusion(t, h, parked)
	}
	if n := h.judge.callCount(); n != 0 {
		t.Errorf("G3 member-input fault called the provider %d times, want 0", n)
	}
	h.assertRoundReleased(t)
	h.pe.processPlan(context.Background(), h.p.ID)
	h.pe.judgeWG.Wait()
	if current, err := h.plans.Get(h.p.ID); err != nil || current.PlanPhase != plan.PhaseStalled {
		t.Errorf("G3 next pass re-judged a parked input fault: plan=%+v err=%v", current, err)
	}
	if err := os.Remove(dir); err != nil {
		t.Fatalf("restore member directory path: %v", err)
	}
	if err := os.Rename(backup, dir); err != nil {
		t.Fatalf("restore members: %v", err)
	}
}

func r3AssertUnreadableMemberConclusion(t *testing.T, h *r3PlanHarness, p *plan.Plan) {
	t.Helper()
	// Accepted Q3 A: missing member inputs cannot invent a result. The
	// conclusion labels it Not verified and explicitly names unreadable members.
	if !strings.Contains(p.HandoverText, "Not verified") ||
		!strings.Contains(p.HandoverText, "member tasks could not be listed") {
		t.Errorf("G3 unreadable-member conclusion must explicitly say Not verified and name the unreadable inputs: %q", p.HandoverText)
	}
}

func r3PlanOwnerWakeTexts(h *r3PlanHarness) []string {
	var texts []string
	for {
		select {
		case msg, ok := <-h.al.bus.InboundChan():
			if !ok {
				return texts
			}
			if msg.AsyncTranscriptSessionID == h.owner && msg.AsyncOriginAgentID == h.p.OwnerAgentID {
				texts = append(texts, msg.Content)
			}
		default:
			return texts
		}
	}
}

func TestR3PlanJudge_PersistentInputFaultIsReportedReleasedAndRecoverable(t *testing.T) {
	t.Run("plan_unreadable_through_outcome", r3PersistentPlanReadFault)
	t.Run("member_fault_reaches_bounded_park", r3PersistentMemberReadFault)
}

// Accepted Q3 A: even a below-bound unavailable round concludes, and the
// members' real work result is carried BOTH in handover and the owner wake.
func TestR3PlanJudge_UnavailableRoundWakesOwnerWithResultBearingConclusion(t *testing.T) {
	noJudgeWait(t)
	h := newR3PlanHarness(t)
	h.judge.chatFn = func(int) (*providers.LLMResponse, error) {
		return nil, errors.New("simulated judge outage")
	}
	h.pe.processPlan(context.Background(), h.p.ID)
	h.pe.judgeWG.Wait()
	h.pe.wakeWG.Wait()
	p, err := h.plans.Get(h.p.ID)
	if err != nil || p.JudgeRounds != 0 || p.State != plan.StateRunning {
		t.Fatalf("G3 unavailable plan status=%+v err=%v, want running/no round consumed", p, err)
	}
	r3AssertResultBearingConclusion(t, []string{p.HandoverText}, h.member.Result, "G3 Q3-A unavailable plan handover")
	r3AssertResultBearingConclusion(t, r3PlanOwnerWakeTexts(h), h.member.Result, "G3 Q3-A unavailable plan owner wake")
	h.assertRoundReleased(t)
}

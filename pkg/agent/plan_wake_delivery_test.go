// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

// plan_wake_delivery_test.go is the ADR-055 regression suite for the plan-wake
// delivery path (FR-012c/FR-012d/FR-016b/FR-016c/FR-021/FR-022/FR-029/FR-044).
//
// ⚠ EVERY test here asserts an OUTCOME, never a mechanism. That is the whole
// point of the file. The defect it exists to prevent survived four reviews
// because the in-package convention was a fake notifier that CAPTURES
// AsyncNotifyEvents — a recorder sitting three hops upstream of the guard that
// discarded every one of them. "Notify was called with AgentID=plansupervisor"
// was green for years while no agent turn had ever run for any plan wake.
//
// So, concretely, in this file:
//
//   - "the wake reached the supervisor" means AN LLM CALL WAS MADE ON THE
//     SUPERVISOR'S OWN PROVIDER and its transcript session has entries — not
//     that an event was recorded, not that an id was written to the plan.
//   - "stop halts the supervision turn" means THE TURN RETURNED, observed from
//     inside the provider call — not that a session id appeared in a cancel
//     fan-out set. The pre-existing fakeSessionCanceller records the string it
//     is handed and returns (true, nil) unconditionally, so a set-membership
//     assertion is green against a control that cancels nothing. It is banned
//     here.
//   - "an origin-less plan is healthy" means ITS ATTEMPT COUNTER DID NOT MOVE
//     and its failed_reason is not supervision_unavailable — the limb that
//     catches a naive fix which routes an empty origin through the notifier's
//     empty-destination rejection and escalates a perfectly healthy UI-created
//     plan to "the supervisor is unavailable".

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// --- A provider that proves a turn RAN -------------------------------------

// recordingTurnProvider is installed on ONE agent instance, so a call to it is
// positive evidence that a real turn ran for THAT agent — the property, not a
// recorded intention to deliver one.
//
// When gate is non-nil the call blocks until the test releases it OR the turn's
// context is cancelled, and records which of the two happened. That is how the
// kill-switch test observes cancellation from inside the turn.
type recordingTurnProvider struct {
	mu       sync.Mutex
	calls    int
	prompts  []string
	entered  chan struct{} // closed on the first call
	gate     chan struct{} // when non-nil, the call blocks on it
	ctxEnded bool          // the blocked call ended via ctx cancellation
	closed   bool
}

func newRecordingTurnProvider() *recordingTurnProvider {
	return &recordingTurnProvider{entered: make(chan struct{})}
}

func (p *recordingTurnProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	p.calls++
	var last string
	if len(messages) > 0 {
		last = messages[len(messages)-1].Content
	}
	p.prompts = append(p.prompts, last)
	gate := p.gate
	if !p.closed {
		p.closed = true
		close(p.entered)
	}
	p.mu.Unlock()

	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			p.mu.Lock()
			p.ctxEnded = true
			p.mu.Unlock()
			return nil, ctx.Err()
		}
	}
	return &providers.LLMResponse{Content: "acknowledged"}, nil
}

func (p *recordingTurnProvider) GetDefaultModel() string { return "test-model" }

func (p *recordingTurnProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func (p *recordingTurnProvider) promptList() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.prompts...)
}

func (p *recordingTurnProvider) cancelledFromInsideTheTurn() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.ctxEnded
}

// --- Harness ----------------------------------------------------------------

const testPlanOwnerAgentID = "plan-owner"

type planWakeHarness struct {
	al         *AgentLoop
	msgBus     *bus.MessageBus
	pe         *PlanEngine
	plans      *plan.Store
	tasks      *task.Store
	clock      *fakePlanClock
	supervisor *recordingTurnProvider
	owner      *recordingTurnProvider
}

// newPlanWakeHarness builds a REAL AgentLoop (real registry, real session
// stores, real async notifier, real message bus) plus a REAL PlanEngine wired
// to it, with a distinct recording provider on the owner agent and on the
// PlanSupervisor agent.
//
// The loop's Run pump is started, because the OWNER wake's delivery genuinely
// depends on it: the notifier publishes onto the bus and Run is what routes a
// system message into processSystemMessage. A test that stubbed that out would
// be asserting the mechanism again.
func newPlanWakeHarness(t *testing.T) *planWakeHarness {
	t.Helper()
	// os.MkdirTemp + a best-effort RemoveAll, NOT t.TempDir: the loop's own
	// background writers (recap, cost, session bookkeeping) can still be
	// flushing when the test ends, and t.TempDir's cleanup FAILS THE TEST on a
	// non-empty directory. That is the same reason newTestAgentLoop in this
	// package uses MkdirTemp. Nothing here asserts on the directories.
	//
	// outerDir is this harness's OWN private container (one per call to
	// newPlanWakeHarness), created exactly once. Every tmp() call below nests
	// INSIDE it (os.MkdirTemp(outerDir, ...), not os.MkdirTemp("", ...)) so
	// filepath.Dir(cfg.Agents.Defaults.Home) — what NewAgentLoop roots the
	// shared session/task store at — is THIS harness's own outerDir, never
	// the shared OS temp root every other test's flat MkdirTemp call used to
	// share. See loop_test.go's newTestAgentLoop doc comment for the leak
	// this closes across the package.
	outerDir, outerErr := os.MkdirTemp("", "plan-wake-outer-*")
	if outerErr != nil {
		t.Fatalf("mkdtemp (outer): %v", outerErr)
	}
	t.Cleanup(func() { _ = os.RemoveAll(outerDir) })
	tmp := func() string {
		t.Helper()
		dir, err := os.MkdirTemp(outerDir, "plan-wake-*")
		if err != nil {
			t.Fatalf("mkdtemp: %v", err)
		}
		return dir
	}
	t.Setenv(config.EnvHome, tmp())

	ownerProvider := newRecordingTurnProvider()
	supervisorProvider := newRecordingTurnProvider()

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: tmp(), DefaultModel: config.DefaultModel{Model: "test-model"}},
			List: []config.AgentConfig{
				{ID: testPlanOwnerAgentID, Name: "Plan Owner", Type: config.AgentTypeCustom, Home: tmp()},
				{ID: planSupervisorAgentID, Name: "PlanSupervisor", Type: config.AgentTypeSystem, Home: tmp()},
			},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	t.Cleanup(func() { al.Close() })

	ownerInst, ok := al.GetRegistry().GetAgent(testPlanOwnerAgentID)
	if !ok {
		t.Fatalf("owner agent %q not registered", testPlanOwnerAgentID)
	}
	ownerInst.Provider = ownerProvider
	supervisorInst, ok := al.GetRegistry().GetAgent(planSupervisorAgentID)
	if !ok {
		t.Fatalf("supervisor agent %q not registered", planSupervisorAgentID)
	}
	supervisorInst.Provider = supervisorProvider

	runCtx, runCancel := context.WithCancel(context.Background())
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		_ = al.Run(runCtx)
	}()

	dir := tmp()
	planStore := plan.New(filepath.Join(dir, "plans"))
	taskStore := task.New(filepath.Join(dir, "tasks"))
	pe := NewPlanEngine(al, planStore, taskStore, nil)
	clock := &fakePlanClock{now: time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)}
	pe.clock = clock
	pe.judge = &fakePlanJudge{}

	t.Cleanup(func() {
		// Drain in-flight wake turns BEFORE teardown: a wake turn is a real
		// turn writing real session files.
		pe.wakeWG.Wait()
		runCancel()
		<-runDone
		al.WaitForActiveRequests()
	})

	return &planWakeHarness{
		al: al, msgBus: msgBus, pe: pe, plans: planStore, tasks: taskStore,
		clock: clock, supervisor: supervisorProvider, owner: ownerProvider,
	}
}

// waitForTurn blocks until p has been called at least want times, or fails.
// Wake turns are dispatched asynchronously by design (an LLM turn must never
// run under the process-wide plan decision lock), so "did a turn run" is
// necessarily an await, not an immediate read.
func waitForTurn(t *testing.T, p *recordingTurnProvider, want int, what string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if p.callCount() >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("%s: expected at least %d agent turn(s) to have RUN, got %d — "+
		"a wake that publishes an event nobody turns into a turn is not a wake", what, want, p.callCount())
}

// waitForPersistedTranscript blocks until sessionID's transcript has at least
// one entry, or fails. A turn persists its assistant entry AFTER the provider
// call returns, and the whole wake turn runs asynchronously, so this is the
// bounded-await form of "the turn's output landed in the right session".
func waitForPersistedTranscript(t *testing.T, al *AgentLoop, sessionID, what string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if store := al.ResolveSessionStore(sessionID); store != nil {
			if entries, err := store.ReadTranscript(sessionID); err == nil && len(entries) > 0 {
				return
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("%s: session %q has an EMPTY transcript — the turn ran but persisted nowhere", what, sessionID)
}

// drainOutbound returns every outbound (user-facing) message published so far.
func drainOutbound(msgBus *bus.MessageBus) []bus.OutboundMessage {
	var out []bus.OutboundMessage
	for {
		select {
		case m := <-msgBus.OutboundChan():
			out = append(out, m)
		default:
			return out
		}
	}
}

// waitForOutbound blocks until an outbound message addressed to
// (channel, chatID) is published, or fails. This is the delivery half of an
// owner wake: the turn's answer actually reaching the conversation the plan
// came from.
func waitForOutbound(t *testing.T, msgBus *bus.MessageBus, channel, chatID string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var seen []bus.OutboundMessage
	for time.Now().Before(deadline) {
		seen = append(seen, drainOutbound(msgBus)...)
		for _, m := range seen {
			if m.Channel == channel && m.ChatID == chatID {
				return
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("the plan outcome never reached its origin chat (%s/%s), outbound = %+v", channel, chatID, seen)
}

// parkPlanAtSupervision drives a plan to plan_phase=awaiting_supervision the
// way production does — one all-terminal member and an UNMET judge verdict —
// so the supervision wake under test is the real one, not a hand-written phase.
func (h *planWakeHarness) parkPlanAtSupervision(t *testing.T, planID string, p *plan.Plan) {
	t.Helper()
	if err := h.plans.Create(p); err != nil {
		t.Fatalf("create plan: %v", err)
	}
	mustCreateTask(t, h.tasks, &task.Task{
		Title: "member", WorkspaceID: "ws", PlanID: planID, Status: task.StatusDone,
	})
	fakeJudge, ok := h.pe.judge.(*fakePlanJudge)
	if !ok {
		t.Fatalf("unexpected type %T for h.pe.judge, want *fakePlanJudge", h.pe.judge)
	}
	fakeJudge.resultFn = func(in JudgeCriteriaInput) JudgeCriteriaResult {
		return JudgeCriteriaResult{Verdict: &task.JudgeVerdict{
			Met:          false,
			PerCriterion: []task.CriterionVerdict{{CriterionID: in.Criteria[0].ID, Met: false, Reason: "not yet"}},
		}}
	}
	h.pe.processPlan(context.Background(), planID)
	h.pe.judgeWG.Wait()
}

func runningPlanWithDoD(id string, sourceChannel, sourceChatID string) *plan.Plan {
	return &plan.Plan{
		ID: id, Title: id, WorkspaceID: "ws",
		OwnerAgentID:  testPlanOwnerAgentID,
		State:         plan.StateRunning,
		SourceChannel: sourceChannel,
		SourceChatID:  sourceChatID,
		DoD:           []task.AcceptanceCriterion{planProseCriterion("the thing is done")},
	}
}

// --- #31b: the supervision wake actually starts a turn ----------------------

// TestSupervisionWake_ActuallyDispatchesATurn is the N15 regression, and the
// deepest instance of the defect this whole change exists to remove.
//
// Before ADR-055 this failed at the very first assertion: wakeOwner hardcoded
// an internal origin channel, the bus ChatID became "system:plan:<id>", and
// processSystemMessage returned at its internal-channel guard before
// dispatching anything. Every existing wake test passed anyway, because the
// fake notifier records the call three hops upstream of that drop.
func TestSupervisionWake_ActuallyDispatchesATurn(t *testing.T) {
	h := newPlanWakeHarness(t)
	h.parkPlanAtSupervision(t, "p1", runningPlanWithDoD("p1", "telegram", "chat-1"))

	// (1) A turn RAN for the supervisor. This is the assertion; "Notify was
	//     called" is explicitly NOT accepted as evidence of it — the
	//     supervision path does not use the notifier at all.
	waitForTurn(t, h.supervisor, 1, "supervision wake")

	got, err := h.plans.Get("p1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Supervision == nil || got.Supervision.SessionID == "" {
		t.Fatalf("supervision.session_id must name the adjudication session, got %+v", got.Supervision)
	}

	// (2) It ran in the plan's OWN supervision session, and that session is a
	//     real store-backed one. A derived id ("plansupervisor:plan:p1")
	//     resolves to no store: the turn would run with an empty transcript
	//     binding and be uncancellable, while every id-shaped assertion stayed
	//     green.
	sessionID := got.Supervision.SessionID
	if strings.Contains(sessionID, "plan:") {
		t.Fatalf("supervision.session_id = %q — a DERIVED id is forbidden; it must be store-minted", sessionID)
	}
	if h.al.ResolveSessionStore(sessionID) == nil {
		t.Fatalf("supervision.session_id %q does not resolve to any session store — "+
			"processSystemMessage would drop it and RequestCancelForSession could never match it", sessionID)
	}
	waitForPersistedTranscript(t, h.al, sessionID, "supervision wake")
	h.pe.wakeWG.Wait() // the turn is fully finished before the leak check below

	// (3) ZERO outbound messages were published to ANY channel. This is the
	//     non-leak property (the adjudicator's deliberation must not reach the
	//     owner's conversation) asserted as a property — not as "a send
	//     failed", which starts leaking the moment anything binds to that id.
	if out := drainOutbound(h.msgBus); len(out) != 0 {
		t.Fatalf("a supervision wake must publish NOTHING outbound, got %d message(s): %+v", len(out), out)
	}

	// (4) The prompt actually asked for adjudication, and did not tell the
	//     recipient the plan awaits "your" correction (it is not the owner).
	prompts := h.supervisor.promptList()
	if len(prompts) == 0 || !strings.Contains(prompts[0], "UNMET") {
		t.Fatalf("supervision prompt should carry the UNMET diagnosis, got %q", prompts)
	}
	if strings.Contains(prompts[0], "awaiting your correction") {
		t.Fatalf("the UNMET wake still addresses the owner: %q", prompts[0])
	}
}

// --- #31c: an owner wake reaches a turn AND the origin chat -----------------

// TestOwnerWake_ReachesATurnAndTheOriginChat covers the other family: an
// OUTCOME wake on a plan that has a real chat origin must both run a turn for
// the owner agent and be delivered into the conversation the plan came from.
//
// It also carries the sibling assertion that the fix did NOT widen the
// internal-channel guard: genuinely internal cli/subagent system traffic must
// still start no turn at all.
func TestOwnerWake_ReachesATurnAndTheOriginChat(t *testing.T) {
	h := newPlanWakeHarness(t)
	p := runningPlanWithDoD("p1", "telegram", "chat-1")
	if err := h.plans.Create(p); err != nil {
		t.Fatalf("create plan: %v", err)
	}

	if _, err := h.pe.StopPlan(context.Background(), "p1", "tester", "web"); err != nil {
		t.Fatalf("StopPlan: %v", err)
	}

	// A turn RAN for the owner agent — the outcome, not a published event.
	waitForTurn(t, h.owner, 1, "owner wake (stop)")

	got, err := h.plans.Get("p1")
	if err != nil {
		t.Fatal(err)
	}
	// The owner session is real and minted, not the composed "plan:<id>" that
	// named nothing.
	if got.OwnerSessionID == "" {
		t.Fatal("the owner wake must mint and record a real owner session")
	}
	if got.OwnerSessionID == "plan:p1" {
		t.Fatal("owner_session_id is still the composed id that names no session")
	}
	if h.al.ResolveSessionStore(got.OwnerSessionID) == nil {
		t.Fatalf("owner_session_id %q resolves to no session store", got.OwnerSessionID)
	}

	// The outcome reached the plan's origin conversation.
	waitForOutbound(t, h.msgBus, "telegram", "chat-1")

	// Sibling assertion (regression): genuinely internal traffic is still
	// dropped. Widening the internal-channel guard would make every cli /
	// subagent completion start a spurious turn.
	before := h.owner.callCount()
	for _, internal := range []string{"cli", "subagent"} {
		if err := h.msgBus.PublishInbound(context.Background(), bus.InboundMessage{
			Channel:            "system",
			Sender:             bus.SenderInfo{CanonicalID: "async:test"},
			ChatID:             internal + ":whatever",
			Content:            "internal chatter",
			AsyncOriginAgentID: testPlanOwnerAgentID,
		}); err != nil {
			t.Fatalf("publish %s system message: %v", internal, err)
		}
	}
	time.Sleep(250 * time.Millisecond)
	if after := h.owner.callCount(); after != before {
		t.Fatalf("internal cli/subagent system messages must start NO turn; owner turns went %d -> %d", before, after)
	}
}

// --- FR-012c: an INTERNAL origin is not a deliverable origin ---------------

// TestInternalOriginPlan_OwnerWakeStillRunsATurn is the case FR-012c was
// supposed to close and did not.
//
// A plan created inside a CLI turn records SourceChannel="cli"
// (pkg/tools/plan.go). That is NON-EMPTY, so a predicate that asks "is the
// origin populated?" sends the owner wake down the notifier leg — where
// processSystemMessage drops it, because `cli` is an internal channel
// (pkg/constants). The wake published an event, the event became no turn, and
// every mechanism-level assertion stayed green while the plan's outcome went
// nowhere.
//
// The property under test is the one FR-012c actually states: a wake DISPATCHES
// AN AGENT TURN. Asserting that directly is what makes this test catch the
// defect; asserting "the notifier was called" is what let it survive.
func TestInternalOriginPlan_OwnerWakeStillRunsATurn(t *testing.T) {
	for _, internal := range []string{"cli", "system", "subagent"} {
		t.Run(internal, func(t *testing.T) {
			h := newPlanWakeHarness(t)
			h.parkPlanAtSupervision(t, "p1", runningPlanWithDoD("p1", internal, internal+"-chat-1"))

			// Family A (supervision) never reads the origin, so it was never
			// affected — assert it to keep the two families distinguishable.
			waitForTurn(t, h.supervisor, 1, "supervision wake on a "+internal+"-origin plan")

			h.pe.planDecisionMu.Lock()
			h.pe.failPlanLocked("p1", plan.FailedReasonJudgeRoundsExhausted, "the plan ran out of rounds")
			h.pe.planDecisionMu.Unlock()

			// THE assertion: the owner wake ran a real turn. Before the
			// originCanDeliver fix this timed out at zero turns.
			waitForTurn(t, h.owner, 1, "owner wake on a "+internal+"-origin plan")

			got, err := h.plans.Get("p1")
			if err != nil {
				t.Fatal(err)
			}
			// An internal origin is a HEALTHY state, exactly like no origin at
			// all: it must not be charged to the supervision escalation ladder.
			if got.Supervision.WakeError != "" {
				t.Errorf("an internal-origin plan is healthy: wake_error must stay unset, got %q",
					got.Supervision.WakeError)
			}
			if got.Supervision.Attempts != 1 {
				t.Errorf("supervision.attempts = %d, want 1 — an internal origin must not inflate the count",
					got.Supervision.Attempts)
			}
			// The synthesis is still persisted somewhere durable.
			if got.OwnerSessionID == "" {
				t.Error("an internal-origin plan still needs a real owner session to persist into")
			}
			h.pe.wakeWG.Wait()
			// And nothing was published to a channel that cannot deliver it.
			if out := drainOutbound(h.msgBus); len(out) != 0 {
				t.Errorf("an internal-origin plan must publish NOTHING outbound, got %d: %+v", len(out), out)
			}
		})
	}
}

// --- #31d: the origin-less plan ---------------------------------------------

// TestOriginLessPlan_SupervisedAndDeliveredWithoutAChatLeg is the case a naive
// fix misses, because the real-channel happy path above passes without it.
//
// A plan created through the Plans UI has NO chat origin, and that is a
// legitimate, expected state. The trap: passing its empty origin to the
// notifier trips the empty-destination rejection, which the escalation ladder
// then reads as a wake failure — terminating a perfectly HEALTHY plan as
// failed(supervision_unavailable). Every mechanism-level assertion stays green
// while that happens, which is why limb (5) below is the point of this test.
func TestOriginLessPlan_SupervisedAndDeliveredWithoutAChatLeg(t *testing.T) {
	h := newPlanWakeHarness(t)
	h.parkPlanAtSupervision(t, "p1", runningPlanWithDoD("p1", "", ""))

	// (1) The supervision wake still runs a turn. An origin-less plan must not
	//     become a second silent-drop case — family A never reads the origin.
	waitForTurn(t, h.supervisor, 1, "supervision wake on an origin-less plan")

	parked, err := h.plans.Get("p1")
	if err != nil {
		t.Fatal(err)
	}
	// (2) …and it did NOT walk the escalation ladder. THIS is the limb that
	//     catches the naive fix.
	if parked.Supervision.WakeError != "" {
		t.Fatalf("a plan with no chat origin is HEALTHY: wake_error must stay unset, got %q", parked.Supervision.WakeError)
	}
	if parked.Supervision.Attempts != 1 {
		t.Fatalf("supervision.attempts = %d, want exactly 1 (the wake itself) — "+
			"a missing origin must not inflate the attempt count", parked.Supervision.Attempts)
	}

	// (3) The OWNER wake still runs a turn, dispatched directly. "No chat to
	//     deliver to" must never mean "no turn ran".
	h.pe.planDecisionMu.Lock()
	h.pe.failPlanLocked("p1", plan.FailedReasonJudgeRoundsExhausted, "the plan ran out of rounds")
	h.pe.planDecisionMu.Unlock()
	waitForTurn(t, h.owner, 1, "owner wake on an origin-less plan")

	got, err := h.plans.Get("p1")
	if err != nil {
		t.Fatal(err)
	}
	// (4) The closing synthesis is PERSISTED, even with no chat to send it to.
	if got.OwnerSessionID == "" {
		t.Fatal("an origin-less plan still needs a real owner session to persist its synthesis into")
	}
	if h.al.ResolveSessionStore(got.OwnerSessionID) == nil {
		t.Fatalf("owner_session_id %q resolves to no store; the synthesis went nowhere", got.OwnerSessionID)
	}
	waitForPersistedTranscript(t, h.al, got.OwnerSessionID, "origin-less owner wake")
	h.pe.wakeWG.Wait() // both wake turns are fully finished before the leak check

	// (5) ZERO outbound anywhere. No fallback to the owner's last-active
	//     channel, no fallback to the creator's most recent session — those
	//     drop a plan's outcome into an unrelated conversation.
	if out := drainOutbound(h.msgBus); len(out) != 0 {
		t.Fatalf("an origin-less plan must publish NOTHING outbound, got %d message(s): %+v", len(out), out)
	}

	// (6) The OWNER wake did not feed the escalation ladder either. This is
	//     the second half of the N16 assertion and the one that catches the
	//     rejected "attempt the chat leg and treat the rejection as benign"
	//     option: that variant passes the empty origin to the notifier, whose
	//     empty-destination rejection is then recorded as a wake error and
	//     charged to the attempt budget — on a plan that is behaving exactly
	//     as designed.
	if got.Supervision.WakeError != "" {
		t.Fatalf("the origin-less OWNER wake recorded a wake error (%q) — a missing chat origin is "+
			"not a failure and must never reach the escalation ladder", got.Supervision.WakeError)
	}
	if got.Supervision.Attempts != 1 {
		t.Fatalf("supervision.attempts = %d after the owner wake, want it unchanged at 1 — "+
			"an origin-less owner wake must not consume the supervision attempt budget", got.Supervision.Attempts)
	}

	// (7) The terminal record tells the truth about why the plan ended.
	if got.FailedReason == plan.FailedReasonSupervisionUnavailable {
		t.Fatal("a healthy plan with no chat origin was falsely diagnosed as failed(supervision_unavailable)")
	}
	if got.FailedReason != plan.FailedReasonJudgeRoundsExhausted {
		t.Fatalf("failed_reason = %q, want judge_rounds_exhausted", got.FailedReason)
	}
}

// --- FR-012b: the MET synthesis stays on the owner --------------------------

// TestMetSynthesisWake_StaysOnTheOwner is a REGRESSION ANCHOR, not a feature
// test. Re-targeting the MET wake to the adjudicator looks like tidy symmetry
// with the other two decision wakes, and it silently retires success
// notification: this is the ONLY wake on the plan's success path (the failure
// wake fires only on failure, the stop wake only on user stop), so a plan that
// SUCCEEDS would notify nobody — neither the owner agent nor the human who
// authored it. Nothing wires a supervisor synthesis back.
func TestMetSynthesisWake_StaysOnTheOwner(t *testing.T) {
	h := newPlanWakeHarness(t)
	if err := h.plans.Create(runningPlanWithDoD("p1", "telegram", "chat-1")); err != nil {
		t.Fatalf("create plan: %v", err)
	}
	mustCreateTask(t, h.tasks, &task.Task{
		Title: "member", WorkspaceID: "ws", PlanID: "p1", Status: task.StatusDone, Result: "all good",
	})
	fakeJudge, ok := h.pe.judge.(*fakePlanJudge)
	if !ok {
		t.Fatalf("unexpected type %T for h.pe.judge, want *fakePlanJudge", h.pe.judge)
	}
	fakeJudge.resultFn = func(in JudgeCriteriaInput) JudgeCriteriaResult {
		return JudgeCriteriaResult{Verdict: &task.JudgeVerdict{
			Met:          true,
			PerCriterion: []task.CriterionVerdict{{CriterionID: in.Criteria[0].ID, Met: true, Reason: "confirmed"}},
		}}
	}

	h.pe.processPlan(context.Background(), "p1")
	h.pe.judgeWG.Wait()

	// The OWNER writes the closing synthesis, in the requester's own chat.
	waitForTurn(t, h.owner, 1, "MET synthesis wake")
	prompts := h.owner.promptList()
	if len(prompts) == 0 || !strings.Contains(prompts[0], "closing synthesis") {
		t.Fatalf("the owner must be commissioned to write the closing synthesis, got %q", prompts)
	}
	if h.supervisor.callCount() != 0 {
		t.Fatal("the MET synthesis was re-targeted to the adjudicator — a plan that SUCCEEDS would then notify nobody")
	}

	got, err := h.plans.Get("p1")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != plan.StateDone {
		t.Fatalf("state = %q, want done", got.State)
	}
}

// --- #63: the kill switch ---------------------------------------------------

// TestStopPlan_HaltsTheInFlightSupervisionTurn is the containment control,
// asserted at the OUTCOME.
//
// ⚠ It deliberately does NOT use fakeSessionCanceller. That fake records the
// session id it is handed and returns (true, nil) regardless, and the fan-out
// discards the "did it fire" result — so "the supervision session appears in
// the cancel set" is green against a control that cancels nothing. That was
// exactly the shipped defect: the owner-session leg of this cascade had ALWAYS
// been a no-op because the id it cancelled named a session nothing ever
// created.
//
// What is asserted instead: the blocked adjudication turn observes its own
// cancellation and RETURNS within a bounded time.
func TestStopPlan_HaltsTheInFlightSupervisionTurn(t *testing.T) {
	h := newPlanWakeHarness(t)
	// Block the supervision turn inside the provider call so it is genuinely
	// in flight when Stop lands.
	h.supervisor.gate = make(chan struct{})

	h.parkPlanAtSupervision(t, "p1", runningPlanWithDoD("p1", "telegram", "chat-1"))

	select {
	case <-h.supervisor.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the supervision turn never started; there is nothing for Stop to halt")
	}

	got, err := h.plans.Get("p1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Supervision == nil || got.Supervision.SessionID == "" {
		t.Fatal("no supervision session recorded; Stop cannot name the turn it must cancel")
	}

	turnsDrained := make(chan struct{})
	go func() {
		h.pe.wakeWG.Wait()
		close(turnsDrained)
	}()

	if _, err := h.pe.StopPlan(context.Background(), "p1", "tester", "web"); err != nil {
		t.Fatalf("StopPlan: %v", err)
	}

	select {
	case <-turnsDrained:
	case <-time.After(15 * time.Second):
		close(h.supervisor.gate) // unblock so the test can exit cleanly
		t.Fatal("the in-flight supervision turn did NOT terminate after StopPlan — " +
			"the kill switch cancelled an id that named nothing")
	}
	if !h.supervisor.cancelledFromInsideTheTurn() {
		close(h.supervisor.gate)
		t.Fatal("the supervision turn ended without its context being cancelled — " +
			"it finished on its own rather than being halted by Stop")
	}
	close(h.supervisor.gate)
}

// --- FR-029: the phase gate --------------------------------------------------

// TestAppendCorrection_AcceptsStalledPhase pins the widened gate. Under the
// pre-ADR-055 gate (equality with the parked phase) this fails 100% of the
// time: a stall wake asked the adjudicator for a diagnosis and then rejected
// every correction that diagnosis produced, leaving a stalled plan with no
// corrector at all and no exit but Stop or idle expiry.
func TestAppendCorrection_AcceptsStalledPhase(t *testing.T) {
	h := newTestPlanEngine(t)
	stalled := plan.PhaseStalled
	mustCreatePlan(t, h.plans, &plan.Plan{
		ID: "p1", Title: "p1", WorkspaceID: "ws", OwnerAgentID: "owner", State: plan.StateRunning,
		PlanPhase:    stalled,
		HandoverText: stallHandoverNotePrefix + "nothing is dispatchable",
	})
	mustCreateTask(t, h.tasks, &task.Task{
		Title: "member", WorkspaceID: "ws", PlanID: "p1", Status: task.StatusDone,
	})

	res, err := h.pe.AppendCorrection(context.Background(), "p1",
		supervisorCaller(),
		CorrectionRequest{
			Verb:                CorrectionAppend,
			FalsifiedAssumption: "the blocker would resolve itself",
			TailMembers: []task.Task{{
				ID: "tail-1", Title: "unblock it", WorkspaceID: "ws", PlanID: "p1", Status: task.StatusNext,
				Criteria: []task.AcceptanceCriterion{planProseCriterion("the blocker is resolved")},
			}},
		})
	if err != nil {
		t.Fatalf("a correction on a STALLED plan must be applied, got error: %v", err)
	}
	if res == nil || res.RevisionID == "" {
		t.Fatal("expected a recorded revision for the applied correction")
	}

	got, err := h.plans.Get("p1")
	if err != nil {
		t.Fatal(err)
	}
	if got.PlanPhase != plan.PhaseDispatching {
		t.Fatalf("plan_phase = %q, want dispatching after an applied correction", got.PlanPhase)
	}
	if strings.HasPrefix(got.HandoverText, stallHandoverNotePrefix) {
		t.Fatalf("the stall note must be cleared by the correction, got %q", got.HandoverText)
	}
	if got.Supervision == nil || got.Supervision.CorrectionRounds != 1 {
		t.Fatalf("supervision.correction_rounds must be 1 after one applied correction, got %+v", got.Supervision)
	}

	// A plan OUTSIDE the supervision-eligible set is still rejected — the gate
	// widened to a set, it did not become "any phase".
	dispatching := plan.PhaseDispatching
	mustCreatePlan(t, h.plans, &plan.Plan{
		ID: "p2", Title: "p2", WorkspaceID: "ws", OwnerAgentID: "owner", State: plan.StateRunning,
		PlanPhase: dispatching,
	})
	if _, err := h.pe.AppendCorrection(context.Background(), "p2",
		supervisorCaller(),
		CorrectionRequest{
			Verb:                CorrectionAppend,
			FalsifiedAssumption: "x",
			TailMembers:         []task.Task{{ID: "t", Title: "t", WorkspaceID: "ws", PlanID: "p2", Status: task.StatusNext}},
		}); err == nil {
		t.Fatal("a correction on a DISPATCHING plan must still be rejected")
	}
}

// --- FR-021/FR-022: the deadline and the ceiling -----------------------------

// TestSupervisionDeadline_ArmsAndBoundsAStalledPlan proves the stall limb is
// bounded like the parked limb: its wake arms a deadline, its silence
// increments the attempt count, and exhausting the ceiling terminates the plan
// with a reason of its own.
//
// Without this, a stall armed no deadline, incremented no attempts, hit no
// ceiling, and was bounded only by 7-day idle expiry.
func TestSupervisionDeadline_ArmsAndBoundsAStalledPlan(t *testing.T) {
	h := newTestPlanEngine(t)
	mustCreateRunningPlan(t, h.plans, "p1", "owner")
	outside := mustCreateTask(t, h.tasks, &task.Task{
		Title: "outside blocker", WorkspaceID: "ws", Status: task.StatusInbox,
	})
	mustCreateTask(t, h.tasks, &task.Task{
		Title: "stuck member", WorkspaceID: "ws", PlanID: "p1",
		Status: task.StatusBlocked, BlockedBy: []string{outside.ID},
	})

	// Tick 1: the stall is surfaced and the first supervision wake is issued.
	h.pe.processPlan(context.Background(), "p1")
	got, err := h.plans.Get("p1")
	if err != nil {
		t.Fatal(err)
	}
	if got.PlanPhase != plan.PhaseStalled {
		t.Fatalf("plan_phase = %q, want stalled", got.PlanPhase)
	}
	if got.Supervision == nil || got.Supervision.Attempts != 1 || got.Supervision.WakeAt == "" {
		t.Fatalf("the stall wake must arm the deadline and count as attempt 1, got %+v", got.Supervision)
	}

	// Inside the deadline: nothing changes. Strictly greater, so exactly at
	// the boundary it does NOT fire.
	h.clock.Set(h.clock.Now().Add(defaultSupervisionTurnTimeout))
	h.pe.processPlan(context.Background(), "p1")
	got, _ = h.plans.Get("p1")
	if got.Supervision.Attempts != 1 {
		t.Fatalf("at exactly wake_at+timeout the deadline must NOT fire, attempts = %d", got.Supervision.Attempts)
	}

	// Past the deadline, twice more: attempts climb to the ceiling.
	for want := 2; want <= defaultSupervisionMaxAttempts; want++ {
		h.clock.Set(h.clock.Now().Add(defaultSupervisionTurnTimeout + time.Second))
		h.pe.processPlan(context.Background(), "p1")
		got, _ = h.plans.Get("p1")
		if got.State != plan.StateRunning {
			t.Fatalf("the plan must stay running while attempts remain, state = %q at attempt %d", got.State, want)
		}
		if got.Supervision.Attempts != want {
			t.Fatalf("supervision.attempts = %d, want %d", got.Supervision.Attempts, want)
		}
	}

	// One more elapsed deadline with the ceiling spent terminates the plan,
	// with a reason distinct from every other terminal cause.
	h.clock.Set(h.clock.Now().Add(defaultSupervisionTurnTimeout + time.Second))
	h.pe.processPlan(context.Background(), "p1")
	got, err = h.plans.Get("p1")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != plan.StateFailed {
		t.Fatalf("state = %q, want failed once the supervision ceiling is spent", got.State)
	}
	if got.FailedReason != plan.FailedReasonSupervisionUnavailable {
		t.Fatalf("failed_reason = %q, want supervision_unavailable", got.FailedReason)
	}
	if !strings.Contains(got.HandoverText, "ADJUDICATION failure") {
		t.Fatalf("the handover must say adjudication failed, not that the plan's work did: %q", got.HandoverText)
	}
	// The owner IS told, and only now — never while attempts remained.
	events := h.notif.eventList()
	told := false
	for _, e := range events {
		if e.SourceKind == "plan_supervision_unavailable" {
			told = true
		}
	}
	if !told {
		t.Fatalf("the owner must be woken with the adjudication-unavailable handover, events = %+v", events)
	}
}

// --- FR-050: the per-field lifecycle ----------------------------------------

// TestSupervisionState_CorrectionRoundsSurviveAParkCycle drives a real
// park -> correct -> dispatch -> re-park cycle and proves the counters follow
// their OWN rules rather than one blanket rule.
//
// This shape is the regression: a plan leaves the supervision-eligible phase
// set on every applied correction, so a blanket "reset everything on leaving"
// zeroed correction_rounds immediately after each increment and every terminal
// record read 0 — inverting the only thing that counter is read for.
func TestSupervisionState_CorrectionRoundsSurviveAParkCycle(t *testing.T) {
	h := newTestPlanEngine(t)
	awaiting := plan.PhaseAwaitingSupervision
	mustCreatePlan(t, h.plans, &plan.Plan{
		ID: "p1", Title: "p1", WorkspaceID: "ws", OwnerAgentID: "owner", State: plan.StateRunning,
		PlanPhase:                  awaiting,
		HandoverText:               "the judge found the DoD unmet",
		LastUnmetTerminalSignature: "sig-1",
		Supervision: &plan.Supervision{
			WakeAt:    h.clock.Now().UTC().Format(time.RFC3339),
			Attempts:  2,
			SessionID: "session_supervision_1",
		},
	})
	mustCreateTask(t, h.tasks, &task.Task{
		Title: "member", WorkspaceID: "ws", PlanID: "p1", Status: task.StatusDone,
	})

	for round := 1; round <= 2; round++ {
		if _, err := h.pe.AppendCorrection(context.Background(), "p1",
			supervisorCaller(),
			CorrectionRequest{
				Verb:                CorrectionAppend,
				FalsifiedAssumption: fmt.Sprintf("assumption %d was wrong", round),
				TailMembers: []task.Task{{
					ID:    fmt.Sprintf("tail-%d", round),
					Title: fmt.Sprintf("tail %d", round),
					// `blocked` keeps the plan non-terminal so the correction
					// does not immediately re-judge.
					WorkspaceID: "ws", PlanID: "p1", Status: task.StatusNext,
					Criteria: []task.AcceptanceCriterion{
						planProseCriterion(fmt.Sprintf("tail %d is done", round)),
					},
				}},
			}); err != nil {
			t.Fatalf("correction %d: %v", round, err)
		}

		got, err := h.plans.Get("p1")
		if err != nil {
			t.Fatal(err)
		}
		if got.Supervision.CorrectionRounds != round {
			t.Fatalf("after correction %d: supervision.correction_rounds = %d, want %d — "+
				"it is cumulative for the life of the plan and MUST NOT reset when the plan leaves the phase",
				round, got.Supervision.CorrectionRounds, round)
		}
		if got.Supervision.Attempts != 0 {
			t.Fatalf("after correction %d: supervision.attempts = %d, want 0 (reset by the correction)",
				round, got.Supervision.Attempts)
		}
		if got.Supervision.WakeAt != "" {
			t.Fatalf("after correction %d: wake_at must be cleared, got %q", round, got.Supervision.WakeAt)
		}
		if got.Supervision.SessionID != "session_supervision_1" {
			t.Fatalf("after correction %d: session_id must be RETAINED (never blanked), got %q — "+
				"blanking it in the window where the adjudication turn is still running leaves Stop "+
				"unable to name the turn it must cancel", round, got.Supervision.SessionID)
		}

		// Re-park for the next round.
		if _, err := h.plans.Update("p1", plan.Patch{PlanPhase: &awaiting}); err != nil {
			t.Fatal(err)
		}
	}
}

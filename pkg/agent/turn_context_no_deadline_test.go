package agent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// deadlineRecordingProvider records, for every provider call, whether the
// call's context carried a deadline. When gate is non-nil the call blocks on
// it (or its own context) so a test can observe a turn that is in flight.
type deadlineRecordingProvider struct {
	mu        sync.Mutex
	deadlines []bool
	entered   chan struct{}
	enterOnce sync.Once
	gate      chan struct{}
}

func newDeadlineRecordingProvider(gated bool) *deadlineRecordingProvider {
	p := &deadlineRecordingProvider{entered: make(chan struct{})}
	if gated {
		p.gate = make(chan struct{})
	}
	return p
}

func (p *deadlineRecordingProvider) Chat(ctx context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	_, has := ctx.Deadline()
	p.mu.Lock()
	p.deadlines = append(p.deadlines, has)
	p.mu.Unlock()
	p.enterOnce.Do(func() { close(p.entered) })
	if p.gate != nil {
		select {
		case <-p.gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return &providers.LLMResponse{Content: "done"}, nil
}

func (p *deadlineRecordingProvider) GetDefaultModel() string { return "deadline-recorder" }

func (p *deadlineRecordingProvider) sawDeadline() (calls int, sawAny bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, d := range p.deadlines {
		sawAny = sawAny || d
	}
	return len(p.deadlines), sawAny
}

// TestTurnContext_NoDeadlineOnNativeRootTaskOrScheduledTurns pins issue #1306
// at the turn boundary itself (T2): an ordinary native chat turn, a native
// task turn and a scheduled run each hand the model a context with NO
// deadline. Restoring any turn deadline makes this fail.
func TestTurnContext_NoDeadlineOnNativeRootTaskOrScheduledTurns(t *testing.T) {
	cases := []struct {
		name string
		run  func(t *testing.T, al *AgentLoop)
	}{
		{"root chat turn", func(t *testing.T, al *AgentLoop) {
			if _, err := al.ProcessDirectAs(context.Background(), testDefaultAgentID, "hello", "no-deadline-root"); err != nil {
				t.Fatalf("ProcessDirect: %v", err)
			}
		}},
		{"native task turn", func(t *testing.T, al *AgentLoop) {
			if _, err := al.processTaskDirect(context.Background(), testDefaultAgentID, "do the task", "no-deadline-task", "task:no-deadline"); err != nil {
				t.Fatalf("processTaskDirect: %v", err)
			}
		}},
		{"scheduled run", func(t *testing.T, al *AgentLoop) {
			meta, err := al.GetSessionStore().NewScheduledSession(testDefaultAgentID)
			if err != nil {
				t.Fatalf("NewScheduledSession: %v", err)
			}
			if _, err := al.ProcessScheduled(context.Background(), testDefaultAgentID, meta.ID, "run the job", "scheduled", meta.ID); err != nil {
				t.Fatalf("ProcessScheduled: %v", err)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			provider := newDeadlineRecordingProvider(false)
			al, cleanup := newSteerALWithProvider(t, provider)
			defer cleanup()
			mintGenuineBootEpochForLoop(t, al)
			tc.run(t, al)
			calls, deadline := provider.sawDeadline()
			if calls == 0 {
				t.Fatal("the model was never called")
			}
			if deadline {
				t.Fatalf("the %s handed the model a context with a deadline — work has no time limit (issue #1306)", tc.name)
			}
		})
	}
}

// TestPlanSupervision_RealTurnHasNoDeadlineAndHoldsItsClaimUntilItSettles
// runs the REAL supervision dispatch (dispatchPlanTurn) with a model that
// keeps working (T2): the supervision turn's context has no deadline, its
// claim stays held — and the plan is not failed — however far the clock moves
// while it works, and the attempt is evaluated only after it settles.
func TestPlanSupervision_RealTurnHasNoDeadlineAndHoldsItsClaimUntilItSettles(t *testing.T) {
	h := newPlanWakeHarness(t)
	provider := newDeadlineRecordingProvider(true)
	supervisorInst, ok := h.al.GetRegistry().GetAgent(planSupervisorAgentID)
	if !ok {
		t.Fatal("supervisor agent not registered")
	}
	supervisorInst.Provider = provider
	// Release the gated model on every exit path (registered after the
	// harness, so it runs before the harness drains its wake turns) — a failed
	// assertion must end the test, not hang it.
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(provider.gate) }) }
	t.Cleanup(release)
	h.parkPlanAtSupervision(t, "p1", runningPlanWithDoD("p1", "telegram", "chat-1"))

	select {
	case <-provider.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the supervision turn never reached its model")
	}
	if _, deadline := provider.sawDeadline(); deadline {
		t.Fatal("the supervision turn's context carries a deadline — plan turns have no time limit (issue #1306)")
	}
	before, err := h.plans.Get("p1")
	if err != nil || before.Supervision == nil {
		t.Fatalf("SETUP: supervision record = %+v, err %v", before, err)
	}
	if !h.pe.supervisionTurnInFlight("p1") {
		t.Fatal("the running supervision turn does not hold its claim")
	}

	// Move the clock far past every supervision bound while the turn works.
	h.clock.Set(h.clock.Now().Add(10 * defaultSupervisionTurnTimeout))
	h.pe.planDecisionMu.Lock()
	cur, _ := h.plans.Get("p1")
	h.pe.evaluateSupervisionDeadlineLocked(cur)
	h.pe.planDecisionMu.Unlock()
	during, _ := h.plans.Get("p1")
	if during.State == plan.StateFailed || during.Supervision.Attempts != before.Supervision.Attempts {
		t.Fatalf("while the supervision turn was working the plan moved (state %s, attempts %d→%d)",
			during.State, before.Supervision.Attempts, during.Supervision.Attempts)
	}
	if !h.pe.supervisionTurnInFlight("p1") {
		t.Fatal("the claim was released while the supervision turn was still working")
	}

	release()
	h.pe.wakeWG.Wait()
	if h.pe.supervisionTurnInFlight("p1") {
		t.Fatal("the supervision claim is still held after the turn settled")
	}
	h.pe.planDecisionMu.Lock()
	cur, _ = h.plans.Get("p1")
	h.pe.evaluateSupervisionDeadlineLocked(cur)
	h.pe.planDecisionMu.Unlock()
	after, _ := h.plans.Get("p1")
	if after.Supervision.Attempts != before.Supervision.Attempts+1 && after.State != plan.StateFailed {
		t.Fatalf("after the turn settled past the interval the attempt was not evaluated (attempts %d→%d, state %s)",
			before.Supervision.Attempts, after.Supervision.Attempts, after.State)
	}
	h.pe.wakeWG.Wait()
}

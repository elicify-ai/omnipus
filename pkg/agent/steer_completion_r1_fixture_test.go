package agent

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// R1 timing is controlled only at the external provider and publication edges.
// Launch/Dispatch, producing identity, disposal, drain and all stores stay real.
// No helper stamps a running state or constructs an execution identity.
type r1CompletionProvider struct {
	mu       sync.Mutex
	requests [][]providers.Message
	answers  []string
	entered  chan int
	release  []chan struct{}
}

func (p *r1CompletionProvider) Chat(ctx context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	p.mu.Lock()
	index := len(p.requests)
	p.requests = append(p.requests, append([]providers.Message(nil), messages...))
	p.mu.Unlock()
	if index >= len(p.answers) {
		return nil, fmt.Errorf("R1 unexpected provider call %d; staged answers=%d", index+1, len(p.answers))
	}
	p.entered <- index
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-p.release[index]:
		return &providers.LLMResponse{Content: p.answers[index], FinishReason: "stop"}, nil
	}
}

func (*r1CompletionProvider) GetDefaultModel() string { return "r1-real-commit-boundary" }

func (p *r1CompletionProvider) Requests() [][]providers.Message {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([][]providers.Message, len(p.requests))
	for i := range p.requests {
		out[i] = append([]providers.Message(nil), p.requests[i]...)
	}
	return out
}

func (p *r1CompletionProvider) open(index int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	select {
	case <-p.release[index]:
	default:
		close(p.release[index])
	}
}

func (p *r1CompletionProvider) openAll() {
	for i := range p.release {
		p.open(i)
	}
}

func r1AwaitProvider(t *testing.T, p *r1CompletionProvider, wantIndex int) {
	t.Helper()
	select {
	case index := <-p.entered:
		if index != wantIndex {
			t.Fatalf("provider entry index=%d, want exactly %d", index, wantIndex)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("provider entry %d did not run; actual calls=%d", wantIndex+1, len(p.Requests()))
	}
}

func r1AdmitChild(t *testing.T, al *AgentLoop, parentID, callID string, answers ...string) (*session.LifecycleRecord, *r1CompletionProvider) {
	t.Helper()
	p := &r1CompletionProvider{answers: answers, entered: make(chan int, len(answers))}
	for range answers {
		p.release = append(p.release, make(chan struct{}))
	}
	inst, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	if !ok {
		t.Fatal("SETUP: default registered agent missing")
	}
	inst.Provider = p
	t.Cleanup(p.openAll)
	launched, err := NewSteerLauncher(al).Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: parentID, TargetAgentID: testDefaultAgentID,
		Task: "do delegated work", Origin: steer.Origin{Kind: steer.OriginKindDelegate, CallID: callID},
	})
	if err != nil {
		t.Fatalf("real Launch: %v", err)
	}
	result, err := NewSteerLauncher(al).Dispatch(context.Background(), launched.SessionID, launched.Generation)
	if err != nil || result.State != steer.DispatchRunning {
		t.Fatalf("real Dispatch=%+v error=%v, want running", result, err)
	}
	r1AwaitProvider(t, p, 0)
	rec := rootReopenedRecord(t, al, launched.SessionID)
	ts := al.getActiveTurnState(rec.SessionID)
	if rec.ExecutionID == nil || ts == nil || al.tsExecutionClaim(ts, rec.SessionID) != al.executionClaimFor(rec) {
		t.Fatalf("SETUP: real admission has no matching persisted/live execution identity: %+v", rec.ExecutionID)
	}
	return rec, p
}

func r1AssertNoFinal(t *testing.T, al *AgentLoop, child *session.LifecycleRecord) {
	t.Helper()
	rec := rootReopenedRecord(t, al, child.SessionID)
	if rec.Terminal() || rec.FinalDelivery != nil || rec.Generation != child.Generation {
		t.Fatalf("premature terminal/outbox: state=%q generation=%d final=%+v, want nonterminal G=%d without an outbox", rec.State, rec.Generation, rec.FinalDelivery, child.Generation)
	}
	msgs, _, _, err := al.GetMessageInboxStore().Drain(child.SteeringSessionID(), child.SessionID, "", 10)
	if err != nil || len(msgs) != 0 {
		t.Fatalf("premature parent final: messages=%d error=%v, want zero before the real commit", len(msgs), err)
	}
}

func r1AssertFinals(t *testing.T, al *AgentLoop, child *session.LifecycleRecord, want map[string]string) {
	t.Helper()
	msgs, _, more, err := al.GetMessageInboxStore().Drain(child.SteeringSessionID(), child.SessionID, "", 10)
	if err != nil || more || len(msgs) != len(want) {
		t.Fatalf("final inbox count=%d more=%v error=%v, want exactly %d", len(msgs), more, err, len(want))
	}
	got := make(map[string]string)
	for _, msg := range msgs {
		handback, decodeErr := msg.AsSessionMessageHandback()
		if decodeErr != nil {
			t.Fatalf("decode actual final %q: %v", messageIDOf(msg), decodeErr)
		}
		id := messageIDOf(msg)
		if _, duplicate := got[id]; duplicate {
			t.Fatalf("duplicate final %q", id)
		}
		got[id] = handback.ResultSoFar
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("actual final payloads=%v, want exact immutable finals=%v", got, want)
	}
}

// The real store, not this hook's invocation count, decides whether a final
// committed. The hook only chooses acceptance inside the already-open finishing
// window; provider/store/outbox/inbox assertions supply the boundary proof.
func r1InjectBeforeCommit(t *testing.T, al *AgentLoop, child *session.LifecycleRecord, inject func()) *int {
	t.Helper()
	calls := 0
	completeStateWriteTestHook = func(id string) {
		if id != child.SessionID {
			return
		}
		calls++
		if calls == 1 {
			r1AssertNoFinal(t, al, child)
			inject()
		}
	}
	t.Cleanup(func() { completeStateWriteTestHook = nil })
	return &calls
}

func r1RequireSameGenerationFinal(t *testing.T, al *AgentLoop, child *session.LifecycleRecord, answer string) {
	t.Helper()
	got := rootReopenedRecord(t, al, child.SessionID)
	if got.Generation != child.Generation || got.State != session.LifecycleCompleted || !reflect.DeepEqual(got.ExecutionID, child.ExecutionID) {
		t.Fatalf("same-admission completion: generation=%d state=%q identity=%+v, want G=%d completed under original %+v", got.Generation, got.State, got.ExecutionID, child.Generation, child.ExecutionID)
	}
	if got.FinalDelivery == nil || got.FinalDelivery.CommitID != child.ExecutionID.RunID {
		t.Fatalf("final producing identity=%+v, want original run %q", got.FinalDelivery, child.ExecutionID.RunID)
	}
	if count := al.pendingSteeringCountForScope(child.SessionID); count != 0 {
		t.Errorf("stranded steering=%d, want zero", count)
	}
	r1AssertFinals(t, al, child, map[string]string{fmt.Sprintf("%s:%d:final", child.SessionID, child.Generation): answer})
}

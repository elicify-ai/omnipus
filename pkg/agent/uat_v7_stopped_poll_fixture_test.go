package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

const qa3StoppedWorkspace = "qa3-v7-stopped-poll"
const qa3StoppedParentAnswer = "Both stopped helpers are handled."

// The ONLY substitutes are providers. Each helper reaches its genuine
// dispatched provider call and remains there until production Stop cancels it.
// No cancellation, notice, inbox, waker, tool or consumption hook is replaced.
type qa3StoppedHelperProvider struct {
	mu      sync.Mutex
	calls   int
	entered chan struct{}
	release chan struct{}
}

func (p *qa3StoppedHelperProvider) Chat(ctx context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	p.entered <- struct{}{}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-p.release:
		return nil, context.Canceled // Teardown-only provider release if setup failed.
	}
}
func (*qa3StoppedHelperProvider) GetDefaultModel() string { return "qa3-v7-helper" }

// On the first REAL notice-woken turn the model polls both helpers; the
// following request captures their real tool results. An unexpected extra
// wake gets a terminal answer, so the dispatch-count assertion exposes it.
type qa3StoppedParentProvider struct {
	mu       sync.Mutex
	action   string
	children []string
	requests [][]providers.Message
}

func (p *qa3StoppedParentProvider) Chat(_ context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	index := len(p.requests)
	p.requests = append(p.requests, append([]providers.Message(nil), messages...))
	if index == 0 && p.action != "" {
		calls := make([]providers.ToolCall, 0, len(p.children))
		for i, id := range p.children {
			calls = append(calls, providers.ToolCall{ID: fmt.Sprintf("qa3-v7-poll-%d", i), Name: "delegate",
				Arguments: map[string]any{"action": p.action, "session_id": id}})
		}
		return &providers.LLMResponse{ToolCalls: calls, FinishReason: "tool_calls"}, nil
	}
	return &providers.LLMResponse{Content: qa3StoppedParentAnswer, FinishReason: "stop"}, nil
}
func (*qa3StoppedParentProvider) GetDefaultModel() string { return "qa3-v7-parent" }
func (p *qa3StoppedParentProvider) Requests() [][]providers.Message {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([][]providers.Message, len(p.requests))
	for i := range p.requests {
		out[i] = append([]providers.Message(nil), p.requests[i]...)
	}
	return out
}

type qa3StoppedFixture struct {
	al       *AgentLoop
	parent   *qa3StoppedParentProvider
	helper   *qa3StoppedHelperProvider
	parentID string
	children []*session.LifecycleRecord
	noticeID []string
	wakes    []bus.InboundMessage
}

func newQA3StoppedFixture(t *testing.T, action string) *qa3StoppedFixture {
	t.Helper()
	home := seedWorkspaceGraph(t, qa3StoppedWorkspace, true, []graphEdge{
		edge(testDefaultAgentID, qaUATHelperAgentID, []string{"direct"}, nil),
	})
	parent := &qa3StoppedParentProvider{action: action}
	helper := &qa3StoppedHelperProvider{entered: make(chan struct{}, 2), release: make(chan struct{})}
	al := newQAUATDelegateLoop(t, home, parent)
	t.Cleanup(func() { close(helper.release) })
	inst, ok := al.GetRegistry().GetAgent(qaUATHelperAgentID)
	if !ok {
		t.Fatal("BLOCKED: helper agent not registered — required by V7")
	}
	inst.Provider = helper
	parentID := newTestSteeringSession(t, al, qa3StoppedWorkspace)
	f := &qa3StoppedFixture{al: al, parent: parent, helper: helper, parentID: parentID}
	ctx := tools.WithTranscriptSessionID(context.Background(), parentID)
	ctx = tools.WithAgentID(ctx, testDefaultAgentID)
	ctx = tools.WithWorkspaceID(ctx, qa3StoppedWorkspace)
	dt := delegateToolFor(t, al)
	for _, label := range []string{"A", "B"} {
		callCtx := tools.WithToolCallID(ctx, "qa3-v7-launch-"+label)
		res := dt.Execute(callCtx, map[string]any{"action": "run", "agent_id": qaUATHelperAgentID,
			"task": "Keep working on helper " + label + " until explicitly stopped.", "label": "V7 helper " + label})
		if res == nil || res.IsError {
			t.Fatalf("SETUP: real delegate(run) helper %s: %+v", label, res)
		}
		var launched generated.DelegateSessionResponse
		if err := json.Unmarshal([]byte(res.ForLLM), &launched); err != nil || string(launched.State) != "running" || launched.Generation != 1 {
			t.Fatalf("SETUP: real helper %s dispatch = %q error=%v; want running generation 1", label, res.ForLLM, err)
		}
		select {
		case <-helper.entered:
		case <-time.After(10 * time.Second): // Deadlock bound, never an elapsed-time oracle.
			t.Fatalf("SETUP: helper %s never reached real provider admission", label)
		}
		rec := rootReopenedRecord(t, al, launched.SessionId)
		ts := al.getActiveTurnState(rec.SessionID)
		if rec.State != session.LifecycleRunning || rec.SteeringSessionID() != parentID || rec.ExecutionID == nil || rec.ExecutionID.RunID == "" || ts == nil || al.tsExecutionClaim(ts, rec.SessionID) != al.executionClaimFor(rec) {
			t.Fatalf("SETUP: helper %s lacks matching real durable/live admission: %+v", label, rec)
		}
		f.children = append(f.children, rec)
		parent.children = append(parent.children, rec.SessionID)
	}
	// Real stop_all tool on each leaf: each leaf's tree is just itself. This
	// exercises registered tool -> one Stop -> owning execution settlement.
	for _, child := range f.children {
		res := dt.Execute(ctx, map[string]any{"action": "stop_all", "session_id": child.SessionID})
		if res == nil || res.IsError {
			t.Fatalf("SETUP: real helper Stop refused: %+v", res)
		}
	}
	joinGoalFixtureRuns(t, al) // Joins Stop settlement and notice publication too.
	for _, child := range f.children {
		stopped := rootReopenedRecord(t, al, child.SessionID)
		transitions, err := al.GetSessionLifecycleStore().ListStoppedTransitions(child.SessionID)
		if err != nil || len(transitions) != 1 || stopped.State != session.LifecycleStopped || stopped.StopNote == nil || stopped.Generation != child.Generation || stopped.Terminal() {
			t.Fatalf("SETUP: helper must have exactly one real landed stop: record=%+v transitions=%+v error=%v", stopped, transitions, err)
		}
		// D6's specified four-part identity, derived from the actual accepted
		// transition rather than the notice generator under test.
		id := fmt.Sprintf("stopped-notice:%s:%s:%d:%d", parentID, child.SessionID, child.Generation, transitions[0].StopSeq)
		f.noticeID = append(f.noticeID, id)
		pending := qa3PendingStoppedMessages(t, f, child.SessionID)
		if len(pending) != 1 {
			t.Fatalf("SETUP: helper %s pending notices=%d, want exactly one actual stop notice", child.SessionID, len(pending))
		}
		notice, err := pending[0].AsSessionMessageError()
		if err != nil || notice.MessageId != id || notice.SessionId != child.SessionID || notice.Fatal || notice.ParentSessionId == nil || *notice.ParentSessionId != parentID || notice.Generation == nil || *notice.Generation != child.Generation || !strings.HasPrefix(notice.Text, "stopped_child:") {
			t.Fatalf("SETUP: actual stopped notice lost specified identity/parent/nonfatal status: %+v error=%v", notice, err)
		}
	}
	// Retain actual transport messages to force the reported A-wake -> poll
	// both -> B-wake order, without replacing or synthesizing a waker.
	byID := make(map[string]bus.InboundMessage)
	for {
		select {
		case wake := <-al.bus.InboundChan():
			id := wake.Metadata["steer_message_id"]
			if _, duplicate := byID[id]; duplicate {
				t.Fatalf("SETUP: initial stopped notice published duplicate wake %q", id)
			}
			byID[id] = wake
		default:
			if len(byID) != 2 {
				t.Fatalf("SETUP: initial stop wake identities=%v, want exactly two", byID)
			}
			for _, id := range f.noticeID {
				wake, ok := byID[id]
				if !ok || wake.Channel != "system" || wake.AsyncTranscriptSessionID != parentID || !strings.Contains(wake.Content, "stopped_child:") {
					t.Fatalf("SETUP: real stop wake for %s lost parent identity: %+v", id, wake)
				}
				f.wakes = append(f.wakes, wake)
			}
			return f
		}
	}
}

func qa3PendingStoppedMessages(t *testing.T, f *qa3StoppedFixture, childID string) []generated.SessionMessage {
	t.Helper()
	msgs, _, more, err := f.al.GetMessageInboxStore().Drain(f.parentID, childID, "", 10)
	if err != nil || more {
		t.Fatalf("read real parent pending inbox: more=%v error=%v", more, err)
	}
	return msgs
}

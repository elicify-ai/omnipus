package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// These strings are controlled provider inputs, not observed production output.
// D5's oracle is one effective receipt of this exact final, never poll AND wake.
const qaUATHelperFinal = "UAT-D5-FINAL-ONCE"
const qaUATPollCallID = "uat-d5-parent-poll"
const qaUATHelperAgentID = "uat-helper"
const qaUATWorkspaceID = "uat-d5-d7-workspace"

// Only the paid provider is replaced. The first request holds a genuinely
// admitted helper; later requests drive the parent's REAL delegate poll tool.
// Keeping the real bus wake until after that poll selects the reported order
// without mocking a waker or using a production test hook.
type qaUATFinalProvider struct {
	mu           sync.Mutex
	requests     [][]providers.Message
	childID      string
	pollAction   string
	childEntered chan struct{}
	childRelease chan struct{}
	releaseOnce  sync.Once
}

func (p *qaUATFinalProvider) Chat(ctx context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	p.mu.Lock()
	index := len(p.requests)
	p.requests = append(p.requests, append([]providers.Message(nil), messages...))
	childID, action := p.childID, p.pollAction
	p.mu.Unlock()
	if index == 0 {
		close(p.childEntered)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-p.childRelease:
			return &providers.LLMResponse{Content: qaUATHelperFinal, FinishReason: "stop"}, nil
		}
	}
	if index == 1 && action != "" {
		return &providers.LLMResponse{ToolCalls: []providers.ToolCall{{
			ID: qaUATPollCallID, Name: "delegate",
			Arguments: map[string]any{"action": action, "session_id": childID},
		}}, FinishReason: "tool_calls"}, nil
	}
	return &providers.LLMResponse{Content: "Helper status checked.", FinishReason: "stop"}, nil
}

func (*qaUATFinalProvider) GetDefaultModel() string { return "uat-delegate-boundary" }

func (p *qaUATFinalProvider) open() { p.releaseOnce.Do(func() { close(p.childRelease) }) }

func (p *qaUATFinalProvider) Requests() [][]providers.Message {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([][]providers.Message, len(p.requests))
	for i := range p.requests {
		out[i] = append([]providers.Message(nil), p.requests[i]...)
	}
	return out
}

type qaUATDelegateFixture struct {
	al       *AgentLoop
	provider *qaUATFinalProvider
	parentID string
	child    *session.LifecycleRecord
}

func newQAUATDelegateFixture(t *testing.T, pollAction string) *qaUATDelegateFixture {
	t.Helper()
	home := seedWorkspaceGraph(t, qaUATWorkspaceID, true, []graphEdge{
		edge(testDefaultAgentID, qaUATHelperAgentID, []string{"direct"}, nil),
	})
	p := &qaUATFinalProvider{
		pollAction: pollAction, childEntered: make(chan struct{}), childRelease: make(chan struct{}),
	}
	al := newQAUATDelegateLoop(t, home, p)
	t.Cleanup(p.open) // Released before the loop's joined Close and TempDir removal.
	parentID := newTestSteeringSession(t, al, qaUATWorkspaceID)
	ctx := tools.WithTranscriptSessionID(context.Background(), parentID)
	ctx = tools.WithAgentID(ctx, testDefaultAgentID)
	ctx = tools.WithToolCallID(ctx, "uat-d5-real-delegate")
	result := delegateToolFor(t, al).Execute(ctx, map[string]any{
		"action": "run", "agent_id": qaUATHelperAgentID,
		"task": "Return exactly this final answer: " + qaUATHelperFinal, "label": "Exactly-once helper",
	})
	if result == nil || result.IsError {
		t.Fatalf("SETUP: real delegate run refused: %+v", result)
	}
	var launched generated.DelegateSessionResponse
	if err := json.Unmarshal([]byte(result.ForLLM), &launched); err != nil {
		t.Fatalf("SETUP: decode real delegate launch response %q: %v", result.ForLLM, err)
	}
	if launched.State != generated.DelegateSessionResponseState("running") || launched.SessionId == "" || launched.Generation != 1 {
		t.Fatalf("SETUP: fresh real dispatch = %+v, want a running generation-1 helper", launched)
	}
	select {
	case <-p.childEntered:
	case <-time.After(10 * time.Second): // Deadlock bound, not an elapsed-time oracle.
		t.Fatal("SETUP: admitted helper did not reach its real provider boundary")
	}
	child := rootReopenedRecord(t, al, launched.SessionId)
	ts := al.getActiveTurnState(child.SessionID)
	if child.State != session.LifecycleRunning || child.SteeringSessionID() != parentID ||
		child.ExecutionID == nil || child.ExecutionID.RunID == "" || child.ExecutionID.BootSeq != al.bootEpochFor() ||
		ts == nil || al.tsExecutionClaim(ts, child.SessionID) != al.executionClaimFor(child) {
		t.Fatalf("SETUP: helper lacks real matching durable/live admission: record=%+v turn=%p", child, ts)
	}
	p.mu.Lock()
	p.childID = child.SessionID
	p.mu.Unlock()
	return &qaUATDelegateFixture{al: al, provider: p, parentID: parentID, child: child}
}

// Genuine boot/store/agent setup, with shipped tool policies and an on-disk
// workspace edge. Never replace the delegation deny checker to get a green.
func newQAUATDelegateLoop(t *testing.T, home string, provider providers.LLMProvider) *AgentLoop {
	t.Helper()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: home, DefaultModel: config.DefaultModel{Model: "test-model"},
				DefaultAgentID: testDefaultAgentID, MaxTokens: 4096, MaxToolIterations: 10,
			},
			List: []config.AgentConfig{
				{ID: testDefaultAgentID, Home: home},
				{ID: qaUATHelperAgentID, Home: home},
			},
		},
		Sandbox: config.OmnipusSandboxConfig{ToolPolicies: config.DefaultConfig().Sandbox.ToolPolicies},
	}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), provider)
	t.Cleanup(al.Close)
	al.SetSessionMessagingStores(
		session.NewMessageInboxStore(filepath.Join(home, "session_messages")),
		session.NewLifecycleStore(filepath.Join(home, "session_lifecycle")),
	)
	epochDir := filepath.Join(home, "boot_epoch")
	if err := os.MkdirAll(epochDir, 0o700); err != nil {
		t.Fatalf("SETUP: create genuine boot epoch directory: %v", err)
	}
	boot := session.NewBootEpochStore(epochDir)
	if epoch, err := boot.Mint(); err != nil || epoch == 0 || boot.Current() != epoch {
		t.Fatalf("SETUP: genuine boot epoch mint=%d current=%d error=%v", epoch, boot.Current(), err)
	}
	al.SetBootEpochStore(boot)
	al.SetSteerSessionLauncher(NewSteerLauncher(al))
	wireSteerCompletionDeps(t, al)
	return al
}

func (f *qaUATDelegateFixture) completeAndRetainWake(t *testing.T) bus.InboundMessage {
	t.Helper()
	f.provider.open()
	joinGoalFixtureRuns(t, f.al) // Join completion/disposal too, not just model exit.
	completed := rootReopenedRecord(t, f.al, f.child.SessionID)
	if completed.State != session.LifecycleCompleted || completed.Generation != f.child.Generation ||
		!reflect.DeepEqual(completed.ExecutionID, f.child.ExecutionID) || completed.FinalDelivery == nil {
		t.Fatalf("SETUP: real helper completion lost admitted identity/final: %+v", completed)
	}
	msgs, _, more, err := f.al.GetMessageInboxStore().Drain(f.parentID, f.child.SessionID, "", 10)
	if err != nil || more || len(msgs) != 1 {
		t.Fatalf("SETUP: real completed helper inbox = %d entries, more=%v error=%v; want exactly 1", len(msgs), more, err)
	}
	handback, err := msgs[0].AsSessionMessageHandback()
	wantID := fmt.Sprintf("%s:%d:final", f.child.SessionID, f.child.Generation)
	if err != nil || handback.Mode != generated.SessionMessageHandbackModeFinal || handback.ResultSoFar != qaUATHelperFinal ||
		handback.MessageId != wantID || handback.SessionId != f.child.SessionID {
		t.Fatalf("SETUP: actual helper final = %+v error=%v, want final %q with id %q from this child", handback, err, qaUATHelperFinal, wantID)
	}
	select {
	case wake := <-f.al.bus.InboundChan():
		if wake.Channel != "system" || wake.AsyncTranscriptSessionID != f.parentID ||
			wake.Metadata["steer_message_id"] != wantID || !strings.Contains(wake.Content, qaUATHelperFinal) {
			t.Fatalf("SETUP: actual bus hand-back wake lost parent/final identity: %+v", wake)
		}
		return wake
	case <-time.After(10 * time.Second):
		t.Fatal("SETUP: completed helper did not publish a real parent hand-back wake")
		return bus.InboundMessage{}
	}
}

package agent

import (
	"context"
	"fmt"
	"maps"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/addressing"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/require"
)

const wu4OriginProbeName = "wu4_origin_context_probe"

// This workload tool reads the same real execution context a builtin receives.
// It neither computes nor installs origin and does not stand in for an approval.
type wu4OriginObservation struct {
	label            string
	originPresent    bool
	channelTriggered bool
	turnID           string
	sessionID        string
}

type wu4OriginProbe struct {
	tools.BaseTool
	mu           sync.Mutex
	observations []wu4OriginObservation
	afterFirst   func() error
	arrivalErr   error
}

func (*wu4OriginProbe) Name() string           { return wu4OriginProbeName }
func (*wu4OriginProbe) Description() string    { return "Observe the executing workload's input origin" }
func (*wu4OriginProbe) Scope() tools.ToolScope { return tools.ScopeGeneral }
func (*wu4OriginProbe) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (p *wu4OriginProbe) Execute(ctx context.Context, _ map[string]any) *tools.ToolResult {
	origin := tools.ToolTurnOrigin(ctx)
	p.mu.Lock()
	p.observations = append(p.observations, wu4OriginObservation{
		label: origin.Label(), originPresent: origin != nil, channelTriggered: origin.ChannelTriggered(),
		turnID: tools.ToolTurnID(ctx), sessionID: tools.ToolTranscriptSessionID(ctx),
	})
	first := len(p.observations) == 1
	p.mu.Unlock()
	if first && p.afterFirst != nil {
		if err := p.afterFirst(); err != nil {
			p.mu.Lock()
			p.arrivalErr = err
			p.mu.Unlock()
			return &tools.ToolResult{ForLLM: "test input arrival failed", IsError: true}
		}
	}
	return &tools.ToolResult{ForLLM: "origin observation recorded"}
}

func (p *wu4OriginProbe) snapshot() ([]wu4OriginObservation, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]wu4OriginObservation(nil), p.observations...), p.arrivalErr
}

// Only the external model edge is scripted. The emitted function calls go
// through the ordinary tool policy and execution pipeline.
type wu4OriginProvider struct {
	mu        sync.Mutex
	responses []providers.LLMResponse
	requests  [][]providers.Message
}

func (*wu4OriginProvider) GetDefaultModel() string { return "test-model" }
func (p *wu4OriginProvider) Chat(_ context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	index := len(p.requests)
	p.requests = append(p.requests, append([]providers.Message(nil), messages...))
	if index >= len(p.responses) {
		return nil, fmt.Errorf("origin probe: unexpected actual provider call %d", index+1)
	}
	response := p.responses[index]
	return &response, nil
}
func (p *wu4OriginProvider) snapshot() [][]providers.Message {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([][]providers.Message(nil), p.requests...)
}

func wu4OriginCall(id string) providers.LLMResponse {
	return providers.LLMResponse{ToolCalls: []providers.ToolCall{{
		ID: id, Type: "function", Name: wu4OriginProbeName, Arguments: map[string]any{},
	}}}
}

func wu4OriginHarness(t *testing.T, midTurn bool) (*secretWakeAcceptanceFixture, *wu4OriginProvider, *wu4OriginProbe) {
	t.Helper()
	f := newSecretWakeAcceptanceFixture(t, false, false)
	p := &wu4OriginProvider{responses: []providers.LLMResponse{
		wu4OriginCall("origin-first-probe"),
		{Content: "origin original answer", FinishReason: "stop"},
		wu4OriginCall("origin-continuation-probe"),
		{Content: "origin continuation answer", FinishReason: "stop"},
	}}
	if midTurn {
		p.responses = []providers.LLMResponse{
			wu4OriginCall("origin-before-arrival"), wu4OriginCall("origin-after-consumption"),
			{Content: "origin continuation answer", FinishReason: "stop"},
		}
	}
	agent, ok := f.al.GetRegistry().GetAgent("omnipus")
	require.True(t, ok, "fixture's real agent must exist")
	agent.Provider = p
	probe := &wu4OriginProbe{}
	agent.Tools.Register(probe)
	policy := *agent.LoadToolPolicy()
	policy.Policies = maps.Clone(policy.Policies)
	if policy.Policies == nil {
		policy.Policies = make(map[string]config.ToolPolicy)
	}
	policy.Policies[wu4OriginProbeName] = "allow"
	agent.StoreToolPolicy(&policy)
	registerAgent(t, f.al, f.home, "noah", p)
	return f, p, probe
}

// Channel provenance is supplied on the real adapter/bus input shape; peer
// provenance is stamped by the real server publisher, not provider metadata.
func wu4OriginInput(t *testing.T, f *secretWakeAcceptanceFixture, sessionID, kind string) bus.InboundMessage {
	t.Helper()
	text := "P0-d late " + kind + " instruction"
	switch kind {
	case "channel":
		return bus.InboundMessage{Channel: "telegram", InstanceID: "telegram-ops", ChatID: "origin-chat", SessionID: sessionID,
			Content: text, Sender: bus.SenderInfo{DisplayName: "Anna", CanonicalID: "telegram:anna"}}
	case "web":
		return bus.InboundMessage{Channel: "webchat", ChatID: "origin-chat", SessionID: sessionID,
			Content: text, GatewayUserID: "qa-secret-wake-owner"}
	case "peer":
		require.NoError(t, f.al.publishRequestWake(context.Background(),
			addressing.Pair{WorkspaceID: testHarnessWorkspaceMembershipID, AgentID: "omnipus"},
			addressing.Sender{Agent: addressing.Pair{WorkspaceID: testHarnessWorkspaceMembershipID, AgentID: "noah"}},
			"wu4-p0d-peer-request", sessionID, text))
		select {
		case msg := <-f.bus.InboundChan():
			require.Equal(t, "wu4-p0d-peer-request", msg.TranscriptEntryID, "instrument: take the actual server-published peer wake")
			require.Equal(t, sessionID, msg.SessionID)
			return msg
		default:
			t.Fatal("real peer publisher did not publish its input")
		}
	default:
		t.Fatalf("unknown origin fixture %q", kind)
	}
	return bus.InboundMessage{}
}

type wu4OriginClosingObserver struct {
	al      *AgentLoop
	childID string
	input   bus.InboundMessage
	once    sync.Once
	mu      sync.Mutex
	calls   int
	err     error
}

func (o *wu4OriginClosingObserver) Observe(boundary steer.Boundary, sessionID string, _ steer.Audience) {
	if boundary != steer.BoundaryFinalReply || sessionID != o.childID {
		return
	}
	o.once.Do(func() {
		var err error
		// Observe the public session-scoped live-turn view, never fabricate or
		// install an activeTurnStates entry to make the missing handoff work.
		if o.al.GetActiveTurnBySession(sessionID) != nil {
			err = fmt.Errorf("instrument: previous helper turn still active at final-reply boundary")
		} else {
			err = o.al.enqueueHelperSteeringFromMessage(o.input, "omnipus", false)
		}
		o.mu.Lock()
		o.calls++
		o.err = err
		o.mu.Unlock()
	})
}
func (o *wu4OriginClosingObserver) result() (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.calls, o.err
}

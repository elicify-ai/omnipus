// Copyright (c) 2026 Omnipus contributors
// License: MIT

package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

const terminalAcceptanceModel = "terminal-outcome-acceptance-model"
const terminalAcceptanceTool = "terminal_outcome_acceptance_probe"

// Only the provider and external operation are scripted. The turn engine,
// EventBus, streamer, outbound relay, session store and replay are real.
// This is a separate fixture, not a dependency on the unmerged RC5 RED pack.
type terminalAcceptanceProvider struct {
	narration string
	answer    string
	calls     atomic.Int32
}

func (p *terminalAcceptanceProvider) GetDefaultModel() string { return terminalAcceptanceModel }
func (p *terminalAcceptanceProvider) Chat(
	_ context.Context, _ []providers.Message, _ []providers.ToolDefinition,
	_ string, _ map[string]any,
) (*providers.LLMResponse, error) {
	switch p.calls.Add(1) {
	case 1:
		return &providers.LLMResponse{
			Content: p.narration, FinishReason: "tool_calls",
			ToolCalls: []providers.ToolCall{{ID: "acceptance-probe-call", Function: &providers.FunctionCall{
				Name: terminalAcceptanceTool, Arguments: "{}",
			}}},
		}, nil
	case 2:
		return &providers.LLMResponse{Content: p.answer, FinishReason: "stop"}, nil
	default:
		return nil, fmt.Errorf("acceptance fixture: unexpected provider call %d", p.calls.Load())
	}
}

func (p *terminalAcceptanceProvider) ChatStream(
	ctx context.Context, messages []providers.Message, defs []providers.ToolDefinition,
	model string, opts map[string]any, onChunk func(string), _ providers.OnToolCallProgress,
) (*providers.LLMResponse, error) {
	response, err := p.Chat(ctx, messages, defs, model, opts)
	if err == nil && response.Content != "" {
		onChunk(response.Content)
	}
	return response, err
}

var _ providers.StreamingProvider = (*terminalAcceptanceProvider)(nil)

type terminalAcceptanceProbe struct{ calls atomic.Int32 }

func (p *terminalAcceptanceProbe) Name() string { return terminalAcceptanceTool }
func (p *terminalAcceptanceProbe) Description() string {
	return "Run the acceptance fixture operation."
}
func (p *terminalAcceptanceProbe) Scope() tools.ToolScope       { return tools.ScopeCore }
func (p *terminalAcceptanceProbe) Category() tools.ToolCategory { return tools.CategoryCore }
func (p *terminalAcceptanceProbe) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (p *terminalAcceptanceProbe) Execute(context.Context, map[string]any) *tools.ToolResult {
	p.calls.Add(1)
	return &tools.ToolResult{ForLLM: "acceptance probe complete"}
}

type terminalAcceptanceDelivery struct {
	message bus.OutboundMessage
	err     error
}

func newTerminalAcceptanceHandler(
	t *testing.T, provider *terminalAcceptanceProvider, probe *terminalAcceptanceProbe, limit int,
) (*WSHandler, agent.EventSubscription, <-chan terminalAcceptanceDelivery) {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080, DevModeBypass: true},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: t.TempDir(), DefaultModel: config.DefaultModel{Model: terminalAcceptanceModel},
				MaxTokens: 4096, MaxToolIterations: limit,
			},
			List: []config.AgentConfig{{ID: "mia"}},
		},
	}
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	al := mustAgentLoop(t, cfg, msgBus, provider)
	al.RegisterTool(probe)
	inst, ok := al.GetRegistry().GetAgent("mia")
	require.True(t, ok, "fixture: Mia must be registered")
	inst.StoreToolPolicy(&tools.ToolPolicyCfg{
		Policies: map[string]config.ToolPolicy{terminalAcceptanceTool: config.ToolPolicyAllow},
	})
	handler := newWSHandler(msgBus, al, "")
	msgBus.SetStreamDelegate(handler)
	webchat := newWebchatChannel(handler)
	handler.webchatCh = webchat
	// Subscribe separately; replacing the production synchronous tap would
	// disable the very gateway event-to-frame path this test must exercise.
	sub := al.SubscribeEvents(256)
	t.Cleanup(func() { al.UnsubscribeEvents(sub.ID) })
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- al.Run(ctx) }()
	delivered := make(chan terminalAcceptanceDelivery, 1)
	relayDone := make(chan struct{})
	go func() {
		defer close(relayDone)
		for {
			select {
			case <-ctx.Done():
				return
			case message, ok := <-msgBus.OutboundChan():
				if !ok {
					return
				}
				var err error
				if message.Channel != "webchat" {
					err = fmt.Errorf("fixture: unexpected outbound channel %q", message.Channel)
				} else {
					err = webchat.Send(ctx, message)
				}
				select {
				case delivered <- terminalAcceptanceDelivery{message: message, err: err}:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-runDone:
			if !errors.Is(err, context.Canceled) {
				assert.NoError(t, err, "fixture: loop shutdown")
			}
		case <-time.After(busDeliveryTimeout):
			t.Error("fixture: agent loop did not shut down")
		}
		select {
		case <-relayDone:
		case <-time.After(busDeliveryTimeout):
			t.Error("fixture: outbound relay did not shut down")
		}
	})
	return handler, sub, delivered
}

// The turn/relay has already completed before collection. A read timeout ends
// the complete capture, including trailing frames; other socket errors fail.
func terminalAcceptanceCollect(t *testing.T, conn *websocket.Conn) []collectedFrame {
	t.Helper()
	var frames []collectedFrame
	for {
		require.NoError(t, conn.SetReadDeadline(time.Now().Add(500*time.Millisecond)))
		_, raw, err := conn.ReadMessage()
		if err != nil {
			var networkErr net.Error
			if errors.As(err, &networkErr) && networkErr.Timeout() {
				return frames
			}
			t.Fatalf("acceptance capture: unexpected socket error: %v", err)
		}
		var probe map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(raw, &probe))
		var kind string
		require.NoError(t, json.Unmarshal(probe["type"], &kind))
		frames = append(frames, collectedFrame{Type: kind, Raw: raw})
	}
}

func terminalAcceptanceLiveText(t *testing.T, frames []collectedFrame, sessionID, turnID string) (string, map[string]string) {
	t.Helper()
	var ids []string
	byID := make(map[string]string)
	for _, frame := range frames {
		if frame.Type != "token" {
			continue
		}
		var token generated.TokenFrame
		require.NoError(t, json.Unmarshal(frame.Raw, &token))
		if token.SessionId != sessionID {
			continue
		}
		require.NotNil(t, token.TurnId, "live content must carry the observed turn identity")
		assert.Equal(t, turnID, *token.TurnId)
		require.NotNil(t, token.MessageId, "live content must carry a stable entry identity")
		id := *token.MessageId
		if _, exists := byID[id]; !exists {
			ids = append(ids, id)
		}
		if token.Replace != nil && *token.Replace {
			byID[id] = token.Content
		} else {
			byID[id] += token.Content
		}
	}
	var text strings.Builder
	for _, id := range ids {
		text.WriteString(byID[id])
	}
	return text.String(), byID
}

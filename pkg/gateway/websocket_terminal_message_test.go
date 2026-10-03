// Copyright (c) 2026 Omnipus contributors
// License: MIT

package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// RED plan for #1081 stream C / RC5:
//   - Requirement: every real stop delivers a terminal message on every exit
//     path, both live and in the transcript (founder's stream-C brief).
//   - Exact cap-message oracle: tool-iteration-limit-spec.md, Machine-Verifiable
//     Constraints / Tool-limit message. Do not read toolLimitResponse as the oracle.
//   - Real boundary: AgentLoop.Run -> wsStreamer -> webchatChannel.Send -> a real
//     WebSocket client, plus the real session store. Only the provider and the
//     external tool operation are scripted; none of the delivery code is mocked.
//   - Cases: silent tool round at the cap; narrated tool round followed by a
//     normal answer below the cap; narrated tool round at the cap.
//   - CHECK probes (deferred): suppress only the final transcript write; suppress
//     only its live delivery; duplicate already-streamed narration on finalize.
//   - Not covered: limit-value validation, cancellation, frontend rendering,
//     historical occurrence. GREEN and mutation proof are deferred to CHECK.
const terminalMessageCapNotice = "I've reached this agent's limit of tool steps for one turn without a final response. An admin can raise the limit in Settings → Performance (\"Max tool calls per turn\"), and each agent's own lower limit is on its profile's Advanced tab."

const terminalMessageModel = "terminal-message-test-model"
const terminalMessageToolName = "terminal_message_probe"

type terminalMessageStreamingProvider struct {
	steps []providers.LLMResponse
	calls atomic.Int32
}

func (p *terminalMessageStreamingProvider) GetDefaultModel() string { return terminalMessageModel }

func (p *terminalMessageStreamingProvider) Chat(
	_ context.Context, _ []providers.Message, _ []providers.ToolDefinition,
	_ string, _ map[string]any,
) (*providers.LLMResponse, error) {
	index := int(p.calls.Add(1)) - 1
	if index >= len(p.steps) {
		return nil, fmt.Errorf("terminal-message fixture: unexpected provider call %d", index+1)
	}
	response := p.steps[index]
	return &response, nil
}

func (p *terminalMessageStreamingProvider) ChatStream(
	ctx context.Context, messages []providers.Message, defs []providers.ToolDefinition,
	model string, opts map[string]any, onChunk func(string), _ providers.OnToolCallProgress,
) (*providers.LLMResponse, error) {
	response, err := p.Chat(ctx, messages, defs, model, opts)
	if err == nil && response.Content != "" {
		onChunk(response.Content)
	}
	return response, err
}

var _ providers.StreamingProvider = (*terminalMessageStreamingProvider)(nil)

type terminalMessageProbe struct{ calls atomic.Int32 }

func (p *terminalMessageProbe) Name() string { return terminalMessageToolName }
func (p *terminalMessageProbe) Description() string {
	return "Run the terminal-message fixture operation."
}
func (p *terminalMessageProbe) Scope() tools.ToolScope       { return tools.ScopeCore }
func (p *terminalMessageProbe) Category() tools.ToolCategory { return tools.CategoryCore }
func (p *terminalMessageProbe) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (p *terminalMessageProbe) Execute(context.Context, map[string]any) *tools.ToolResult {
	p.calls.Add(1)
	return &tools.ToolResult{ForLLM: "probe complete"}
}

type terminalMessageDelivery struct {
	message bus.OutboundMessage
	err     error
}

// Mirror websocket_provider_refusal_test.go's real-loop/webchat fixture, but
// acknowledge completion of Send before collecting frames. A missing cap token
// therefore cannot be blamed on a relay that the test never ran or waited for.
func newTerminalMessageWSHandler(
	t *testing.T, provider *terminalMessageStreamingProvider, probe *terminalMessageProbe, maxIterations int,
) (*WSHandler, <-chan terminalMessageDelivery) {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080, DevModeBypass: true},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: t.TempDir(), DefaultModel: config.DefaultModel{Model: terminalMessageModel},
				MaxTokens: 4096, MaxToolIterations: maxIterations,
			},
			List: []config.AgentConfig{{ID: "mia"}},
		},
	}
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	al := mustAgentLoop(t, cfg, msgBus, provider)
	al.RegisterTool(probe)
	inst, ok := al.GetRegistry().GetAgent("mia")
	require.True(t, ok, "fixture: chat target must exist")
	inst.StoreToolPolicy(&tools.ToolPolicyCfg{
		Policies: map[string]config.ToolPolicy{terminalMessageToolName: config.ToolPolicyAllow},
	})

	handler := newWSHandler(msgBus, al, "")
	msgBus.SetStreamDelegate(handler)
	webchat := newWebchatChannel(handler)
	handler.webchatCh = webchat
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- al.Run(ctx) }()

	delivered := make(chan terminalMessageDelivery, 1)
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
				case delivered <- terminalMessageDelivery{message: message, err: err}:
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
				assert.NoError(t, err, "fixture: agent loop shutdown")
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
	return handler, delivered
}

func TestWS_IterationCap_AfterStreamedNarration_DeliversTerminalMessage(t *testing.T) {
	const narration = "I will run the probe."
	const normalAnswer = "The probe completed."
	cases := []struct {
		name          string
		narration     string
		maxIterations int
		wantTerminal  string
		wantCalls     int32
		wantFailed    bool
	}{
		{"silent_tool_round_at_cap_control", "", 1, terminalMessageCapNotice, 1, true},
		{"narrated_round_then_final_answer_control", narration, 2, normalAnswer, 2, false},
		{"narrated_tool_round_at_cap", narration, 1, terminalMessageCapNotice, 1, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			provider := &terminalMessageStreamingProvider{steps: []providers.LLMResponse{
				{
					Content: tc.narration, FinishReason: "tool_calls",
					ToolCalls: []providers.ToolCall{{ID: "terminal-probe-call", Function: &providers.FunctionCall{
						Name: terminalMessageToolName, Arguments: "{}",
					}}},
				},
				{Content: normalAnswer, FinishReason: "stop"},
			}}
			probe := &terminalMessageProbe{}
			handler, delivered := newTerminalMessageWSHandler(t, provider, probe, tc.maxIterations)
			store := handler.agentLoop.GetSessionStore()
			require.NotNil(t, store, "fixture: a real session store is required")
			meta, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
			require.NoError(t, err)
			srv := httptest.NewServer(handler)
			t.Cleanup(srv.Close)
			t.Cleanup(handler.Wait)
			conn := dialTestWS(t, srv)
			t.Cleanup(func() { assert.NoError(t, conn.Close()) })
			sendWSAuthFrameDevMode(t, conn)
			agentID := "mia"
			data, err := json.Marshal(generated.MessageFrame{
				Type: "message", Content: "Run the probe.", AgentId: &agentID, SessionId: &meta.ID,
			})
			require.NoError(t, err)
			require.NoError(t, conn.WriteMessage(websocket.TextMessage, data))

			select {
			case delivery := <-delivered:
				require.NoError(t, delivery.err, "fixture: the real webchat Send must finish")
				assert.Equal(t, "webchat", delivery.message.Channel)
				assert.Equal(t, tc.wantTerminal, delivery.message.Content,
					"fixture: the loop must reach the expected exit and supply its terminal notice")
			case <-time.After(busDeliveryTimeout):
				t.Fatal("fixture: no outbound response reached the real webchat relay")
			}
			require.Equal(t, tc.wantCalls, provider.calls.Load(), "fixture: exact provider rounds")
			require.Equal(t, int32(1), probe.calls.Load(), "fixture: the tool operation actually ran")

			frames := collectWSFrames(t, conn, 2*time.Second, busDeliveryTimeout)
			var liveText strings.Builder
			var doneCount int
			var failedDone bool
			for _, frame := range frames {
				switch frame.Type {
				case "token":
					var token generated.TokenFrame
					require.NoError(t, json.Unmarshal(frame.Raw, &token))
					assert.Equal(t, meta.ID, token.SessionId)
					liveText.WriteString(token.Content)
				case "error":
					var notice generated.ErrorFrame
					require.NoError(t, json.Unmarshal(frame.Raw, &notice))
					liveText.WriteString(notice.Message)
				case "done":
					var done generated.DoneFrame
					require.NoError(t, json.Unmarshal(frame.Raw, &done))
					assert.Equal(t, meta.ID, done.SessionId)
					doneCount++
					if done.Stats != nil && done.Stats.TurnFailed != nil && *done.Stats.TurnFailed {
						failedDone = true
					}
				}
			}
			require.Positive(t, doneCount, "fixture: the client must observe completion, not a collection timeout")
			assert.Equal(t, tc.wantFailed, failedDone, "fixture: cap exit versus normal completion")
			assert.Equal(t, tc.narration+tc.wantTerminal, liveText.String(),
				"LIVE terminal message must follow narration exactly once; frames=%v", collectedFrameTypes(frames))

			entries, err := store.ReadTranscript(meta.ID)
			require.NoError(t, err)
			var assistantText []string
			var toolCalls int
			for _, entry := range entries {
				if entry.Role == "assistant" && entry.Content != "" {
					assistantText = append(assistantText, entry.Content)
				}
				toolCalls += len(entry.ToolCalls)
			}
			require.Positive(t, toolCalls, "fixture: transcript reads the turn's real tool-call record")
			wantText := []string{tc.wantTerminal}
			if tc.narration != "" {
				wantText = []string{tc.narration, tc.wantTerminal}
			}
			assert.Equal(t, wantText, assistantText,
				"TRANSCRIPT must preserve narration then the terminal message, without duplicate narration")
		})
	}
}

package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

type ceilingNoticeProvider struct{ calls int }

func (*ceilingNoticeProvider) GetDefaultModel() string { return "ceiling-notice-model" }
func (p *ceilingNoticeProvider) Chat(context.Context, []providers.Message, []providers.ToolDefinition, string, map[string]any) (*providers.LLMResponse, error) {
	p.calls++
	if p.calls > 2 {
		return nil, fmt.Errorf("unexpected model call %d past the ceiling", p.calls)
	}
	return &providers.LLMResponse{
		Content: fmt.Sprintf("Reading note %d.", p.calls), FinishReason: "tool_calls",
		ToolCalls: []providers.ToolCall{{ID: fmt.Sprintf("notice-call-%d", p.calls), Type: "function", Name: "ceiling_notice_tool",
			Function: &providers.FunctionCall{Name: "ceiling_notice_tool", Arguments: `{}`}}},
	}, nil
}
func (p *ceilingNoticeProvider) ChatStream(ctx context.Context, messages []providers.Message, defs []providers.ToolDefinition, model string, opts map[string]any, chunk func(string), _ providers.OnToolCallProgress) (*providers.LLMResponse, error) {
	response, err := p.Chat(ctx, messages, defs, model, opts)
	if err == nil {
		chunk(response.Content)
	}
	return response, err
}

type ceilingNoticeTool struct {
	tools.BaseTool
	calls int
}

func (*ceilingNoticeTool) Name() string        { return "ceiling_notice_tool" }
func (*ceilingNoticeTool) Description() string { return "Test process-edge note reader" }
func (*ceilingNoticeTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (*ceilingNoticeTool) Scope() tools.ToolScope { return tools.ScopeGeneral }
func (tool *ceilingNoticeTool) Execute(context.Context, map[string]any) *tools.ToolResult {
	tool.calls++
	return &tools.ToolResult{ForLLM: fmt.Sprintf("note %d read", tool.calls)}
}

type ceilingNoticePlainProvider struct{ provider *ceilingNoticeProvider }

func (p *ceilingNoticePlainProvider) GetDefaultModel() string { return p.provider.GetDefaultModel() }
func (p *ceilingNoticePlainProvider) Chat(ctx context.Context, messages []providers.Message, defs []providers.ToolDefinition, model string, opts map[string]any) (*providers.LLMResponse, error) {
	return p.provider.Chat(ctx, messages, defs, model, opts)
}

// Failure-dispatch row 7.2: the REAL engine and gateway streamer must both
// identify the limit sentence as harness output, including the Send fallback.
// Only the paid provider and external tool execution are scripted.
func TestIterationLimitSystemNotice(t *testing.T) {
	t.Run("streaming_provider", func(t *testing.T) { testIterationLimitSystemNotice(t, true) })
	t.Run("nonstreaming_provider", func(t *testing.T) { testIterationLimitSystemNotice(t, false) })
}

func testIterationLimitSystemNotice(t *testing.T, streaming bool) {
	const notice = "I've reached this agent's limit of tool steps for one turn without a final response. " +
		"An admin can raise the limit in Settings → Performance (\"Max tool calls per turn\"), " +
		"and each agent's own lower limit is on its profile's Advanced tab."
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OMNIPUS_HOME", home)
	cfg := &config.Config{Agents: config.AgentsConfig{
		Defaults: config.AgentDefaults{Home: filepath.Join(home, "workspace"),
			DefaultModel: config.DefaultModel{Model: "ceiling-notice-model"}, MaxTokens: 4096, MaxToolIterations: 10},
		List: []config.AgentConfig{{ID: "mia", MaxToolIterations: 2}},
	}}
	cfg.Sandbox.ToolPolicies = config.DefaultToolPolicyCeiling()
	cfg.Sandbox.ToolPolicies["ceiling_notice_tool"] = "allow"
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	provider := &ceilingNoticeProvider{}
	var modelProvider providers.LLMProvider = provider
	if !streaming {
		modelProvider = &ceilingNoticePlainProvider{provider: provider}
	}
	al := mustAgentLoop(t, cfg, msgBus, modelProvider)
	tool := &ceilingNoticeTool{}
	al.RegisterTool(tool)
	handler := newWSHandler(msgBus, al, "")
	handler.webchatCh = newWebchatChannel(handler)
	t.Cleanup(handler.Wait)
	msgBus.SetStreamDelegate(handler)
	store := al.GetSessionStore()
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	conn := makeTestConn()
	conn.sendCh = make(chan []byte, 256)
	bindTestConnToSession(handler, "ceiling-notice-chat", meta.ID, conn)

	answer, err := al.ProcessScheduled(context.Background(), "mia", meta.ID, "Read two notes sequentially.", "webchat", "ceiling-notice-chat")
	require.NoError(t, err)
	assert.Equal(t, notice, answer, "preserve the existing caller-visible stop reason")
	require.Equal(t, 2, provider.calls, "the real engine reaches the per-agent ceiling")
	require.Equal(t, 2, tool.calls, "both real dispatches complete once")
	// Exercise the same fallback the channel manager consumes after the turn.
	require.NoError(t, handler.webchatCh.Send(context.Background(), bus.OutboundMessage{
		Channel: "webchat", ChatID: "ceiling-notice-chat", SessionID: meta.ID, Content: answer,
	}))
	live, _ := wu22GatewayFinalFrames(t, &wu22GatewayFinalizeHarness{handler: handler, store: store, sid: meta.ID, conn: conn})
	var liveNotices []generated.ReplayMessageFrame
	for _, raw := range framesOfType(t, live, "replay_message") {
		var frame generated.ReplayMessageFrame
		require.NoError(t, json.Unmarshal(raw, &frame))
		if frame.Content == notice {
			liveNotices = append(liveNotices, frame)
			assert.Equal(t, "system", frame.Role, "the live UI must use its existing system-message renderer")
			assert.Nil(t, frame.Model, "no model attribution on harness prose")
		}
	}
	assert.Len(t, liveNotices, 1, "one live system notice, not just a correct stored row")
	for _, raw := range framesOfType(t, live, "token") {
		var frame generated.TokenFrame
		require.NoError(t, json.Unmarshal(raw, &frame))
		assert.NotEqual(t, notice, frame.Content, "Send must not turn the notice back into model-output tokens")
	}
	entries, err := store.ReadTranscript(meta.ID)
	require.NoError(t, err)
	var savedNotices []session.TranscriptEntry
	var narration []string
	for _, entry := range entries {
		if entry.Content == notice {
			savedNotices = append(savedNotices, entry)
			assert.Equal(t, session.EntryTypeSystem, entry.Type)
			assert.Equal(t, "system", entry.Role)
			assert.Empty(t, entry.Model)
		} else if entry.Role == "assistant" && entry.Content != "" {
			narration = append(narration, entry.Content)
		}
	}
	assert.Equal(t, []string{"Reading note 1.", "Reading note 2."}, narration, "real narration stays ordered and is not replaced by the harness notice")
	require.Len(t, savedNotices, 1, "stream finalization must not append a second assistant copy")
	if len(liveNotices) == 1 {
		require.NotNil(t, liveNotices[0].Id)
		assert.Equal(t, savedNotices[0].ID, *liveNotices[0].Id, "live and reload use the same durable notice identity")
	}
	done := wu22GatewayDoneFrames(t, live)
	require.NotEmpty(t, done)
	require.NotNil(t, done[0].Stats)
	require.NotNil(t, done[0].Stats.TurnFailed)
	assert.True(t, *done[0].Stats.TurnFailed, "the cutoff still counts as a failed turn, not a successful answer")
}

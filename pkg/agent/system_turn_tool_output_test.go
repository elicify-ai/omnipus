package agent

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	systemTurnProbeToolName      = "system_turn_output_probe"
	asyncSystemTurnProbeToolName = "async_system_turn_output_probe"
	systemTurnRawMarker          = "RAW-TOOL-OUTPUT-MUST-NOT-REACH-CHAT"
	asyncSystemTurnRawMarker     = "ASYNC-RAW-TOOL-OUTPUT-MUST-NOT-REACH-CHAT"
	systemTurnNarration          = "Your plan was stopped."
)

type systemTurnOutputProbe struct {
	tools.BaseTool
}

func (*systemTurnOutputProbe) Name() string { return systemTurnProbeToolName }

func (*systemTurnOutputProbe) Description() string {
	return "test-only tool that returns distinct model and user-facing output"
}

func (*systemTurnOutputProbe) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

func (*systemTurnOutputProbe) Scope() tools.ToolScope { return tools.ScopeGeneral }

func (*systemTurnOutputProbe) Execute(context.Context, map[string]any) *tools.ToolResult {
	return &tools.ToolResult{
		ForLLM:  "probe completed",
		ForUser: systemTurnRawMarker,
	}
}

type asyncSystemTurnOutputProbe struct {
	tools.BaseTool
}

func (*asyncSystemTurnOutputProbe) Name() string { return asyncSystemTurnProbeToolName }

func (*asyncSystemTurnOutputProbe) Description() string {
	return "test-only async tool that reports user-facing output through its callback"
}

func (*asyncSystemTurnOutputProbe) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

func (*asyncSystemTurnOutputProbe) Scope() tools.ToolScope { return tools.ScopeGeneral }

func (*asyncSystemTurnOutputProbe) Execute(context.Context, map[string]any) *tools.ToolResult {
	return tools.ErrorResult("async probe must execute through ExecuteAsync")
}

func (*asyncSystemTurnOutputProbe) ExecuteAsync(
	ctx context.Context,
	_ map[string]any,
	callback tools.AsyncCallback,
) *tools.ToolResult {
	callback(ctx, &tools.ToolResult{ForUser: asyncSystemTurnRawMarker})
	return &tools.ToolResult{ForLLM: "async probe started", Async: true}
}

func TestProcessSystemMessage_SuppressesToolOutputButDeliversNarration(t *testing.T) {
	provider := testutil.NewScenario().
		WithToolCall(systemTurnProbeToolName, `{}`).
		WithText(systemTurnNarration)
	al, msgBus, delegate, _ := newAsyncResultTestLoop(t, provider)
	al.RegisterTool(&systemTurnOutputProbe{})
	setAskPolicyForAllAgents(t, al, systemTurnProbeToolName, config.ToolPolicyAllow)

	msg := drainNotify(t, al, msgBus, AsyncNotifyEvent{
		Channel:    "webchat",
		ChatID:     "chat-leak-regression",
		AgentID:    delegate.ID,
		SourceKind: "plan_stopped_by_user",
		Content:    "The plan was stopped by the user.",
	})

	response, err := al.processSystemMessage(context.Background(), msg)
	require.NoError(t, err)
	assert.Equal(t, systemTurnNarration, response,
		"the system-woken turn must retain its own final narration")

	outbound := drainOutbound(msgBus)
	contents := make([]string, 0, len(outbound))
	for _, message := range outbound {
		contents = append(contents, message.Content)
	}
	assert.Equal(t, []string{systemTurnNarration}, contents,
		"system-woken turns must publish final narration without publishing raw tool output")
}

func TestProcessSystemMessage_SuppressesAsyncToolOutputButDeliversNarration(t *testing.T) {
	provider := testutil.NewScenario().
		WithToolCall(asyncSystemTurnProbeToolName, `{}`).
		WithText(systemTurnNarration)
	al, msgBus, delegate, _ := newAsyncResultTestLoop(t, provider)
	al.RegisterTool(&asyncSystemTurnOutputProbe{})
	setAskPolicyForAllAgents(t, al, asyncSystemTurnProbeToolName, config.ToolPolicyAllow)

	msg := drainNotify(t, al, msgBus, AsyncNotifyEvent{
		Channel:    "webchat",
		ChatID:     "async-chat-leak-regression",
		AgentID:    delegate.ID,
		SourceKind: "plan_stopped_by_user",
		Content:    "The plan was stopped by the user.",
	})

	response, err := al.processSystemMessage(context.Background(), msg)
	require.NoError(t, err)
	assert.Equal(t, systemTurnNarration, response,
		"the system-woken turn must retain its own final narration")

	outbound := drainOutbound(msgBus)
	contents := make([]string, 0, len(outbound))
	for _, message := range outbound {
		contents = append(contents, message.Content)
	}
	assert.Equal(t, []string{systemTurnNarration}, contents,
		"system-woken turns must publish final narration without publishing async tool output")
}

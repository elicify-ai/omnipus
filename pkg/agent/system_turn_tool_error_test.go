package agent

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	systemTurnErrorProbeToolName      = "system_turn_error_probe"
	asyncSystemTurnErrorProbeToolName = "async_system_turn_error_probe"
	systemTurnErrorText               = "bash requires audit logging; aborting"
)

type systemTurnErrorProbe struct {
	tools.BaseTool
}

func (*systemTurnErrorProbe) Name() string { return systemTurnErrorProbeToolName }

func (*systemTurnErrorProbe) Description() string {
	return "test-only tool that returns a user-facing failure"
}

func (*systemTurnErrorProbe) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

func (*systemTurnErrorProbe) Scope() tools.ToolScope { return tools.ScopeGeneral }

func (*systemTurnErrorProbe) Execute(context.Context, map[string]any) *tools.ToolResult {
	return &tools.ToolResult{
		ForLLM:  "shell audit logging is unavailable",
		ForUser: systemTurnErrorText,
		IsError: true,
	}
}

type asyncSystemTurnErrorProbe struct {
	tools.BaseTool
}

type abortAfterAsyncToolHook struct{}

func (*abortAfterAsyncToolHook) BeforeTool(
	_ context.Context,
	call *ToolCallHookRequest,
) (*ToolCallHookRequest, HookDecision, error) {
	return call, HookDecision{Action: HookActionContinue}, nil
}

func (*abortAfterAsyncToolHook) AfterTool(
	_ context.Context,
	result *ToolResultHookResponse,
) (*ToolResultHookResponse, HookDecision, error) {
	return result, HookDecision{Action: HookActionAbortTurn, Reason: "stop after async dispatch"}, nil
}

func (*asyncSystemTurnErrorProbe) Name() string { return asyncSystemTurnErrorProbeToolName }

func (*asyncSystemTurnErrorProbe) Description() string {
	return "test-only async tool that reports a user-facing failure"
}

func (*asyncSystemTurnErrorProbe) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

func (*asyncSystemTurnErrorProbe) Scope() tools.ToolScope { return tools.ScopeGeneral }

func (*asyncSystemTurnErrorProbe) Execute(context.Context, map[string]any) *tools.ToolResult {
	return tools.ErrorResult("async probe must execute through ExecuteAsync")
}

func (*asyncSystemTurnErrorProbe) ExecuteAsync(
	ctx context.Context,
	_ map[string]any,
	callback tools.AsyncCallback,
) *tools.ToolResult {
	callback(ctx, &tools.ToolResult{
		ForLLM:  "shell command timed out",
		ForUser: systemTurnErrorText,
		IsError: true,
	})
	return &tools.ToolResult{ForLLM: "async probe started", Async: true}
}

func TestProcessSystemMessage_ExternalChannelPublishesAttributedToolError(t *testing.T) {
	provider := testutil.NewScenario().
		WithToolCall(systemTurnErrorProbeToolName, `{}`).
		WithText(systemTurnNarration)
	al, msgBus, delegate, _ := newAsyncResultTestLoop(t, provider)
	al.RegisterTool(&systemTurnErrorProbe{})
	setAskPolicyForAllAgents(t, al, systemTurnErrorProbeToolName, config.ToolPolicyAllow)

	msg := drainNotify(t, al, msgBus, AsyncNotifyEvent{
		Channel:    "telegram.main",
		ChatID:     "external-tool-error",
		AgentID:    delegate.ID,
		SourceKind: "plan_stopped_by_user",
		Content:    "The plan was stopped by the user.",
	})

	response, err := al.processSystemMessage(context.Background(), msg)
	require.NoError(t, err)
	assert.Equal(t, systemTurnNarration, response)

	outbound := drainOutbound(msgBus)
	require.Len(t, outbound, 2,
		"the external user must receive an attributed failure notice and the turn narration")
	assert.Equal(t, "telegram.main", outbound[0].Channel)
	assert.Equal(t, "external-tool-error", outbound[0].ChatID)
	assert.Equal(t, "Tool `system_turn_error_probe` failed:\n"+systemTurnErrorText, outbound[0].Content)
	assert.Equal(t, systemTurnNarration, outbound[1].Content)
}

func TestProcessSystemMessage_ExternalChannelPublishesAttributedAsyncToolError(t *testing.T) {
	provider := testutil.NewScenario().
		WithToolCall(asyncSystemTurnErrorProbeToolName, `{}`).
		WithText(systemTurnNarration)
	al, msgBus, delegate, _ := newAsyncResultTestLoop(t, provider)
	al.RegisterTool(&asyncSystemTurnErrorProbe{})
	setAskPolicyForAllAgents(t, al, asyncSystemTurnErrorProbeToolName, config.ToolPolicyAllow)

	msg := drainNotify(t, al, msgBus, AsyncNotifyEvent{
		Channel:    "telegram.main",
		ChatID:     "external-async-tool-error",
		AgentID:    delegate.ID,
		SourceKind: "plan_stopped_by_user",
		Content:    "The plan was stopped by the user.",
	})

	response, err := al.processSystemMessage(context.Background(), msg)
	require.NoError(t, err)
	assert.Equal(t, systemTurnNarration, response)

	outbound := drainOutbound(msgBus)
	require.Len(t, outbound, 2,
		"the external user must receive an attributed async failure notice and the turn narration")
	assert.Equal(t, "telegram.main", outbound[0].Channel)
	assert.Equal(t, "external-async-tool-error", outbound[0].ChatID)
	assert.Equal(t, "Tool `async_system_turn_error_probe` failed:\n"+systemTurnErrorText, outbound[0].Content)
	assert.Equal(t, systemTurnNarration, outbound[1].Content)
}

func TestProcessSystemMessage_ExternalChannelSuppressesSuccessfulToolOutput(t *testing.T) {
	provider := testutil.NewScenario().
		WithToolCall(systemTurnProbeToolName, `{}`).
		WithText(systemTurnNarration)
	al, msgBus, delegate, _ := newAsyncResultTestLoop(t, provider)
	al.RegisterTool(&systemTurnOutputProbe{})
	setAskPolicyForAllAgents(t, al, systemTurnProbeToolName, config.ToolPolicyAllow)

	msg := drainNotify(t, al, msgBus, AsyncNotifyEvent{
		Channel:    "telegram.main",
		ChatID:     "external-success",
		AgentID:    delegate.ID,
		SourceKind: "plan_stopped_by_user",
		Content:    "The plan was stopped by the user.",
	})

	response, err := al.processSystemMessage(context.Background(), msg)
	require.NoError(t, err)
	assert.Equal(t, systemTurnNarration, response)

	outbound := drainOutbound(msgBus)
	require.Len(t, outbound, 1, "successful raw tool output must remain suppressed externally")
	assert.Equal(t, systemTurnNarration, outbound[0].Content)
}

func TestProcessSystemMessage_ExternalChannelSuppressesSuccessfulAsyncToolOutput(t *testing.T) {
	provider := testutil.NewScenario().
		WithToolCall(asyncSystemTurnProbeToolName, `{}`).
		WithText(systemTurnNarration)
	al, msgBus, delegate, _ := newAsyncResultTestLoop(t, provider)
	al.RegisterTool(&asyncSystemTurnOutputProbe{})
	setAskPolicyForAllAgents(t, al, asyncSystemTurnProbeToolName, config.ToolPolicyAllow)

	msg := drainNotify(t, al, msgBus, AsyncNotifyEvent{
		Channel:    "telegram.main",
		ChatID:     "external-async-success",
		AgentID:    delegate.ID,
		SourceKind: "plan_stopped_by_user",
		Content:    "The plan was stopped by the user.",
	})

	response, err := al.processSystemMessage(context.Background(), msg)
	require.NoError(t, err)
	assert.Equal(t, systemTurnNarration, response)

	outbound := drainOutbound(msgBus)
	require.Len(t, outbound, 1, "successful async raw tool output must remain suppressed externally")
	assert.Equal(t, systemTurnNarration, outbound[0].Content)
}

func TestProcessSystemMessage_WebchatKeepsToolErrorStructured(t *testing.T) {
	provider := testutil.NewScenario().
		WithToolCall(systemTurnErrorProbeToolName, `{}`).
		WithText(systemTurnNarration)
	al, msgBus, delegate, _ := newAsyncResultTestLoop(t, provider)
	al.RegisterTool(&systemTurnErrorProbe{})
	setAskPolicyForAllAgents(t, al, systemTurnErrorProbeToolName, config.ToolPolicyAllow)
	sub := al.SubscribeEvents(32)
	t.Cleanup(func() { al.UnsubscribeEvents(sub.ID) })

	msg := drainNotify(t, al, msgBus, AsyncNotifyEvent{
		Channel:    "webchat",
		ChatID:     "structured-tool-error",
		AgentID:    delegate.ID,
		SourceKind: "plan_stopped_by_user",
		Content:    "The plan was stopped by the user.",
	})

	response, err := al.processSystemMessage(context.Background(), msg)
	require.NoError(t, err)
	assert.Equal(t, systemTurnNarration, response)

	outbound := drainOutbound(msgBus)
	require.Len(t, outbound, 1, "webchat must not receive a duplicate plain-text tool error")
	assert.Equal(t, systemTurnNarration, outbound[0].Content)

	var toolEnd *ToolExecEndPayload
	for _, event := range drainEvents(sub.C) {
		payload, ok := event.Payload.(ToolExecEndPayload)
		if ok && payload.Tool == systemTurnErrorProbeToolName {
			toolEnd = &payload
			break
		}
	}
	require.NotNil(t, toolEnd, "the webchat forwarder needs a ToolExecEnd event for its structured frame")
	assert.True(t, toolEnd.IsError)
	assert.Equal(t, "shell audit logging is unavailable", toolEnd.Result)
}

func TestProcessSystemMessage_WebchatPublishesAttributedAsyncToolError(t *testing.T) {
	provider := testutil.NewScenario().
		WithToolCall(asyncSystemTurnErrorProbeToolName, `{}`).
		WithText(systemTurnNarration)
	al, msgBus, delegate, _ := newAsyncResultTestLoop(t, provider)
	al.RegisterTool(&asyncSystemTurnErrorProbe{})
	setAskPolicyForAllAgents(t, al, asyncSystemTurnErrorProbeToolName, config.ToolPolicyAllow)
	sub := al.SubscribeEvents(32)
	t.Cleanup(func() { al.UnsubscribeEvents(sub.ID) })
	store := al.GetSessionStore()
	require.NotNil(t, store)
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", delegate.ID)
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.DeleteSession(meta.ID) })

	msg := drainNotify(t, al, msgBus, AsyncNotifyEvent{
		Channel:             "webchat",
		ChatID:              "webchat-async-tool-error",
		AgentID:             delegate.ID,
		TranscriptSessionID: meta.ID,
		SourceKind:          "plan_stopped_by_user",
		Content:             "The plan was stopped by the user.",
	})

	response, err := al.processSystemMessage(context.Background(), msg)
	require.NoError(t, err)
	assert.Equal(t, systemTurnNarration, response)

	outbound := drainOutbound(msgBus)
	require.Len(t, outbound, 1,
		"the structured tool event must not race the final assistant response through token/done transport")
	assert.Equal(t, systemTurnNarration, outbound[0].Content)

	events := drainEvents(sub.C)
	var toolEnds []ToolExecEndPayload
	callbackErrorEventIndex := -1
	turnEndEventIndex := -1
	for i, event := range events {
		payload, ok := event.Payload.(ToolExecEndPayload)
		if event.Kind == EventKindToolExecEnd && ok && payload.Tool == asyncSystemTurnErrorProbeToolName {
			toolEnds = append(toolEnds, payload)
			if payload.IsError {
				callbackErrorEventIndex = i
			}
		}
		if event.Kind == EventKindTurnEnd {
			turnEndEventIndex = i
		}
	}
	require.Len(t, toolEnds, 2, "the async start acknowledgement and callback must each emit one result")
	assert.False(t, toolEnds[0].IsError)
	assert.True(t, toolEnds[0].Async)
	liveError := toolEnds[len(toolEnds)-1]
	assert.True(t, liveError.IsError,
		"a callback that finishes immediately must not be overwritten by the async-start acknowledgement")
	assert.Equal(t, "Tool `async_system_turn_error_probe` failed:\n"+systemTurnErrorText, liveError.Result)
	assert.True(t, liveError.Async)
	assert.NotEqual(t, toolEnds[0].ToolCallID, liveError.ToolCallID,
		"a delayed callback must use a server-unique notice ID so reused provider call IDs cannot corrupt another turn")
	assert.Equal(t, meta.ID, liveError.SessionID,
		"the tool error frame must reach a reattached viewer through the originating transcript session")
	require.NotEqual(t, -1, turnEndEventIndex)
	assert.Less(t, callbackErrorEventIndex, turnEndEventIndex,
		"an immediate callback error must reach the UI before the turn's done frame bakes the tool call")

	entries, err := store.ReadTranscript(meta.ID)
	require.NoError(t, err)
	var durableNotice bool
	for _, entry := range entries {
		if entry.Type == session.EntryTypeSystem && entry.Status == "error" &&
			entry.Content == "Tool `async_system_turn_error_probe` failed:\n"+systemTurnErrorText {
			durableNotice = true
			break
		}
	}
	assert.True(t, durableNotice,
		"the attributed failure must replay after an offline callback, not depend on a live webchat viewer")
}

func TestProcessSystemMessage_ImmediateAsyncErrorSurvivesPostDispatchAbort(t *testing.T) {
	provider := testutil.NewScenario().WithToolCall(asyncSystemTurnErrorProbeToolName, `{}`)
	al, msgBus, delegate, _ := newAsyncResultTestLoop(t, provider)
	al.RegisterTool(&asyncSystemTurnErrorProbe{})
	setAskPolicyForAllAgents(t, al, asyncSystemTurnErrorProbeToolName, config.ToolPolicyAllow)
	require.NoError(t, al.MountHook(NamedHook("abort-after-async-tool", &abortAfterAsyncToolHook{})))
	sub := al.SubscribeEvents(32)
	t.Cleanup(func() { al.UnsubscribeEvents(sub.ID) })
	store := al.GetSessionStore()
	require.NotNil(t, store)
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", delegate.ID)
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.DeleteSession(meta.ID) })

	msg := drainNotify(t, al, msgBus, AsyncNotifyEvent{
		Channel:             "webchat",
		ChatID:              "webchat-aborted-async-tool-error",
		AgentID:             delegate.ID,
		TranscriptSessionID: meta.ID,
		SourceKind:          "plan_stopped_by_user",
		Content:             "The plan was stopped by the user.",
	})

	_, err = al.processSystemMessage(context.Background(), msg)
	require.Error(t, err)

	var callbackErrorFound bool
	for _, event := range drainEvents(sub.C) {
		payload, ok := event.Payload.(ToolExecEndPayload)
		if event.Kind == EventKindToolExecEnd && ok && payload.Tool == asyncSystemTurnErrorProbeToolName &&
			payload.IsError && payload.Result == "Tool `async_system_turn_error_probe` failed:\n"+systemTurnErrorText {
			callbackErrorFound = true
			break
		}
	}
	assert.True(t, callbackErrorFound,
		"a post-dispatch abort must release an already-completed async callback instead of stranding it")
}

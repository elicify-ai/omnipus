// Omnipus - Ultra-lightweight personal AI agent
// License: MIT

package bedrock

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers/protocoltypes"
)

func TestNewProvider_APIKeyOnlyAndRegionalEndpoint(t *testing.T) {
	_, err := NewProvider("")
	require.ErrorContains(t, err, "api key is required")

	p, err := NewProvider("test-key", WithRegion("ap-southeast-3"))
	require.NoError(t, err)
	assert.Equal(t, "ap-southeast-3", p.Region())
	assert.Equal(t, "https://bedrock-runtime.ap-southeast-3.amazonaws.com", p.Endpoint())
}

func TestConvertMessages_SystemPrompts(t *testing.T) {
	messages, system := convertMessages([]Message{
		{Role: "system", Content: "You are helpful."},
		{Role: "user", Content: "Hello"},
	})
	require.Len(t, system, 1)
	assert.Equal(t, "You are helpful.", system[0].Text)
	require.Len(t, messages, 1)
	assert.Equal(t, "user", messages[0].Role)
}

func TestConvertMessages_UserMessage(t *testing.T) {
	messages, system := convertMessages([]Message{{Role: "user", Content: "What is 2+2?"}})
	assert.Empty(t, system)
	require.Len(t, messages, 1)
	require.Len(t, messages[0].Content, 1)
	assert.Equal(t, "What is 2+2?", *messages[0].Content[0].Text)
}

func TestConvertMessages_AssistantMessage(t *testing.T) {
	messages, _ := convertMessages([]Message{{Role: "assistant", Content: "Four."}})
	require.Len(t, messages, 1)
	assert.Equal(t, "assistant", messages[0].Role)
	assert.Equal(t, "Four.", *messages[0].Content[0].Text)
}

func TestConvertMessages_ToolResult(t *testing.T) {
	messages, _ := convertMessages([]Message{{Role: "tool", Content: "result", ToolCallID: "call_123"}})
	result := messages[0].Content[0].ToolResult
	require.NotNil(t, result)
	assert.Equal(t, "call_123", result.ToolUseID)
	assert.Equal(t, "result", *result.Content[0].Text)
}

func TestConvertMessages_MultipleToolResultsMerged(t *testing.T) {
	messages, _ := convertMessages([]Message{
		{Role: "user", Content: "weather"},
		{Role: "assistant", ToolCalls: []ToolCall{
			{ID: "call_nyc", Name: "weather", Arguments: map[string]any{"city": "NYC"}},
			{ID: "call_la", Name: "weather", Arguments: map[string]any{"city": "LA"}},
		}},
		{Role: "tool", Content: "sunny", ToolCallID: "call_nyc"},
		{Role: "tool", Content: "clear", ToolCallID: "call_la"},
	})
	require.Len(t, messages, 3)
	assert.Equal(t, "user", messages[2].Role)
	require.Len(t, messages[2].Content, 2)
	assert.Equal(t, "call_nyc", messages[2].Content[0].ToolResult.ToolUseID)
	assert.Equal(t, "call_la", messages[2].Content[1].ToolResult.ToolUseID)
}

func TestConvertMessages_AssistantWithToolCalls(t *testing.T) {
	messages, _ := convertMessages([]Message{{
		Role: "assistant", Content: "Calculating.",
		ToolCalls: []ToolCall{{ID: "call_456", Name: "calculator", Arguments: map[string]any{"expression": "2+2"}}},
	}})
	require.Len(t, messages[0].Content, 2)
	assert.Equal(t, "Calculating.", *messages[0].Content[0].Text)
	assert.Equal(t, "calculator", messages[0].Content[1].ToolUse.Name)
	assert.Equal(t, "call_456", messages[0].Content[1].ToolUse.ToolUseID)
}

func TestConvertTools_Basic(t *testing.T) {
	toolConfig := convertTools([]ToolDefinition{{Function: ToolFunctionDefinition{
		Name: "get_weather", Description: "Get weather",
		Parameters: map[string]any{"type": "object", "properties": map[string]any{"city": map[string]any{"type": "string"}}},
	}}})
	require.Len(t, toolConfig.Tools, 1)
	assert.Equal(t, "get_weather", toolConfig.Tools[0].ToolSpec.Name)
	assert.Equal(t, "Get weather", toolConfig.Tools[0].ToolSpec.Description)
	assert.Equal(t, "object", toolConfig.Tools[0].ToolSpec.InputSchema.JSON["type"])
}

func TestConvertTools_SkipsEmptyName(t *testing.T) {
	toolConfig := convertTools([]ToolDefinition{
		{Function: ToolFunctionDefinition{Name: ""}},
		{Function: ToolFunctionDefinition{Name: "   "}},
		{Function: ToolFunctionDefinition{Name: "valid"}},
	})
	require.Len(t, toolConfig.Tools, 1)
	assert.Equal(t, "valid", toolConfig.Tools[0].ToolSpec.Name)
}

func TestConvertTools_NilParameters(t *testing.T) {
	toolConfig := convertTools([]ToolDefinition{{Function: ToolFunctionDefinition{Name: "simple"}}})
	require.Len(t, toolConfig.Tools, 1)
	assert.Equal(t, "object", toolConfig.Tools[0].ToolSpec.InputSchema.JSON["type"])
}

func TestBuildUserContent_TextOnly(t *testing.T) {
	content := buildUserContent(Message{Content: "Hello"})
	require.Len(t, content, 1)
	assert.Equal(t, "Hello", *content[0].Text)
}

func TestBuildUserContent_WithImage(t *testing.T) {
	const encoded = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADUlEQVR4nGNgYAAAAAMAASsJTYQAAAAASUVORK5CYII="
	content := buildUserContent(Message{Content: "Look", Media: []string{"data:image/png;base64," + encoded}})
	require.Len(t, content, 2)
	require.NotNil(t, content[1].Image)
	assert.Equal(t, "png", content[1].Image.Format)
	want, err := base64.StdEncoding.DecodeString(encoded)
	require.NoError(t, err)
	assert.Equal(t, want, content[1].Image.Source.Bytes)
}

func TestBuildUserContent_SkipsInvalidBase64(t *testing.T) {
	content := buildUserContent(Message{Content: "Invalid", Media: []string{"data:image/png;base64,not-valid!!!"}})
	require.Len(t, content, 1)
	assert.Nil(t, content[0].Image)
}

func TestBuildUserContent_SkipsNonBase64Data(t *testing.T) {
	content := buildUserContent(Message{Content: "Invalid", Media: []string{"data:image/png,raw"}})
	require.Len(t, content, 1)
	assert.Nil(t, content[0].Image)
}

func TestBuildAssistantContent_SkipsEmptyToolName(t *testing.T) {
	content := buildAssistantContent(Message{Content: "response", ToolCalls: []ToolCall{
		{ID: "1", Name: ""}, {ID: "2", Name: "   "}, {ID: "3", Name: "valid"},
	}})
	require.Len(t, content, 2)
	assert.Equal(t, "valid", content[1].ToolUse.Name)
}

func TestBuildAssistantContent_NilArguments(t *testing.T) {
	content := buildAssistantContent(Message{ToolCalls: []ToolCall{{ID: "1", Name: "tool"}}})
	require.Len(t, content, 1)
	assert.Empty(t, content[0].ToolUse.Input)
}

func TestBuildAssistantContent_FunctionFallback(t *testing.T) {
	content := buildAssistantContent(Message{ToolCalls: []ToolCall{{
		ID: "1", Function: &FunctionCall{Name: "fallback", Arguments: `{"key":"value"}`},
	}}})
	require.Len(t, content, 1)
	assert.Equal(t, "fallback", content[0].ToolUse.Name)
	assert.Equal(t, "value", content[0].ToolUse.Input["key"])
}

func TestParseResponse_TextOnly(t *testing.T) {
	text := "Hello!"
	response, err := parseResponse(&converseResponse{
		Output:     converseOutput{Message: bedrockResponseMessage{Content: []responseContentBlock{{Text: &text}}}},
		StopReason: "end_turn", Usage: &tokenUsage{InputTokens: 10, OutputTokens: 5},
	})
	require.NoError(t, err)
	assert.Equal(t, "Hello!", response.Content)
	assert.Equal(t, "stop", response.FinishReason)
	assert.Equal(t, 15, response.Usage.TotalTokens)
}

func TestParseResponse_StopReasons(t *testing.T) {
	tests := map[string]string{
		"end_turn": "stop", "tool_use": "tool_calls", "max_tokens": "length",
		"stop_sequence": "stop", "content_filtered": "content_filter", "guardrail_intervened": "content_filter",
	}
	for input, want := range tests {
		t.Run(input, func(t *testing.T) {
			response, err := parseResponse(&converseResponse{StopReason: input})
			require.NoError(t, err)
			assert.Equal(t, want, response.FinishReason)
		})
	}
}

func parseCannedResponse(t *testing.T, raw string) *converseResponse {
	t.Helper()
	var output converseResponse
	require.NoError(t, json.Unmarshal([]byte(raw), &output))
	return &output
}

func TestParseResponse_WithToolCalls(t *testing.T) {
	output := parseCannedResponse(t, `{
		"output":{"message":{"role":"assistant","content":[
			{"text":"Checking."},
			{"toolUse":{"toolUseId":"call_1","name":"weather","input":{"city":"Jakarta"}}}
		]}},"stopReason":"tool_use","usage":{"inputTokens":20,"outputTokens":15,"totalTokens":35}}`)
	response, err := parseResponse(output)
	require.NoError(t, err)
	assert.Equal(t, "Checking.", response.Content)
	require.Len(t, response.ToolCalls, 1)
	assert.Equal(t, map[string]any{"city": "Jakarta"}, response.ToolCalls[0].Arguments)
	assert.JSONEq(t, `{"city":"Jakarta"}`, response.ToolCalls[0].Function.Arguments)
	assert.Equal(t, 35, response.Usage.TotalTokens)
}

func TestParseResponse_MultipleToolCalls(t *testing.T) {
	output := parseCannedResponse(t, `{
		"output":{"message":{"content":[
			{"toolUse":{"toolUseId":"call_1","name":"a","input":{"arg":"one"}}},
			{"toolUse":{"toolUseId":"call_2","name":"b","input":{"arg":"two"}}}
		]}},"stopReason":"tool_use"}`)
	response, err := parseResponse(output)
	require.NoError(t, err)
	require.Len(t, response.ToolCalls, 2)
	assert.Equal(t, "call_1", response.ToolCalls[0].ID)
	assert.Equal(t, "call_2", response.ToolCalls[1].ID)
}

func TestParseResponse_ToolCallNumbersReachToolAsNumbers(t *testing.T) {
	output := parseCannedResponse(t, `{
		"output":{"message":{"content":[{"toolUse":{"toolUseId":"call_num","name":"resize","input":{
			"count":3,"ratio":1.5,"big":12345678901234567890,"list":[1,2],"nested":{"depth":42},
			"label":"unchanged","active":true,"missing":null
		}}}]}},"stopReason":"tool_use"}`)
	response, err := parseResponse(output)
	require.NoError(t, err)
	args := response.ToolCalls[0].Arguments
	assert.Equal(t, json.Number("3"), args["count"])
	assert.Equal(t, json.Number("12345678901234567890"), args["big"])
	assert.JSONEq(t,
		`{"active":true,"big":12345678901234567890,"count":3,"label":"unchanged","list":[1,2],"missing":null,"nested":{"depth":42},"ratio":1.5}`,
		response.ToolCalls[0].Function.Arguments)
}

func TestParseResponse_ToolCallWithNilInput(t *testing.T) {
	response, err := parseResponse(&converseResponse{
		Output: converseOutput{Message: bedrockResponseMessage{Content: []responseContentBlock{{
			ToolUse: &responseToolUseBlock{ToolUseID: "call_nil", Name: "no_args"},
		}}}}, StopReason: "tool_use",
	})
	require.NoError(t, err)
	require.Len(t, response.ToolCalls, 1)
	assert.NotNil(t, response.ToolCalls[0].Arguments)
	assert.Empty(t, response.ToolCalls[0].Arguments)
}

func TestChat_RequestResponseAndBearerAuthentication(t *testing.T) {
	const imageData = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="
	var gotPath, gotAuthorization string
	var gotRequest converseRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		gotAuthorization = r.Header.Get("Authorization")
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotRequest))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"output":{"message":{"role":"assistant","content":[
				{"text":"Calling tool."},
				{"toolUse":{"toolUseId":"call_7","name":"weather","input":{"city":"Jakarta"}}}
			]}},"stopReason":"tool_use","usage":{"inputTokens":11,"outputTokens":7,"totalTokens":18}}`))
	}))
	t.Cleanup(server.Close)

	p, err := NewProvider("fake-bedrock-key", WithBaseEndpoint(server.URL))
	require.NoError(t, err)
	response, err := p.Chat(context.Background(), []Message{
		{Role: "system", Content: "Be concise."},
		{Role: "user", Content: "Weather?", Media: []string{"data:image/png;base64," + imageData}},
	}, []ToolDefinition{{Function: protocoltypes.ToolFunctionDefinition{
		Name: "weather", Description: "Get weather", Parameters: map[string]any{"type": "object"},
	}}}, "anthropic.claude-opus-4-6-v1", map[string]any{"max_tokens": 32, "temperature": 0.25})
	require.NoError(t, err)

	assert.Equal(t, "/model/anthropic.claude-opus-4-6-v1/converse", gotPath)
	assert.Equal(t, "Bearer fake-bedrock-key", gotAuthorization)
	require.Len(t, gotRequest.System, 1)
	assert.Equal(t, "Be concise.", gotRequest.System[0].Text)
	require.Len(t, gotRequest.Messages[0].Content, 2)
	assert.Equal(t, "png", gotRequest.Messages[0].Content[1].Image.Format)
	require.NotNil(t, gotRequest.InferenceConfig)
	assert.Equal(t, int32(32), *gotRequest.InferenceConfig.MaxTokens)
	require.NotNil(t, gotRequest.ToolConfig)
	assert.Equal(t, "weather", gotRequest.ToolConfig.Tools[0].ToolSpec.Name)

	assert.Equal(t, "Calling tool.", response.Content)
	assert.Equal(t, "tool_calls", response.FinishReason)
	assert.Equal(t, 18, response.Usage.TotalTokens)
	require.Len(t, response.ToolCalls, 1)
	assert.Equal(t, "Jakarta", response.ToolCalls[0].Arguments["city"])
}

func TestChat_ErrorMapping(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Amzn-Errortype", "ThrottlingException:http://internal")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"message":"rate limited"}`))
	}))
	t.Cleanup(server.Close)
	p, err := NewProvider("fake-key", WithBaseEndpoint(server.URL))
	require.NoError(t, err)
	_, err = p.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, "model", nil)
	require.Error(t, err)
	var httpErr *HTTPError
	require.True(t, errors.As(err, &httpErr))
	assert.Equal(t, http.StatusTooManyRequests, httpErr.StatusCode)
	assert.Equal(t, "ThrottlingException", httpErr.Code)
	assert.Equal(t, "rate limited", httpErr.Message)
}

//go:build goolm && stdjson

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers"
	anthropicmessages "github.com/elicify-ai/omnipus/pkg/providers/anthropic_messages"
	"github.com/elicify-ai/omnipus/pkg/providers/azure"
	"github.com/elicify-ai/omnipus/pkg/providers/bedrock"
)

// Scenario 9; MAJ-CW-005: read actual final HTTP bodies, not a successful
// sanitizer return or a synthetic DTO. Exact unit-3 notice copy is deferred;
// original instructions and every retained call/result MUST already survive.
func TestCWSlideR1_AllAdapterRequestsPreserveRetainedGroups(t *testing.T) {
	for _, family := range []string{"openai", "anthropic", "bedrock", "responses"} {
		t.Run(family, func(t *testing.T) {
			h := cwR1New(t, 32_768)
			ts := h.turn("original adapter anchor: exact user instructions")
			h.append(t, providers.Message{Role: "user", Content: ts.userMessage})
			oldSource := strings.Repeat("readable archived source ", 1_000) // 24k chars per result: share > W/2, total still < B.
			h.append(t, cwR1Step("wire-old-a", "old assistant a", oldSource)...)
			h.append(t, cwR1Step("wire-old-b", "old assistant b", oldSource)...)
			newCalls := []providers.ToolCall{cwR1Call("wire-new-a"), cwR1Call("wire-new-b")}
			h.append(t, providers.Message{Role: "assistant", Content: "newest exact narration", ToolCalls: newCalls},
				providers.Message{Role: "tool", ToolCallID: "wire-new-a", Content: "newest exact first result"},
				providers.Message{Role: "tool", ToolCallID: "wire-new-b", Content: "newest exact second result"})
			messages := h.al.assembleMessages(context.Background(), ts, h.agent.Sessions.GetHistory(h.key), "", nil, nil)
			const instructions = "R1 original pinned instructions. Never replace this precedence or text with a request-only notice."
			messages[0] = providers.Message{Role: "system", Content: instructions} // Known immutable request input; not a production prompt hook.
			require.Greater(t, toolResultShareTokens(messages), 16_384, "instrument: default relative share fires")
			require.Less(t, requestTokens(messages, nil), agentContextBudget(h.agent), "instrument: total alone must not cause this slide")
			out := h.check(t, ts, messages)
			r, p := cwR1Adapter(t, family)
			cwR1Send(t, cwR1Flow(h, ts, out, p))
			requests := r.requests(t)
			require.Len(t, requests, 1, "the actual adapter reached the recording HTTP endpoint")
			wire := cwR1DecodeWire(t, family, requests[0])
			require.Contains(t, wire.instructions, instructions, "Responses and other adapters cannot replace original pinned instructions")
			require.Equal(t, []string{ts.userMessage}, wire.users, "one original user anchor precedes retained history")
			wantIDs := []string{"wire-old-b", "wire-new-a", "wire-new-b"}
			var gotIDs []string
			for _, call := range wire.calls {
				gotIDs = append(gotIDs, call.ID)
				require.Equal(t, "r1_tool", call.Function.Name, "exact retained call name")
				require.JSONEq(t, `{"purpose":"checkpoint"}`, call.Function.Arguments, "retained call arguments are not lost/replaced")
			}
			require.Equal(t, wantIDs, gotIDs, "final serialization omits ONE oldest step and preserves remaining complete call order")
			require.Equal(t, []providers.Message{
				{Role: "tool", ToolCallID: "wire-old-b", Content: oldSource},
				{Role: "tool", ToolCallID: "wire-new-a", Content: "newest exact first result"},
				{Role: "tool", ToolCallID: "wire-new-b", Content: "newest exact second result"},
			}, wire.results, "all matching result roles/correlations/text/order survive, not silently repaired away")
			require.Greater(t, cwR1Skip(t, h), 0, "final body is bound to an actual persisted slide")
			require.Len(t, h.archive(t), 8, "adapter preparation cannot append a duplicate user anchor")
		})
	}
}

func TestCWSlideR1_PartialNewestGroupCannotDisappearIntoSuccessfulSend(t *testing.T) {
	h := cwR1New(t, 80_000)
	ts := h.turn("partial group must be loud")
	partial := []providers.Message{
		{Role: "system", Content: "original partial-group instructions"},
		{Role: "user", Content: ts.userMessage},
		{Role: "assistant", ToolCalls: []providers.ToolCall{cwR1Call("partial-a"), cwR1Call("partial-b")}},
		{Role: "tool", ToolCallID: "partial-a", Content: "first available result"},
	}
	h.append(t, partial[1:]...)
	r, p := cwR1OpenAI(t, 0)
	rr := cwR1Flow(h, ts, partial, p)
	rr.rq.prepareCallMessages()
	flow := rr.rq.prepareLLMRequest()
	var sendErr error
	if flow == agentLoopRunTurnRequestNext {
		_, sendErr = rr.rq.ri.rf.rt.callProvider(rr.rq.ri.rf.callMessages, nil)
		t.Logf("partial-group provider-boundary result: %v", sendErr)
	}
	// An incomplete LIVE step must be retained pending its result or rejected
	// visibly. A serializer/sanitizer dropping it and sending is not a cut.
	require.Len(t, r.requests(t), 0, "incomplete newest live group cannot be silently erased or sent missing one declared result")
	if flow == agentLoopRunTurnRequestNext {
		require.Error(t, sendErr, "a rejected provider-boundary attempt must return a visible error, not swallow it")
	} else {
		require.Equal(t, partial, rr.rq.ri.messages, "a pending incomplete group must remain intact in the live candidate")
	}
	require.Equal(t, partial[2].ToolCalls, h.archive(t)[1].ToolCalls, "both original declarations remain archived; no repair-as-eviction")
}

// Internal assertion projection, NOT a gateway/SPA wire-format type.
// not-wire-format
type cwR1WireView struct {
	instructions string
	users        []string
	calls        []providers.ToolCall
	results      []providers.Message
}

func cwR1Adapter(t *testing.T, family string) (*cwR1Recorder, providers.LLMProvider) {
	t.Helper()
	response := cwR1OpenAIReply
	switch family {
	case "anthropic":
		response = `{"id":"r1","type":"message","role":"assistant","content":[{"type":"text","text":"r1-success"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`
	case "bedrock":
		response = `{"output":{"message":{"role":"assistant","content":[{"text":"r1-success"}]}},"stopReason":"end_turn","usage":{"inputTokens":1,"outputTokens":1,"totalTokens":2}}`
	case "responses":
		response = `{"id":"r1","object":"response","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"r1-success"}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`
	}
	r := cwR1Server(t, 0, response)
	var p providers.LLMProvider
	var err error
	switch family {
	case "openai":
		p, err = providers.NewHTTPProviderWithTimeouts("r1-inert-fixture-key", r.server.URL, "", "max_tokens", 10, 0, nil)
	case "anthropic":
		p = anthropicmessages.NewProvider("r1-inert-fixture-key", r.server.URL)
	case "bedrock":
		p, err = bedrock.NewProvider("r1-inert-fixture-key", bedrock.WithBaseEndpoint(r.server.URL))
	case "responses":
		p, err = azure.NewProvider("r1-inert-fixture-key", r.server.URL, "")
	default:
		t.Fatalf("unsupported fixture family %q", family)
	}
	require.NoError(t, err, "real adapter constructs with an inert local network fixture")
	return r, p
}

func cwR1DecodeWire(t *testing.T, family string, body map[string]json.RawMessage) cwR1WireView {
	t.Helper()
	view := cwR1WireView{}
	if family == "openai" {
		messages := cwR1Messages(t, body)
		cwR1AssertComplete(t, messages)
		for _, m := range messages {
			switch m.Role {
			case "system":
				view.instructions += m.Content
			case "user":
				view.users = append(view.users, m.Content)
			case "assistant":
				view.calls = append(view.calls, m.ToolCalls...)
			case "tool":
				view.results = append(view.results, m)
			}
		}
		return view
	}
	var items []map[string]any
	if family == "responses" {
		require.NoError(t, json.Unmarshal(body["instructions"], &view.instructions))
		require.NoError(t, json.Unmarshal(body["input"], &items))
		for _, item := range items {
			switch item["type"] {
			case "function_call":
				view.calls = append(view.calls, providers.ToolCall{ID: cwR1String(t, item, "call_id"), Function: &providers.FunctionCall{
					Name: cwR1String(t, item, "name"), Arguments: cwR1String(t, item, "arguments")}})
			case "function_call_output":
				view.results = append(view.results, providers.Message{Role: "tool", ToolCallID: cwR1String(t, item, "call_id"), Content: cwR1String(t, item, "output")})
			default:
				if item["role"] == "user" {
					view.users = append(view.users, cwR1Text(t, item["content"]))
				}
			}
		}
		return view
	}
	var system any
	require.NoError(t, json.Unmarshal(body["system"], &system))
	// Anthropic permits a string system prompt; Bedrock uses text blocks.
	// Both native forms carry the SAME independently supplied instructions.
	view.instructions = cwR1Text(t, system)
	require.NoError(t, json.Unmarshal(body["messages"], &items))
	for _, item := range items {
		if family == "anthropic" {
			if text, ok := item["content"].(string); ok {
				if item["role"] == "user" {
					view.users = append(view.users, text)
				}
				continue // Native plain-text messages coexist with tool content blocks.
			}
		}
		blocks, ok := item["content"].([]any)
		require.True(t, ok, "native tool messages contain an explicit ordered content array")
		for _, raw := range blocks {
			block, ok := raw.(map[string]any)
			require.True(t, ok)
			if family == "anthropic" {
				switch block["type"] {
				case "tool_use":
					view.calls = append(view.calls, cwR1NativeCall(t, block, "id", "input"))
				case "tool_result":
					view.results = append(view.results, providers.Message{Role: "tool", ToolCallID: cwR1String(t, block, "tool_use_id"), Content: cwR1Text(t, block["content"])})
				case "text":
					if item["role"] == "user" {
						view.users = append(view.users, cwR1String(t, block, "text"))
					}
				}
			} else if use, ok := block["toolUse"].(map[string]any); ok {
				view.calls = append(view.calls, cwR1NativeCall(t, use, "toolUseId", "input"))
			} else if result, ok := block["toolResult"].(map[string]any); ok {
				view.results = append(view.results, providers.Message{Role: "tool", ToolCallID: cwR1String(t, result, "toolUseId"), Content: cwR1Text(t, result["content"])})
			} else if item["role"] == "user" {
				view.users = append(view.users, cwR1String(t, block, "text"))
			}
		}
	}
	return view
}

func cwR1NativeCall(t *testing.T, block map[string]any, idKey, inputKey string) providers.ToolCall {
	t.Helper()
	args, err := json.Marshal(block[inputKey])
	require.NoError(t, err)
	return providers.ToolCall{ID: cwR1String(t, block, idKey), Function: &providers.FunctionCall{
		Name: cwR1String(t, block, "name"), Arguments: string(args)}}
}

func cwR1String(t *testing.T, object map[string]any, key string) string {
	t.Helper()
	value, ok := object[key].(string)
	require.True(t, ok, "final serialized field %s must be a string, got %T", key, object[key])
	return value
}

func cwR1Text(t *testing.T, value any) string {
	t.Helper()
	if text, ok := value.(string); ok {
		return text
	}
	blocks, ok := value.([]any)
	require.True(t, ok, "final text is a string or a native text-block array, got %T", value)
	var text strings.Builder
	for _, raw := range blocks {
		block, ok := raw.(map[string]any)
		require.True(t, ok, "native content must be an object, got %s", fmt.Sprint(raw))
		text.WriteString(cwR1String(t, block, "text"))
	}
	return text.String()
}

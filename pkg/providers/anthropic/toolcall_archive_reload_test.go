package anthropicprovider

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
)

// N1 RED probe (architect finding, question source
// coordination/squads/session-core-build-20261008/ARCHITECT-ANSWER-CONV-PROVENANCE.md, note N1).
//
// Oracle: a tool call reloaded from a .context archive line must survive the
// request build as a tool_use block carrying its real name and its complete
// argument object, and its tool result must keep a matching tool_use — no
// orphan result. The expected request shape is derived from the Anthropic
// Messages API contract (tool_use = {type,id,name,input}; tool_result carries
// tool_use_id) and from the sibling regression
// pkg/providers/anthropic_messages/reconstructed_toolcall_test.go (#1081),
// which pins the SAME archived-call shape for the anthropic_messages, bedrock
// and responses adapters.
//
// The reloaded shape is the real one: protocoltypes.ToolCall tags Name and
// Arguments json:"-", so a decoded archive line keeps only Function.Name and
// Function.Arguments. archivedReloadedMessages reconstructs it exactly as
// pkg/memory/jsonl.go::readMessages decodes it (json.Unmarshal of the archive
// line into an embedded providers.Message) and asserts the precondition.
//
// Note: the SDK `anthropic` provider is the ONE adapter that has no
// Function.Name fallback in its request builder (buildParams skips a call whose
// flat Name is empty). The sibling adapters named above already fall back and
// are covered by #1081.

func TestBuildParams_ArchivedReloadedToolCallKeepsToolUseBlock(t *testing.T) {
	messages := archivedReloadedMessages(t)

	params, err := buildParams(messages, nil, "claude-sonnet-4.6", map[string]any{"max_tokens": 1024})
	if err != nil {
		t.Fatalf("buildParams() error: %v", err)
	}

	uses := collectToolUses(params)
	if len(uses) != 1 {
		t.Fatalf("tool_use blocks in request = %d, want 1: the archived call was dropped, "+
			"leaving a tool_result without its tool_use", len(uses))
	}
	if uses[0].ID != "call-archived" || uses[0].Name != "some_tool" {
		t.Errorf("tool_use id/name = %q/%q, want %q/%q", uses[0].ID, uses[0].Name, "call-archived", "some_tool")
	}
	wantInput := map[string]any{"query": "recovered-query", "limit": float64(2), "enabled": true}
	if !reflect.DeepEqual(uses[0].Input, wantInput) {
		t.Errorf("tool_use input = %#v, want %#v (exact archived argument JSON)", uses[0].Input, wantInput)
	}

	results := collectToolResults(params)
	if len(results) != 1 || results[0] != "call-archived" {
		t.Fatalf("tool_result tool_use_ids = %v, want exactly [call-archived] matching the tool_use", results)
	}
}

// TestBuildParams_LiveToolCallKeepsToolUseBlock is the positive control: a call
// whose flat top-level Name IS set must produce a tool_use block. It proves the
// assertion above can pass — the instrument is not blind to a correctly built
// request — and isolates the failure to the empty-flat-Name case.
func TestBuildParams_LiveToolCallKeepsToolUseBlock(t *testing.T) {
	messages := []Message{{
		Role: "assistant", Content: "Calling tool.",
		ToolCalls: []ToolCall{{
			ID: "call-live", Name: "live_tool", Arguments: map[string]any{"query": "live-query"},
		}},
	}}

	params, err := buildParams(messages, nil, "claude-sonnet-4.6", map[string]any{"max_tokens": 1024})
	if err != nil {
		t.Fatalf("buildParams() error: %v", err)
	}

	uses := collectToolUses(params)
	if len(uses) != 1 {
		t.Fatalf("tool_use blocks in request = %d, want 1 (control: flat Name set)", len(uses))
	}
	if uses[0].ID != "call-live" || uses[0].Name != "live_tool" {
		t.Errorf("tool_use id/name = %q/%q, want %q/%q", uses[0].ID, uses[0].Name, "call-live", "live_tool")
	}
	if !reflect.DeepEqual(uses[0].Input, map[string]any{"query": "live-query"}) {
		t.Errorf("tool_use input = %#v, want the live argument map", uses[0].Input)
	}
}

// archivedReloadedMessages reconstructs the message slice a gateway restart
// hands to the provider: the canonical .context archive-line shape, decoded
// exactly as pkg/memory/jsonl.go::readMessages decodes it. The fixture mirrors
// pkg/providers/anthropic_messages/reconstructed_toolcall_test.go.
func archivedReloadedMessages(t *testing.T) []Message {
	t.Helper()
	const archiveJSON = `[
		{"role":"user","content":"Use some_tool."},
		{"role":"assistant","content":"Calling tool.","tool_calls":[
			{"id":"call-archived","type":"function","function":{
				"name":"some_tool",
				"arguments":"{\"query\":\"recovered-query\",\"limit\":2,\"enabled\":true}"
			}}
		]},
		{"role":"tool","tool_call_id":"call-archived","content":"tool-result"}
	]`
	var messages []Message
	if err := json.Unmarshal([]byte(archiveJSON), &messages); err != nil {
		t.Fatalf("decode archive fixture: %v", err)
	}
	if len(messages) != 3 || len(messages[1].ToolCalls) != 1 {
		t.Fatalf("decoded fixture shape = %#v, want 3 messages with one tool call", messages)
	}
	// Precondition: the reload dropped the flattened fields — the call now
	// carries only Function.Name/Function.Arguments. If this fails the fixture
	// no longer represents a reloaded archive line.
	call := messages[1].ToolCalls[0]
	if call.Name != "" || call.Arguments != nil {
		t.Fatalf("fixture precondition: reloaded call Name=%q Arguments=%v, want empty/nil", call.Name, call.Arguments)
	}
	if call.Function == nil || call.Function.Name != "some_tool" {
		t.Fatalf("fixture precondition: Function = %+v, want name some_tool", call.Function)
	}
	return messages
}

func collectToolUses(params anthropic.MessageNewParams) []*anthropic.ToolUseBlockParam {
	var out []*anthropic.ToolUseBlockParam
	for _, m := range params.Messages {
		for _, b := range m.Content {
			if b.OfToolUse != nil {
				out = append(out, b.OfToolUse)
			}
		}
	}
	return out
}

func collectToolResults(params anthropic.MessageNewParams) []string {
	var out []string
	for _, m := range params.Messages {
		for _, b := range m.Content {
			if b.OfToolResult != nil {
				out = append(out, b.OfToolResult.ToolUseID)
			}
		}
	}
	return out
}

package memory

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
)

// N1 RED probe (architect finding, question source
// coordination/squads/session-core-build-20261008/ARCHITECT-ANSWER-CONV-PROVENANCE.md, note N1).
//
// Premise proof, real path. Writes a session through the REAL store with the
// exact assistant message the live turn producer builds
// (pkg/agent/loop_run_turn_response.go::recordToolCalls), then reloads it through
// the REAL reload path a gateway restart uses
// (JSONLStore.GetHistory -> readMessages -> WindowHistory) and pins the decoded
// shape: the flattened convenience fields are gone.
//
// protocoltypes.ToolCall tags Name, Arguments and ThoughtSignature json:"-" —
// they are never serialized, so a reloaded call carries ONLY
// Function.Name/Function.Arguments. This is the shape the provider request
// builders receive after a restart.
//
// Oracle: a reloaded call's top-level Name must be empty and Arguments nil,
// while Function.Name/Function.Arguments survive byte-exact. Derived from the
// struct tags in pkg/providers/protocoltypes/types.go::ToolCall and from the
// writer in pkg/memory/jsonl.go::addMsgLocked — not from observed output.
func TestReload_DropsFlattenedToolCallName(t *testing.T) {
	const (
		sessionKey = "sess-n1-reload"
		toolName   = "get_weather"
		toolArgs   = `{"city":"SF"}`
		toolCallID = "call_1"
		toolResult = `{"temp":72}`
	)

	store, err := NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}
	ctx := context.Background()

	// The exact object recordToolCalls persists: Name == Function.Name,
	// Function.Arguments the marshalled argument string, top-level
	// Arguments nil, ThoughtSignature duplicated.
	assistant := providers.Message{
		Role: "assistant",
		ToolCalls: []providers.ToolCall{{
			ID:       toolCallID,
			Type:     "function",
			Name:     toolName,
			Function: &providers.FunctionCall{Name: toolName, Arguments: toolArgs},
		}},
	}
	err = store.AddFullMessage(ctx, sessionKey, assistant)
	if err != nil {
		t.Fatalf("AddFullMessage(assistant): %v", err)
	}
	err = store.AddFullMessage(ctx, sessionKey, providers.Message{
		Role: "tool", ToolCallID: toolCallID, Content: toolResult,
	})
	if err != nil {
		t.Fatalf("AddFullMessage(tool result): %v", err)
	}

	history, err := store.GetHistory(ctx, sessionKey)
	if err != nil {
		t.Fatalf("GetHistory (reload): %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("reloaded messages = %d, want 2", len(history))
	}

	var reloaded *providers.ToolCall
	for i := range history {
		if len(history[i].ToolCalls) > 0 {
			reloaded = &history[i].ToolCalls[0]
			break
		}
	}
	if reloaded == nil {
		t.Fatal("no reloaded tool call found in history")
	}

	if reloaded.Name != "" {
		t.Errorf("reloaded ToolCall.Name = %q, want empty (json:\"-\" drops it)", reloaded.Name)
	}
	if reloaded.Arguments != nil {
		t.Errorf("reloaded ToolCall.Arguments = %v, want nil (json:\"-\")", reloaded.Arguments)
	}
	if reloaded.Function == nil {
		t.Fatal("reloaded ToolCall.Function = nil, want the serialized function block")
	}
	if reloaded.Function.Name != toolName {
		t.Errorf("reloaded Function.Name = %q, want %q", reloaded.Function.Name, toolName)
	}
	if reloaded.Function.Arguments != toolArgs {
		t.Errorf("reloaded Function.Arguments = %q, want %q (byte-exact)", reloaded.Function.Arguments, toolArgs)
	}
}

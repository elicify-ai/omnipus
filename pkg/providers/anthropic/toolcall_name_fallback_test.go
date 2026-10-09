package anthropicprovider

import (
	"reflect"
	"strings"
	"testing"
)

// Reviewer fix-round tests (code-reviewer / silent-failure-hunter /
// pr-test-analyzer, N1): they pin the error branch and the fallback
// precedence commit cc363fe0f added to buildParams.
//
// Oracle: the Anthropic Messages API contract — a tool_use block is
// {type,id,name,input} and every tool_result needs a matching tool_use —
// plus the sibling fallback rule the fix follows (#1081: fall back to
// Function.Name; a flat top-level field wins when set; an undecodable
// stored argument payload is non-fatal and ships an empty input block).
// The "no request" assertion uses the builder seam: alongside its error,
// buildParams returns a params value carrying no messages, so nothing can
// be sent.

func TestBuildParams_NamelessToolCallFailsVisibly(t *testing.T) {
	cases := []struct {
		name string
		call ToolCall
	}{
		{"no name at all", ToolCall{ID: "call-no-function"}},
		{"function name empty too", ToolCall{
			ID:       "call-empty-function-name",
			Function: &FunctionCall{Name: "", Arguments: "{}"},
		}},
		{"whitespace-only name", ToolCall{
			ID:       "call-blank-name",
			Name:     "   ",
			Function: &FunctionCall{Name: "  ", Arguments: "{}"},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// The trailing tool_result documents the stakes: dropping the
			// tool_use would orphan it, which the API rejects with a 400.
			messages := []Message{
				{Role: "assistant", ToolCalls: []ToolCall{tc.call}},
				{Role: "user", ToolCallID: tc.call.ID, Content: "tool-result"},
			}

			params, err := buildParams(messages, nil, "claude-sonnet-4.6", map[string]any{"max_tokens": 1024})

			if err == nil {
				t.Fatalf("buildParams() error = nil, want a visible failure for a call with no name")
			}
			if !strings.Contains(err.Error(), tc.call.ID) {
				t.Errorf("error %q does not name the tool call id %q", err.Error(), tc.call.ID)
			}
			if len(params.Messages) != 0 {
				t.Errorf("params carries %d messages on the error path, want 0 (nothing may be sent)", len(params.Messages))
			}
		})
	}
}

func TestBuildParams_FlatNameWinsOverFunctionName(t *testing.T) {
	messages := []Message{{Role: "assistant", ToolCalls: []ToolCall{{
		ID:       "call-both-names",
		Name:     "a",
		Function: &FunctionCall{Name: "b", Arguments: "{}"},
	}}}}

	params, err := buildParams(messages, nil, "claude-sonnet-4.6", map[string]any{"max_tokens": 1024})
	if err != nil {
		t.Fatalf("buildParams() error: %v", err)
	}

	uses := collectToolUses(params)
	if len(uses) != 1 {
		t.Fatalf("tool_use blocks = %d, want 1", len(uses))
	}
	if uses[0].Name != "a" {
		t.Errorf("tool_use name = %q, want %q (flat top-level Name must win over Function.Name %q)",
			uses[0].Name, "a", "b")
	}
}

func TestBuildParams_ArgumentPrecedenceAndFallback(t *testing.T) {
	t.Run("flat Arguments win over Function.Arguments", func(t *testing.T) {
		messages := []Message{{Role: "assistant", ToolCalls: []ToolCall{{
			ID:        "call-args-both",
			Name:      "some_tool",
			Arguments: map[string]any{"flat": true},
			Function:  &FunctionCall{Name: "some_tool", Arguments: `{"nested":true}`},
		}}}}

		params, err := buildParams(messages, nil, "claude-sonnet-4.6", map[string]any{"max_tokens": 1024})
		if err != nil {
			t.Fatalf("buildParams() error: %v", err)
		}

		uses := collectToolUses(params)
		if len(uses) != 1 {
			t.Fatalf("tool_use blocks = %d, want 1", len(uses))
		}
		if !reflect.DeepEqual(uses[0].Input, map[string]any{"flat": true}) {
			t.Errorf("tool_use input = %#v, want the flat Arguments map (Function.Arguments must lose)", uses[0].Input)
		}
	})

	t.Run("malformed Function.Arguments still emits the block with empty input", func(t *testing.T) {
		messages := []Message{{Role: "assistant", ToolCalls: []ToolCall{{
			ID:       "call-malformed-args",
			Name:     "some_tool",
			Function: &FunctionCall{Name: "some_tool", Arguments: `{invalid`},
		}}}}

		params, err := buildParams(messages, nil, "claude-sonnet-4.6", map[string]any{"max_tokens": 1024})
		if err != nil {
			t.Fatalf("buildParams() error = %v, want nil (an undecodable stored payload is non-fatal by design)", err)
		}

		uses := collectToolUses(params)
		if len(uses) != 1 {
			t.Fatalf("tool_use blocks = %d, want 1 (the block must survive malformed args, not be dropped)", len(uses))
		}
		if !reflect.DeepEqual(uses[0].Input, map[string]any{}) {
			t.Errorf("tool_use input = %#v, want an empty map (the documented empty-arguments block)", uses[0].Input)
		}
	})
}

// Sprint H agent payload tests — FR-H-002, FR-H-003
// Traces to: sprint-h-subagent-block-spec.md TDD row 4.

package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// TestToolExecStartPayload_CarriesParentSpawnCallID verifies FR-H-002:
// ToolExecStartPayload and ToolExecEndPayload carry ParentSpawnCallID.
// Traces to: sprint-h-subagent-block-spec.md TDD row 4.
func TestToolExecStartPayload_CarriesParentSpawnCallID(t *testing.T) {
	t.Run("ToolExecStartPayload has ParentSpawnCallID field", func(t *testing.T) {
		p := ToolExecStartPayload{
			ToolCallID:        session.ToolCallID("t1"),
			ChatID:            "chat-abc",
			Tool:              "fs.list",
			Arguments:         map[string]any{"path": "/tmp"},
			ParentSpawnCallID: session.ToolCallID("c1"),
		}
		assert.Equal(t, session.ToolCallID("t1"), p.ToolCallID,
			"ToolExecStartPayload.ToolCallID must be settable and readable")
		assert.Equal(t, "chat-abc", p.ChatID,
			"ToolExecStartPayload.ChatID must be settable and readable")
		assert.Equal(t, "fs.list", p.Tool,
			"ToolExecStartPayload.Tool must be settable and readable")
		assert.Equal(t, map[string]any{"path": "/tmp"}, p.Arguments,
			"ToolExecStartPayload.Arguments must be settable and readable")
		assert.Equal(t, session.ToolCallID("c1"), p.ParentSpawnCallID,
			"ToolExecStartPayload.ParentSpawnCallID must be settable and readable")
		assert.Empty(t, ToolExecStartPayload{}.ParentSpawnCallID,
			"ParentSpawnCallID must default to empty string for top-level tool calls")
	})

	t.Run("ToolExecEndPayload has ParentSpawnCallID field", func(t *testing.T) {
		p := ToolExecEndPayload{
			ToolCallID:        session.ToolCallID("t1"),
			ChatID:            "chat-abc",
			Tool:              "fs.list",
			IsError:           false,
			ParentSpawnCallID: session.ToolCallID("c1"),
		}
		assert.Equal(t, session.ToolCallID("t1"), p.ToolCallID,
			"ToolExecEndPayload.ToolCallID must be settable and readable")
		assert.Equal(t, "chat-abc", p.ChatID,
			"ToolExecEndPayload.ChatID must be settable and readable")
		assert.Equal(t, "fs.list", p.Tool,
			"ToolExecEndPayload.Tool must be settable and readable")
		assert.False(t, p.IsError,
			"ToolExecEndPayload.IsError must be settable and readable")
		assert.Equal(t, session.ToolCallID("c1"), p.ParentSpawnCallID,
			"ToolExecEndPayload.ParentSpawnCallID must be settable and readable")
		assert.Empty(t, ToolExecEndPayload{}.ParentSpawnCallID,
			"ParentSpawnCallID must default to empty string for top-level tool calls")
	})
}

// TestSpawnToolCallIDContext verifies the context helpers that thread the spawn tool
// call ID down to spawnSubTurn (FR-H-003).
func TestSpawnToolCallIDContext(t *testing.T) {
	t.Run("withSpawnToolCallID injects and spawnToolCallIDFromContext retrieves", func(t *testing.T) {
		ctx := context.Background()
		assert.Empty(t, spawnToolCallIDFromContext(ctx),
			"empty context must return empty string")

		ctx2 := withSpawnToolCallID(ctx, "spawn-call-123")
		assert.Equal(t, "spawn-call-123", spawnToolCallIDFromContext(ctx2),
			"injected ID must be retrievable from context")

		// Original context must be unaffected.
		assert.Empty(t, spawnToolCallIDFromContext(ctx),
			"original context must not be mutated by withSpawnToolCallID")
	})

	t.Run("nested override — innermost wins", func(t *testing.T) {
		ctx := withSpawnToolCallID(context.Background(), "outer")
		ctx2 := withSpawnToolCallID(ctx, "inner")
		assert.Equal(t, "inner", spawnToolCallIDFromContext(ctx2))
		assert.Equal(t, "outer", spawnToolCallIDFromContext(ctx))
	})
}

// TestSubTurnSpawnPayload_HasSpanFields verifies FR-H-004:
// SubTurnSpawnPayload and SubTurnEndPayload carry the required span fields.
func TestSubTurnSpawnPayload_HasSpanFields(t *testing.T) {
	t.Run("SubTurnSpawnPayload span fields", func(t *testing.T) {
		p := SubTurnSpawnPayload{
			AgentID:           "max",
			Label:             "subturn-1",
			ParentTurnID:      "turn-abc",
			SpanID:            "span_c1",
			ParentSpawnCallID: session.ToolCallID("c1"),
			TaskLabel:         "audit go files",
			ChatID:            "chat-xyz",
		}
		assert.Equal(t, "max", p.AgentID)
		assert.Equal(t, "subturn-1", p.Label)
		assert.Equal(t, "turn-abc", p.ParentTurnID)
		assert.Equal(t, "span_c1", p.SpanID)
		assert.Equal(t, session.ToolCallID("c1"), p.ParentSpawnCallID)
		assert.Equal(t, "audit go files", p.TaskLabel)
		assert.Equal(t, "chat-xyz", p.ChatID)
	})

	t.Run("SubTurnEndPayload span fields", func(t *testing.T) {
		p := SubTurnEndPayload{
			AgentID:           "max",
			Status:            SubTurnStatusSuccess,
			SpanID:            "span_c1",
			ParentSpawnCallID: session.ToolCallID("c1"),
			DurationMS:        4210,
			ChatID:            "chat-xyz",
		}
		assert.Equal(t, "max", p.AgentID)
		assert.Equal(t, SubTurnStatusSuccess, p.Status)
		assert.Equal(t, "span_c1", p.SpanID)
		assert.Equal(t, session.ToolCallID("c1"), p.ParentSpawnCallID)
		assert.Equal(t, int64(4210), p.DurationMS)
		assert.Equal(t, "chat-xyz", p.ChatID)
	})
}

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import "context"

// recallConversationMeta is a METADATA-ONLY mirror of the recall_conversation
// tool. Its executable implementation lives in pkg/agent
// (agent.RecallConversationTool) because it needs the per-turn session /
// transcript context the agent loop supplies and pkg/tools does not have.
//
// pkg/tools cannot import pkg/agent (import cycle), so without this stub the
// general-builtin catalog (GeneralBuiltinMetadata) and the Constraint #6
// coverage universe (pkg/gateway buildKnownBuiltinToolNames) would be blind to
// recall_conversation — which is exactly why it shipped with NO seeded
// tool-policy and was therefore denied-by-default (fail-closed) on every
// install, even though every agent registers and can call it. This stub
// carries ONLY Name/Description/Parameters/Category/Scope for the catalog and
// the coverage universe; it is never Execute()d (the real per-agent
// registration handles execution).
//
// Source of truth: pkg/agent/recall_conversation.go — keep Name / Description /
// Parameters in sync with RecallConversationTool there.
type recallConversationMeta struct{ BaseTool }

func (recallConversationMeta) Name() string           { return "recall_conversation" }
func (recallConversationMeta) Scope() ToolScope       { return ScopeGeneral }
func (recallConversationMeta) Category() ToolCategory { return CategoryMemory }

func (recallConversationMeta) Description() string {
	return "Look back at an earlier part of the CURRENT conversation that has scrolled out of view. " +
		"Use this when the user refers to something discussed earlier that you can no longer see " +
		"above. Find it by keyword (query), by turn numbers (turn_range, e.g. \"5-10\"), or by time. " +
		"The matching earlier exchanges are brought back so you can read and reference them. " +
		"To retrieve one capped or emptied TOOL RESULT verbatim, pass the tool_call_id a " +
		"[capped]/[emptied] mark cites; large results come back one page at a time (offset/length). " +
		"Provide exactly one of query, turn_range, time, or tool_call_id. " +
		"Note: to find facts saved across DIFFERENT conversations, use recall_memory instead."
}

func (recallConversationMeta) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type": "string",
				"description": "Keywords to search for among the earlier turns. The most relevant " +
					"earlier exchanges are brought back (up to 8).",
			},
			"turn_range": map[string]any{
				"type":        "string",
				"description": "A range of turn numbers to bring back, e.g. \"5-10\" (inclusive).",
			},
			"time": map[string]any{
				"type": "object",
				"description": "Bring back turns whose time falls between 'from' and 'to'. " +
					"Times are Unix seconds or RFC3339 (e.g. 2026-07-01T12:00:00Z).",
				"properties": map[string]any{
					"from": map[string]any{
						"description": "Start of the time window (Unix seconds or RFC3339). 0 = start of the conversation.",
					},
					"to": map[string]any{
						"description": "End of the time window (Unix seconds or RFC3339). 0 = now.",
					},
				},
			},
			"tool_call_id": map[string]any{
				"type": "string",
				"description": "Bring back one archived tool result by the tool_call_id a " +
					"[capped]/[emptied] mark cites. Returns one page of the result; the page " +
					"framing states the total size and the next offset when more remains.",
			},
			"archive_line": map[string]any{
				"type": "integer",
				"description": "Only with tool_call_id: the zero-based archive line the mark cites, " +
					"to disambiguate duplicate ids. When omitted, the most recent line wins.",
			},
			"offset": map[string]any{
				"type": "integer",
				"description": "Only with tool_call_id: page start in characters into the full " +
					"result (default 0). Use the next offset the previous page's framing stated.",
			},
			"length": map[string]any{
				"type": "integer",
				"description": "Only with tool_call_id: page size in characters (min 1); values " +
					"above the page maximum are clamped to it.",
			},
		},
	}
}

// Execute is never called — this is a catalog/coverage metadata stub. The real
// recall_conversation is registered per-agent from pkg/agent.
func (recallConversationMeta) Execute(context.Context, map[string]any) *ToolResult {
	return ErrorResult("recall_conversation: metadata-only catalog stub — not executable " +
		"(the real tool is registered per-agent in pkg/agent)")
}

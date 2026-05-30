// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dapicom-ai/omnipus/pkg/logger"
	"github.com/dapicom-ai/omnipus/pkg/providers"
	"github.com/dapicom-ai/omnipus/pkg/session"
	"github.com/dapicom-ai/omnipus/pkg/tools"
)

// repairHistory reconstructs missing tool_use / tool_result pairs in the
// outgoing message list using the session transcript as the source of truth.
//
// The provider-facing jsonl can drift from the transcript when OpenRouter
// rotates backing providers mid-stream: an assistant message gets persisted
// with only some of the tool_uses the model actually emitted, while the tools
// still execute and their results land in the context. On the next LLM call
// Anthropic rejects the conversation with HTTP 400
//
//	"Each tool_result block must have a corresponding tool_use block in the previous message."
//
// Instead of dropping the orphan (which silently loses data), we look the id
// up in the transcript — which always has the tool name + arguments — and
// synthesize the missing side of the pair. For missing tool_results the
// transcript only carries metadata, not the actual result text; we re-invoke
// idempotent tools to regenerate the real result, and emit a short error
// stub for anything non-idempotent to unstick the conversation.
//
// The repair is ephemeral. We do not rewrite the context jsonl on disk — the
// next turn will re-reconcile from the same transcript, and a deeper fix to
// the streaming path that stops the drift at source will make this a no-op.
func repairHistory(
	ctx context.Context,
	messages []providers.Message,
	transcriptStore *session.UnifiedStore,
	transcriptSessionID string,
	toolRegistry *tools.ToolRegistry,
	agentID string,
	toolPolicy ...*tools.ToolPolicyCfg,
) ([]providers.Message, repairStats) {
	// Variadic toolPolicy is optional for backward compatibility with tests that
	// pass nil registries. Callers that want H3 enforcement pass it explicitly.
	var policy *tools.ToolPolicyCfg
	if len(toolPolicy) > 0 {
		policy = toolPolicy[0]
	}
	stats := repairStats{}
	if len(messages) == 0 {
		return messages, stats
	}

	declaredIDs, resolvedIDs := collectToolIDs(messages)

	orphanResults := map[string]bool{}
	for id := range resolvedIDs {
		if !declaredIDs[id] {
			orphanResults[id] = true
		}
	}
	orphanUses := map[string]bool{}
	for id := range declaredIDs {
		if !resolvedIDs[id] {
			orphanUses[id] = true
		}
	}

	if len(orphanResults) == 0 && len(orphanUses) == 0 {
		return messages, stats
	}

	transcriptIndex := loadTranscriptToolCalls(transcriptStore, transcriptSessionID)
	if transcriptIndex == nil {
		// Transcript unreachable — repair can't reconstruct. Fall through and
		// let the wire-level sanitizer drop orphans as a last resort.
		logger.WarnCF("agent.repair", "Transcript unavailable; orphans will be dropped at the wire",
			map[string]any{"session_id": transcriptSessionID, "agent_id": agentID})
		return messages, stats
	}

	out := messages
	if len(orphanResults) > 0 {
		out = injectSyntheticToolUses(out, orphanResults, transcriptIndex, &stats)
	}
	if len(orphanUses) > 0 {
		out = injectSyntheticToolResults(ctx, out, orphanUses, transcriptIndex, toolRegistry, agentID, policy, &stats)
	}

	if stats.anyRepaired() {
		logger.InfoCF("agent.repair", "Reconciled tool_use / tool_result pairs from transcript",
			map[string]any{
				"agent_id":              agentID,
				"session_id":            transcriptSessionID,
				"synthetic_tool_uses":   stats.SyntheticToolUses,
				"reinvoked_idempotent":  stats.ReinvokedIdempotent,
				"synthetic_error_stubs": stats.SyntheticErrorStubs,
				"unrepairable":          stats.Unrepairable,
			})
	}
	return out, stats
}

// collectToolIDs walks the message list and builds two sets:
//   - declared: every tool_use id declared by any assistant message
//   - resolved: every tool_call_id referenced by any tool message
func collectToolIDs(messages []providers.Message) (declared, resolved map[string]bool) {
	declared = map[string]bool{}
	resolved = map[string]bool{}
	for _, m := range messages {
		switch m.Role {
		case "assistant":
			for _, tc := range m.ToolCalls {
				if tc.ID != "" {
					declared[tc.ID] = true
				}
			}
		case "tool":
			if m.ToolCallID != "" {
				resolved[m.ToolCallID] = true
			}
		}
	}
	return declared, resolved
}

// injectSyntheticToolUses patches assistant messages with synthesized tool_use
// blocks for every orphan tool_result found between this assistant and the
// next one. Anthropic's invariant is "each tool_result's tool_use must be in
// the preceding assistant", so that's exactly the scope we patch. The
// original assistant message is rebuilt immutably — callers keep the
// input slice unmodified.
func injectSyntheticToolUses(
	messages []providers.Message,
	orphanResults map[string]bool,
	transcriptIndex map[string]*session.ToolCall,
	stats *repairStats,
) []providers.Message {
	out := make([]providers.Message, len(messages))
	copy(out, messages)
	for i := range out {
		if out[i].Role != "assistant" {
			continue
		}
		added := syntheticToolUsesForAssistant(out, i, orphanResults, transcriptIndex, stats)
		if len(added) == 0 {
			continue
		}
		// Make a shallow copy of ToolCalls before appending so we don't
		// mutate slices shared with the caller's input.
		merged := make([]providers.ToolCall, 0, len(out[i].ToolCalls)+len(added))
		merged = append(merged, out[i].ToolCalls...)
		merged = append(merged, added...)
		out[i].ToolCalls = merged
	}
	return out
}

// syntheticToolUsesForAssistant scans forward from assistant index i to the
// next assistant (or end) and returns synthetic ToolCalls for every orphan
// tool_result found in that window.
func syntheticToolUsesForAssistant(
	all []providers.Message,
	assistantIdx int,
	orphanResults map[string]bool,
	transcriptIndex map[string]*session.ToolCall,
	stats *repairStats,
) []providers.ToolCall {
	var added []providers.ToolCall
	for j := assistantIdx + 1; j < len(all); j++ {
		next := all[j]
		if next.Role == "assistant" {
			break
		}
		if next.Role != "tool" || next.ToolCallID == "" || !orphanResults[next.ToolCallID] {
			continue
		}
		entry := transcriptIndex[next.ToolCallID]
		if entry == nil {
			stats.Unrepairable++
			continue
		}
		added = append(added, providers.ToolCall{
			ID:        string(entry.ID),
			Type:      "function",
			Name:      entry.Tool,
			Arguments: entry.Parameters,
			Function: &providers.FunctionCall{
				Name:      entry.Tool,
				Arguments: marshalArgumentsString(entry.Parameters),
			},
		})
		stats.SyntheticToolUses++
	}
	return added
}

// injectSyntheticToolResults inserts a synthetic tool_result message for
// every orphan tool_use, placed at the end of the block of tool_result
// messages that follow the declaring assistant. Idempotent tools are
// re-invoked to regenerate a real result; everything else gets a short
// error stub so the conversation keeps moving.
func injectSyntheticToolResults(
	ctx context.Context,
	messages []providers.Message,
	orphanUses map[string]bool,
	transcriptIndex map[string]*session.ToolCall,
	toolRegistry *tools.ToolRegistry,
	agentID string,
	toolPolicy *tools.ToolPolicyCfg,
	stats *repairStats,
) []providers.Message {
	// For each assistant message, collect the orphan tool_use ids it owns in
	// the order they appear on its ToolCalls slice (preserves wire order).
	orphanIDsByAssistant := map[int][]string{}
	for i, m := range messages {
		if m.Role != "assistant" {
			continue
		}
		for _, tc := range m.ToolCalls {
			if orphanUses[tc.ID] {
				orphanIDsByAssistant[i] = append(orphanIDsByAssistant[i], tc.ID)
			}
		}
	}
	if len(orphanIDsByAssistant) == 0 {
		return messages
	}

	// Walk the input with a plain index so we can fast-forward past the
	// tool_result block that follows an owner assistant. Each message is
	// copied exactly once into the output; synthesized tool_results are
	// appended at the end of each owner's tool_result block.
	out := make([]providers.Message, 0, len(messages)+len(orphanUses))
	i := 0
	for i < len(messages) {
		m := messages[i]
		out = append(out, m)
		ids, owned := orphanIDsByAssistant[i]
		if !owned {
			i++
			continue
		}
		// Copy any real tool_result messages that already follow this assistant.
		j := i + 1
		for j < len(messages) && messages[j].Role == "tool" {
			out = append(out, messages[j])
			j++
		}
		// Append one synthesized result per unresolved tool_use.
		for _, id := range ids {
			entry := transcriptIndex[id]
			content, handled := tryReinvokeIdempotent(ctx, id, entry, toolRegistry, agentID, toolPolicy, stats)
			if !handled {
				content = syntheticErrorResult(id, entry)
				stats.SyntheticErrorStubs++
			}
			out = append(out, providers.Message{
				Role:       "tool",
				Content:    content,
				ToolCallID: id,
			})
		}
		i = j
	}
	return out
}

// tryReinvokeIdempotent runs the tool again if its name is on the idempotent
// whitelist — a side-effect-free replay that fills the missing result from
// reality rather than a placeholder. Returns the new result content and true
// on success; empty string and false if the tool isn't idempotent, isn't
// registered, or the invocation returned an error.
func tryReinvokeIdempotent(
	ctx context.Context,
	toolCallID string,
	entry *session.ToolCall,
	toolRegistry *tools.ToolRegistry,
	agentID string,
	toolPolicy *tools.ToolPolicyCfg,
	stats *repairStats,
) (string, bool) {
	if entry == nil || toolRegistry == nil {
		return "", false
	}
	if !isIdempotentTool(entry.Tool) {
		return "", false
	}
	// H3: check per-agent policy before re-invoking. A tool that policy now
	// denies must not be re-executed during repair — this would bypass the
	// policy enforcement gate that governs normal tool-call paths.
	if toolPolicy != nil {
		effective := tools.ResolveEffectivePolicy(toolPolicy, entry.Tool)
		if effective == "deny" {
			logger.WarnCF("agent.repair",
				"Skipping re-invocation of idempotent tool: policy now denies it",
				map[string]any{
					"tool":         entry.Tool,
					"tool_call_id": toolCallID,
					"agent_id":     agentID,
				})
			return "", false
		}
	}
	logger.InfoCF("agent.repair", "Re-invoking idempotent tool to reconstruct lost result",
		map[string]any{"tool": entry.Tool, "tool_call_id": toolCallID, "agent_id": agentID})
	result := toolRegistry.ExecuteWithContext(ctx, entry.Tool, cloneArgs(entry.Parameters), "", "", nil)
	if result == nil {
		return "", false
	}
	if result.IsError {
		logger.WarnCF("agent.repair", "Idempotent re-invocation returned error; falling back to synthetic stub",
			map[string]any{"tool": entry.Tool, "tool_call_id": toolCallID, "error": result.ForLLM})
		return "", false
	}
	stats.ReinvokedIdempotent++
	return result.ForLLM, true
}

// syntheticErrorResult produces a short tool_result body explaining that the
// real result was lost and cannot be regenerated safely. The LLM sees this as
// if the tool ran and returned a note, and typically recovers gracefully.
func syntheticErrorResult(toolCallID string, entry *session.ToolCall) string {
	if entry != nil {
		return fmt.Sprintf(
			"[omnipus:tool_result_recovered] The original result for %s (id=%s) was lost in the stream. "+
				"If this information is still needed, call the tool again. Do NOT assume success or failure.",
			entry.Tool, toolCallID,
		)
	}
	return fmt.Sprintf(
		"[omnipus:tool_result_recovered] The original result for tool call id=%s was lost in the stream and cannot be reconstructed. "+
			"If this information is still needed, re-issue the tool call.",
		toolCallID,
	)
}

// loadTranscriptToolCalls reads the session transcript and indexes every tool
// call entry by its id. Returns nil if the transcript can't be loaded — the
// caller treats that as "no transcript available, fall through to the wire
// sanitizer."
func loadTranscriptToolCalls(store *session.UnifiedStore, sessionID string) map[string]*session.ToolCall {
	if store == nil || sessionID == "" {
		return nil
	}
	entries, err := store.ReadTranscript(sessionID)
	if err != nil {
		logger.WarnCF("agent.repair", "ReadTranscript failed; cannot reconstruct orphans",
			map[string]any{"session_id": sessionID, "error": err.Error()})
		return nil
	}
	index := make(map[string]*session.ToolCall, len(entries)*2)
	for i := range entries {
		entry := &entries[i]
		for j := range entry.ToolCalls {
			tc := &entry.ToolCalls[j]
			if tc.ID != "" {
				index[string(tc.ID)] = tc
			}
		}
	}
	return index
}

// isIdempotentTool returns true when re-invoking the named tool is safe —
// meaning calling it again with the same arguments will not mutate external
// state or charge the user twice. Only tools with no side effects outside the
// agent's read path belong here. New tools default to "not idempotent"; add
// them explicitly after an audit.
func isIdempotentTool(name string) bool {
	switch name {
	case
		"web_search",
		"web_fetch",
		"read_file",
		"list_dir",
		"find_skills",
		"agent_list",
		"task_list",
		"skills",
		"find":
		return true
	}
	return false
}

func cloneArgs(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func marshalArgumentsString(args map[string]any) string {
	if len(args) == 0 {
		return "{}"
	}
	data, err := json.Marshal(args)
	if err != nil {
		return "{}"
	}
	return string(data)
}

type repairStats struct {
	SyntheticToolUses   int
	ReinvokedIdempotent int
	SyntheticErrorStubs int
	Unrepairable        int
}

func (s repairStats) anyRepaired() bool {
	return s.SyntheticToolUses+s.ReinvokedIdempotent+s.SyntheticErrorStubs+s.Unrepairable > 0
}

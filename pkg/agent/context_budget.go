// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"encoding/json"
	"unicode/utf8"

	"github.com/elicify-ai/omnipus/pkg/providers"
)

// parseTurnBoundaries returns the starting index of each Turn in the history.
// A Turn is a complete "user input → LLM iterations → final response" cycle
// (as defined in #1316). Each Turn begins at a user message and extends
// through all subsequent assistant/tool messages until the next user message.
//
// Cutting at a Turn boundary guarantees that no tool-call sequence
// (assistant+ToolCalls → tool results) is split across the cut.
func parseTurnBoundaries(history []providers.Message) []int {
	var starts []int
	for i, msg := range history {
		if msg.Role == "user" {
			starts = append(starts, i)
		}
	}
	return starts
}

// estimateMessageTokens estimates the token count for a single message,
// including Content, ReasoningContent, ToolCalls arguments, ToolCallID
// metadata, and Media items. Uses a heuristic of 2.5 characters per token.
func estimateMessageTokens(msg providers.Message) int {
	chars := utf8.RuneCountInString(msg.Content)

	// ReasoningContent (extended thinking / chain-of-thought) can be
	// substantial and is stored in session history via AddFullMessage.
	if msg.ReasoningContent != "" {
		chars += utf8.RuneCountInString(msg.ReasoningContent)
	}

	for _, tc := range msg.ToolCalls {
		chars += len(tc.ID) + len(tc.Type)
		if tc.Function != nil {
			// Count function name + arguments (the wire format for most providers).
			// tc.Name mirrors tc.Function.Name — count only once to avoid double-counting.
			chars += len(tc.Function.Name) + len(tc.Function.Arguments)
		} else {
			// Fallback: some provider formats use top-level Name without Function.
			chars += len(tc.Name)
		}
	}

	if msg.ToolCallID != "" {
		chars += len(msg.ToolCallID)
	}

	// Per-message overhead for role label, JSON structure, separators.
	const messageOverhead = 12
	chars += messageOverhead

	tokens := chars * 2 / 5

	// Media items (images, files) are serialized by provider adapters into
	// multipart or image_url payloads. Add a fixed per-item token estimate
	// directly (not through the chars heuristic) since actual cost depends
	// on resolution and provider-specific image tokenization.
	const mediaTokensPerItem = 256
	tokens += len(msg.Media) * mediaTokensPerItem

	return tokens
}

// estimateToolDefsTokens estimates the total token cost of tool definitions
// as they appear in the LLM request. Each tool's name, description, and
// JSON schema parameters contribute to the context window budget.
func estimateToolDefsTokens(defs []providers.ToolDefinition) int {
	if len(defs) == 0 {
		return 0
	}

	totalChars := 0
	for _, d := range defs {
		totalChars += len(d.Function.Name) + len(d.Function.Description)

		if d.Function.Parameters != nil {
			if paramJSON, err := json.Marshal(d.Function.Parameters); err == nil {
				totalChars += len(paramJSON)
			}
		}

		// Per-tool overhead: type field, JSON structure, separators.
		totalChars += 20
	}

	return totalChars * 2 / 5
}

// contextBudget is the ONE budget B every consumer reads (ADR-066 D6,
// FR-028):
//
//	B = W − max_tokens − ceil(0.05·W) − pinnedCoreOverhead
//
// W is the resolved context window, max_tokens the output reserve, the 5 %
// term the headroom that keeps a just-trimmed window from re-trimming on the
// very next turn, and pinnedCoreOverhead the estimated cost of the pinned
// system prompt plus the breadcrumb block. B is what the NON-pinned request
// (window history, injected notes, tool surface) may occupy; it can be ≤ 0
// when max_tokens alone exceeds the window — callers treat that as "always
// over" (FR-005b clamps max_tokens so a real instance never gets there).
//
// The formula is the one windowTrim always used for its suffix fit-check;
// the pre-turn and timeout-recovery checks now derive their threshold from
// the same helper instead of comparing against the raw window or a
// percentage-scaled one (the retired summarize_token_percent).
func contextBudget(contextWindow, maxTokens, pinnedCoreOverhead int) int {
	headroom := (contextWindow + 19) / 20 // ceil(0.05 * contextWindow)
	return contextWindow - maxTokens - headroom - pinnedCoreOverhead
}

// pinnedCoreOverheadTokens estimates the pinned core an assembled request
// always carries: the agent's system prompt (via the ContextBuilder cache —
// the static prompt is already cached, so this is cheap) plus the
// breadcrumb block's hard cap. It uses the same chars*2/5 heuristic as
// estimateMessageTokens so the two cannot drift (chars/4 would underestimate
// by ~38 % and cause under-eviction on small-window models).
func pinnedCoreOverheadTokens(agent *AgentInstance) int {
	overhead := 0
	if agent.ContextBuilder != nil {
		overhead = len(agent.ContextBuilder.BuildSystemPromptWithCache()) * 2 / 5
	}
	// breadcrumbTokenCap is the hard cap on the breadcrumb block (~1000
	// tokens); use it as a conservative estimate of the breadcrumb overhead.
	return overhead + breadcrumbTokenCap
}

// agentContextBudget resolves B for one agent instance — the single call
// every budget site (pre-turn, timeout-recovery, windowTrim; mid-turn and
// model-switch once T066-13/T066-09 land) makes, so they can never disagree.
func agentContextBudget(agent *AgentInstance) int {
	contextWindow := agent.ContextWindow
	if contextWindow <= 0 {
		// Pre-ADR-066-D2 fallback, kept where windowTrim always had it.
		// T066-09 deletes it: NewAgentInstance will resolve W through the
		// ladder and an exempt (cli_driver) agent skips every budget check.
		contextWindow = 128000
	}
	return contextBudget(contextWindow, agent.MaxTokens, pinnedCoreOverheadTokens(agent))
}

// isOverContextBudget reports whether the assembled request would exceed the
// budget B (see contextBudget). It counts every message except the pinned
// system prompt at messages[0] — that cost is already inside B via
// pinnedCoreOverhead — plus the tool surface. System-role notes the turn
// injects after the pinned prompt (scratchpad, workspace instructions,
// manifest) are real request bytes and DO count. The output reserve is not
// added here either: B already subtracted it. This enables proactive
// trimming before calling the LLM, rather than reacting to 400 errors.
func isOverContextBudget(
	budget int,
	messages []providers.Message,
	toolDefs []providers.ToolDefinition,
) bool {
	msgTokens := 0
	for i, m := range messages {
		if i == 0 && m.Role == "system" {
			continue // pinned core: accounted for in B
		}
		msgTokens += estimateMessageTokens(m)
	}

	toolTokens := estimateToolDefsTokens(toolDefs)

	return msgTokens+toolTokens > budget
}

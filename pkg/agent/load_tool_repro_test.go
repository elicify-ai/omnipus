// Omnipus — reproduction tests for ToolSearch bugs C2/C3/C4 and message normalization B.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// These tests are written RED first (they fail before the fixes) and GREEN after.
// Build tags: goolm,stdjson  (same as the rest of the agent package tests).

package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// ── Helper: retrieve *tools.ToolsTool for an agent ───────────────────────────

func loadToolFor(t *testing.T, al *AgentLoop, agentID string) *tools.ToolsTool {
	t.Helper()
	ag, ok := al.registry.GetAgent(agentID)
	require.True(t, ok, "agent %q must exist", agentID)
	raw, ok := ag.Tools.Get("ToolSearch")
	require.True(t, ok, "ToolSearch must be registered for %q", agentID)
	tt, ok := raw.(*tools.ToolsTool)
	require.True(t, ok, "ToolSearch must be *tools.ToolsTool")
	return tt
}

func execCtx(agentID, sessionID string) context.Context {
	ctx := tools.WithAgentID(context.Background(), agentID)
	return tools.WithTranscriptSessionID(ctx, sessionID)
}

// ── C2: Full-tier tools must return no-op SUCCESS (not error) ─────────────────

// TestLoadTool_FullTierReturnsNoopSuccess reproduces C2:
// ToolSearch{names:["search_web"]} for Jim currently returns an error
// ("unknown or not available") instead of a graceful no-op success.
func TestLoadTool_FullTierReturnsNoopSuccess(t *testing.T) {
	cfg := newCompressedCfg(t)
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.Close()

	// search_web is full-tier.
	require.Equal(t, tools.ManifestFull, tools.ToolManifestTier("search_web"),
		"search_web must be ManifestFull for this test to be meaningful")

	tt := loadToolFor(t, al, "jim")
	ctx := execCtx("jim", "sess-c2-full-tier")

	result := tt.Execute(ctx, map[string]any{"names": []any{"search_web"}})

	// C2 fix: full-tier must NOT be an error — it's a no-op success.
	assert.False(t, result.IsError,
		"search_web (full-tier) must produce no-op SUCCESS, got error: %s", result.ForLLM)
	assert.True(t, strings.Contains(result.ForLLM, "already available") ||
		strings.Contains(result.ForLLM, "already"),
		"no-op message must mention 'already available'; got: %s", result.ForLLM)
	assert.True(t, strings.Contains(result.ForLLM, "search_web"),
		"no-op message must name the tool; got: %s", result.ForLLM)
}

// ── C3: Policy-denied tools must give a clear reason ─────────────────────────

// TestLoadTool_PolicyDeniedCarriesReason reproduces C3:
// ToolSearch{names:["create_task"]} for Ava currently says
// "unknown or not available" instead of "denied by … policy".
//
// Note: we use create_task (ManifestLazy, registered for all agents via
// registerSharedTools, NOT in Ava's deny-by-default allow-list) — NOT
// read_file (ManifestFull). Full-tier tools return a no-op success (C2
// behavior) regardless of policy, so they are not the right subject here.
//
// send_file was the original fixture here, but ADR-071 D3 promoted it (along
// with list_mounts, message_parent, recall_conversation) into ManifestFull
// (see pkg/tools/manifest.go's fullManifestToolNames) as a conversational
// addressing primitive with no natural discovery moment — it is no longer
// ManifestLazy, so it can't exercise this policy-denial path. create_task
// stays ManifestLazy under ADR-071 D3 (moved to the previewed lazy tier
// alongside bash/navigate/update_task) and is still absent from Ava's
// deny-by-default allow-list, so it reproduces the same policy-denied
// scenario the test is meant to cover.
func TestLoadTool_PolicyDeniedCarriesReason(t *testing.T) {
	cfg := newCompressedCfg(t)
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.Close()

	// create_task is lazy-tier and NOT in Ava's explicit allow-list.
	require.Equal(t, tools.ManifestLazy, tools.ToolManifestTier("create_task"),
		"create_task must be ManifestLazy for this test to be meaningful")

	// Structural guarantee: create_task must NOT be in Ava's policy-filtered set.
	avaAgent, ok := al.registry.GetAgent("ava")
	require.True(t, ok)
	allTools := avaAgent.Tools.GetAll()
	policyFiltered, _ := tools.FilterToolsByPolicy(allTools, avaAgent.AgentType, avaAgent.LoadToolPolicy())
	for _, tool := range policyFiltered {
		require.NotEqual(t, "create_task", tool.Name(),
			"create_task must NOT be policy-allowed for ava — update test if Ava policy changed")
	}

	tt := loadToolFor(t, al, "ava")
	ctx := execCtx("ava", "sess-c3-denied")

	result := tt.Execute(ctx, map[string]any{"names": []any{"create_task"}})

	assert.True(t, result.IsError,
		"create_task must be rejected for ava (policy denied); got: %s", result.ForLLM)
	assert.True(t,
		strings.Contains(result.ForLLM, "denied") || strings.Contains(result.ForLLM, "policy"),
		"error message must mention 'denied' or 'policy'; got: %s", result.ForLLM)
	// Must NOT say "unknown or not available" — that conflates denied vs. unknown.
	assert.False(t, strings.Contains(result.ForLLM, "unknown or not available"),
		"error must not say 'unknown or not available' for a policy-denied tool; got: %s", result.ForLLM)
}

// ── C4: Unknown names must suggest the correct name ──────────────────────────

// TestLoadTool_UnknownNameSuggestsAlternative reproduces C4:
// ToolSearch{names:["task_update"]} (hallucinated — real name is "update_task")
// currently says "unknown or not available" with no hint.
func TestLoadTool_UnknownNameSuggestsAlternative(t *testing.T) {
	cfg := newCompressedCfg(t)
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.Close()

	tt := loadToolFor(t, al, "jim")
	ctx := execCtx("jim", "sess-c4-unknown")

	result := tt.Execute(ctx, map[string]any{"names": []any{"task_update"}})

	assert.True(t, result.IsError,
		"task_update (hallucinated name) must be rejected")
	// Must suggest the real name "update_task" or at least say "did you mean".
	assert.True(t,
		strings.Contains(result.ForLLM, "update_task") ||
			strings.Contains(strings.ToLower(result.ForLLM), "did you mean"),
		"error must suggest 'update_task' or 'did you mean'; got: %s", result.ForLLM)
}

// ── C1 control: Allowed lazy tool still loads ─────────────────────────────────

// TestLoadTool_AllowedLazySucceeds_Ava confirms that Ava can still load
// find_skills (allowed, lazy) after the C2/C3/C4 fixes.
func TestLoadTool_AllowedLazySucceeds_Ava(t *testing.T) {
	cfg := newCompressedCfg(t)
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	defer al.Close()

	// find_skills must be lazy (control assertion).
	require.Equal(t, tools.ManifestLazy, tools.ToolManifestTier("find_skills"),
		"find_skills must be ManifestLazy")

	tt := loadToolFor(t, al, "ava")
	ctx := execCtx("ava", "sess-c1-ava-lazy")

	result := tt.Execute(ctx, map[string]any{"names": []any{"find_skills"}})
	assert.False(t, result.IsError,
		"find_skills must be loadable by ava; got: %s", result.ForLLM)

	// A hallucinated name for ava must still fail.
	result2 := tt.Execute(ctx, map[string]any{"names": []any{"definitely_not_a_real_tool_xyz"}})
	assert.True(t, result2.IsError,
		"hallucinated name must be rejected")
}

// ── B: Message normalization ──────────────────────────────────────────────────

// TestNormalizeMessagesForProvider covers the five normalization rules.
func TestNormalizeMessagesForProvider(t *testing.T) {
	msg := func(role, content string) providers.Message {
		return providers.Message{Role: role, Content: content}
	}
	toolCall := func(id string) providers.ToolCall {
		return providers.ToolCall{ID: id, Type: "function"}
	}
	msgWithCalls := func(role, content string, calls ...providers.ToolCall) providers.Message {
		return providers.Message{Role: role, Content: content, ToolCalls: calls}
	}
	toolResult := func(content, callID string) providers.Message {
		return providers.Message{Role: "tool", Content: content, ToolCallID: callID}
	}

	t.Run("consecutive_assistant_merged", func(t *testing.T) {
		input := []providers.Message{
			msg("system", "sys"),
			msg("user", "hi"),
			msg("assistant", "A1"),
			msg("assistant", "A2"),
		}
		got := normalizeMessagesForProvider(input)
		require.Len(t, got, 3, "two adjacent assistant messages must be merged into one")
		assert.Equal(t, "assistant", got[2].Role)
		assert.True(t,
			strings.Contains(got[2].Content, "A1") && strings.Contains(got[2].Content, "A2"),
			"merged assistant content must contain both A1 and A2; got: %s", got[2].Content)
	})

	t.Run("empty_assistant_dropped", func(t *testing.T) {
		// Place the empty assistant between user and a following assistant so that
		// dropping it does not create consecutive same-role messages (which would
		// trigger the merge rule and change the expected count).
		input := []providers.Message{
			msg("system", "sys"),
			msg("user", "hi"),
			msg("assistant", ""),           // empty — must be dropped
			msg("assistant", "real reply"), // non-empty — must survive
		}
		got := normalizeMessagesForProvider(input)
		// After drop: [system, user, assistant("real reply")] = 3 messages.
		require.Len(t, got, 3, "empty assistant with no tool_calls must be dropped; got %v", got)
		// Verify no empty assistant remains.
		for _, m := range got {
			if m.Role == "assistant" {
				assert.NotEmpty(t, m.Content, "surviving assistant must have content")
			}
		}
		assert.Equal(t, "real reply", got[2].Content, "non-empty assistant content must survive")
	})

	t.Run("orphan_tool_passthrough", func(t *testing.T) {
		// Rule D (Fix A): a role="tool" message whose ToolCallID is NOT declared
		// by any preceding assistant's ToolCalls is a TRUE ORPHAN and must be
		// dropped by the normalizer. This guards the transcript-less path
		// (processSystemMessage has no TranscriptStore → repairHistory is skipped)
		// where an orphan tool result would otherwise reach the Anthropic serializer
		// and cause HTTP 400.
		input := []providers.Message{
			msg("system", "sys"),
			msg("user", "hi"),
			toolResult("some result", "call-1"), // no preceding assistant declared "call-1"
		}
		got := normalizeMessagesForProvider(input)
		// The true orphan must be DROPPED (ToolCallID "call-1" was never declared).
		for _, m := range got {
			if m.Role == "tool" {
				assert.Fail(t, "true orphan tool result must be dropped by normalizer (Rule D)",
					"unexpected tool message in output: %+v", m)
			}
		}
		// The non-tool messages must survive.
		assert.Len(t, got, 2, "system and user messages must survive orphan drop")
	})

	t.Run("valid_alternating_passthrough", func(t *testing.T) {
		tc := toolCall("c1")
		input := []providers.Message{
			msg("system", "sys"),
			msg("user", "q"),
			msgWithCalls("assistant", "", tc),
			toolResult("result", "c1"),
			msg("user", "next"),
		}
		got := normalizeMessagesForProvider(input)
		// Already valid — must pass through unchanged.
		require.Len(t, got, len(input), "valid alternating history must pass through unchanged")
	})

	t.Run("single_system_unchanged", func(t *testing.T) {
		input := []providers.Message{msg("system", "only system")}
		got := normalizeMessagesForProvider(input)
		require.Len(t, got, 1)
		assert.Equal(t, "system", got[0].Role)
		assert.Equal(t, "only system", got[0].Content)
	})
}

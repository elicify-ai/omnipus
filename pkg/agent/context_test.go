package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
)

func msg(role, content string) providers.Message {
	return providers.Message{Role: role, Content: content}
}

func assistantWithTools(toolIDs ...string) providers.Message {
	calls := make([]providers.ToolCall, len(toolIDs))
	for i, id := range toolIDs {
		calls[i] = providers.ToolCall{ID: id, Type: "function"}
	}
	return providers.Message{Role: "assistant", ToolCalls: calls}
}

func toolResult(id string) providers.Message {
	return providers.Message{Role: "tool", Content: "result", ToolCallID: id}
}

func TestSanitizeHistoryForProvider_EmptyHistory(t *testing.T) {
	result := sanitizeHistoryForProvider(nil)
	if len(result) != 0 {
		t.Fatalf("expected empty, got %d messages", len(result))
	}

	result = sanitizeHistoryForProvider([]providers.Message{})
	if len(result) != 0 {
		t.Fatalf("expected empty, got %d messages", len(result))
	}
}

func TestSanitizeHistoryForProvider_SingleToolCall(t *testing.T) {
	history := []providers.Message{
		msg("user", "hello"),
		assistantWithTools("A"),
		toolResult("A"),
		msg("assistant", "done"),
	}

	result := sanitizeHistoryForProvider(history)
	if len(result) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(result))
	}
	assertRoles(t, result, "user", "assistant", "tool", "assistant")
}

func TestSanitizeHistoryForProvider_MultiToolCalls(t *testing.T) {
	history := []providers.Message{
		msg("user", "do two things"),
		assistantWithTools("A", "B"),
		toolResult("A"),
		toolResult("B"),
		msg("assistant", "both done"),
	}

	result := sanitizeHistoryForProvider(history)
	if len(result) != 5 {
		t.Fatalf("expected 5 messages, got %d: %+v", len(result), roles(result))
	}
	assertRoles(t, result, "user", "assistant", "tool", "tool", "assistant")
}

func TestSanitizeHistoryForProvider_AssistantToolCallAfterPlainAssistant(t *testing.T) {
	history := []providers.Message{
		msg("user", "hi"),
		msg("assistant", "thinking"),
		assistantWithTools("A"),
		toolResult("A"),
	}

	result := sanitizeHistoryForProvider(history)
	if len(result) != 2 {
		t.Fatalf("expected 2 messages, got %d: %+v", len(result), roles(result))
	}
	assertRoles(t, result, "user", "assistant")
}

func TestSanitizeHistoryForProvider_OrphanedLeadingTool(t *testing.T) {
	history := []providers.Message{
		toolResult("A"),
		msg("user", "hello"),
	}

	result := sanitizeHistoryForProvider(history)
	if len(result) != 1 {
		t.Fatalf("expected 1 message, got %d: %+v", len(result), roles(result))
	}
	assertRoles(t, result, "user")
}

func TestSanitizeHistoryForProvider_ToolAfterUserDropped(t *testing.T) {
	history := []providers.Message{
		msg("user", "hello"),
		toolResult("A"),
	}

	result := sanitizeHistoryForProvider(history)
	if len(result) != 1 {
		t.Fatalf("expected 1 message, got %d: %+v", len(result), roles(result))
	}
	assertRoles(t, result, "user")
}

func TestSanitizeHistoryForProvider_ToolAfterAssistantNoToolCalls(t *testing.T) {
	history := []providers.Message{
		msg("user", "hello"),
		msg("assistant", "hi"),
		toolResult("A"),
	}

	result := sanitizeHistoryForProvider(history)
	if len(result) != 2 {
		t.Fatalf("expected 2 messages, got %d: %+v", len(result), roles(result))
	}
	assertRoles(t, result, "user", "assistant")
}

func TestSanitizeHistoryForProvider_AssistantToolCallAtStart(t *testing.T) {
	history := []providers.Message{
		assistantWithTools("A"),
		toolResult("A"),
		msg("user", "hello"),
	}

	result := sanitizeHistoryForProvider(history)
	if len(result) != 1 {
		t.Fatalf("expected 1 message, got %d: %+v", len(result), roles(result))
	}
	assertRoles(t, result, "user")
}

func TestSanitizeHistoryForProvider_MultiToolCallsThenNewRound(t *testing.T) {
	history := []providers.Message{
		msg("user", "do two things"),
		assistantWithTools("A", "B"),
		toolResult("A"),
		toolResult("B"),
		msg("assistant", "done"),
		msg("user", "hi"),
		assistantWithTools("C"),
		toolResult("C"),
		msg("assistant", "done again"),
	}

	result := sanitizeHistoryForProvider(history)
	if len(result) != 9 {
		t.Fatalf("expected 9 messages, got %d: %+v", len(result), roles(result))
	}
	assertRoles(t, result, "user", "assistant", "tool", "tool", "assistant", "user", "assistant", "tool", "assistant")
}

func TestSanitizeHistoryForProvider_ConsecutiveMultiToolRounds(t *testing.T) {
	history := []providers.Message{
		msg("user", "start"),
		assistantWithTools("A", "B"),
		toolResult("A"),
		toolResult("B"),
		assistantWithTools("C", "D"),
		toolResult("C"),
		toolResult("D"),
		msg("assistant", "all done"),
	}

	result := sanitizeHistoryForProvider(history)
	if len(result) != 8 {
		t.Fatalf("expected 8 messages, got %d: %+v", len(result), roles(result))
	}
	assertRoles(t, result, "user", "assistant", "tool", "tool", "assistant", "tool", "tool", "assistant")
}

func TestSanitizeHistoryForProvider_PlainConversation(t *testing.T) {
	history := []providers.Message{
		msg("user", "hello"),
		msg("assistant", "hi"),
		msg("user", "how are you"),
		msg("assistant", "fine"),
	}

	result := sanitizeHistoryForProvider(history)
	if len(result) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(result))
	}
	assertRoles(t, result, "user", "assistant", "user", "assistant")
}

func TestSanitizeHistoryForProvider_DuplicateToolResults(t *testing.T) {
	history := []providers.Message{
		msg("user", "do something"),
		assistantWithTools("A", "B"),
		toolResult("A"),
		toolResult("B"),
		toolResult("A"), // duplicate
		toolResult("B"), // duplicate
		msg("assistant", "done"),
	}

	result := sanitizeHistoryForProvider(history)
	if len(result) != 5 {
		t.Fatalf("expected 5 messages, got %d: %+v", len(result), roles(result))
	}
	assertRoles(t, result, "user", "assistant", "tool", "tool", "assistant")
	// Verify the kept tool results have the correct IDs
	if result[2].ToolCallID != "A" {
		t.Errorf("expected tool result A, got %q", result[2].ToolCallID)
	}
	if result[3].ToolCallID != "B" {
		t.Errorf("expected tool result B, got %q", result[3].ToolCallID)
	}
}

func roles(msgs []providers.Message) []string {
	r := make([]string, len(msgs))
	for i, m := range msgs {
		r[i] = m.Role
	}
	return r
}

func assertRoles(t *testing.T, msgs []providers.Message, expected ...string) {
	t.Helper()
	if len(msgs) != len(expected) {
		t.Fatalf("role count mismatch: got %v, want %v", roles(msgs), expected)
	}
	for i, exp := range expected {
		if msgs[i].Role != exp {
			t.Errorf("message[%d]: got role %q, want %q", i, msgs[i].Role, exp)
		}
	}
}

// TestSanitizeHistoryForProvider_IncompleteToolResults tests the forward validation
// that ensures assistant messages with tool_calls have ALL matching tool results.
// This fixes the DeepSeek error: "An assistant message with 'tool_calls' must be
// followed by tool messages responding to each 'tool_call_id'."
func TestSanitizeHistoryForProvider_IncompleteToolResults(t *testing.T) {
	// Assistant expects tool results for both A and B, but only A is present
	history := []providers.Message{
		msg("user", "do two things"),
		assistantWithTools("A", "B"),
		toolResult("A"),
		// toolResult("B") is missing - this would cause DeepSeek to fail
		msg("user", "next question"),
		msg("assistant", "answer"),
	}

	result := sanitizeHistoryForProvider(history)
	// The assistant message with incomplete tool results should be dropped,
	// along with its partial tool result. The remaining messages are:
	// user ("do two things"), user ("next question"), assistant ("answer")
	if len(result) != 3 {
		t.Fatalf("expected 3 messages, got %d: %+v", len(result), roles(result))
	}
	assertRoles(t, result, "user", "user", "assistant")
}

// TestSanitizeHistoryForProvider_MissingAllToolResults tests the case where
// an assistant message has tool_calls but no tool results follow at all.
func TestSanitizeHistoryForProvider_MissingAllToolResults(t *testing.T) {
	history := []providers.Message{
		msg("user", "do something"),
		assistantWithTools("A"),
		// No tool results at all
		msg("user", "hello"),
		msg("assistant", "hi"),
	}

	result := sanitizeHistoryForProvider(history)
	// The assistant message with no tool results should be dropped.
	// Remaining: user ("do something"), user ("hello"), assistant ("hi")
	if len(result) != 3 {
		t.Fatalf("expected 3 messages, got %d: %+v", len(result), roles(result))
	}
	assertRoles(t, result, "user", "user", "assistant")
}

// TestSanitizeHistoryForProvider_PartialToolResultsInMiddle tests that
// incomplete tool results in the middle of a conversation are properly handled.
func TestSanitizeHistoryForProvider_PartialToolResultsInMiddle(t *testing.T) {
	history := []providers.Message{
		msg("user", "first"),
		assistantWithTools("A"),
		toolResult("A"),
		msg("assistant", "done"),
		msg("user", "second"),
		assistantWithTools("B", "C"),
		toolResult("B"),
		// toolResult("C") is missing
		msg("user", "third"),
		assistantWithTools("D"),
		toolResult("D"),
		msg("assistant", "all done"),
	}

	result := sanitizeHistoryForProvider(history)
	// First round is complete (user, assistant+tools, tool, assistant),
	// second round is incomplete and dropped (assistant+tools, partial tool),
	// third round is complete (user, assistant+tools, tool, assistant).
	// Remaining: user, assistant, tool, assistant, user, user, assistant, tool, assistant
	if len(result) != 9 {
		t.Fatalf("expected 9 messages, got %d: %+v", len(result), roles(result))
	}
	assertRoles(t, result, "user", "assistant", "tool", "assistant", "user", "user", "assistant", "tool", "assistant")
}

// TestBuildSystemPrompt_CoreAgentUsesCompiledPrompt verifies that a core agent
// (e.g., Jim) gets its compiled prompt from coreagent.GetPrompt, not the
// default "You are omnipus" identity or a SOUL.md file.
//
// BDD: Given a ContextBuilder with agentID="jim",
//
//	When BuildSystemPrompt is called,
//	Then the output contains "You are Jim" from the compiled prompt,
//	And does NOT contain the default identity fallback text.
//
// Traces to: issue #45 — compiled prompts for core agents
func TestBuildSystemPrompt_CoreAgentUsesCompiledPrompt(t *testing.T) {
	workspace := t.TempDir()

	cb := NewContextBuilder(workspace)
	cb.WithAgentInfo("jim", "Jim")

	prompt := cb.BuildSystemPrompt()

	// Must contain the compiled prompt content for Jim
	if !strings.Contains(prompt, "You are Jim") {
		t.Errorf(
			"expected compiled prompt to contain 'You are Jim', got first 500 chars:\n%s",
			prompt[:min(len(prompt), 500)],
		)
	}

	// Must NOT contain the default identity fallback
	if strings.Contains(prompt, "You are a helpful AI assistant") {
		t.Error("core agent must NOT fall back to default identity — compiled prompt was not injected")
	}
}

// TestBuildSystemPrompt_CustomAgentUsesDefaultIdentity verifies that a non-core
// agent without a SOUL.md gets the default identity.
func TestBuildSystemPrompt_CustomAgentUsesDefaultIdentity(t *testing.T) {
	workspace := t.TempDir()

	cb := NewContextBuilder(workspace)
	cb.WithAgentInfo("custom-bot", "Custom Bot")

	prompt := cb.BuildSystemPrompt()

	// Non-core agents should NOT get a compiled prompt
	if strings.Contains(prompt, "You are Jim") {
		t.Error("non-core agent must NOT get a compiled prompt")
	}
}

// TestBuildSystemPrompt_NoEmojiRule_AppliesUniformly is a regression test for
// the live-UAT finding (persona "Dana"): the seeded Worker subagent's
// self-identification reply persisted an emoji into the session transcript,
// violating CLAUDE.md's "No emoji in stored data or UI chrome" rule. Worker's
// compiled prompt is intentionally empty (coreagent.prompts["worker"] == ""),
// so it falls through to the generic default identity — which, before this
// fix, carried no style guidance against emoji at all.
//
// The fix adds Rule 6 ("No emoji") to getWorkspaceAndRules(), the ONE helper
// shared by every BuildSystemPrompt branch (core agents with a compiled
// prompt, custom agents with a SOUL.md, and agents with neither — Worker's
// exact case). This test proves the rule reaches all three branches, so any
// future seeded agent with an empty/absent soul inherits the same guidance
// structurally rather than needing it hand-copied into each persona string.
//
// BDD: Given a ContextBuilder for (a) a core agent with a compiled prompt
// (jim), (b) a custom agent with an on-disk SOUL.md, and (c) an agent with
// neither (the Worker fallback case),
//
//	When BuildSystemPrompt is called for each,
//	Then all three outputs contain the "No emoji" rule text.
//
// Traces to: CLAUDE.md "No emoji in stored data or UI chrome".
func TestBuildSystemPrompt_NoEmojiRule_AppliesUniformly(t *testing.T) {
	const noEmojiMarker = "**No emoji**"

	t.Run("core agent with compiled prompt (jim)", func(t *testing.T) {
		workspace := t.TempDir()
		cb := NewContextBuilder(workspace)
		cb.WithAgentInfo("jim", "Jim")

		prompt := cb.BuildSystemPrompt()
		if !strings.Contains(prompt, noEmojiMarker) {
			t.Errorf("core-agent prompt missing %q, first 800 chars:\n%s", noEmojiMarker, prompt[:min(len(prompt), 800)])
		}
	})

	t.Run("custom agent with on-disk SOUL.md", func(t *testing.T) {
		workspace := t.TempDir()
		soulDir := workspace
		if err := os.MkdirAll(soulDir, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(filepath.Join(soulDir, "SOUL.md"), []byte("You are Custom Bot, a friendly helper."), 0o644); err != nil {
			t.Fatalf("WriteFile SOUL.md: %v", err)
		}

		cb := NewContextBuilder(workspace)
		cb.WithAgentInfo("custom-bot", "Custom Bot")

		prompt := cb.BuildSystemPrompt()
		if !strings.Contains(prompt, noEmojiMarker) {
			t.Errorf("custom-agent (SOUL.md) prompt missing %q, first 800 chars:\n%s", noEmojiMarker, prompt[:min(len(prompt), 800)])
		}
	})

	t.Run("agent with no compiled prompt and no SOUL.md (Worker fallback)", func(t *testing.T) {
		workspace := t.TempDir()
		cb := NewContextBuilder(workspace)
		// "worker" resolves coreagent.GetPrompt("worker") == "" (empty by
		// design), and the temp workspace has no SOUL.md — so this exercises
		// the exact getIdentity() fallback branch that produced the
		// unguarded "Hello! I'm Worker..." self-introduction in UAT.
		cb.WithAgentInfo("worker", "Worker")

		prompt := cb.BuildSystemPrompt()
		if !strings.Contains(prompt, noEmojiMarker) {
			t.Errorf("worker-fallback prompt missing %q, first 800 chars:\n%s", noEmojiMarker, prompt[:min(len(prompt), 800)])
		}
	})
}

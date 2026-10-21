// Package agent — regression test for bug #416 and per-call accounting.
//
// ProcessScheduled completes four provider calls: three narrated segments and
// one no-text tool round. Prompt-caching's Contract changes, FR-015 and the
// squad lead's Q1 option-A ruling require four assistant accounting entries,
// each with its own usage, while visible narration stays exactly three segments.
// This test exercises the non-streaming path; it makes no streaming claim.

package agent

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// TestInterleavedAssistantText_AllSegmentsPersisted reproduces bug #416.
//
// Scenario: two narrated tool rounds, one no-text tool round, then a final
// plain-text response. Every completed call has its own accounting entry:
//
//	Round 1: "Got it, saving fact 1." + remember, 100 input / 20 output
//	Round 2: empty visible text + remember, 50 input / 10 output
//	Round 3: "Got it, saving fact 2." + remember, 40 input / 5 output
//	Final:   "All three facts saved!", 30 input / 15 output
//
// The #416 narration/order guarantee remains. No-text accounting is not an
// extra visible narration, and the final entry must not duplicate turn usage.
func TestInterleavedAssistantText_AllSegmentsPersisted(t *testing.T) {
	// Cannot use t.Parallel() — this test calls t.Setenv (OMNIPUS_HOME).

	// -----------------------------------------------------------------------
	// Arrange: minimal AgentLoop wired to a scripted provider.
	// -----------------------------------------------------------------------
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	// RememberTool.Parameters requires content, category and room. Valid
	// arguments are necessary to exercise successful saves, not validation errors.
	provider := testutil.NewScenario().
		WithTextAndToolCall("Got it, saving fact 1.", "remember", `{"content":"Name: Alex","category":"reference","room":"private"}`).
		WithUsage(100, 20).
		// The no-text call still requires its own assistant accounting entry.
		WithToolCall("remember", `{"content":"Favorite color: blue","category":"reference","room":"private"}`).
		WithUsage(50, 10).
		WithTextAndToolCall("Got it, saving fact 2.", "remember", `{"content":"Pet: cat","category":"reference","room":"private"}`).
		WithUsage(40, 5).
		WithText("All three facts saved!").
		WithUsage(30, 15)

	cfg := &config.Config{}
	cfg.Providers = []*config.ModelConfig{{Provider: "openai", Model: "scripted-model"}}
	cfg.Agents.Defaults.Home = filepath.Join(home, "workspace")
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Provider: "openai", Model: "scripted-model"}
	cfg.Agents.Defaults.MaxTokens = 4096
	cfg.Agents.Defaults.MaxToolIterations = 10

	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })

	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	t.Cleanup(func() { al.Close() })

	// Register the agent that will own the scheduled session.
	mia := registerAgent(t, al, home, "mia", provider)
	// The agent's pinned pair is (openai, scripted-model). Named-pair routing
	// (#1340) serves a turn only from that pair's configured connection and
	// never borrows the agent's default client, so the scripted provider is
	// installed as exactly that connection.
	mia.storeProviderPoolBuild(providerPoolBuild{
		pool:   map[string]providers.LLMProvider{"openai": provider},
		routes: map[providers.FallbackCandidate]providers.LLMProvider{{Provider: "openai", Model: "scripted-model"}: provider},
	})
	// No-default-policy model (CLAUDE.md hard constraint 6): "remember" is a
	// real builtin tool with zero policy coverage on this bare test config, so
	// it now fails closed to "deny" unless granted explicitly — this test's
	// whole scenario depends on the scripted "remember" tool calls actually
	// executing so the interleaved-transcript ordering can be observed.
	mia.StoreToolPolicy(&tools.ToolPolicyCfg{
		Policies: map[string]config.ToolPolicy{"remember": "allow"},
	})

	// -----------------------------------------------------------------------
	// Arrange: session that carries a transcript store.
	// ProcessScheduled uses GetSessionStore() as the transcript store.
	// NewScheduledSession creates the session under the shared store which
	// ProcessScheduled's ResolveSessionStore can find.
	// -----------------------------------------------------------------------
	meta, err := al.GetSessionStore().NewScheduledSession("mia")
	require.NoError(t, err, "NewScheduledSession must succeed")
	sessionID := meta.ID
	require.NotEmpty(t, sessionID)

	// -----------------------------------------------------------------------
	// Act: drive a full turn through ProcessScheduled (non-streaming path).
	// -----------------------------------------------------------------------
	reply, err := al.ProcessScheduled(
		context.Background(),
		"mia", sessionID,
		"Save these 3 facts one at a time, write a sentence after each",
		"scheduled", sessionID,
	)
	require.NoError(t, err, "ProcessScheduled must not error")
	assert.Equal(t, 4, provider.CallCount(), "all four scripted provider calls must complete")
	assert.Equal(t, "All three facts saved!", reply)

	// -----------------------------------------------------------------------
	// Assert: the transcript must contain all three assistant text segments.
	// -----------------------------------------------------------------------
	// ResolveSessionStore finds the store that owns sessionID.
	store := al.ResolveSessionStore(sessionID)
	require.NotNil(t, store, "ResolveSessionStore must find the session store")

	entries, err := store.ReadTranscript(sessionID)
	require.NoError(t, err, "ReadTranscript must succeed")
	require.NotEmpty(t, entries, "transcript must be non-empty")

	// Collect assistant text entries in order.
	var assistantTexts []string
	for _, e := range entries {
		if e.Role == "assistant" && e.Content != "" {
			assistantTexts = append(assistantTexts, e.Content)
		}
	}

	assert.Equal(t, []string{
		"Got it, saving fact 1.", "Got it, saving fact 2.", "All three facts saved!",
	}, assistantTexts, "exactly three visible narrations must persist in order (#416)")

	var assistantEntries []session.TranscriptEntry
	toolCalls := make([]session.ToolCall, 0, 3)
	for _, e := range entries {
		if e.Role == "assistant" {
			assistantEntries = append(assistantEntries, e)
		}
		toolCalls = append(toolCalls, e.ToolCalls...)
	}
	require.Len(t, toolCalls, 3, "the scenario must execute and persist all three remember calls")
	for i, call := range toolCalls {
		assert.Equal(t, "remember", call.Tool, "tool call %d", i+1)
		assert.Equal(t, "success", call.Status, "remember call %d must save, not fail argument validation", i+1)
		assert.Empty(t, call.Error, "remember call %d must not carry an execution error", i+1)
	}

	// Contract changes + Q1 option A: one accounting entry for EACH completed
	// call, including the no-text call, never a final-entry whole-turn duplicate.
	// FR-015: this synthetic model has no input price; every token is unpriced.
	wants := []struct {
		content                   string
		prompt, completion, total int
	}{
		{"Got it, saving fact 1.", 100, 20, 120},
		{"", 50, 10, 60},
		{"Got it, saving fact 2.", 40, 5, 45},
		{"All three facts saved!", 30, 15, 45},
	}
	require.Len(t, assistantEntries, len(wants),
		"one assistant accounting entry per completed call, including the no-text call (Q1 option A)")
	var promptTotal, completionTotal, tokenTotal, unpricedTotal int
	var costTotal float64
	for i, want := range wants {
		entry := assistantEntries[i]
		assert.Equal(t, want.content, entry.Content, "call %d content", i+1)
		assert.Equal(t, "openai", entry.ProviderID, "call %d provider_id", i+1)
		assert.Equal(t, "scripted-model", entry.ModelID, "call %d verbatim model_id", i+1)
		assert.Equal(t, "scripted-model", entry.Model, "call %d display model", i+1)
		assert.Equal(t, want.prompt, entry.PromptTokens, "call %d uncached input", i+1)
		assert.Equal(t, want.completion, entry.CompletionTokens, "call %d output", i+1)
		assert.Zero(t, entry.CacheReadTokens, "call %d has no cache read in its fixture", i+1)
		assert.Zero(t, entry.CacheWriteTokens, "call %d has no cache write in its fixture", i+1)
		assert.Zero(t, entry.CacheWrite5mTokens, "call %d has no 5-minute write", i+1)
		assert.Zero(t, entry.CacheWrite1hTokens, "call %d has no 1-hour write", i+1)
		assert.Equal(t, want.total, entry.Tokens, "call %d total must not be the turn total", i+1)
		assert.Equal(t, "unpriced", entry.BillingClass, "call %d billing_class (FR-015)", i+1)
		assert.Equal(t, want.total, entry.UnpricedTokens, "call %d must keep every unpriced token", i+1)
		assert.Zero(t, entry.Cost, "call %d must not invent a fallback dollar cost (FR-015)", i+1)
		promptTotal += entry.PromptTokens
		completionTotal += entry.CompletionTokens
		tokenTotal += entry.Tokens
		unpricedTotal += entry.UnpricedTokens
		costTotal += entry.Cost
	}
	// Derived only from the four WithUsage inputs: 220 + 50 = 120+60+45+45.
	assert.Equal(t, 220, promptTotal, "all four calls' uncached input must be conserved")
	assert.Equal(t, 50, completionTotal, "all four calls' output must be conserved")
	assert.Equal(t, 270, tokenTotal, "all four calls counted exactly once (#411)")
	assert.Equal(t, 270, unpricedTotal, "all four calls' tokens stay explicitly unpriced")
	assert.Zero(t, costTotal, "unpriced synthetic usage must not create dollars")

	// #416 ordering still holds: each assistant entry precedes the tool call
	// it introduced. Q1 option A includes the no-text accounting entry too.
	type entryKind int
	const (
		kindAssistant entryKind = iota
		kindToolCall
		kindOther
	)
	kind := func(e session.TranscriptEntry) entryKind {
		if e.Role == "assistant" {
			return kindAssistant
		}
		if e.Type == session.EntryTypeToolCall || len(e.ToolCalls) > 0 {
			return kindToolCall
		}
		return kindOther
	}

	var sequence []entryKind
	for _, e := range entries {
		k := kind(e)
		if k != kindOther {
			sequence = append(sequence, k)
		}
	}

	// The no-text round has an assistant accounting entry, but no narration.
	expectedPattern := []entryKind{
		kindAssistant, kindToolCall,
		kindAssistant, kindToolCall,
		kindAssistant, kindToolCall,
		kindAssistant,
	}
	assert.Equal(t, expectedPattern, sequence,
		"all four per-call entries must preserve assistant-before-tool order (#416, Q1 option A)")
}

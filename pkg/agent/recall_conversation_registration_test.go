// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

//go:build goolm && stdjson

package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// TestRecallConversation_RealRegistration_ReachesEvictedTurns is an
// end-to-end regression test for a store-wiring bug: recall_conversation was
// registered against al.GetSessionStore() (the shared routing store, rooted
// at $OMNIPUS_HOME/sessions/) instead of agent.Sessions (the per-agent store
// that windowTrim actually evicts from and assembleMessages reads
// breadcrumbs from — turn.go's ts.session = agent.Sessions). The two are
// different UnifiedStore instances rooted at different directories for the
// same session key, so the tool always read an empty/unrelated archive.
//
// Every other recall_conversation test in this package hand-constructs
// RecallConversationTool with a stub archive reader, so none of them would
// catch a wiring bug at the real registration site (registerSharedTools in
// loop.go). This test drives the ACTUAL registered tool instance
// (agent.Tools.Get("recall_conversation")) through a real windowTrim
// eviction and confirms it can still read the evicted turn — proving the
// archive reader bound at registration is the same store windowTrim wrote
// to.
func TestRecallConversation_RealRegistration_ReachesEvictedTurns(t *testing.T) {
	const (
		cw         = 2000
		mt         = 400
		delegateID = "recall-wiring-test-agent"
		sessionKey = "recall-wiring-session"
	)
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				ContextWindow:     cw,
				MaxTokens:         mt,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{
				{ID: delegateID, Name: "Recall Wiring Test Agent", Type: config.AgentTypeCustom},
			},
		},
	}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	t.Cleanup(al.Close)

	agent, ok := al.GetRegistry().GetAgent(delegateID)
	require.True(t, ok, "test setup: delegate agent must be registered")

	// Seed enough turns that windowTrim evicts the earliest one from the live
	// window while the archive (agent.Sessions, the per-agent store) keeps
	// everything.
	filler := strings.Repeat("x", 400)
	const evictedMarker = "EVICTED_TURN_ONE_MARKER_9f3a1c"
	history := append([]providers.Message{}, makeTurn(evictedMarker+filler, filler)...)
	for i := 0; i < 5; i++ {
		history = append(history, makeTurn(filler, filler)...)
	}
	agent.Sessions.SetHistory(sessionKey, history)
	require.NoError(t, agent.Sessions.Save(sessionKey))

	windowBefore := agent.Sessions.GetHistory(sessionKey)
	require.Equal(t, len(history), len(windowBefore), "history must start unevicted")

	_, ok = al.windowTrim(agent, sessionKey)
	require.True(t, ok, "windowTrim must succeed")

	windowAfter := agent.Sessions.GetHistory(sessionKey)
	require.Less(t, len(windowAfter), len(windowBefore),
		"windowTrim must actually evict turns for this test to be meaningful")
	for _, m := range windowAfter {
		require.NotContains(t, m.Content, evictedMarker,
			"turn 1 must be evicted from the LIVE window for this test to prove anything")
	}

	// The archive must still have it — this is the pre-existing, well-tested
	// half of the mechanism.
	archived, err := agent.Sessions.ReadArchive(context.Background(), sessionKey)
	require.NoError(t, err)
	require.NotEmpty(t, archived)
	require.Contains(t, archived[0].Content, evictedMarker,
		"test setup: archive must retain the evicted turn")

	// The regression check: fetch the REAL registered tool (not a
	// hand-constructed stub) and confirm it can recall the evicted turn.
	tool, ok := agent.Tools.Get("recall_conversation")
	require.True(t, ok, "recall_conversation must be registered for this agent")

	ctx := tools.WithSessionKey(context.Background(), sessionKey)
	result := tool.Execute(ctx, map[string]any{"turn_range": "1-1"})
	require.NotNil(t, result)
	require.False(t, result.IsError, "recall_conversation must not error: %s", result.ForLLM)

	// Execute() only returns a short confirmation string to the model; the
	// actual recalled messages are merged into the next BuildMessages
	// assembly via the installed RecallSpan (recall_span.go). Assert on the
	// span itself — the direct, authoritative proof the tool read the right
	// archive.
	span := al.activeRecallSpan(sessionKey)
	require.NotNil(t, span,
		"recall_conversation (via its REAL production registration) must install a recall "+
			"span — if this is nil, the tool is registered against the wrong session store "+
			"(see loop.go's recall_conversation registration block)")
	spanMsgs := span.Messages()
	require.NotEmpty(t, spanMsgs, "installed recall span must carry the recalled messages")
	found := false
	for _, m := range spanMsgs {
		if strings.Contains(m.Content, evictedMarker) {
			found = true
			break
		}
	}
	require.True(t, found,
		"the recall span's messages must contain the evicted turn's content — got %+v", spanMsgs)
}

// TestMemoryTools_RegisteredRegardlessOfAgentID pins the removal of the
// `agentID != "main"` gate: instance.go used to register remember/
// recall_memory/run_retrospective (and loop.go used to register
// recall_conversation) for every agent EXCEPT the sentinel — a hardcoded
// identity check, not a capability one. That gate is gone; every agent now
// gets all four memory tools registered unconditionally, governed solely by
// tool policy (which may still DENY them — registration and policy are
// separate concerns). This test proves it for an agent literally named
// "main" (no longer reserved) as the strongest possible regression pin: if
// the old identity-based gate ever crept back in, this is exactly the ID it
// would silently exclude again.
func TestMemoryTools_RegisteredRegardlessOfAgentID(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{
				{ID: "main", Name: "Just An Agent Named Main", Type: config.AgentTypeCustom},
				{ID: "mia", Name: "Mia", Type: config.AgentTypeCustom},
			},
		},
	}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	t.Cleanup(al.Close)

	for _, agentID := range []string{"main", "mia"} {
		agent, ok := al.GetRegistry().GetAgent(agentID)
		require.True(t, ok, "test setup: agent %q must be registered", agentID)

		for _, toolName := range []string{"remember", "recall_memory", "run_retrospective", "recall_conversation"} {
			_, ok := agent.Tools.Get(toolName)
			require.True(t, ok, "agent %q must have %q registered — the old `agentID != \"main\"` gate is gone", agentID, toolName)
		}
	}
}

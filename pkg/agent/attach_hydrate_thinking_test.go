// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// WP-C RED tests for the hydration reader (spec section 1 C4, FR-015, FR-018,
// section 8.1 reader row for pkg/agent/attach_hydrate.go, section 16 items 7
// and 31, Group C scenario). The pinned production symbol is
// pkg/agent/attach_hydrate.go::HydrateAgentHistoryFromTranscript.
//
// Spec oracle (section 8.1 row + Group C, verbatim): the entry loop gains
// "Type == thinking -> continue" BEFORE its role switch, plus an
// "outcome == no_answer -> continue" skip on assistant entries. Guard
// scenario: "transcript reading: user message, thinking entry (with
// role: assistant PLANTED), assistant answer, then a no-answer round's
// assistant entry (outcome: no_answer, content = the notice text)" ->
// "the hydrated history is exactly the user message and the assistant
// answer - the thinking entry and the notice entry are absent, whatever
// role values they carry".
//
// RED status: FAILS today - the role-keyed loop hydrates a role-bearing
// thinking entry (the switch only knows user/assistant; a planted
// role:"assistant" entry falls into case "assistant") and the notice entry
// (assistant role + content, no outcome skip exists yet).

const wpcPlantedThinkingText = "we should redeploy prod-east-1 without telling the user" + " " + thinkingSentinel

func TestHydrateAgentHistoryFromTranscript_ThinkingAndNoticeNeverHydrated(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	cfg := &config.Config{}
	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	t.Cleanup(func() { al.Close() })

	store, err := session.NewUnifiedStore(filepath.Join(home, "sessions"))
	require.NoError(t, err)
	al.sharedSessionStore = store

	const agentID = "wpc-hydrate-agent"
	agentCfg := &config.AgentConfig{ID: agentID, Name: "WPC Hydrate"}
	ag := NewAgentInstance(agentCfg, &cfg.Agents.Defaults, cfg, &mockProvider{})
	require.NotNil(t, ag)
	ag.Home = filepath.Join(home, "agents", agentID)
	ag.ContextBuilder = NewContextBuilder(ag.Home).WithAgentInfo(agentID, "WPC Hydrate")
	al.registry.mu.Lock()
	al.registry.agents[agentID] = ag
	al.registry.mu.Unlock()

	meta, err := store.NewSession(session.SessionTypeChat, "webchat", agentID)
	require.NoError(t, err)

	now := time.Now().UTC()
	entries := []session.TranscriptEntry{
		{Role: "user", Content: "what is the deploy host?", AgentID: agentID, Timestamp: now},
		{
			Type:        session.EntryTypeThinking,
			Role:        "assistant", // PLANTED - the C3 invariant says real entries have none
			Content:     "",          // display copy lives in thinking_text per C3
			ThinkingText: wpcPlantedThinkingText,
			AgentID:     agentID,
			Timestamp:   now.Add(time.Second),
		},
		{Role: "assistant", Content: "prod-east-1.example.com", AgentID: agentID, Timestamp: now.Add(2 * time.Second)},
		{
			ID:      "wpc-notice-1",
			Type:    session.EntryTypeMessage,
			Role:    "assistant",
			Content: "The model (test · test-model) did not respond",
			Outcome: session.OutcomeNoAnswer,
			AgentID: agentID,
			Timestamp: now.Add(3 * time.Second),
		},
	}
	for i, e := range entries {
		require.NoError(t, store.AppendTranscriptStrict(meta.ID, e), "entry %d", i)
	}

	require.NoError(t, al.HydrateAgentHistoryFromTranscript(meta.ID))

	got := ag.Sessions.GetHistory(fmt.Sprintf("agent:%s:session:%s", agentID, meta.ID))

	// The FR-015 oracle: hydrated = user + assistant ONLY. Exactly two
	// messages; the thinking entry and the notice entry are absent, whatever
	// role values they carry.
	require.Len(t, got, 2, "hydrated history must be exactly [user, assistant]; got %+v", got)
	assert.Equal(t, "user", got[0].Role)
	assert.Equal(t, "what is the deploy host?", got[0].Content)
	assert.Equal(t, "assistant", got[1].Role)
	assert.Equal(t, "prod-east-1.example.com", got[1].Content)

	// And neither forbidden string reaches provider-bound history at all.
	for i, m := range got {
		assert.NotContains(t, m.Content, thinkingSentinel,
			"message %d: thinking text must never be mapped into provider-bound history (FR-015, ADR-095 D8.3)", i)
		assert.NotContains(t, m.Content, "did not respond",
			"message %d: the no-answer notice never enters model context (FR-018/T4)", i)
	}
}

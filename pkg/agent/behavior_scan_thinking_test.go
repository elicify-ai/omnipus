// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// WP-C RED tests for the behavior-scan reader (spec section 8.1 row for
// pkg/agent/behavior_scan.go: "Skip (explicit type filter) - behavior
// analysis reads answer content; the reader gains the same explicit
// type == thinking exclusion rather than relying on incidental filtering").
//
// Two oracles:
//  1. resolveBehaviorScanEntries (the reader that reads the transcript)
//     must not return thinking entries. RED today: it returns every entry
//     as-is - no type filter exists.
//  2. ScanBehaviorCriterionEntries (the pure scanner) is unaffected by the
//     presence of a thinking entry: verdicts are computed from tool-call
//     records only. Guard: expected GREEN in RED (already-compliant code).

func TestResolveBehaviorScanEntries_ExcludesThinkingEntries(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	cfg := &config.Config{}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	t.Cleanup(func() { al.Close() })

	store, err := session.NewUnifiedStore(t.TempDir())
	require.NoError(t, err)
	al.sharedSessionStore = store

	const agentID = "wpc-scan-agent"
	agentCfg := &config.AgentConfig{ID: agentID, Name: "WPC Scan"}
	ag := NewAgentInstance(agentCfg, &cfg.Agents.Defaults, cfg, &mockProvider{})
	require.NotNil(t, ag)
	ag.Sessions = store // GetAgentStore resolves the agent's own store
	al.registry.mu.Lock()
	al.registry.agents[agentID] = ag
	al.registry.mu.Unlock()

	meta, err := store.NewSession(session.SessionTypeChat, "web scan", agentID)
	require.NoError(t, err)

	now := time.Now().UTC()
	require.NoError(t, store.AppendTranscriptStrict(meta.ID, session.TranscriptEntry{
		Role: "user", Content: "run the scan turn", AgentID: agentID, Timestamp: now,
	}))
	require.NoError(t, store.AppendTranscriptStrict(meta.ID, session.TranscriptEntry{
		Type:         session.EntryTypeThinking,
		Role:         "assistant", // PLANTED - real entries carry no role
		Content:      "",
		ThinkingText: "scan-reader probe " + thinkingSentinel,
		AgentID:      agentID,
		Timestamp:    now.Add(time.Second),
	}))
	require.NoError(t, store.AppendTranscriptStrict(meta.ID, session.TranscriptEntry{
		Role: "assistant", Content: "scan answer", AgentID: agentID, Timestamp: now.Add(2 * time.Second),
	}))

	entries := al.resolveBehaviorScanEntries(JudgeCriteriaInput{
		Scope:           task.VerdictScopeGoal,
		GoalSessionID:   meta.ID,
		AssigneeAgentID: agentID,
	})
	require.NotEmpty(t, entries, "the reader must resolve the transcript entries (instrument check)")
	for i, e := range entries {
		assert.NotEqual(t, session.EntryTypeThinking, e.Type,
			"entry %d: the behavior-scan reader must exclude thinking entries (section 8.1: explicit type filter)", i)
		assert.NotContains(t, e.ThinkingText, thinkingSentinel, "entry %d", i)
	}
}

func TestScanBehaviorCriterionEntries_ThinkingEntryDoesNotPerturbVerdicts(t *testing.T) {
	base := []session.TranscriptEntry{
		{
			Role: "assistant", Timestamp: time.Now().UTC(),
			ToolCalls: []session.ToolCall{{ID: "tc1", Tool: "bash", Status: "success"}},
		},
	}
	withThinking := append(append([]session.TranscriptEntry{}, base...), session.TranscriptEntry{
		Type:         session.EntryTypeThinking,
		Role:         "", // real shape: no role
		Content:      "",
		ThinkingText: "hidden reasoning " + thinkingSentinel,
		Timestamp:    time.Now().UTC(),
	})

	criterion := BehaviorCriterion{Tool: "bash", MinCount: wpcIntPtr(1)}
	attemptStart := time.Now().UTC().Add(-time.Hour)

	gotWith := ScanBehaviorCriterionEntries(withThinking, criterion, attemptStart)
	gotWithout := ScanBehaviorCriterionEntries(base, criterion, attemptStart)

	assert.Equal(t, gotWithout.Met, gotWith.Met,
		"a thinking entry must not change the behavior verdict")
	assert.Equal(t, gotWithout.Observed, gotWith.Observed,
		"a thinking entry must not change the observed tool-call count")
	assert.Equal(t, gotWithout.Reason, gotWith.Reason,
		"a thinking entry must not change the scan reason")
}

func wpcIntPtr(i int) *int { return &i }

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
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// WP-C tests for the verifier readers (spec section 8.1 rows for
// pkg/agent/verifier_adjudication.go and pkg/agent/verifier_provenance.go:
// "Exclude from evidence (explicit change) - same class as judge evidence;
// analysis text must not become verification evidence").
//
// Guards: expected GREEN in RED. renderTranscriptEntriesForWindow reads
// Content and ToolCalls only (a thinking entry carries neither); the
// investigation-log builder reads ToolCalls only. The tests pin the outcome
// the section 8.1 explicit filter must preserve.

func TestRenderTranscriptEntriesForWindow_ThinkingNeverEntersEvidenceWindow(t *testing.T) {
	now := time.Now().UTC()
	msgs := renderTranscriptEntriesForWindow([]session.TranscriptEntry{
		{Role: "user", Content: "the real instruction", Timestamp: now},
		{
			Type:         session.EntryTypeThinking,
			Role:         "", // real shape: no role
			Content:      "",
			ThinkingText: "evidence-window probe " + thinkingSentinel,
			Timestamp:    now,
		},
		{Role: "assistant", Content: "the answer", Timestamp: now},
	})

	require.Len(t, msgs, 2, "the evidence window carries exactly [user, assistant]; got %+v", msgs)
	assert.Equal(t, "user", msgs[0].Role)
	assert.Equal(t, "the real instruction", msgs[0].Content)
	assert.Equal(t, "assistant", msgs[1].Role)
	assert.Equal(t, "the answer", msgs[1].Content)
	for i, m := range msgs {
		assert.NotContains(t, m.Content, thinkingSentinel,
			"message %d: thinking text must never enter verification evidence (section 8.1)", i)
	}
}

func TestBuildInvestigationLogFromJudgeTranscript_ThinkingNeverEntersInvestigationLog(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	cfg := &config.Config{}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	t.Cleanup(func() { al.Close() })

	store, err := session.NewUnifiedStore(t.TempDir())
	require.NoError(t, err)
	al.sharedSessionStore = store

	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "wpc-judge-agent")
	require.NoError(t, err)

	now := time.Now().UTC()
	require.NoError(t, store.AppendTranscriptStrict(meta.ID, session.TranscriptEntry{
		Role: "assistant", AgentID: "wpc-judge-agent", Timestamp: now,
		ToolCalls: []session.ToolCall{{ID: "tc1", Tool: "bash", Status: "success", Parameters: map[string]any{"path": "logs/x"}}},
	}))
	require.NoError(t, store.AppendTranscriptStrict(meta.ID, session.TranscriptEntry{
		Type:         session.EntryTypeThinking,
		Role:         "",
		Content:      "",
		ThinkingText: "investigation probe " + thinkingSentinel,
		AgentID:      "wpc-judge-agent",
		Timestamp:    now,
	}))

	calls := al.buildInvestigationLogFromJudgeTranscript("wpc-judge-agent", meta.ID)
	require.Len(t, calls, 1, "exactly the real tool call is in the investigation log; got %+v", calls)
	assert.Equal(t, "bash", calls[0].Tool)
	assert.Equal(t, "logs/x", calls[0].Target)
	assert.NotContains(t, calls[0].Tool, thinkingSentinel)
	assert.NotContains(t, calls[0].Target, thinkingSentinel)
}

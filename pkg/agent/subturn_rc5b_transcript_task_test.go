// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSpawnSubTurn_PersistsDelegatedTaskToChildTranscript is the regression
// coverage for RC-5b (ADR-057 UAT root-cause fix): the delegated task text
// (cfg.SystemPrompt) reaches the LLM as opts.UserMessage but, before this
// fix, was recorded ONLY in the child's ephemeral, in-memory session
// (agent.Sessions / newEphemeralSession) — never in the child's own durable
// transcript.jsonl. Because every OTHER writer of user-role transcript
// entries (channel ingress, scheduled/heartbeat runs, the websocket
// handler) is unreachable from a delegated sub-turn, a delegated child's
// durable transcript carried zero user messages, making it impossible to
// audit what the child was actually asked to do (observed live: 11 workers
// in one UAT session, all with empty transcripts).
//
// This test must FAIL on the pre-fix code (no writer ever appends a
// role:"user" entry to the child's own session) and PASS once spawnSubTurn
// writes that entry via parentTS.transcriptStore.AppendTranscriptStrict.
func TestSpawnSubTurn_PersistsDelegatedTaskToChildTranscript(t *testing.T) {
	al, _, _, provider, cleanup := newTestAgentLoop(t)
	_ = provider
	defer cleanup()

	store := al.GetSessionStore()
	require.NotNil(t, store, "test harness did not wire a shared session store")

	parentMeta, err := store.NewSession(session.SessionTypeChat, "test-channel", "main")
	require.NoError(t, err, "store.NewSession (parent)")
	parentSessionID := parentMeta.ID

	parentTS := &turnState{
		ctx:                 context.Background(),
		turnID:              "parent-T0-rc5b",
		depth:               0,
		childTurnIDs:        []string{},
		pendingResults:      make(chan *tools.ToolResult, 4),
		session:             &ephemeralSessionStore{},
		agent:               al.registry.GetDefaultAgent(),
		transcriptSessionID: parentSessionID,
		routingSessionID:    session.RoutingSessionID(parentSessionID),
		transcriptStore:     store,
	}

	spawnCtx := withSpawnToolCallID(context.Background(), "test-spawn-call-rc5b")

	const taskText = "Investigate the checkout failure in module X and report findings."
	// Pin the child id explicitly (mirrors TestSpawnSubTurn_TranscriptSessionIDIsChildsOwn_
	// RoutingSessionIDInheritsFromParent) so this test cannot collide with a
	// left-behind "subturn-N" child from an earlier test sharing the same
	// process-wide session store temp root.
	expectedChildID := "child-rc5b-" + parentSessionID
	cfg := SubTurnConfig{
		Model:             "gpt-4o-mini",
		Tools:             []tools.Tool{},
		SystemPrompt:      taskText,
		DelegateSessionID: expectedChildID,
	}

	result, err := spawnSubTurn(spawnCtx, al, parentTS, cfg)
	require.NoError(t, err)
	require.NotNil(t, result, "expected the child's turn to actually run and return a result")

	childEntries, err := store.ReadTranscript(expectedChildID)
	require.NoError(t, err)
	require.NotEmpty(t, childEntries, "the child's own transcript file must contain entries")

	var found bool
	gotEntries := make([]session.TranscriptEntry, 0, len(childEntries))
	for _, e := range childEntries {
		gotEntries = append(gotEntries, e)
		if e.Role == "user" && e.Content == taskText {
			found = true
		}
	}
	assert.True(t, found,
		"child's durable transcript must contain a role:\"user\" entry carrying the "+
			"delegated task text %q — got entries: %+v", taskText, gotEntries)
}

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// WP-C test for the repair reader (spec section 8.1 row for
// pkg/agent/repair.go: "Skip in repairability checks - thinking entries are
// not answer content; they replay untouched"). The pinned production symbol
// is pkg/agent/repair.go::loadTranscriptToolCalls.
//
// Guard: expected GREEN in RED - the index is built from entry.ToolCalls
// only, and a thinking entry carries no tool calls (C3 shape). The test
// pins the equivalence the section 8.1 row requires.

func TestLoadTranscriptToolCalls_ThinkingEntryDoesNotDisturbIndex(t *testing.T) {
	store, err := session.NewUnifiedStore(t.TempDir())
	require.NoError(t, err)
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "wpc-repair-agent")
	require.NoError(t, err)

	now := time.Now().UTC()
	base := []session.TranscriptEntry{
		{Role: "user", Content: "run the tool", Timestamp: now},
		{
			Role: "assistant", Timestamp: now,
			ToolCalls: []session.ToolCall{{ID: "call_wpc_1", Tool: "bash", Status: "success", Parameters: map[string]any{"cmd": "ls"}}},
		},
	}
	for i, e := range base {
		require.NoError(t, store.AppendTranscriptStrict(meta.ID, e), "entry %d", i)
	}
	without := loadTranscriptToolCalls(store, meta.ID)
	require.Len(t, without, 1, "instrument check: the baseline index holds the one real call")

	// Insert a thinking entry between the user and assistant entries, in a
	// second session, and require the identical index.
	meta2, err := store.NewSession(session.SessionTypeChat, "webchat", "wpc-repair-agent")
	require.NoError(t, err)
	withThinking := []session.TranscriptEntry{
		base[0],
		{
			Type:         session.EntryTypeThinking,
			Role:         "",
			Content:      "",
			ThinkingText: "repair probe " + thinkingSentinel,
			Timestamp:    now,
		},
		base[1],
	}
	for i, e := range withThinking {
		require.NoError(t, store.AppendTranscriptStrict(meta2.ID, e), "entry %d", i)
	}
	with := loadTranscriptToolCalls(store, meta2.ID)

	require.Len(t, with, 1, "a thinking entry adds nothing to the tool-call index")
	assert.Equal(t, without["call_wpc_1"].ID, with["call_wpc_1"].ID,
		"the index entry for the real call is unchanged by the thinking entry's presence")
	assert.NotContains(t, with["call_wpc_1"].Parameters, thinkingSentinel)
}

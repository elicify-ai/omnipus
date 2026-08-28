// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package session

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestUpdateToolCallProjections_BatchRewriteAndRevert covers the transcript
// half of ADR-066 FR-022 (T066-12): the D5 emptying pass marks several tool
// calls `emptied` in ONE rewrite, each record's `result` becomes the
// projected content (the recall mark), the previous state comes back so an
// aborted turn can put the transcript back, and an id that matches nothing
// is skipped without failing the batch.
func TestUpdateToolCallProjections_BatchRewriteAndRevert(t *testing.T) {
	store := newTestStore(t)

	meta, err := store.NewSession(SessionTypeChat, "", "test-agent")
	require.NoError(t, err)
	sid := meta.ID

	require.NoError(t, store.AppendTranscript(sid, TranscriptEntry{
		ID: "e1", Type: EntryTypeToolCall, AgentID: "jim",
		ToolCalls: []ToolCall{
			{ID: "c1", Tool: "bash", Status: "success", Result: map[string]any{"text": "full one"}},
		},
	}))
	require.NoError(t, store.AppendTranscript(sid, TranscriptEntry{
		ID: "e2", Type: EntryTypeToolCall, AgentID: "jim",
		ToolCalls: []ToolCall{
			{ID: "c2", Tool: "read_file", Status: "error", Error: "boom"}, // Result nil: reason lives in Error
			{ID: "c3", Tool: "read_file", Status: "success", Result: map[string]any{"text": "full three"}},
		},
	}))

	prev, err := store.UpdateToolCallProjections(sid, []ToolCallProjectionUpdate{
		{ToolCallID: "c1", ContentState: "emptied", Result: map[string]any{"text": "[mark c1]"}},
		{ToolCallID: "c2", ContentState: "emptied", Result: map[string]any{"text": "[mark c2]"}},
		{ToolCallID: "missing", ContentState: "emptied", Result: map[string]any{"text": "never"}},
	})
	require.NoError(t, err)
	require.Len(t, prev, 2, "one previous-state row per record that was found; the unknown id is skipped")
	prevByID := map[ToolCallID]ToolCallProjectionUpdate{}
	for _, p := range prev {
		prevByID[p.ToolCallID] = p
	}
	assert.Equal(t, "", prevByID["c1"].ContentState)
	assert.Equal(t, "full one", prevByID["c1"].Result["text"])
	assert.Nil(t, prevByID["c2"].Result, "a failed call's previous Result is nil — must round-trip as nil")

	entries, err := store.ReadTranscript(sid)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	assert.Equal(t, "emptied", entries[0].ToolCalls[0].ContentState)
	assert.Equal(t, "[mark c1]", entries[0].ToolCalls[0].Result["text"])
	assert.Equal(t, "success", entries[0].ToolCalls[0].Status, "status untouched")
	assert.Equal(t, "emptied", entries[1].ToolCalls[0].ContentState)
	assert.Equal(t, "[mark c2]", entries[1].ToolCalls[0].Result["text"])
	assert.Equal(t, "boom", entries[1].ToolCalls[0].Error, "error text untouched")
	assert.Equal(t, "", entries[1].ToolCalls[1].ContentState, "sibling on the same entry untouched")
	assert.Equal(t, "full three", entries[1].ToolCalls[1].Result["text"])

	// Revert by feeding the previous rows back (restoreSession's path).
	_, err = store.UpdateToolCallProjections(sid, prev)
	require.NoError(t, err)
	entries, err = store.ReadTranscript(sid)
	require.NoError(t, err)
	assert.Equal(t, "", entries[0].ToolCalls[0].ContentState)
	assert.Equal(t, "full one", entries[0].ToolCalls[0].Result["text"])
	assert.Equal(t, "", entries[1].ToolCalls[0].ContentState)
	assert.Nil(t, entries[1].ToolCalls[0].Result, "nil Result CLEARS the field on this method")

	// Empty batch is a no-op, not an error.
	prev, err = store.UpdateToolCallProjections(sid, nil)
	require.NoError(t, err)
	assert.Nil(t, prev)
}

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build goolm && stdjson

// Regression tests for the ArchiveLine stability invariant on the sub-turn
// session store (ADR-066 FR-019).
//
// memory.ProjectionKey.ArchiveLine is "the zero-based archive line", and
// every producer derives it from a ReadArchive index. memory.JSONLStore
// upholds the implied invariant — an index never moves, because the archive
// is append-only and TruncateHistory only advances Skip. ephemeralSessionStore,
// which EVERY delegated sub-turn uses, truncates from the FRONT in two places
// (TruncateHistory and the maxEphemeralHistorySize ring), shifting every
// surviving message's index down while the recorded keys stayed put. The
// capped/emptied state then addressed the wrong message — or nothing at all —
// so the full, uncapped result went back into the request. After a provider
// context overflow that meant the RETRY sent more than the attempt that had
// just overflowed.

package agent

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/memory"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// projectionLineFor returns the recorded archive line for toolCallID, or -1.
func projectionLineFor(pm memory.ProjectionMeta, toolCallID string) int {
	for k := range pm.Entries {
		if k.ToolCallID == toolCallID {
			return k.ArchiveLine
		}
	}
	return -1
}

// archiveIndexOf returns the index of the tool message answering toolCallID
// in the store's current ReadArchive view, or -1.
func archiveIndexOf(t *testing.T, store session.SessionStore, toolCallID string) int {
	t.Helper()
	archive, err := store.ReadArchive(context.Background(), "sub")
	require.NoError(t, err)
	for i := range archive {
		if archive[i].Role == "tool" && archive[i].ToolCallID == toolCallID {
			return i
		}
	}
	return -1
}

func TestEphemeralSessionStore_ProjectionKeySurvivesFrontTruncation(t *testing.T) {
	store := newEphemeralSession(nil)

	for i := 0; i < 20; i++ {
		store.AddMessage("sub", "user", fmt.Sprintf("filler %d", i))
	}
	store.AddFullMessage("sub", providers.Message{
		Role:      "assistant",
		ToolCalls: []providers.ToolCall{{ID: "call_big", Name: "big_tool"}},
	})
	store.AddFullMessage("sub", providers.Message{
		Role: "tool", ToolCallID: "call_big", Content: "the full, uncapped result",
	})

	// The choke point records the line as len(ReadArchive)-1.
	line := archiveIndexOf(t, store, "call_big")
	require.GreaterOrEqual(t, line, 0)
	store.SetProjectionState("sub",
		memory.ProjectionKey{ToolCallID: "call_big", ArchiveLine: line}, memory.ProjectionCapped)
	require.Equal(t, line, projectionLineFor(store.Projection("sub"), "call_big"))

	// windowTrim's retry path drops the oldest messages.
	store.TruncateHistory("sub", 5)

	newLine := archiveIndexOf(t, store, "call_big")
	require.GreaterOrEqual(t, newLine, 0, "the result is still in the ring")
	require.NotEqual(t, line, newLine, "precondition: front truncation moved the index")

	assert.Equal(t, newLine, projectionLineFor(store.Projection("sub"), "call_big"),
		"the recorded projection line must follow its message across a front truncation — "+
			"otherwise the lookup misses and the FULL uncapped result goes back into the request")
}

func TestEphemeralSessionStore_ProjectionKeySurvivesRingWrap(t *testing.T) {
	store := newEphemeralSession(nil)

	store.AddFullMessage("sub", providers.Message{
		Role:      "assistant",
		ToolCalls: []providers.ToolCall{{ID: "call_ring", Name: "big_tool"}},
	})
	store.AddFullMessage("sub", providers.Message{
		Role: "tool", ToolCallID: "call_ring", Content: "the full, uncapped result",
	})
	line := archiveIndexOf(t, store, "call_ring")
	require.GreaterOrEqual(t, line, 0)
	store.SetProjectionState("sub",
		memory.ProjectionKey{ToolCallID: "call_ring", ArchiveLine: line}, memory.ProjectionCapped)

	// Wrap the ring by exactly one message.
	for i := 0; i < maxEphemeralHistorySize-1; i++ {
		store.AddMessage("sub", "user", fmt.Sprintf("filler %d", i))
	}

	newLine := archiveIndexOf(t, store, "call_ring")
	require.GreaterOrEqual(t, newLine, 0, "the result is still in the ring")
	require.NotEqual(t, line, newLine, "precondition: one wrap shifted every key by one")

	assert.Equal(t, newLine, projectionLineFor(store.Projection("sub"), "call_ring"),
		"one ring wrap must not re-inflate every capped result in the sub-turn")
}

// TestEphemeralSessionStore_DroppedEntriesAreNotReported — a key whose
// message has left the ring must not come back pointing at someone else's
// line.
func TestEphemeralSessionStore_DroppedEntriesAreNotReported(t *testing.T) {
	store := newEphemeralSession(nil)
	store.AddFullMessage("sub", providers.Message{
		Role:      "assistant",
		ToolCalls: []providers.ToolCall{{ID: "call_gone", Name: "big_tool"}},
	})
	store.AddFullMessage("sub", providers.Message{Role: "tool", ToolCallID: "call_gone", Content: "x"})
	store.SetProjectionState("sub",
		memory.ProjectionKey{ToolCallID: "call_gone", ArchiveLine: 1}, memory.ProjectionCapped)

	for i := 0; i < 5; i++ {
		store.AddMessage("sub", "user", "later")
	}
	store.TruncateHistory("sub", 3) // drops the assistant + tool pair

	assert.Equal(t, -1, archiveIndexOf(t, store, "call_gone"), "precondition: the result left the ring")
	assert.Equal(t, -1, projectionLineFor(store.Projection("sub"), "call_gone"),
		"an entry whose message is gone must be omitted, never re-pointed at a surviving line")
}

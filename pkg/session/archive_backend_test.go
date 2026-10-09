// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Tests for the archive-backed session store (archive_backend.go) — the
// SessionStore + ContextWindowStore implementation over ArchiveDayStore +
// AddressedWindow. Oracles are the interfaces' existing JSONL semantics
// (session_store.go, pkg/memory/window.go) plus the spec's FR-006/FR-047 rules,
// never the implementation. This slice is NOT wired into UnifiedStore yet.
package session

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/memory"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/stretchr/testify/require"
)

func newTestBackend(t *testing.T) *archiveBackend {
	t.Helper()
	return newArchiveBackend(t.TempDir())
}

func userMsg(s string) providers.Message { return providers.Message{Role: "user", Content: s} }
func asstMsg(s string) providers.Message { return providers.Message{Role: "assistant", Content: s} }

// Append then read: a simple history round-trips through the archive.
func TestArchiveBackend_AppendAndGetHistory(t *testing.T) {
	b := newTestBackend(t)
	const key = "sess-1"
	ctx := context.Background()

	snap, err := b.AppendWindowMessage(ctx, key, userMsg("hello"))
	require.NoError(t, err)
	require.Equal(t, 1, snap.State.Count, "append must advance the archive cursor")

	_, err = b.AppendWindowMessage(ctx, key, asstMsg("hi there"))
	require.NoError(t, err)

	got := b.GetHistory(key)
	require.Len(t, got, 2)
	require.Equal(t, "hello", got[0].Content)
	require.Equal(t, "hi there", got[1].Content)
}

// SnapshotWindow exposes the JSONL-shaped State + Archive + Retracted.
func TestArchiveBackend_SnapshotShape(t *testing.T) {
	b := newTestBackend(t)
	const key = "sess-1"
	ctx := context.Background()
	for _, m := range []providers.Message{userMsg("m1"), asstMsg("m2"), userMsg("m3")} {
		require.NoError(t, b.appendMessage(key, m))
	}
	snap, err := b.SnapshotWindow(ctx, key)
	require.NoError(t, err)
	require.Equal(t, 3, snap.State.Count)
	require.Equal(t, 0, snap.State.Skip)
	require.Len(t, snap.Archive, 3)
	require.Equal(t, "m1", snap.Archive[0].Message.Content)
}

// FR-006: RollbackWindow must RESTORE the turn-start view and NEVER rewrite
// retained bytes — the aborted bytes stay on disk and stay excluded from the
// window. Proven on the raw partition file.
func TestArchiveBackend_RollbackIsNonDestructive(t *testing.T) {
	b := newTestBackend(t)
	const key = "sess-1"
	ctx := context.Background()
	require.NoError(t, b.appendMessage(key, userMsg("m1")))
	require.NoError(t, b.appendMessage(key, asstMsg("m2")))
	snap, err := b.SnapshotWindow(ctx, key)
	require.NoError(t, err)
	require.Equal(t, 2, snap.State.Count, "instrument control: snapshot sees 2 lines")

	require.NoError(t, b.appendMessage(key, userMsg("m3")))
	require.NoError(t, b.appendMessage(key, asstMsg("m4")))

	rawPath := filepath.Join(b.baseDir, key, "u2archive", "current.jsonl")
	before, err := os.ReadFile(rawPath)
	require.NoError(t, err)

	require.NoError(t, b.RollbackWindow(ctx, key, snap.State))

	after, err := os.ReadFile(rawPath)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(string(after), string(before)),
		"FR-006: rollback must never rewrite retained archive bytes")

	// The window a request is built from holds exactly the turn-start messages.
	got := b.GetHistory(key)
	require.Len(t, got, 2, "the aborted span must be excluded from the model view")
	require.Equal(t, "m1", got[0].Content)
	require.Equal(t, "m2", got[1].Content)

	// The aborted bytes remain recallable (ReadArchive ignores Skip/exclusion).
	all, err := b.ReadArchive(ctx, key)
	require.NoError(t, err)
	require.Len(t, all, 4, "the aborted bytes stay on disk for recall")
}

// TruncateHistory keeps only the last N messages.
func TestArchiveBackend_TruncateKeepsLast(t *testing.T) {
	b := newTestBackend(t)
	const key = "sess-1"
	for _, m := range []providers.Message{userMsg("m1"), asstMsg("m2"), userMsg("m3"), asstMsg("m4")} {
		require.NoError(t, b.appendMessage(key, m))
	}
	b.TruncateHistory(key, 2)
	got := b.GetHistory(key)
	require.Len(t, got, 2)
	require.Equal(t, "m3", got[0].Content)
	require.Equal(t, "m4", got[1].Content)
}

// FR-047: SetHistory fills an EMPTY archive and refuses a non-empty one.
func TestArchiveBackend_SetHistoryFirstFillOnly(t *testing.T) {
	b := newTestBackend(t)
	const key = "sess-1"
	b.SetHistory(key, []providers.Message{userMsg("a"), asstMsg("b")})
	require.Len(t, b.GetHistory(key), 2)

	// Second fill is refused: the history is unchanged.
	b.SetHistory(key, []providers.Message{userMsg("X")})
	got := b.GetHistory(key)
	require.Len(t, got, 2, "SetHistory must refuse a non-empty archive")
	require.Equal(t, "a", got[0].Content)
}

// Projection state round-trips and survives a fresh backend over the same dir.
func TestArchiveBackend_ProjectionRoundTripPersists(t *testing.T) {
	b := newTestBackend(t)
	const key = "sess-1"
	require.NoError(t, b.appendMessage(key, asstMsg("m1")))
	pk := memory.ProjectionKey{ToolCallID: "call_0", ArchiveLine: 0}
	b.SetProjectionState(key, pk, memory.ProjectionEmptied)
	b.MarkHydrated(key)

	fresh := newArchiveBackend(b.baseDir)
	pm := fresh.Projection(key)
	require.Equal(t, memory.ProjectionEmptied, pm.Entries[pk])
	require.True(t, pm.Hydrated)
}

// The raw-range scan yields the LITERAL model_message JSON and never the
// private envelope (no source/model_ref/marks leakage).
func TestArchiveBackend_ScanArchiveRangeExcludesPrivateEnvelope(t *testing.T) {
	b := newTestBackend(t)
	const key = "sess-1"
	ctx := context.Background()
	require.NoError(t, b.appendMessage(key, providers.Message{Role: "user", Content: "SECRET-BODY"}))

	var sawModel bool
	err := b.ScanArchiveRange(ctx, key, 0, 0, func(idx int, raw []byte, _ memory.ArchivedMessage) error {
		require.Equal(t, 0, idx)
		require.Contains(t, string(raw), "SECRET-BODY", "the literal model_message value is quoted")
		require.NotContains(t, string(raw), `"source"`, "private source must not leak into raw recall")
		require.NotContains(t, string(raw), `"model_ref"`, "no model_ref in a raw payload line")
		sawModel = true
		return nil
	})
	require.NoError(t, err)
	require.True(t, sawModel)
}

// ScanEvictedArchive reports the persisted Skip and streams only the prefix.
func TestArchiveBackend_ScanEvictedArchive(t *testing.T) {
	b := newTestBackend(t)
	const key = "sess-1"
	ctx := context.Background()
	for _, m := range []providers.Message{userMsg("m1"), asstMsg("m2"), userMsg("m3")} {
		require.NoError(t, b.appendMessage(key, m))
	}
	b.TruncateHistory(key, 1) // Skip advances to 2

	var idxs []int
	skip, err := b.ScanEvictedArchive(ctx, key, func(idx int, _ []byte, _ memory.ArchivedMessage) error {
		idxs = append(idxs, idx)
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, 2, skip)
	require.Equal(t, []int{0, 1}, idxs)
}

// A tool result appended through the SessionStore write surface (which carries
// no producing-assistant identity) is joined to its assistant occurrence from
// the archive itself, so the archive's tool_result_for rule is satisfied.
func TestArchiveBackend_ToolResultJoinsItsAssistantOccurrence(t *testing.T) {
	b := newTestBackend(t)
	const key = "sess-1"
	ctx := context.Background()
	asst := providers.Message{Role: "assistant", Content: "calling", ToolCalls: []providers.ToolCall{{
		ID: "call_0", Type: "function", Function: &providers.FunctionCall{Name: "read", Arguments: "{}"},
	}}}
	require.NoError(t, b.appendMessage(key, asst))
	require.NoError(t, b.appendMessage(key, providers.Message{Role: "tool", ToolCallID: "call_0", Content: "result"}))

	all, err := b.ReadArchive(ctx, key)
	require.NoError(t, err)
	require.Len(t, all, 2, "both the assistant call and its result are archived")
	require.Equal(t, "call_0", all[1].Message.ToolCallID)
}

// A LIVE orphan tool result (no matching assistant call) is tolerated with an
// UNPROVEN producer (empty assistant id) — the pre-cutover runtime stored such a
// result, and the FR-006 rollback tests append one. (A CONVERTED result never
// carries an empty producer; CONV refuses unprovable joins.)
func TestArchiveBackend_OrphanToolResultIsStoredWithUnprovenProducer(t *testing.T) {
	b := newTestBackend(t)
	const key = "sess-1"
	ctx := context.Background()
	require.NoError(t, b.appendMessage(key, providers.Message{Role: "tool", ToolCallID: "call_missing", Content: "orphan"}))
	all, err := b.ReadArchive(ctx, key)
	require.NoError(t, err)
	require.Len(t, all, 1, "the live orphan result is stored")
	require.Equal(t, "call_missing", all[0].Message.ToolCallID)
}

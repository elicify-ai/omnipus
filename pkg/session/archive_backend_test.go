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

// appendModelMsg appends one model message through the checked model seam (the
// dense AppendWindowMessage the callers migrated off is deleted, DEL-12) and
// returns the bounded view after the append.
func appendModelMsg(b *archiveBackend, ctx context.Context, key string, msg providers.Message) (WindowView, error) {
	_, view, err := b.AppendModelMessage(ctx, key, ModelAppend{
		Message: msg, ViewMembership: ViewMembershipModel, Source: EntrySource{Kind: sourceKindForRole(msg.Role)},
	})
	return view, err
}

// Append then read: a simple history round-trips through the archive.
func TestArchiveBackend_AppendAndGetHistory(t *testing.T) {
	b := newTestBackend(t)
	const key = "sess-1"
	ctx := context.Background()

	snap, err := appendModelMsg(b, ctx, key, userMsg("hello"))
	require.NoError(t, err)
	require.Equal(t, 1, snap.State.Count, "append must advance the archive cursor")

	_, err = appendModelMsg(b, ctx, key, asstMsg("hi there"))
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
	snap, err := b.WindowView(ctx, key)
	require.NoError(t, err)
	require.Equal(t, 3, snap.State.Count)
	require.Equal(t, 0, snap.State.Skip)
	require.Len(t, snap.Live, 3)
	require.Equal(t, "m1", snap.Live[0].Message.Content)
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
	snap, err := b.WindowView(ctx, key)
	require.NoError(t, err)
	require.Equal(t, 2, snap.State.Count, "instrument control: snapshot sees 2 lines")

	require.NoError(t, b.appendMessage(key, userMsg("m3")))
	require.NoError(t, b.appendMessage(key, asstMsg("m4")))

	rawPath := filepath.Join(b.baseDir, key, "transcript.jsonl")
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

// The window-trim flow keeps only the last N messages in the LIVE window by
// advancing Skip (CommitWindow), and never deletes the earlier lines — they
// stay on disk for recall. Supersedes the old TruncateHistory call: that
// in-place rewrite API is deleted (DEL-12), the invariant is unchanged.
func TestArchiveBackend_CommitWindowTrimKeepsLastInTheWindow(t *testing.T) {
	b := newTestBackend(t)
	const key = "sess-1"
	ctx := context.Background()
	for _, m := range []providers.Message{userMsg("m1"), asstMsg("m2"), userMsg("m3"), asstMsg("m4")} {
		require.NoError(t, b.appendMessage(key, m))
	}
	snap, err := b.WindowView(ctx, key)
	require.NoError(t, err)
	before := snap.State
	after := before
	after.Skip = 2
	require.NoError(t, b.CommitWindow(ctx, key, before, after))

	got := b.GetHistory(key)
	require.Len(t, got, 2)
	require.Equal(t, "m3", got[0].Content)
	require.Equal(t, "m4", got[1].Content)

	all, err := b.ReadArchive(ctx, key)
	require.NoError(t, err)
	require.Len(t, all, 4, "the trimmed prefix stays on disk for recall (FR-006)")
}

// FR-047/SetHistory is deleted with the legacy model-store surface (DEL-10/12);
// its "first fill only" invariant is superseded by the append-only archive, so
// the old TestArchiveBackend_SetHistoryFirstFillOnly has no new-path equivalent.

// Projection state round-trips and survives a fresh backend over the same dir.
func TestArchiveBackend_ProjectionRoundTripPersists(t *testing.T) {
	b := newTestBackend(t)
	const key = "sess-1"
	require.NoError(t, b.appendMessage(key, asstMsg("m1")))
	pk := memory.ProjectionKey{ToolCallID: "call_0", ArchiveLine: 0}
	b.SetProjectionState(key, pk, memory.ProjectionEmptied)

	fresh := newArchiveBackend(b.baseDir)
	pm := fresh.Projection(key)
	require.Equal(t, memory.ProjectionEmptied, pm.Entries[pk])
}

// Negative control (slice 5b): the raw-range recall boundary yields ONLY the
// literal model_message JSON — never source, tool_result_for, model_origin,
// model_ref or any disk mark. The canonical positive control is that the model
// content and the exact argument string ARE present.
func TestArchiveBackend_RawRecallExcludesEveryPrivateField(t *testing.T) {
	base := t.TempDir()
	us, err := NewUnifiedStore(base)
	require.NoError(t, err)
	t.Cleanup(func() { _ = us.Close() })
	const key = "sess-raw"
	ctx := context.Background()
	us.AddFullMessage(key, providers.Message{Role: "assistant", Content: "calling",
		ToolCalls: []providers.ToolCall{{ID: "c", Type: "function",
			Function: &providers.FunctionCall{Name: "read", Arguments: `{"path":"SECRET_ARG"}`}}}})
	// The tool result joins its issuing assistant (a private tool_result_for that
	// must not leak into raw recall).
	us.AddFullMessage(key, providers.Message{Role: "tool", ToolCallID: "c", Content: "SECRET_RESULT"})

	seen := 0
	err = us.ScanArchiveRange(ctx, key, 0, 1, func(idx int, raw []byte, _ memory.ArchivedMessage) error {
		s := string(raw)
		for _, priv := range []string{`"source"`, `"model_ref"`, `"tool_result_for"`, `"model_origin"`,
			`"partition_key"`, `"byte_offset"`, `"view_membership"`} {
			require.NotContains(t, s, priv, "private field %s leaked into raw recall", priv)
		}
		if idx == 0 {
			require.Contains(t, s, "SECRET_ARG", "the exact argument string is quoted (positive control)")
		} else {
			require.Contains(t, s, "SECRET_RESULT")
		}
		seen++
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, 2, seen)

	// Byte-exact: the emitted raw value equals the stored model_message literal.
	rawFile, ferr := os.ReadFile(filepath.Join(base, key, "transcript.jsonl"))
	require.NoError(t, ferr)
	stored := literalModelMessage([]byte(strings.SplitN(string(rawFile), "\n", 2)[0]))
	var emitted []byte
	require.NoError(t, us.ScanArchiveRange(ctx, key, 0, 0, func(_ int, raw []byte, _ memory.ArchivedMessage) error {
		emitted = append([]byte(nil), raw...)
		return nil
	}))
	require.Equal(t, stored, emitted, "raw recall emits the stored model_message literal unchanged")
}

// ScanEvictedArchive reports the persisted Skip and streams only the prefix.
func TestArchiveBackend_ScanEvictedArchive(t *testing.T) {
	b := newTestBackend(t)
	const key = "sess-1"
	ctx := context.Background()
	for _, m := range []providers.Message{userMsg("m1"), asstMsg("m2"), userMsg("m3")} {
		require.NoError(t, b.appendMessage(key, m))
	}
	snap, err := b.WindowView(ctx, key)
	require.NoError(t, err)
	before := snap.State
	after := before
	after.Skip = 2 // advance Skip to 2, as the window trim does
	require.NoError(t, b.CommitWindow(ctx, key, before, after))

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

// An ORPHAN tool result (no matching assistant call) is refused: every tool
// result must name the assistant occurrence that issued its call (Decision A/B,
// FR-006). The runtime has no orphan producer; the abort fixtures were corrected
// to carry a valid call/result pair.
func TestArchiveBackend_OrphanToolResultIsRefused(t *testing.T) {
	b := newTestBackend(t)
	const key = "sess-1"
	err := b.appendMessage(key, providers.Message{Role: "tool", ToolCallID: "call_missing", Content: "orphan"})
	require.Error(t, err, "an orphan tool result must be refused")
	require.Empty(t, b.GetHistory(key), "nothing was written for the refused orphan")
}

// The model archive is keyed by the immutable OWNING session id, not the agent
// routing key (Decision D "Identity"), so it lives beside the chat transcript.
func TestArchiveBackend_RoutingKeyResolvesToOwningSession(t *testing.T) {
	b := newTestBackend(t)
	const routing = "agent:mia:session:sess-owner-1"
	require.NoError(t, b.appendMessage(routing, userMsg("hi")))

	require.FileExists(t, filepath.Join(b.baseDir, "sess-owner-1", "transcript.jsonl"),
		"the archive lands at the owning session's transcript path")
	_, err := os.Stat(filepath.Join(b.baseDir, "agent:mia:session:sess-owner-1"))
	require.True(t, os.IsNotExist(err), "no stray routing-key directory is created")
	require.Len(t, b.GetHistory(routing), 1, "reads through the routing key resolve to the same store")
}

// Deleting a session removes its model content too (no private data survives the
// chat's deletion) — the owning-session keying makes this natural.
func TestUnifiedStore_DeleteSessionRemovesModelArchive(t *testing.T) {
	base := t.TempDir()
	us, err := NewUnifiedStore(base)
	require.NoError(t, err)
	t.Cleanup(func() { _ = us.Close() })
	const id = "sess-del"
	us.AddMessage("agent:mia:session:"+id, "user", "hello")
	archivePath := filepath.Join(base, id, "transcript.jsonl")
	require.FileExists(t, archivePath)

	require.NoError(t, us.DeleteSession(id))
	_, err = os.Stat(archivePath)
	require.True(t, os.IsNotExist(err), "the model archive is removed with the session")
}

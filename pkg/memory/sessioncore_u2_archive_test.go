// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// RED pack, session-core U2 (one append-only archive), pkg/memory slice.
//
// Spec: docs/internal/specs/session-core-spec.md FR-005, FR-006 (DEL-12).
// ADR: docs/internal/architecture/ADR-20261006-session-core-with-an-agent-address-book.md.
// Every expected value below comes from the spec, never from the current code.
// These drive the EXISTING JSONLStore seam (the model archive the spec keeps in
// `.context/`); the new behaviour U2 must show is that the archive is append-only.

package memory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// FR-006: "Clear/correction/rollback/projection MUST append effects or move
// view/window metadata, never rewrite retained bytes." DEL-12 deletes
// RollbackWindow's destructive whole-file rewrite.
//
// Oracle: the FR-006 sentence, not the implementation. Property under test:
// the RAW on-disk archive keeps every appended record after a rollback to an
// earlier cursor — a rollback moves the window metadata, it does not truncate
// the retained bytes. The current RollbackWindow rewrites (truncates) the file,
// which is exactly what this asserts against.
func TestSessionCoreU2_RollbackNeverRewritesRetainedArchiveBytes(t *testing.T) {
	dir := t.TempDir()
	store, err := NewJSONLStore(dir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	const key = "sess-u2-rollback"

	// Four records land; the first two are snapshotted as the rollback target.
	require.NoError(t, store.AddMessage(ctx, key, "user", "m1"))
	require.NoError(t, store.AddMessage(ctx, key, "assistant", "m2"))
	snap, err := store.SnapshotWindow(ctx, key)
	require.NoError(t, err)
	require.Equal(t, 2, snap.State.Count, "instrument control: snapshot sees 2 records")

	require.NoError(t, store.AddMessage(ctx, key, "user", "m3"))
	require.NoError(t, store.AddMessage(ctx, key, "assistant", "m4"))

	rawPath := filepath.Join(dir, key+".jsonl")
	before, err := os.ReadFile(rawPath)
	require.NoError(t, err)
	require.Equal(t, 4, lineCount(before), "instrument control: 4 records are on disk before rollback")

	require.NoError(t, store.RollbackWindow(ctx, key, snap.State))

	after, err := os.ReadFile(rawPath)
	require.NoError(t, err)

	// The retained bytes survive byte-for-byte (append-only).
	require.True(t, strings.HasPrefix(string(after), string(before)),
		"FR-006: rollback must never rewrite retained archive bytes; before=%q after=%q", string(before), string(after))
	require.GreaterOrEqual(t, lineCount(after), lineCount(before),
		"FR-006: rollback may only append; the archive must not lose records (before=%d after=%d lines)",
		lineCount(before), lineCount(after))

	// Control: the store stays readable after the rollback — the effect was
	// applied through the window metadata, not by corrupting the archive.
	_, err = store.SnapshotWindow(ctx, key)
	require.NoError(t, err, "control: the window reads back cleanly after a rollback")
}

func lineCount(b []byte) int {
	return strings.Count(strings.TrimSuffix(string(b), "\n"), "\n") + 1
}

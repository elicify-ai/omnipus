// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// requirePOSIXNonRootOrFail guards the chmod-injection tests below. Windows has no
// POSIX permission bits, so it skips with its reason. Root IS NOT skipped
// (R2-X-qa-11): a root run bypasses file modes, so the injected failure never
// happens and the test would pass or skip while proving nothing. Both CI surfaces
// (runci.sh's re-exec as `ci`, GitHub's runner user) are non-root, so this only
// ever fires on a developer's root shell, where it says exactly what to do.
func requirePOSIXNonRootOrFail(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("needs POSIX file permissions")
	}
	if os.Geteuid() == 0 {
		t.Fatal("this test injects a failure with chmod, which root bypasses; run it as an unprivileged user " +
			"(a skip here would hide the guarded /clear rollback behind a green run)")
	}
}

// Delta review R1 #12: /clear is all or nothing. If the divider cannot be
// recorded the window move is undone and the error says nothing was cleared;
// the old code moved the window first and left it cleared with no divider.
func TestClearConversation_MarkerFailureUndoesTheWindowMove(t *testing.T) {
	requirePOSIXNonRootOrFail(t)
	al, inst, id := clearFixture(t, session.SessionTypeChat)
	store := al.GetSessionStore()
	transcript := filepath.Join(store.BaseDir(), id, "transcript.jsonl")
	require.NoError(t, os.Chmod(transcript, 0o400)) // readable (the window reads it), not appendable
	t.Cleanup(func() { _ = os.Chmod(transcript, 0o600) })

	err := al.clearConversation(context.Background(), inst, id, id)
	require.Error(t, err, "the divider could not be recorded")
	assert.Contains(t, err.Error(), "nothing was cleared")

	require.NoError(t, os.Chmod(transcript, 0o600))
	assert.Len(t, inst.Sessions.GetHistory(id), 2, "the failed /clear left the model window exactly as it was")
	view, verr := store.WindowView(context.Background(), id)
	require.NoError(t, verr)
	assert.Zero(t, view.State.Skip)
	assert.Zero(t, view.State.ClearFloor)
}

// Delta review R1 #8: a /clear on an already-empty window (retention emptied it,
// nothing new arrived) still drops the retention notice. Cursor and floor do not
// move in that case, so the clear itself must say so.
func TestClearSessionWindow_DropsTheExpiryNoticeEvenWhenNothingMoves(t *testing.T) {
	al, inst, id := clearFixture(t, session.SessionTypeChat)
	store := al.GetSessionStore()
	require.NoError(t, clearSessionWindow(inst.Sessions, id)) // first clear: window now empty, floor == count

	metaPath := filepath.Join(store.BaseDir(), id, "backend_meta.json")
	raw, err := os.ReadFile(metaPath)
	require.NoError(t, err)
	var meta map[string]any
	require.NoError(t, json.Unmarshal(raw, &meta))
	meta["expiry_note"] = "Retention expired all remaining model content for this conversation; the context window is now empty."
	raw, err = json.Marshal(meta)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(metaPath, raw, 0o600))
	view, err := store.WindowView(context.Background(), id)
	require.NoError(t, err)
	require.NotEmpty(t, view.ExpiryNote, "precondition: the notice is in place on an empty window")

	require.NoError(t, clearSessionWindow(inst.Sessions, id)) // a second /clear, no new content
	view, err = store.WindowView(context.Background(), id)
	require.NoError(t, err)
	assert.Empty(t, view.ExpiryNote, "a /clear starts over: the stale retention notice is gone")
}

// S1-10: a /clear that fails and is undone puts EVERYTHING back, including the
// retention notice the clear dropped; otherwise the agent is never told what
// retention expired.
func TestClearConversation_MarkerFailureAlsoRestoresTheRetentionNotice(t *testing.T) {
	requirePOSIXNonRootOrFail(t)
	al, inst, id := clearFixture(t, session.SessionTypeChat)
	store := al.GetSessionStore()
	const note = "Retention expired older conversation content for this conversation."
	metaPath := filepath.Join(store.BaseDir(), id, "backend_meta.json")
	raw, err := os.ReadFile(metaPath)
	require.NoError(t, err)
	var meta map[string]any
	require.NoError(t, json.Unmarshal(raw, &meta))
	meta["expiry_note"] = note
	raw, err = json.Marshal(meta)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(metaPath, raw, 0o600))

	transcript := filepath.Join(store.BaseDir(), id, "transcript.jsonl")
	require.NoError(t, os.Chmod(transcript, 0o400))
	t.Cleanup(func() { _ = os.Chmod(transcript, 0o600) })
	require.Error(t, al.clearConversation(context.Background(), inst, id, id))
	require.NoError(t, os.Chmod(transcript, 0o600))

	view, verr := store.WindowView(context.Background(), id)
	require.NoError(t, verr)
	assert.Equal(t, note, view.ExpiryNote, "the failed /clear was undone, so the retention notice must be back")
}

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

// Delta review R1 #12: /clear is all or nothing. If the divider cannot be
// recorded the window move is undone and the error says nothing was cleared;
// the old code moved the window first and left it cleared with no divider.
func TestClearConversation_MarkerFailureUndoesTheWindowMove(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs POSIX file permissions and a non-root user")
	}
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

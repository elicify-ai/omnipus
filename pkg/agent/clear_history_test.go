// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Regression test for /new (alias /clear) against a REAL archive-backed
// session store.
//
// The bug: rt.ClearHistory cleared by calling SetHistory(key, []). ADR-066
// FR-047 narrowed memory.JSONLStore.SetHistory to a first-fill primitive that
// REFUSES a non-empty archive with ErrArchiveNotEmpty, and
// session.SessionWriter.SetHistory is fire-and-forget — the refusal was
// swallowed into a slog.Error. So on CLI, Telegram, Discord and every other
// surface, /clear replied "Chat history cleared!" and the next message was
// answered with the entire prior conversation still in the window. The only
// existing coverage stubbed ClearHistory with a closure, so nothing exercised
// the real store.

package agent

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// TestClearSessionWindow_EmptiesTheLiveWindowOnANonEmptyArchive fails before
// the fix (SetHistory refuses, GetHistory still returns all three messages)
// and passes after it.
func TestClearSessionWindow_EmptiesTheLiveWindowOnANonEmptyArchive(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	store, err := session.NewUnifiedStore(filepath.Join(home, "sessions"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	const key = "webchat:clear-me"
	store.AddMessage(key, "user", "what is the deploy host?")
	store.AddMessage(key, "assistant", "prod-east-1.example.com")
	store.AddMessage(key, "user", "thanks")
	require.Len(t, store.GetHistory(key), 3, "precondition: the window holds the conversation")

	require.NoError(t, clearSessionWindow(store, key))

	assert.Empty(t, store.GetHistory(key),
		"/clear must empty the live window — a refused SetHistory used to leave it fully intact "+
			"while the command still reported success")

	// The archive is preserved: recall by tool_call_id and the [capped]/
	// [emptied] marks must keep resolving after a clear.
	archived, archErr := store.ReadArchive(t.Context(), key)
	require.NoError(t, archErr)
	assert.Len(t, archived, 3, "/clear advances Skip; it must never delete archive lines")

	// A message sent after the clear starts a fresh window.
	store.AddMessage(key, "user", "new topic")
	assert.Len(t, store.GetHistory(key), 1,
		"the next message must be answered without the prior conversation in the window")
}

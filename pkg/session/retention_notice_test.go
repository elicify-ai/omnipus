// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package session

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func expiryNoticeEntries(t *testing.T, s *UnifiedStore, sid string) []TranscriptEntry {
	t.Helper()
	entries, err := s.ReadTranscript(sid)
	require.NoError(t, err)
	var out []TranscriptEntry
	for _, e := range entries {
		if e.SystemSubtype == SystemSubtypeExpiryNotice {
			out = append(out, e)
		}
	}
	return out
}

// FR-007: an expiry that moves the window tells the agent, in the model view
// (WindowView.ExpiryNote, which the next request carries) and in the chat
// transcript, and the cursor's new position is the clear floor so nothing
// expired is ever quoted.
func TestRetentionNotice_WindowMovedByExpiryCarriesTheNoticeAndFloor(t *testing.T) {
	store, sid, _, _ := wiredModelRetentionSession(t)

	removed, err := store.RetentionSweep(7)
	require.NoError(t, err)
	require.Equal(t, 1, removed)

	view, err := store.WindowView(context.Background(), sid)
	require.NoError(t, err)
	require.Contains(t, view.ExpiryNote, "Retention expired older conversation content")
	require.Equal(t, 3, view.State.Skip)
	require.Equal(t, view.State.Skip, view.State.ClearFloor, "what expired is gone: the floor is the cursor")

	notices := expiryNoticeEntries(t, store, sid)
	require.Len(t, notices, 1)
	require.Equal(t, view.ExpiryNote, notices[0].Content)
	require.Equal(t, EntryTypeSystem, notices[0].Type)
	require.Equal(t, ViewMembershipChat, notices[0].ViewMembership)
}

// A sweep that expires nothing new announces nothing new.
func TestRetentionNotice_SecondSweepAddsNoNotice(t *testing.T) {
	store, sid, _, _ := wiredModelRetentionSession(t)
	_, err := store.RetentionSweep(7)
	require.NoError(t, err)
	_, err = store.RetentionSweep(7)
	require.NoError(t, err)
	require.Len(t, expiryNoticeEntries(t, store, sid), 1)
}

// A /clear starts over: the model view no longer announces the expiry.
func TestRetentionNotice_ClearDropsTheModelNotice(t *testing.T) {
	store, sid, _, _ := wiredModelRetentionSession(t)
	_, err := store.RetentionSweep(7)
	require.NoError(t, err)

	ctx := context.Background()
	view, err := store.WindowView(ctx, sid)
	require.NoError(t, err)
	require.NotEmpty(t, view.ExpiryNote, "precondition")
	after := view.State.Clone()
	after.Skip = after.Count
	after.ClearFloor = after.Count
	require.NoError(t, store.CommitWindow(ctx, sid, view.State, after))

	view, err = store.WindowView(ctx, sid)
	require.NoError(t, err)
	require.Empty(t, view.ExpiryNote)
}

package session

// Gate-1 fix round 3 for PR #1345: code-reviewer R2-1, already-placed arm.

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSavedChatInput_PlacedButIndexNotRetiredReportsPlaced: placement of a
// saved input lands its model reference, then the pending-index retirement
// fails. A later lookup must report the input as placed (so no second wake
// turn is admitted for it) and must clear the stale pending entry.
func TestSavedChatInput_PlacedButIndexNotRetiredReportsPlaced(t *testing.T) {
	s := newTestStore(t)
	meta, err := s.NewSession(SessionTypeChat, "", "agent")
	require.NoError(t, err)
	sid := meta.ID
	const id = "saved-input-r3-placed"
	addr, err := s.SaveChatInput(sid, id, "agent", "helper report placed once")
	require.NoError(t, err)

	SetSavedInputIndexWriteFaultForTest(func(string) error { return errors.New("injected index write failure") })
	t.Cleanup(func() { SetSavedInputIndexWriteFaultForTest(nil) })
	_, _, err = s.PlaceSavedInput(context.Background(), sid, addr)
	require.Error(t, err, "SETUP: the model reference lands, the index retirement fails")
	SetSavedInputIndexWriteFaultForTest(nil)
	archive, err := s.archiveStore(sid)
	require.NoError(t, err)
	landed, err := savedSourceAlreadyPlaced(archive, addr, -1)
	require.NoError(t, err)
	require.True(t, landed, "SETUP: the placement itself is durable")

	gotAddr, placed, err := s.SavedChatInput(sid, id)
	require.NoError(t, err)
	require.Equal(t, addr, gotAddr)
	require.True(t, placed, "a durably placed input is reported placed, whatever the stale index says")
	pending, err := s.PendingChatInputs(context.Background(), sid)
	require.NoError(t, err)
	require.Empty(t, pending, "the stale pending entry is cleared")
}

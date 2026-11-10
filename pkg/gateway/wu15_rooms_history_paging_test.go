package gateway

import (
	"fmt"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

// D15/FR-025: room absence/mirror assertions mean all retained history, not
// merely a bounded suffix. Sixty-two source groups and two clear boundaries
// require three actual pages with the default 40-group cap and closed clears.
func TestWU15Rooms_HistoryHelperEnumeratesAllRetainedPagesAcrossClear(t *testing.T) {
	h := wu15NewHarness(t)
	sid := h.main("mia")
	store := h.al.GetSessionStore()
	require.NotNil(t, store)
	at := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	wantIDs := make([]string, 0, 62)
	for i := 0; i < 62; i++ {
		id := fmt.Sprintf("room-history-%02d", i)
		entry := session.TranscriptEntry{ID: id, Role: "user", Content: id,
			AgentID: "mia", ViewMembership: session.ViewMembershipChat, Timestamp: at.Add(time.Duration(i) * time.Second)}
		if i == 20 || i == 41 {
			entry.Type, entry.Role, entry.Content = session.EntryTypeSystem, "system", session.ClearMarkerText
		}
		require.NoError(t, store.AppendTranscriptStrict(sid, entry), "actual canonical writer must publish each indexed source")
		wantIDs = append(wantIDs, id)
	}
	messages := h.messages("mia")
	require.Equal(t, wantIDs, messageIDs(messages), "whole-history assertions must cross both clears and reach the true retained beginning")
	for _, message := range messages {
		require.NotNil(t, message.HistoryPosition, "the real page, not a legacy full-read fallback, owns each occurrence")
	}
}

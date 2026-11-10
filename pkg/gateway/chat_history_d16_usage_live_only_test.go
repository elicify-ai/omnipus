package gateway

import (
	"encoding/json"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/stretchr/testify/require"
)

// D16/S4: history failure is independent of an authoritative current usage
// cache. Drive the real required-storage fault, never fake an unavailable page.
func historyD16UsageLiveOnly(t *testing.T) {
	h, provider, sid, peer, _ := historyD16TwoUsageTurns(t)
	restore := historyMigrationUnreadable(t, h.store, sid)
	defer restore()
	rawFrames := h.attach(peer, sid)
	frames := make([]wireFrame, len(rawFrames))
	for i, raw := range rawFrames {
		frames[i] = decodeWire(t, raw)
	}
	historyMigrationAssertLiveOnly(t, sid, frames)
	states := 0
	for _, raw := range rawFrames {
		if historyD16Type(t, raw) == "session_state" {
			var state generated.SessionStateFrame
			require.NoError(t, json.Unmarshal(raw, &state))
			require.Equal(t, &sid, state.SessionId)
			states++
			historyD16AssertTotal(t, state.SessionTokensTotal, 46323, "authorized live_only current state")
		}
	}
	require.Equal(t, 1, states)
	restore()
	historyD16StoredTotal(t, h.store, sid, 46323)
	historyD16AssertAttachUsage(t, sid, h.attach(peer, sid), 46323)
	require.Equal(t, 2, provider.count(), "history recovery neither executes nor repeats accounting")
}

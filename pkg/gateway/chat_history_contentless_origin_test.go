package gateway

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// UAT INCIDENT-1 (7d2d03675): every real agent turn starts with a content-less,
// tool-less assistant bookkeeping record (tokens, turn id). The index counts it
// as an eligible page source group, but the WS snapshot emitted no frame for
// it, so the client saw source_group_count N with only N-1 history origins and
// rejected the whole snapshot ("Could not load messages." until Retry).
// Oracle: the REST page of the same session, i.e. the counted source groups.
func TestHistorySnapshot_ContentlessEligibleSourceKeepsItsOrigin(t *testing.T) {
	home, cfg := historyD16Config(t)
	provider := &historyD16Provider{}
	h := newHistoryD16Harness(t, home, cfg, provider, false)
	sid := h.session()
	at := time.Date(2026, 10, 23, 9, 45, 0, 0, time.UTC)
	chat := session.ViewMembershipChat
	for _, e := range []session.TranscriptEntry{
		{ID: "contentless-user", Role: "user", Content: "run one command", AgentID: "mia", Timestamp: at, ViewMembership: chat},
		// The turn-start bookkeeping row: no content, no tool calls, not truncated.
		{ID: "contentless-turn-start", Role: "assistant", AgentID: "mia", Timestamp: at.Add(time.Second), ViewMembership: chat, TurnID: "mia-turn-1", Tokens: 120},
		{ID: "contentless-answer", Role: "assistant", Content: "done", AgentID: "mia", Timestamp: at.Add(2 * time.Second), ViewMembership: chat, TurnID: "mia-turn-1"},
	} {
		_, err := h.store.AppendTranscriptAddressed(sid, e)
		require.NoError(t, err)
	}
	require.NoError(t, h.store.FlushSessionStats(sid))

	page := h.page(sid, "")
	counted := make([]string, 0, len(page.Entries))
	for _, entry := range page.Entries {
		require.NotNil(t, entry.HistoryPosition)
		counted = append(counted, *entry.HistoryPosition)
	}
	require.Len(t, counted, 3, "positive control: the content-less turn start is a counted page source group")
	require.Equal(t, 3, page.PageInfo.SourceGroupCount)

	peer := h.connect()
	limit, floor := 40, 20
	require.NoError(t, peer.WriteJSON(generated.AttachSessionFrame{Type: "attach_session", SessionId: sid, HistoryLimit: &limit, HistoryMinRows: &floor}))
	wire := h.readUntil(peer, func(raw []byte) bool { return historyD16Type(t, raw) == "catch_up_complete" })
	var snapshot *generated.SessionSnapshotFrame
	origins := []string{}
	seen := map[string]bool{}
	for _, raw := range wire {
		if historyD16Type(t, raw) == "session_snapshot" {
			snapshot = &generated.SessionSnapshotFrame{}
			require.NoError(t, json.Unmarshal(raw, snapshot))
			continue
		}
		var carrier struct {
			HistoryOrigin *generated.HistoryEntryOrigin `json:"history_origin"`
		}
		require.NoError(t, json.Unmarshal(raw, &carrier))
		if carrier.HistoryOrigin != nil && !seen[carrier.HistoryOrigin.Position] {
			seen[carrier.HistoryOrigin.Position] = true
			origins = append(origins, carrier.HistoryOrigin.Position)
		}
	}
	require.NotNil(t, snapshot)
	require.Equal(t, page.PageInfo.SourceGroupCount, snapshot.PageInfo.SourceGroupCount)
	require.Equal(t, counted, origins, "every counted source group carries its own history origin, in order")
	require.Zero(t, provider.count(), "history paging invokes no model")
}

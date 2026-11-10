package integration

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

// D1/D10/D15: these fixtures seed fresh, idle sessions, before any model turn.
// Use the same canonical writer/index publisher as the product, not raw bytes
// behind the gateway's empty index. Join this writer before HTTP/WS reads or a
// live turn; the gateway is the sole canonical writer afterwards.
func appendIndexedIntegrationTranscript(t *testing.T, gw *testutil.TestGateway, sid string, records []map[string]any) {
	t.Helper()
	home := filepath.Dir(gw.HomeDir()) // same AgentLoop home derivation as loop_construct.go
	store, err := session.NewUnifiedStoreWithHome(filepath.Join(home, "sessions"), home)
	require.NoError(t, err)
	defer func() { require.NoError(t, store.Close(), "join the fixture writer before attach/live work") }()
	meta, err := store.GetMeta(sid)
	require.NoError(t, err, "REST-created canonical session must exist")
	require.Equal(t, sid, meta.ID)
	wantIDs := make([]string, 0, len(records))
	for _, record := range records {
		data, marshalErr := json.Marshal(record)
		require.NoError(t, marshalErr)
		var entry session.TranscriptEntry
		require.NoError(t, json.Unmarshal(data, &entry))
		if entry.Type == "" {
			entry.Type = session.EntryTypeMessage
		}
		if entry.AgentID == "" {
			entry.AgentID = integrationChatAgentID
		}
		entry.ViewMembership = session.ViewMembershipChat
		require.NotEmpty(t, entry.ID, "fixture retains each original source identity")
		require.False(t, entry.Timestamp.IsZero(), "fixture retains its original saved timestamp")
		require.NoError(t, store.AppendTranscriptStrict(sid, entry), "actual product append/index publication")
		wantIDs = append(wantIDs, entry.ID)
	}
	require.NoError(t, store.CheckChatHistoryReadiness(context.Background(), sid), "the fixture must have a valid published history baseline")
	saved, err := store.ReadTranscript(sid)
	require.NoError(t, err)
	gotIDs := make([]string, 0, len(saved))
	for _, entry := range saved {
		gotIDs = append(gotIDs, entry.ID)
	}
	require.Equal(t, wantIDs, gotIDs, "fixture must preserve every source in ascending canonical order")
}

// Replay done is not attach completion (D7/D10). Fail loudly on missing frames,
// malformed JSON or unavailable history, instead of returning an apparently
// complete empty transcript after a timeout. Keep the caller's original budget.
func collectIntegrationHistoryCatchUp(t *testing.T, conn *websocket.Conn, timeout time.Duration) []map[string]any {
	t.Helper()
	frames := make([]map[string]any, 0)
	sawSnapshot := false
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(timeout)))
	for {
		_, raw, err := conn.ReadMessage()
		require.NoError(t, err, "D10 requires an actual catch_up_complete, not a timeout/empty-history fallback")
		var fields map[string]any
		require.NoError(t, json.Unmarshal(raw, &fields))
		frames = append(frames, fields)
		switch fields["type"] {
		case "error":
			t.Fatalf("canonical integration fixture unexpectedly refused: %s", raw)
		case "session_state":
			var state generated.SessionStateFrame
			require.NoError(t, json.Unmarshal(raw, &state))
			if state.HistoryState != nil {
				require.Equal(t, "available", state.HistoryState.Status, "a fixture failure must not become live-only replay success")
			}
		case "session_snapshot":
			require.False(t, sawSnapshot, "one snapshot per attach, never duplicate replacement")
			sawSnapshot = true
			var snapshot generated.SessionSnapshotFrame
			require.NoError(t, json.Unmarshal(raw, &snapshot))
			require.Equal(t, "latest", snapshot.PageInfo.Mode)
			require.Equal(t, 40, snapshot.PageInfo.Limit)
			require.Equal(t, 20, snapshot.PageInfo.MinRows)
		case "catch_up_complete":
			require.True(t, sawSnapshot, "a snapshot completion requires actual page metadata")
			var complete generated.CatchUpCompleteFrame
			require.NoError(t, json.Unmarshal(raw, &complete))
			require.Equal(t, "snapshot", complete.Mode, "these no-cursor attaches must reconstruct the real selected page")
			require.NotEmpty(t, complete.SessionId)
			return frames
		}
	}
}

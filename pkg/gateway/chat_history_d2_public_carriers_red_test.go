package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

func TestChatHistoryPage_DisplayShorteningPublicCarriers(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	sid := createTestSession(t, api)
	store := api.agentLoop.GetSessionStore()
	value := d2ResultBytes(t, 51201)
	d2AppendResult(t, store, sid, value)
	require.NoError(t, store.Close())
	before := pagingProjectionTree(t, api.homePath)
	want := map[string]any{"_history_preview": true, "original_size_bytes": float64(51201), "preview": `{"text":"` + string(makeD2ASCII(4087))}
	p := pagingRead(t, api, sid, "")
	require.Len(t, p.Entries, 1)
	require.NotNil(t, p.Entries[0].ToolCalls)
	require.Equal(t, want, *(*p.Entries[0].ToolCalls)[0].Result)
	w := httptest.NewRecorder()
	api.HandleSessions(w, httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+sid, nil))
	require.Equal(t, 200, w.Code)
	var detail generated.SessionDetail
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &detail))
	require.NotNil(t, detail.Messages)
	require.Len(t, *detail.Messages, 1)
	require.Equal(t, p.Entries, *detail.Messages, "session detail and REST page carry identical shortened public Messages")
	r, err := newHistoryPageRequest(sid, session.ChatHistoryPageOptions{Limit: 40}, historyPageProjectionDeps{})
	require.NoError(t, err)
	snapshot, err := r.Read(context.Background(), store)
	require.NoError(t, err)
	require.Equal(t, p.Entries, snapshot.Response.Entries)
	found := 0
	for _, raw := range snapshot.ReplayFrames {
		var frame map[string]any
		require.NoError(t, json.Unmarshal(raw, &frame))
		if frame["type"] == "tool_call_result" {
			found++
			require.Equal(t, want, frame["result"])
			require.Equal(t, *p.Entries[0].HistoryPosition, frame["history_origin"].(map[string]any)["position"])
		}
	}
	require.Equal(t, 1, found)
	require.Equal(t, before, pagingProjectionTree(t, api.homePath), "no archive/index/tool_results publication or live offload while reading")
}
func makeD2ASCII(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'x'
	}
	return b
}

func TestChatHistoryPage_DisplayShorteningKeepsStructuredVerdict(t *testing.T) {
	s, sid, _ := pagingProjectionFixture(t)
	e := pagingCarrierBoundaryEntry(t, sid, "verdict", 1) // real typed64KiB criterion reason + JSON envelope.
	require.Greater(t, len(e.Content), 65536)
	require.NoError(t, s.AppendTranscriptStrict(sid, e))
	g := d2PrepareOnlyGroup(t, s, sid)
	require.NotNil(t, g.message.Content)
	require.Equal(t, e.Content, *g.message.Content)
	require.Nil(t, g.message.ContentOriginalSizeBytes)
	require.NotNil(t, g.message.Verdict, "parsed verdict survives, not merely its JSON string")
	var expected map[string]any
	require.NoError(t, json.Unmarshal([]byte(e.Content), &expected))
	want := expected["per_criterion"]
	found := 0
	for _, raw := range g.frames {
		var frame map[string]any
		require.NoError(t, json.Unmarshal(raw, &frame))
		if frame["type"] == "judge_verdict" {
			found++
			require.Equal(t, want, frame["per_criterion"])
		}
	}
	require.Equal(t, 1, found)
}

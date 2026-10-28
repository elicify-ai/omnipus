package gateway

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

func TestHistoryEntryRead_FullProjectionAfterEffects(t *testing.T) {
	for _, kind := range []string{"settled", "projected", "retracted"} {
		t.Run(kind, func(t *testing.T) {
			api, cleanup := newTestRestAPI(t)
			defer cleanup()
			t.Setenv("OMNIPUS_BEARER_TOKEN", "d2-full-effects-token")
			sid := createTestSession(t, api)
			s := api.agentLoop.GetSessionStore()
			call := session.ToolCall{ID: "d2-effect-call", Tool: "read_file", Status: "pending", Parameters: map[string]any{}, ContentState: "full"}
			address, err := s.AppendTranscriptAddressed(sid, session.TranscriptEntry{ID: "d2-effect-owner", Type: session.EntryTypeToolCall, AgentID: "mia", ViewMembership: session.ViewMembershipChat, ToolCalls: []session.ToolCall{call}})
			require.NoError(t, err)
			want := map[string]any{"preserved": "structured sibling", "text": "settled-" + strings.Repeat("x", 60000)}
			call.Status = "success"
			call.Result = want
			_, err = s.SettleToolCall(sid, address, "pending", call)
			require.NoError(t, err)
			if kind != "settled" {
				text := "projected-" + strings.Repeat("y", 60000)
				effect, e := s.ProjectToolCalls(sid, []session.ToolCallProjectionEdit{{Target: address, ToolCallID: call.ID, ContentState: "capped", Text: text}})
				require.NoError(t, e)
				if kind == "retracted" {
					_, err = s.RetractToolCallEffects(sid, []session.ArchiveAddress{effect})
					require.NoError(t, err)
				} else {
					want = map[string]any{"preserved": "structured sibling", "text": text}
				}
			}
			require.NoError(t, s.Close())
			before := pagingProjectionTree(t, api.homePath)
			position := pagingPositionOracle(sid, address)
			for attempt := 0; attempt < 2; attempt++ {
				w := d2EntryRequest(api, sid, position, "Bearer d2-full-effects-token", nil)
				require.Equal(t, 200, w.Code)
				var message generated.Message
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &message))
				require.Equal(t, "d2-effect-owner", message.Id)
				require.NotNil(t, message.HistoryPosition)
				require.Equal(t, position, *message.HistoryPosition)
				require.NotNil(t, message.ToolCalls)
				require.Len(t, *message.ToolCalls, 1)
				require.NotNil(t, (*message.ToolCalls)[0].Result)
				require.Equal(t, want, *(*message.ToolCalls)[0].Result, "full public projection applies correction/retraction, without preview or lost sibling keys")
				require.Nil(t, message.ContentOriginalSizeBytes)
			}
			require.Equal(t, before, pagingProjectionTree(t, api.homePath), "no archive/index/tool_results writes during repeated full reads")
		})
	}
}
func TestHistoryEntryRead_MissingMetadataIs404(t *testing.T) {
	api, sid, _ := d2EntryFixture(t)
	s := api.agentLoop.GetSessionStore()
	require.NoError(t, s.Close())
	s.EvictSessionMeta(sid)
	require.NoError(t, os.Remove(filepath.Join(s.BaseDir(), sid, "meta.json")))
	d2EntryRefusal(t, d2EntryRequest(api, sid, "malformed", "Bearer d2-route-test-token", nil), 404, "session not found", sid, "meta.json")
}

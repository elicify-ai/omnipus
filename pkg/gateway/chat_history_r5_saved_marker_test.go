package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type historyR5MarkerFixture struct { // not-wire-format: private test fixture.
	prepared preparedHistoryPage
	full     generated.Message
	sid      string
	position string
}

// Real canonical append -> real bounded page reader/weigher/validator, NOT a
// direct prepareGroup call. The full-entry control uses the actual HTTP route.
func historyR5ReadStoredResult(t *testing.T, stored map[string]any) historyR5MarkerFixture {
	t.Helper()
	api, cleanup := newTestRestAPI(t)
	t.Cleanup(cleanup)
	t.Setenv("OMNIPUS_BEARER_TOKEN", "r5-marker-test-token")
	sid := createTestSession(t, api)
	s := api.agentLoop.GetSessionStore()
	address, err := s.AppendTranscriptAddressed(sid, session.TranscriptEntry{
		ID: "r5-marker-owner", Type: session.EntryTypeToolCall, Role: "assistant", AgentID: "mia",
		Timestamp: time.Date(2026, 10, 31, 7, 0, 0, 0, time.UTC), ViewMembership: session.ViewMembershipChat,
		ToolCalls: []session.ToolCall{{ID: "r5-marker-call", Tool: "read_file", Status: "success", ContentState: "full", Parameters: map[string]any{}, Result: stored}},
	})
	require.NoError(t, err, "actual canonical writer accepts the saved result")
	require.NoError(t, s.Close(), "join background writes before observing unchanged canonical bytes")
	canonical := filepath.Join(s.BaseDir(), sid, "transcript.jsonl")
	before, err := os.ReadFile(canonical)
	require.NoError(t, err)
	position := pagingPositionOracle(sid, address)
	full := d2EntryRequest(api, sid, position, "Bearer r5-marker-test-token", nil)
	require.Equal(t, http.StatusOK, full.Code, full.Body.String())
	var message generated.Message
	require.NoError(t, json.Unmarshal(full.Body.Bytes(), &message))
	request, err := newHistoryPageRequest(sid, session.ChatHistoryPageOptions{Limit: 20}, historyPageProjectionDeps{ToolStore: newToolResultStore(api.homePath)})
	require.NoError(t, err)
	prepared, err := request.Read(context.Background(), s)
	require.NoError(t, err, "real reader/marker validator must degrade one item, not fail the whole page")
	require.Len(t, prepared.Response.Entries, 1)
	require.Equal(t, 1, prepared.Response.PageInfo.SourceGroupCount)
	require.Equal(t, sid, prepared.Response.SessionId)
	require.False(t, prepared.Response.HasEarlier)
	require.False(t, prepared.Response.HasLater)
	var encoded generated.SessionHistoryResponse
	require.NoError(t, json.Unmarshal(prepared.encodedResponse, &encoded))
	require.Equal(t, prepared.Response, encoded, "actual encoded REST carrier agrees with generated page")
	after, err := os.ReadFile(canonical)
	require.NoError(t, err)
	require.Equal(t, before, after, "neither full entry nor display page may rewrite canonical bytes")
	return historyR5MarkerFixture{prepared: prepared, full: message, sid: sid, position: position}
}

func historyR5AssertPlaceholder(t *testing.T, m generated.Message, position string) {
	t.Helper()
	require.Equal(t, "r5-marker-owner", m.Id, "D4 retains the saved occurrence identity")
	require.NotNil(t, m.HistoryPosition)
	require.Equal(t, position, *m.HistoryPosition)
	require.NotNil(t, m.Content)
	assert.Equal(t, "This saved message could not be displayed.", *m.Content, "SEC-R5-3 saved reserved marker must become the D4 placeholder")
	assert.Nil(t, m.ToolCalls, "D4 placeholder must not retain saved tool calls or a minted reader preview")
	assert.Nil(t, m.ContentOriginalSizeBytes, "placeholder is not a text preview")
}

// SEC-R5-3 / A2.2, D4: shortening an oversized persisted marker must not erase
// its reserved-key provenance. Both actual prepared carriers must agree with
// the already-protected full-entry route, not emit a schema-valid NEW marker.
func TestHistoryPage_R5SavedOversizedMarkerIsPlaceholder(t *testing.T) {
	const canary = "R5_PERSISTED_MARKER_CANARY"
	stored := map[string]any{"_history_preview": true, "original_size_bytes": 60000, "preview": canary, "zz_large": strings.Repeat("z", 51201)}
	// The extra field alone exceeds 51,200 bytes. Its sorted key keeps the
	// marker/canary in the first 4,096 bytes of the broken replacement preview.
	oversized, ok := stored["zz_large"].(string)
	require.True(t, ok, "fixture's oversized saved field is a string")
	require.Greater(t, len(oversized), 51200)
	f := historyR5ReadStoredResult(t, stored)
	t.Run("full_entry_control", func(t *testing.T) {
		historyR5AssertPlaceholder(t, f.full, f.position)
	})
	t.Run("rest_page_carrier", func(t *testing.T) {
		historyR5AssertPlaceholder(t, f.prepared.Response.Entries[0], f.position)
		assert.Equal(t, f.full, f.prepared.Response.Entries[0], "SEC-R5-3 exact same placeholder as full-entry route")
		assert.NotContains(t, string(f.prepared.encodedResponse), "_history_preview", "no saved or newly minted marker on REST page")
		assert.NotContains(t, string(f.prepared.encodedResponse), canary)
	})
	t.Run("snapshot_carrier", func(t *testing.T) {
		messages, results := 0, 0
		for _, raw := range f.prepared.ReplayFrames {
			var frame generated.ReplayMessageFrame
			require.NoError(t, json.Unmarshal(raw, &frame))
			assert.NotContains(t, string(raw), "_history_preview", "SEC-R5-3 no saved or newly minted marker on WS")
			assert.NotContains(t, string(raw), canary, "SEC-R5-3 no saved marker body on WS")
			switch frame.Type {
			case "replay_message":
				messages++
				assert.Equal(t, "This saved message could not be displayed.", frame.Content)
				assert.Equal(t, "system", frame.Role)
				require.NotNil(t, frame.Id)
				assert.Equal(t, "r5-marker-owner", *frame.Id)
				require.NotNil(t, frame.HistoryOrigin)
				assert.Equal(t, f.position, frame.HistoryOrigin.Position)
			case "tool_call_result":
				results++
			}
		}
		assert.Equal(t, 1, messages, "SEC-R5-3 exactly one actual placeholder frame, not vacuous no-leak")
		assert.Equal(t, 0, results, "SEC-R5-3 saved marker must not be promoted to a successful tool-result frame")
	})
}

// Positive controls for SEC-R5-3: rejecting a persisted reserved key must not
// reject a genuine reader-created preview, nor an ordinary small saved result.
func TestHistoryPage_R5GenuinePreviewAndSmallResultRemainReadable(t *testing.T) {
	for _, large := range []bool{false, true} {
		name, text := "small_result", "R5_SMALL_RESULT_CONTROL"
		if large {
			name = "genuine_reader_preview"
			// Prefix {"text":" is nine bytes; the three-byte euro crosses
			// 4,096. The independent UTF-8 cut must therefore be 4,095 bytes.
			text = strings.Repeat("a", 4086) + "€" + strings.Repeat("x", 51201)
		}
		t.Run(name, func(t *testing.T) {
			stored := map[string]any{"text": text}
			f := historyR5ReadStoredResult(t, stored)
			m := f.prepared.Response.Entries[0]
			require.Equal(t, "r5-marker-owner", m.Id)
			require.NotNil(t, m.HistoryPosition)
			require.Equal(t, f.position, *m.HistoryPosition)
			require.NotNil(t, f.full.ToolCalls)
			require.Len(t, *f.full.ToolCalls, 1)
			require.NotNil(t, (*f.full.ToolCalls)[0].Result)
			require.Equal(t, stored, *(*f.full.ToolCalls)[0].Result, "control: full-entry result is whole and unmarked")
			want := stored
			if large {
				encoded, err := json.Marshal(stored)
				require.NoError(t, err)
				cut := 4096 // A2.1 exact encoded-byte ceiling.
				for !utf8.Valid(encoded[:cut]) {
					cut--
				}
				require.Equal(t, 4095, cut, "independent UTF-8 boundary arithmetic")
				want = map[string]any{"_history_preview": true, "original_size_bytes": float64(len(encoded)), "preview": string(encoded[:cut])}
			}
			require.NotNil(t, m.ToolCalls)
			require.Len(t, *m.ToolCalls, 1)
			require.NotNil(t, (*m.ToolCalls)[0].Result)
			require.Equal(t, want, *(*m.ToolCalls)[0].Result, "control: exact REST result, not just marker presence")
			results := 0
			for _, raw := range f.prepared.ReplayFrames {
				var frame generated.ToolCallResultFrame
				require.NoError(t, json.Unmarshal(raw, &frame))
				if frame.Type == "tool_call_result" {
					results++
					require.Equal(t, "r5-marker-call", frame.CallId)
					require.Equal(t, "success", frame.Status)
					require.NotNil(t, frame.HistoryOrigin)
					require.Equal(t, f.position, frame.HistoryOrigin.Position)
					require.Equal(t, want, frame.Result, "control: exact WS result and UTF-8 prefix/size")
				}
			}
			require.Equal(t, 1, results, "positive control: inspected a real history-origin tool result, not an empty frame set")
		})
	}
}

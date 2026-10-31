package gateway

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/media/mediatest"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// T84's exhaustive typed source-carrier inventory also includes media and the
// resolved question card. These use the canonical media/Ask producers rather
// than a fabricated frame that would only test JSON stamping in isolation.
func s11OriginExtendedClasses(t *testing.T) {
	for _, known := range []bool{false, true} {
		t.Run(fmt.Sprintf("media/known=%t", known), func(t *testing.T) {
			store, sid, home := pagingProjectionFixture(t)
			mediaStore := mediatest.NewFileMediaStore(t)
			image, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAAC0lEQVR4nGP4DwQACfsD/fteaysAAAAASUVORK5CYII=")
			require.NoError(t, err)
			path := filepath.Join(home, "s11-pixel.png")
			require.NoError(t, os.WriteFile(path, image, 0600))
			ref, err := mediaStore.Store(path, media.MediaMeta{Filename: "s11-pixel.png", ContentType: "image/png", CleanupPolicy: media.CleanupPolicyForgetOnly}, "s11-fixture")
			require.NoError(t, err)
			turn := ""
			if known {
				turn = "s11-media-recorded-turn"
			}
			source := session.TranscriptEntry{ID: "s11-media-source", Type: session.EntryTypeToolCall, Role: "assistant", AgentID: "mia", TurnID: turn, ViewMembership: session.ViewMembershipChat, Timestamp: time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC),
				ToolCalls: []session.ToolCall{{ID: "s11-media-call", Tool: "send_file", Status: "success", Result: map[string]any{"media": []any{map[string]any{"ref": ref, "filename": "s11-pixel.png", "content_type": "image/png", "type": "image"}}}}}}
			address, err := store.AppendTranscriptAddressed(sid, source)
			require.NoError(t, err)
			request, err := newHistoryPageRequest(sid, session.ChatHistoryPageOptions{Limit: 20, MinRows: 1}, historyPageProjectionDeps{ToolStore: newToolResultStore(home), MediaStore: mediaStore})
			require.NoError(t, err)
			prepared, err := request.Read(context.Background(), store)
			require.NoError(t, err)
			require.Equal(t, 1, prepared.Response.PageInfo.SourceGroupCount)
			s11AssertOriginCarriers(t, prepared, map[string]generated.HistoryEntryOrigin{source.ID: s11ExpectedOrigin(pagingPositionOracle(sid, address), source.ID, turn)}, []string{"tool_call_start", "tool_call_result", "media"})
		})
		t.Run(fmt.Sprintf("question/known=%t", known), func(t *testing.T) {
			store, sid, home := pagingProjectionFixture(t)
			terminal := terminalAskFixture()
			terminal.TranscriptSessionID = sid
			want := make(map[string]generated.HistoryEntryOrigin)
			for i, source := range askReplayEntries(t, terminal) {
				source.ID, source.AgentID, source.ViewMembership = fmt.Sprintf("s11-ask-%d", i), "mia", session.ViewMembershipChat
				source.Timestamp = time.Date(2026, 10, 28, 12, 0, i, 0, time.UTC)
				source.TurnID = ""
				if known {
					source.TurnID = fmt.Sprintf("s11-ask-source-turn-%d", i)
				}
				address, err := store.AppendTranscriptAddressed(sid, source)
				require.NoError(t, err)
				want[source.ID] = s11ExpectedOrigin(pagingPositionOracle(sid, address), source.ID, source.TurnID)
			}
			request, err := newHistoryPageRequest(sid, session.ChatHistoryPageOptions{Limit: 20, MinRows: 1}, historyPageProjectionDeps{ToolStore: newToolResultStore(home), TerminalAsk: terminal})
			require.NoError(t, err)
			prepared, err := request.Read(context.Background(), store)
			require.NoError(t, err)
			require.Equal(t, 4, prepared.Response.PageInfo.SourceGroupCount)
			s11AssertOriginCarriers(t, prepared, want, []string{"ask_user_question"})
		})
	}
}

func s11ExpectedOrigin(position, id, turn string) generated.HistoryEntryOrigin {
	origin := generated.HistoryEntryOrigin{Position: position, EntryId: &id}
	if turn != "" {
		origin.TurnId = &turn
	}
	return origin
}

func s11AssertOriginCarriers(t *testing.T, prepared preparedHistoryPage, want map[string]generated.HistoryEntryOrigin, required []string) {
	t.Helper()
	seen := make(map[string]int)
	for _, raw := range prepared.ReplayFrames {
		var frame map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(raw, &frame))
		var kind string
		require.NoError(t, json.Unmarshal(frame["type"], &kind))
		if kind == "done" {
			require.NotContains(t, frame, "history_origin")
			continue
		}
		require.NoError(t, validateHistorySourceFrame(raw))
		var actual generated.HistoryEntryOrigin
		require.NoError(t, json.Unmarshal(frame["history_origin"], &actual))
		require.NotNil(t, actual.EntryId)
		expected, ok := want[*actual.EntryId]
		require.True(t, ok, "every emitted origin belongs to its own canonical source")
		assert.Equal(t, expected, actual, "S11d persisted source turn/position on %s, never the neighbouring source's turn", kind)
		if expected.TurnId == nil {
			var fields map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(frame["history_origin"], &fields))
			assert.NotContains(t, fields, "turn_id", "unknown source omits turn_id rather than null/empty")
		}
		seen[kind]++
	}
	for _, kind := range required {
		require.Positive(t, seen[kind], "instrument: real %s source frame was emitted", kind)
	}
}

package gateway

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/media/mediatest"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

func TestChatHistoryPage_DisplayShorteningPreservesMediaFacts(t *testing.T) {
	s, sid, home := pagingProjectionFixture(t)
	store := mediatest.NewFileMediaStore(t)
	png, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAAC0lEQVR4nGP4DwQACfsD/fteaysAAAAASUVORK5CYII=")
	require.NoError(t, err)
	path := filepath.Join(home, "d2-pixel.png")
	require.NoError(t, os.WriteFile(path, png, 0600))
	ref, err := store.Store(path, media.MediaMeta{Filename: "d2-pixel.png", ContentType: "image/png", CleanupPolicy: media.CleanupPolicyForgetOnly}, "history-fixture")
	require.NoError(t, err)
	value := map[string]any{"AAA": strings.Repeat("x", 60000), "media": []any{map[string]any{"ref": ref, "filename": "d2-pixel.png", "content_type": "image/png", "type": "image"}}}
	raw, err := json.Marshal(value)
	require.NoError(t, err)
	require.NotContains(t, string(raw[:4096]), ref, "positive control: media facts lie beyond the preview")
	require.NoError(t, s.AppendTranscriptStrict(sid, session.TranscriptEntry{ID: "d2-media-owner", Type: session.EntryTypeToolCall, AgentID: "mia", ViewMembership: session.ViewMembershipChat, ToolCalls: []session.ToolCall{{ID: "d2-media-call", Tool: "send_file", Status: "success", Parameters: map[string]any{}, Result: value}}}))
	g := d2PrepareOnlyGroup(t, s, sid, historyPageProjectionDeps{MediaStore: store})
	found := 0
	for _, frame := range g.frames {
		var f map[string]any
		require.NoError(t, json.Unmarshal(frame, &f))
		if f["type"] == "media" {
			found++
			require.Equal(t, *g.message.HistoryPosition, d2OriginPosition(t, f))
			require.Contains(t, string(frame), "d2-pixel.png")
		}
	}
	require.Equal(t, 1, found, "full reduced media descriptors survive replacement of the result body")
	result := d2Result(t, g)
	require.Equal(t, true, result["_history_preview"])
	require.Equal(t, float64(len(raw)), result["original_size_bytes"])
	require.Equal(t, string(raw[:4096]), result["preview"])
}
func TestChatHistoryPage_DisplayShorteningPreservesChildFact(t *testing.T) {
	for _, placement := range []string{"top-level", "inside text"} {
		t.Run(placement, func(t *testing.T) {
			s, sid, _ := pagingProjectionFixture(t)
			value := map[string]any{"AAA": strings.Repeat("x", 60000)}
			if placement == "top-level" {
				value["session_id"] = "d2-child-session"
			} else {
				value["text"] = `{"session_id":"d2-child-session"}`
			}
			encoded, err := json.Marshal(value)
			require.NoError(t, err)
			require.NotContains(t, string(encoded[:4096]), "d2-child-session")
			require.NoError(t, s.AppendTranscriptStrict(sid, session.TranscriptEntry{ID: "d2-delegate-owner", Type: session.EntryTypeToolCall, AgentID: "mia", ViewMembership: session.ViewMembershipChat, ToolCalls: []session.ToolCall{{ID: "d2-delegate-call", Tool: "delegate", Status: "error", Parameters: map[string]any{"action": "run", "agent_id": "jim", "task": "test child classification"}, Result: value}}}))
			g := d2PrepareOnlyGroup(t, s, sid)
			starts := 0
			for _, raw := range g.frames {
				var frame map[string]any
				require.NoError(t, json.Unmarshal(raw, &frame))
				if frame["type"] == "subagent_start" {
					starts++
					require.Equal(t, *g.message.HistoryPosition, d2OriginPosition(t, frame))
				}
			}
			require.Equal(t, 1, starts, "an error after creating a child is not reclassified as a refused launch without child")
			require.Equal(t, true, d2Result(t, g)["_history_preview"])
		})
	}
}

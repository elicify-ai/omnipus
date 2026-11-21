package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

func officeReceiveMedia(t *testing.T, conn *wsConn) generated.MediaFrame {
	t.Helper()
	select {
	case raw := <-conn.sendCh:
		var frame generated.MediaFrame
		require.NoError(t, json.Unmarshal(raw, &frame))
		require.Equal(t, "media", frame.Type)
		return frame
	case <-time.After(2 * time.Second):
		t.Fatal("live preview media frame did not reach its bound connection")
		return generated.MediaFrame{}
	}
}

func TestOfficePreview_LiveEmptyPartsOnlyWhenUnavailable(t *testing.T) {
	for _, outcome := range []string{"complete", "partial", "unavailable", "missing_preview"} {
		t.Run(outcome, func(t *testing.T) {
			h, _, _ := newTestWSHandler(t)
			t.Cleanup(h.Wait)
			conn := makeTestConn()
			bindTestConnToSession(h, "office-chat", "office-session", conn)
			msg := bus.OutboundMediaMessage{ChatID: "office-chat", SessionID: "office-session", ToolCallID: "call", Presentation: "page_thumbnails"}
			if outcome != "missing_preview" {
				reason := "renderer_failed"
				msg.Preview = &generated.MediaPreview{SourceName: "report.docx", SourceVersion: "a1b2c3d4e5f6", PageCount: 2, FirstPage: 1, LastPage: 2, Outcome: outcome, UnavailableReason: &reason}
			}
			err := newWebchatChannel(h).SendMedia(context.Background(), msg)
			if outcome != "unavailable" {
				require.ErrorContains(t, err, "has no stored thumbnails and is not marked unavailable")
				require.Empty(t, conn.sendCh)
				return
			}
			require.NoError(t, err)
			frame := officeReceiveMedia(t, conn)
			require.Empty(t, frame.Parts)
			require.Equal(t, msg.Preview, frame.Preview)
			require.Equal(t, "call", *frame.ToolCallId)
			require.Equal(t, "page_thumbnails", *frame.Presentation)
			// T-46: round-trip the GENERATED type; every new contract field survives.
			raw, err := json.Marshal(frame)
			require.NoError(t, err)
			var decoded generated.MediaFrame
			require.NoError(t, json.Unmarshal(raw, &decoded))
			require.Equal(t, frame, decoded)
		})
	}
}

func officeMediaProjection(frame generated.MediaFrame) generated.MediaFrame {
	// Sequence/history ownership are carrier metadata, not the preview group.
	return generated.MediaFrame{Type: frame.Type, SessionId: frame.SessionId, Parts: frame.Parts, ToolCallId: frame.ToolCallId, Presentation: frame.Presentation, Preview: frame.Preview}
}

func TestOfficePreview_LiveReplayAndPreparedHistoryKeepRepeatedGroups(t *testing.T) {
	fixture := newAttachFixture(t, "office-history-turn", "office-history-message")
	store := newTestFileMediaStore(t)
	fixture.loop.SetMediaStore(store)
	path := filepath.Join(t.TempDir(), "shared.png")
	historyCrossGroupPNG(t, path)
	ref, err := store.Store(path, media.MediaMeta{Filename: "report.docx", ContentType: "image/png", CleanupPolicy: media.CleanupPolicyForgetOnly}, media.SessionInlineScopePrefix+fixture.sid)
	require.NoError(t, err)
	conn := makeTestConn()
	bindTestConnToSession(fixture.h, "office-live", fixture.sid, conn)
	channel := newWebchatChannel(fixture.h)
	live := make([]generated.MediaFrame, 0, 3)
	for i, id := range []string{"preview-first", "screen-between", "preview-second"} {
		var tc session.ToolCall
		msg := bus.OutboundMediaMessage{ChatID: "office-live", SessionID: fixture.sid, Parts: []bus.MediaPart{{Ref: ref, Type: "image", Filename: "report.docx", ContentType: "image/png"}}}
		if i == 1 {
			tc = session.ToolCall{ID: session.ToolCallID(id), Tool: "browser_screenshot", Status: "success", Result: map[string]any{"media": []any{map[string]any{"ref": ref, "filename": "report.docx", "content_type": "image/png", "type": "image"}}}}
		} else {
			tc = previewToolCall(session.ToolCallID(id), "report.docx", "a1b2c3d4e5f6", 12, 1, 1, "complete", "", []map[string]any{thumbPart(ref, 1)})
			msg.ToolCallID = id
			msg.Presentation = "page_thumbnails"
			msg.Preview = &generated.MediaPreview{SourceName: "report.docx", SourceVersion: "a1b2c3d4e5f6", PageCount: 12, FirstPage: 1, LastPage: 1, Outcome: "complete"}
			msg.Parts[0].PageNumber = 1
			msg.Parts[0].Caption = "report.docx · page 1 of 12"
		}
		require.NoError(t, channel.SendMedia(context.Background(), msg))
		live = append(live, officeMediaProjection(officeReceiveMedia(t, conn)))
		require.NoError(t, fixture.store.AppendTranscriptStrict(fixture.sid, session.TranscriptEntry{ID: id, Type: session.EntryTypeToolCall, Role: "assistant", AgentID: "mia", Timestamp: time.Date(2026, 10, 31, 12, 0, i, 0, time.UTC), ViewMembership: session.ViewMembershipChat, ToolCalls: []session.ToolCall{tc}}))
	}
	require.Len(t, live, 3, "positive control: same physical file really produced two preview groups plus a screenshot")
	entries, err := fixture.store.ReadTranscript(fixture.sid)
	require.NoError(t, err)
	var replay []generated.MediaFrame
	_, err = streamReplay(context.Background(), fixture.sid, entries, computeReplayStats(entries), func(frame any) error {
		if m, ok := frame.(generated.MediaFrame); ok {
			replay = append(replay, officeMediaProjection(m))
		}
		return nil
	}, store, nil, nil, nil)
	require.NoError(t, err)
	require.Equal(t, live, replay, "whole replay must match live group fields and page captions")
	request, err := newHistoryPageRequest(fixture.sid, session.ChatHistoryPageOptions{}, historyPageProjectionDeps{MediaStore: store})
	require.NoError(t, err)
	prepared, err := request.Read(context.Background(), fixture.store)
	require.NoError(t, err)
	var snapshot []generated.MediaFrame
	for _, raw := range prepared.ReplayFrames {
		var kind struct {
			Type string `json:"type"`
		}
		require.NoError(t, json.Unmarshal(raw, &kind))
		if kind.Type == "media" {
			var frame generated.MediaFrame
			require.NoError(t, json.Unmarshal(raw, &frame))
			snapshot = append(snapshot, officeMediaProjection(frame))
		}
	}
	require.Equal(t, live, snapshot, "T-62: the actual shared REST/snapshot preparation must not deduplicate distinct tool-call groups by physical path")
}

func TestOfficePreview_RESTHistoryRetainsPreviewRecord(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	t.Cleanup(cleanup)
	store := api.agentLoop.GetSessionStore()
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	tc := previewToolCall("rest-preview", "report.docx", "a1b2c3d4e5f6", 12, 1, 4, "unavailable", "renderer_failed", nil)
	require.NoError(t, store.AppendTranscriptStrict(meta.ID, session.TranscriptEntry{ID: "rest-office-entry", Type: session.EntryTypeToolCall, Role: "assistant", AgentID: "mia", Timestamp: time.Now(), ViewMembership: session.ViewMembershipChat, ToolCalls: []session.ToolCall{tc}}))
	request, err := newHistoryPageRequest(meta.ID, session.ChatHistoryPageOptions{}, api.historyProjectionDeps(store, meta.ID))
	require.NoError(t, err)
	_, preparationErr := request.Read(context.Background(), store)
	t.Logf("actual REST history preparation diagnostic: %v", preparationErr)
	recorder := httptest.NewRecorder()
	api.HandleSessions(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+meta.ID+"/messages", nil))
	require.Equal(t, http.StatusOK, recorder.Code, "%s", recorder.Body.String())
	var response generated.SessionHistoryResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.Len(t, response.Entries, 1)
	require.Contains(t, recorder.Body.String(), `"presentation":"page_thumbnails"`)
	require.Contains(t, recorder.Body.String(), `"source_version":"a1b2c3d4e5f6"`)
	require.Contains(t, recorder.Body.String(), `"unavailable_reason":"renderer_failed"`)
}

func TestOfficePreview_RetryUnknownPreviewUsesSpecifiedRoute(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	t.Cleanup(cleanup)
	meta, err := api.agentLoop.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	control := httptest.NewRecorder()
	api.HandleSessions(control, httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+meta.ID, nil))
	require.Equal(t, http.StatusOK, control.Code, "session fixture must actually be reachable")
	recorder := httptest.NewRecorder()
	api.HandleSessions(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/sessions/"+meta.ID+"/previews/unknown-preview/retry", nil))
	if recorder.Code == http.StatusMethodNotAllowed {
		t.Fatal("BLOCKED: preview retry route not implemented — required by office-preview spec §5.2 / FR-038")
	}
	require.Equal(t, http.StatusNotFound, recorder.Code, "known session, unknown preview → 404, not generic method rejection")
}

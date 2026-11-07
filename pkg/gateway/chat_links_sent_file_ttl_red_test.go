package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/require"
)

// Q17 A: a sent file stays available after the store's MaxAge, and a chat
// whose transcript names a ref the store no longer knows still opens.
// BS-5.4: the reloaded card's media frame carries library_ref for notes/plan.md.
// A store error other than media.ErrNotFound stays the visible storage failure.
const sentFileGoneRef = "media://018f6e5a-7c3e-7a3e-8e3a-0a1b2c3d4e5f"

func sentFilePlan(t *testing.T, h *WSHandler) (home, plan string) {
	t.Helper()
	home = chatLinksHome(t, h)
	work, err := workspace.SafeWorkDir(home, chatLinksWorkspaceID)
	require.NoError(t, err)
	plan = filepath.Join(work, "notes", "plan.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(plan), 0o755))
	require.NoError(t, os.WriteFile(plan, []byte("plan"), 0o644))
	return home, plan
}

func sentFileAgedStore(t *testing.T) *media.FileMediaStore {
	t.Helper()
	store := media.NewFileMediaStoreWithCleanup(media.MediaCleanerConfig{
		MaxAge: time.Millisecond, Interval: time.Hour,
	})
	t.Cleanup(store.Stop)
	return store
}

func TestSentFile_StaysServedAndOnTheHistoryCardAfterMaxAge(t *testing.T) {
	fixture := newAttachFixture(t, "sent-file-ttl-turn", "sent-file-ttl-msg")
	_, plan := sentFilePlan(t, fixture.h)
	store := sentFileAgedStore(t)
	fixture.h.agentLoop.SetMediaStore(store)
	tool := tools.NewSendFileTool(filepath.Dir(filepath.Dir(plan)), false, 0, store)
	tool.SetContext("webchat", fixture.sid)
	result := tool.Execute(context.Background(), map[string]any{"path": plan})
	if result.IsError {
		t.Fatalf("instrument: send_file failed: %s", result.ForLLM)
	}
	if len(result.Media) != 1 {
		t.Fatalf("instrument: send_file media = %v, want one ref", result.Media)
	}
	ref := result.Media[0]
	if _, err := store.Resolve(ref); err != nil {
		t.Fatalf("instrument: send_file ref does not resolve before cleanup: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	removed := store.CleanExpired()
	if removed != 0 {
		t.Errorf("CleanExpired removed %d, want 0: a sent file older than MaxAge stays (Q17 A)", removed)
	}
	if _, err := store.Resolve(ref); err != nil {
		t.Errorf("sent file ref no longer resolves after MaxAge: %v", err)
	}
	api := pagingAPIForAttach(fixture)
	api.mediaStore = store
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/media/"+strings.TrimPrefix(ref, "media://"), nil)
	api.HandleMedia(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "plan" {
		t.Errorf("GET /media = %d %q, want 200 and the sent file bytes", rec.Code, rec.Body.String())
	}
	ws := chatLinksWorkspaceID
	require.NoError(t, fixture.store.SetMeta(fixture.sid, session.MetaPatch{WorkspaceID: &ws}))
	chatLinksSendFileEntry(t, fixture.store, fixture.sid, "sent-plan", "call-plan", ref, "plan.md", "text/markdown", "file")
	conn := fixture.newConn("sent-file-ttl-viewer")
	fixture.h.handleAttachSession(context.Background(), "sent-file-ttl-viewer", fixture.sid, nil, conn)
	frames := pagingDrain(t, conn)
	complete := indexOfType(frames, "catch_up_complete")
	if complete < 0 || frames[complete].Mode != "snapshot" {
		mode := ""
		if complete >= 0 {
			mode = frames[complete].Mode
		}
		t.Errorf("history attach mode = %q, want snapshot so the card can reload (BS-5.4)", mode)
	}
	part, raw, ok := sentFileMedia(frames, "plan.md")
	if !ok {
		t.Error("BS-5.4: history snapshot has no plan.md media frame after MaxAge")
		return
	}
	chatLinksRequireRef(t, part, raw)
}

func TestHistory_UnknownMediaRefStillOpens(t *testing.T) {
	fixture := newAttachFixture(t, "unknown-ref-turn", "unknown-ref-msg")
	store := newTestFileMediaStore(t)
	fixture.h.agentLoop.SetMediaStore(store)
	require.NoError(t, fixture.store.AppendTranscriptStrict(fixture.sid, session.TranscriptEntry{
		ID: "keep-text", Role: "user", Content: "Keep this plan", AgentID: "mia",
		Timestamp: time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC), ViewMembership: session.ViewMembershipChat,
	}))
	chatLinksSendFileEntry(t, fixture.store, fixture.sid, "gone-row", "call-gone", sentFileGoneRef, "gone.md", "text/markdown", "file")
	api := pagingAPIForAttach(fixture)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+fixture.sid+"/messages", nil)
	api.serveSessionHistory(rec, req.WithContext(context.Background()), fixture.sid)
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, "Keep this plan") || !strings.Contains(body, "send_file") {
		t.Errorf("history page = %d %s, want 200 with the text and the send_file row (Q17 A)", rec.Code, body)
	}
	if strings.Contains(body, "session storage failed") {
		t.Errorf("unknown ref failed the page: %s", body)
	}
	conn := fixture.newConn("unknown-ref-viewer")
	fixture.h.handleAttachSession(context.Background(), "unknown-ref-viewer", fixture.sid, nil, conn)
	frames := pagingDrain(t, conn)
	complete := indexOfType(frames, "catch_up_complete")
	if complete < 0 || frames[complete].Mode != "snapshot" {
		mode := ""
		if complete >= 0 {
			mode = frames[complete].Mode
		}
		t.Fatalf("attach mode = %q, want snapshot with no error frame (Q17 A)", mode)
	}
	var goneResult []byte
	for _, frame := range frames {
		if frame.Type == "error" {
			t.Fatalf("attach sent an error frame: %s", frame.raw)
		}
		// Q17 A / docs/protocol/websocket-protocol.md: the media frame for a
		// ref the store does not know is absent. The tool_call_result for that
		// call stays, and its saved result is the transcript result unchanged.
		// The card's URL is /api/v1/media/<id>, not the media:// ref, so the
		// frame type is the check. This attach has no other file.
		if frame.Type == "media" {
			t.Fatalf("unknown ref still produced a media frame: %s", frame.raw)
		}
		if frame.Type != "tool_call_result" {
			continue
		}
		var got generated.ToolCallResultFrame
		if err := json.Unmarshal(frame.raw, &got); err != nil {
			t.Fatalf("tool_call_result: %v", err)
		}
		if got.CallId != "call-gone" {
			continue
		}
		goneResult = frame.raw
		want, err := json.Marshal(map[string]any{"media": []any{map[string]any{
			"ref": sentFileGoneRef, "filename": "gone.md", "content_type": "text/markdown", "type": "file",
		}}})
		if err != nil {
			t.Fatal(err)
		}
		gotResult, err := json.Marshal(got.Result)
		if err != nil {
			t.Fatal(err)
		}
		if string(gotResult) != string(want) || got.Tool != "send_file" || got.Status != "success" {
			t.Fatalf("tool_call_result for call-gone saved result = tool %q status %q %s, want send_file success %s", got.Tool, got.Status, gotResult, want)
		}
	}
	if goneResult == nil {
		t.Fatal("attach snapshot dropped the tool_call_result for call-gone")
	}
	if !sentFileSawText(frames, "Keep this plan") {
		t.Fatal("attach snapshot dropped the text row")
	}
}

func TestHistory_StoreErrorOtherThanNotFoundStillFails(t *testing.T) {
	fixture := newAttachFixture(t, "store-fault-turn", "store-fault-msg")
	store := newTestFileMediaStore(t)
	fixture.h.agentLoop.SetMediaStore(store)
	// A workspace ref resolved with no caller workspace is ErrWorkspaceContextRequired,
	// not ErrNotFound (pkg/media/resolve.go). The page must stay a visible storage failure.
	chatLinksSendFileEntry(t, fixture.store, fixture.sid, "fault-row", "call-fault",
		media.WorkspaceRefPrefix+"W/gone", "gone.md", "text/markdown", "file")
	api := pagingAPIForAttach(fixture)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+fixture.sid+"/messages", nil)
	api.serveSessionHistory(rec, req.WithContext(context.Background()), fixture.sid)
	const want = `{"error":"This session could not be read because session storage failed. Check disk space and permissions, then retry. Details are in the server log."}` + "\n"
	if rec.Code != http.StatusInternalServerError || rec.Body.String() != want {
		t.Fatalf("store error page = %d %s, want 500 %s", rec.Code, rec.Body.String(), want)
	}
}

func TestHistory_UnknownRefDoesNotDropTheGoodCard(t *testing.T) {
	fixture := newAttachFixture(t, "neighbour-turn", "neighbour-msg")
	_, plan := sentFilePlan(t, fixture.h)
	store := newTestFileMediaStore(t)
	fixture.h.agentLoop.SetMediaStore(store)
	ref := chatLinksStore(t, store, plan, "plan.md", "text/markdown", "tool:send_file:webchat:"+fixture.sid)
	ws := chatLinksWorkspaceID
	require.NoError(t, fixture.store.SetMeta(fixture.sid, session.MetaPatch{WorkspaceID: &ws}))
	require.NoError(t, fixture.store.AppendTranscriptStrict(fixture.sid, session.TranscriptEntry{
		ID: "keep-text", Role: "user", Content: "Keep this plan", AgentID: "mia",
		Timestamp: time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC), ViewMembership: session.ViewMembershipChat,
	}))
	chatLinksSendFileEntry(t, fixture.store, fixture.sid, "good-row", "call-good", ref, "plan.md", "text/markdown", "file")
	chatLinksSendFileEntry(t, fixture.store, fixture.sid, "gone-row", "call-gone", sentFileGoneRef, "gone.md", "text/markdown", "file")
	api := pagingAPIForAttach(fixture)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+fixture.sid+"/messages", nil)
	api.serveSessionHistory(rec, req.WithContext(context.Background()), fixture.sid)
	if rec.Code != http.StatusOK {
		t.Errorf("history page = %d %s, want 200 when one ref is unknown and one is good", rec.Code, rec.Body.String())
	}
	conn := fixture.newConn("neighbour-viewer")
	fixture.h.handleAttachSession(context.Background(), "neighbour-viewer", fixture.sid, nil, conn)
	frames := pagingDrain(t, conn)
	part, raw, ok := sentFileMedia(frames, "plan.md")
	if !ok {
		t.Error("the good card was dropped with the unknown ref")
	} else {
		chatLinksRequireRef(t, part, raw)
	}
	if _, _, gone := sentFileMedia(frames, "gone.md"); gone {
		t.Error("unknown ref still produced a gone.md card")
	}
}

func sentFileMedia(frames []wireFrame, filename string) (generated.MediaPart, []byte, bool) {
	for _, frame := range frames {
		if frame.Type != "media" {
			continue
		}
		var wire generated.MediaFrame
		if err := json.Unmarshal(frame.raw, &wire); err != nil {
			continue
		}
		for _, part := range wire.Parts {
			if part.Filename == filename {
				return part, frame.raw, true
			}
		}
	}
	return generated.MediaPart{}, nil, false
}

func sentFileSawText(frames []wireFrame, text string) bool {
	for _, frame := range frames {
		if frame.Type == "replay_message" && frame.Content == text {
			return true
		}
		if strings.Contains(string(frame.raw), text) {
			return true
		}
	}
	return false
}

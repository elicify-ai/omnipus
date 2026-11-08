package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
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
// BS-5.4: the reloaded card's media frame is present. Ported to landing/fix-1685
// without the chat-links library_ref assertion: MediaPart has no library_ref
// on the release line.
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
	part, _, ok := sentFileMedia(frames, "plan.md")
	if !ok {
		t.Error("BS-5.4: history snapshot has no plan.md media frame after MaxAge")
		return
	}
	// The card must carry the saved facts AND a working download: the URL the
	// card actually names is requested here, not one rebuilt from the ref
	// (docs/using-omnipus-ui.md: a sent file's card and download keep working).
	if part.Filename != "plan.md" || part.Type != "file" || part.ContentType != "text/markdown" {
		t.Errorf("plan.md card = filename %q type %q content_type %q, want plan.md file text/markdown as saved", part.Filename, part.Type, part.ContentType)
	}
	recCard := httptest.NewRecorder()
	reqCard := httptest.NewRequest(http.MethodGet, part.Url, nil)
	api.HandleMedia(recCard, reqCard)
	if recCard.Code != http.StatusOK || recCard.Body.String() != "plan" {
		t.Errorf("GET the card's own URL %s = %d %q, want 200 and the sent file bytes: the card's download must work", part.Url, recCard.Code, recCard.Body.String())
	}
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
	// AC-D2-1: the saved tool row stays unchanged on every carrier. Decode the
	// page and compare the addressed call's saved result exactly — a substring
	// of the tool name accepts a rewritten result (the WebSocket frame below is
	// checked the same way).
	var page generated.SessionHistoryResponse
	if err := json.Unmarshal([]byte(body), &page); err != nil {
		t.Fatalf("history page decode: %v", err)
	}
	wantRestResult, err := json.Marshal(map[string]any{"media": []any{map[string]any{
		"ref": sentFileGoneRef, "filename": "gone.md", "content_type": "text/markdown", "type": "file",
	}}})
	if err != nil {
		t.Fatal(err)
	}
	sawRestResult := false
	for _, entry := range page.Entries {
		if entry.ToolCalls == nil {
			continue
		}
		for _, call := range *entry.ToolCalls {
			if call.Id != "call-gone" {
				continue
			}
			sawRestResult = true
			gotResult, err := json.Marshal(call.Result)
			if err != nil {
				t.Fatal(err)
			}
			if call.Tool != "send_file" || call.Status != "success" || string(gotResult) != string(wantRestResult) {
				t.Errorf("REST saved tool result for call-gone = tool %q status %q %s, want send_file success %s unchanged", call.Tool, call.Status, gotResult, wantRestResult)
			}
		}
	}
	if !sawRestResult {
		t.Error("REST history page has no saved tool result row for call-gone")
	}
	var logBuf bytes.Buffer
	oldHandler := slog.Default().Handler()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	defer slog.SetDefault(slog.New(oldHandler))
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
	// docs/troubleshooting.md: the omission is not silent — the gateway warns
	// with the session and the file's reference. This attach has exactly one
	// unknown ref, so exactly one such warning must exist.
	warned := 0
	for _, line := range strings.Split(strings.TrimSuffix(logBuf.String(), "\n"), "\n") {
		if line == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			continue
		}
		if record["msg"] != "replay: skipped file card, media ref unknown to the store" {
			continue
		}
		warned++
		if record["session_id"] != fixture.sid || record["ref"] != sentFileGoneRef {
			t.Errorf("missing-card warning = session_id %v ref %v, want session %q and ref %q", record["session_id"], record["ref"], fixture.sid, sentFileGoneRef)
		}
	}
	if warned != 1 {
		t.Errorf("missing-card warning count = %d, want exactly one for the skipped unknown ref", warned)
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
	if _, _, ok := sentFileMedia(frames, "plan.md"); !ok {
		t.Error("the good card was dropped with the unknown ref")
	}
	if _, _, gone := sentFileMedia(frames, "gone.md"); gone {
		t.Error("unknown ref still produced a gone.md card")
	}
}

// chatLinksSendFileEntryResult appends a send_file tool call whose saved
// result is exactly the given map (the single-ref helper above covers the
// common one-descriptor shape).
func chatLinksSendFileEntryResult(t *testing.T, store *session.UnifiedStore, sid, id, callID string, result map[string]any) {
	t.Helper()
	require.NoError(t, store.AppendTranscriptStrict(sid, session.TranscriptEntry{
		ID: id, Type: session.EntryTypeToolCall, Role: "assistant", AgentID: "mia",
		Timestamp: time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC), ViewMembership: session.ViewMembershipChat,
		ToolCalls: []session.ToolCall{{
			ID: session.ToolCallID(callID), Tool: "send_file", Status: "success",
			Parameters: map[string]any{}, Result: result,
		}},
	}))
}

func TestHistory_SkipsOnlyTheUnknownRefInsideOneToolResult(t *testing.T) {
	// One saved send_file result names a live file and a file the store no
	// longer knows, in the same media array — in both orders. Only the unknown
	// part is skipped (docs/protocol/websocket-protocol.md): the good card
	// keeps its saved facts and working URL, the unknown card is absent, and
	// the saved tool result is unchanged on both carriers.
	for _, goneFirst := range []bool{true, false} {
		name := "good-then-gone"
		if goneFirst {
			name = "gone-then-good"
		}
		t.Run(name, func(t *testing.T) {
			fixture := newAttachFixture(t, "sibling-"+name+"-turn", "sibling-"+name+"-msg")
			_, plan := sentFilePlan(t, fixture.h)
			store := newTestFileMediaStore(t)
			fixture.h.agentLoop.SetMediaStore(store)
			goodRef := chatLinksStore(t, store, plan, "plan.md", "text/markdown", "tool:send_file:webchat:"+fixture.sid)
			good := map[string]any{"ref": goodRef, "filename": "plan.md", "content_type": "text/markdown", "type": "file"}
			gone := map[string]any{"ref": sentFileGoneRef, "filename": "gone.md", "content_type": "text/markdown", "type": "file"}
			descriptors := []any{gone, good}
			if !goneFirst {
				descriptors = []any{good, gone}
			}
			ws := chatLinksWorkspaceID
			require.NoError(t, fixture.store.SetMeta(fixture.sid, session.MetaPatch{WorkspaceID: &ws}))
			chatLinksSendFileEntryResult(t, fixture.store, fixture.sid, "sibling-row", "call-sibling", map[string]any{"media": descriptors})
			api := pagingAPIForAttach(fixture)
			api.mediaStore = store
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+fixture.sid+"/messages", nil)
			api.serveSessionHistory(rec, req.WithContext(context.Background()), fixture.sid)
			if rec.Code != http.StatusOK {
				t.Errorf("history page = %d %s, want 200 when one descriptor of a result is unknown", rec.Code, rec.Body.String())
			}
			var page generated.SessionHistoryResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
				t.Fatalf("history page decode: %v", err)
			}
			wantResult, err := json.Marshal(map[string]any{"media": descriptors})
			if err != nil {
				t.Fatal(err)
			}
			sawRest := false
			for _, entry := range page.Entries {
				if entry.ToolCalls == nil {
					continue
				}
				for _, call := range *entry.ToolCalls {
					if call.Id != "call-sibling" {
						continue
					}
					sawRest = true
					gotResult, err := json.Marshal(call.Result)
					if err != nil {
						t.Fatal(err)
					}
					if call.Tool != "send_file" || call.Status != "success" || string(gotResult) != string(wantResult) {
						t.Errorf("REST saved result for call-sibling = tool %q status %q %s, want send_file success %s unchanged", call.Tool, call.Status, gotResult, wantResult)
					}
				}
			}
			if !sawRest {
				t.Error("REST page dropped the saved tool result for call-sibling")
			}
			conn := fixture.newConn("sibling-viewer-" + name)
			fixture.h.handleAttachSession(context.Background(), "sibling-viewer-"+name, fixture.sid, nil, conn)
			frames := pagingDrain(t, conn)
			complete := indexOfType(frames, "catch_up_complete")
			if complete < 0 || frames[complete].Mode != "snapshot" {
				mode := ""
				if complete >= 0 {
					mode = frames[complete].Mode
				}
				t.Fatalf("attach mode = %q, want snapshot with the good sibling still on it", mode)
			}
			var mediaFrames int
			var parts []generated.MediaPart
			for _, frame := range frames {
				if frame.Type != "media" {
					continue
				}
				mediaFrames++
				var wire generated.MediaFrame
				if err := json.Unmarshal(frame.raw, &wire); err != nil {
					t.Fatalf("media frame decode: %v", err)
				}
				parts = append(parts, wire.Parts...)
			}
			if mediaFrames != 1 || len(parts) != 1 {
				t.Fatalf("media frames = %d with %d parts, want exactly one frame carrying exactly the good part", mediaFrames, len(parts))
			}
			part := parts[0]
			if part.Filename != "plan.md" || part.Type != "file" || part.ContentType != "text/markdown" {
				t.Errorf("surviving part = filename %q type %q content_type %q, want the good plan.md file as saved", part.Filename, part.Type, part.ContentType)
			}
			recCard := httptest.NewRecorder()
			reqCard := httptest.NewRequest(http.MethodGet, part.Url, nil)
			api.HandleMedia(recCard, reqCard)
			if recCard.Code != http.StatusOK || recCard.Body.String() != "plan" {
				t.Errorf("GET the surviving card's URL %s = %d %q, want 200 and the good file bytes", part.Url, recCard.Code, recCard.Body.String())
			}
			if _, _, goneCard := sentFileMedia(frames, "gone.md"); goneCard {
				t.Error("the unknown descriptor still produced a gone.md card")
			}
			var sawSaved bool
			for _, frame := range frames {
				if frame.Type != "tool_call_result" {
					continue
				}
				var got generated.ToolCallResultFrame
				if err := json.Unmarshal(frame.raw, &got); err != nil {
					t.Fatalf("tool_call_result: %v", err)
				}
				if got.CallId != "call-sibling" {
					continue
				}
				sawSaved = true
				gotResult, err := json.Marshal(got.Result)
				if err != nil {
					t.Fatal(err)
				}
				if got.Tool != "send_file" || got.Status != "success" || string(gotResult) != string(wantResult) {
					t.Errorf("saved tool result for call-sibling = tool %q status %q %s, want send_file success %s unchanged", got.Tool, got.Status, gotResult, wantResult)
				}
			}
			if !sawSaved {
				t.Fatal("attach snapshot dropped the saved tool result for call-sibling")
			}
		})
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

// Helpers ported verbatim from the chat-links test pack
// (chat_links_library_ref_red_test.go), which the release line does not carry.
const chatLinksWorkspaceID = "W"

func chatLinksHome(t *testing.T, h *WSHandler) string {
	t.Helper()
	home := h.agentLoop.GetConfig().Agents.Defaults.Home
	require.NotEmpty(t, home, "instrument: agent home")
	h.home = home
	return home
}

func chatLinksStore(t *testing.T, store *media.FileMediaStore, path, filename, contentType, scope string) string {
	t.Helper()
	ref, err := store.Store(path, media.MediaMeta{
		Filename: filename, ContentType: contentType, CleanupPolicy: media.CleanupPolicyForgetOnly,
	}, scope)
	require.NoError(t, err)
	resolved, resolveErr := store.Resolve(ref)
	require.NoError(t, resolveErr)
	require.Equal(t, path, resolved, "instrument: the ref must resolve to the placed file")
	return ref
}

func chatLinksSendFileEntry(t *testing.T, store *session.UnifiedStore, sid, id, callID, ref, filename, contentType, mediaType string) {
	t.Helper()
	require.NoError(t, store.AppendTranscriptStrict(sid, session.TranscriptEntry{
		ID: id, Type: session.EntryTypeToolCall, Role: "assistant", AgentID: "mia",
		Timestamp: time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC), ViewMembership: session.ViewMembershipChat,
		ToolCalls: []session.ToolCall{{
			ID: session.ToolCallID(callID), Tool: "send_file", Status: "success",
			Parameters: map[string]any{},
			Result: map[string]any{"media": []any{map[string]any{
				"ref": ref, "filename": filename, "content_type": contentType, "type": mediaType,
			}}},
		}},
	}))
}

func TestSentFile_ReusedUnpinnedUploadStaysAfterMaxAge(t *testing.T) {
	// Q29 A: a file already registered under an unpinned upload scope, then
	// sent with send_file, keeps the ref send_file returned. After MaxAge it
	// still resolves, /media serves it, and the history card is present.
	// An upload that was never sent still expires.
	fixture := newAttachFixture(t, "reused-upload-turn", "reused-upload-msg")
	_, plan := sentFilePlan(t, fixture.h)
	resolvedPath, err := filepath.EvalSymlinks(plan)
	require.NoError(t, err)
	store := sentFileAgedStore(t)
	fixture.h.agentLoop.SetMediaStore(store)
	uploadRef, err := store.Store(resolvedPath, media.MediaMeta{
		Filename: "plan.md", ContentType: "text/markdown", Source: "upload:webchat",
		CleanupPolicy: media.CleanupPolicyForgetOnly,
	}, "upload:"+fixture.sid)
	require.NoError(t, err)
	controlPath := filepath.Join(filepath.Dir(resolvedPath), "upload-only.md")
	require.NoError(t, os.WriteFile(controlPath, []byte("temp"), 0o644))
	controlRef, err := store.Store(controlPath, media.MediaMeta{
		Filename: "upload-only.md", CleanupPolicy: media.CleanupPolicyForgetOnly,
	}, "upload:"+fixture.sid)
	require.NoError(t, err)
	time.Sleep(20 * time.Millisecond)
	tool := tools.NewSendFileTool(filepath.Dir(filepath.Dir(plan)), false, 0, store)
	tool.SetContext("webchat", fixture.sid)
	result := tool.Execute(context.Background(), map[string]any{"path": plan})
	if result.IsError {
		t.Fatalf("instrument: send_file failed: %s", result.ForLLM)
	}
	if len(result.Media) != 1 || result.Media[0] != uploadRef {
		t.Fatalf("instrument: send_file ref = %v, want the existing upload ref %s", result.Media, uploadRef)
	}
	store.CleanExpired()
	if _, resolveErr := store.Resolve(controlRef); !errors.Is(resolveErr, media.ErrNotFound) {
		t.Errorf("control: unsent upload error = %v, want ErrNotFound", resolveErr)
	}
	if got, resolveErr := store.Resolve(uploadRef); resolveErr != nil || got != resolvedPath {
		t.Errorf("reused upload ref after MaxAge = %q %v, want the sent file", got, resolveErr)
	}
	api := pagingAPIForAttach(fixture)
	api.mediaStore = store
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/media/"+strings.TrimPrefix(uploadRef, "media://"), nil)
	api.HandleMedia(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "plan" {
		t.Errorf("GET /media = %d %q, want 200 and the sent file bytes", rec.Code, rec.Body.String())
	}
	ws := chatLinksWorkspaceID
	require.NoError(t, fixture.store.SetMeta(fixture.sid, session.MetaPatch{WorkspaceID: &ws}))
	chatLinksSendFileEntry(t, fixture.store, fixture.sid, "sent-plan", "call-plan", uploadRef, "plan.md", "text/markdown", "file")
	conn := fixture.newConn("reused-upload-viewer")
	fixture.h.handleAttachSession(context.Background(), "reused-upload-viewer", fixture.sid, nil, conn)
	frames := pagingDrain(t, conn)
	complete := indexOfType(frames, "catch_up_complete")
	if complete < 0 || frames[complete].Mode != "snapshot" {
		mode := ""
		if complete >= 0 {
			mode = frames[complete].Mode
		}
		t.Errorf("history attach mode = %q, want snapshot so the card can reload", mode)
	}
	part, _, ok := sentFileMedia(frames, "plan.md")
	if !ok {
		t.Error("history snapshot has no plan.md card for the reused upload after MaxAge")
		return
	}
	// The reused ref's card must name a working download too (see the fresh-file
	// test above): request the URL the card itself carries.
	if part.Filename != "plan.md" || part.Type != "file" || part.ContentType != "text/markdown" {
		t.Errorf("reused-upload card = filename %q type %q content_type %q, want plan.md file text/markdown as saved", part.Filename, part.Type, part.ContentType)
	}
	recCard := httptest.NewRecorder()
	reqCard := httptest.NewRequest(http.MethodGet, part.Url, nil)
	api.HandleMedia(recCard, reqCard)
	if recCard.Code != http.StatusOK || recCard.Body.String() != "plan" {
		t.Errorf("GET the reused card's own URL %s = %d %q, want 200 and the sent file bytes", part.Url, recCard.Code, recCard.Body.String())
	}
}

func TestSentFile_SentAcrossHandoverStaysServedFromTheLiveStore(t *testing.T) {
	// SFH-3: an ongoing turn keeps its outgoing media store across a reload
	// (HandoverTo); the live successor is what /media and history actually
	// serve. When the successor's copy of a still-unsent upload has expired and
	// the turn then sends that file through the outgoing store, the send is a
	// success only if the LIVE store serves the ref the tool reports
	// (docs/using-omnipus-ui.md: a sent file does not expire — its card and
	// download keep working after a reload). Either the pin reaches the live
	// store or the tool re-stores it there; a successor error swallowed to nil
	// reports a file the live store cannot serve, and this test rejects that.
	fixture := newAttachFixture(t, "handover-send-turn", "handover-send-msg")
	_, plan := sentFilePlan(t, fixture.h)
	resolvedPath, err := filepath.EvalSymlinks(plan)
	require.NoError(t, err)
	oldStore := sentFileAgedStore(t)
	uploadRef, err := oldStore.Store(resolvedPath, media.MediaMeta{
		Filename: "plan.md", ContentType: "text/markdown", Source: "upload:webchat",
		CleanupPolicy: media.CleanupPolicyForgetOnly,
	}, "upload:"+fixture.sid)
	require.NoError(t, err)
	liveStore := sentFileAgedStore(t)
	oldStore.HandoverTo(liveStore)
	fixture.h.agentLoop.SetMediaStore(liveStore)
	// The outgoing cleaner is retired by the reload; only the live copy can
	// expire. The upload was never sent, so the live copy expiring is the
	// required expiry control, deterministically aged past MaxAge.
	oldStore.Stop()
	time.Sleep(20 * time.Millisecond)
	if removed := liveStore.CleanExpired(); removed == 0 {
		t.Fatalf("instrument: live store expired nothing, want the unsent upload gone")
	}
	if _, liveErr := liveStore.Resolve(uploadRef); !errors.Is(liveErr, media.ErrNotFound) {
		t.Fatalf("instrument: live successor still holds the unsent upload: %v", liveErr)
	}
	if _, oldErr := oldStore.Resolve(uploadRef); oldErr != nil {
		t.Fatalf("instrument: outgoing store lost its own copy: %v", oldErr)
	}
	tool := tools.NewSendFileTool(filepath.Dir(filepath.Dir(plan)), false, 0, oldStore)
	tool.SetContext("webchat", fixture.sid)
	result := tool.Execute(context.Background(), map[string]any{"path": plan})
	if result.IsError {
		t.Fatalf("instrument: send_file through the outgoing store failed: %s", result.ForLLM)
	}
	if len(result.Media) != 1 {
		t.Fatalf("instrument: send_file media = %v, want one ref", result.Media)
	}
	sentRef := result.Media[0]
	if got, resolveErr := liveStore.Resolve(sentRef); resolveErr != nil || got != resolvedPath {
		t.Errorf("sent ref %s in the LIVE store = %q %v, want the sent file: a successful send must stay served from the store users hit (SFH-3)", sentRef, got, resolveErr)
	}
	api := pagingAPIForAttach(fixture)
	api.mediaStore = liveStore
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/media/"+strings.TrimPrefix(sentRef, "media://"), nil)
	api.HandleMedia(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "plan" {
		t.Errorf("live GET /media = %d %q, want 200 and the sent file bytes", rec.Code, rec.Body.String())
	}
	ws := chatLinksWorkspaceID
	require.NoError(t, fixture.store.SetMeta(fixture.sid, session.MetaPatch{WorkspaceID: &ws}))
	chatLinksSendFileEntry(t, fixture.store, fixture.sid, "handover-row", "call-handover", sentRef, "plan.md", "text/markdown", "file")
	conn := fixture.newConn("handover-viewer")
	fixture.h.handleAttachSession(context.Background(), "handover-viewer", fixture.sid, nil, conn)
	frames := pagingDrain(t, conn)
	complete := indexOfType(frames, "catch_up_complete")
	if complete < 0 || frames[complete].Mode != "snapshot" {
		mode := ""
		if complete >= 0 {
			mode = frames[complete].Mode
		}
		t.Errorf("history attach mode = %q, want snapshot with the sent card present", mode)
	}
	part, _, ok := sentFileMedia(frames, "plan.md")
	if !ok {
		t.Error("history snapshot has no plan.md card for the file sent across the handover")
		return
	}
	recCard := httptest.NewRecorder()
	reqCard := httptest.NewRequest(http.MethodGet, part.Url, nil)
	api.HandleMedia(recCard, reqCard)
	if recCard.Code != http.StatusOK || recCard.Body.String() != "plan" {
		t.Errorf("GET the replayed card's URL %s = %d %q, want 200 and the sent file bytes", part.Url, recCard.Code, recCard.Body.String())
	}
}

func TestHistory_RegistryPermissionErrorIsNotAMissingFile(t *testing.T) {
	// Q30 A: only a file that is not there may be treated as gone. A registry
	// load that cannot inspect a registered file (here, no search permission
	// on its parent) must keep that cause. The spec states no exact sentence,
	// so this asserts the error class and that the cause is kept.
	home := t.TempDir()
	parent := filepath.Join(home, "hidden")
	require.NoError(t, os.MkdirAll(parent, 0o755))
	plan := filepath.Join(parent, "plan.md")
	require.NoError(t, os.WriteFile(plan, []byte("plan"), 0o644))
	// Each phase below gets its own temporary home via t.Setenv — never a
	// restore to the saved original: when OMNIPUS_HOME is unset in the
	// environment the saved value is "", os.Setenv leaves the variable present
	// but empty, and ensureIsolatedTestOmnipusHome
	// (pkg/gateway/test_agent_loop_helper_test.go::ensureIsolatedTestOmnipusHome)
	// then reads that as this test managing its own home and does nothing. The
	// gateway falls back to $HOME/.omnipus and TestMain's leak guard
	// (pkg/gateway/testmain_test.go::TestMain) fails the whole run although
	// every assertion here passed (#1685 round 3, 2026-11-08). t.Setenv
	// restores the variable at test end and never writes "".
	t.Setenv("OMNIPUS_HOME", home)
	stored := media.NewFileMediaStore()
	t.Cleanup(stored.Stop)
	ref, err := stored.Store(plan, media.MediaMeta{
		Filename: "plan.md", ContentType: "text/markdown", CleanupPolicy: media.CleanupPolicyForgetOnly,
	}, "tool:send_file:webchat:perm-chat")
	require.NoError(t, err)
	visiblePath := filepath.Join(home, "visible.md")
	require.NoError(t, os.WriteFile(visiblePath, []byte("visible"), 0o644))
	visibleRef, err := stored.Store(visiblePath, media.MediaMeta{
		Filename: "visible.md", ContentType: "text/plain", CleanupPolicy: media.CleanupPolicyForgetOnly,
	}, "tool:send_file:webchat:perm-chat")
	require.NoError(t, err)
	stored.SaveRegistry()
	require.NoError(t, os.Chmod(parent, 0))
	t.Cleanup(func() { _ = os.Chmod(parent, 0o755) })
	if _, statErr := os.Stat(plan); !errors.Is(statErr, fs.ErrPermission) {
		t.Fatalf("instrument: hiding the parent did not deny search: %v", statErr)
	}
	loaded := media.NewFileMediaStore()
	t.Cleanup(loaded.Stop)
	loadErr := loaded.LoadRegistry()
	require.NoError(t, os.Chmod(parent, 0o755))
	if loadErr == nil || !errors.Is(loadErr, fs.ErrPermission) {
		t.Errorf("LoadRegistry error = %v, want a permission error and not a successful drop", loadErr)
	}
	if _, resolveErr := loaded.Resolve(ref); resolveErr == nil || errors.Is(resolveErr, media.ErrNotFound) || !errors.Is(resolveErr, fs.ErrPermission) {
		t.Errorf("Resolve after a permission failure = %v, want the permission cause, not ErrNotFound", resolveErr)
	}
	// docs/troubleshooting.md: the failed row survives the next registry save,
	// and once the cause is fixed, a restart brings the card and download back.
	// A healthy row saved alongside it keeps loading too.
	loaded.SaveRegistry()
	restarted := media.NewFileMediaStore()
	t.Cleanup(restarted.Stop)
	require.NoError(t, restarted.LoadRegistry(), "restart after the permission is fixed must load the whole registry")
	if got, resolveErr := restarted.Resolve(ref); resolveErr != nil || got != plan {
		t.Errorf("restart after fixing the cause: ref %s resolves to %q %v, want the original file back", ref, got, resolveErr)
	}
	if got, resolveErr := restarted.Resolve(visibleRef); resolveErr != nil || got != visiblePath {
		t.Errorf("restart: healthy row resolves to %q %v, want its file", got, resolveErr)
	}
	// The gateway phase gets a fresh home of its own: the fixture's helper must
	// see a real path, and this phase's media registry stays out of the
	// fixture's home, as the helper would have arranged itself.
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	sentFileExpectVisibleStoreFailure(t, loaded, ref, "perm-turn", "perm-msg", "perm-viewer")

	// Control: a file that is actually gone is still skipped, on REST and attach.
	controlHome := t.TempDir()
	t.Setenv("OMNIPUS_HOME", controlHome)
	gonePath := filepath.Join(controlHome, "gone.md")
	require.NoError(t, os.WriteFile(gonePath, []byte("gone"), 0o644))
	origin := media.NewFileMediaStore()
	t.Cleanup(origin.Stop)
	goneRef, err := origin.Store(gonePath, media.MediaMeta{
		Filename: "gone.md", ContentType: "text/markdown", CleanupPolicy: media.CleanupPolicyForgetOnly,
	}, "tool:send_file:webchat:gone-chat")
	require.NoError(t, err)
	origin.SaveRegistry()
	require.NoError(t, os.Remove(gonePath))
	skipped := media.NewFileMediaStore()
	t.Cleanup(skipped.Stop)
	if err := skipped.LoadRegistry(); err != nil {
		t.Fatalf("instrument: loading a registry whose file is gone: %v", err)
	}
	if _, resolveErr := skipped.Resolve(goneRef); !errors.Is(resolveErr, media.ErrNotFound) {
		t.Errorf("control: missing file error = %v, want ErrNotFound", resolveErr)
	}
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	sentFileExpectSkippedMissingRef(t, skipped, goneRef)
}

func sentFileExpectVisibleStoreFailure(t *testing.T, store *media.FileMediaStore, ref, turn, msg, viewer string) {
	t.Helper()
	fixture := newAttachFixture(t, turn, msg)
	fixture.h.agentLoop.SetMediaStore(store)
	require.NoError(t, fixture.store.AppendTranscriptStrict(fixture.sid, session.TranscriptEntry{
		ID: "keep-text", Role: "user", Content: "Keep this plan", AgentID: "mia",
		Timestamp: time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC), ViewMembership: session.ViewMembershipChat,
	}))
	chatLinksSendFileEntry(t, fixture.store, fixture.sid, "perm-row", "call-perm", ref, "plan.md", "text/markdown", "file")
	api := pagingAPIForAttach(fixture)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+fixture.sid+"/messages", nil)
	api.serveSessionHistory(rec, req.WithContext(context.Background()), fixture.sid)
	// The visible failure is the documented one, not merely a non-200: a
	// permission-caused projection failure returns the fixed sanitized storage
	// copy (docs/troubleshooting.md; same contract the store-error test pins).
	const wantBody = `{"error":"This session could not be read because session storage failed. Check disk space and permissions, then retry. Details are in the server log."}` + "\n"
	if rec.Code != http.StatusInternalServerError || rec.Body.String() != wantBody {
		t.Errorf("history page = %d %s, want the documented storage failure 500 %q", rec.Code, rec.Body.String(), wantBody)
	}
	conn := fixture.newConn(viewer)
	fixture.h.handleAttachSession(context.Background(), viewer, fixture.sid, nil, conn)
	frames := pagingDrain(t, conn)
	// Attach shape per docs/protocol/websocket-protocol.md (history_state): the
	// failure is visible as unavailable history with the fixed user message and
	// one live_only completion after it — never a snapshot, a replayed success,
	// a turn error frame, or silence.
	require.Equal(t, -1, indexOfType(frames, "session_snapshot"), "a failed history must never send a wipe")
	require.Equal(t, -1, indexOfType(frames, "replay_message"), "no fabricated reconstruction of unreadable history")
	require.Equal(t, -1, indexOfType(frames, "done"), "a history failure must not claim replay success")
	require.Equal(t, -1, indexOfType(frames, "error"), "history_state carries the failure, never a generic turn error frame")
	stateCount, completeCount := 0, 0
	for _, frame := range frames {
		switch frame.Type {
		case "session_state":
			stateCount++
			var state generated.SessionStateFrame
			require.NoError(t, json.Unmarshal(frame.raw, &state))
			require.Equal(t, &fixture.sid, state.SessionId)
			require.NotNil(t, state.HistoryState)
			require.Equal(t, "unavailable", state.HistoryState.Status)
			require.NotNil(t, state.HistoryState.Reason)
			require.Contains(t, []string{"index_missing", "index_inconsistent", "catchup_limit", "storage_error", "projection_error"}, *state.HistoryState.Reason, "reason is sanitized wire vocabulary, never a free-text cause")
			require.NotNil(t, state.HistoryState.Message)
			require.Equal(t, "This session could not be read because session storage failed. Check disk space and permissions, then retry. Details are in the server log.", *state.HistoryState.Message)
			require.NotNil(t, state.PendingApprovals, "authorized current controls remain a real array")
		case "catch_up_complete":
			completeCount++
			var complete generated.CatchUpCompleteFrame
			require.NoError(t, json.Unmarshal(frame.raw, &complete))
			require.Equal(t, fixture.sid, complete.SessionId)
			require.Equal(t, "live_only", complete.Mode)
		}
	}
	require.Equal(t, 1, stateCount, "one actual session_state, not silence or duplicates")
	require.Equal(t, 1, completeCount, "one live_only completion, not silence or duplicates")
	require.Greater(t, indexOfType(frames, "catch_up_complete"), indexOfType(frames, "session_state"), "the completion follows the unavailable-history state")
}

func sentFileExpectSkippedMissingRef(t *testing.T, store *media.FileMediaStore, ref string) {
	t.Helper()
	fixture := newAttachFixture(t, "missing-file-turn", "missing-file-msg")
	fixture.h.agentLoop.SetMediaStore(store)
	require.NoError(t, fixture.store.AppendTranscriptStrict(fixture.sid, session.TranscriptEntry{
		ID: "keep-text", Role: "user", Content: "Keep this plan", AgentID: "mia",
		Timestamp: time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC), ViewMembership: session.ViewMembershipChat,
	}))
	chatLinksSendFileEntry(t, fixture.store, fixture.sid, "gone-row", "call-gone", ref, "gone.md", "text/markdown", "file")
	api := pagingAPIForAttach(fixture)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+fixture.sid+"/messages", nil)
	api.serveSessionHistory(rec, req.WithContext(context.Background()), fixture.sid)
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, "Keep this plan") || !strings.Contains(body, "send_file") {
		t.Errorf("control: history page = %d %s, want 200 with the text and the tool row", rec.Code, body)
	}
	conn := fixture.newConn("missing-file-viewer")
	fixture.h.handleAttachSession(context.Background(), "missing-file-viewer", fixture.sid, nil, conn)
	frames := pagingDrain(t, conn)
	complete := indexOfType(frames, "catch_up_complete")
	if complete < 0 || frames[complete].Mode != "snapshot" {
		mode := ""
		if complete >= 0 {
			mode = frames[complete].Mode
		}
		t.Errorf("control: attach mode = %q, want snapshot", mode)
	}
	for _, frame := range frames {
		if frame.Type == "media" {
			t.Errorf("control: a missing file still produced a media frame: %s", frame.raw)
		}
	}
	var saw bool
	for _, frame := range frames {
		if frame.Type != "tool_call_result" || !strings.Contains(string(frame.raw), ref) {
			continue
		}
		saw = true
	}
	if !saw {
		t.Error("control: attach snapshot dropped the saved tool result for the missing file")
	}
}

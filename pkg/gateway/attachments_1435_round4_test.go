package gateway

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

// Issue #1435 Round 4 item 1 — real replay-window producer.
//
// A real loop + store persist an empty truncated assistant entry, then a
// real turn with a missing attachment persists the typed media_unavailable
// notice. A second WS connection then attaches and captures the gateway's
// real replay window while the first connection's provider is still held.
// The recorded frames are the SPA test's INPUT (not the expected state
// oracle); canonicalization only folds per-run generated identifiers.
func TestIssue1435R4_ReplayNoticeInputs(t *testing.T) {
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	provider := newControllableStreamProvider(providerRound{tokens: []string{"Working on the remaining request.", " Still progressing."}})
	// The provider closes pausedCh itself when it reaches the pause point;
	// the test only supplies the resume channel.
	_, resume := provider.pauseAfter(0, 0)
	var once sync.Once
	open := func() { once.Do(func() { close(resume) }) }
	defer open() // release the provider before server cleanup on any assertion path
	server, handler, _ := newControllableStreamTestServer(t, provider)
	store := media.NewFileMediaStore()
	t.Cleanup(store.Stop)
	handler.agentLoop.SetMediaStore(store)
	owner, err := handler.agentLoop.GetSessionStore().NewSession(session.SessionTypeChat, "web", "mia")
	require.NoError(t, err)

	// Persist the earlier empty truncated assistant entry through the REAL
	// store, before the turn: exactly the shape streamReplay re-emits
	// (Content == "" && Truncated && Role == "assistant").
	require.NoError(t, handler.agentLoop.GetSessionStore().AppendTranscriptStrict(owner.ID, session.TranscriptEntry{
		ID: "issue1435-r4-truncated-assistant", Role: "assistant", Content: "",
		Truncated: true, AgentID: "mia", Timestamp: time.Now().UTC(),
	}))

	file := filepath.Join(t.TempDir(), "accepted.txt")
	require.NoError(t, os.WriteFile(file, []byte("accepted file"), 0o600))
	ref, err := store.Store(file, media.MediaMeta{
		Filename: "accepted.txt", ContentType: "text/plain",
		Source: "upload:webchat", CleanupPolicy: media.CleanupPolicyForgetOnly,
	}, media.SessionUploadScopePrefix+owner.ID)
	require.NoError(t, err)
	_, err = store.Resolve(ref)
	require.NoError(t, err, "instrument: the attachment really was accepted")
	require.NoError(t, store.ReleaseAll(media.SessionUploadScopePrefix+owner.ID))
	_, err = store.Resolve(ref)
	require.ErrorIs(t, err, media.ErrNotFound)

	conn := dialTestWS(t, server)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	sendWSAuthFrameDevMode(t, conn)
	request, err := json.Marshal(wsClientFrameTestHelper{Type: "message",
		Content:   "Continue processing the text even if the attachment is unavailable.",
		SessionID: owner.ID, AgentID: "mia", Media: []string{ref}})
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, request))

	liveTurnID := issue1435R4CaptureLiveTurn(t, conn, owner.ID)

	// Second connection: attach and capture the real replay window while the
	// provider is still held (the turn is genuinely still running).
	replayFrames := issue1435R4CaptureReplayWindow(t, server, owner.ID)
	replayErrorIndex := issue1435R4AssertReplayWindow(t, replayFrames)
	open() // the turn finishes; capture the real live continuation (hybrid case 1b)

	liveAfterFrames, liveReplyMessageID := issue1435R4CaptureLiveContinuation(t, conn, owner.ID)

	// SPA input container. Canonicalization folds per-run generated values
	// only; frame order, types, codes, content and entry-id relationships
	// are preserved exactly.
	//
	// The gateway mints a FRESH reply message id per run, so it is folded by
	// VALUE (like the round-3 producer does) — otherwise every bare run would
	// compare a new UUID against the committed fixture. It is the only
	// per-run opaque id folded this way; the notice's entry id keeps its own
	// token, so the two stay distinguishable below.
	sessionToken, turnToken := "1435-round4-session", "1435-round4-turn"
	replyToken := "1435-round4-reply"
	folder := &issue1435R4Folder{
		ownerID:            owner.ID,
		liveTurnID:         liveTurnID,
		liveReplyMessageID: liveReplyMessageID,
		sessionToken:       sessionToken,
		turnToken:          turnToken,
		replyToken:         replyToken,
		entryTokens:        map[string]string{},
	}
	folder.canonicalize(replayFrames)
	folder.canonicalize(liveAfterFrames)

	// The notice's identity and the reply's identity must stay DISTINCT:
	// folding the per-run reply id must not collapse the notice onto the
	// reply it is supposed to stay separate from.
	noticeEntryID, replyFoldedID := issue1435R4FoldedIdentities(replayFrames, liveAfterFrames)
	require.NotEmpty(t, noticeEntryID, "instrument: the notice must carry an entry id")
	require.Equal(t, replyToken, replyFoldedID, "the reply's per-run message id must be folded to the stable token")
	require.NotEqual(t, noticeEntryID, replyFoldedID,
		"the notice's entry id and the reply's message id must stay distinguishable after folding")

	document := map[string]any{
		"session_id":        sessionToken,
		"turn_id":           turnToken,
		"replay_frames":     replayFrames,
		"live_after_frames": liveAfterFrames,
	}
	encoded, err := json.MarshalIndent(document, "", "  ")
	require.NoError(t, err)
	issue1435R4PairFixture(t, encoded)
	t.Logf("real replay window: %d frames, replay_error at index %d", len(replayFrames), replayErrorIndex)
}

// issue1435R4CaptureLiveTurn reads the live turn's frames up to its first
// token and returns the live turn id. The informational notice must not end
// the backend turn before its reply, and the notice must have been persisted
// before the replay capture starts.
func issue1435R4CaptureLiveTurn(t *testing.T, conn *websocket.Conn, ownerID string) string {
	t.Helper()
	liveTurnID := ""
	noticeSeen := false
	for {
		require.NoError(t, conn.SetReadDeadline(time.Now().Add(20*time.Second)))
		_, raw, readErr := conn.ReadMessage()
		require.NoError(t, readErr, "real gateway must deliver the live turn")
		var frame map[string]any
		require.NoError(t, json.Unmarshal(raw, &frame))
		if frame["session_id"] != ownerID {
			continue
		}
		if payload, ok := frame["payload"].(map[string]any); ok {
			if llm, ok := payload["llm_error"].(map[string]any); ok && llm["code"] == "media_unavailable" {
				noticeSeen = true
			}
		}
		require.NotEqual(t, "done", frame["type"],
			"the informational notice must not end the backend turn before its reply")
		if frame["type"] == "token" {
			if id, ok := frame["turn_id"].(string); ok {
				liveTurnID = id
			}
			break
		}
	}
	require.True(t, noticeSeen, "instrument: the notice must be persisted before the replay capture")
	return liveTurnID
}

// issue1435R4CaptureReplayWindow attaches a second connection and captures
// the gateway's real replay window while the first connection's provider is
// still held (the turn is genuinely still running).
func issue1435R4CaptureReplayWindow(t *testing.T, server *httptest.Server, ownerID string) []any {
	t.Helper()
	conn2 := dialTestWS(t, server)
	t.Cleanup(func() { require.NoError(t, conn2.Close()) })
	sendWSAuthFrameDevMode(t, conn2)
	attach, err := json.Marshal(wsClientFrameTestHelper{Type: "attach_session", SessionID: ownerID})
	require.NoError(t, err)
	require.NoError(t, conn2.WriteMessage(websocket.TextMessage, attach))
	var replayFrames []any
	for {
		require.NoError(t, conn2.SetReadDeadline(time.Now().Add(20*time.Second)))
		_, raw, readErr := conn2.ReadMessage()
		require.NoError(t, readErr, "real gateway must finish the replay window")
		var frame map[string]any
		require.NoError(t, json.Unmarshal(raw, &frame))
		switch frame["type"] {
		case "replay_message", "replay_error", "catch_up_complete":
			replayFrames = append(replayFrames, frame)
		}
		if frame["type"] == "catch_up_complete" {
			goto replayDone
		}
	}
replayDone:
	return replayFrames
}

// issue1435R4AssertReplayWindow checks the shape of the captured replay
// window and returns the index of the notice frame inside it.
func issue1435R4AssertReplayWindow(t *testing.T, replayFrames []any) int {
	t.Helper()
	var replayErrorIndex = -1
	var replayTypes []string
	for i, value := range replayFrames {
		frame := value.(map[string]any)
		replayTypes = append(replayTypes, frame["type"].(string))
		if frame["type"] == "replay_error" {
			replayErrorIndex = i
		}
	}
	require.Equal(t, []string{"replay_message", "replay_message", "replay_error", "catch_up_complete"}, replayTypes,
		"instrument: the real replay window must carry the truncated assistant entry, the user entry, then the notice")
	require.GreaterOrEqual(t, replayErrorIndex, 0)
	return replayErrorIndex
}

// issue1435R4CaptureLiveContinuation captures the real live continuation
// (token frames and the genuine done) after the held provider resumes.
func issue1435R4CaptureLiveContinuation(t *testing.T, conn *websocket.Conn, ownerID string) ([]any, string) {
	t.Helper()
	var liveAfterFrames []any
	liveReplyMessageID := ""
	for {
		require.NoError(t, conn.SetReadDeadline(time.Now().Add(20*time.Second)))
		_, raw, readErr := conn.ReadMessage()
		require.NoError(t, readErr, "real gateway must finish the resumed turn")
		var frame map[string]any
		require.NoError(t, json.Unmarshal(raw, &frame))
		if frame["session_id"] != ownerID {
			continue
		}
		switch frame["type"] {
		case "token", "done":
			if liveReplyMessageID == "" {
				if id, ok := frame["message_id"].(string); ok {
					liveReplyMessageID = id
				}
			}
			liveAfterFrames = append(liveAfterFrames, frame)
		}
		if frame["type"] == "done" {
			goto liveDone
		}
	}
liveDone:
	require.NotEmpty(t, liveReplyMessageID, "instrument: the resumed turn must carry a gateway-minted reply message id")
	return liveAfterFrames, liveReplyMessageID
}

// issue1435R4Folder carries the per-run generated values that the
// canonicalizer folds out of the captured frames, and the shared map that
// keeps the folded entry ids consistent across the replay window and the
// live continuation.
type issue1435R4Folder struct {
	ownerID            string
	liveTurnID         string
	liveReplyMessageID string
	sessionToken       string
	turnToken          string
	replyToken         string
	entryTokens        map[string]string
}

// canonicalize folds the per-run generated values only; frame order, types,
// codes, content and entry-id relationships are preserved exactly.
func (f *issue1435R4Folder) canonicalize(value any) {
	switch object := value.(type) {
	case []any:
		for _, child := range object {
			f.canonicalize(child)
		}
	case map[string]any:
		if object["session_id"] == f.ownerID {
			object["session_id"] = f.sessionToken
		}
		if object["turn_id"] == f.liveTurnID {
			object["turn_id"] = f.turnToken
		}
		for _, key := range []string{"id", "entry_id"} {
			if text, ok := object[key].(string); ok {
				if seen, exists := f.entryTokens[text]; exists {
					object[key] = seen
				} else {
					token := "1435-round4-entry-" + string(rune('a'+len(f.entryTokens)))
					f.entryTokens[text] = token
					object[key] = token
				}
			}
		}
		if object["boot_id"] != nil {
			object["boot_id"] = "1435-round4-boot"
		}
		for key, child := range object {
			lower := key
			if text, ok := child.(string); ok && text == f.liveReplyMessageID {
				object[key] = f.replyToken
			}
			if lower == "timestamp" || lower == "emitted_at" {
				if _, ok := child.(string); ok {
					object[key] = "2026-10-27T00:00:00Z"
				}
			}
			// Timing-only performance metrics are opaque INPUT facts.
			if lower == "duration_ms" || lower == "duration" || lower == "latency_ms" {
				if _, ok := child.(float64); ok {
					object[key] = float64(1)
				}
			}
			f.canonicalize(child)
		}
	}
}

// issue1435R4FoldedIdentities returns the notice's entry id and the reply's
// folded message id, so the two can be compared after folding.
func issue1435R4FoldedIdentities(replayFrames, liveAfterFrames []any) (string, string) {
	noticeEntryID, replyFoldedID := "", ""
	for _, value := range replayFrames {
		frame := value.(map[string]any)
		if frame["type"] == "replay_error" {
			noticeEntryID, _ = frame["entry_id"].(string)
		}
	}
	for _, value := range liveAfterFrames {
		frame := value.(map[string]any)
		if frame["type"] == "token" {
			replyFoldedID, _ = frame["message_id"].(string)
		}
	}
	return noticeEntryID, replyFoldedID
}

// issue1435R4PairFixture keeps the committed SPA input fixture paired with
// the real gateway replay producer. The fixture is INPUT, not the state
// oracle: it is rewritten only under UPDATE_1435_R4_INPUTS=1.
func issue1435R4PairFixture(t *testing.T, encoded []byte) {
	t.Helper()
	path := filepath.Join("..", "..", "tests", "fixtures", "attachments-1435-round4", "replay-frames.json")
	if os.Getenv("UPDATE_1435_R4_INPUTS") == "1" {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, append(encoded, '\n'), 0o600))
	}
	paired, err := os.ReadFile(path)
	require.NoError(t, err, "the paired SPA input fixture must be recorded before the consumer runs")
	require.JSONEq(t, string(encoded), string(paired),
		"the SPA input fixture must stay paired with the real gateway replay producer; it is not the state oracle")
}

// Issue #1435 Round 4 item 4 (architect F5): with the media cleaner
// disabled, the retention sweep that removes a session and cascades its
// upload files must also release that session's pinned media mappings on
// the LIVE store. A reload may not be required for the mapping to go.
func TestIssue1435R4_RetentionReleasesPinsWithCleanerDisabled(t *testing.T) {
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	api, cleanup := newTestRestAPI(t)
	t.Cleanup(cleanup)
	// Cleaner DISABLED: a bare FileMediaStore — no cleaner config, so
	// CleanExpired must never run for this store in this test.
	store := media.NewFileMediaStore()
	t.Cleanup(store.Stop)
	api.agentLoop.SetMediaStore(store)
	api.mediaStore = store
	api.homePath = filepath.Dir(api.agentLoop.GetSessionStore().BaseDir())
	t.Setenv("OMNIPUS_HOME", api.homePath)

	sessions := api.agentLoop.GetSessionStore()
	owner, err := sessions.NewSession(session.SessionTypeChat, "web", "mia")
	require.NoError(t, err)
	recorder := issue1435R2Upload(t, api, owner.ID)
	require.Equal(t, http.StatusCreated, recorder.Code, "upload setup must genuinely succeed: %s", recorder.Body.String())
	var result struct {
		Files []struct {
			Ref *string `json:"ref"`
		} `json:"files"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &result))
	require.Len(t, result.Files, 1)
	require.NotNil(t, result.Files[0].Ref)
	ref := *result.Files[0].Ref
	path, err := store.Resolve(ref)
	require.NoError(t, err, "positive control: the accepted ref really resolves")
	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "accepted owner-scoped attachment", string(contents))

	// Real retention: age the session transcript, then run the production
	// sweep. It removes the transcript and cascade-deletes the uploads dir.
	transcript := filepath.Join(sessions.BaseDir(), owner.ID, "transcript.jsonl")
	require.NoError(t, os.WriteFile(transcript, []byte("{}\n"), 0o600))
	old := time.Date(2000, time.January, 1, 0, 0, 0, 0, time.UTC)
	require.NoError(t, os.Chtimes(transcript, old, old))
	removed, err := sessions.RetentionSweep(1)
	require.NoError(t, err)
	require.Positive(t, removed, "instrument: the real retention sweep must remove the expired transcript")
	_, err = os.Stat(path)
	require.ErrorIs(t, err, fs.ErrNotExist, "instrument: the cascade really deleted the upload bytes")

	// THE ROUND-4 ASSERTION: the pinned mapping must not outlive its
	// conversation when the cleaner is disabled.
	_, resolveErr := store.Resolve(ref)
	require.ErrorIs(t, resolveErr, media.ErrNotFound,
		"#1435 R4: retention removed the conversation and its upload bytes, but the pinned mapping is still live in the running store")
}

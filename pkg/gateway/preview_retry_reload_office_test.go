package gateway

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/preview"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/protocoltypes"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/tests/officepreview"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Oracle: architect Retry ruling, Follow-up 2026-11-01; office-preview
// §5.2 / T-66 / US-3.2 / B-12 at spec 52010360e. Successful Retry settles
// only the original call's chat display, never its model result. A new
// request must replay the stored group, not the old unavailable card or a
// cached live frame. Failure persistence and last-success-wins are separate cases.
func TestOfficePreview_RetryPersistsInBrowserHistoryWithoutModelMedia(t *testing.T) {
	f := newOfficeRetryFixture(t)
	officeRetrySharedRenderer(t)
	data := officepreview.PDF(1)
	require.NoError(t, os.WriteFile(filepath.Join(workDir(f.api, f.wsID), "report.pdf"), data, 0600))
	f.requireLibraryBytes(t, "report.pdf", data)
	original := previewToolCall("retry-original-call", "report.pdf", officeRetryDigest(data), 1, 1, 1, "unavailable", "renderer_failed", nil)
	original.Parameters = map[string]any{"file": "report.pdf", "pages": "1"}
	original.DurationMS = 1234 // Sentinel: Retry must preserve the original invocation's timing.
	original.Result["text"] = "original model tool result"
	neighbor := session.ToolCall{ID: "unrelated-read-call", Tool: "read_file", Status: "success", Result: map[string]any{"text": "retain the other call in this entry"}}
	store := f.api.agentLoop.GetSessionStore()
	require.NoError(t, store.AppendTranscriptStrict(f.sid, session.TranscriptEntry{
		ID: "original-preview-entry", Type: session.EntryTypeToolCall, Role: "assistant", AgentID: "mia",
		Timestamp: time.Now(), ViewMembership: session.ViewMembershipChat,
		ToolCalls: []session.ToolCall{original, neighbor},
	}))
	modelKey := "agent:mia:session:" + f.sid
	officeRetrySeedModel(t, store, modelKey, data)
	beforeModel := officeRetryModelBytes(t, store, modelKey)
	beforePage, beforeReplay, _ := officeRetryFreshHistory(t, f)
	require.Len(t, beforePage.Entries, 1, "instrument: model-only messages must not become chat entries")
	require.Len(t, beforeReplay, 1, "instrument must actually expose the original preview card")
	require.Equal(t, "unavailable", beforeReplay[0].Preview.Outcome)
	require.Empty(t, beforeReplay[0].Parts)

	response := f.retry(t, f.sid)
	require.Equal(t, http.StatusAccepted, response.Code, "%s", response.Body.String())
	var started generated.PreviewRetryResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &started))
	require.Equal(t, generated.PreviewRetryResponse{Status: "started"}, started)
	live := officeReceiveMedia(t, f.conn)
	f.requireRenderedFrame(t, live) // Real registered renderer, stored PNG, independent red-page pixels.
	require.Equal(t, beforeModel, officeRetryModelBytes(t, store, modelKey), "successful live Retry must leave the existing model window and retained bytes unchanged")
	t.Log("control: actual Retry succeeded; live complete group has retrievable red PNG; nonempty model history unchanged")

	// A browser reload cannot depend on the renderer or the existing hub journal.
	// No settle is manufactured by this test. Reopen disk state and create new
	// REST/shared snapshot requests; only production Retry may have saved the group.
	preview.SetShared(nil)
	require.Nil(t, preview.Shared(), "reload control: shared rendering is unavailable")
	afterPage, afterReplay, reopened := officeRetryFreshHistory(t, f)
	require.Equal(t, beforeModel, officeRetryModelBytes(t, reopened, modelKey), "T-66: reloaded model context/archive must be byte-identical, including the original PDF and tool result")
	require.Equal(t, int64(0), f.provider.calls.Load(), "Retry/reload must not invoke a model")
	require.Empty(t, f.bus.InboundChan(), "Retry/reload must not queue a model turn")
	require.Len(t, afterPage.Entries, 1, "successful Retry must not append a second chat tool-call entry")
	entries, err := reopened.ReadTranscript(f.sid)
	require.NoError(t, err)
	var targetCalls, otherCalls []session.ToolCall
	for _, entry := range entries {
		for _, call := range entry.ToolCalls {
			switch call.ID {
			case original.ID:
				require.Equal(t, "original-preview-entry", entry.ID, "settle the original occurrence rather than replacing it with a duplicate")
				targetCalls = append(targetCalls, call)
			case neighbor.ID:
				otherCalls = append(otherCalls, call)
			}
		}
	}
	require.Len(t, targetCalls, 1, "one settled original call, not a duplicate with replay dedupe hiding it")
	require.Len(t, otherCalls, 1, "the other call in the original entry must survive")
	require.JSONEq(t, string(officeRetryNonDisplay(t, neighbor)), string(officeRetryNonDisplay(t, otherCalls[0])))
	require.JSONEq(t, string(officeRetryNonDisplay(t, original)), string(officeRetryNonDisplay(t, targetCalls[0])), "ruling: original tool call unchanged except media/preview")
	t.Log("control: fresh durable history loaded without rendering; original call/neighbor retained; model window/archive byte-identical; no model turn")

	require.Len(t, afterReplay, 1, "reload must contain exactly one target thumbnail group")
	assert.Equal(t, "complete", afterReplay[0].Preview.Outcome, "T-66: successful Retry must persist; reload must replace the old unavailable card")
	assert.Nil(t, afterReplay[0].Preview.UnavailableReason, "the old renderer_failed card must not survive a successful Retry")
	assert.Equal(t, officeMediaProjection(live), officeMediaProjection(afterReplay[0]), "T-66: shared browser replay must restore exactly the successful live thumbnails and page metadata")
	f.requireRenderedFrame(t, afterReplay[0])
}

func officeRetrySeedModel(t *testing.T, store *session.UnifiedStore, key string, pdf []byte) {
	t.Helper()
	ctx := context.Background()
	_, _, err := store.AppendModelMessage(ctx, key, session.ModelAppend{
		Message:        providers.Message{Role: "user", Content: "Preview report.pdf"},
		ViewMembership: session.ViewMembershipModel, Source: session.EntrySource{Kind: "user"},
	})
	require.NoError(t, err)
	assistant, _, err := store.AppendModelMessage(ctx, key, session.ModelAppend{
		Message: providers.Message{Role: "assistant", ToolCalls: []providers.ToolCall{{
			ID: "retry-original-call", Type: "function",
			Function: &protocoltypes.FunctionCall{Name: "preview_document", Arguments: `{"file":"report.pdf","pages":"1"}`},
		}}},
		ViewMembership: session.ViewMembershipModel, Source: session.EntrySource{Kind: "agent"},
	})
	require.NoError(t, err)
	_, _, err = store.AppendModelMessage(ctx, key, session.ModelAppend{
		Message:        providers.Message{Role: "tool", ToolCallID: "retry-original-call", Content: "original model tool result", Media: []string{"data:application/pdf;base64," + base64.StdEncoding.EncodeToString(pdf)}},
		ViewMembership: session.ViewMembershipModel, Source: session.EntrySource{Kind: "tool"}, ToolResultFor: &assistant.Addr,
	})
	require.NoError(t, err)
}

type officeRetryModelState struct {
	Window  []byte
	Archive []byte
	Raw     [][]byte
}

func officeRetryModelBytes(t *testing.T, store *session.UnifiedStore, key string) officeRetryModelState {
	t.Helper()
	ctx := context.Background()
	view, err := store.WindowView(ctx, key)
	require.NoError(t, err)
	messages, ordinals := view.History()
	require.Len(t, messages, 3, "nonempty control: user, assistant declaration, original tool result")
	require.Equal(t, []int{0, 1, 2}, ordinals)
	require.Equal(t, []string{"user", "assistant", "tool"}, []string{messages[0].Role, messages[1].Role, messages[2].Role})
	require.Equal(t, "retry-original-call", messages[2].ToolCallID)
	require.Equal(t, "original model tool result", messages[2].Content)
	require.Equal(t, []string{"data:application/pdf;base64," + base64.StdEncoding.EncodeToString(officepreview.PDF(1))}, messages[2].Media, "original native PDF, no new thumbnail media")
	require.Equal(t, 3, view.State.Count, "Retry must not append and then hide a model slot")
	window, err := json.Marshal(messages)
	require.NoError(t, err)
	archive, err := store.ReadArchive(ctx, key)
	require.NoError(t, err)
	require.Len(t, archive, 3, "retained model archive must not gain a hidden result or media")
	archiveBytes, err := json.Marshal(archive)
	require.NoError(t, err)
	var raw [][]byte
	err = store.ReadModelSlots(ctx, key, 0, 2, func(_ session.ModelSlot, record []byte) error {
		raw = append(raw, append([]byte(nil), record...))
		return nil
	})
	require.NoError(t, err)
	require.Len(t, raw, 3, "compare real retained model payload bytes, not an empty view")
	return officeRetryModelState{Window: window, Archive: archiveBytes, Raw: raw}
}

func officeRetryFreshHistory(t *testing.T, f *officeRetryFixture) (generated.SessionHistoryResponse, []generated.MediaFrame, *session.UnifiedStore) {
	t.Helper()
	fresh, err := session.NewUnifiedStoreWithHome(f.api.agentLoop.GetSessionStore().BaseDir(), f.api.homePath)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, fresh.Close()) })
	request, err := newHistoryPageRequest(f.sid, session.ChatHistoryPageOptions{}, historyPageProjectionDeps{MediaStore: f.api.mediaStore})
	require.NoError(t, err)
	prepared, err := request.Read(context.Background(), fresh)
	require.NoError(t, err, "new browser snapshot preparation must read persistent history, not the live hub")
	response := httptest.NewRecorder()
	history := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+f.sid+"/messages", nil)
	history = history.WithContext(contextWithUser(history.Context(), "alice"))
	f.api.HandleSessions(response, history)
	require.Equal(t, http.StatusOK, response.Code, "actual browser history route: %s", response.Body.String())
	var page generated.SessionHistoryResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &page))
	require.Equal(t, page.Entries, prepared.Response.Entries, "real REST and fresh disk-backed replay must agree")
	require.False(t, page.HasEarlier, "fixture is small: every original call must be selected")
	require.False(t, page.HasLater)
	var groups []generated.MediaFrame
	for _, raw := range prepared.ReplayFrames {
		var frame generated.MediaFrame
		require.NoError(t, json.Unmarshal(raw, &frame))
		if frame.Type == "media" {
			require.NotNil(t, frame.ToolCallId)
			require.Equal(t, "retry-original-call", *frame.ToolCallId)
			require.NotNil(t, frame.Preview)
			groups = append(groups, frame)
		}
	}
	return page, groups, fresh
}

func officeRetryNonDisplay(t *testing.T, call session.ToolCall) []byte {
	t.Helper()
	// Clone through JSON; never strip display fields from the stored record.
	raw, err := json.Marshal(call)
	require.NoError(t, err)
	var cloned session.ToolCall
	require.NoError(t, json.Unmarshal(raw, &cloned))
	delete(cloned.Result, "media")
	delete(cloned.Result, "preview")
	raw, err = json.Marshal(cloned)
	require.NoError(t, err)
	return raw
}

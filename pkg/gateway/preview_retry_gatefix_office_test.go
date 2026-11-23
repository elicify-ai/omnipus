package gateway

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/preview"
	"github.com/elicify-ai/omnipus/pkg/preview/component"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/tests/officepreview"
	"github.com/stretchr/testify/require"
)

// G1 / architect F1, CR F1, SF-01. Contract MediaPreview.page_count is at
// least 1. A failed retry must keep the page count already recorded for
// that call. Zero is dropped by the SPA schema, so the card stays on
// "Retrying…". The oracle is the recorded count (4), not a hardcoded 1.
func TestOfficePreview_FailedRetryKeepsRecordedPageCount(t *testing.T) {
	f := newOfficeRetryFixture(t)
	gatefixReadyDummyRenderer(t, f.api.homePath)
	// Not a readable PDF: the renderer and the text fallback both fail, so
	// the retry takes the error path that omits page_count (SF-01).
	data := []byte("not a readable PDF")
	rel := "report.pdf"
	require.NoError(t, os.WriteFile(filepath.Join(workDir(f.api, f.wsID), rel), data, 0o600))
	f.requireLibraryBytes(t, rel, data)
	const callID = "gatefix-g1-call"
	const recordedPages = 4
	gatefixRecordPreview(t, f, callID, rel, data, recordedPages, 1, recordedPages, "unavailable", "renderer_failed")

	response := gatefixPostRetry(t, f, callID)
	require.Equal(t, http.StatusAccepted, response.Code, "%s", response.Body.String())
	frame := officeReceiveMedia(t, f.conn)
	require.NotNil(t, frame.Preview)
	require.Equal(t, "unavailable", frame.Preview.Outcome)
	require.NotNil(t, frame.Preview.UnavailableReason)
	require.Equal(t, preview.ReasonRendererFailed, *frame.Preview.UnavailableReason)
	require.Equal(t, recordedPages, frame.Preview.PageCount, "a failed retry must send the recorded page_count (%d); 0 is below the contract minimum and the SPA drops the frame", recordedPages)
}

// G2 / SF-04. Spec §5.2: a successful retry settles the chat record. When
// that write fails, the person must not be shown an unqualified success,
// and the stored record stays the previous one.
func TestOfficePreview_PersistenceFailureIsNotShownAsSuccess(t *testing.T) {
	f := newOfficeRetryFixture(t)
	officeRetrySharedRenderer(t)
	data := officepreview.PDF(1)
	rel := "report.pdf"
	require.NoError(t, os.WriteFile(filepath.Join(workDir(f.api, f.wsID), rel), data, 0o600))
	f.requireLibraryBytes(t, rel, data)
	const callID = "gatefix-g2-call"
	gatefixRecordPreview(t, f, callID, rel, data, 1, 1, 1, "unavailable", "renderer_failed")
	frozen := gatefixFreezeArchives(t, f)
	t.Cleanup(func() { gatefixThawArchives(t, frozen) })

	response := gatefixPostRetry(t, f, callID)
	require.Equal(t, http.StatusAccepted, response.Code, "%s", response.Body.String())
	frame := officeReceiveMedia(t, f.conn)
	require.NotEmpty(t, frame.Parts, "instrument: the renderer must produce pages so settle is attempted; an unavailable frame never reaches persistence")
	require.NotNil(t, frame.Preview)
	require.NotEqual(t, "complete", frame.Preview.Outcome, "SF-04: a retry whose chat record could not be saved must not be shown as a successful preview")

	_, call, found, err := f.api.agentLoop.GetSessionStore().LocateChatToolCall(f.sid, session.ToolCallID(callID))
	require.NoError(t, err)
	require.True(t, found)
	group, _ := call.Result["preview"].(map[string]any)
	require.Equal(t, "unavailable", group["outcome"], "the stored preview stays the previous record when settle fails")
}

func gatefixReadyDummyRenderer(t *testing.T, home string) {
	t.Helper()
	previous := preview.Shared()
	t.Cleanup(func() { preview.SetShared(previous) })
	root := filepath.Join(officepreview.OwnRoot(t), "renderer")
	officepreview.RegisteredRendererFixture(t, home, root, true)
	self, err := os.Executable()
	require.NoError(t, err)
	svc, err := preview.New(preview.Config{Home: home, Self: self, Resolver: &component.Resolver{Home: home, Executable: self}})
	require.NoError(t, err)
	require.Equal(t, "ready", svc.ComponentStatus().Renderer, "instrument: the dummy registration must look ready so the retry is accepted and then fails while rendering")
	preview.SetShared(svc)
}

func gatefixRecordPreview(t *testing.T, f *officeRetryFixture, callID, rel string, data []byte, pages, first, last int, outcome, reason string) {
	t.Helper()
	tc := previewToolCall(session.ToolCallID(callID), "report.pdf", officeRetryDigest(data), pages, first, last, outcome, reason, nil)
	tc.Parameters = map[string]any{"file": rel, "pages": "1"}
	require.NoError(t, f.api.agentLoop.GetSessionStore().AppendTranscriptStrict(f.sid, session.TranscriptEntry{
		ID: callID + "-entry", Type: session.EntryTypeToolCall, Role: "assistant", AgentID: "mia",
		Timestamp: time.Now(), ViewMembership: session.ViewMembershipChat, ToolCalls: []session.ToolCall{tc},
	}))
}

func gatefixPostRetry(t *testing.T, f *officeRetryFixture, callID string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/"+f.sid+"/previews/"+callID+"/retry", nil)
	r = r.WithContext(contextWithUser(r.Context(), "alice"))
	w := httptest.NewRecorder()
	f.api.HandleSessions(w, r)
	return w
}

func gatefixFreezeArchives(t *testing.T, f *officeRetryFixture) []string {
	t.Helper()
	store := f.api.agentLoop.GetSessionStore()
	dir := filepath.Join(store.BaseDir(), f.sid)
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	var frozen []string
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".jsonl" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		require.NoError(t, os.Chmod(path, 0o444))
		probe, openErr := os.OpenFile(path, os.O_RDWR|os.O_APPEND, 0o600)
		if openErr == nil {
			_ = probe.Close()
			t.Fatalf("instrument: %s is still writable, so settle would succeed and this would not be the persistence failure", path)
		}
		frozen = append(frozen, path)
	}
	require.NotEmpty(t, frozen, "instrument: %s has no archive to make read-only (entries: %v)", dir, entries)
	return frozen
}

func gatefixThawArchives(t *testing.T, paths []string) {
	t.Helper()
	for _, path := range paths {
		require.NoError(t, os.Chmod(path, 0o644))
	}
}

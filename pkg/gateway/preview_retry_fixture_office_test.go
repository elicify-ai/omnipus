package gateway

// Retry oracles: architect-retry-ruling.md (2026-11-01), Parts 1–2,
// replacing office-preview §5.2 / T-66. Fixtures use the real Library,
// session store, REST dispatch, renderer, channel manager and WebSocket hub.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/preview"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/tests/officepreview"
	"github.com/stretchr/testify/require"
)

type officeRetryProvider struct {
	restMockProvider
	calls atomic.Int64
}

func (p *officeRetryProvider) Chat(context.Context, []providers.Message, []providers.ToolDefinition, string, map[string]any) (*providers.LLMResponse, error) {
	p.calls.Add(1)
	return &providers.LLMResponse{}, nil
}

type officeRetryFixture struct {
	api      *restAPI
	bus      *bus.MessageBus
	provider *officeRetryProvider
	ws       *WSHandler
	conn     *wsConn
	wsID     string
	sid      string
}

func newOfficeRetryFixture(t *testing.T) *officeRetryFixture {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	cfg := &config.Config{Agents: config.AgentsConfig{Defaults: config.AgentDefaults{Home: home, DefaultModel: config.DefaultModel{Model: "test-model"}}}}
	seedTestAgents(cfg)
	mb := bus.NewMessageBus()
	provider := &officeRetryProvider{}
	// Prove the paid-network-edge instrument observes a call, then reset.
	_, err := provider.Chat(context.Background(), nil, nil, "test-model", nil)
	require.NoError(t, err)
	require.Equal(t, int64(1), provider.calls.Swap(0))
	loop := mustAgentLoop(t, cfg, mb, provider)
	api := &restAPI{agentLoop: loop, homePath: home, mediaStore: newTestFileMediaStore(t)}
	loop.SetMediaStore(api.mediaStore)
	wsID := seedLibraryWorkspace(t, api, "Retry Library")
	meta, err := loop.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	require.NoError(t, loop.GetSessionStore().SetMeta(meta.ID, session.MetaPatch{WorkspaceID: &wsID}))
	require.NoError(t, os.MkdirAll(workDir(api, wsID), 0700))
	h := newWSHandler(mb, loop, "")
	t.Cleanup(h.Wait)
	conn := makeTestConn()
	bindTestConnToSession(h, "retry-person", meta.ID, conn)
	cm := channels.NewManagerForTesting(nil)
	cm.RegisterChannel("webchat", newWebchatChannel(h))
	loop.SetChannelManager(cm)
	f := &officeRetryFixture{api: api, bus: mb, provider: provider, ws: h, conn: conn, wsID: wsID, sid: meta.ID}
	control := httptest.NewRecorder()
	api.HandleSessions(control, httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+f.sid, nil))
	require.Equal(t, http.StatusOK, control.Code, "real addressed session control: %s", control.Body.String())
	return f
}

func officeRetryDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])[:12] // §5.2: SHA-256, first 12 hex
}

func (f *officeRetryFixture) record(t *testing.T, source string, data []byte) {
	t.Helper()
	tc := previewToolCall("retry-original-call", "report.pdf", officeRetryDigest(data), 1, 1, 1, "unavailable", "renderer_failed", nil)
	tc.Parameters = map[string]any{"file": source, "pages": "1"}
	require.NoError(t, f.api.agentLoop.GetSessionStore().AppendTranscriptStrict(f.sid, session.TranscriptEntry{ID: "original-preview-entry", Type: session.EntryTypeToolCall, Role: "assistant", AgentID: "mia", Timestamp: time.Now(), ViewMembership: session.ViewMembershipChat, ToolCalls: []session.ToolCall{tc}}))
}

func (f *officeRetryFixture) retry(t *testing.T, sid string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/"+sid+"/previews/retry-original-call/retry", nil)
	r = r.WithContext(contextWithUser(r.Context(), "alice"))
	w := httptest.NewRecorder()
	f.api.HandleSessions(w, r)
	if w.Code == http.StatusMethodNotAllowed {
		t.Fatal("BLOCKED: preview retry route not implemented — required by office-preview §5.2 / T-66, architect Retry ruling 2026-11-01 Parts 1–2")
	}
	return w
}

func officeRetrySharedRenderer(t *testing.T) {
	t.Helper()
	previous := preview.Shared()
	t.Cleanup(func() { preview.SetShared(previous) })
	svc := officepreview.Service(t, false, true)
	preview.SetShared(svc)
	adm, err := svc.Admit(context.Background(), false)
	require.NoError(t, err)
	t.Cleanup(adm.Release)
	result, err := svc.Preview(context.Background(), adm, preview.Request{Name: "instrument.pdf", Ext: ".pdf", Data: officepreview.PDF(1), Default: true})
	adm.Release()
	require.NoError(t, err)
	require.Len(t, result.Pages, 1, "registered component must actually render, not just resolve")
}

func (f *officeRetryFixture) requireLibraryBytes(t *testing.T, rel string, data []byte) {
	t.Helper()
	// Exercise the actual person-facing route, not a host os.ReadFile shortcut.
	w := libGet(t, f.api, "/api/v1/library/"+f.wsID+"/download?path="+url.QueryEscape(rel))
	require.Equal(t, http.StatusOK, w.Code, "Library authority control: %s", w.Body.String())
	require.Equal(t, data, w.Body.Bytes())
}

func (f *officeRetryFixture) requireRenderedFrame(t *testing.T, frame generated.MediaFrame) {
	t.Helper()
	require.Equal(t, f.sid, frame.SessionId)
	require.NotNil(t, frame.ToolCallId)
	require.Equal(t, "retry-original-call", *frame.ToolCallId)
	require.NotNil(t, frame.Presentation)
	require.Equal(t, "page_thumbnails", *frame.Presentation)
	require.Equal(t, &generated.MediaPreview{SourceName: "report.pdf", SourceVersion: officeRetryDigest(officepreview.PDF(1)), PageCount: 1, FirstPage: 1, LastPage: 1, Outcome: "complete"}, frame.Preview)
	require.Len(t, frame.Parts, 1)
	part := frame.Parts[0]
	require.Equal(t, "image", part.Type)
	require.Equal(t, "image/png", part.ContentType)
	require.NotNil(t, part.PageNumber)
	require.Equal(t, 1, *part.PageNumber)
	require.NotNil(t, part.Caption)
	require.Equal(t, "report.pdf · page 1 of 1", *part.Caption)
	w := httptest.NewRecorder()
	f.api.HandleMedia(w, httptest.NewRequest(http.MethodGet, part.Url, nil))
	require.Equal(t, http.StatusOK, w.Code, "Retry must store retrievable pixels: %s", w.Body.String())
	img, err := png.Decode(w.Body)
	require.NoError(t, err)
	require.Equal(t, 400, img.Bounds().Dx()) // independently specified PDF: 200x100 pt, 2x
	require.Equal(t, 200, img.Bounds().Dy())
	r, g, b, a := img.At(200, 100).RGBA()
	require.Equal(t, []uint32{65535, 0, 0, 65535}, []uint32{r, g, b, a}, "page 1 independently specified red fill")
}

func officeRetryError(t *testing.T, w *httptest.ResponseRecorder, status int, code, source string) {
	t.Helper()
	require.Equal(t, status, w.Code, "%s", w.Body.String())
	var response generated.ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	require.NotNil(t, response.Code)
	require.Equal(t, code, *response.Code)
	require.NotEmpty(t, response.Error)
	require.NotContains(t, w.Body.String(), source, "refusal must not echo a source path")
	require.NotContains(t, w.Body.String(), filepath.Base(source), "even the private source name is not refusal text")
}

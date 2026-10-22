package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

// D16's native boundary is real engine -> canonical writer -> gateway/hub.
// Only the external provider/tool boundary is controlled, never these units.
type historyD16Harness struct {
	t       *testing.T
	home    string
	cfg     *config.Config
	loop    *agent.AgentLoop
	store   *session.UnifiedStore
	handler *WSHandler
	api     *restAPI
	server  *httptest.Server
	bus     *bus.MessageBus
	events  agent.EventSubscription
	cancel  context.CancelFunc
	runDone chan error
	peers   []*websocket.Conn
	close   sync.Once
}

func historyD16Config(t *testing.T) (string, *config.Config) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	cfg := config.DefaultConfig()
	cfg.Gateway.DevModeBypass = true
	cfg.Agents.Defaults.Home = filepath.Join(home, "workspace")
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "d16-frozen-model"}
	cfg.Agents.List = []config.AgentConfig{{ID: "mia", Name: "Mia", Home: cfg.Agents.Defaults.Home}}
	cfg.Sandbox.Mode = config.SandboxModeOff
	return home, cfg
}

func newHistoryD16Harness(t *testing.T, home string, cfg *config.Config, provider providers.LLMProvider, run bool) *historyD16Harness {
	t.Helper()
	ensureIsolatedTestOmnipusHome(t)
	pinConfigWorkspaceToTestHome(cfg)
	ensureTestWorkspaceMembership(t, cfg)
	messages := bus.NewMessageBus()
	loop, err := agent.NewAgentLoop(cfg, messages, provider)
	require.NoError(t, err, "D16 setup uses the real engine constructor")
	installTestSessionLauncher(t, loop)
	handler := newWSHandler(messages, loop, "")
	messages.SetStreamDelegate(handler)
	ctx, cancel := context.WithCancel(context.Background())
	h := &historyD16Harness{t: t, home: home, cfg: cfg, loop: loop, store: loop.GetSessionStore(), handler: handler,
		api: &restAPI{agentLoop: loop, homePath: home}, server: httptest.NewServer(handler), bus: messages,
		events: loop.SubscribeEvents(1024), cancel: cancel}
	require.NotNil(t, h.store)
	if run {
		h.runDone = make(chan error, 1)
		go func() { h.runDone <- loop.Run(ctx) }()
	}
	t.Cleanup(h.shutdown)
	return h
}

func (h *historyD16Harness) shutdown() {
	h.close.Do(func() {
		for _, peer := range h.peers {
			_ = peer.Close() // transport may already be closed by explicit reload
		}
		h.loop.Stop()
		h.cancel()
		if h.runDone != nil {
			select {
			case err := <-h.runDone:
				if err != nil {
					require.ErrorIs(h.t, err, context.Canceled)
				}
			case <-time.After(30 * time.Second):
				h.t.Fatal("D16 owned engine Run did not join")
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		require.True(h.t, h.loop.WaitForActiveRequestsContext(ctx), "D16 all owned turn work must join before storage reopen")
		h.loop.UnsubscribeEvents(h.events.ID)
		h.server.Close()
		h.handler.Wait()
		h.loop.Close()
		h.bus.Close()
	})
}

func (h *historyD16Harness) session() string {
	h.t.Helper()
	meta, err := h.store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(h.t, err)
	return meta.ID
}

func (h *historyD16Harness) connect() *websocket.Conn {
	h.t.Helper()
	peer, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(h.server.URL, "http"), nil)
	if response != nil && response.Body != nil {
		require.NoError(h.t, response.Body.Close())
	}
	require.NoError(h.t, err)
	h.peers = append(h.peers, peer)
	require.NoError(h.t, peer.WriteJSON(generated.AuthFrame{Type: "auth", Token: "d16-disposable-dev-token"}))
	initial := h.readUntil(peer, func(raw []byte) bool { return historyD16Type(h.t, raw) == "session_state" })
	var state generated.SessionStateFrame
	require.NoError(h.t, json.Unmarshal(initial[len(initial)-1], &state))
	require.Nil(h.t, state.SessionId, "connection-open state is unscoped")
	require.Nil(h.t, state.SessionTokensTotal, "D16 unscoped state cannot claim another session's usage")
	return peer
}

func historyD16Type(t *testing.T, raw []byte) string {
	t.Helper()
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &fields))
	var kind string
	require.NoError(t, json.Unmarshal(fields["type"], &kind))
	return kind
}

func (h *historyD16Harness) readUntil(peer *websocket.Conn, done func([]byte) bool) [][]byte {
	h.t.Helper()
	require.NoError(h.t, peer.SetReadDeadline(time.Now().Add(30*time.Second)))
	frames := make([][]byte, 0)
	for {
		_, raw, err := peer.ReadMessage()
		require.NoError(h.t, err, "D16 real socket must reach its actual protocol boundary")
		frames = append(frames, raw)
		require.NotEqual(h.t, "error", historyD16Type(h.t, raw), "D16 public ingress unexpectedly refused: %s", raw)
		if done(raw) {
			return frames
		}
	}
}

func (h *historyD16Harness) attach(peer *websocket.Conn, sid string) [][]byte {
	h.t.Helper()
	require.NoError(h.t, peer.WriteJSON(generated.AttachSessionFrame{Type: "attach_session", SessionId: sid}))
	return h.readUntil(peer, func(raw []byte) bool { return historyD16Type(h.t, raw) == "catch_up_complete" })
}

func (h *historyD16Harness) send(peer *websocket.Conn, sid, clientID, content string) {
	h.t.Helper()
	agentID := "mia"
	require.NoError(h.t, peer.WriteJSON(generated.MessageFrame{Type: "message", AgentId: &agentID,
		SessionId: &sid, ClientMessageId: &clientID, Content: content}))
}

func (h *historyD16Harness) turnDone(peer *websocket.Conn) (generated.DoneFrame, [][]byte) {
	h.t.Helper()
	var result generated.DoneFrame
	frames := h.readUntil(peer, func(raw []byte) bool {
		if historyD16Type(h.t, raw) != "done" {
			return false
		}
		require.NoError(h.t, json.Unmarshal(raw, &result))
		return result.Stats != nil && result.Stats.Tokens != nil
	})
	return result, frames
}

func (h *historyD16Harness) event(match func(agent.Event) bool) agent.Event {
	h.t.Helper()
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	for {
		select {
		case event, open := <-h.events.C:
			require.True(h.t, open, "D16 real event subscription ended before expected event")
			if match(event) {
				return event
			}
		case <-timer.C:
			h.t.Fatal("D16 genuine engine event was not observed")
		}
	}
}

func (h *historyD16Harness) page(sid, query string) generated.SessionHistoryResponse {
	h.t.Helper()
	w := httptest.NewRecorder()
	h.api.HandleSessions(w, httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+sid+"/messages"+query, nil))
	require.Equal(h.t, http.StatusOK, w.Code, w.Body.String())
	var page generated.SessionHistoryResponse
	require.NoError(h.t, json.Unmarshal(w.Body.Bytes(), &page))
	require.Equal(h.t, sid, page.SessionId)
	message, serverError := validateBodyAgainstSchema("SessionHistoryResponse", w.Body.Bytes())
	require.False(h.t, serverError)
	require.Empty(h.t, message)
	return page
}

func (h *historyD16Harness) detail(sid string) generated.SessionDetail {
	h.t.Helper()
	w := httptest.NewRecorder()
	h.api.HandleSessions(w, httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+sid, nil))
	require.Equal(h.t, http.StatusOK, w.Code, w.Body.String())
	var detail generated.SessionDetail
	require.NoError(h.t, json.Unmarshal(w.Body.Bytes(), &detail))
	require.Equal(h.t, sid, detail.Session.Id)
	return detail
}

package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// All stores and default-workspace lookup share one disposable home. The loop
// is real but never Start()ed: this test is the only inbound-bus consumer.
func l1WU1IdentitySocket(t *testing.T, validation bool) (*WSHandler, *bus.MessageBus, *agent.AgentLoop) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Home = home
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "test-model"}
	cfg.Gateway.DevModeBypass = true
	cfg.Gateway.ValidateInbound = validation
	cfg.Agents.List = []config.AgentConfig{
		{ID: "mia", Type: config.AgentTypeCore},
		{ID: "jim", Type: config.AgentTypeCore},
		{ID: "omnipus", Type: config.AgentTypeCore},
	}
	path := filepath.Join(home, "workspaces", "default.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte(`{"id":"default","name":"Default","status":"active","is_default":true,"core_team":["mia","jim","omnipus"]}`), 0o600))
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	h := newWSHandler(msgBus, al, "")
	h.home = home
	return h, msgBus, al
}

func l1WU1OpenSocket(t *testing.T, h *WSHandler) (*websocket.Conn, func()) {
	t.Helper()
	srv := httptest.NewServer(h)
	conn := dialTestWS(t, srv)
	var once sync.Once
	finish := func() {
		once.Do(func() { require.NoError(t, conn.Close()); srv.Close(); h.Wait() })
	}
	t.Cleanup(finish)
	sendWSAuthFrameDevMode(t, conn)
	return conn, finish
}

// Serialize metadata immediately, rather than holding pointers into a live
// cache. Include every durable transcript, so a refusal cannot quietly append.
func l1WU1SessionSnapshot(t *testing.T, store *session.UnifiedStore) map[string][]byte {
	t.Helper()
	metas, err := store.ListSessions()
	require.NoError(t, err)
	raw, err := json.Marshal(metas)
	require.NoError(t, err)
	out := map[string][]byte{"metadata": raw}
	for _, meta := range metas {
		entries, err := store.ReadTranscript(meta.ID)
		require.NoError(t, err)
		out[meta.ID], err = json.Marshal(entries)
		require.NoError(t, err)
	}
	return out
}

func l1WU1AssertNoInbound(t *testing.T, msgBus *bus.MessageBus) {
	t.Helper()
	select {
	case msg := <-msgBus.InboundChan():
		t.Errorf("refused input must make zero dispatch writes, got %+v", msg)
	default:
	}
}

func TestL1WU1RESTOmnipusCaseAliases_RefuseWithoutAliasOwnedWrites(t *testing.T) {
	// F01; FR-005 / §6.2; chief: normalize raw input before singleton guards.
	for _, validation := range []bool{false, true} {
		t.Run(map[bool]string{false: "validation_off", true: "validation_on"}[validation], func(t *testing.T) {
			api := l1WU2OmnipusWriteAPI(t)
			writeTestWorkspaceJSON(t, api, "default", "active", []string{"test-agent", "omnipus"}, true)
			api.agentLoop.GetConfig().Gateway.ValidateInbound = validation
			store := api.agentLoop.GetSessionStore()
			main, err := store.GetOrCreateMainSession("default", "omnipus")
			require.NoError(t, err)
			srv := httptest.NewServer(http.HandlerFunc(api.HandleSessions))
			defer srv.Close()
			call := func(body string) (int, string) {
				t.Helper()
				response, err := srv.Client().Post(srv.URL+"/api/v1/sessions", "application/json", strings.NewReader(body))
				require.NoError(t, err)
				defer response.Body.Close()
				raw, err := io.ReadAll(response.Body)
				require.NoError(t, err)
				return response.StatusCode, string(raw)
			}
			status, raw := call(`{"agent_id":"test-agent"}`)
			require.Equal(t, http.StatusCreated, status, "other-agent positive control: %s", raw)
			for _, alias := range []string{"omnipus", "OMNIPUS", "OmNiPuS"} {
				for _, workspace := range []string{"", "default"} {
					t.Run(alias+"/workspace_"+workspace, func(t *testing.T) {
						before := l1WU1SessionSnapshot(t, store)
						body, err := json.Marshal(map[string]string{"agent_id": alias, "workspace_id": workspace})
						require.NoError(t, err)
						status, raw := call(string(body))
						assert.Equal(t, http.StatusConflict, status, "raw case alias must be the same system agent: %s", raw)
						assert.Contains(t, raw, "Omnipus has one conversation")
						assert.Contains(t, raw, main.ID, "refusal names the existing canonical main")
						assert.Equal(t, before, l1WU1SessionSnapshot(t, store), "no extra or alias-owned metadata/transcript write")
					})
				}
			}
		})
	}
}

func TestL1WU1WebSocketOmnipusCaseAliases_RefuseWithoutAliasOwnedWrites(t *testing.T) {
	// F01; real raw WS frame, not a transformed decoded-input probe.
	for _, validation := range []bool{false, true} {
		for _, alias := range []string{"OMNIPUS", "OmNiPuS"} {
			for _, workspace := range []string{"", "default"} {
				label := map[bool]string{false: "off", true: "on"}[validation] + "/" + alias + "/workspace_" + workspace
				t.Run(label, func(t *testing.T) {
					h, msgBus, al := l1WU1IdentitySocket(t, validation)
					store := al.GetSessionStore()
					main, err := store.GetOrCreateMainSession("default", "omnipus")
					require.NoError(t, err)
					before := l1WU1SessionSnapshot(t, store)
					conn, finish := l1WU1OpenSocket(t, h)
					body := map[string]string{"type": "message", "content": "must not mint alias chat", "agent_id": alias}
					if workspace != "" {
						body["workspace_id"] = workspace
					}
					require.NoError(t, conn.WriteJSON(body))
					outcome := l1WU1WSMintOutcome(t, conn)
					finish() // synchronize completion before inspecting zero writes/dispatch
					assert.Equal(t, "error", outcome["type"], "raw alias must not be admitted: %v", outcome)
					assert.Contains(t, outcome["message"], "Omnipus has one conversation")
					assert.Contains(t, outcome["message"], main.ID)
					assert.Equal(t, before, l1WU1SessionSnapshot(t, store), "no extra or alias-owned writes")
					l1WU1AssertNoInbound(t, msgBus)
				})
			}
		}
	}
}

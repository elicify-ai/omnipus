package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

const wu15Workspace = "sales"
const wu15FixtureToken = "omnipus_0000000000000000000000000000000000000000000000000000000000000000"

// The room implementation is never doubled. Boot, registration, HTTP, hub,
// receiver admission and the stores are real. Only the paid provider is scripted.
// Oracles are US-17, K23/K24/K26/K27/K33/K45 and amendment R2, not room internals.
type wu15RoomHarness struct {
	t       *testing.T
	home    string
	cfg     *config.Config
	al      *agent.AgentLoop
	rs      *services
	api     *restAPI
	baseURL string
	ctx     context.Context
	cancel  context.CancelFunc
	runDone chan error
	close   sync.Once
	conns   []*websocket.Conn
}

func wu15NewHarness(t *testing.T) *wu15RoomHarness {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OMNIPUS_HOME", home)
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	// Deliberately public fixture credential in a disposable home, never a real token.
	hash, err := bcrypt.GenerateFromPassword([]byte(wu15FixtureToken), bcrypt.MinCost)
	require.NoError(t, err)
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Users: []config.UserConfig{{Username: "wu15-tester", Tokens: []config.TokenEntry{{Hash: config.BcryptHash(hash)}}}}},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Home: filepath.Join(home, "workspace"), DefaultModel: config.DefaultModel{Model: "wu15-unpriced"}, MaxTokens: 4096},
			List: []config.AgentConfig{
				{ID: "mia", Name: "Mia", Type: config.AgentTypeCustom},
				{ID: "jim", Name: "Jim", Type: config.AgentTypeCustom},
				{ID: "researcher2", Name: "Researcher2", Type: config.AgentTypeCustom},
				{ID: "ray", Name: "Ray", Type: config.AgentTypeCustom},
				{ID: "gp", Name: "GP", Type: config.AgentTypeWorker},
				{ID: "omnipus", Name: "Omnipus", Type: config.AgentTypeCore},
			},
		},
	}
	cfg.Sandbox.AuditLog = true
	cfg.Sandbox.ToolPolicies = map[string]string{"send_message": "allow", "answer_request": "allow", "ask_user_question": "allow", "goal_claim": "allow"}
	require.NoError(t, os.MkdirAll(cfg.Agents.Defaults.Home, 0o700))
	seedBindingWorkspace(t, home, wu15Workspace, "mia", "jim", "researcher2", "gp")
	seedBindingWorkspace(t, home, "marketing", "ray")
	return wu15Boot(t, home, cfg)
}

func wu15Boot(t *testing.T, home string, cfg *config.Config) *wu15RoomHarness {
	t.Helper()
	// Use the actual gateway listener, not hand-registered room handlers. The
	// ephemeral port probe is a transport fixture; no fixed/shared gateway port.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr, ok := ln.Addr().(*net.TCPAddr)
	require.True(t, ok)
	cfg.Gateway.Port = addr.Port
	require.NoError(t, ln.Close())
	require.NoError(t, config.SaveConfig(filepath.Join(home, "config.json"), cfg))
	b := bus.NewMessageBus()
	al, loopErr := agent.NewAgentLoop(cfg, b, &wu15ScriptProvider{answer: "room answer"})
	require.NoError(t, loopErr, "SETUP: real agent-loop construction")
	ctx, cancel := context.WithCancel(context.Background())
	rs, bootErr := setupAndStartServices(ctx, cfg, credentials.SecretBundle{}, al, b, home,
		newUnlockedStore(t, home), &SandboxApplyResult{}, wu15BuiltinCatalog(), tools.NewMCPRegistry(), false)
	if bootErr != nil {
		cancel()
		t.Fatalf("SETUP: real gateway boot failed: %v", bootErr)
	}
	h := &wu15RoomHarness{t: t, home: home, cfg: cfg, al: al, rs: rs, api: rs.restAPIRef,
		baseURL: fmt.Sprintf("http://127.0.0.1:%d", addr.Port), ctx: ctx, cancel: cancel}
	t.Cleanup(h.shutdown)
	require.NotNil(t, h.api, "SETUP: production boot must expose its REST API")
	// A known route is the positive transport/auth control even on the RED base.
	status, body := h.http(http.MethodGet, "/api/v1/workspaces/"+wu15Workspace, nil)
	require.Equal(t, http.StatusOK, status, "SETUP: healthy workspace route: %s", body)
	return h
}

func (h *wu15RoomHarness) shutdown() {
	h.close.Do(func() {
		for _, conn := range h.conns {
			require.NoError(h.t, conn.Close())
		}
		h.cancel()
		if h.runDone != nil {
			select {
			case err := <-h.runDone:
				require.True(h.t, wu15LoopErrorIsCancellation(err), "agent loop must exit cleanly: %v", err)
			case <-time.After(30 * time.Second):
				h.t.Error("SETUP: agent loop did not shut down")
			}
		}
		stopAndCleanupServices(h.rs, 10*time.Second, false)
		h.al.Close()
	})
}

func (h *wu15RoomHarness) startLoop() {
	h.t.Helper()
	require.Nil(h.t, h.runDone, "one real loop consumer per harness")
	h.runDone = make(chan error, 1)
	go func() { h.runDone <- h.al.Run(h.ctx) }()
}

func (h *wu15RoomHarness) http(method, path string, value any) (int, []byte) {
	h.t.Helper()
	var body []byte
	if value != nil {
		var err error
		body, err = json.Marshal(value)
		require.NoError(h.t, err)
	}
	return h.rawHTTP(method, path, body)
}

func (h *wu15RoomHarness) rawHTTP(method, path string, body []byte) (int, []byte) {
	h.t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, h.baseURL+path, bytes.NewReader(body))
	require.NoError(h.t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+wu15FixtureToken)
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	require.NoError(h.t, err, "real HTTP request %s %s", method, path)
	defer func() { require.NoError(h.t, resp.Body.Close()) }()
	data, readErr := io.ReadAll(resp.Body)
	require.NoError(h.t, readErr)
	return resp.StatusCode, data
}

func (h *wu15RoomHarness) create(kind, name string, ids ...string) gen.Room {
	h.t.Helper()
	req := gen.RoomCreateRequest{Kind: gen.RoomCreateRequestKind(kind), AgentIds: ids}
	if name != "" {
		req.Name = &name
	}
	status, data := h.http(http.MethodPost, "/api/v1/workspaces/"+wu15Workspace+"/rooms", req)
	if status == http.StatusNotFound || status == http.StatusMethodNotAllowed {
		h.t.Fatalf("BLOCKED: POST /api/v1/workspaces/{id}/rooms not implemented — required by K24/FR-161 (want 201, got %d: %s)", status, data)
	}
	require.Equal(h.t, http.StatusCreated, status, "K24: room creation: %s", data)
	var room gen.Room
	require.NoError(h.t, json.Unmarshal(data, &room))
	require.NotEmpty(h.t, room.Id, "K23: server-issued room id")
	require.Equal(h.t, wu15Workspace, room.WorkspaceId)
	require.Equal(h.t, gen.RoomKind(kind), room.Kind)
	require.ElementsMatch(h.t, ids, room.Members, "K24: exact member set")
	require.NotNil(h.t, room.Muted, "K23: muted is never null")
	return room
}

func (h *wu15RoomHarness) room(id string) gen.Room {
	h.t.Helper()
	status, data := h.http(http.MethodGet, "/api/v1/rooms/"+id, nil)
	require.Equal(h.t, http.StatusOK, status, "K23: GET room: %s", data)
	var room gen.Room
	require.NoError(h.t, json.Unmarshal(data, &room))
	return room
}

func (h *wu15RoomHarness) post(id, clientID, text string, mentions ...string) {
	h.t.Helper()
	if mentions == nil {
		mentions = []string{}
	}
	status, data := h.http(http.MethodPost, "/api/v1/rooms/"+id+"/messages", gen.RoomMessageRequest{
		ClientMessageId: clientID, Text: text, Mentions: mentions,
	})
	require.Equal(h.t, http.StatusAccepted, status, "K26: recording accepted: %s", data)
}

func (h *wu15RoomHarness) main(id string) string {
	h.t.Helper()
	sid, err := session.MainSessionID(wu15Workspace, id)
	require.NoError(h.t, err)
	return sid
}

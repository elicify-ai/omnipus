package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"syscall"
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
	bus     *bus.MessageBus
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
	// Pin the admission cap: under host memory pressure the live gate floors
	// the auto-detected cap (observed effective_concurrent_cap=2), which
	// rejects the third warmed main and stalls these fixtures at "did not
	// enter its model edge". An explicit value always wins outright, so the
	// fixture no longer depends on host memory.
	cfg.Performance.MaxParallelAgents = 8
	cfg.Sandbox.ToolPolicies = map[string]string{"send_message": "allow", "answer_request": "allow", tools.AskUserQuestionToolName: "allow", "goal_claim": "allow"}
	require.NoError(t, os.MkdirAll(cfg.Agents.Defaults.Home, 0o700))
	seedBindingWorkspace(t, home, wu15Workspace, "mia", "jim", "researcher2", "gp")
	seedBindingWorkspace(t, home, "marketing", "ray")
	return wu15Boot(t, home, cfg)
}

func wu15Boot(t *testing.T, home string, cfg *config.Config) *wu15RoomHarness {
	t.Helper()
	// Use the actual gateway listener, not hand-registered room handlers. The
	// ephemeral port probe is a transport fixture; no fixed/shared gateway port.
	//
	// The port comes from the listen/close/reuse idiom, and the window between
	// our close and the gateway's bind is a real TOCTOU race: another test
	// binary in the same `go test` run can be handed the same number. Train 3
	// PR CI lost it on 7be3eecbf — a SETUP crash naming an innocent test
	// ("bind shared HTTP server at 127.0.0.1:44991: bind: address already in
	// use"). Mirror pkg/agent/testutil's bootUntilReady: retry the WHOLE
	// allocate -> configure -> boot cycle, ONLY on EADDRINUSE, bounded, and
	// fail loudly with the last error — never a blanket try-again that could
	// mask a real boot bug. A draw that cannot even listen is a squatted
	// port; it redraws for free.
	const maxBindAttempts = 5
	var lastErr error
	var lastPort int
	for attempt := 1; attempt <= maxBindAttempts; attempt++ {
		ln, listenErr := net.Listen("tcp", "127.0.0.1:0")
		if listenErr != nil {
			lastErr = listenErr
			continue
		}
		addr, ok := ln.Addr().(*net.TCPAddr)
		if !ok {
			_ = ln.Close()
			lastErr = fmt.Errorf("listener address is %T, want *net.TCPAddr", ln.Addr())
			continue
		}
		port := addr.Port
		require.NoError(t, ln.Close())
		lastPort = port
		if wu15BootPortDrawnForTest != nil {
			wu15BootPortDrawnForTest(port)
		}

		cfg.Gateway.Port = port
		require.NoError(t, config.SaveConfig(filepath.Join(home, "config.json"), cfg))
		b := bus.NewMessageBus()
		al, loopErr := agent.NewAgentLoop(cfg, b, &wu15ScriptProvider{answer: "room answer"})
		require.NoError(t, loopErr, "SETUP: real agent-loop construction")
		ctx, cancel := context.WithCancel(context.Background())
		rs, bootErr := setupAndStartServices(ctx, cfg, credentials.SecretBundle{}, al, b, home,
			newUnlockedStore(t, home), &SandboxApplyResult{}, wu15BuiltinCatalog(), tools.NewMCPRegistry(), false)
		if bootErr == nil {
			h := &wu15RoomHarness{t: t, home: home, cfg: cfg, al: al, bus: b, rs: rs, api: rs.restAPIRef,
				baseURL: fmt.Sprintf("http://127.0.0.1:%d", port), ctx: ctx, cancel: cancel}
			t.Cleanup(h.shutdown)
			require.NotNil(t, h.api, "SETUP: production boot must expose its REST API")
			// A known route is the positive transport/auth control even on the RED base.
			status, body := h.http(http.MethodGet, "/api/v1/workspaces/"+wu15Workspace, nil)
			require.Equal(t, http.StatusOK, status, "SETUP: healthy workspace route: %s", body)
			return h
		}
		// The attempt failed: release the partial boot's context-bound loops
		// before deciding whether this is the bind race or a real defect.
		cancel()
		lastErr = bootErr
		if !errors.Is(bootErr, syscall.EADDRINUSE) {
			break // only the bind race retries
		}
		t.Logf("wu15Boot: port %d lost the bind race (attempt %d/%d): %v — redrawing a fresh port",
			port, attempt, maxBindAttempts, bootErr)
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("SETUP: real gateway boot failed in %d attempts (last port %d): %v",
		maxBindAttempts, lastPort, lastErr)
	return nil
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
		channel, found := h.rs.ChannelManager.GetChannel("webchat")
		stopAndCleanupServices(h.rs, 10*time.Second, false)
		if found {
			webchat, ok := channel.(*webchatChannel)
			require.True(h.t, ok, "SETUP: real webchat transport")
			webchat.wsHandler.Wait()
		}
		h.al.Close()
	})
}

func (h *wu15RoomHarness) startLoop() {
	h.t.Helper()
	// Q-A: stopMain may already have started the one loop to prime a main.
	// Reusing it must not add a second inbound consumer.
	if h.runDone != nil {
		return
	}
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

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Founder rule 2026-10-06: /stop-redirect <text> stops THIS chat's own turn
// (the same session-only stop as one Stop click: polite, then forced at 3 s)
// and continues THIS chat with the text, in any chat. UAT defect: in a root
// chat whose turn was streaming plain text the redirect did nothing for ~45 s
// and the instruction was appended to the uninterrupted reply.
//
// Real socket, real gateway handler, real agent loop with the production
// steering wiring; only the external model is replaced.

package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

const redirectStreamItem = "plant-list-item "

// newRedirectStreamServer builds the production-shaped gateway around provider
// and returns the server, the loop and a valid bearer token.
func newRedirectStreamServer(t *testing.T, provider providers.LLMProvider) (*httptest.Server, *agent.AgentLoop, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	token := "omnipus_" + strings.Repeat("7", 64)
	hash, err := bcrypt.GenerateFromPassword([]byte(token), bcrypt.MinCost)
	require.NoError(t, err)
	workspaceDir := filepath.Join(home, "workspace")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o755))
	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			Host: "127.0.0.1", Port: 0,
			Users: []config.UserConfig{{Username: "redirect-user", Tokens: []config.TokenEntry{{Hash: config.BcryptHash(hash)}}}},
		},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Home: workspaceDir, DefaultModel: config.DefaultModel{Model: "redirect-streaming"}, MaxTokens: 4096},
			List:     []config.AgentConfig{{ID: "mia", Home: workspaceDir}},
		},
		Sandbox: config.OmnipusSandboxConfig{Mode: config.SandboxModeOff},
	}
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	al := mustAgentLoop(t, cfg, msgBus, provider)

	// The production steering wiring (gateway_boot.go::wireSteerDeps): a
	// lifecycle store, a minted boot epoch and the gateway's canceller.
	boot := session.NewBootEpochStore(home)
	_, err = boot.Mint()
	require.NoError(t, err)
	al.SetBootEpochStore(boot)
	lifecycle := session.NewLifecycleStore(filepath.Join(home, "session_lifecycle"))
	al.SetSessionMessagingStores(session.NewMessageInboxStore(filepath.Join(home, "session_messages")), lifecycle)
	setGatewaySteerCanceller(al, agent.NewSteerCanceller(lifecycle, al.SteerGenerationCancel).
		SetRevivalStateWriter(al.WriteSteerRevivalState))
	t.Cleanup(func() { gatewaySteerCancellers.Delete(al) })

	runCtx, cancelRun := context.WithCancel(context.Background())
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		_ = al.Run(runCtx)
	}()
	t.Cleanup(func() {
		cancelRun()
		select {
		case <-runDone:
		case <-time.After(30 * time.Second):
			t.Log("agent loop Run did not exit within 30s")
		}
	})
	time.Sleep(20 * time.Millisecond)

	handler := newWSHandler(msgBus, al, "")
	msgBus.SetStreamDelegate(handler)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	t.Cleanup(handler.Wait)

	return srv, al, token
}

// sseModel is a local OpenAI-compatible endpoint: request 1 streams a long
// plain-text list over SSE (a chunk every few ms) until the client hangs up;
// every later request is answered at once. It lets the REAL HTTP provider
// (streaming parser, cancellation, stall watch) run under the redirect.
type sseModel struct {
	mu        sync.Mutex
	bodies    []string
	started   chan struct{}
	hungUp    chan struct{}
	finished  chan struct{}
	requestNo int
}

func (m *sseModel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	m.mu.Lock()
	m.requestNo++
	n := m.requestNo
	m.bodies = append(m.bodies, string(raw))
	m.mu.Unlock()
	w.Header().Set("Content-Type", "text/event-stream")
	flusher, _ := w.(http.Flusher)
	send := func(content string) {
		chunk, _ := json.Marshal(map[string]any{"choices": []map[string]any{{"delta": map[string]any{"content": content}}}})
		_, _ = fmt.Fprintf(w, "data: %s\n\n", chunk)
		if flusher != nil {
			flusher.Flush()
		}
	}
	done := func(reason string) {
		fin, _ := json.Marshal(map[string]any{"choices": []map[string]any{{"delta": map[string]any{}, "finish_reason": reason}}})
		_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", fin)
		if flusher != nil {
			flusher.Flush()
		}
	}
	if n > 1 {
		send("redirected answer")
		done("stop")
		return
	}
	m.started <- struct{}{}
	for i := 0; i < 20000; i++ {
		select {
		case <-r.Context().Done():
			m.hungUp <- struct{}{}
			return
		case <-time.After(5 * time.Millisecond):
		}
		send(fmt.Sprintf("%s%d. ", redirectStreamItem, i))
	}
	m.finished <- struct{}{}
	done("stop")
}

func (m *sseModel) Bodies() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.bodies...)
}

func TestWSRedirect_StreamingTextTurnOverTheRealHTTPProvider(t *testing.T) {
	model := &sseModel{started: make(chan struct{}, 2), hungUp: make(chan struct{}, 2), finished: make(chan struct{}, 2)}
	endpoint := httptest.NewServer(model)
	t.Cleanup(endpoint.Close)
	provider, err := providers.NewHTTPProviderWithTimeouts("test-key", endpoint.URL, "", "", 60, 0, nil)
	require.NoError(t, err)
	srv, _, token := newRedirectStreamServer(t, provider)

	conn := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })
	write := func(v any) {
		raw, err := json.Marshal(v)
		require.NoError(t, err)
		require.NoError(t, conn.WriteMessage(websocket.TextMessage, raw))
	}
	write(wsClientFrameTestHelper{Type: "auth", Token: token})
	write(wsClientFrameTestHelper{Type: "message", Content: "list 150 plants, one per line"})
	sessionID := readFrameOfType(t, conn, "session_started", 5*time.Second).SessionID
	require.NotEmpty(t, sessionID)
	select {
	case <-model.started:
	case <-time.After(8 * time.Second):
		t.Fatal("SETUP: the long stream never started")
	}
	time.Sleep(300 * time.Millisecond)

	const instruction = "now just say the word mango"
	write(generated.RedirectFrame{Type: "redirect", SessionId: sessionID, Instruction: instruction})
	select {
	case <-model.hungUp:
	case <-model.finished:
		t.Fatal("the streaming turn ran to its natural end: /stop-redirect did not stop it")
	case <-time.After(6 * time.Second):
		t.Fatal("the streaming turn was still streaming 6s after /stop-redirect")
	}
	deadline := time.Now().Add(10 * time.Second)
	for len(model.Bodies()) < 2 {
		require.True(t, time.Now().Before(deadline), "the continued turn never reached the model")
		time.Sleep(20 * time.Millisecond)
	}
	var req struct {
		Messages []struct {
			Role    string `json:"role"`
			Content any    `json:"content"`
		} `json:"messages"`
	}
	require.NoError(t, json.Unmarshal([]byte(model.Bodies()[1]), &req))
	lastUser := ""
	for _, m := range req.Messages {
		if m.Role == "user" {
			lastUser, _ = m.Content.(string)
		}
	}
	require.Equal(t, instruction, lastUser, "the continued turn's last user message is the exact instruction")
	require.Len(t, model.Bodies(), 2, "stopped stream + exactly one continued turn")
}

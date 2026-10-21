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
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

type historyUS16Harness struct {
	loop           *agent.AgentLoop
	api            *restAPI
	reg            *approvalRegistryV2
	handler        *WSHandler
	server         *httptest.Server
	provider       *historyUS16Provider
	sid, workDir   string
	events         agent.EventSubscription
	observedEvents []agent.Event
	cancel         context.CancelFunc
	runDone        chan struct{}
	runErr         error
	stopOnce       sync.Once
}

func newHistoryUS16Harness(t *testing.T) *historyUS16Harness {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	t.Setenv("OMNIPUS_BEARER_TOKEN", historyUS16Token)
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "test-model"}
	cfg.Gateway.ValidateInbound = true
	cfg.Sandbox.AutoApprove = false // Genuine operator Ask, not Auto/God mode.
	cfg.Sandbox.ToolPolicies["write_file"] = "ask"
	seedTestAgents(cfg)
	messages := bus.NewMessageBus()
	t.Cleanup(messages.Close)
	provider := &historyUS16Provider{resumed: make(chan struct{}), cancelled: make(chan struct{})}
	loop := mustAgentLoop(t, cfg, messages, provider)
	lifecycle := session.NewLifecycleStore(filepath.Join(home, "us16-lifecycle"))
	loop.SetSessionMessagingStores(session.NewMessageInboxStore(filepath.Join(home, "us16-inbox")), lifecycle)
	setGatewaySteerCanceller(loop, agent.NewSteerCanceller(lifecycle, loop.SteerGenerationCancel))
	t.Cleanup(func() { gatewaySteerCancellers.Delete(loop) })
	classifier := agent.NewSteerRecordClassifier(lifecycle, loop.GetSessionStore())
	loop.SetSteerAudienceDeps(agent.NewSteerAudienceResolver(classifier), steer.NopBoundaryObserver{}, agent.NewSteerUpwardDeliverer())
	reg := newApprovalRegistryV2(64, 10*time.Minute)
	handler := newWSHandler(messages, loop, "")
	reg.setResolutionListener(handler.broadcastToolApprovalResolved)
	handler.approvalRegV2 = reg
	loop.SetToolApprover(newPolicyApproverAdapter(reg, handler))
	messages.SetStreamDelegate(handler) // Real WS streamer; this new bus has no prior delegate.
	api := &restAPI{agentLoop: loop, homePath: home, approvalReg: reg}
	mux := http.NewServeMux()
	mux.Handle("/ws", handler)
	mux.Handle("/api/v1/tool-approvals/", api.withAuth(api.HandleToolApprovals))
	sid := pagingUnindexedSession(t, loop.GetSessionStore())
	workDir, workErr := workspace.EnsureWorkDir(home, testHarnessWorkspaceMembershipID)
	require.NoError(t, workErr)
	ctx, cancel := context.WithCancel(context.Background())
	h := &historyUS16Harness{loop: loop, api: api, reg: reg, handler: handler, provider: provider, server: httptest.NewServer(mux), sid: sid, workDir: workDir, events: loop.SubscribeEvents(256), cancel: cancel, runDone: make(chan struct{})}
	t.Cleanup(func() {
		// Cleanup still closes network resources if a join assertion fails.
		defer h.loop.UnsubscribeEvents(h.events.ID)
		defer h.handler.Wait()
		defer h.server.Close()
		h.stop(t)
	})
	go func() { defer close(h.runDone); h.runErr = loop.Run(ctx) }()
	return h
}

func (h *historyUS16Harness) stop(t *testing.T) {
	t.Helper()
	h.stopOnce.Do(func() {
		h.loop.Stop()
		h.cancel()
		for _, entry := range h.reg.pendingApprovals() {
			h.reg.resolve(entry.ApprovalID, ApprovalActionCancel, false)
		}
	})
	historyUS16Await(t, h.runDone, "owned ordinary dispatch loop joined")
	require.NoError(t, h.runErr)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	require.True(t, h.loop.WaitForActiveRequestsContext(ctx), "owned engine work must actually drain")
}

func (h *historyUS16Harness) connect(t *testing.T) *historyUS16Peer {
	t.Helper()
	conn, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(h.server.URL, "http")+"/ws", nil)
	if err != nil && response != nil && response.Body != nil {
		require.NoError(t, response.Body.Close())
	}
	require.NoError(t, err)
	peer := &historyUS16Peer{conn: conn}
	t.Cleanup(func() { peer.close(t) })
	peer.send(t, generated.AuthFrame{Type: "auth", Token: historyUS16Token})
	peer.until(t, "session_state") // Actual successful auth before session ingress.
	return peer
}

func (p *historyUS16Peer) until(t *testing.T, kind string) [][]byte {
	t.Helper()
	var frames [][]byte
	require.NoError(t, p.conn.SetReadDeadline(time.Now().Add(15*time.Second)))
	for {
		_, raw, readErr := p.conn.ReadMessage()
		require.NoError(t, readErr, "actual socket did not deliver %s", kind)
		var envelope wsTypeOnly
		require.NoError(t, json.Unmarshal(raw, &envelope))
		frames = append(frames, raw)
		if envelope.Type == kind {
			return frames
		}
		require.NotEqual(t, "error", envelope.Type, "unexpected real gateway error before %s: %s", kind, raw)
	}
}

func (h *historyUS16Harness) attach(t *testing.T, peer *historyUS16Peer) [][]byte {
	t.Helper()
	peer.send(t, generated.AttachSessionFrame{Type: "attach_session", SessionId: h.sid})
	return peer.until(t, "catch_up_complete")
}

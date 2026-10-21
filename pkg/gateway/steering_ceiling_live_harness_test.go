package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

type ceilingLiveHarness struct {
	al        *agent.AgentLoop
	handler   *WSHandler
	store     *session.UnifiedStore
	sid       string
	conn      *websocket.Conn
	provider  *ceilingLiveProvider
	tool      *ceilingLiveTool
	sub       agent.EventSubscription
	outbound  <-chan bus.OutboundMessage
	delivered <-chan terminalAcceptanceDelivery
	relayGate chan struct{}
	relayOnce sync.Once
}

func newCeilingLiveHarness(t *testing.T, budget int, streaming bool) *ceilingLiveHarness {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OMNIPUS_HOME", home)
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", DevModeBypass: true},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Home: filepath.Join(home, "workspace"), DefaultModel: config.DefaultModel{Model: ceilingLiveModel}, MaxTokens: 4096, MaxToolIterations: 10},
			List:     []config.AgentConfig{{ID: "mia", MaxToolIterations: budget}},
		},
	}
	cfg.Sandbox.ToolPolicies = config.DefaultToolPolicyCeiling()
	cfg.Sandbox.ToolPolicies[ceilingLiveToolName] = "allow"
	messageBus := bus.NewMessageBus()
	t.Cleanup(messageBus.Close)
	provider := &ceilingLiveProvider{}
	var model providers.LLMProvider = provider
	if streaming {
		model = &ceilingLiveStreamingProvider{provider}
	}
	al := mustAgentLoop(t, cfg, messageBus, model)
	tool := newCeilingLiveTool()
	al.RegisterTool(tool)
	handler := newWSHandler(messageBus, al, "")
	handler.webchatCh = newWebchatChannel(handler)
	// Match production: the bus delegates through Manager, then its WS fallback.
	manager, err := channels.NewManager(cfg, credentials.SecretBundle{}, messageBus, nil)
	require.NoError(t, err)
	manager.SetStreamFallback(handler)
	al.SetChannelManager(manager)
	store := al.GetSessionStore()
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	sub := al.SubscribeEvents(512)
	t.Cleanup(func() { al.UnsubscribeEvents(sub.ID) })
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- al.Run(ctx) }()
	outbound := make(chan bus.OutboundMessage, 8)
	delivered := make(chan terminalAcceptanceDelivery, 8)
	gate := make(chan struct{})
	relayDone := make(chan struct{})
	go func() {
		defer close(relayDone)
		for {
			select {
			case <-ctx.Done():
				return
			case message, ok := <-messageBus.OutboundChan():
				if !ok {
					return
				}
				outbound <- message
				select {
				case <-gate:
				case <-ctx.Done():
					return
				}
				delivered <- terminalAcceptanceDelivery{message: message, err: handler.webchatCh.Send(ctx, message)}
			}
		}
	}()
	srv := httptest.NewServer(handler)
	conn := dialTestWS(t, srv)
	sendWSAuthFrameDevMode(t, conn)
	h := &ceilingLiveHarness{al: al, handler: handler, store: store, sid: meta.ID, conn: conn,
		provider: provider, tool: tool, sub: sub, outbound: outbound, delivered: delivered, relayGate: gate}
	t.Cleanup(func() {
		tool.openAll()
		h.openRelay()
		assert.NoError(t, conn.Close())
		srv.Close()
		cancel()
		handler.Wait()
		select {
		case err := <-runDone:
			if !errors.Is(err, context.Canceled) {
				assert.NoError(t, err)
			}
		case <-time.After(busDeliveryTimeout):
			t.Error("real loop did not finish")
		}
		select {
		case <-relayDone:
		case <-time.After(busDeliveryTimeout):
			t.Error("outbound relay did not finish")
		}
	})
	return h
}

func (h *ceilingLiveHarness) openRelay() { h.relayOnce.Do(func() { close(h.relayGate) }) }

func (h *ceilingLiveHarness) send(t *testing.T, text string, number int) {
	t.Helper()
	agentID, clientID := "mia", fmt.Sprintf("ceiling-live-input-%d", number)
	require.NoError(t, h.conn.WriteJSON(generated.MessageFrame{Type: "message", AgentId: &agentID,
		SessionId: &h.sid, ClientMessageId: &clientID, Content: text}))
}

func (h *ceilingLiveHarness) awaitTool(t *testing.T, index int) {
	t.Helper()
	select {
	case <-h.tool.entered[index]:
	case <-time.After(busDeliveryTimeout):
		t.Fatalf("tool %d never reached its real execution gate", index+1)
	}
}

func (h *ceilingLiveHarness) awaitSteers(t *testing.T, count int) {
	t.Helper()
	for accepted := 0; accepted < count; {
		select {
		case event := <-h.sub.C:
			if event.Kind == agent.EventKindInterruptReceived {
				accepted++
			}
		case <-time.After(busDeliveryTimeout):
			t.Fatalf("only %d of %d follow-ups reached live steering", accepted, count)
		}
	}
}

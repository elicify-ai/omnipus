package gateway

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
)

// connectPairingChannel models the WhatsApp channel the connect starts: its
// Start begins a pairing attempt through the production emitter
// (EmitWhatsAppPairing), exactly as the real channel's pairing observer does.
type connectPairingChannel struct {
	al         *agent.AgentLoop
	id         string
	running    atomic.Bool
	stopCalled chan struct{}
}

func (c *connectPairingChannel) Name() string { return c.id }
func (c *connectPairingChannel) Start(context.Context) error {
	c.running.Store(true)
	c.al.EmitWhatsAppPairing(c.id, channels.PairingStatusWaiting, "", "")
	c.al.EmitWhatsAppPairing(c.id, channels.PairingStatusCode, "QR-F3-CODE", "")
	return nil
}
func (c *connectPairingChannel) Stop(context.Context) error                      { c.running.Store(false); return nil }
func (c *connectPairingChannel) Send(context.Context, bus.OutboundMessage) error { return nil }
func (c *connectPairingChannel) IsRunning() bool                                 { return c.running.Load() }
func (c *connectPairingChannel) IsAllowed(string) bool                           { return true }
func (c *connectPairingChannel) IsAllowedSender(bus.SenderInfo) bool             { return true }
func (c *connectPairingChannel) ReasoningChannelID() string                      { return "" }
func (c *connectPairingChannel) StopPairing(context.Context) bool {
	c.stopCalled <- struct{}{}
	return true
}

// TestPairingStop_ConnectFramesCarryTheExactConnectCall is Train 2 gate r2
// pr-test F3: the approved connect_channel call's own tool-call id must be
// the pairing request id — not merely a non-empty one — on the code and
// timeout frames the gateway forwarder delivers to the chat. (The exact-id
// stopped frame after a chat Stop is pinned by the Retry ownership tests.)
func TestPairingStop_ConnectFramesCarryTheExactConnectCall(t *testing.T) {
	w := newWU6Fixture(t)
	seedWU5Membership(t, w.f1277.home)
	const waInstance = "whatsapp.wu7"
	const connectCallID = "wu7-connect-wa-exact"
	ch := &connectPairingChannel{al: w.al, id: waInstance, stopCalled: make(chan struct{}, 2)}
	w.svc.ChannelManager.RegisterChannel(waInstance, ch)
	// The fixture has no reload pipeline; with the reload reported complete
	// the connect reaches its real explicit start (EnsureChannelStarted).
	w.al.WireSysagentDeps(&systools.Deps{
		Home: w.f1277.home, ConfigPath: w.f1277.configPath,
		GetCfg: w.al.GetConfig, MutateConfig: w.al.MutateConfig,
		SaveConfigLocked:  func(c *config.Config) error { return config.SaveConfig(w.f1277.configPath, c) },
		CredStore:         w.store,
		WaitForReloadFunc: func() error { return nil },
	})

	main, err := w.al.GetSessionStore().GetOrCreateMainSession(testHarnessWorkspaceMembershipID, "omnipus")
	require.NoError(t, err)
	owner := wu6Owner
	require.NoError(t, w.al.GetSessionStore().SetMeta(main.ID, session.MetaPatch{Owner: &owner}))

	// The chat's pairing view, through the production forwarder.
	h := makeMinimalHandler()
	h.agentLoop = w.al
	wc, frames := makeForwarderTestConn(64)
	wc.setPairingInterest(waInstance, true)
	go h.eventForwarder(wc, "wu7-wa-connect", w.al.SubscribeEvents(64), make(chan struct{}))

	w.provider.setDefaultScript([]providers.LLMResponse{
		{ToolCalls: []providers.ToolCall{{ID: "wu7-ts-connect-wa", Type: "function", Name: "ToolSearch",
			Arguments: map[string]any{"names": []any{"connect_channel"}}}}},
		{ToolCalls: []providers.ToolCall{{ID: connectCallID, Type: "function", Name: "connect_channel",
			Arguments: map[string]any{"id": waInstance, "workspace_id": "sales", "agent_id": "mia", "expected_revision": "absent"}}}},
		{Content: "done", FinishReason: "stop"},
	})
	pubCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, w.bus.PublishInbound(pubCtx, bus.InboundMessage{
		Channel: "webchat", ChatID: "wu7-wa-connect", SessionID: main.ID,
		Content: "connect the WhatsApp channel whatsapp.wu7", UserInitiated: true,
		Metadata: map[string]string{"agent_id": "omnipus", "workspace_id": "sales"},
	}))
	w.awaitApproval("connect_channel", 15*time.Second)
	w.approveNext()

	requireFrame := func(status string) map[string]any {
		t.Helper()
		deadline := time.After(20 * time.Second)
		for {
			select {
			case data := <-frames:
				var frame map[string]any
				require.NoError(t, json.Unmarshal(data, &frame))
				if frame["type"] == "whatsapp_pairing" && frame["status"] == status {
					return frame
				}
			case <-deadline:
				t.Fatalf("no forwarded %q pairing frame reached the chat", status)
			}
		}
	}
	code := requireFrame("code")
	require.Equal(t, connectCallID, code["request_id"], "the code frame names exactly this connect call")
	require.Equal(t, main.ID, code["session_id"])

	w.al.EmitWhatsAppPairing(waInstance, channels.PairingStatusTimeout, "", "the QR code expired before it was scanned")
	timeout := requireFrame("timeout")
	require.Equal(t, connectCallID, timeout["request_id"], "the timeout frame names exactly this connect call")
	require.Equal(t, main.ID, timeout["session_id"])
	require.Equal(t, "the QR code expired before it was scanned", timeout["message"])
}

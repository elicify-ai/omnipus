package channels

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"golang.org/x/time/rate"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// Oracle: spec C-REPLY "Return authorization" — a rebound, deleted or
// unauthorized capture refuses with no replacement; OwnershipChecked alone and
// the system-origin exemption are insufficient; the author's own pair grants
// nothing (BDD-08.9: no guest grant, I2 negative recipient).

func boundCfg(instance, ws, agentID string) *config.Config {
	cfg := &config.Config{Channels: map[string]config.ChannelInstanceConfig{}}
	cfg.Channels[instance] = config.ChannelInstanceConfig{
		WorkspaceID: ws,
		Identity:    &config.ChannelIdentity{Kind: config.ChannelIdentityKindAgent, ID: agentID},
	}
	return cfg
}

func answer(instance string) bus.OutboundMessage {
	return bus.OutboundMessage{
		Channel: instance, ChatID: "chat-1", Content: "the answer",
		AgentID: "jim", WorkspaceID: "ws-1", // the GUEST author
		OwnershipChecked: true, // must not matter
		Return: &bus.ReturnRoute{
			RequestID: "q1", SourceSessionID: "main-session-ws-1+mia",
			OwnerWorkspaceID: "ws-1", OwnerAgentID: "mia",
		},
	}
}

func TestCheckReturnRoute(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *config.Config
		mutate  func(*bus.OutboundMessage)
		refused bool
	}{
		{"still owned by the captured source owner", boundCfg("telegram.a", "ws-1", "mia"), nil, false},
		{"rebound to another agent", boundCfg("telegram.a", "ws-1", "ray"), nil, true},
		{"rebound to another workspace", boundCfg("telegram.a", "ws-2", "mia"), nil, true},
		{"unbound since", &config.Config{Channels: map[string]config.ChannelInstanceConfig{"telegram.a": {}}}, nil, true},
		{"instance deleted", &config.Config{Channels: map[string]config.ChannelInstanceConfig{}}, nil, true},
		{"nil config fails closed", nil, nil, true},
		{"the guest author's own pair is not a grant", boundCfg("telegram.a", "ws-1", "jim"), nil, true},
		{"no request id", boundCfg("telegram.a", "ws-1", "mia"), func(m *bus.OutboundMessage) { m.Return.RequestID = "" }, true},
		{"no destination chat", boundCfg("telegram.a", "ws-1", "mia"), func(m *bus.OutboundMessage) { m.ChatID = "" }, true},
		{"captured unbound, still unbound", &config.Config{Channels: map[string]config.ChannelInstanceConfig{"telegram.a": {}}},
			func(m *bus.OutboundMessage) { m.Return.OwnerWorkspaceID, m.Return.OwnerAgentID = "", "" }, false},
		{"captured unbound, bound since", boundCfg("telegram.a", "ws-1", "mia"),
			func(m *bus.OutboundMessage) { m.Return.OwnerWorkspaceID, m.Return.OwnerAgentID = "", "" }, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := answer("telegram.a")
			if tt.mutate != nil {
				tt.mutate(&msg)
			}
			err := checkReturnRoute(tt.cfg, msg)
			if tt.refused {
				if !errors.Is(err, ErrReturnRouteRefused) {
					t.Fatalf("want ErrReturnRouteRefused, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("want admitted, got %v", err)
			}
		})
	}
}

func TestCheckReturnRoute_NoRouteIsOutOfScope(t *testing.T) {
	if err := checkReturnRoute(nil, bus.OutboundMessage{Channel: "x", ChatID: "c"}); err != nil {
		t.Fatalf("a message without a return route must pass untouched, got %v", err)
	}
}

func routedWorker(sent *atomic.Int32, sendErr error) *channelWorker {
	ch := &mockChannel{sendFn: func(_ context.Context, _ bus.OutboundMessage) error {
		sent.Add(1)
		return sendErr
	}}
	return &channelWorker{ch: ch, limiter: rate.NewLimiter(rate.Inf, 1)}
}

// The final send boundary re-reads the CURRENT config: a rebind between
// dispatch and send refuses, and nothing reaches the channel.
func TestSendWithRetry_RefusesAnswerAfterRebind(t *testing.T) {
	m := newTestManager()
	m.liveCfg.Store(boundCfg("telegram.a", "ws-1", "ray")) // rebound after admission
	var sent atomic.Int32
	var observed atomic.Int32
	m.SetReturnRefusalObserver(func(_ bus.OutboundMessage, err error) {
		if errors.Is(err, ErrReturnRouteRefused) {
			observed.Add(1)
		}
	})
	before := ReturnRouteRefusals()

	m.sendWithRetry(context.Background(), "telegram.a", routedWorker(&sent, nil), answer("telegram.a"), false)

	if sent.Load() != 0 {
		t.Fatalf("Send called %d times after a rebind; want 0", sent.Load())
	}
	if observed.Load() != 1 || ReturnRouteRefusals() != before+1 {
		t.Fatalf("refusal must be observed and counted once (observed=%d counted=%d)", observed.Load(), ReturnRouteRefusals()-before)
	}
}

func TestSendWithRetry_DeliversAnswerWhileBindingHolds(t *testing.T) {
	m := newTestManager()
	m.liveCfg.Store(boundCfg("telegram.a", "ws-1", "mia"))
	var sent atomic.Int32
	m.sendWithRetry(context.Background(), "telegram.a", routedWorker(&sent, nil), answer("telegram.a"), false)
	if sent.Load() != 1 {
		t.Fatalf("Send called %d times with the binding intact; want 1", sent.Load())
	}
}

// A rebind that lands between the first failed attempt and its retry stops
// the retry: no second attempt reaches the connector.
func TestSendWithRetry_RebindBetweenAttemptsStopsRetry(t *testing.T) {
	m := newTestManager()
	m.liveCfg.Store(boundCfg("telegram.a", "ws-1", "mia"))
	var sent atomic.Int32
	ch := &mockChannel{sendFn: func(_ context.Context, _ bus.OutboundMessage) error {
		sent.Add(1)
		m.liveCfg.Store(boundCfg("telegram.a", "ws-1", "ray")) // rebind during the first attempt
		return ErrTemporary
	}}
	w := &channelWorker{ch: ch, limiter: rate.NewLimiter(rate.Inf, 1)}

	m.sendWithRetry(context.Background(), "telegram.a", w, answer("telegram.a"), true)

	if sent.Load() != 1 {
		t.Fatalf("Send called %d times; the retry after a rebind must be refused (want exactly 1)", sent.Load())
	}
}

// A refused answer must not touch the placeholder either.
func TestSendWithRetry_RefusedAnswerLeavesPlaceholderAlone(t *testing.T) {
	m := newTestManager()
	m.liveCfg.Store(boundCfg("telegram.a", "ws-1", "ray"))
	ch := &mockChannel{sendFn: func(context.Context, bus.OutboundMessage) error { return nil }}
	m.RecordPlaceholder("telegram.a", "chat-1", "ph-1")
	w := &channelWorker{ch: ch, limiter: rate.NewLimiter(rate.Inf, 1)}

	m.sendWithRetry(context.Background(), "telegram.a", w, answer("telegram.a"), false)

	if ch.editedMessages != 0 {
		t.Fatalf("placeholder edited %d times for a refused answer; want 0", ch.editedMessages)
	}
}

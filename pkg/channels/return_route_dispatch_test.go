package channels

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/time/rate"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// guestAnswer is what the server's reply path really publishes: the GUEST is the
// author, OwnershipChecked stays false, and the captured owner is in Return.
func guestAnswer(instance string) bus.OutboundMessage {
	m := answer(instance)
	m.OwnershipChecked = false
	return m
}

// F6: a valid captured return from a guest is authorized by the captured route,
// not refused by the ordinary ownership check; a broken route and an ordinary
// non-owner send are still refused.
func TestAuthorizeOutbound_GuestReturnAuthorizedByRouteNotOwnership(t *testing.T) {
	m := newTestManager()
	m.config = boundCfg("telegram.a", "ws-1", "mia") // still owned by the captured owner
	if !m.authorizeOutbound(guestAnswer("telegram.a")) {
		t.Fatal("a valid captured return from a guest must be authorized (router -> dispatch)")
	}

	m.config = boundCfg("telegram.a", "ws-1", "ray") // rebound since admission
	if m.authorizeOutbound(guestAnswer("telegram.a")) {
		t.Fatal("a rebound instance must refuse the captured return")
	}

	// Ownership is not weakened for anything else: jim (non-owner) sending with
	// no captured route and no ownership decision is still refused.
	m.config = boundCfg("telegram.a", "ws-1", "mia")
	plain := guestAnswer("telegram.a")
	plain.Return = nil
	if m.authorizeOutbound(plain) {
		t.Fatal("an ordinary agent-originated send by a non-owner must still be refused")
	}
}

// Router -> bus -> real dispatcher -> worker -> Send, with OwnershipChecked
// false: the guest's answer reaches the channel; after a rebind it does not.
func TestDispatchOutbound_GuestReturnReachesSendAndRebindStopsIt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m, mb := newDeliveryManager(ctx)
	defer mb.Close()
	cfg := boundCfg("telegram.a", "ws-1", "mia")
	m.config = cfg
	m.liveCfg.Store(cfg)

	delivered := make(chan bus.OutboundMessage, 2)
	m.RegisterChannel("telegram.a", &mockChannel{sendFn: func(_ context.Context, msg bus.OutboundMessage) error {
		delivered <- msg
		return nil
	}})

	if err := mb.PublishOutbound(context.Background(), guestAnswer("telegram.a")); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-delivered:
		if got.AgentID != "jim" || got.Content != "the answer" {
			t.Fatalf("delivered = %+v; the guest must stay the author", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("a valid guest return never reached Send: dispatch dropped it before the route check")
	}

	rebound := boundCfg("telegram.a", "ws-1", "ray")
	m.config = rebound
	m.liveCfg.Store(rebound)
	if err := mb.PublishOutbound(context.Background(), guestAnswer("telegram.a")); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-delivered:
		t.Fatalf("an answer was delivered after the instance was rebound: %+v", got)
	case <-time.After(500 * time.Millisecond):
	}
}

// editRebinds is a channel whose placeholder edit rebinds the instance and then
// fails - the F2 window between the pre-send check and the first Send.
type editRebinds struct {
	mockChannel
	rebind func()
}

func (e *editRebinds) EditMessage(_ context.Context, _, _ string, _ string) error {
	e.rebind()
	return errors.New("edit failed")
}

// F2: a rebind during a failing placeholder edit must stop the FIRST Send too.
func TestSendWithRetry_RebindDuringFailedPlaceholderEditStopsTheFirstSend(t *testing.T) {
	m := newTestManager()
	m.liveCfg.Store(boundCfg("telegram.a", "ws-1", "mia"))
	var sent atomic.Int32
	ch := &editRebinds{rebind: func() { m.liveCfg.Store(boundCfg("telegram.a", "ws-1", "ray")) }}
	ch.sendFn = func(context.Context, bus.OutboundMessage) error { sent.Add(1); return nil }
	m.RecordPlaceholder("telegram.a", "chat-1", "ph-1")
	w := &channelWorker{ch: ch, limiter: rate.NewLimiter(rate.Inf, 1)}
	before := ReturnRouteRefusals()

	m.sendWithRetry(context.Background(), "telegram.a", w, guestAnswer("telegram.a"), false)

	if sent.Load() != 0 {
		t.Fatalf("Send ran %d times after a rebind during the failed edit; want 0", sent.Load())
	}
	if ReturnRouteRefusals() != before+1 {
		t.Fatal("the refusal at the first send must be counted")
	}
}

var _ = config.ChannelInstanceConfig{}

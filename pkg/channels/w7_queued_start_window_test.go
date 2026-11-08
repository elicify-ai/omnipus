package channels

// Founder decision 2026-11-08 08:13Z, Q2 A (team-lead brief, round 7): a
// queued Operation=start message is NOT dropped while the channel is in the
// not-running window (the state issue #1687's failing managed reconnect
// produces); it is kept and delivered once the channel is running again,
// exactly once.
//
// The pin lives on the manager's own start pipeline
// (manager.go::dispatchOutbound → enqueueOutbound → runWorker →
// sendWithRetry's dequeue check, manager_outbound.go::startMayProceed — the
// exact drop the round-3 developer report cites) and uses a
// starts-supported probe channel type ("telegram"), because a WhatsApp start
// can never reach this pipeline: StartAllowed refuses WhatsApp at row e
// (PlatformStartRuleFor("whatsapp").StartSupported is false — the T-1
// WhatsApp cannot-start rule) both at the send_message tool
// (pkg/agent/message_start_policy.go::CheckStart) and at dispatch, before
// anything can be queued. The not-running window the probe simulates is the
// same observable state the WhatsApp reconnect failure produces on the
// manager's surfaces: registered, started once, IsRunning false.
//
// Oracle: the founder decision, not the current code. RED on today's code:
// the dequeue check refuses the queued start (row f via startMayProceed) and
// drops it with one info log ("refused a queued start").

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
)

// w7QueueProbeChannel is a Channel double whose Send records every
// Operation=start handoff ("delivered" = the manager handed the message to
// the channel) and whose running flag the test flips to produce the
// not-running window. All behaviour the start pipeline reads (IsRunning,
// start bookkeeping) is real state; only the wire send is captured, which is
// the boundary this pin is about.
type w7QueueProbeChannel struct {
	name string
	mu   sync.Mutex
	run  bool
	gate chan struct{} // when non-nil, the next Send parks on it once
	dels []string      // ChatID of every Operation=start handed to Send
}

func (c *w7QueueProbeChannel) Name() string { return c.name }
func (c *w7QueueProbeChannel) Start(context.Context) error {
	c.setRun(true)
	return nil
}
func (c *w7QueueProbeChannel) Stop(context.Context) error {
	c.setRun(false)
	return nil
}
func (c *w7QueueProbeChannel) IsRunning() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.run
}
func (c *w7QueueProbeChannel) IsAllowed(string) bool               { return true }
func (c *w7QueueProbeChannel) IsAllowedSender(bus.SenderInfo) bool { return true }
func (c *w7QueueProbeChannel) ReasoningChannelID() string          { return "" }

func (c *w7QueueProbeChannel) setRun(v bool) {
	c.mu.Lock()
	c.run = v
	c.mu.Unlock()
}

func (c *w7QueueProbeChannel) armGate() {
	c.mu.Lock()
	c.gate = make(chan struct{})
	c.mu.Unlock()
}

func (c *w7QueueProbeChannel) openGate() {
	c.mu.Lock()
	g := c.gate
	c.gate = nil
	c.mu.Unlock()
	if g != nil {
		close(g)
	}
}

func (c *w7QueueProbeChannel) Send(_ context.Context, msg bus.OutboundMessage) error {
	c.mu.Lock()
	if msg.Operation == bus.OperationStart {
		c.dels = append(c.dels, fmt.Sprintf("%s@%d", msg.Content, time.Now().UnixNano()))
	}
	g := c.gate
	c.mu.Unlock()
	if g != nil {
		<-g // park this one handoff so a second start can only sit queued
	}
	return nil
}

func (c *w7QueueProbeChannel) startCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.dels)
}

func (c *w7QueueProbeChannel) startCountOf(content string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, d := range c.dels {
		if strings.HasPrefix(d, content+"@") {
			n++
		}
	}
	return n
}

func (c *w7QueueProbeChannel) dumpDeliveries(t *testing.T) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	t.Logf("probe handoffs: %v", c.dels)
}

// TestQueuedStart_SurvivesTheNotRunningWindowAndDeliversOnce pins founder Q2
// A. Start 1 parks inside Send so the worker is busy; start 2 is published
// while the channel still runs and can therefore only sit in the worker's
// queue; the running flag then drops (the window), start 1 returns, and the
// worker dequeues start 2 into the window. Today the dequeue check refuses
// it (manager_outbound.go::startMayProceed → StartAllowed row f) and drops
// it with one info log: the RED. The founder pins that it is kept and
// delivered once the channel is running again, exactly once.
func TestQueuedStart_SurvivesTheNotRunningWindowAndDeliversOnce(t *testing.T) {
	probe := &w7QueueProbeChannel{name: "w7-queue-tg"}
	RegisterFactory("telegram", func(_ *config.Config, _ string, _ credentials.SecretBundle, _ *bus.MessageBus) (Channel, error) {
		return probe, nil
	})
	t.Cleanup(func() {
		factoriesMu.Lock()
		delete(factories, "telegram")
		factoriesMu.Unlock()
	})

	const (
		instance  = "w7-queue-tg"
		workspace = "ws-w7"
		agent     = "agent-w7"
		target    = "15550001111"
	)
	cfg := minimalCfg(map[string]config.ChannelInstanceConfig{
		instance: {
			Type:        "telegram",
			Enabled:     true,
			WorkspaceID: workspace,
			Identity:    &config.ChannelIdentity{Kind: config.ChannelIdentityKindAgent, ID: agent},
			Outbound:    &config.ChannelOutboundConfig{Policy: string(StartPolicyAgentMayStart), Targets: []string{target}},
			// The telegram prerequisite check (prerequisites.go::prereqTelegram)
			// demands a non-empty TokenRef and otherwise silently skips the
			// instance in initChannels' ladder; the probe factory ignores it.
			TokenRef: "w7-fake-token-ref",
		},
	})
	mb := bus.NewMessageBus()
	t.Cleanup(mb.Close)
	m, err := NewManager(cfg, credentials.SecretBundle{}, mb, nil)
	require.NoError(t, err)
	m.SetPolicySource(func() *config.Config { return cfg })
	startCtx, cancelStart := context.WithCancel(context.Background())
	defer cancelStart()
	require.NoError(t, m.StartAll(startCtx))
	t.Cleanup(func() { _ = m.StopAll(context.Background()) })
	require.True(t, probe.IsRunning(), "instrument: the activation ladder started the probe channel")

	publish := func(content string) {
		require.NoError(t, mb.PublishOutbound(context.Background(), bus.OutboundMessage{
			Channel:     instance,
			ChatID:      target,
			Content:     content,
			WorkspaceID: workspace,
			AgentID:     agent,
			Operation:   bus.OperationStart,
		}))
	}

	probe.armGate()
	publish("w7-queued-1")
	// 10s bound: the dispatch → enqueue → dequeue → Send handoff is local
	// processing; the bound is generous, the verdict is the handoff itself.
	require.Eventually(t, func() bool { return probe.startCount() == 1 },
		10*time.Second, 10*time.Millisecond,
		"instrument: start 1 reached the channel and the worker is parked inside its send")
	publish("w7-queued-2")
	// Short bounded grace so start 2 is inside the worker queue before the
	// window opens; the parked send guarantees it cannot be dequeued early.
	time.Sleep(100 * time.Millisecond)

	// The not-running window — the observable state the WhatsApp managed
	// reconnect failure produces (registered, started once, IsRunning false).
	probe.setRun(false)
	probe.openGate()
	// The window must be OBSERVABLE to the dequeue check: opening the gate
	// lets the parked worker return from start 1, dequeue start 2 and run the
	// dequeue start check while the channel still reports not running. This
	// bounded wait holds the window open across that local handoff (well
	// under a second; 2s is generous) BEFORE the connection comes back, so
	// the founder's kept-and-delivered pin is exercised against a real
	// window, not one that closed first.
	time.Sleep(2 * time.Second)
	probe.setRun(true)

	// RED on today's code: start 2 was refused at the dequeue check and
	// dropped; the count stays at 1 and this bound expires.
	require.Eventually(t, func() bool {
		return probe.startCountOf("w7-queued-2") >= 1
	},
		10*time.Second, 10*time.Millisecond,
		"founder Q2 A: a queued start is not dropped during the not-running window — it is kept and delivered once the channel is running again")
	// Quiet window (2s, well beyond the local handoff path) then exactly once:
	// the queued start is delivered a single time, and start 1 was not
	// duplicated either.
	time.Sleep(2 * time.Second)
	probe.dumpDeliveries(t)
	require.Equal(t, 1, probe.startCountOf("w7-queued-1"),
		"instrument: start 1 was handed to the channel exactly once")
	require.Equal(t, 1, probe.startCountOf("w7-queued-2"),
		"founder Q2 A: the queued start is delivered exactly once, never duplicated or re-delivered")
}

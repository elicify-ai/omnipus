package channels

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
)

// failOnceChannel fails its first Start (a transient platform outage) and
// starts on every later attempt. It records what the manager delivers.
type failOnceChannel struct {
	name   string
	mu     sync.Mutex
	starts int
	run    bool
	texts  []string
	media  []string
}

func (c *failOnceChannel) Name() string { return c.name }
func (c *failOnceChannel) Start(context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.starts++
	if c.starts == 1 {
		return errors.New("platform temporarily unreachable")
	}
	c.run = true
	return nil
}
func (c *failOnceChannel) Stop(context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.run = false
	return nil
}
func (c *failOnceChannel) Send(_ context.Context, msg bus.OutboundMessage) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.texts = append(c.texts, msg.Content)
	return nil
}
func (c *failOnceChannel) SendMedia(_ context.Context, msg bus.OutboundMediaMessage) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.media = append(c.media, msg.ChatID)
	return nil
}
func (c *failOnceChannel) IsRunning() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.run
}
func (c *failOnceChannel) IsAllowed(string) bool               { return true }
func (c *failOnceChannel) IsAllowedSender(bus.SenderInfo) bool { return true }
func (c *failOnceChannel) ReasoningChannelID() string          { return "" }

func (c *failOnceChannel) delivered() (texts, media []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.texts...), append([]string(nil), c.media...)
}

// registerFailOnceFactory installs a factory for typeName that counts how
// many channel instances the manager constructs and hands back the latest.
func registerFailOnceFactory(t *testing.T, typeName string) (constructed func() []*failOnceChannel) {
	t.Helper()
	var mu sync.Mutex
	var built []*failOnceChannel
	RegisterFactory(typeName, func(_ *config.Config, instanceID string, _ credentials.SecretBundle, _ *bus.MessageBus) (Channel, error) {
		mu.Lock()
		defer mu.Unlock()
		ch := &failOnceChannel{name: instanceID}
		built = append(built, ch)
		return ch, nil
	})
	t.Cleanup(func() {
		factoriesMu.Lock()
		delete(factories, typeName)
		factoriesMu.Unlock()
	})
	return func() []*failOnceChannel {
		mu.Lock()
		defer mu.Unlock()
		return append([]*failOnceChannel(nil), built...)
	}
}

func requireDelivered(t *testing.T, mb *bus.MessageBus, ch *failOnceChannel, name string) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, mb.PublishOutbound(ctx, bus.OutboundMessage{Channel: name, ChatID: "chat-1", Content: "the reply"}))
	require.NoError(t, mb.PublishOutboundMedia(ctx, bus.OutboundMediaMessage{Channel: name, ChatID: "chat-1"}))
	require.Eventually(t, func() bool {
		texts, media := ch.delivered()
		return len(texts) == 1 && len(media) == 1
	}, 10*time.Second, 20*time.Millisecond, "the explicitly started channel must receive the text and media reply")
	texts, _ := ch.delivered()
	require.Equal(t, []string{"the reply"}, texts)
}

// TestManager_StartChannel_AfterFailedBootStart_DeliversReplies is Train 2
// gate r2 CR-T2-R2-01 / R2-A01 / T2-SF-09: a channel whose first boot Start
// failed stays registered with no reply worker; an unchanged approved
// reconnect reloads nothing and then starts it through StartChannel. The
// explicit start must complete the manager lifecycle, so a real outbound
// text and media reply reach the channel (not only IsRunning).
func TestManager_StartChannel_AfterFailedBootStart_DeliversReplies(t *testing.T) {
	built := registerFailOnceFactory(t, "gate-r2-failboot")
	RegisterFactory("gate-r2-healthy", stubFactory(nil))
	t.Cleanup(func() {
		factoriesMu.Lock()
		delete(factories, "gate-r2-healthy")
		factoriesMu.Unlock()
	})
	cfg := minimalCfg(map[string]config.ChannelInstanceConfig{
		"gate-r2-failboot": {Type: "gate-r2-failboot", Enabled: true},
		"gate-r2-healthy":  {Type: "gate-r2-healthy", Enabled: true},
	})
	mb := bus.NewMessageBus()
	t.Cleanup(mb.Close)
	m, err := NewManager(cfg, credentials.SecretBundle{}, mb, nil)
	require.NoError(t, err)
	require.NoError(t, m.StartAll(context.Background()), "the healthy channel keeps the gateway up")
	t.Cleanup(func() { _ = m.StopAll(context.Background()) })
	require.Len(t, built(), 1)
	flaky := built()[0]
	require.False(t, flaky.IsRunning(), "the first boot start failed")

	require.NoError(t, m.Reload(context.Background(), cfg, credentials.SecretBundle{}), "an unchanged approved reconnect reloads nothing")
	started, err := m.StartChannel("gate-r2-failboot")
	require.NoError(t, err)
	require.True(t, started)

	requireDelivered(t, mb, flaky, "gate-r2-failboot")

	started, err = m.StartChannel("gate-r2-failboot")
	require.NoError(t, err)
	require.False(t, started, "a running channel is left alone (no duplicate worker)")
	require.Len(t, built(), 1)
}

// TestManager_StartChannel_AfterFailedReloadStart_IsNotRebuiltByTheNextReload
// covers the reload variant: a newly added instance whose first Start failed
// has no committed config hash. Once StartChannel succeeds, the manager must
// record it as successfully started, so the next unchanged reload does not
// construct and start a second instance next to the running one.
func TestManager_StartChannel_AfterFailedReloadStart_IsNotRebuiltByTheNextReload(t *testing.T) {
	built := registerFailOnceFactory(t, "gate-r2-failreload")
	RegisterFactory("gate-r2-healthy2", stubFactory(nil))
	t.Cleanup(func() {
		factoriesMu.Lock()
		delete(factories, "gate-r2-healthy2")
		factoriesMu.Unlock()
	})
	base := minimalCfg(map[string]config.ChannelInstanceConfig{
		"gate-r2-healthy2": {Type: "gate-r2-healthy2", Enabled: true},
	})
	mb := bus.NewMessageBus()
	t.Cleanup(mb.Close)
	m, err := NewManager(base, credentials.SecretBundle{}, mb, nil)
	require.NoError(t, err)
	require.NoError(t, m.StartAll(context.Background()))
	t.Cleanup(func() { _ = m.StopAll(context.Background()) })

	withFlaky := minimalCfg(map[string]config.ChannelInstanceConfig{
		"gate-r2-healthy2":   {Type: "gate-r2-healthy2", Enabled: true},
		"gate-r2-failreload": {Type: "gate-r2-failreload", Enabled: true},
	})
	require.NoError(t, m.Reload(context.Background(), withFlaky, credentials.SecretBundle{}))
	require.Len(t, built(), 1)
	started, err := m.StartChannel("gate-r2-failreload")
	require.NoError(t, err)
	require.True(t, started)

	require.NoError(t, m.Reload(context.Background(), withFlaky, credentials.SecretBundle{}))
	require.Len(t, built(), 1, "the next unchanged reload must not construct a second instance")
	requireDelivered(t, mb, built()[0], "gate-r2-failreload")
}

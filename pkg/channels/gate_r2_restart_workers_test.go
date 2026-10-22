package channels

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
)

// failOnceChannel records what the manager delivers and how it was started.
// startFailures is shared by every instance its factory builds, so "the
// first start fails" holds across the fresh instance a restart constructs.
type failOnceChannel struct {
	name          string
	startFailures *atomic.Int32
	mu            sync.Mutex
	run           bool
	texts         []string
	media         []string
	starts        []startIntent
}

type startIntent struct {
	token string
	boot  bool
}

func (c *failOnceChannel) Name() string { return c.name }
func (c *failOnceChannel) Start(ctx context.Context) error {
	token, boot := PairingStartFrom(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.starts = append(c.starts, startIntent{token: token, boot: boot})
	if c.startFailures != nil && c.startFailures.Add(-1) >= 0 {
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

func (c *failOnceChannel) startIntents() []startIntent {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]startIntent(nil), c.starts...)
}

// registerFailOnceFactory installs a factory for typeName whose instances
// together fail their first `failures` starts, and returns every built instance.
func registerFailOnceFactory(t *testing.T, typeName string, failures int32) (constructed func() []*failOnceChannel) {
	t.Helper()
	var mu sync.Mutex
	var built []*failOnceChannel
	shared := &atomic.Int32{}
	shared.Store(failures)
	RegisterFactory(typeName, func(_ *config.Config, instanceID string, _ credentials.SecretBundle, _ *bus.MessageBus) (Channel, error) {
		mu.Lock()
		defer mu.Unlock()
		ch := &failOnceChannel{name: instanceID, startFailures: shared}
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

func registerHealthyFactory(t *testing.T, typeName string) {
	t.Helper()
	RegisterFactory(typeName, stubFactory(nil))
	t.Cleanup(func() {
		factoriesMu.Lock()
		delete(factories, typeName)
		factoriesMu.Unlock()
	})
}

func requireDelivered(t *testing.T, mb *bus.MessageBus, ch *failOnceChannel, name string) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, mb.PublishOutbound(ctx, bus.OutboundMessage{Channel: name, ChatID: "chat-1", Content: "the reply"}))
	require.NoError(t, mb.PublishOutboundMedia(ctx, bus.OutboundMediaMessage{Channel: name, ChatID: "chat-1"}))
	require.Eventually(t, func() bool {
		texts, media := ch.delivered()
		return len(texts) == 1 && len(media) == 1
	}, 10*time.Second, 20*time.Millisecond, "the restarted channel must receive the text and media reply")
	texts, _ := ch.delivered()
	require.Equal(t, []string{"the reply"}, texts)
}

// TestManager_RequestRestart_AfterFailedBootStart_DeliversReplies is Train 2
// gate r2 CR-T2-R2-01 / R2-A01 / T2-SF-09, now through the gate r3 M10 path:
// a channel whose boot Start failed stays registered with no reply worker; an
// approved explicit restart is serviced by the serialized reload lifecycle,
// which builds a fresh instance with its text and media workers, so a real
// outbound reply reaches it.
func TestManager_RequestRestart_AfterFailedBootStart_DeliversReplies(t *testing.T) {
	built := registerFailOnceFactory(t, "gate-r3-failboot", 1)
	registerHealthyFactory(t, "gate-r3-healthy")
	cfg := minimalCfg(map[string]config.ChannelInstanceConfig{
		"gate-r3-failboot": {Type: "gate-r3-failboot", Enabled: true},
		"gate-r3-healthy":  {Type: "gate-r3-healthy", Enabled: true},
	})
	mb := bus.NewMessageBus()
	t.Cleanup(mb.Close)
	m, err := NewManager(cfg, credentials.SecretBundle{}, mb, nil)
	require.NoError(t, err)
	require.NoError(t, m.StartAll(context.Background()), "the healthy channel keeps the gateway up")
	t.Cleanup(func() { _ = m.StopAll(context.Background()) })
	require.False(t, built()[0].IsRunning(), "the first boot start failed")

	m.RequestRestart("gate-r3-failboot", "")
	require.NoError(t, m.Reload(context.Background(), cfg, credentials.SecretBundle{}), "an unchanged configuration still services the explicit restart")
	require.Len(t, built(), 2, "the restart builds a fresh instance")
	fresh := built()[1]
	require.True(t, fresh.IsRunning())
	requireDelivered(t, mb, fresh, "gate-r3-failboot")

	require.NoError(t, m.Reload(context.Background(), cfg, credentials.SecretBundle{}))
	require.Len(t, built(), 2, "a restart is serviced once; the next unchanged reload builds nothing")
}

// TestManager_RequestRestart_AfterFailedReloadStart_IsNotRebuiltByTheNextReload:
// a newly added instance whose first Start failed is restarted explicitly; once
// it runs, the next unchanged reload does not construct a second instance.
func TestManager_RequestRestart_AfterFailedReloadStart_IsNotRebuiltByTheNextReload(t *testing.T) {
	built := registerFailOnceFactory(t, "gate-r3-failreload", 1)
	registerHealthyFactory(t, "gate-r3-healthy2")
	base := minimalCfg(map[string]config.ChannelInstanceConfig{
		"gate-r3-healthy2": {Type: "gate-r3-healthy2", Enabled: true},
	})
	mb := bus.NewMessageBus()
	t.Cleanup(mb.Close)
	m, err := NewManager(base, credentials.SecretBundle{}, mb, nil)
	require.NoError(t, err)
	require.NoError(t, m.StartAll(context.Background()))
	t.Cleanup(func() { _ = m.StopAll(context.Background()) })

	withFlaky := minimalCfg(map[string]config.ChannelInstanceConfig{
		"gate-r3-healthy2":   {Type: "gate-r3-healthy2", Enabled: true},
		"gate-r3-failreload": {Type: "gate-r3-failreload", Enabled: true},
	})
	require.NoError(t, m.Reload(context.Background(), withFlaky, credentials.SecretBundle{}))
	require.Len(t, built(), 1)
	require.False(t, built()[0].IsRunning())
	m.RequestRestart("gate-r3-failreload", "")
	require.NoError(t, m.Reload(context.Background(), withFlaky, credentials.SecretBundle{}))
	built2 := built()
	require.True(t, built2[len(built2)-1].IsRunning())
	count := len(built2)

	require.NoError(t, m.Reload(context.Background(), withFlaky, credentials.SecretBundle{}))
	require.Len(t, built(), count, "the next unchanged reload must not construct another instance")
	requireDelivered(t, mb, built()[count-1], "gate-r3-failreload")
}

// TestManager_RequestRestart_RestartsAStoppedChannel keeps gate r1 architect
// F04's intent: a channel stopped in place (its pairing was stopped) has an
// unchanged configuration, so only an explicit restart starts it again; an
// unknown name is ignored.
func TestManager_RequestRestart_RestartsAStoppedChannel(t *testing.T) {
	built := registerFailOnceFactory(t, "gate-r3-stopped", 0)
	cfg := minimalCfg(map[string]config.ChannelInstanceConfig{
		"gate-r3-stopped": {Type: "gate-r3-stopped", Enabled: true},
	})
	mb := bus.NewMessageBus()
	t.Cleanup(mb.Close)
	m, err := NewManager(cfg, credentials.SecretBundle{}, mb, nil)
	require.NoError(t, err)
	require.NoError(t, m.StartAll(context.Background()))
	t.Cleanup(func() { _ = m.StopAll(context.Background()) })
	require.NoError(t, built()[0].Stop(context.Background()))

	require.NoError(t, m.Reload(context.Background(), cfg, credentials.SecretBundle{}))
	require.Len(t, built(), 1, "control: an unchanged reload alone does not restart it")
	m.RequestRestart("gate-r3-stopped", "")
	m.RequestRestart("no-such-channel", "")
	require.NoError(t, m.Reload(context.Background(), cfg, credentials.SecretBundle{}))
	require.Len(t, built(), 2)
	require.True(t, built()[1].IsRunning())
}

// TestManager_ReloadHandoff_RestartAndRegisterDuringHandoffComplete is gate r3
// CR-T2-R3-01 (M10): while a reload waits for the old dispatchers with the
// manager lock released, an explicit restart is requested and a channel is
// registered on the new dispatch context. The reload must still finish (it
// joins only the generation it cancelled), the restart is serviced by the
// next reload, and both channels deliver text and media.
func TestManager_ReloadHandoff_RestartAndRegisterDuringHandoffComplete(t *testing.T) {
	built := registerFailOnceFactory(t, "gate-r3-handoff", 1)
	registerHealthyFactory(t, "gate-r3-healthy3")
	cfg := minimalCfg(map[string]config.ChannelInstanceConfig{
		"gate-r3-handoff":  {Type: "gate-r3-handoff", Enabled: true},
		"gate-r3-healthy3": {Type: "gate-r3-healthy3", Enabled: true},
	})
	mb := bus.NewMessageBus()
	t.Cleanup(mb.Close)
	m, err := NewManager(cfg, credentials.SecretBundle{}, mb, nil)
	require.NoError(t, err)
	require.NoError(t, m.StartAll(context.Background()))
	t.Cleanup(func() { _ = m.StopAll(context.Background()) })

	registered := &failOnceChannel{name: "gate-r3-registered"}
	var once sync.Once
	m.mu.Lock()
	m.reloadHandoffHook = func() {
		once.Do(func() {
			m.RequestRestart("gate-r3-handoff", "")
			m.RegisterChannel("gate-r3-registered", registered)
		})
	}
	m.mu.Unlock()
	done := make(chan error, 1)
	go func() { done <- m.Reload(context.Background(), cfg, credentials.SecretBundle{}) }()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(30 * time.Second):
		t.Fatal("the reload deadlocked on a worker started during its own handoff")
	}
	m.mu.Lock()
	m.reloadHandoffHook = nil
	m.mu.Unlock()
	require.NoError(t, m.Reload(context.Background(), cfg, credentials.SecretBundle{}), "the next reload services the requested restart")
	all := built()
	fresh := all[len(all)-1]
	require.True(t, fresh.IsRunning(), "the restart requested during the handoff was serviced")
	requireDelivered(t, mb, fresh, "gate-r3-handoff")
	requireDelivered(t, mb, registered, "gate-r3-registered")
}

// TestManager_Starts_CarryTheirPairingIntent pins how the manager hands pairing
// intent to a start (gate r3 M2, M14, founder restart ruling): the gateway's
// boot start says boot; a reload start consumes only a ready admission bound to
// the instance's current revision; a stale-revision admission is interrupted
// and the start carries no token.
func TestManager_Starts_CarryTheirPairingIntent(t *testing.T) {
	built := registerFailOnceFactory(t, "gate-r3-intent", 0)
	cfg := minimalCfg(map[string]config.ChannelInstanceConfig{
		"gate-r3-intent": {Type: "gate-r3-intent", Enabled: true},
	})
	mb := bus.NewMessageBus()
	t.Cleanup(mb.Close)
	m, err := NewManager(cfg, credentials.SecretBundle{}, mb, nil)
	require.NoError(t, err)
	require.NoError(t, m.StartAll(context.Background()))
	t.Cleanup(func() { _ = m.StopAll(context.Background()) })
	require.Equal(t, []startIntent{{boot: true}}, built()[0].startIntents(), "the boot start says boot and carries no token")

	store := m.PairingAttempts()
	owner := &PairingOwner{SessionID: "chat-a", RequestID: "req-a"}
	admitted, err := store.Admit("gate-r3-intent", PairingAdmissionConnect, owner)
	require.NoError(t, err)
	require.True(t, store.MarkReady("gate-r3-intent", admitted.AttemptID, ChannelRevision(cfg, "gate-r3-intent")))
	// Fixture: the accepted ready write and its reload share one private identity.
	finalManagerAcceptedSnapshot(t, store, cfg, "gate-r3-intent", admitted.AttemptID)
	m.RequestRestart("gate-r3-intent", admitted.AttemptID)
	require.NoError(t, m.Reload(context.Background(), cfg, credentials.SecretBundle{}))
	all := built()
	require.Equal(t, []startIntent{{token: admitted.AttemptID}}, all[len(all)-1].startIntents(), "the restart consumes exactly its admitted token")

	consumed, ok := store.Attempt("gate-r3-intent", admitted.AttemptID)
	require.True(t, ok)
	require.Equal(t, PairingOutcomeNoPairing, consumed.Outcome, "a start that activated no pairing closes its admission")

	// Fixture: materialize a legitimate idle predecessor before the uncommitted
	// stale admission; rejecting that old snapshot must not reuse a boot trace.
	m.RequestRestart("gate-r3-intent", "")
	require.NoError(t, m.Reload(context.Background(), cfg, credentials.SecretBundle{}))
	idleCount := len(built())

	stale, err := store.Admit("gate-r3-intent", PairingAdmissionEnable, nil)
	require.NoError(t, err)
	require.True(t, store.MarkReady("gate-r3-intent", stale.AttemptID, "an-older-revision"))
	m.RequestRestart("gate-r3-intent", stale.AttemptID)
	require.NoError(t, m.Reload(context.Background(), cfg, credentials.SecretBundle{}))
	all = built()
	require.Len(t, all, idleCount, "the uncommitted stale snapshot authorizes no new start")
	// Gate r4 R4-T1 (corrected oracle): the channel still starts — a linked
	// device must connect — but with no pairing token, and a start without a
	// token can never begin a pairing loop: the store refuses it, so no
	// fallback unowned attempt exists after the stale admission.
	require.Equal(t, []startIntent{{}}, all[len(all)-1].startIntents(), "the start carries no token")
	// Gate r5 R5-A05: the reload does not interrupt it either — a reload
	// never decides on behalf of an attempt it was not built for; a stale
	// admission is closed by its own operation or the next admission.
	require.True(t, store.IsReady("gate-r3-intent", stale.AttemptID), "not consumed, not interrupted")
	latest, ok := store.Latest("gate-r3-intent")
	require.True(t, ok)
	require.Equal(t, stale.AttemptID, latest.AttemptID, "no fallback attempt was minted")
	_, activateErr := store.Activate("gate-r3-intent", "", func(string) func(context.Context) error { return nil })
	require.ErrorIs(t, activateErr, ErrPairingConflict, "a start without its own token is refused")
}

// TestManager_RevokedAdmissionNeverRestarts is gate r4 R4-A01: a restart
// queued for an admitted attempt that a Stop revoked before the reload ran
// is dropped — no new instance, no start, no fallback pairing.
func TestManager_RevokedAdmissionNeverRestarts(t *testing.T) {
	built := registerFailOnceFactory(t, "gate-r4-revoked", 0)
	cfg := minimalCfg(map[string]config.ChannelInstanceConfig{
		"gate-r4-revoked": {Type: "gate-r4-revoked", Enabled: true},
	})
	mb := bus.NewMessageBus()
	t.Cleanup(mb.Close)
	m, err := NewManager(cfg, credentials.SecretBundle{}, mb, nil)
	require.NoError(t, err)
	require.NoError(t, m.StartAll(context.Background()))
	t.Cleanup(func() { _ = m.StopAll(context.Background()) })
	store := m.PairingAttempts()
	owner := &PairingOwner{SessionID: "chat-a", RequestID: "req-a"}
	_, err = store.Admit("gate-r4-revoked", PairingAdmissionRetry, owner)
	require.ErrorIs(t, err, ErrPairingConflict, "instrument: nothing to retry yet")
	admitted, err := store.Admit("gate-r4-revoked", PairingAdmissionConnect, owner)
	require.NoError(t, err)
	require.True(t, store.MarkReady("gate-r4-revoked", admitted.AttemptID, ChannelRevision(cfg, "gate-r4-revoked")))
	m.RequestRestart("gate-r4-revoked", admitted.AttemptID)
	_, revoked := store.BeginStop(admitted)
	require.True(t, revoked, "the chat's Stop revokes the ready admission")

	before := len(built())
	require.NoError(t, m.Reload(context.Background(), cfg, credentials.SecretBundle{}))
	require.Len(t, built(), before, "the revoked admission's restart was dropped: no new instance")
	require.Len(t, built()[0].startIntents(), 1, "and nothing was started again")
	latest, ok := store.Latest("gate-r4-revoked")
	require.True(t, ok)
	require.Equal(t, admitted.AttemptID, latest.AttemptID)
	require.Equal(t, PairingOutcomeStopped, latest.Outcome)

	// Control: the same restart for a still-ready admission does run.
	next, err := store.Admit("gate-r4-revoked", PairingAdmissionRetry, owner)
	require.NoError(t, err, "the stopped chat may deliberately Retry")
	require.True(t, store.MarkReady("gate-r4-revoked", next.AttemptID, ChannelRevision(cfg, "gate-r4-revoked")))
	m.RequestRestart("gate-r4-revoked", next.AttemptID)
	require.NoError(t, m.Reload(context.Background(), cfg, credentials.SecretBundle{}))
	all := built()
	require.Len(t, all, before+1, "a ready admission's restart builds a fresh instance")
	require.Equal(t, []startIntent{{token: next.AttemptID}}, all[len(all)-1].startIntents())
}

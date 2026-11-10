package channels

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
)

const r9CredentialID = "gate-r9-credential-retry"
const r9CredentialRef = "R9_SYNTHETIC_TOKEN"

// Only the process-edge adapter is controlled. Manager selection, credential
// availability, retained ownership, worker joins and dispatch remain real.
type r9CredentialConnection struct {
	*failOnceChannel
	stopErrors        []error
	stopCalls         int
	credentialPresent bool
	sent              chan bus.OutboundMessage // one positive delivery proof per case
}

func (c *r9CredentialConnection) Stop(context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopCalls++
	if c.stopCalls <= len(c.stopErrors) {
		return c.stopErrors[c.stopCalls-1]
	}
	c.run = false
	return nil
}
func (c *r9CredentialConnection) Send(ctx context.Context, msg bus.OutboundMessage) error {
	if err := c.failOnceChannel.Send(ctx, msg); err != nil {
		return err
	}
	select {
	case c.sent <- msg:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (c *r9CredentialConnection) stops() int { c.mu.Lock(); defer c.mu.Unlock(); return c.stopCalls }

type r9CredentialFixture struct {
	m                     *Manager
	mb                    *bus.MessageBus
	cfg                   *config.Config
	present, absent       credentials.SecretBundle
	mu                    sync.Mutex
	built                 []*r9CredentialConnection
	hash                  string
	generation            uint64
	public                []byte
	oldDone, oldMediaDone <-chan struct{}
}

func (f *r9CredentialFixture) resources() []*r9CredentialConnection {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*r9CredentialConnection(nil), f.built...)
}

func newR9CredentialFixture(t *testing.T, stopErrors []error) *r9CredentialFixture {
	t.Helper()
	f := &r9CredentialFixture{mb: bus.NewMessageBus(), present: credentials.SecretBundle{credentials.SecretRef(r9CredentialRef): "synthetic-test-value"}, absent: credentials.SecretBundle{}}
	t.Cleanup(f.mb.Close)
	t.Cleanup(SwapFactoryForTesting(r9CredentialID, func(_ *config.Config, id string, secrets credentials.SecretBundle, _ *bus.MessageBus) (Channel, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		c := &r9CredentialConnection{failOnceChannel: &failOnceChannel{name: id}, credentialPresent: secrets.GetString(r9CredentialRef) != "", sent: make(chan bus.OutboundMessage, 1)}
		if len(f.built) == 0 {
			c.stopErrors = append([]error(nil), stopErrors...)
		}
		f.built = append(f.built, c)
		return c, nil
	}))
	before := minimalCfg(map[string]config.ChannelInstanceConfig{r9CredentialID: {Type: r9CredentialID, Enabled: false, TokenRef: r9CredentialRef}})
	var err error
	f.m, err = NewManager(before, f.present, f.mb, nil)
	require.NoError(t, err)
	t.Cleanup(func() {
		// Test fault inputs are disabled ONLY in teardown, after every protocol
		// assertion, so an earlier fatal assertion cannot leave workers behind.
		for _, c := range f.resources() {
			c.mu.Lock()
			c.stopErrors = nil
			c.mu.Unlock()
		}
		require.NoError(t, f.m.StopAll(context.Background()))
	})
	require.NoError(t, f.m.StartAll(context.Background()))
	f.cfg, err = before.Clone()
	require.NoError(t, err)
	inst := f.cfg.Channels[r9CredentialID]
	inst.Enabled = true
	f.cfg.Channels[r9CredentialID] = inst
	order := f.m.PairingAttempts().LockChannel(r9CredentialID)
	commit, err := PreparePairingConfig(before, f.cfg, []*PairingOrder{order}, nil)
	require.NoError(t, err)
	commit.Commit(true)
	order.Unlock()
	require.NoError(t, f.m.Reload(context.Background(), f.cfg, f.present), "establish an enabled registered resource with an accepted nonzero generation")
	all := f.resources()
	require.Len(t, all, 1)
	current, registered := f.m.GetChannel(r9CredentialID)
	require.True(t, registered)
	require.Same(t, all[0], current)
	require.True(t, current.IsRunning())
	require.True(t, all[0].credentialPresent)
	f.generation, _ = f.cfg.ChannelOrderGeneration(r9CredentialID)
	require.NotZero(t, f.generation)
	f.hash = toChannelHashes(f.cfg)[r9CredentialID]
	require.NotEmpty(t, f.hash)
	f.public, err = json.Marshal(f.cfg.Channels)
	require.NoError(t, err)
	f.m.mu.RLock()
	w := f.m.workers[r9CredentialID]
	f.m.mu.RUnlock()
	require.NotNil(t, w)
	f.oldDone, f.oldMediaDone = w.done, w.mediaDone
	require.NotEqual(t, credentialSignatures(f.cfg, f.present), credentialSignatures(f.cfg, f.absent), "instrument: only configured credential availability differs")
	return f
}

func (f *r9CredentialFixture) fixedSettings(t *testing.T) {
	t.Helper()
	public, err := json.Marshal(f.cfg.Channels)
	require.NoError(t, err)
	require.Equal(t, f.public, public, "settings bytes never change during credential-only reloads")
	require.Equal(t, f.hash, toChannelHashes(f.cfg)[r9CredentialID])
	gen, _ := f.cfg.ChannelOrderGeneration(r9CredentialID)
	require.Equal(t, f.generation, gen)
	f.m.mu.RLock()
	hash := f.m.channelHashes[r9CredentialID]
	origin := f.m.resourceConfigOrders[r9CredentialID]
	forced := len(f.m.forcedRestarts)
	f.m.mu.RUnlock()
	assert.Equal(t, f.hash, hash)
	assert.Equal(t, f.generation, origin)
	assert.Zero(t, forced, "no forced restart or generation change can hide a lost credential-only retry trigger")
}
func r9Join(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("HARNESS: actual worker did not join its cancelled context")
	}
}
func r9Closed(done <-chan struct{}) bool {
	select {
	case <-done:
		return true
	default:
		return false
	}
}
func (f *r9CredentialFixture) noWorkerBeforeDisposal(t *testing.T) {
	t.Helper()
	f.m.mu.RLock()
	w := f.m.workers[r9CredentialID]
	var done, media <-chan struct{}
	if w != nil {
		done, media = w.done, w.mediaDone
	}
	f.m.mu.RUnlock()
	if w == nil {
		return
	} // removing the drained worker is also safe
	assert.True(t, r9Closed(done), "retained old text worker must remain joined until exact disposal succeeds")
	assert.True(t, r9Closed(media), "retained old media worker must remain joined until exact disposal succeeds")
}
func (f *r9CredentialFixture) deliver(t *testing.T, c *r9CredentialConnection) {
	t.Helper()
	msg := bus.OutboundMessage{Channel: r9CredentialID, ChatID: "r9-chat", Content: "delivery after confirmed cleanup"}
	require.NoError(t, f.mb.PublishOutbound(context.Background(), msg))
	select {
	case got := <-c.sent:
		require.Equal(t, msg, got)
	case <-time.After(5 * time.Second):
		t.Fatal("HARNESS: real current worker did not acknowledge its positive delivery")
	}
}

func r9CredentialFailureRetries(t *testing.T) {
	first, second := errors.New("credential-only cleanup first failure"), errors.New("credential-only cleanup second failure")
	f := newR9CredentialFixture(t, []error{first, second})
	old := f.resources()[0]
	f.fixedSettings(t)
	err := f.m.Reload(context.Background(), f.cfg, f.absent)
	require.ErrorIs(t, err, first)
	require.Equal(t, 1, old.stops())
	r9Join(t, f.oldDone)
	r9Join(t, f.oldMediaDone)
	f.noWorkerBeforeDisposal(t)
	current, registered := f.m.GetChannel(r9CredentialID)
	require.True(t, registered)
	require.Same(t, old, current)
	require.Len(t, f.resources(), 1, "first failed cleanup must not construct a replacement")
	f.fixedSettings(t)

	err = f.m.Reload(context.Background(), f.cfg, f.absent) // exactly identical settings, generation and credential bundle
	assert.ErrorIs(t, err, second, "same-input reload must retry and surface the exact retained Stop failure")
	assert.Equal(t, 2, old.stops(), "the later reload must actually invoke this captured connection's Stop again")
	current, registered = f.m.GetChannel(r9CredentialID)
	assert.True(t, registered)
	assert.Same(t, old, current)
	assert.Len(t, f.resources(), 1, "no replacement construction while exact disposal is unfinished")
	assert.Len(t, old.startIntents(), 1, "no connection restart while retained cleanup is unfinished")
	f.noWorkerBeforeDisposal(t)
	f.fixedSettings(t)

	require.NoError(t, f.m.Reload(context.Background(), f.cfg, f.absent), "third captured Stop succeeds")
	assert.Equal(t, 3, old.stops())
	all := f.resources()
	assert.Len(t, all, 2, "actual successful disposal installs one replacement")
	current, registered = f.m.GetChannel(r9CredentialID)
	assert.True(t, registered)
	assert.True(t, current != old, "old retained connection cannot stand in for the requested replacement")
	assert.False(t, old.IsRunning())
	if len(all) == 2 {
		replacement := all[1]
		require.Same(t, replacement, current)
		require.True(t, replacement.IsRunning())
		require.False(t, replacement.credentialPresent, "the replacement constructor receives the current credential bundle")
		require.Len(t, replacement.startIntents(), 1)
		f.deliver(t, replacement) // actual worker success, not only registration
	}
	f.fixedSettings(t)
}

// CR-T2-R9-01: failed disposal must retain a credential-only restart reason,
// not just a hash whose unchanged value no longer selects removal next time.
func TestManager_CredentialOnlyRestartRetainsCleanupRetry(t *testing.T) {
	t.Run("failed credential-only cleanup retries exact resource until success", r9CredentialFailureRetries)
	t.Run("unchanged credentials cause no extra connection restart", func(t *testing.T) {
		f := newR9CredentialFixture(t, nil)
		old := f.resources()[0]
		for i := 0; i < 2; i++ {
			require.NoError(t, f.m.Reload(context.Background(), f.cfg, f.present))
			f.fixedSettings(t)
		} // repeated identical observations
		require.Len(t, f.resources(), 1)
		require.Zero(t, old.stops())
		require.Len(t, old.startIntents(), 1)
		current, registered := f.m.GetChannel(r9CredentialID)
		require.True(t, registered)
		require.Same(t, old, current)
		require.True(t, current.IsRunning())
		f.deliver(t, old)
	})
	t.Run("already disposed old resource receives no second Stop", func(t *testing.T) {
		f := newR9CredentialFixture(t, nil)
		old := f.resources()[0]
		require.NoError(t, f.m.Reload(context.Background(), f.cfg, f.absent))
		all := f.resources()
		require.Len(t, all, 2)
		replacement := all[1]
		require.Equal(t, 1, old.stops())
		require.False(t, old.IsRunning())
		require.True(t, replacement.IsRunning())
		for i := 0; i < 2; i++ {
			require.NoError(t, f.m.Reload(context.Background(), f.cfg, f.absent))
			f.fixedSettings(t)
		}
		require.Len(t, f.resources(), 2)
		require.Equal(t, 1, old.stops())
		require.Zero(t, replacement.stops())
		require.Len(t, replacement.startIntents(), 1)
		current, registered := f.m.GetChannel(r9CredentialID)
		require.True(t, registered)
		require.Same(t, replacement, current)
		f.deliver(t, replacement)
	})
}

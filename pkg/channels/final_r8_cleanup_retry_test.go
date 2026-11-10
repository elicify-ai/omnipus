package channels

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
)

type finalCleanupResource struct {
	*failOnceChannel
	entered, resume chan struct{}
	stopFailures    []error
	stops           int
}

func (c *finalCleanupResource) Start(ctx context.Context) error {
	close(c.entered)
	<-c.resume
	return c.failOnceChannel.Start(ctx) // actual successful adapter boundary, never a failed Start
}
func (c *finalCleanupResource) Stop(context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stops++
	if c.stops <= len(c.stopFailures) {
		return c.stopFailures[c.stops-1]
	}
	c.run = false
	return nil
}
func (c *finalCleanupResource) stopCount() int { c.mu.Lock(); defer c.mu.Unlock(); return c.stops }

func finalCleanupConfig(t *testing.T, store *PairingAttempts, before *config.Config, enabled bool) *config.Config {
	t.Helper()
	candidate, err := before.Clone()
	require.NoError(t, err)
	inst := candidate.Channels["gate-final-cleanup"]
	inst.Enabled = enabled
	candidate.Channels["gate-final-cleanup"] = inst
	order := store.LockChannel("gate-final-cleanup")
	commit, err := PreparePairingConfig(before, candidate, []*PairingOrder{order}, nil)
	require.NoError(t, err)
	commit.Commit(true)
	order.Unlock()
	return candidate
}

// T2-SF-23: a retry is the same captured-resource recovery protocol, not a
// log-only generic removal. Keep its error, exact pointer and retry hash through
// repeated failures; remove them only after that captured Stop really succeeds.
func TestManager_RetainedObsoleteStartKeepsSecondCleanupFailure(t *testing.T) {
	for _, failures := range []int{2, 1, 0} {
		t.Run(fmt.Sprintf("cleanup failures=%d", failures), func(t *testing.T) {
			firstErr, secondErr := errors.New("first captured cleanup unfinished"), errors.New("second captured cleanup unfinished")
			errs := []error{firstErr, secondErr}[:failures]
			started, resume, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(resume) }) }
			captured := &finalCleanupResource{failOnceChannel: &failOnceChannel{name: "gate-final-cleanup"}, entered: started, resume: resume, stopFailures: errs}
			t.Cleanup(SwapFactoryForTesting("gate-final-cleanup", func(*config.Config, string, credentials.SecretBundle, *bus.MessageBus) (Channel, error) {
				return captured, nil
			}))
			before := minimalCfg(map[string]config.ChannelInstanceConfig{"gate-final-cleanup": {Type: "gate-final-cleanup", Enabled: false}})
			mb := bus.NewMessageBus()
			t.Cleanup(mb.Close)
			m, err := NewManager(before, credentials.SecretBundle{}, mb, nil)
			require.NoError(t, err)
			require.NoError(t, m.StartAll(context.Background()))
			cfgA := finalCleanupConfig(t, m.PairingAttempts(), before, true)
			var firstReloadErr error
			go func() {
				defer close(done)
				firstReloadErr = m.Reload(context.Background(), cfgA, credentials.SecretBundle{})
			}()
			t.Cleanup(func() {
				release()
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Error("HARNESS: owning manager reload did not join")
				}
				require.NoError(t, captured.Stop(context.Background()))
				require.NoError(t, m.StopAll(context.Background()))
			})
			select {
			case <-started:
			case <-time.After(5 * time.Second):
				t.Fatal("HARNESS: real manager's successful Start did not reach held external stage")
			}
			cfgB := finalCleanupConfig(t, m.PairingAttempts(), cfgA, false)
			release()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("HARNESS: owning manager reload did not finish after release")
			}
			if failures == 0 {
				require.NoError(t, firstReloadErr)
				_, registered := m.GetChannel("gate-final-cleanup")
				require.False(t, registered)
				require.Equal(t, 1, captured.stopCount())
				require.False(t, captured.IsRunning())
				return
			}
			require.ErrorIs(t, firstReloadErr, firstErr, "first failure must already be visible")
			current, registered := m.GetChannel("gate-final-cleanup")
			require.True(t, registered)
			require.Same(t, captured, current)
			m.mu.Lock()
			firstHash, retained := m.channelHashes["gate-final-cleanup"]
			m.mu.Unlock()
			require.True(t, retained)
			require.NotEmpty(t, firstHash, "instrument: a retry hash was actually retained after first failure")
			secondReloadErr := m.Reload(context.Background(), cfgB, credentials.SecretBundle{})
			if failures == 2 {
				assert.ErrorIs(t, secondReloadErr, secondErr, "second failure is still cleanup unfinished, never successful reload")
				current, registered = m.GetChannel("gate-final-cleanup")
				assert.True(t, registered, "retain the exact unresolved resource for a third retry")
				assert.True(t, current == captured, "exact captured pointer must survive failed cleanup; got %T", current)
				m.mu.Lock()
				hash, exists := m.channelHashes["gate-final-cleanup"]
				m.mu.Unlock()
				assert.True(t, exists, "second failure must not discard its retry/removal hash")
				assert.Equal(t, firstHash, hash)
				require.NoError(t, m.Reload(context.Background(), cfgB, credentials.SecretBundle{}), "third cleanup succeeds")
				assert.Equal(t, 3, captured.stopCount(), "third reload must reach the same retained captured Stop")
			} else {
				require.NoError(t, secondReloadErr, "single-failure recovery control")
				assert.Equal(t, 2, captured.stopCount())
			}
			_, registered = m.GetChannel("gate-final-cleanup")
			assert.False(t, registered)
			m.mu.Lock()
			_, exists := m.channelHashes["gate-final-cleanup"]
			m.mu.Unlock()
			assert.False(t, exists, "successful captured disposal clears retry bookkeeping")
			assert.False(t, captured.IsRunning(), "only an actual successful Stop ends this resource")
		})
	}
}

// Identity control: the retained continuation may dispose A, never unregister
// a different currently registered pointer. Exercise the real cleanup primitive.
func TestManager_CapturedUnpublishedCleanupPreservesDifferentRegisteredResource(t *testing.T) {
	cfg := minimalCfg(map[string]config.ChannelInstanceConfig{"gate-final-cleanup": {Type: "gate-final-cleanup", Enabled: false}})
	mb := bus.NewMessageBus()
	t.Cleanup(mb.Close)
	m, err := NewManager(cfg, credentials.SecretBundle{}, mb, nil)
	require.NoError(t, err)
	a := &finalCleanupResource{failOnceChannel: &failOnceChannel{name: "gate-final-cleanup", run: true}}
	b := &failOnceChannel{name: "gate-final-cleanup", run: true}
	m.mu.Lock()
	m.channels["gate-final-cleanup"] = b // fixture represents an already-published later pointer
	m.channelHashes["gate-final-cleanup"] = "later-resource-hash"
	mr := &managerReload{m: m, ctx: context.Background(), cfg: cfg, list: map[string]string{"gate-final-cleanup": "old-resource-hash"}}
	mr.disposeUnpublishedStart("gate-final-cleanup", a)
	m.mu.Unlock()
	require.NoError(t, mr.startCleanupErr)
	current, registered := m.GetChannel("gate-final-cleanup")
	require.True(t, registered)
	require.Same(t, b, current)
	require.True(t, b.IsRunning())
	require.False(t, a.IsRunning())
	require.Equal(t, 1, a.stopCount())
	m.mu.Lock()
	hash := m.channelHashes["gate-final-cleanup"]
	m.mu.Unlock()
	require.Equal(t, "later-resource-hash", hash)
}

package gateway

import (
	"context"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/channels"
)

// pr-test R7-T2 / DESIGN §3–4: A really prepares a fresh registry, then reaches
// its final config-publication guard. The passive contended-acquisition hook
// holds A before acquiring ANY order. B commits and publishes through the real
// reload meanwhile. This is not a held RequestRestart or a mock registry swap.
func TestChannelReload_OldPreparedPublicationPreservesNewWinner(t *testing.T) {
	for _, identical := range []bool{false, true} {
		name := "changed public config"
		if identical {
			name = "identical-public-config ABA"
		}
		t.Run(name, func(t *testing.T) {
			f := newPairingRESTFixture(t)
			f.endedOwnedAttempt()
			f.reloadJoins.joinCurrent(t)
			require.Equal(t, http.StatusOK, f.put("enable", retryBody("req-a", f.chatA)).Code)
			f.await(channels.PairingStatusCode)
			f.reloadJoins.joinCurrent(t)
			oldCfg := f.al.GetConfig()
			oldGeneration, exists := oldCfg.ChannelOrderGeneration("whatsapp")
			require.True(t, exists)
			ids := f.store.ConfigOrderInstances()
			for id := range oldCfg.Channels {
				ids = append(ids, id)
			}
			sort.Strings(ids)
			require.NotEmpty(t, ids)
			first := ids[0]
			held, resume, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var holding atomic.Bool
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(resume) }) }
			f.store.SetOrderContendedHookForTest(func(id string) {
				if id == first && holding.CompareAndSwap(false, true) {
					close(held)
					<-resume
				}
			})
			guard := f.store.LockChannel(first)
			var unlockOnce sync.Once
			unlock := func() { unlockOnce.Do(guard.Unlock) }
			var oldErr error
			go func() {
				defer close(done)
				oldErr = f.al.ReloadProviderAndConfig(context.Background(), f.provider, oldCfg)
			}()
			t.Cleanup(func() { unlock(); release(); orderingJoin(t, done); f.store.SetOrderContendedHookForTest(nil) })
			orderingWaitStage(t, held)
			unlock() // A is at the real final boundary, holding no earlier order.
			// End the old platform attempt before B's settings write, so this
			// detector isolates stale publication rather than replacement joining.
			f.factory.latest(t).timeout()
			if !identical {
				require.Equal(t, http.StatusOK, f.put("configure", `{"allow_from":["winner-b"]}`).Code)
				f.reloadJoins.joinCurrent(t)
			}
			require.Equal(t, http.StatusOK, f.put("enable", retryBody("req-a", f.chatA)).Code)
			code := f.await(channels.PairingStatusCode)
			require.Equal(t, "req-a", code.RequestID) // Retry keeps its card's original request id; its attempt id is new.
			f.reloadJoins.joinCurrent(t)
			winnerCfg, winnerRegistry := f.al.GetConfig(), f.al.GetRegistry()
			winnerResource, registered := f.svc.ChannelManager.GetChannel("whatsapp")
			require.True(t, registered)
			winner, ok := f.store.Latest("whatsapp")
			require.True(t, ok)
			require.Equal(t, channels.PairingPhaseActive, winner.Phase)
			generation, exists := winnerCfg.ChannelOrderGeneration("whatsapp")
			require.True(t, exists)
			require.NotEqual(t, oldGeneration, generation, "real newer accepted operation, not a public hash substitute")
			if identical {
				require.Equal(t, oldCfg.Channels["whatsapp"], winnerCfg.Channels["whatsapp"])
			}
			release()
			orderingJoin(t, done)
			assert.EqualError(t, oldErr, "saved channel configuration is not applied; reload it before writing")
			assert.Same(t, winnerCfg, f.al.GetConfig(), "A must not publish its obsolete config")
			assert.Same(t, winnerRegistry, f.al.GetRegistry(), "A's actually prepared registry must not replace B's")
			current, registered := f.svc.ChannelManager.GetChannel("whatsapp")
			assert.True(t, registered)
			assert.Same(t, winnerResource, current)
			assert.True(t, current.IsRunning())
			last, ok := f.store.Latest("whatsapp")
			require.True(t, ok)
			assert.Equal(t, winner, last)
		})
	}
}

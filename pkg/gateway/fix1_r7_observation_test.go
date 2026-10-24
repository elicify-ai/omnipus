package gateway

import (
	"fmt"
	"sort"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/channels"
)

// T2-R7-S2: passive input is not a mutating channel intent and cannot allocate
// permanent global order entries. Count actual tracked instances, not heap/time.
func TestPairingObservation_InactiveAbsentSubscriptionsRetainNoEntries(t *testing.T) {
	t.Run("absent inactive IDs and close do not grow the store", func(t *testing.T) {
		s := newT3Delivery(t)
		before := s.store.ConfigOrderInstances()
		sort.Strings(before)
		// 64 distinct valid instance ids are test input, not a performance bound.
		for i := 0; i < 64; i++ {
			id := fmt.Sprintf("whatsapp.passive-%04d", i)
			_, exists := s.f.al.GetConfig().Channels[id]
			require.False(t, exists, "instrument: each observer id is genuinely absent")
			s.h.subscribePairingInterest(s.wc, id, false)
		}
		s.wc.close()
		after := s.store.ConfigOrderInstances()
		sort.Strings(after) // Enumeration order is not part of the store contract.
		assert.Equal(t, before, after, "observer-only inactive requests must retain no new global entries after close")
		require.Empty(t, s.wc.pairingSubs)
		require.Empty(t, t3Drain(t, s.frames))
	})

	t.Run("real channel replay still takes its actual ordering guard", func(t *testing.T) {
		s := newT3Delivery(t)
		a := s.start(channels.PairingAdmissionConnect)
		s.code(a, "QR-real-channel")
		contended := make(chan string, 1)
		s.store.SetOrderContendedHookForTest(func(id string) { contended <- id })
		order := s.store.LockChannel("whatsapp")
		var once sync.Once
		release := func() { once.Do(order.Unlock) }
		s.releases = append(s.releases, release)
		done := s.goWork(func() { s.h.subscribePairingInterest(s.wc, "whatsapp", true) })
		signal := t3ContendedOrFinished(t, contended, done)
		release()
		t3Await(t, done, "guarded real-channel replay completion")
		require.Equal(t, "contended", signal)
		require.Equal(t, t3Code("QR-real-channel", &s.owner), t3Take(t, s.frames))
		s.finish()
		require.Empty(t, t3Drain(t, s.frames))
	})
}

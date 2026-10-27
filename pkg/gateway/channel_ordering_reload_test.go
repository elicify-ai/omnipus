package gateway

import (
	"net/http/httptest"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/channels"
)

// orderingReloadJoins observes the fixture's actual runReloadCycle invocation.
// A receipt closes only AFTER the entire owning cycle (coalesced work included)
// returns. It never polls a value, invokes a second reload, or replaces exec.
type orderingReloadJoins struct {
	mu      sync.Mutex
	cycles  []<-chan struct{}
	changed chan struct{}
}

func newOrderingReloadJoins() *orderingReloadJoins {
	return &orderingReloadJoins{changed: make(chan struct{})}
}

func (j *orderingReloadJoins) begin() chan struct{} {
	j.mu.Lock()
	defer j.mu.Unlock()
	done := make(chan struct{})
	j.cycles = append(j.cycles, done)
	close(j.changed)
	j.changed = make(chan struct{})
	return done
}

func (j *orderingReloadJoins) mark() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return len(j.cycles)
}

// Three seconds bounds a broken join only. It is not the 300 ms response
// deadline, a latency assertion, or a replacement for completion evidence.
func orderingJoin(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("HARNESS: owning runReloadCycle did not return; this is not assertion-level RED")
	}
}

func (j *orderingReloadJoins) joinCurrent(t *testing.T) {
	t.Helper()
	j.mu.Lock()
	var last <-chan struct{}
	if len(j.cycles) != 0 {
		last = j.cycles[len(j.cycles)-1]
	}
	j.mu.Unlock()
	if last != nil {
		orderingJoin(t, last)
	}
}

func (j *orderingReloadJoins) next(t *testing.T, after int) <-chan struct{} {
	t.Helper()
	guard := time.NewTimer(3 * time.Second)
	defer guard.Stop()
	for {
		j.mu.Lock()
		if len(j.cycles) > after {
			done := j.cycles[after]
			j.mu.Unlock()
			return done
		}
		changed := j.changed
		j.mu.Unlock()
		select {
		case <-changed:
		case <-guard.C:
			t.Fatal("HARNESS: the reload owner never acknowledged entry; not behavioral RED")
		}
	}
}

// orderingExpireRetry scopes a fake clock to the HTTP handler's own wait. The
// real reload owner is outside this bubble and held at construction by the
// existing factory gate. No external transport or service is run on fake time.
func orderingExpireRetry(t *testing.T, f *pairingRESTFixture) *httptest.ResponseRecorder {
	t.Helper()
	var response *httptest.ResponseRecorder
	synctest.Test(t, func(st *testing.T) {
		start := time.Now()
		response = f.put("enable", retryBody("req-a", f.chatA))
		require.Equal(st, 300*time.Millisecond, time.Since(start), "DESIGN T8: only the response wait expires at 300 ms logical time")
	})
	return response
}

func orderingHoldFactory(t *testing.T, f *pairingRESTFixture) func() {
	t.Helper()
	gate := make(chan struct{})
	f.factory.mu.Lock()
	f.factory.gate = gate
	f.factory.mu.Unlock()
	var once sync.Once
	release := func() {
		once.Do(func() {
			f.factory.mu.Lock()
			f.factory.gate = nil
			f.factory.mu.Unlock()
			close(gate)
		})
	}
	t.Cleanup(release)
	return release
}

// DESIGN §5 T8: response expiry is not a reload join or permission to settle.
// A same-cycle follow-up is requested while held to exercise final coalesced
// completion; no reload is requested merely in order to wait for completion.
func TestPairingREST_ExpiryDoesNotStandForSettlement(t *testing.T) {
	f := newPairingRESTFixture(t)
	f.endedOwnedAttempt()
	f.reloadJoins.joinCurrent(t)
	mark := f.reloadJoins.mark()
	previous := reloadWaitTimeout
	reloadWaitTimeout = 300 * time.Millisecond
	release := orderingHoldFactory(t, f)
	t.Cleanup(func() {
		release()
		f.reloadJoins.joinCurrent(t)
		reloadWaitTimeout = previous
	})

	rec := orderingExpireRetry(t, f)
	require.Equal(t, 500, rec.Code, rec.Body.String())
	require.Equal(t, "The WhatsApp connection is enabled, but its restart could not be confirmed yet. "+
		"The new code appears in this chat when the pairing starts.", decodeError(t, rec).Error)
	owner := f.reloadJoins.next(t, mark)
	select {
	case <-owner:
		t.Fatal("expiry incorrectly stood for the owning reload's completion")
	default:
	}
	reservation, ok := f.store.Latest("whatsapp")
	require.True(t, ok)
	require.Equal(t, channels.PairingPhaseReady, reservation.Phase)
	require.Equal(t, &channels.PairingOwner{SessionID: f.chatA, RequestID: "req-a"}, reservation.Owner)

	release()
	orderingJoin(t, owner)
	code := f.await(channels.PairingStatusCode)
	require.Equal(t, "req-a", code.RequestID)
	require.Equal(t, f.chatA, code.SessionID)
	active, ok := f.store.Latest("whatsapp")
	require.True(t, ok)
	require.Equal(t, reservation.AttemptID, active.AttemptID, "no fallback admission")
	require.Equal(t, channels.PairingPhaseActive, active.Phase)
	require.Equal(t, mark+1, f.reloadJoins.mark(), "joining must not trigger another cycle")
	require.False(t, f.svc.reloadOutcome.lastFailed(), "the owning cycle's actual outcome")
}

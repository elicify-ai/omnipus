package whatsapp

import (
	"context"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mau.fi/whatsmeow"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
)

// T2-SF-21 / DESIGN §3–4: this is the actual native QR loop/Stop handle and
// the real agent replacement-disposal worker, not a fake Dispose outcome.
// Fake time advances the shipped bounded retry schedule, not a wall-clock
// performance oracle. The independent outcome oracle is the actual late join.
func TestPairingReplacement_ExhaustedRetriesStillSettleOnNativeLateJoin(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		before := &config.Config{Channels: map[string]config.ChannelInstanceConfig{"whatsapp": {Type: "whatsapp", Enabled: false}}}
		manager, err := channels.NewManager(before, credentials.SecretBundle{}, bus.NewMessageBus(), nil)
		require.NoError(t, err)
		f := newNativeFixture(t, t.TempDir())
		f.store = manager.PairingAttempts()
		f.ch.SetPairingAttempts(f.store, "whatsapp")
		al := &agent.AgentLoop{}
		al.SetChannelManager(manager)
		a := f.admitReady(t, channels.PairingAdmissionConnect, ownerA)
		_, err = f.ch.beginAttempt(a)
		require.NoError(t, err)
		held, resume, joined := make(chan struct{}), make(chan struct{}), make(chan struct{})
		f.ch.SetPairingAttemptObserver(func(_ string, _ channels.PairingStatus, qr, _ string) {
			if qr == "QR-HOLD" {
				close(held)
				<-resume
			}
		})
		qr := make(chan whatsmeow.QRChannelItem, 1)
		f.ch.runCtx, f.ch.runCancel = context.WithCancel(context.Background())
		f.ch.wg.Add(1)
		go func() { f.ch.runQRLoop(a, qr, false); close(joined) }()
		qr <- whatsmeow.QRChannelItem{Event: "code", Code: "QR-HOLD"}
		<-held
		var released atomic.Bool
		release := func() {
			if released.CompareAndSwap(false, true) {
				close(resume)
			}
		}
		t.Cleanup(release)
		candidate := &config.Config{Channels: map[string]config.ChannelInstanceConfig{"whatsapp": {Type: "whatsapp", Enabled: true}}}
		order := f.store.LockChannel("whatsapp")
		commit, err := channels.PreparePairingConfig(before, candidate, []*channels.PairingOrder{order}, nil)
		require.NoError(t, err)
		progress := commit.Commit(true)
		order.Unlock()
		require.Len(t, progress, 1)
		require.NotNil(t, progress[0].Dispose, "the config commit captured the real native handle")
		var calls atomic.Int32
		nativeDispose := progress[0].Dispose
		progress[0].Dispose = func(ctx context.Context) error { calls.Add(1); return nativeDispose(ctx) }
		al.PublishPairingProgress(progress)
		// Current schedule: six 10s contexts, separated by 2/5/15/30/60s.
		// Advance beyond the bounded schedule, then settle all runnable work.
		time.Sleep(6*10*time.Second + (2+5+15+30+60)*time.Second + time.Second)
		synctest.Wait()
		t.Logf("actual native teardown invocations after bounded schedule: %d", calls.Load())
		require.Positive(t, calls.Load(), "instrument: the real replacement worker ran")
		pending, exists := f.store.Attempt("whatsapp", a)
		require.True(t, exists)
		require.Equal(t, channels.PairingPhaseStopping, pending.Phase)
		_, err = f.store.Admit("whatsapp", channels.PairingAdmissionConnect, &channels.PairingOwner{SessionID: "chat-b", RequestID: "before-join"})
		require.ErrorIs(t, err, channels.ErrPairingConflict, "held native work excludes new work")
		release()
		<-joined
		f.ch.wg.Wait()
		synctest.Wait() // actual native join and any real completion bridge, not response expiry
		settled, exists := f.store.Attempt("whatsapp", a)
		require.True(t, exists)
		assert.Equal(t, channels.PairingPhaseTerminal, settled.Phase, "late captured completion must settle even after all bounded retries returned")
		assert.Equal(t, channels.PairingOutcomeInterrupted, settled.Outcome)
		_, err = f.store.Admit("whatsapp", channels.PairingAdmissionConnect, &channels.PairingOwner{SessionID: "chat-b", RequestID: "after-join"})
		assert.NoError(t, err, "no permanent replacement-stopping lockout after the actual native loop joined")
	})
}

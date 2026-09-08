package browser

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/tools/browser/webrtc"
	"github.com/stretchr/testify/require"
)

type lossOfferTuple struct {
	binding, offer, generation uint64
	target                     string
}

type qualifiedLossRelay struct {
	adapterRelay
	lossMu         sync.Mutex
	installed      lossOfferTuple
	snapshotSerial uint64
	qualifiedLost  func(uint64, uint64, uint64, string)
	afterSnapshot  func()
}

func (r *qualifiedLossRelay) SetOnIngestLostForOffer(fn func(uint64, uint64, uint64, string)) {
	r.lossMu.Lock()
	defer r.lossMu.Unlock()
	r.qualifiedLost = fn
}
func (r *qualifiedLossRelay) InstalledIngestOffer() (binding, offer, generation uint64, target string, serial uint64) {
	r.lossMu.Lock()
	tuple, n, after := r.installed, r.snapshotSerial, r.afterSnapshot
	r.afterSnapshot = nil
	r.lossMu.Unlock()
	if after != nil {
		after()
	}
	return tuple.binding, tuple.offer, tuple.generation, tuple.target, n
}
func qualifiedLossFixture(t *testing.T) (*CaptureSession, *qualifiedLossRelay, *healthRecorder, uint64) {
	t.Helper()
	r := &qualifiedLossRelay{adapterRelay: adapterRelay{nextToken: 40}, installed: lossOfferTuple{47, 9, 1, "page-a"}, snapshotSerial: 10}
	var starts int32
	cs, err := NewCaptureSessionWithDeps(nil, "qualified-loss", r, fakeEncoderStarter(&starts, nil), nil)
	require.NoError(t, err)
	t.Cleanup(cs.Stop)
	_, err = cs.BeginFrameTransition("page-a", 800, 600, 1)
	require.NoError(t, err)
	epoch := adapterBind(t, cs, context.Background())
	rec := &healthRecorder{}
	cs.SetOnVideoHealth(rec.observe)
	r.mu.Lock()
	r.stats = webrtc.Stats{VideoPackets: 99, VideoReceipt: webrtc.VideoReceipt{BindingToken: 47, Generation: 1, TargetID: "page-a", Serial: 10}}
	r.mu.Unlock()
	return cs, r, rec, epoch
}

func TestCaptureHealthLossPrefersQualifiedRelayCapability(t *testing.T) {
	cs, r, _, _ := qualifiedLossFixture(t)
	r.lossMu.Lock()
	qualified := r.qualifiedLost
	r.lossMu.Unlock()
	require.NotNil(t, qualified, "current relay must install original-offer loss callback")
	r.mu.Lock()
	legacy := r.onIngestLost
	r.mu.Unlock()
	require.Nil(t, legacy, "a modern relay must not retain the unqualified fallback")
	_ = cs
}

func TestCaptureHealthLossRejectsRetiredOfferIdentity(t *testing.T) {
	shrinkRecoveryTiming(t, time.Hour, time.Hour)
	for _, scenario := range []string{"current", "retired offer", "old binding", "stale generation", "wrong target", "missing offer", "unbound", "canceled", "replacement binding", "stopped"} {
		t.Run(scenario, func(t *testing.T) {
			cs, r, rec, epoch := qualifiedLossFixture(t)
			original := lossOfferTuple{47, 9, 1, "page-a"}
			switch scenario {
			case "retired offer":
				original.offer = 8
			case "old binding":
				original.binding = 40
			case "stale generation":
				original.generation = 2
				r.installed = original
			case "wrong target":
				original.target = "page-b"
				r.installed = original
			case "missing offer":
				original.offer = 0
				r.installed = original
			case "unbound":
				cs.UnbindIngest(epoch)
			case "canceled":
				cs.mu.Lock()
				cs.ingestBindingCancel()
				cs.mu.Unlock()
			case "replacement binding":
				adapterBind(t, cs, context.Background())
			case "stopped":
				cs.Stop()
			}
			cs.onIngestLostForOffer(original.binding, original.offer, original.generation, original.target)
			want := 0
			if scenario == "current" {
				want = 1
			}
			require.Equal(t, want, r.recaptureCount(), "loss must retain original offer and current capture ownership")
			require.Equal(t, want, rec.count(VideoHealthLost))
		})
	}
}

func TestCaptureHealthLossSnapshotKeepsLaterFinitePacketPostClaim(t *testing.T) {
	shrinkRecoveryTiming(t, time.Hour, time.Hour)
	cs, r, rec, _ := qualifiedLossFixture(t)
	// The validated old installed tuple and serial 10 are returned together.
	// A replacement installs and receives its sole packet before the CS claim
	// resumes. A late Stats baseline would incorrectly absorb that packet.
	r.afterSnapshot = func() {
		r.lossMu.Lock()
		r.installed = lossOfferTuple{47, 10, 1, "page-a"}
		r.snapshotSerial = 11
		r.lossMu.Unlock()
		r.mu.Lock()
		r.stats.VideoReceipt.Serial = 11
		r.mu.Unlock()
	}
	cs.onIngestLostForOffer(47, 9, 1, "page-a")
	cs.RecordVideoProgress()
	require.Equal(t, 1, rec.count(VideoHealthRecovered), "packet after the installed snapshot remains post-loss evidence")
	require.Equal(t, 1, r.recaptureCount())
}

func TestCaptureHealthLossCancellationDuringInstalledSnapshot(t *testing.T) {
	shrinkRecoveryTiming(t, time.Hour, time.Hour)
	cs, r, rec, _ := qualifiedLossFixture(t)
	cs.mu.Lock()
	cancelOriginalBinding := cs.ingestBindingCancel
	cs.mu.Unlock()
	// The original socket can end while the coherent relay snapshot waits on
	// its state lock; capture ownership must be rechecked after that wait.
	r.afterSnapshot = cancelOriginalBinding
	cs.onIngestLostForOffer(47, 9, 1, "page-a")
	require.Zero(t, rec.count(VideoHealthLost), "retired binding cannot claim loss after the snapshot wait")
	require.Zero(t, r.recaptureCount())
	cs.mu.Lock()
	episode := cs.ingestRecoveryCtx
	cs.mu.Unlock()
	require.Nil(t, episode, "rejected source must not create even a canceled recovery episode")
}

type healthSnapshotCancelRelay struct {
	*adapterRelay
	afterStats func()
}

func (r *healthSnapshotCancelRelay) Stats() webrtc.Stats {
	stats := r.adapterRelay.Stats()
	if r.afterStats != nil {
		r.afterStats()
	}
	return stats
}

func TestCaptureHealthLossCancellationDuringProgressSnapshot(t *testing.T) {
	shrinkRecoveryTiming(t, time.Hour, time.Hour)
	for _, sampled := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "sampled"}[sampled], func(t *testing.T) {
			cs, r, rec, epoch := receiptHealthFixture(t)
			sample := CaptureHealthObservation{CaptureGeneration: 1, TargetID: "page-a", Generation: 47, TrackState: "ended"}
			require.True(t, cs.RecordIngestHeartbeat(epoch, &sample))
			sample = cs.CaptureHealth()
			cs.mu.Lock()
			cs.relay = &healthSnapshotCancelRelay{adapterRelay: r, afterStats: cs.ingestBindingCancel}
			cs.mu.Unlock()
			if sampled {
				require.False(t, cs.ReportCaptureFailureForObservation(sample), "sampled failure lost its original binding during relay snapshot")
			} else {
				cs.ReportCaptureFailure()
			}
			require.Zero(t, rec.count(VideoHealthLost), "canceled source cannot publish a newly claimed loss")
			require.Zero(t, r.recaptureCount())
			cs.mu.Lock()
			episode := cs.ingestRecoveryCtx
			cs.mu.Unlock()
			require.Nil(t, episode, "canceled snapshot cannot create an episode")
		})
	}
}

package browser

import (
	"reflect"
	"testing"
	"time"
)

// The independent review's ownership invariant: only the exact current server
// observation may start recovery. A matching local generation alone is not an
// identity, since reconnects restart that counter.
func TestCaptureHealthObservationUsesServerBindingIdentity(t *testing.T) {
	cs := &CaptureSession{}
	for i := 0; i < 2; i++ {
		epoch := bindHealthFixture(cs)
		sample := CaptureHealthObservation{BindingEpoch: 999, Generation: 1, TrackState: "live"}
		if !cs.RecordIngestHeartbeat(epoch, &sample) {
			t.Fatal("current heartbeat rejected")
		}
		if got := cs.CaptureHealth().BindingEpoch; got != epoch {
			t.Fatalf("observation binding = %d, want server binding %d", got, epoch)
		}
	}
}

func TestCaptureHealthFailureRejectsSupersededObservation(t *testing.T) {
	shrinkRecoveryTiming(t, time.Hour, time.Hour)
	for _, replacement := range []string{"binding", "generation", "new sample", "unbound", "stopped"} {
		t.Run(replacement, func(t *testing.T) {
			relay := &fakeRelay{}
			cs, rec := newRecoveryTestSession(t, relay)
			epoch := bindHealthFixture(cs)
			failed := CaptureHealthObservation{Generation: 1, TrackState: "ended", PeerState: "connected"}
			if !cs.RecordIngestHeartbeat(epoch, &failed) {
				t.Fatal("fixture heartbeat rejected")
			}
			old := cs.CaptureHealth()
			switch replacement {
			case "binding":
				epoch = bindHealthFixture(cs)
				cs.RecordIngestHeartbeat(epoch, &failed)
			case "generation":
				failed.Generation = 2
				cs.RecordIngestHeartbeat(epoch, &failed)
			case "new sample":
				failed.TrackState = "live"
				cs.RecordIngestHeartbeat(epoch, &failed)
			case "unbound":
				cs.UnbindIngest(epoch)
			case "stopped":
				cs.Stop()
			}
			before := relay.recaptureCount()
			if cs.ReportCaptureFailureForObservation(old) {
				t.Error("superseded observation was accepted as current failure evidence")
			}
			if got := relay.recaptureCount(); got != before {
				t.Errorf("superseded evidence requested recapture: got %d want %d", got, before)
			}
			if got := rec.states(); len(got) != 0 {
				t.Errorf("superseded evidence changed video health: %v", got)
			}
		})
	}
}

func TestCaptureHealthCurrentFailureStartsOneRecovery(t *testing.T) {
	shrinkRecoveryTiming(t, time.Hour, time.Hour)
	relay := &fakeRelay{}
	cs, rec := newRecoveryTestSession(t, relay)
	epoch := bindHealthFixture(cs)
	failed := CaptureHealthObservation{Generation: 1, TrackState: "ended", PeerState: "connected"}
	if !cs.RecordIngestHeartbeat(epoch, &failed) {
		t.Fatal("fixture heartbeat rejected")
	}
	current := cs.CaptureHealth()
	before := relay.recaptureCount()
	firstAccepted := cs.ReportCaptureFailureForObservation(current)
	secondAccepted := cs.ReportCaptureFailureForObservation(current)
	if !firstAccepted || !secondAccepted {
		t.Fatal("current failure report rejected")
	}
	if got := relay.recaptureCount(); got != before+1 {
		t.Fatalf("duplicate current evidence caused %d recaptures, want 1", got-before)
	}
	if got, want := rec.states(), []VideoHealthState{VideoHealthLost, VideoHealthRecovering}; !reflect.DeepEqual(got, want) {
		t.Fatalf("current failure events = %v, want %v", got, want)
	}
}

// Review timeline: packet 101 precedes loss and therefore cannot prove its
// recovery; packet 102 follows loss and can. These are event counts, not time
// delays, and the real recovery state machine runs throughout.
func TestCaptureHealthRecoveryRequiresPostLossProgress(t *testing.T) {
	shrinkRecoveryTiming(t, time.Hour, time.Hour)
	relay := &fakeRelay{}
	cs, rec := newRecoveryTestSession(t, relay)
	relay.mu.Lock()
	relay.stats.VideoPackets = 101
	relay.mu.Unlock()
	relay.triggerIngestLost()
	cs.RecordVideoProgress()
	if got := rec.count(VideoHealthRecovered); got != 0 {
		t.Errorf("pre-loss packets completed recovery %d times", got)
	}
	cs.mu.Lock()
	attempts, pending := cs.ingestRecoveryAttempts, cs.ingestRecoveryTimer != nil
	cs.mu.Unlock()
	if attempts != 1 || !pending {
		t.Errorf("pre-loss packets canceled the retry: attempts=%d pending=%t", attempts, pending)
	}
	relay.mu.Lock()
	relay.stats.VideoPackets = 102
	relay.mu.Unlock()
	cs.RecordVideoProgress()
	if got := rec.count(VideoHealthRecovered); got != 1 {
		t.Errorf("post-loss progress must produce exactly one recovery, got %d", got)
	}
	cs.mu.Lock()
	attempts, pending = cs.ingestRecoveryAttempts, cs.ingestRecoveryTimer != nil
	cs.mu.Unlock()
	if attempts != 0 || pending {
		t.Errorf("post-loss progress did not retire retry: attempts=%d pending=%t", attempts, pending)
	}
}

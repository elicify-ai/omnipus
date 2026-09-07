package browser

import (
	"context"
	"testing"
	"time"
)

// Connection ownership ends when its original context is canceled, even if the
// reader has not yet reached UnbindIngest. No late message may extend liveness
// or alter the evidence on which recovery acts.
func TestCaptureHealthCanceledBindingCannotRefreshHeartbeat(t *testing.T) {
	for _, structured := range []bool{false, true} {
		name := "bare"
		if structured {
			name = "structured"
		}
		t.Run(name, func(t *testing.T) {
			cs, _ := adapterFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			epoch := adapterBind(t, cs, ctx)
			current := CaptureHealthObservation{Generation: 1, TrackState: "live", SampleTimestampMS: 10}
			if !cs.RecordIngestHeartbeat(epoch, &current) {
				t.Fatal("current binding heartbeat rejected")
			}
			before := cs.CaptureHealth()
			cs.mu.Lock()
			cs.lastPingAt = time.Unix(100, 0)
			cs.mu.Unlock()
			cancel()
			var late *CaptureHealthObservation
			if structured {
				late = &CaptureHealthObservation{Generation: 2, TrackState: "ended", SampleTimestampMS: 20}
			}
			if cs.RecordIngestHeartbeat(epoch, late) {
				t.Error("canceled connection refreshed heartbeat")
			}
			if got := cs.CaptureHealth(); got != before {
				t.Errorf("canceled connection changed evidence: got %+v want %+v", got, before)
			}
			if got := cs.LastPingAt(); !got.Equal(time.Unix(100, 0)) {
				t.Errorf("canceled connection extended liveness: %v", got)
			}
		})
	}
}

func TestCaptureHealthCanceledBindingCannotTriggerRecovery(t *testing.T) {
	shrinkRecoveryTiming(t, time.Hour, time.Hour)
	cs, relay := adapterFixture(t)
	recorder := &healthRecorder{}
	cs.SetOnVideoHealth(recorder.observe)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	epoch := adapterBind(t, cs, ctx)
	failed := CaptureHealthObservation{Generation: 1, TrackState: "ended"}
	if !cs.RecordIngestHeartbeat(epoch, &failed) {
		t.Fatal("current binding heartbeat rejected")
	}
	observation := cs.CaptureHealth()
	cancel()
	if cs.ReportCaptureFailureForObservation(observation) {
		t.Error("canceled connection's observation triggered recovery")
	}
	if count := relay.recaptureCount(); count != 0 {
		t.Errorf("canceled connection requested %d recaptures, want zero", count)
	}
	if events := recorder.states(); len(events) != 0 {
		t.Errorf("canceled connection published recovery events: %v", events)
	}
}

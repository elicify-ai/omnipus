package browser

import (
	"testing"
	"time"
)

func bindHealthFixture(cs *CaptureSession) uint64 {
	_, epoch := cs.BindIngest(func(string, *string, int, int, int) error { return nil }, func() {})
	return epoch
}

func TestCaptureHealthReplacementClearsPreviousEvidence(t *testing.T) {
	cs := &CaptureSession{}
	first := bindHealthFixture(cs)
	sample := CaptureHealthObservation{Generation: 1, TrackState: "ended", PeerState: "failed"}
	if !cs.RecordIngestHeartbeat(first, &sample) {
		t.Fatal("current connection heartbeat rejected")
	}
	bindHealthFixture(cs)
	if got := cs.CaptureHealth(); got != (CaptureHealthObservation{}) {
		t.Fatalf("replacement retained previous connection failure evidence: %+v", got)
	}
}

func TestCaptureHealthRejectsInactiveConnectionWithoutChangingEvidence(t *testing.T) {
	for _, state := range []string{"superseded", "unbound", "stopped", "never bound"} {
		t.Run(state, func(t *testing.T) {
			cs := &CaptureSession{}
			epoch := uint64(0)
			if state != "never bound" {
				epoch = bindHealthFixture(cs)
			}
			switch state {
			case "superseded":
				bindHealthFixture(cs)
			case "unbound":
				cs.UnbindIngest(epoch)
			case "stopped":
				cs.stopped = true
			}
			before := CaptureHealthObservation{Generation: 7, TrackState: "live", PeerState: "connected", ObservedAt: time.Unix(100, 0)}
			cs.captureHealth = before
			cs.lastPingAt = time.Unix(200, 0)
			late := CaptureHealthObservation{Generation: 1, TrackState: "ended"}
			if cs.RecordIngestHeartbeat(epoch, &late) {
				t.Error("inactive capture connection heartbeat accepted")
			}
			if got := cs.CaptureHealth(); got != before {
				t.Errorf("inactive connection changed health: got %+v want %+v", got, before)
			}
			if got := cs.LastPingAt(); !got.Equal(time.Unix(200, 0)) {
				t.Errorf("inactive connection refreshed liveness: %v", got)
			}
		})
	}
}

func TestCaptureHealthCurrentHeartbeatPreservesMeasuredSample(t *testing.T) {
	cs := &CaptureSession{}
	epoch := bindHealthFixture(cs)
	sample := CaptureHealthObservation{Generation: 3, TrackState: "live", PeerState: "connected", HasSourceFrames: true, SourceFrames: 0, SampleTimestampMS: 10}
	before := time.Now()
	if !cs.RecordIngestHeartbeat(epoch, &sample) {
		t.Fatal("current connection heartbeat rejected")
	}
	got := cs.CaptureHealth()
	if got.ObservedAt.Before(before) || got.ObservedAt.After(time.Now()) {
		t.Fatalf("sample observation not stamped at receipt: %v", got.ObservedAt)
	}
	want := sample
	want.BindingEpoch = epoch
	want.ObservedAt = got.ObservedAt
	if got != want {
		t.Fatalf("measured sample changed: got %+v want %+v", got, want)
	}
	cs.lastPingAt = time.Unix(200, 0)
	if !cs.RecordIngestHeartbeat(epoch, nil) {
		t.Fatal("current bare heartbeat rejected")
	}
	if current := cs.CaptureHealth(); current != got {
		t.Fatalf("bare heartbeat refreshed or erased independent stage evidence: %+v", current)
	}
	if cs.LastPingAt().Before(before) {
		t.Fatal("current bare heartbeat did not refresh liveness")
	}
}

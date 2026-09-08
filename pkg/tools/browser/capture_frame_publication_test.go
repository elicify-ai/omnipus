package browser

import "testing"

func claimTestFramePublication(cs *CaptureSession) VideoHealthEvent {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return cs.claimFramePublicationLocked()
}

func TestCaptureFramePublicationTransition(t *testing.T) {
	for _, measured := range []bool{false, true} {
		name := "pending geometry"
		w, h := 0, 0
		if measured {
			name = "measured geometry"
			w, h = 800, 600
		}
		t.Run(name, func(t *testing.T) {
			cs, _ := newRecoveryTestSession(t, &fakeRelay{})
			frame, err := cs.BeginFrameTransition("target-a", w, h, 1)
			if err != nil {
				t.Fatal(err)
			}
			event := claimTestFramePublication(cs)
			if event.Version == 0 || event.State != VideoHealthTransitioning || event.Frame != frame || event.Attempt != 0 || event.Detail != "" {
				t.Fatalf("transition claim must retain original unready frame: %+v; frame=%+v", event, frame)
			}
			if !cs.IsCurrentVideoHealthEvent(event) {
				t.Fatal("new frame claim is not current")
			}
		})
	}
}

func TestCaptureFramePublicationFirstBoundary(t *testing.T) {
	cs, _ := newRecoveryTestSession(t, &fakeRelay{})
	frame, err := cs.BeginFrameTransition("target-a", 800, 600, 1)
	if err != nil {
		t.Fatal(err)
	}
	pending := claimTestFramePublication(cs)
	if !cs.CommitFrameBoundary(frame.Generation, "target-a", 0) {
		t.Fatal("zero RTP timestamp is a valid first boundary")
	}
	event := claimTestFramePublication(cs)
	frame.Ready = true
	if event.Version <= pending.Version || event.State != VideoHealthRecovered || event.Frame != frame || event.Attempt != 0 || event.Detail != "" {
		t.Fatalf("first boundary did not publish the exact ready picture: %+v; frame=%+v", event, frame)
	}
	if cs.IsCurrentVideoHealthEvent(pending) {
		t.Fatal("pending claim survived newer ready boundary")
	}
}

func TestCaptureFramePublicationPreservesActiveFailure(t *testing.T) {
	for _, state := range []VideoHealthState{VideoHealthLost, VideoHealthRecovering, VideoHealthUnrecoverable} {
		t.Run(string(state), func(t *testing.T) {
			cs, _ := newRecoveryTestSession(t, &fakeRelay{})
			frame, err := cs.BeginFrameTransition("target-a", 800, 600, 1)
			if err != nil {
				t.Fatal(err)
			}
			cs.mu.Lock()
			failure := cs.videoHealthEventLocked(state, 2, "original failure")
			cs.mu.Unlock()
			if !cs.CommitFrameBoundary(frame.Generation, "target-a", 42) {
				t.Fatal("boundary rejected")
			}
			event := claimTestFramePublication(cs)
			frame.Ready, frame.Timestamp = true, 42
			if event.State != state || event.Attempt != 2 || event.Detail != "original failure" || event.MaxAttempts != failure.MaxAttempts || event.Frame != frame || event.Version <= failure.Version {
				t.Fatalf("boundary invented recovery or lost original failure: got=%+v failure=%+v", event, failure)
			}
		})
	}
}

func TestCaptureFramePublicationNewFrameRetiresPriorFailure(t *testing.T) {
	cs, _ := newRecoveryTestSession(t, &fakeRelay{})
	if _, err := cs.BeginFrameTransition("target-a", 800, 600, 1); err != nil {
		t.Fatal(err)
	}
	cs.mu.Lock()
	old := cs.videoHealthEventLocked(VideoHealthLost, 2, "old target failed")
	cs.mu.Unlock()
	frame, err := cs.BeginFrameTransition("target-b", 900, 700, 2)
	if err != nil {
		t.Fatal(err)
	}
	event := claimTestFramePublication(cs)
	if event.State != VideoHealthTransitioning || event.Attempt != 0 || event.Detail != "" || event.Frame != frame || event.Version <= old.Version {
		t.Fatalf("new target inherited old failure: %+v", event)
	}
}

func TestCaptureFramePublicationSnapshotsOwnTheirAudience(t *testing.T) {
	cs, _ := newRecoveryTestSession(t, &fakeRelay{})
	if _, err := cs.BeginFrameTransition("target-a", 800, 600, 1); err != nil {
		t.Fatal(err)
	}
	cs.AddViewer("original-viewer")
	cs.mu.Lock()
	event := cs.videoHealthEventLocked(VideoHealthLost, 1, "original")
	cs.mu.Unlock()
	event.ViewerIDs[0] = "observer mutation"
	first, ok := cs.CurrentVideoHealthEvent()
	if !ok || first.Version != event.Version || len(first.ViewerIDs) != 1 || first.ViewerIDs[0] != "original-viewer" {
		t.Fatalf("saved claim aliased observer: %+v ok=%v", first, ok)
	}
	first.ViewerIDs[0] = "snapshot mutation"
	second, ok := cs.CurrentVideoHealthEvent()
	if !ok || second.Version != event.Version || len(second.ViewerIDs) != 1 || second.ViewerIDs[0] != "original-viewer" {
		t.Fatalf("snapshot read changed saved claim/version: %+v ok=%v", second, ok)
	}
}

func TestCaptureFramePublicationUnavailableSnapshot(t *testing.T) {
	for _, state := range []string{"uninitialized", "stopped", "invalidated", "exhausted"} {
		t.Run(state, func(t *testing.T) {
			cs, _ := newRecoveryTestSession(t, &fakeRelay{})
			if state != "uninitialized" {
				if _, err := cs.BeginFrameTransition("target-a", 800, 600, 1); err != nil {
					t.Fatal(err)
				}
				claimTestFramePublication(cs)
			}
			switch state {
			case "stopped":
				cs.Stop()
			case "invalidated":
				cs.mu.Lock()
				cs.nextVideoHealthVersionLocked()
				cs.mu.Unlock()
			case "exhausted":
				cs.mu.Lock()
				cs.videoHealthVersion = ^uint64(0)
				cs.mu.Unlock()
				if event := claimTestFramePublication(cs); event.Version != 0 {
					t.Fatalf("version exhaustion wrapped: %+v", event)
				}
			}
			if event, ok := cs.CurrentVideoHealthEvent(); ok {
				t.Fatalf("%s snapshot was authorized: %+v", state, event)
			}
		})
	}
}

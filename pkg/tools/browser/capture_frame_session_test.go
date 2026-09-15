package browser

import (
	"testing"
	"time"
)

func TestCaptureFrameSessionRejectsReplacementClaims(t *testing.T) {
	first := &CaptureSession{token: []byte("first independent capture token")}
	second := &CaptureSession{token: []byte("second independent capture token")}
	a, err := first.BeginFrameTransition("page-a", 800, 600, 1)
	if err != nil {
		t.Fatal(err)
	}
	b, err := second.BeginFrameTransition("page-a", 800, 600, 1)
	if err != nil {
		t.Fatal(err)
	}
	if a.CaptureID == "" || a.CaptureID == b.CaptureID || a.Generation != 1 || b.Generation != 1 {
		t.Fatalf("captures lack distinct identities: %+v, %+v", a, b)
	}
	if !first.CommitFrameBoundary(1, "page-a", 0) || !second.CommitFrameBoundary(1, "page-a", 0) {
		t.Fatal("matching frame boundary was rejected")
	}
	if !first.AcceptsInputGeneration(a.CaptureID, 1) || second.AcceptsInputGeneration(a.CaptureID, 1) {
		t.Fatal("replacement capture accepted an old capture's otherwise matching claim")
	}
	if first.AcceptsInputGeneration("", 1) || first.AcceptsInputGeneration(a.CaptureID, 0) {
		t.Fatal("missing capture or generation bypassed the gate")
	}
}

func TestCaptureFrameSessionObserverCanReadState(t *testing.T) {
	cs := &CaptureSession{token: []byte("capture observer fixture")}
	events := make(chan CaptureFrameState, 4)
	cs.SetOnFrameState(func(event CaptureFrameState) {
		read := make(chan CaptureFrameState, 1)
		go func() { read <- cs.FrameState() }()
		select {
		case got := <-read:
			if got != event {
				t.Errorf("observer got stale state: %+v versus %+v", event, got)
			}
		case <-time.After(2 * time.Second):
			t.Error("observer cannot read capture state while its callback runs")
		}
		events <- event
	})
	transition, err := cs.BeginFrameTransition("page-a", 800, 600, 1)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-events:
		if got != transition || got.Ready {
			t.Fatalf("transition event = %+v", got)
		}
	default:
		t.Fatal("transition did not notify observers")
	}
	if !cs.CommitFrameBoundary(transition.Generation, "page-a", 123) {
		t.Fatal("matching boundary rejected")
	}
	select {
	case got := <-events:
		if !got.Ready || got.Timestamp != 123 {
			t.Fatalf("boundary event = %+v", got)
		}
	default:
		t.Fatal("committed boundary did not notify observers")
	}
	cs.mu.Lock()
	cs.stopped = true
	cs.mu.Unlock()
	if cs.AcceptsInputGeneration(transition.CaptureID, transition.Generation) || cs.CommitFrameBoundary(transition.Generation, "page-a", 124) {
		t.Fatal("stopped capture authorized input or committed media")
	}
	if _, err := cs.BeginFrameTransition("page-b", 800, 600, 1); err == nil {
		t.Fatal("stopped capture accepted transition")
	}
}

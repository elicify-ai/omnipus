package browser

import "testing"

// A transition may reserve a target before its layout has been measured. A
// packet boundary alone cannot prove the coordinate space used by input.
func TestCaptureFramePendingGeometryCannotAuthorizeInput(t *testing.T) {
	var frames captureFrameTracker
	pending, err := frames.begin(captureFrameGeometry{TargetID: "page-a", Scale: 1})
	if err != nil {
		t.Fatal(err)
	}
	if frames.commit(pending.Generation, "page-a", 123) {
		t.Error("unmeasured layout accepted a video boundary")
	}
	if frames.accepts(pending.Generation) {
		t.Error("unmeasured layout authorized input")
	}
	if got := frames.snapshot(); got != pending {
		t.Errorf("unmeasured layout changed its pending proof: got %+v want %+v", got, pending)
	}
	// One CSS pixel is the protocol's minimum valid measured dimension.
	measured, err := frames.begin(captureFrameGeometry{TargetID: "page-a", Width: 1, Height: 1, Scale: 1})
	if err != nil || measured.Generation != pending.Generation+1 {
		t.Fatalf("measured layout did not replace pending generation: %+v err=%v", measured, err)
	}
	if !frames.commit(measured.Generation, "page-a", 124) || !frames.accepts(measured.Generation) {
		t.Fatal("minimum measured layout failed to authorize its matching boundary")
	}
}

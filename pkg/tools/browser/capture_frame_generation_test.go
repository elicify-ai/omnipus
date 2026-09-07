package browser

import (
	"math"
	"testing"
)

// FR-009's oracle is the display identity protocol: input remains rejected
// between a target/layout transition and its matching forwarded frame.
// The real state machine is tested without relay, clock, or browser mocks.
func TestCaptureFrameGenerationTransitions(t *testing.T) {
	var frames captureFrameTracker
	if frames.accepts(0) || frames.commit(0, "page-a", 0) {
		t.Fatal("uninitialized capture authorized input or media")
	}
	a := captureFrameGeometry{TargetID: "page-a", Width: 800, Height: 600, Scale: 1}
	first, err := frames.begin(a)
	if err != nil || first.Generation != 1 || first.Ready || first.Geometry != a {
		t.Fatalf("initial transition = %+v, %v", first, err)
	}
	if frames.accepts(1) || frames.commit(1, "page-b", 90) {
		t.Fatal("input or wrong-target media accepted before matching frame")
	}
	if !frames.commit(1, "page-a", 0) || !frames.accepts(1) {
		t.Fatal("matching frame with timestamp zero did not authorize generation")
	}
	again, err := frames.begin(a)
	if err != nil || again.Generation != 1 || !again.Ready {
		t.Fatalf("duplicate layout unnecessarily invalidated picture: %+v, %v", again, err)
	}
	a.Width = 600
	a.Height = 800
	resized, err := frames.begin(a)
	if err != nil || resized.Generation != 2 || resized.Ready || frames.accepts(1) || frames.accepts(2) {
		t.Fatalf("resize retained old input authorization: %+v, %v", resized, err)
	}
	if frames.commit(1, "page-a", 900) || !frames.commit(2, "page-a", 1000) || !frames.accepts(2) {
		t.Fatal("resize boundary failed to distinguish old and new media")
	}
	a.TargetID = "page-b"
	switched, err := frames.begin(a)
	if err != nil || switched.Generation != 3 || frames.accepts(2) || frames.accepts(3) {
		t.Fatalf("same-size target switch reused old proof: %+v, %v", switched, err)
	}
	if frames.commit(3, "page-a", 2000) || !frames.commit(3, "page-b", 2000) || !frames.accepts(3) {
		t.Fatal("target switch accepted wrong target or rejected matching target")
	}
	a.Scale = 2
	scaled, err := frames.begin(a)
	if err != nil || scaled.Generation != 4 || scaled.Ready || frames.accepts(3) {
		t.Fatalf("device scale change retained old proof: %+v, %v", scaled, err)
	}
}

func TestCaptureFrameGenerationRejectsInvalidGeometry(t *testing.T) {
	valid := captureFrameGeometry{TargetID: "page-a", Width: 800, Height: 600, Scale: 1}
	for _, tc := range []struct {
		name   string
		change func(*captureFrameGeometry)
	}{
		{"missing target", func(g *captureFrameGeometry) { g.TargetID = "" }},
		{"negative width", func(g *captureFrameGeometry) { g.Width = -1 }},
		{"partial size", func(g *captureFrameGeometry) { g.Height = 0 }},
		{"oversized width", func(g *captureFrameGeometry) { g.Width = 16385 }},
		{"oversized height", func(g *captureFrameGeometry) { g.Height = 16385 }},
		{"zero scale", func(g *captureFrameGeometry) { g.Scale = 0 }},
		{"oversized scale", func(g *captureFrameGeometry) { g.Scale = 4.01 }},
		{"nonfinite scale", func(g *captureFrameGeometry) { g.Scale = math.NaN() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var frames captureFrameTracker
			before, err := frames.begin(valid)
			if err != nil {
				t.Fatal(err)
			}
			bad := valid
			tc.change(&bad)
			if _, err := frames.begin(bad); err == nil {
				t.Fatal("invalid geometry accepted")
			}
			if got := frames.snapshot(); got != before {
				t.Fatalf("invalid request changed identity: %+v", got)
			}
		})
	}
}

func TestCaptureFrameGenerationBoundaryDoesNotMoveBackward(t *testing.T) {
	var frames captureFrameTracker
	if _, err := frames.begin(captureFrameGeometry{TargetID: "page-a", Scale: 1}); err != nil {
		t.Fatal(err)
	}
	if !frames.commit(1, "page-a", 0xfffffff0) || !frames.commit(1, "page-a", 20) {
		t.Fatal("valid boundary crossing timestamp wrap rejected")
	}
	for _, old := range []uint32{19, 0xfffffff0, 20 + 0x80000000} {
		if frames.commit(1, "page-a", old) {
			t.Fatalf("old or ambiguous timestamp %d replaced committed boundary", old)
		}
		if got := frames.snapshot(); got.Timestamp != 20 || !got.Ready {
			t.Fatalf("rejected boundary changed displayed identity: %+v", got)
		}
	}
}

func TestCaptureFrameGenerationExhaustionDoesNotWrap(t *testing.T) {
	// Boundary fixture comes from the wire contract's maximum exact integer,
	// not from a production constant that could drift together with the test.
	frames := captureFrameTracker{current: captureFrameSnapshot{
		Generation: 9007199254740991,
		Geometry:   captureFrameGeometry{TargetID: "page-a", Width: 800, Height: 600, Scale: 1},
		Timestamp:  90,
		Ready:      true,
	}}
	before := frames.snapshot()
	if _, err := frames.begin(captureFrameGeometry{TargetID: "page-b", Width: 800, Height: 600, Scale: 1}); err == nil {
		t.Fatal("generation counter exceeded the exact wire range")
	}
	if got := frames.snapshot(); got != before || frames.accepts(1) {
		t.Fatalf("exhaustion revived a stale identity: %+v", got)
	}
}

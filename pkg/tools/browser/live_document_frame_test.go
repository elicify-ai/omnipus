package browser

import (
	"context"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

// FR-004/009/011: page mutation retires the old picture before Chrome sees
// the command. An acknowledgement is not evidence of a newly painted page.
func TestLiveNavigationRetiresPictureBeforeBrowserCommand(t *testing.T) {
	for _, kind := range []string{"navigate", "navigate_back", "reload"} {
		t.Run(kind, func(t *testing.T) {
			var cs *CaptureSession
			var original CaptureFrameState
			calls := 0
			lv := newNavigateTestLiveView(t, func(context.Context, time.Duration, ...chromedp.Action) error {
				calls++
				if cs.AcceptsInputGeneration(original.CaptureID, original.Generation) {
					t.Error("old picture still authorizes input when browser receives navigation")
				}
				return nil
			})
			original = installInputTestPicture(t, lv)
			cs = lv.mgr.CaptureSessionForPanel(lv.sessionID)
			original = cs.FrameState()
			if !original.Ready {
				t.Fatal("fixture picture not committed")
			}
			in := LiveInput{Kind: kind}
			if kind == "navigate" {
				in.URL = "https://8.8.8.8/destination" // Public literal: no DNS or real navigation in this protocol fixture.
			}
			if err := lv.dispatchInputContext(context.Background(), "viewer", in); err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatalf("navigation dispatched %d times, want once", calls)
			}
			if frame := cs.FrameState(); frame.Ready || frame.Generation <= original.Generation {
				t.Fatalf("command acknowledgement reopened old picture: original=%+v current=%+v", original, frame)
			}
			if err := lv.dispatchInputContext(context.Background(), "viewer", inputWithTestPicture(original, LiveInput{Kind: "text", Text: "stale"})); err == nil || !IsBenignLiveInputError(err) {
				t.Fatalf("old-picture text after navigation was not rejected: %v", err)
			}
			if calls != 1 {
				t.Fatalf("old-picture text reached browser: commands=%d", calls)
			}
		})
	}
}

func TestLiveRejectedNavigationPreservesHealthyPicture(t *testing.T) {
	calls := 0
	lv := newNavigateTestLiveView(t, func(context.Context, time.Duration, ...chromedp.Action) error { calls++; return nil })
	installInputTestPicture(t, lv)
	cs := lv.mgr.CaptureSessionForPanel(lv.sessionID)
	original := cs.FrameState()
	if !original.Ready {
		t.Fatal("fixture picture not committed")
	}
	if err := lv.dispatchInputContext(context.Background(), "viewer", LiveInput{Kind: "navigate", URL: "file:///private/not-allowed"}); err == nil || IsBenignLiveInputError(err) {
		t.Fatalf("unsafe destination was not explicitly refused: %v", err)
	}
	if calls != 0 || cs.FrameState() != original {
		t.Fatalf("refused navigation disturbed healthy capture: calls=%d frame=%+v", calls, cs.FrameState())
	}
}

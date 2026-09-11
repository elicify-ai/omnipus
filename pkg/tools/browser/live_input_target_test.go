package browser

import (
	"context"
	"testing"
	"time"

	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
)

// The picture-to-input contract requires the receiving tab, not just the
// capture identity and generation, to match the displayed picture. Keep real
// capture admission and manager state; replace only browser command delivery.
func TestLiveInputCommittedPictureRequiresSameTarget(t *testing.T) {
	for _, scenario := range []string{"matching target", "different target", "missing target", "missing tab set", "stale context"} {
		t.Run(scenario, func(t *testing.T) {
			calls := 0
			lv := newNavigateTestLiveView(t, func(context.Context, time.Duration, ...chromedp.Action) error {
				calls++
				return nil
			})
			active := &tabEntry{ctx: lv.tabCtx, targetID: target.ID("displayed-target")}
			lv.mgr.sessions[lv.sessionID] = &sessionEntry{tabs: []*tabEntry{active}}
			cs, _ := newRecoveryTestSession(t, &fakeRelay{})
			lv.mgr.captures = map[string]*CaptureSession{lv.sessionID: cs}
			frame, err := cs.BeginFrameTransition("displayed-target", 800, 600, 1)
			if err != nil {
				t.Fatal(err)
			}
			if !cs.CommitFrameBoundary(frame.Generation, "displayed-target", 123) {
				t.Fatal("fixture failed to commit its displayed picture")
			}
			switch scenario {
			case "different target":
				active.targetID = "another-panels-target"
			case "missing target":
				active.targetID = ""
			case "missing tab set":
				delete(lv.mgr.sessions, lv.sessionID)
			case "stale context":
				var cancel context.CancelFunc
				active.ctx, cancel = context.WithCancel(context.Background())
				defer cancel()
			}
			err = lv.dispatchInput("viewer", LiveInput{Kind: "text", Text: "typed", CaptureID: frame.CaptureID, CaptureGeneration: frame.Generation})
			if scenario == "matching target" {
				if err != nil || calls != 1 {
					t.Fatalf("matching picture must dispatch once: calls=%d err=%v", calls, err)
				}
			} else if err == nil || !IsBenignLiveInputError(err) || err.Error() != "browser live: displayed target changed; wait for the current picture" || calls != 0 {
				t.Fatalf("mismatched picture must reject before command delivery: calls=%d err=%v", calls, err)
			}
		})
	}
}

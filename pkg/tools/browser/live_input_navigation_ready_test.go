package browser

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// Navigation is an escape from an unavailable picture, so Back must reach
// Chrome without presenting a current frame claim. Ordinary input must not.
func TestLiveInputBackWorksWithoutReadyPicture(t *testing.T) {
	for _, scenario := range []string{"missing", "stale", "pending"} {
		t.Run(scenario, func(t *testing.T) {
			var methods []string
			executor := liveInputExecutor(func(_ context.Context, method string, params, result any) error {
				methods = append(methods, method)
				switch method {
				case "Page.getNavigationHistory":
					r := result.(*page.GetNavigationHistoryReturns)
					r.CurrentIndex = 1
					r.Entries = []*page.NavigationEntry{{ID: 17}, {ID: 23}}
				case "Page.navigateToHistoryEntry":
					if id := params.(*page.NavigateToHistoryEntryParams).EntryID; id != 17 {
						t.Fatalf("Back chose entry %d, want 17", id)
					}
				default:
					t.Fatalf("unexpected command %s", method)
				}
				return nil
			})
			lv := newNavigateTestLiveView(t, func(ctx context.Context, _ time.Duration, actions ...chromedp.Action) error {
				for _, action := range actions {
					if err := action.Do(cdp.WithExecutor(ctx, executor)); err != nil {
						return err
					}
				}
				return nil
			})
			lv.viewers["viewer"] = struct{}{}
			reg := &LiveViewRegistry{mgr: lv.mgr, views: map[string]*LiveView{lv.sessionID: lv}}
			in := LiveInput{Kind: "navigate_back"}
			if scenario != "missing" {
				frame := installInputTestPicture(t, lv)
				in = inputWithTestPicture(frame, in)
				if scenario == "stale" {
					in.CaptureGeneration++
				} else {
					cs := lv.mgr.CaptureSessionForPanel(lv.sessionID)
					if _, err := cs.BeginFrameTransition("input-fixture-target", 900, 600, 1); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := reg.InputContext(context.Background(), lv.sessionID, "viewer", in); err != nil {
				t.Fatalf("Back blocked by %s picture: %v", scenario, err)
			}
			if want := []string{"Page.getNavigationHistory", "Page.navigateToHistoryEntry"}; !reflect.DeepEqual(methods, want) {
				t.Fatalf("Back commands %v, want %v", methods, want)
			}
			in.Kind, in.Text = "text", "must not reach old picture"
			if err := reg.InputContext(context.Background(), lv.sessionID, "viewer", in); err == nil || !IsBenignLiveInputError(err) {
				t.Fatalf("ordinary input accepted without current picture: %v", err)
			}
			if len(methods) != 2 {
				t.Fatalf("ordinary input issued commands: %v", methods)
			}
		})
	}
}

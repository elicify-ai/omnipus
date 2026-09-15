package browser

import (
	"context"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/require"
)

// ADR-085: deliberate human input stands the agent down before Chrome sees
// it; stale sources and pointer hover must never create that human hold.
func TestDedicatedInputHandoverAdmission(t *testing.T) {
	for _, tc := range []struct {
		name                                                 string
		kind                                                 string
		disabled, stale, wrongPicture, detached, otherHolder bool
		wantDispatch, wantHold                               bool
	}{
		{name: "text claims", kind: "text", wantDispatch: true, wantHold: true},
		{name: "key claims", kind: "key_down", wantDispatch: true, wantHold: true},
		{name: "click claims", kind: "mouse_down", wantDispatch: true, wantHold: true},
		{name: "wheel claims", kind: "wheel", wantDispatch: true, wantHold: true},
		{name: "hover does not claim", kind: "mouse_move", wantDispatch: true},
		{name: "disabled does not claim", kind: "text", disabled: true, wantDispatch: true},
		{name: "stale source does not claim", kind: "text", stale: true},
		{name: "old picture does not claim", kind: "text", wrongPicture: true},
		{name: "detached viewer does not claim", kind: "text", detached: true},
		{name: "other live holder is not stolen", kind: "text", otherHolder: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var lv *LiveView
			commands := 0
			lv = newNavigateTestLiveView(t, func(context.Context, time.Duration, ...chromedp.Action) error {
				commands++
				lv.mu.Lock()
				held := lv.controller == "human" && lv.isStoodDownLocked()
				lv.mu.Unlock()
				require.Equal(t, tc.wantHold, held, "ownership must be established before browser dispatch")
				return nil
			})
			picture := installInputTestPicture(t, lv)
			lv.mgr.cfg.TakeControlEnabled = !tc.disabled
			if !tc.detached {
				lv.viewers["human"] = struct{}{}
			}
			if tc.otherHolder {
				lv.viewers["other"] = struct{}{}
				require.True(t, lv.takeControl("other"))
			}
			lv.cssViewportW, lv.cssViewportH = 800, 600
			before := time.Now().Add(-time.Minute)
			lv.lastControlActivity = before
			source, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.stale {
				cancel()
			}
			in := inputWithTestPicture(picture, LiveInput{Kind: tc.kind, Text: "x", Key: "a", Code: "KeyA", KeyCode: 65, Button: "left", HasXY: true, X: 10, Y: 10, CaptureWidth: 800, CaptureHeight: 600, DeltaY: 1, SourceContext: source})
			if tc.wrongPicture {
				in.CaptureGeneration++
			}
			err := lv.dispatchInputContext(context.Background(), "human", in)
			if tc.wantDispatch {
				require.NoError(t, err)
				require.Equal(t, 1, commands)
			} else {
				require.Error(t, err)
				require.Zero(t, commands)
			}
			lv.mu.Lock()
			holder, stoodDown, activity := lv.controller, lv.isStoodDownLocked(), lv.lastControlActivity
			lv.mu.Unlock()
			if tc.wantHold {
				require.Equal(t, "human", holder)
				require.True(t, stoodDown)
				require.True(t, activity.After(before))
			} else if tc.otherHolder {
				require.Equal(t, "other", holder)
				require.True(t, stoodDown)
			} else {
				require.Empty(t, holder)
				require.False(t, stoodDown)
			}
		})
	}
}

func TestDedicatedInputHandoverNotifiesOwnViewerAcrossRelease(t *testing.T) {
	lv := newNavigateTestLiveView(t, func(context.Context, time.Duration, ...chromedp.Action) error { return nil })
	picture := installInputTestPicture(t, lv)
	lv.mgr.cfg.TakeControlEnabled = true
	lv.viewers["human"] = struct{}{}
	lv.mgr.live.views[lv.sessionID] = lv
	events := make(chan string, 8)
	lv.mgr.Live().SetControlAcquiredSink(lv.sessionID, "human", func() { events <- "controlling" })
	lv.mgr.Live().SetReleaseNotifySink(lv.sessionID, "human", func() { events <- "released" })
	next := func(want string) {
		t.Helper()
		select {
		case got := <-events:
			require.Equal(t, want, got)
		case <-time.After(time.Second):
			t.Fatalf("missing %s notification", want)
		}
	}
	in := inputWithTestPicture(picture, LiveInput{Kind: "text", Text: "x", SourceContext: context.Background()})
	require.NoError(t, lv.dispatchInputContext(context.Background(), "human", in))
	next("controlling")
	require.NoError(t, lv.dispatchInputContext(context.Background(), "human", in))
	require.True(t, lv.mgr.Live().ReleaseStoodDownForViewer(lv.sessionID, "human"))
	next("released") // A repeated input in the same hold must not notify again.
	require.False(t, lv.mgr.Live().IsStoodDown(lv.sessionID))
	require.NoError(t, lv.dispatchInputContext(context.Background(), "human", in))
	next("controlling")
}

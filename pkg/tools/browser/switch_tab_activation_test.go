package browser

import (
	"context"
	"sync"
	"testing"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// focusTreatment classifies one tabFocusFn call by the CDP actions it carried,
// so tests can tell the two halves of the treatment apart: bringing a tab to
// the foreground (foregroundTabActions) versus releasing the one being left
// (backgroundTabActions). Both go through the SAME seam — see tabFocusFn's doc
// comment for why the actions are passed through rather than hidden.
func focusTreatment(actions []chromedp.Action) string {
	var broughtToFront bool
	var focusEnabled, focusDisabled bool
	for _, a := range actions {
		switch v := a.(type) {
		case *page.BringToFrontParams:
			broughtToFront = true
		case *emulation.SetFocusEmulationEnabledParams:
			if v.Enabled {
				focusEnabled = true
			} else {
				focusDisabled = true
			}
		}
	}
	switch {
	case broughtToFront && focusEnabled:
		return "foreground"
	case !broughtToFront && focusDisabled:
		return "background"
	default:
		return "unknown"
	}
}

// Regression coverage for the live-measured 2026-08-03 defect: switching tabs
// updated ONLY this manager's se.activeIdx and never told Chrome, so the
// WebRTC capture path — whose encoder resolves its target with
// chrome.tabs.query({active: true, ...}) (captureext/embedded/encoder.js
// findActiveTargetTab) — kept streaming the tab the user had just switched
// AWAY from.
//
// Measured on UAT v36 before the fix: the tab strip said "Google", the URL bar
// said "en.wikipedia.org/wiki/Octopus", and the pixels showed Wikipedia — a
// three-way desync, with the video track still readyState:"live", muted:false
// and ZERO console errors (only a downstream stalled-RTP watchdog warning).
// That silence is why these tests assert on the ACTIVATION CALL itself rather
// than on any error signal: there was none to observe.
//
// activateTabInChrome (manager.go's SwitchTab) is the ONLY BringToFront call
// left on the tab-switch path (ADR-061: the JPEG screencast path, which used
// to call page.BringToFront() itself before every StartScreencast, is gone —
// video is carried exclusively by WebRTC now). These tests pin the guarantee
// that SwitchTab still tells Chrome itself which tab is active, so the
// WebRTC capture's chrome.tabs.query({active:true}) resolution can't desync
// from it.

// recordingActivator is a test double for the tabFocusFn seam that records
// every context it was asked to focus, in order, together with which half of
// the treatment that call carried.
type recordingActivator struct {
	mu        sync.Mutex
	ctxs      []context.Context
	treatment []string
	err       error
}

func (r *recordingActivator) fn(tabCtx context.Context, actions ...chromedp.Action) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ctxs = append(r.ctxs, tabCtx)
	r.treatment = append(r.treatment, focusTreatment(actions))
	return r.err
}

// calls returns the contexts brought to the FOREGROUND, in order — the
// "activations" every test in this file was written about. Release-of-focus
// calls on the tab being left (review finding F9) are reported separately by
// blurCalls so they cannot be miscounted as activations.
func (r *recordingActivator) calls() []context.Context {
	return r.filtered("foreground")
}

func (r *recordingActivator) blurCalls() []context.Context {
	return r.filtered("background")
}

func (r *recordingActivator) filtered(want string) []context.Context {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]context.Context, 0, len(r.ctxs))
	for i, c := range r.ctxs {
		if r.treatment[i] == want {
			out = append(out, c)
		}
	}
	return out
}

// newManagerWithRecordedActivation builds a fake-tab manager whose tab
// activation is recorded rather than dispatched to real CDP.
func newManagerWithRecordedActivation(t *testing.T) (*BrowserManager, *recordingActivator) {
	t.Helper()
	m := newTestManagerWithFakeTabs(t)
	rec := &recordingActivator{}
	m.tabFocusFn = rec.fn
	return m, rec
}

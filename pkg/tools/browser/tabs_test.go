package browser

// tabs_test.go — ADR-041 D1/D2/D3 unit coverage for the tab-set model:
// add/switch/close/neighbor-activation, the FR-060 memory gate on both
// Session() and adoption, Session(default) following the active tab,
// ReconcileTabs adopting a newly-detected target, and the
// never-zero-tabs invariant. Every test here uses BrowserManager.createTabFn
// (a test seam mirroring evalCDP/listTargets' exact rationale — see its doc
// comment in manager.go) to fabricate tabs without a real Chromium/CDP
// connection, so these run in any environment, including this devpod which
// has no Chromium binary.

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeTabFactory returns a BrowserManager.createTabFn stand-in that
// fabricates a tabEntry with a real (but never-dialed) chromedp context —
// chromedp.NewContext allocates no browser and dials nothing; only Run does,
// and this fake path never calls Run — instead of an ordinary
// context.WithCancel context. installTargetListenerLocked's
// chromedp.ListenTarget call panics with ErrInvalidContext on anything that
// isn't FromContext-recognizable (see live_deadlock_test.go's identical
// tabCtx, tabCancel := chromedp.NewContext(...) precedent), so this fake
// factory must use the real constructor even though it never talks to CDP.
// Each call gets a fresh, incrementing fake target ID unless targetID is
// already non-empty (the ADR-041 D2 adoption path), in which case the
// requested ID is preserved — mirroring createTab's real behavior of
// attaching to the CALLER-supplied target.
func fakeTabFactory() (fn func(allocCtx context.Context, targetID target.ID) (*tabEntry, error), canceledCount *int32) {
	var n int64
	canceled := new(int32)
	fn = func(allocCtx context.Context, targetID target.ID) (*tabEntry, error) {
		ctx, cancel := chromedp.NewContext(context.Background())
		id := targetID
		if id == "" {
			id = target.ID(fmt.Sprintf("fake-target-%d", atomic.AddInt64(&n, 1)))
		}
		return &tabEntry{
			ctx: ctx,
			cancel: func() {
				atomic.AddInt32(canceled, 1)
				cancel()
			},
			targetID: id,
		}, nil
	}
	return fn, canceled
}

// newTestManagerWithFakeTabs builds a manager with no real Chromium behind it.
//
// It takes no tab cap: ADR-075 D1.5a deleted every counter, and the only limit
// is the FR-060 memory gate. A test that needs the gate to refuse installs
// refuseTabsAtOrAbove (or unmeasurableHost) on m.memoryPressureFn — the seam
// that replaced the cap argument this helper used to take.
func newTestManagerWithFakeTabs(t *testing.T) *BrowserManager {
	t.Helper()
	cfg, err := DefaultConfig()
	require.NoError(t, err)
	m := &BrowserManager{
		cfg:      cfg,
		key:      testKey,
		sessions: make(map[string]*sessionEntry),
		started:  true, // skip ensureStarted's real Chromium launch
	}
	// A tool call reaches mgr.Live() through controlledResult, so a manager
	// built as a struct literal needs its live-view registry wired the same way
	// NewBrowserManager wires one. Without it every tool-level test in this
	// package panics on a nil registry the moment the control-lock gate runs.
	m.live = newLiveViewRegistry(m)
	fn, _ := fakeTabFactory()
	m.createTabFn = fn
	// Fake tabs are chromedp contexts with no CDP connection behind them, so
	// SwitchTab's real Page.bringToFront would block until PageTimeout. Same
	// rationale as createTabFn — see tabFocusFn's doc comment.
	m.tabFocusFn = func(context.Context, ...chromedp.Action) error { return nil }
	return m
}

// TestApplyReconcileOutcome_AdoptedVsUnadoptedVsNoop pins the three
// result-map shapes applyReconcileOutcome (tools.go) produces from a
// BrowserManager.ReconcileOutcome — the mapping ClickTool.Execute relies on
// to surface ADR-041 fix F2's unadopted signal to the agent.
func TestApplyReconcileOutcome_AdoptedVsUnadoptedVsNoop(t *testing.T) {
	t.Run("adopted", func(t *testing.T) {
		tab := Tab{Index: 2, URL: "https://cal.com/booking", Active: true}
		result := map[string]any{"success": true}
		applyReconcileOutcome(result, ReconcileOutcome{Adopted: true, NewActive: &tab})
		assert.Equal(t, true, result["opened_new_tab"])
		assert.Equal(t, 2, result["new_tab_index"])
		assert.Equal(t, "https://cal.com/booking", result["new_tab_url"])
		assert.Nil(t, result["tab_opened_but_not_adopted"])
	})

	t.Run("unadopted_memory_pressure", func(t *testing.T) {
		result := map[string]any{"success": true}
		applyReconcileOutcome(result, ReconcileOutcome{Unadopted: true, Reason: tabAdoptReasonMemoryPressure})
		assert.Equal(t, true, result["tab_opened_but_not_adopted"])
		assert.Equal(t, "memory_pressure", result["reason"])
		assert.Contains(t, result["note"], "browser_close_tab")
		// FR-063: name a remedy that exists; name no limit and no config key.
		note, isString := result["note"].(string)
		require.True(t, isString, "the note must be a string the model can read")
		assert.Contains(t, strings.ToLower(note), "memory")
		assert.NotContains(t, strings.ToLower(note), deletedTabCapConfigKey)
		assert.NotContains(t, strings.ToLower(note), "tools.browser")
		assert.Nil(t, result["opened_new_tab"])
	})

	// FR-063's own guard: a reason code with no arm of its own must NOT fall
	// through to the default "it could not be adopted" text, because that text
	// suggests nothing and the model retries the same open in a loop.
	t.Run("every_reason_code_has_its_own_arm", func(t *testing.T) {
		for _, reason := range []tabAdoptReason{tabAdoptReasonMemoryPressure, tabAdoptReasonAttachFailed} {
			result := map[string]any{"success": true}
			applyReconcileOutcome(result, ReconcileOutcome{Unadopted: true, Reason: reason})
			note, _ := result["note"].(string)
			assert.NotEmpty(t, note, "reason %q reached no arm of its own", reason)
		}
	})

	t.Run("unadopted_attach_failed", func(t *testing.T) {
		result := map[string]any{"success": true}
		applyReconcileOutcome(result, ReconcileOutcome{Unadopted: true, Reason: tabAdoptReasonAttachFailed})
		assert.Equal(t, true, result["tab_opened_but_not_adopted"])
		assert.Equal(t, "attach_failed", result["reason"])
		assert.Nil(t, result["opened_new_tab"])
	})

	t.Run("noop", func(t *testing.T) {
		result := map[string]any{"success": true}
		applyReconcileOutcome(result, ReconcileOutcome{})
		assert.Nil(t, result["opened_new_tab"])
		assert.Nil(t, result["tab_opened_but_not_adopted"])
		assert.Nil(t, result["note"])
	})

	// Second-fix-wave regression: a single click can spawn two new targets in
	// one go — one adopted, one stranded. Both keys must appear together;
	// before the fix an if/else-if here reported only the Adopted case.
	t.Run("adopted_and_unadopted_together", func(t *testing.T) {
		tab := Tab{Index: 1, URL: "https://example.com/a", Active: true}
		result := map[string]any{"success": true}
		applyReconcileOutcome(result, ReconcileOutcome{
			Adopted: true, NewActive: &tab,
			Unadopted: true, Reason: tabAdoptReasonMemoryPressure, UnadoptedCount: 1,
		})
		assert.Equal(t, true, result["opened_new_tab"], "adoption must still be reported")
		assert.Equal(t, 1, result["new_tab_index"])
		assert.Equal(t, true, result["tab_opened_but_not_adopted"], "the stranded tab must ALSO be reported")
		assert.Equal(t, "memory_pressure", result["reason"])
		assert.NotEmpty(t, result["note"])
	})
}

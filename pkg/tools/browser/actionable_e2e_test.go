package browser

// actionable_e2e_test.go — the four-condition gate against a REAL page.
//
// These are the tests that decide whether the gate is worth its cost. The unit
// tests prove the ordering rule and the wording; only a real renderer proves
// that an overlay actually reads as "hit-testable", that an animating element
// actually reads as "not stable", and that a closed shadow root degrades to a
// recorded pass rather than a hard failure.
//
// Gated by skipIfNoBrowser like the rest of the package's E2E suites.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/require"
)

const gateFixtureHTML = `<!doctype html><html><head><style>
  body { margin: 0; font-family: sans-serif; }
  #ok { position: absolute; top: 20px; left: 20px; width: 120px; height: 40px; }
  #covered { position: absolute; top: 100px; left: 20px; width: 120px; height: 40px; }
  #cookie-banner { position: absolute; top: 90px; left: 0; width: 400px; height: 60px;
                   background: #333; color: #fff; z-index: 10; }
  #hidden { display: none; }
  /* padding and border zeroed deliberately: a <button> with only width:0
     and height:0 still has a NON-zero border box from its UA defaults, so
     without these it is genuinely visible and the gate is right to pass it. */
  #zerobox { width: 0; height: 0; padding: 0; border: 0; overflow: hidden; }
  /* #mover is driven by requestAnimationFrame (see the script at the bottom of
     this fixture), NOT by a CSS animation. Do not "simplify" it back to
     'animation: slide 1s linear infinite' — that fixture aliased and made this
     suite intermittently red. See moverStepPx's comment for the measurement. */
  #mover { position: absolute; top: 200px; left: 0; width: 100px; height: 30px; }
  #under-closed { position: absolute; top: 300px; left: 20px; width: 120px; height: 40px; }
  #closed-host { position: absolute; top: 300px; left: 20px; width: 120px; height: 40px; z-index: 5; }
  #under-frame { position: absolute; top: 400px; left: 20px; width: 120px; height: 40px; }
  #frame-overlay { position: absolute; top: 400px; left: 20px; width: 120px; height: 40px;
                   z-index: 5; border: 0; }
</style></head><body>
  <button id="ok">Ready</button>
  <button id="covered">Underneath</button>
  <div id="cookie-banner">We use cookies</div>
  <button id="hidden">Never rendered</button>
  <button id="zerobox">No box</button>
  <button id="mover">Moving</button>
  <button id="disabled-btn" disabled style="position:absolute;top:150px;left:20px;">Disabled</button>
  <button id="aria-off" aria-disabled="true" style="position:absolute;top:150px;left:160px;">Aria disabled</button>
  <!-- The target sits UNDER a custom element whose shadow root is CLOSED.
       elementFromPoint returns the host and cannot be descended past it, so
       whether the click would reach #under-closed is genuinely unknowable —
       which is what "indeterminate" means. (Targeting the host itself would
       be a determinate "self", not this case.) -->
  <button id="under-closed">Beneath a closed root</button>
  <closed-shadow id="closed-host"></closed-shadow>
  <!-- Same shape for the cross-document case: the top document's
       elementFromPoint resolves the iframe ELEMENT, never anything inside it. -->
  <button id="under-frame">Beneath a frame</button>
  <iframe id="frame-overlay" srcdoc="&lt;body style='margin:0;background:#fc8'&gt;&lt;/body&gt;"></iframe>
  <script>
    // #mover advances a FIXED number of pixels on every animation frame,
    // rather than a fixed number of pixels per unit of WALL-CLOCK TIME.
    //
    // This is not a stylistic preference, it is the whole reason the element
    // works as a fixture. The gate proves CondStable by comparing the box read
    // in RT1 against the box read inside a requestAnimationFrame callback in
    // RT2 — i.e. exactly ONE FRAME apart. A CSS 'animation: slide 1s linear
    // infinite' moves the element as a function of the animation timeline, and
    // Blink samples that timeline at the LAST PRODUCED FRAME's time, so one
    // frame of motion is '300px x (frame interval / 1s)'. On a fast foreground
    // tab (60Hz) that is ~5px and the fixture works. On a tab that is not
    // foregrounded it does not: this project has already measured such a tab
    // compositing at roughly one frame every one-to-two seconds (see
    // capture_session.go's reassertForegroundAsync), and one second is exactly
    // the animation's period. The motion per frame then collapses to nothing —
    // measured against this project's own Chrome at 1s and 2s sampling gaps,
    // the box moved by between 0.000px and 0.031px and was byte-identical on
    // 2 of 18 samples. An identical box is precisely what gateProbe.sameBox
    // reports as STABLE, so the gate passed an element that never stops
    // moving and TestWaitActionable_StabilityOneFrameApart went red. Chrome's
    // anti-backgrounding flags do not close this: exec_resolver.go already
    // sets all three and the same note records that a background tab still
    // composites at ~0.5fps regardless.
    //
    // Stepping per FRAME removes the dependency on frame rate entirely: one
    // frame of motion is moverStepPx, always. The step and the wrap are
    // coprime (7 and 300), so consecutive frames can never land on the same
    // left offset. Measured at 60Hz: 60/60 gate RT1/RT2 pairs differed, with a
    // minimum delta of exactly 7.000px and zero zero-deltas across 30
    // consecutive frames.
    //
    // Ordering is what makes RT2 see the NEXT frame's position: this callback
    // re-registers itself from inside the callback, so it is queued for frame
    // N+1 before the gate's own probe callback is registered, and callbacks run
    // in registration order within a frame. Verified, not assumed: the gate
    // refused this element on CondStable 15 times out of 15.
    (function(){
      var moverEl = document.getElementById('mover');
      var moverStepPx = 7;   // coprime with the wrap below
      var moverWrapPx = 300; // same travel the old CSS keyframes covered
      var moverX = 0;
      function stepMover(){
        moverX = (moverX + moverStepPx) % moverWrapPx;
        moverEl.style.left = moverX + 'px';
        window.requestAnimationFrame(stepMover);
      }
      window.requestAnimationFrame(stepMover);
    })();

    customElements.define('closed-shadow', class extends HTMLElement {
      constructor() {
        super();
        const r = this.attachShadow({mode: 'closed'});
        r.innerHTML = '<div style="width:120px;height:40px;background:#8cf">inside</div>';
      }
    });
  </script>
</body></html>`

func gateFixtureServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(gateFixtureHTML))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// gateFixtureTab navigates a real browser to the fixture and returns its tab
// context.
func gateFixtureTab(t *testing.T) (context.Context, *BrowserManager) {
	t.Helper()
	srv := gateFixtureServer(t)
	registry, mgr := newPermissiveRegistry(t, testBrowserCfg(t))

	nav := mustGetTool(t, registry, "browser_navigate")
	res := nav.Execute(context.Background(), map[string]any{"url": srv.URL})
	require.False(t, res.IsError, "navigate must succeed; got: %s", res.ForLLM)

	tabCtx, err := mgr.Session(testSessionID)
	require.NoError(t, err)
	return tabCtx, mgr
}

// TestWaitActionable_AllFourConditions is the table the whole design exists
// for: each row is an element that fails exactly one condition, and the error
// must name THAT condition — not "context deadline exceeded", which is equally
// true of all of them and tells an agent nothing about what to do next.
func TestWaitActionable_AllFourConditions(t *testing.T) {
	skipIfNoBrowser(t)
	tabCtx, _ := gateFixtureTab(t)

	cases := []struct {
		name     string
		selector string
		want     ActionCondition
		detail   string
	}{
		{"display:none", "#hidden", CondVisible, ""},
		{"zero-sized box", "#zerobox", CondVisible, ""},
		{"disabled attribute", "#disabled-btn", CondEnabled, ""},
		{"aria-disabled", "#aria-off", CondEnabled, ""},
		{"covered by an overlay", "#covered", CondHitTestable, "cookie-banner"},
		{"animating", "#mover", CondStable, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := waitActionable(tabCtx, "browser_click", tc.selector, tc.selector, 1200*time.Millisecond)
			if err == nil {
				t.Fatalf("%s must not pass the gate", tc.selector)
			}
			var na *ErrNotActionable
			if !errors.As(err, &na) {
				t.Fatalf("want *ErrNotActionable, got %T: %v", err, err)
			}
			if na.Failed != tc.want {
				t.Errorf("%s: gate named %q, want %q (full error: %s)", tc.selector, na.Failed, tc.want, err)
			}
			if tc.detail != "" && !strings.Contains(err.Error(), tc.detail) {
				t.Errorf("%s: the error must name what is in the way (%q); got %q", tc.selector, tc.detail, err)
			}
			if strings.Contains(err.Error(), "context deadline exceeded") {
				t.Errorf("%s: a bare timeout is what this replaces; got %q", tc.selector, err)
			}
		})
	}

	// The control: an ordinary, clickable button passes, and fast.
	t.Run("plain button passes", func(t *testing.T) {
		start := time.Now()
		if err := waitActionable(tabCtx, "browser_click", "#ok", "#ok", 5*time.Second); err != nil {
			t.Fatalf("a plainly clickable button must pass the gate; got %v", err)
		}
		if d := time.Since(start); d > 2*time.Second {
			t.Errorf("the gate took %s on the fast path; US-4's claim is that it is not a tax", d)
		}
	})
}

// TestWaitActionable_IndeterminateHitTestPasses — a hit test that cannot be
// PERFORMED is not evidence the click is wrong. A closed shadow root cannot be
// descended, so it degrades to a recorded pass rather than a hard failure —
// with the counter incremented, so an operator can see it happened.
func TestWaitActionable_IndeterminateHitTestPasses(t *testing.T) {
	skipIfNoBrowser(t)
	tabCtx, _ := gateFixtureTab(t)

	for _, tc := range []struct {
		name     string
		selector string
	}{
		{"under a closed shadow root", "#under-closed"},
		{"under a cross-document frame", "#under-frame"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := gateIndeterminateTotal()
			out, err := waitActionableOutcome(tabCtx, "browser_click", tc.selector, tc.selector, 2*time.Second)
			if err != nil {
				t.Fatalf("%s must not FAIL the gate — an un-performable hit test is not evidence the "+
					"click is wrong; got %v", tc.name, err)
			}
			if out.HitTest != hitTestIndeterminate {
				t.Errorf("hit_test = %q, want %q — the pass must be visible, never silent", out.HitTest, hitTestIndeterminate)
			}
			if gateIndeterminateTotal() != before+1 {
				t.Error("an indeterminate hit test must increment its counter, or nobody can tell how " +
					"often the check is not being performed")
			}
		})
	}

	// The control: an element the hit test CAN answer must not be reported as
	// indeterminate, or the field means nothing.
	t.Run("a plain element is determinate", func(t *testing.T) {
		out, err := waitActionableOutcome(tabCtx, "browser_click", "#ok", "#ok", 3*time.Second)
		if err != nil {
			t.Fatalf("unexpected: %v", err)
		}
		if out.HitTest == hitTestIndeterminate {
			t.Error("a plainly hit-testable button must not read as indeterminate")
		}
	})
}

// TestWaitActionable_IncrementsFailureCounters — FR-032, per condition.
func TestWaitActionable_IncrementsFailureCounters(t *testing.T) {
	skipIfNoBrowser(t)
	tabCtx, _ := gateFixtureTab(t)

	before := gateFailureTotal(CondEnabled)
	_ = waitActionable(tabCtx, "browser_click", "#disabled-btn", "#disabled-btn", 600*time.Millisecond)
	if gateFailureTotal(CondEnabled) != before+1 {
		t.Errorf("the %q failure counter did not move", CondEnabled)
	}
	// And it did NOT move a different condition's counter.
	beforeHit := gateFailureTotal(CondHitTestable)
	_ = waitActionable(tabCtx, "browser_click", "#disabled-btn", "#disabled-btn", 600*time.Millisecond)
	if gateFailureTotal(CondHitTestable) != beforeHit {
		t.Error("a disabled element must not be counted against hit-testable — per-condition means per-condition")
	}
}

// TestWaitActionable_FastPathRoundTripCount — FR-007. Exactly two
// Runtime.evaluate round trips inside the gate on the fast path, and the
// counter is scoped to the GATE, never to the tool: chromedp.Click issues its
// own DOM.getBoxModel, DOM.resolveNode, DOM.scrollIntoViewIfNeeded,
// DOM.getContentQuads and two Input.dispatchMouseEvent AFTER the gate returns,
// so a counter wrapping browser_click would assert something false.
func TestWaitActionable_FastPathRoundTripCount(t *testing.T) {
	skipIfNoBrowser(t)
	tabCtx, _ := gateFixtureTab(t)

	armGateEvalCounter()
	err := waitActionable(tabCtx, "browser_click", "#ok", "#ok", 5*time.Second)
	n := disarmGateEvalCounter()
	if err != nil {
		t.Fatalf("fast path must pass: %v", err)
	}
	if n != 2 {
		t.Fatalf("the gate issued %d Runtime.evaluate round trips on the fast path, want exactly 2 "+
			"(RT1 reads the box, enabled and hit test in one pass; RT2 re-reads the box after one "+
			"animation frame for stability). A third means a probe was added.", n)
	}
}

// TestActionabilityGate_RevertSwitchIsLive — FR-034. Flipping the setting
// changes behaviour with no restart, which is the entire reason the switch
// exists: an operator whose site regresses needs something to turn NOW.
func TestActionabilityGate_RevertSwitchIsLive(t *testing.T) {
	skipIfNoBrowser(t)
	tabCtx, _ := gateFixtureTab(t)
	t.Cleanup(func() { SetActionabilityGate(ActionabilityGateFull) })

	// Under the full gate, the covered button fails on hit-testability.
	SetActionabilityGate(ActionabilityGateFull)
	err := waitActionable(tabCtx, "browser_click", "#covered", "#covered", 900*time.Millisecond)
	var na *ErrNotActionable
	if !errors.As(err, &na) || na.Failed != CondHitTestable {
		t.Fatalf("full gate must reject the covered button on hit-testability; got %v", err)
	}

	// Flip it. No restart, no re-wiring: the same element now passes, because
	// visible_only is the pre-change behaviour verbatim.
	SetActionabilityGate(ActionabilityGateVisibleOnly)
	if err := waitActionable(tabCtx, "browser_click", "#covered", "#covered", 2*time.Second); err != nil {
		t.Fatalf("visible_only is the OLD behaviour verbatim — a covered but visible element passed before "+
			"this change and must pass under the revert; got %v", err)
	}
}

// TestWaitActionable_StabilityOneFrameApart — CondStable is proven across RT1
// and RT2 and nowhere else. An element animating continuously never presents
// two identical boxes one frame apart, and that is what the gate detects.
func TestWaitActionable_StabilityOneFrameApart(t *testing.T) {
	skipIfNoBrowser(t)
	tabCtx, _ := gateFixtureTab(t)

	err := waitActionable(tabCtx, "browser_click", "#mover", "#mover", 1200*time.Millisecond)
	var na *ErrNotActionable
	if !errors.As(err, &na) {
		t.Fatalf("an element that never stops moving must fail the gate; got %v", err)
	}
	if na.Failed != CondStable {
		t.Errorf("gate named %q, want %q", na.Failed, CondStable)
	}
	// The scope of the guarantee, stated as a test rather than left to be
	// discovered: a STATIC element is stable, so the check is not simply
	// always-false.
	if err := waitActionable(tabCtx, "browser_click", "#ok", "#ok", 3*time.Second); err != nil {
		t.Fatalf("a static element must read as stable, or CondStable rejects everything; got %v", err)
	}
}

// TestGateFixture_MoverAdvancesEveryFrame guards the FIXTURE, not the gate.
//
// TestWaitActionable_StabilityOneFrameApart can only prove anything if #mover
// genuinely presents a different box on consecutive animation frames. When the
// fixture animated `left` on a 1-second CSS timeline that stopped being true
// the moment the tab's frame rate fell near that period, and the suite went
// intermittently red for a reason no assertion in it could name. A wall-clock
// animation would pass this file's other tests on a fast machine and reproduce
// that failure on a slow one, so the invariant is asserted here directly.
//
// The oracle comes from the fixture's specification (advance moverStepPx=7 per
// frame, wrapping at 300), NOT from whatever the browser happens to report: a
// per-frame delta must be congruent to +7 (mod 300) — that is +7, or 7-300 =
// -293 on the frame that wraps — and must never be 0. A time-based animation
// fails this immediately: its per-frame delta tracks the frame interval, so it
// is ~5px at 60Hz, a different value at 30Hz, and ~0 under throttling.
func TestGateFixture_MoverAdvancesEveryFrame(t *testing.T) {
	skipIfNoBrowser(t)
	tabCtx, _ := gateFixtureTab(t)

	// Sample #mover's box on 31 consecutive real animation frames, in-page, so
	// the sampling cadence IS the frame cadence — the same relationship the
	// gate's RT1/RT2 pair has.
	const samples = 31
	var xs []float64
	script := fmt.Sprintf(`new Promise(function(resolve){
		var xs = [];
		function tick(){
			xs.push(document.querySelector('#mover').getBoundingClientRect().x);
			if (xs.length < %d) { window.requestAnimationFrame(tick); }
			else { resolve(xs); }
		}
		window.requestAnimationFrame(tick);
	})`, samples)
	require.NoError(t, chromedp.Run(tabCtx, chromedp.Evaluate(script, &xs,
		func(p *runtime.EvaluateParams) *runtime.EvaluateParams {
			return p.WithAwaitPromise(true)
		})), "sampling #mover across consecutive frames must succeed")
	require.Len(t, xs, samples, "the in-page sampler must return one reading per frame")

	for i := 1; i < len(xs); i++ {
		d := xs[i] - xs[i-1]
		if d == 0 {
			t.Fatalf("frame %d: #mover did not move at all (x stayed %.4f). The gate proves "+
				"CondStable by comparing two boxes ONE FRAME apart, so a fixture that can "+
				"present the same box on consecutive frames cannot prove anything.", i, xs[i])
		}
		// +7 normally, 7-300 on the wrapping frame. Anything else means the
		// motion is a function of elapsed TIME rather than of frames.
		if d != 7 && d != 7-300 {
			t.Fatalf("frame %d: #mover advanced %.4fpx, want exactly +7 (or -293 when it wraps). "+
				"A per-frame step is what makes this fixture independent of the tab's frame "+
				"rate; a time-based animation (CSS keyframes, a setTimeout loop) produces a "+
				"frame-interval-dependent delta and collapses to ~0 on a throttled tab.", i, d)
		}
	}
}

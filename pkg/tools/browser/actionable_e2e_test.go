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
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const gateFixtureHTML = `<!doctype html><html><head><style>
  body { margin: 0; font-family: sans-serif; }
  #ok { position: absolute; top: 20px; left: 20px; width: 120px; height: 40px; }
  #covered { position: absolute; top: 100px; left: 20px; width: 120px; height: 40px; }
  #cookie-banner { position: absolute; top: 90px; left: 0; width: 400px; height: 60px;
                   background: #333; color: #fff; z-index: 10; }
  #hidden { display: none; }
  #zerobox { width: 0; height: 0; overflow: hidden; }
  #mover { position: absolute; top: 200px; left: 0; width: 100px; height: 30px;
           animation: slide 1s linear infinite; }
  @keyframes slide { from { left: 0px; } to { left: 300px; } }
  #closed-host { position: absolute; top: 300px; left: 20px; width: 120px; height: 40px; }
</style></head><body>
  <button id="ok">Ready</button>
  <button id="covered">Underneath</button>
  <div id="cookie-banner">We use cookies</div>
  <button id="hidden">Never rendered</button>
  <button id="zerobox">No box</button>
  <button id="mover">Moving</button>
  <button id="disabled-btn" disabled style="position:absolute;top:150px;left:20px;">Disabled</button>
  <button id="aria-off" aria-disabled="true" style="position:absolute;top:150px;left:160px;">Aria disabled</button>
  <closed-shadow id="closed-host"></closed-shadow>
  <script>
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

	before := gateIndeterminateTotal()
	out, err := waitActionableOutcome(tabCtx, "browser_click", "#closed-host", "#closed-host", 2*time.Second)
	if err != nil {
		t.Fatalf("a closed shadow root must not FAIL the gate — it is not evidence the click is wrong; got %v", err)
	}
	if out.HitTest != hitTestIndeterminate {
		t.Errorf("hit_test = %q, want %q — the pass must be visible, never silent", out.HitTest, hitTestIndeterminate)
	}
	if gateIndeterminateTotal() != before+1 {
		t.Error("an indeterminate hit test must increment its counter, or nobody can tell how often the check is not being performed")
	}
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

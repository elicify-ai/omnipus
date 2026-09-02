package browser

// actionable.go — the actionability gate (ADR-072 D2, spec §3).
//
// The problem it exists to solve: before this, a click that could not land
// said "context deadline exceeded". That sentence tells an agent nothing it
// can act on — it cannot tell "the button is not there" from "the button is
// there but a cookie banner is on top of it" from "the button is there and
// disabled". Each of those has a different next move, and the agent was
// choosing between them blind.
//
// The gate replaces that with a CLOSED set of four conditions, checked in a
// fixed order, and an error that names WHICH one was never met:
//
//	visible -> stable -> enabled -> hit-testable
//
// The order is not cosmetic. A later condition is meaningless while an
// earlier one is false (an element with no box has no centre to hit-test),
// so the reported condition is the FIRST one that never became true within
// the budget, not the last one observed failing.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

// ActionCondition is the CLOSED set the gate reports on failure. ADR-072
// criterion 7 requires the error to name which condition was unmet; a closed
// set is what makes that testable rather than prose.
type ActionCondition string

const (
	// CondVisible — rendered, with a non-zero box.
	CondVisible ActionCondition = "visible"
	// CondStable — two box reads one animation frame apart are identical.
	CondStable ActionCondition = "stable"
	// CondEnabled — not [disabled], not aria-disabled="true".
	CondEnabled ActionCondition = "enabled"
	// CondHitTestable — the node under the element's centre point is this
	// node or a descendant of it.
	CondHitTestable ActionCondition = "hit-testable"
)

// actionConditionOrder is the evaluation order, and the ONLY place the set is
// enumerated. TestActionCondition_SetIsExactlyFour asserts against it so a
// fifth condition cannot be added without a test failure — the closed set is
// the requirement, not an implementation detail.
var actionConditionOrder = []ActionCondition{CondVisible, CondStable, CondEnabled, CondHitTestable}

// hitTestIndeterminate is the value the structured hit_test result field
// carries when the hit test could not be PERFORMED — a closed shadow root, or
// a cross-origin frame. It passes the gate: neither is evidence the click is
// wrong. It is never silent, which is the point; see gateIndeterminateCount.
const hitTestIndeterminate = "indeterminate"

// ErrNotActionable is the ONLY error type the gate returns on timeout.
//
// It keeps the Err prefix rather than the XxxError form the errname linter
// wants. The name is fixed verbatim by the shared D2 interface contract every
// other browser stream codes against, so renaming it would break those call
// sites to satisfy a naming convention.
//
// The suppression below is ONE line on purpose: gofmt treats //nolint as a
// directive and moves it to the end of the comment block, which tears a
// multi-line rationale off its own opening clause and leaves it scrambled.
//
//nolint:errname // Name fixed by the shared D2 interface contract; see above.
type ErrNotActionable struct {
	// Failed is the FIRST condition, in actionConditionOrder, that never
	// became true within the budget.
	Failed ActionCondition
	// Display is the user-facing locator (displayLocator) — never the
	// internal marker selector.
	Display string
	// Detail is the human-readable reason, e.g. the occluding element for
	// CondHitTestable.
	Detail string
	// Tool is the calling tool, e.g. "browser_click".
	Tool string
}

func (e *ErrNotActionable) Error() string {
	if e.Detail == "" {
		return fmt.Sprintf("%s: element %q is not actionable: %s", e.Tool, e.Display, e.Failed)
	}
	return fmt.Sprintf("%s: element %q is not actionable: %s (%s)", e.Tool, e.Display, e.Failed, e.Detail)
}

// ---------------------------------------------------------------------------
// FR-034 — the revert switch
// ---------------------------------------------------------------------------

const (
	// ActionabilityGateFull runs all four conditions. The default.
	ActionabilityGateFull = "full"
	// ActionabilityGateVisibleOnly runs the pre-change chromedp.WaitVisible
	// behaviour, verbatim. It exists ONLY so an operator whose site regresses
	// under the stricter gate has something to turn while the regression is
	// diagnosed, and it is expected to be DELETED in the same change that
	// acts on the gate-failure counters below.
	ActionabilityGateVisibleOnly = "visible_only"
)

// actionabilityGate holds the live tools.browser.actionability_gate value.
// One chokepoint, read INSIDE waitActionable, so there is no second branch at
// any call site and no restart is needed to flip it.
var actionabilityGate atomic.Value // string

// SetActionabilityGate pushes the live tools.browser.actionability_gate value
// into the gate. Called from the agent loop's browser wiring on first seed and
// on every config reload, mirroring tools.SetPreviewAllLazy.
//
// An unrecognised or empty value means "full" — a typo must not silently
// weaken the gate.
func SetActionabilityGate(v string) {
	if strings.TrimSpace(v) != ActionabilityGateVisibleOnly {
		v = ActionabilityGateFull
	}
	actionabilityGate.Store(strings.TrimSpace(v))
}

func currentActionabilityGate() string {
	if v, ok := actionabilityGate.Load().(string); ok && v == ActionabilityGateVisibleOnly {
		return ActionabilityGateVisibleOnly
	}
	return ActionabilityGateFull
}

// ---------------------------------------------------------------------------
// FR-032 — per-condition telemetry
// ---------------------------------------------------------------------------

// gateFailureCount counts gate failures per condition, and
// gateIndeterminateCount counts hit tests that could not be performed. They
// are package-local atomics with a settable recorder alongside, rather than a
// direct Prometheus registration: pkg/tools/browser has no metrics dependency
// today and the spec assigns no gateway edit site for these names. The
// recorder seam is what lets a later change export them as
// omnipus_browser_gate_failure_total{condition} and
// omnipus_browser_gate_indeterminate_total without touching this file's logic.
var (
	gateFailureCount      [4]atomic.Uint64
	gateIndeterminateHits atomic.Uint64

	// gateMetricRecorder, when set, is called on every failure (with the
	// condition) and on every indeterminate hit test (with
	// hitTestIndeterminate). nil by default; never called under any lock.
	gateMetricRecorder atomic.Value // func(string)
)

// SetGateMetricRecorder installs the sink for the FR-032 counters. Safe to
// call at any time; passing nil is not supported (use a no-op func).
func SetGateMetricRecorder(f func(string)) {
	if f == nil {
		return
	}
	gateMetricRecorder.Store(f)
}

func recordGateMetric(label string) {
	if f, ok := gateMetricRecorder.Load().(func(string)); ok && f != nil {
		f(label)
	}
}

func conditionIndex(c ActionCondition) int {
	for i, k := range actionConditionOrder {
		if k == c {
			return i
		}
	}
	return -1
}

func noteGateFailure(c ActionCondition) {
	if i := conditionIndex(c); i >= 0 {
		gateFailureCount[i].Add(1)
	}
	recordGateMetric(string(c))
}

func noteGateIndeterminate() {
	gateIndeterminateHits.Add(1)
	recordGateMetric(hitTestIndeterminate)
}

// gateFailureTotal reads the per-condition failure counter (test affordance
// and the read side of the recorder seam).
func gateFailureTotal(c ActionCondition) uint64 {
	if i := conditionIndex(c); i >= 0 {
		return gateFailureCount[i].Load()
	}
	return 0
}

func gateIndeterminateTotal() uint64 { return gateIndeterminateHits.Load() }

// ---------------------------------------------------------------------------
// FR-007 — the round-trip counting seam
// ---------------------------------------------------------------------------

// gateEvalCount counts the Runtime.evaluate round trips issued INSIDE the
// gate. It is armed on waitActionable entry and disarmed on return, so it is
// scoped to the gate and never to the tool: chromedp.Click issues its own
// DOM.getBoxModel, DOM.resolveNode, DOM.scrollIntoViewIfNeeded,
// DOM.getContentQuads and two Input.dispatchMouseEvent AFTER the gate returns,
// and a counter wrapping browser_click would read those too and assert
// something false.
var (
	gateEvalCount  atomic.Int64
	gateEvalArmed  atomic.Bool
	gateEvalActive atomic.Bool
)

func armGateEvalCounter() {
	gateEvalCount.Store(0)
	gateEvalArmed.Store(true)
}

func disarmGateEvalCounter() int64 {
	gateEvalArmed.Store(false)
	return gateEvalCount.Load()
}

func countGateEval() {
	if gateEvalArmed.Load() && gateEvalActive.Load() {
		gateEvalCount.Add(1)
	}
}

// ---------------------------------------------------------------------------
// The gate
// ---------------------------------------------------------------------------

// gateProbe is the JSON shape the in-page probe returns. All of visible,
// enabled and hit-testable are computed IN-PAGE in one call, which is what
// keeps the fast path at two round trips rather than four.
type gateProbe struct {
	Found    bool    `json:"found"`
	X        float64 `json:"x"`
	Y        float64 `json:"y"`
	W        float64 `json:"w"`
	H        float64 `json:"h"`
	Visible  bool    `json:"visible"`
	Enabled  bool    `json:"enabled"`
	Hit      string  `json:"hit"`
	Occluder string  `json:"occluder"`
}

func (p gateProbe) sameBox(o gateProbe) bool {
	return p.X == o.X && p.Y == o.Y && p.W == o.W && p.H == o.H
}

// gateProbeJS computes the whole gate in one in-page pass.
//
// document.elementFromPoint is used rather than DOM.getNodeForLocation on
// purpose, and this note exists so it is not "simplified" back:
// getNodeForLocation is a separate round trip, needs a second
// DOM.describeNode to name the occluder, and cannot see into shadow roots.
// elementFromPoint gives the hit node, its tag and id, and the shadow-root
// descent, inside the same evaluate that reads the box.
const gateProbeJS = `(function(){
  var sel = %s;
  var raf = %s;
  function probe(){
    var el = document.querySelector(sel);
    if (!el) { return {found:false}; }
    var r = el.getBoundingClientRect();
    var visible = r.width > 0 && r.height > 0;
    try {
      var cs = window.getComputedStyle(el);
      if (cs && (cs.visibility === 'hidden' || cs.display === 'none')) { visible = false; }
    } catch (e) {}
    var enabled = !(el.matches('[disabled]') || el.getAttribute('aria-disabled') === 'true');
    var hit = 'indeterminate';
    var occluder = '';
    var cx = r.x + r.width / 2;
    var cy = r.y + r.height / 2;
    if (visible && cx >= 0 && cy >= 0 && cx <= window.innerWidth && cy <= window.innerHeight) {
      var node = null;
      try { node = document.elementFromPoint(cx, cy); } catch (e) { node = null; }
      var guard = 0;
      while (node && node.shadowRoot && guard++ < 32) {
        var inner = null;
        try { inner = node.shadowRoot.elementFromPoint(cx, cy); } catch (e) { inner = null; }
        if (!inner || inner === node) { break; }
        node = inner;
      }
      if (!node) {
        hit = 'indeterminate';
      } else if (node === el) {
        hit = 'self';
      } else if (el.contains(node)) {
        hit = 'descendant';
      } else if (node.tagName === 'IFRAME' || node.tagName === 'FRAME') {
        hit = 'indeterminate';
      } else if (node.tagName && node.tagName.indexOf('-') > 0 && node.shadowRoot === null) {
        hit = 'indeterminate';
      } else {
        hit = 'occluded';
        occluder = String(node.tagName || '').toLowerCase() + (node.id ? ('#' + node.id) : '');
      }
    }
    return {found:true, x:r.x, y:r.y, w:r.width, h:r.height,
            visible:visible, enabled:enabled, hit:hit, occluder:occluder};
  }
  if (!raf) { return Promise.resolve(probe()); }
  return new Promise(function(resolve){
    window.requestAnimationFrame(function(){ resolve(probe()); });
  });
})()`

func buildGateProbeJS(target string, afterFrame bool) (string, error) {
	selJSON, err := json.Marshal(target)
	if err != nil {
		return "", err
	}
	raf := "false"
	if afterFrame {
		raf = "true"
	}
	return fmt.Sprintf(gateProbeJS, selJSON, raf), nil
}

// runGateProbe issues exactly ONE Runtime.evaluate.
func runGateProbe(ctx context.Context, target string, afterFrame bool) (gateProbe, error) {
	script, err := buildGateProbeJS(target, afterFrame)
	if err != nil {
		return gateProbe{}, err
	}
	var out gateProbe
	countGateEval()
	err = chromedp.Run(ctx, chromedp.Evaluate(script, &out, func(p *runtime.EvaluateParams) *runtime.EvaluateParams {
		return p.WithAwaitPromise(true)
	}))
	return out, err
}

// gateOutcome is what a PASSING gate reports back to its caller.
type gateOutcome struct {
	// HitTest is "self", "descendant" or hitTestIndeterminate. Callers put
	// it on the tool result when it is indeterminate, so an operator can see
	// that the check could not be performed rather than assuming it passed.
	HitTest string
}

// waitActionable runs the four-condition gate. It is the seam every ACTION
// tool calls (click, type, select_option, press_key-with-target, hover,
// upload_file); NEVER a read-only tool (get_text, screenshot, wait, snapshot)
// and NEVER browser_handle_dialog, which is a recovery verb (FR-035).
func waitActionable(tabCtx context.Context, toolName, target, display string, timeout time.Duration) error {
	_, err := waitActionableOutcome(tabCtx, toolName, target, display, timeout)
	return err
}

// waitActionableOutcome is waitActionable with the structured result the
// hit_test field needs. waitActionable is the fixed-signature seam other units
// code against; this is the same single implementation, not a second one.
func waitActionableOutcome(
	tabCtx context.Context,
	toolName, target, display string,
	timeout time.Duration,
) (gateOutcome, error) {
	gateEvalActive.Store(true)
	defer gateEvalActive.Store(false)

	// FR-034: read the live revert switch HERE — one chokepoint, no second
	// branch anywhere else. visible_only is the pre-change behaviour verbatim.
	if currentActionabilityGate() == ActionabilityGateVisibleOnly {
		ctx, cancel := context.WithTimeout(tabCtx, timeout)
		defer cancel()
		if err := chromedp.Run(ctx, chromedp.WaitVisible(target, chromedp.ByQuery)); err != nil {
			if translated, ok := translatePostGateErr(err, toolName, display); ok {
				return gateOutcome{}, translated
			}
			return gateOutcome{}, err
		}
		return gateOutcome{HitTest: "self"}, nil
	}

	ctx, cancel := context.WithTimeout(tabCtx, timeout)
	defer cancel()

	everTrue := map[ActionCondition]bool{}
	var lastDetail = map[ActionCondition]string{}

	for {
		// RT1 — one Runtime.evaluate: box, visible, enabled, hit, occluder.
		first, err := runGateProbe(ctx, target, false)
		if err == nil && first.Found && first.Visible {
			everTrue[CondVisible] = true

			// RT2 — the same script, scheduled after one requestAnimationFrame
			// INSIDE the page, so the frame wait costs no extra round trip.
			second, serr := runGateProbe(ctx, target, true)
			if serr == nil && second.Found {
				if second.sameBox(first) {
					everTrue[CondStable] = true

					if second.Enabled {
						everTrue[CondEnabled] = true

						switch second.Hit {
						case "self", "descendant":
							return gateOutcome{HitTest: second.Hit}, nil
						case hitTestIndeterminate:
							// PASSES, but never silently: a closed shadow
							// root or a cross-origin frame is not evidence
							// the click is wrong.
							noteGateIndeterminate()
							return gateOutcome{HitTest: hitTestIndeterminate}, nil
						default:
							occ := second.Occluder
							if occ == "" {
								occ = "another element"
							}
							lastDetail[CondHitTestable] = "covered by " + occ
						}
					} else {
						lastDetail[CondEnabled] = "the element is disabled"
					}
				} else {
					lastDetail[CondStable] = "the element is still moving"
				}
			}
		} else if err == nil && first.Found {
			lastDetail[CondVisible] = "the element is present but has no rendered box"
		} else if err == nil {
			lastDetail[CondVisible] = "no element matches this locator"
		}

		select {
		case <-ctx.Done():
			failed, detail := firstUnmet(everTrue, lastDetail)
			noteGateFailure(failed)
			return gateOutcome{}, &ErrNotActionable{
				Failed:  failed,
				Display: display,
				Detail:  detail,
				Tool:    toolName,
			}
		case <-time.After(textResolvePollInterval):
		}
	}
}

// firstUnmet returns the FIRST condition, in actionConditionOrder, that never
// became true — first, not last, because a later condition is meaningless
// while an earlier one is false.
func firstUnmet(everTrue map[ActionCondition]bool, detail map[ActionCondition]string) (ActionCondition, string) {
	for _, c := range actionConditionOrder {
		if !everTrue[c] {
			return c, detail[c]
		}
	}
	// Every condition was true at some point but the pass never lined up in
	// one observation. Hit-testability is the last gate, so that is what the
	// caller was waiting on.
	return CondHitTestable, detail[CondHitTestable]
}

// ---------------------------------------------------------------------------
// FR-037 — a post-gate failure must not escape as a bare timeout
// ---------------------------------------------------------------------------

// translatePostGateErr maps a failure from an action the gate had ALREADY
// passed into ErrNotActionable{CondVisible}.
//
// The window is real, not theoretical: the gate runs strictly BEFORE
// chromedp.Click, which then runs its own NodeVisible wait, and when that wait
// fails chromedp's retryWithSleep polls to the context deadline and returns a
// bare "context deadline exceeded" — the exact string this wave exists to
// eliminate. It happens whenever a single-page app re-renders between the gate
// and the dispatch.
//
// The condition reported is `visible` because that is literally what chromedp
// re-checked and lost. NO fifth condition is added: the closed set stays at
// four.
// Returns (translated, true) when it recognised a post-gate visibility loss,
// and (nil, false) for anything else, a nil error included. Callers branch on
// the bool — comparing the returned error against the input to see whether it
// changed reads as an errors.Is mistake and is one wrapping away from being
// one.
func postGateErr(err error, toolName, display string) (error, bool) {
	if err == nil {
		return nil, false
	}
	if errors.Is(err, chromedp.ErrNotVisible) || errors.Is(err, context.DeadlineExceeded) ||
		strings.Contains(err.Error(), "context deadline exceeded") {
		noteGateFailure(CondVisible)
		return &ErrNotActionable{
			Failed:  CondVisible,
			Display: display,
			Detail:  "the element passed the actionability gate and then stopped being visible before the action was dispatched",
			Tool:    toolName,
		}, true
	}
	return nil, false
}

// translatePostGateErr is postGateErr under the name the other D2 stream
// reached for first. Two streams landed on two names for one function within
// the same hour and each kept breaking the other's build; this alias ends
// that. It is redundant on purpose and should collapse into postGateErr once
// the wave settles — there is one implementation, so the two cannot drift.
func translatePostGateErr(err error, toolName, display string) (error, bool) {
	return postGateErr(err, toolName, display)
}

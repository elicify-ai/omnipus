package browser

// Unit tests for the actionability gate's CLOSED condition set and its
// "name the first unmet condition" rule (spec §10 order 7). No browser: the
// ordering rule and the error wording are decided in Go, and that is what is
// asserted here. The live four-condition behaviour against a real page is
// actionable_e2e_test.go.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// TestActionCondition_SetIsExactlyFour enumerates the constants so a fifth
// cannot be added silently. ADR-072 criterion 7 is that a failure names WHICH
// condition was unmet; that is only meaningful while the set stays closed.
func TestActionCondition_SetIsExactlyFour(t *testing.T) {
	want := []ActionCondition{CondVisible, CondStable, CondEnabled, CondHitTestable}
	if len(actionConditionOrder) != 4 {
		t.Fatalf("the actionability condition set is closed at FOUR; found %d: %v",
			len(actionConditionOrder), actionConditionOrder)
	}
	for i, c := range want {
		if actionConditionOrder[i] != c {
			t.Errorf("condition %d is %q, want %q — the order is load-bearing: a later condition is meaningless while an earlier one is false",
				i, actionConditionOrder[i], c)
		}
	}
	// The literal wire values are what an agent reads in the error.
	literals := map[ActionCondition]string{
		CondVisible:     "visible",
		CondStable:      "stable",
		CondEnabled:     "enabled",
		CondHitTestable: "hit-testable",
	}
	for c, want := range literals {
		if string(c) != want {
			t.Errorf("condition literal %q != %q; agents match on this string", string(c), want)
		}
	}
}

// TestWaitActionable_NamesFailedCondition_Table drives the gate's decision
// function across all four literals. The oracle is the spec's rule — the FIRST
// condition in order that never became true — not the implementation.
func TestWaitActionable_NamesFailedCondition_Table(t *testing.T) {
	cases := []struct {
		name     string
		everTrue map[ActionCondition]bool
		want     ActionCondition
	}{
		{
			name:     "nothing ever true -> visible",
			everTrue: map[ActionCondition]bool{},
			want:     CondVisible,
		},
		{
			name:     "visible but never stable -> stable",
			everTrue: map[ActionCondition]bool{CondVisible: true},
			want:     CondStable,
		},
		{
			name:     "visible and stable but disabled -> enabled",
			everTrue: map[ActionCondition]bool{CondVisible: true, CondStable: true},
			want:     CondEnabled,
		},
		{
			name:     "visible, stable, enabled but occluded -> hit-testable",
			everTrue: map[ActionCondition]bool{CondVisible: true, CondStable: true, CondEnabled: true},
			want:     CondHitTestable,
		},
		{
			// A later condition true while an earlier one is not must NOT
			// win: the earlier failure is what the agent has to fix first.
			name:     "enabled seen true but never visible -> visible",
			everTrue: map[ActionCondition]bool{CondEnabled: true, CondHitTestable: true},
			want:     CondVisible,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := firstUnmet(tc.everTrue, map[ActionCondition]string{})
			if got != tc.want {
				t.Fatalf("firstUnmet = %q, want %q", got, tc.want)
			}
			err := &ErrNotActionable{Failed: got, Display: "#go", Tool: "browser_click"}
			if !strings.Contains(err.Error(), string(tc.want)) {
				t.Errorf("the error must NAME the condition; %q does not contain %q", err.Error(), tc.want)
			}
			if !strings.Contains(err.Error(), "#go") {
				t.Errorf("the error must name the locator the agent gave; got %q", err.Error())
			}
			if strings.Contains(err.Error(), "deadline exceeded") {
				t.Errorf("a gate failure must never read as a bare timeout; got %q", err.Error())
			}
		})
	}
}

func TestErrNotActionable_HitTestNamesTheOccluder(t *testing.T) {
	err := &ErrNotActionable{
		Failed:  CondHitTestable,
		Display: "button.confirm",
		Detail:  "covered by div#cookie-banner",
		Tool:    "browser_click",
	}
	if !strings.Contains(err.Error(), "div#cookie-banner") {
		t.Errorf("a hit-testable failure must name the occluding element; got %q", err.Error())
	}
}

// FR-037: a post-gate failure is translated, never surfaced bare.
func TestTranslatePostGateErr_MapsDeadlineToVisible(t *testing.T) {
	for _, in := range []error{context.DeadlineExceeded, errors.New("context deadline exceeded")} {
		out, ok := postGateErr(in, "browser_click", "#go")
		if !ok {
			t.Fatalf("a post-gate deadline must be recognised; got ok=false for %v", in)
		}
		var na *ErrNotActionable
		if !errors.As(out, &na) {
			t.Fatalf("want *ErrNotActionable, got %T: %v", out, out)
		}
		if na.Failed != CondVisible {
			t.Errorf("a post-gate loss is reported as %q; got %q — no FIFTH condition is added", CondVisible, na.Failed)
		}
		if strings.Contains(out.Error(), "deadline exceeded") {
			t.Errorf("the bare timeout string must not survive translation; got %q", out.Error())
		}
	}
}

func TestTranslatePostGateErr_PassesUnrelatedErrorsThrough(t *testing.T) {
	in := errors.New("could not find node with given selector")
	if _, ok := postGateErr(in, "browser_click", "#go"); ok {
		t.Error("an unrelated error must not be translated into a gate failure — a specific message beats a generic one laid over it")
	}
	if _, ok := postGateErr(nil, "browser_click", "#go"); ok {
		t.Error("nil must not be reported as a post-gate failure")
	}
}

// FR-034: the revert switch is read live, and an unrecognised value must never
// silently WEAKEN the gate.
func TestActionabilityGate_UnknownValueMeansFull(t *testing.T) {
	t.Cleanup(func() { SetActionabilityGate(ActionabilityGateFull) })

	for _, v := range []string{"", "  ", "nonsense", "FULL", "Visible_Only"} {
		SetActionabilityGate(v)
		if got := currentActionabilityGate(); got != ActionabilityGateFull {
			t.Errorf("SetActionabilityGate(%q) -> %q; an unrecognised value must mean the FULL gate", v, got)
		}
	}
	SetActionabilityGate(ActionabilityGateVisibleOnly)
	if got := currentActionabilityGate(); got != ActionabilityGateVisibleOnly {
		t.Errorf("SetActionabilityGate(visible_only) -> %q", got)
	}
}

// FR-032: the counters move on a failure and on an indeterminate hit test, and
// they are per-condition.
func TestGateCounters_RecordPerCondition(t *testing.T) {
	before := gateFailureTotal(CondEnabled)
	beforeInd := gateIndeterminateTotal()

	var seen []string
	SetGateMetricRecorder(func(label string) { seen = append(seen, label) })
	t.Cleanup(func() { SetGateMetricRecorder(func(string) {}) })

	noteGateFailure(CondEnabled)
	noteGateIndeterminate()

	if gateFailureTotal(CondEnabled) != before+1 {
		t.Errorf("the per-condition failure counter did not move for %q", CondEnabled)
	}
	if gateIndeterminateTotal() != beforeInd+1 {
		t.Error("the indeterminate counter did not move")
	}
	if len(seen) != 2 || seen[0] != string(CondEnabled) || seen[1] != hitTestIndeterminate {
		t.Errorf("recorder saw %v, want [enabled indeterminate]", seen)
	}
}

// FR-007's counting seam: it counts what the GATE issues and nothing else.
func TestGateEvalCounter_ScopedToTheGate(t *testing.T) {
	armGateEvalCounter()
	gateEvalActive.Store(true)
	countGateEval()
	countGateEval()
	gateEvalActive.Store(false)
	// Traffic outside the gate must not be counted — this stands in for
	// chromedp.Click's own DOM.getBoxModel / Input.dispatchMouseEvent.
	countGateEval()
	if n := disarmGateEvalCounter(); n != 2 {
		t.Fatalf("gate round-trip count = %d, want 2 (the RT1/RT2 pair, and nothing the tool issues after the gate returns)", n)
	}
}

func TestGateProbeJS_EmbedsSelectorSafely(t *testing.T) {
	js, err := buildGateProbeJS(`[data-omnipus-tsel="a\"b"]`, true)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(js, `"[data-omnipus-tsel="a"b"]"`) {
		t.Error("the selector must be JSON-encoded into the probe, never concatenated raw")
	}
	if !strings.Contains(js, "requestAnimationFrame") {
		t.Error("the RT2 probe must schedule itself after one animation frame")
	}
	plain, err := buildGateProbeJS("#a", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plain, "var raf = false") {
		t.Error("the RT1 probe must not wait for a frame")
	}
	// The gate must never reach for a separate DOM round trip.
	for _, banned := range []string{"getNodeForLocation", "getBoxModel"} {
		if strings.Contains(plain, banned) {
			t.Errorf("the gate probe must not use %s — it is a second round trip that cannot see shadow roots", banned)
		}
	}
}

// TestActionabilityGate_ConfigKeyIsActuallyRead is the test the config key did
// not have. tools.browser.actionability_gate shipped with a struct home, an
// env tag and a documentation row, and NOTHING read it — a setting an operator
// could turn with no effect, which is worse than no setting at all.
//
// It asserts the wiring at the source level because the write side lives in
// pkg/agent (which imports pkg/tools/browser, not the other way round), so a
// runtime assertion here cannot reach it.
func TestActionabilityGate_ConfigKeyIsActuallyRead(t *testing.T) {
	// The read side: the value is consulted INSIDE the gate.
	src := readSourceForTest(t, "actionable.go")
	if !strings.Contains(src, "currentActionabilityGate()") {
		t.Error("nothing reads the actionability gate setting inside waitActionable")
	}

	// The write side: something pushes live config in, on reload.
	loopSrc := readSourceForTest(t, "../../agent/loop.go")
	if !strings.Contains(loopSrc, "browser.SetActionabilityGate(cfg.Tools.Browser.ActionabilityGate)") {
		t.Error("tools.browser.actionability_gate has no writer — the operator would set it and nothing would change, which is the failure mode this project has shipped before")
	}
}

// ---------------------------------------------------------------------------
// R5 finding 4 — a page that never answers must not be reported as an element
// that is not visible.
// ---------------------------------------------------------------------------

// TestWaitActionable_PageNeverAnswers_ReportsTheTabNotTheElement is the
// regression for the confident-wrong diagnosis.
//
// The oracle is the requirement, not the code: when NO probe ever came back
// from the page, the gate has observed nothing about the element, so any
// sentence about the element is unfounded. The one it used to emit —
// firstUnmet's answer for an empty everTrue — was `visible`, which an agent
// reads as "your locator matched nothing" and answers by retrying a locator
// that will never work while a dialog is up.
//
// The probe is driven against a context that is not a chromedp context, so
// every chromedp.Run returns an error immediately and the page answers
// nothing. That is the same observable the blocked-renderer case produces (a
// dialog holds the main thread, Runtime.evaluate never returns, the deadline
// takes every probe), which is why this needs no browser to be a real test of
// the branch.
func TestWaitActionable_PageNeverAnswers_ReportsTheTabNotTheElement(t *testing.T) {
	SetActionabilityGate(ActionabilityGateFull)
	t.Cleanup(func() { SetActionabilityGate(ActionabilityGateFull) })

	_, err := waitActionableOutcome(context.Background(), "browser_click", "#go", "#go", 60*time.Millisecond)
	if err == nil {
		t.Fatal("a gate that never got an answer must still fail")
	}

	var na *ErrNotActionable
	if errors.As(err, &na) {
		t.Fatalf("a page that never answered must NOT produce a claim about the element; got %q (condition %q)",
			err.Error(), na.Failed)
	}

	var tna *TabNotAnsweringError
	if !errors.As(err, &tna) {
		t.Fatalf("want *TabNotAnsweringError, got %T: %v", err, err)
	}
	if tna.Tool != "browser_click" || tna.Display != "#go" {
		t.Errorf("the error must carry the calling tool and the user-facing locator; got tool=%q display=%q",
			tna.Tool, tna.Display)
	}

	msg := err.Error()
	// The verb that actually clears the most common cause has to be IN the
	// message — an agent cannot act on a diagnosis it is not given.
	if !strings.Contains(msg, "browser_handle_dialog") {
		t.Errorf("the message must name browser_handle_dialog, the one-call fix for the usual cause; got %q", msg)
	}
	// ...and the second outcome, so an agent whose handle_dialog answers "no
	// dialog" is not left with no next move.
	if !strings.Contains(msg, "re-navigate") {
		t.Errorf("the message must name the recovery for a wedged tab too; got %q", msg)
	}
	if strings.Contains(msg, "not actionable") {
		t.Errorf("the message must not read as an actionability verdict on the element; got %q", msg)
	}
}

// TestWaitActionable_PageAnswers_StillNamesTheCondition is the other half, and
// the reason the fix above is narrow rather than a blanket change: when the
// page DOES answer, the four-condition verdict is exactly what the agent
// needs and must be unaffected.
func TestWaitActionable_PageAnswers_StillNamesTheCondition(t *testing.T) {
	// firstUnmet is the decision the answered path makes; driving it directly
	// keeps this assertion free of a browser while still asserting that the
	// answered path's oracle is untouched.
	failed, _ := firstUnmet(map[ActionCondition]bool{CondVisible: true, CondStable: true}, map[ActionCondition]string{})
	if failed != CondEnabled {
		t.Errorf("an answered page still names the first unmet condition; got %q, want %q", failed, CondEnabled)
	}
}

// ---------------------------------------------------------------------------
// R5 finding 5 — the diagnostic fallback must not name a mechanism that did
// not run.
// ---------------------------------------------------------------------------

// TestVisibleOnlyErr_DoesNotClaimAGateWasPassed is the regression for the
// false cause.
//
// visible_only exists so an operator whose site regresses under the strict
// gate has something to turn WHILE DIAGNOSING. Its failure used to be routed
// through postGateErr, whose detail says the element "passed the actionability
// gate and then stopped being visible before the action was dispatched". In
// this mode there is no four-condition gate at all — chromedp's visibility
// wait is the whole check and it is the thing that failed. Nothing passed
// anything, and a wrong cause is costliest in exactly the mode an operator
// turns on to find a cause.
func TestVisibleOnlyErr_DoesNotClaimAGateWasPassed(t *testing.T) {
	err := visibleOnlyErr("browser_click", "#go")
	msg := err.Error()

	if strings.Contains(msg, "passed the actionability gate") {
		t.Errorf("visible_only ran no gate to pass — that cause is false; got %q", msg)
	}
	if !strings.Contains(msg, "visible_only") {
		t.Errorf("the message must name the mode that produced this coarse answer; got %q", msg)
	}
	// The scope of the answer matters as much as the answer: under the full
	// gate `visible` means the other three were checked too, and here it means
	// they were not looked at.
	for _, unchecked := range []string{"stability", "enabledness", "hit-testability"} {
		if !strings.Contains(msg, unchecked) {
			t.Errorf("the message must say %s was not evaluated, or a coarse answer reads as a precise one; got %q",
				unchecked, msg)
		}
	}

	var na *ErrNotActionable
	if !errors.As(err, &na) {
		t.Fatalf("want *ErrNotActionable, got %T", err)
	}
	if na.Failed != CondVisible {
		t.Errorf("the condition was always true and stays CondVisible; got %q", na.Failed)
	}

	// The two must not converge again: a later "these are the same, collapse
	// them" simplification is precisely how the false cause came back once.
	post, ok := postGateErr(context.DeadlineExceeded, "browser_click", "#go")
	if !ok {
		t.Fatal("postGateErr must still translate a deadline")
	}
	if post.Error() == msg {
		t.Error("the visible_only failure and the post-gate failure describe DIFFERENT mechanisms and must not share one sentence")
	}
}

// TestGateVisibilityLoss_PredicateIsShared_AndNarrow pins the one predicate
// both translators use. A specific, named CDP failure must survive: rewriting
// it into "not visible" replaces a true statement with a plausible one.
func TestGateVisibilityLoss_PredicateIsShared_AndNarrow(t *testing.T) {
	for _, in := range []error{context.DeadlineExceeded, errors.New("context deadline exceeded")} {
		if !gateVisibilityLoss(in) {
			t.Errorf("%v is a visibility-wait loss", in)
		}
	}
	for _, in := range []error{nil, errors.New("could not find node with given selector"), errors.New("invalid context")} {
		if gateVisibilityLoss(in) {
			t.Errorf("%v is a specific failure and must be passed through untouched", in)
		}
	}
}

// TestTabNotAnswering_CountsNoConditionFailure — the FR-032 counters count
// CONDITIONS that were checked and failed. A tab that never answered had no
// condition checked, so counting one would inflate `visible` with cases that
// say nothing about visibility, and that counter is the evidence a later
// change is meant to act on.
func TestTabNotAnswering_CountsNoConditionFailure(t *testing.T) {
	t.Cleanup(func() { SetActionabilityGate(ActionabilityGateFull) })
	SetActionabilityGate(ActionabilityGateFull)

	before := gateFailureTotal(CondVisible)
	_, _ = waitActionableOutcome(context.Background(), "browser_click", "#go", "#go", 40*time.Millisecond)
	if after := gateFailureTotal(CondVisible); after != before {
		t.Errorf("the visible counter moved from %d to %d for a tab that answered nothing", before, after)
	}
}

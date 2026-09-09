// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// goal_compile_dod_floor_test.go covers code-review fix-wave finding #2
// (echo-vs-judged divergence): compileGoalIntent — the deterministic
// fallback AND the marker-only path both route through it — now populates
// the floor DoD (ADR-080 D-DOD layer 3) at COMPILE time, so the confirm
// echo / persisted pending-or-active JSON always shows the SAME DoD the
// judge later adjudicates against (compiledGoalCriteriaFor's Criteria UNION
// DoD seam). Before the fix, loadCompiledGoal injected the floor DoD only
// on LOAD, so the freshly-compiled CompiledGoal a user actually confirmed
// against (formatGoalEcho / the queued goal_status frame) never showed it.

package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// assertFloorDoD asserts dod is exactly the newFloorDoD() ladder (same IDs,
// text, judgment, provenance) — the byte-stable sentinel items, not a fresh
// derivation.
func assertFloorDoD(t *testing.T, dod []task.AcceptanceCriterion) {
	t.Helper()
	floor := newFloorDoD()
	if len(dod) != len(floor) {
		t.Fatalf("dod has %d items, want %d (the floor DoD): %+v", len(dod), len(floor), dod)
	}
	for i, want := range floor {
		got := dod[i]
		if got.ID != want.ID || got.Text != want.Text || got.Judgment != want.Judgment ||
			got.Provenance != want.Provenance {
			t.Errorf("dod[%d] = %+v, want %+v", i, got, want)
		}
	}
}

// TestCompileGoalIntent_MarkerOnly_CarriesFloorDoDAtCompileTime is the direct
// unit-level proof: compileGoalIntent's own return (never mind what a later
// load-time backfill would add) already carries the floor DoD, and the echo
// built straight from that CompiledGoal renders the Definition of Done
// block — matching what compiledGoalCriteriaFor will judge.
func TestCompileGoalIntent_MarkerOnly_CarriesFloorDoDAtCompileTime(t *testing.T) {
	fc := fakeFeasibilityContext{bashReachable: true}
	res := compileGoalIntent("[tests pass]", fc, "tester")
	if res.Rejection != nil {
		t.Fatalf("unexpected rejection: %+v", res.Rejection)
	}
	assertFloorDoD(t, res.Goal.DoD)

	echo := formatGoalEcho(res.Goal)
	for _, want := range []string{"Definition of Done", "No secrets or credentials", "grounded, not assumed"} {
		if !strings.Contains(echo, want) {
			t.Fatalf("echo built from the freshly compiled goal must show the floor DoD block (missing %q), got:\n%s", want, echo)
		}
	}

	// The judged set (compiledGoalCriteriaFor) must be the SAME items the
	// echo just showed — echoed == judged, the fix's whole point.
	criteriaJSON, err := marshalCompiledGoal(res.Goal)
	if err != nil {
		t.Fatal(err)
	}
	judged := compiledGoalCriteriaFor(criteriaJSON, "make the tests pass", "tester")
	for _, want := range res.Goal.DoD {
		found := false
		for _, j := range judged {
			if j.ID == want.ID {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("judged set (compiledGoalCriteriaFor) is missing echoed DoD item %q", want.ID)
		}
	}
}

// TestGoalTwoPhase_MarkerOnly_ActiveGoalCarriesFloorDoD drives the FULL
// marker-only engine path (US-3 S3, zero LLM, immediate activation) and
// asserts the persisted GoalCriteriaJSON — what a later goal round actually
// judges against — carries the floor DoD, and that the judged union
// (compiledGoalCriteriaFor) includes those same items.
func TestGoalTwoPhase_MarkerOnly_ActiveGoalCarriesFloorDoD(t *testing.T) {
	al, agentInst, provider, store, sid, opts := twoPhaseHarness(t,
		func(int, []providers.Message) (*providers.LLMResponse, error) {
			return nil, errors.New("marker-only goals must never reach the LLM compile")
		}, nil)
	allowBashPolicy(agentInst) // [tests] compiles a bash-run check criterion

	matched, handled, _ := setGoal(t, al, agentInst, opts, "[tests]")
	if !matched || handled {
		t.Fatalf("marker-only set: matched=%v handled=%v, want matched=true handled=false", matched, handled)
	}
	if provider.callCount() != 0 {
		t.Fatalf("marker-only set made %d LLM calls, want 0", provider.callCount())
	}

	meta, err := store.GetMeta(sid)
	if err != nil {
		t.Fatal(err)
	}
	if meta.GoalCondition == "" || meta.GoalCriteriaJSON == "" {
		t.Fatal("setup: marker-only goal must activate immediately with compiled criteria persisted")
	}
	compiled := loadCompiledGoal(meta.GoalCriteriaJSON)
	if compiled == nil {
		t.Fatal("GoalCriteriaJSON must parse as a CompiledGoal")
	}
	assertFloorDoD(t, compiled.DoD)

	judged := compiledGoalCriteriaFor(meta.GoalCriteriaJSON, meta.GoalCondition, sid)
	for _, want := range compiled.DoD {
		found := false
		for _, j := range judged {
			if j.ID == want.ID {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("judged set is missing the persisted floor DoD item %q", want.ID)
		}
	}
}

// TestGoalTwoPhase_LLMFailure_FallbackPendingCarriesFloorDoD drives the
// deterministic-fallback path (LLM call errors, compileGoalIntentLLM's
// fallback() closure calls compileGoalIntent directly) and asserts the
// PENDING echo — what the user actually confirms — shows the floor DoD, and
// that the persisted GoalPendingJSON's DoD is the judged set once activated.
func TestGoalTwoPhase_LLMFailure_FallbackPendingCarriesFloorDoD(t *testing.T) {
	al, agentInst, provider, store, sid, opts := twoPhaseHarness(t,
		func(int, []providers.Message) (*providers.LLMResponse, error) {
			return nil, errors.New("provider down")
		}, nil)

	_, handled, echo := setGoal(t, al, agentInst, opts, "make the tests pass")
	if !handled {
		t.Fatal("failed compile must still answer with the (fallback) pending echo")
	}
	if provider.callCount() != 1 {
		t.Fatalf("want exactly 1 (failed) compile attempt, got %d", provider.callCount())
	}
	if !strings.Contains(echo, "Definition of Done") {
		t.Fatalf("fallback pending echo must show the floor DoD block, got:\n%s", echo)
	}

	mid, _ := store.GetMeta(sid)
	if mid.GoalPendingJSON == "" {
		t.Fatal("setup: fallback must park pending")
	}
	pending := loadCompiledGoal(mid.GoalPendingJSON)
	if pending == nil {
		t.Fatal("GoalPendingJSON must parse as a CompiledGoal")
	}
	assertFloorDoD(t, pending.DoD)

	// Confirm activates — the judged set (post-activation GoalCriteriaJSON)
	// must carry the SAME DoD items the pending echo showed.
	matched, handled2, _ := al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal confirm", UserInitiated: true}, agentInst, opts)
	if !matched || handled2 {
		t.Fatal("fallback pending must be confirmable")
	}
	after, _ := store.GetMeta(sid)
	judged := compiledGoalCriteriaFor(after.GoalCriteriaJSON, after.GoalCondition, sid)
	for _, want := range pending.DoD {
		found := false
		for _, j := range judged {
			if j.ID == want.ID {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("post-confirm judged set is missing the echoed floor DoD item %q", want.ID)
		}
	}
}

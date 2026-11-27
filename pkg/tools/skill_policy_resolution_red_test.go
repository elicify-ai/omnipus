package tools

// RED tests — Skills & Tools redesign (#1963, epic #1519 page F), order 1.
// TestSkillPolicyResolution_Table: the per-skill decision of C-11 as a table
// over Dataset D1.
//
// Oracles: skills-tools-redesign-spec.md C-0 (two layers, strictest wins),
// C-3 (trust floor only for unverified skills; survives God Mode), C-11
// (grant → global × agent strictest-wins → God Mode floor on the global side
// → trust floor; outcome deny | ask_pending | allowed) and Dataset D1 rows
// 1–14. Rows 15–16 (upgrade) are order 3 (TestSkillPolicyUpgrade_FromSkillTool).
//
// The production resolver ("one function beside
// pkg/tools/compositor.go::resolveEffectivePolicyWith", C-11) does not exist
// at the landing base. Per the qa-lead RED rule the test is written anyway and
// fails LOUD with BLOCKED until the symbol lands. Expected values below come
// from the spec's datasets, never from the implementation.
//
// Part L, Ruling A (team-lead, 2026-11-26): skillOutcomeOf is bound to the
// function every real entry point calls, tools.ResolveSkillDecision, in its
// own shape (one input struct, one result struct) and not to a test-only
// wrapper beside it: a second copy of the C-11 logic is what FR-066 forbids.
// The datasets are unchanged; only the adapter around the call changed. The
// production structs do not exist on a tree without the backend, so the seam is
// an `any` checked by tests/helpers/nativebinding and called by field name
// through tests/helpers/structcall: this file still compiles there, and the run
// shows BLOCKED instead of a build error. skill_policy_decision_bindings_test.go
// binds it. Whether every entry point of C-11 reaches that same function is
// TestSkillPolicyResolution_EveryEntryPointUsesTheOneOutcome
// (skill_entry_points_one_outcome_red_test.go here, and its pkg/agent twin).

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/tests/helpers/nativebinding"
	"github.com/elicify-ai/omnipus/tests/helpers/structcall"
)

// skillOutcomeInput is one Dataset D1 input row: (global, agent override,
// trust, verified, God Mode) plus the grant gate that C-11 puts first.
// Values are the spec's stored vocabulary: "allow" | "ask" | "deny" | ""
// ("" = no stored entry on that side).
type skillOutcomeInput struct {
	name        string
	granted     bool
	global      string
	agentValue  string
	trust       string
	verified    bool
	godMode     bool
	wantOutcome string // "allow" | "ask_pending" | "deny" (C-11 outcome set)
	wantRefusal string // non-empty: the refusal reason the outcome must name
}

// d1ResolutionTable is Dataset D1 rows 1–14, transcribed from the spec.
// Every expected value cites its row in the spec's dataset.
var d1ResolutionTable = []skillOutcomeInput{
	{
		name: "D1#1 trust ignored for a verified skill (S-13)",
		// allow, none, ask, yes, off -> allow
		granted: true, global: "allow", agentValue: "", trust: "ask", verified: true, godMode: false,
		wantOutcome: "allow",
	},
	{
		name: "D1#2 agent stricter than global wins (S-09)",
		// allow, deny, ask, yes, off -> deny
		granted: true, global: "allow", agentValue: "deny", trust: "ask", verified: true, godMode: false,
		wantOutcome: "deny",
	},
	{
		name: "D1#3 stored looser agent allow is floored to global ask (S-19)",
		// ask, allow, ask, yes, off -> ask
		granted: true, global: "ask", agentValue: "allow", trust: "ask", verified: true, godMode: false,
		wantOutcome: "ask_pending",
	},
	{
		name: "D1#4 global deny wins over agent allow (S-19)",
		// deny, none, allow, yes, off -> deny
		granted: true, global: "deny", agentValue: "", trust: "allow", verified: true, godMode: false,
		wantOutcome: "deny",
	},
	{
		name: "D1#5 trust deny refuses an unverified skill (S-29)",
		// allow, none, deny, no, off -> deny
		granted: true, global: "allow", agentValue: "", trust: "deny", verified: false, godMode: false,
		wantOutcome: "deny",
	},
	{
		name: "D1#6 trust ask floors an unverified skill to ask (S-30)",
		// allow, none, ask, no, off -> ask
		granted: true, global: "allow", agentValue: "", trust: "ask", verified: false, godMode: false,
		wantOutcome: "ask_pending",
	},
	{
		name: "D1#7 trust allow leaves the permission outcome (S-32)",
		// allow, none, allow, no, off -> allow
		granted: true, global: "allow", agentValue: "", trust: "allow", verified: false, godMode: false,
		wantOutcome: "allow",
	},
	{
		name: "D1#8 God Mode floors a global ask (S-31)",
		// ask, none, allow, no, on -> allow
		granted: true, global: "ask", agentValue: "", trust: "allow", verified: false, godMode: true,
		wantOutcome: "allow",
	},
	{
		name: "D1#9 trust floor survives God Mode (S-31, ST-Q2 A)",
		// allow, none, ask, no, on -> ask (trust card)
		granted: true, global: "allow", agentValue: "", trust: "ask", verified: false, godMode: true,
		wantOutcome: "ask_pending",
	},
	{
		name: "D1#10 per-agent value survives God Mode (S-19)",
		// allow, ask, ask, yes, on -> ask
		granted: true, global: "allow", agentValue: "ask", trust: "ask", verified: true, godMode: true,
		wantOutcome: "ask_pending",
	},
	{
		name: "D1#11 built-in skill loads under trust deny (S-35)",
		// allow, none, deny, yes (built-in), off -> allow
		granted: true, global: "allow", agentValue: "", trust: "deny", verified: true, godMode: false,
		wantOutcome: "allow",
	},
	{
		name: "D1#12 God Mode floors a global deny; agent allow then holds (S-76)",
		// deny, allow, allow, yes, on -> allow
		granted: true, global: "deny", agentValue: "allow", trust: "allow", verified: true, godMode: true,
		wantOutcome: "allow",
	},
	{
		name: "D1#13 no entry on either side denies (tripwire, S-17)",
		// missing, none, ask, yes, off -> deny (+ Error log; the log tripwire
		// is exercised at the entry point — the C-2 self-heal (order 2)
		// removes the missing-entry state entirely)
		granted: true, global: "", agentValue: "", trust: "ask", verified: true, godMode: false,
		wantOutcome: "deny",
	},
	{
		name: "D1#14 a skill not granted is refused whatever its permission (S-14)",
		// allow, none, ask, yes, not granted -> refused: not granted.
		// C-11 puts the grant gate first, so the resolver's outcome is deny and
		// the reason it reports reads as not granted.
		granted: false, global: "allow", agentValue: "", trust: "ask", verified: true, godMode: false,
		wantOutcome: "deny",
		wantRefusal: "not granted",
	},
}

// skillOutcomeOf is the test-local seam for the C-11 resolver. The binding is
// tools.ResolveSkillDecision, which takes one SkillDecisionInput and returns
// one SkillDecision; it stays nil here so the RED run fails with BLOCKED
// instead of panicking. Only raw inputs cross the native boundary: the spec's
// expected outcome and refusal stay in the test table and are never handed to
// the function.
var skillOutcomeOf any

// decideD1Row runs one Dataset D1 row through the bound function and returns
// the outcome and the refusal reason it reports. The input struct is filled by
// field name from the row's raw values. The key is any one skill's key: the
// decision does not depend on which.
func decideD1Row(t *testing.T, row skillOutcomeInput) (outcome, reason string) {
	t.Helper()
	got := structcall.Call(t, skillOutcomeOf, structcall.Fields{
		"Key":      "deep-research",
		"Granted":  row.granted,
		"Global":   row.global,
		"Agent":    row.agentValue,
		"Trust":    row.trust,
		"Verified": row.verified,
		"GodMode":  row.godMode,
	})
	return got.Str(t, "Outcome"), got.Str(t, "Reason")
}

// readsAsNotGranted reports whether a refusal reason says the skill is not
// granted, whatever its spelling (a code such as not_granted or a sentence).
func readsAsNotGranted(reason string) bool {
	return strings.Contains(strings.ReplaceAll(strings.ToLower(reason), "_", " "), "not granted")
}

// TestSkillPolicyResolution_Table drives Dataset D1 rows 1–14 through the
// C-11 per-skill decision.
//
// Refusal column. The earlier seam returned a refusal only for a skill that is
// not granted. The function every entry point calls reports a reason for every
// deny, so the column is read as the spec states it: the not-granted row
// reports a reason that reads as not granted (S-14), and no other row does.
// The three refusals the spec words are three different sentences — S-09
// "naming the skill setting", S-14 "refused as not granted", S-29 the trust
// sentence — so the reasons of D1#4, D1#5 and D1#14 must differ, and the
// refusals S-09, S-29 and S-14 describe (D1#2, D1#5, D1#14) must each carry
// one. A function that returns one constant reason for every deny fails that.
//
// Traces to: BDD S-09, S-13, S-14, S-19, S-29, S-30, S-31, S-35; Dataset D1.
func TestSkillPolicyResolution_Table(t *testing.T) {
	nativebinding.Require(t, "pkg/tools", skillOutcomeOf,
		"C-0/C-3/C-11/D1: the one per-skill decision function (the binding is ResolveSkillDecision); that every entry point reaches it is TestSkillPolicyResolution_EveryEntryPointUsesTheOneOutcome")
	reasons := map[string]string{}
	for _, tc := range d1ResolutionTable {
		t.Run(tc.name, func(t *testing.T) {
			outcome, reason := decideD1Row(t, tc)
			if outcome != tc.wantOutcome {
				t.Fatalf("outcome = %q, want %q (spec Dataset %s)", outcome, tc.wantOutcome, tc.name)
			}
			if tc.wantRefusal != "" && !readsAsNotGranted(reason) {
				t.Fatalf("refusal reason = %q, want it to name %q (spec Dataset %s)", reason, tc.wantRefusal, tc.name)
			}
			if tc.wantRefusal == "" && readsAsNotGranted(reason) {
				t.Fatalf("refusal reason = %q, want it not to say not granted: the skill is granted (spec Dataset %s)", reason, tc.name)
			}
			reasons[strings.SplitN(tc.name, " ", 2)[0]] = reason
		})
	}
	if t.Failed() {
		return // a row failed above; comparing reasons now would only repeat it
	}

	for _, id := range []string{"D1#2", "D1#5", "D1#14"} {
		if reasons[id] == "" {
			t.Errorf("%s is a refusal the spec words (S-09, S-29, S-14), so the decision must report a reason for it; the reason is empty", id)
		}
	}
	for _, pair := range [][2]string{{"D1#4", "D1#14"}, {"D1#4", "D1#5"}, {"D1#5", "D1#14"}} {
		if reasons[pair[0]] == reasons[pair[1]] {
			t.Errorf("%s and %s are refused for the same reason %q; the spec words them as different sentences (S-09/S-19, S-29, S-14)", pair[0], pair[1], reasons[pair[0]])
		}
	}
}

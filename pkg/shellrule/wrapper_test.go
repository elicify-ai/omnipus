// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Regression tests for the R3 security-review fix (CRITICAL, founder
// decision 2026-09-24: "look through wrappers and fail closed"). Every case
// here is written to FAIL against the pre-fix evaluateSegment (which
// resolved exactly one, un-unwrapped head per segment) and PASS after it.
package shellrule

import "testing"

// TestEvaluateCommand_WrapperDoesNotBypassDenyRule is R3's central
// regression: a deny rule keyed on the REAL binary ("rm") must still fire
// when the command reaches it through a known wrapper. Before the fix, every
// one of these resolved a head of the WRAPPER itself (env/sudo/nice/
// command/timeout/xargs), which the deny rule could never match —
// Action=ActionNone, ceiling governs, meaning an `allow` ceiling (Auto
// mode's own definition) ran the denied command unprompted.
func TestEvaluateCommand_WrapperDoesNotBypassDenyRule(t *testing.T) {
	dir := t.TempDir()
	writeFakeExecutable(t, dir, "rm")
	opts := baseOptions(dir)
	rules := []Rule{{Action: ActionDeny, Binary: "rm"}}

	cases := []string{
		"env rm -rf x",
		"sudo rm -rf x",
		"nice -n 10 rm -rf x",
		"command rm -rf x",
		"env sudo rm -rf x",
		"timeout 5 rm -rf x",
		"xargs rm",
	}
	for _, cmd := range cases {
		t.Run(cmd, func(t *testing.T) {
			got := EvaluateCommand(cmd, rules, opts)
			if got.Action != ActionDeny {
				t.Fatalf("EvaluateCommand(%q).Action = %q, want deny — R3 wrapper bypass", cmd, got.Action)
			}
			if len(got.Segments) != 1 || got.Segments[0].MatchedRule == nil {
				t.Fatalf("EvaluateCommand(%q): expected exactly one segment with MatchedRule set, got %+v", cmd, got.Segments)
			}
			if got.Segments[0].MatchedRule.Binary != "rm" {
				t.Errorf("EvaluateCommand(%q): MatchedRule.Binary = %q, want %q", cmd, got.Segments[0].MatchedRule.Binary, "rm")
			}
		})
	}
}

// TestEvaluateCommand_InterpreterFormDeniedWhenScriptContainsDenyWord is
// R3's interpreter fail-closed rule: an interpreter/eval form whose real
// command this package cannot resolve at all must still deny when a DENY
// rule's binary name appears as a word inside the literal script text.
func TestEvaluateCommand_InterpreterFormDeniedWhenScriptContainsDenyWord(t *testing.T) {
	dir := t.TempDir()
	writeFakeExecutable(t, dir, "bash")
	writeFakeExecutable(t, dir, "sh")
	opts := baseOptions(dir)
	rules := []Rule{{Action: ActionDeny, Binary: "rm"}}

	cases := []string{
		`bash -c "rm -rf x"`,
		`sh -c 'rm -rf x'`,
		`eval "rm -rf x"`,
	}
	for _, cmd := range cases {
		t.Run(cmd, func(t *testing.T) {
			got := EvaluateCommand(cmd, rules, opts)
			if got.Action != ActionDeny {
				t.Fatalf("EvaluateCommand(%q).Action = %q, want deny — R3 interpreter fail-closed", cmd, got.Action)
			}
		})
	}
}

// TestEvaluateCommand_InterpreterFormAsksWithOnlyAskRule proves the "route
// to ask, not allow" half of R3's interpreter rule: with only an ASK rule
// configured (no deny rule's binary appears in the script text at all), an
// interpreter/eval segment must ask rather than silently defer to the
// ceiling as though D3 had nothing to say.
func TestEvaluateCommand_InterpreterFormAsksWithOnlyAskRule(t *testing.T) {
	dir := t.TempDir()
	writeFakeExecutable(t, dir, "bash")
	opts := baseOptions(dir)
	rules := []Rule{{Action: ActionAsk, Binary: "npm", ArgPrefix: "publish"}}

	got := EvaluateCommand(`bash -c "echo hi"`, rules, opts)
	if got.Action != ActionAsk {
		t.Fatalf("Action = %q, want ask", got.Action)
	}
	if len(got.Segments) != 1 || got.Segments[0].MatchedRule == nil {
		t.Fatalf("expected the segment to carry a genuine (non-nil MatchedRule) ask verdict, got %+v", got.Segments)
	}
}

// TestEvaluateCommand_NoRulesAtAll_BehaviourUnchanged proves the fix adds no
// new prompts for a zero-rule configuration — the common, unwired-test
// baseline every other D3 test already relies on.
func TestEvaluateCommand_NoRulesAtAll_BehaviourUnchanged(t *testing.T) {
	dir := t.TempDir()
	for _, bin := range []string{"ls", "git", "bash", "rm"} {
		writeFakeExecutable(t, dir, bin)
	}
	opts := baseOptions(dir)
	var rules []Rule

	cases := []string{
		"ls -la",
		"git status",
		"env rm -rf x",
		`bash -c "echo hi"`,
	}
	for _, cmd := range cases {
		t.Run(cmd, func(t *testing.T) {
			got := EvaluateCommand(cmd, rules, opts)
			if got.Action != ActionNone {
				t.Errorf("EvaluateCommand(%q).Action = %q, want none (zero rules configured) — new prompt for a plain command", cmd, got.Action)
			}
		})
	}
}

// TestEvaluateCommand_UnrecognisedWrapperLaterArgDenyWordAsks is the R3
// residual blind-spot safety net: a head this package does not recognise as
// a known wrapper (so it cannot look through it) must still route to ask
// when one of its LATER argument words equals a configured deny rule's
// binary — never silently pass as "no rule matched".
func TestEvaluateCommand_UnrecognisedWrapperLaterArgDenyWordAsks(t *testing.T) {
	dir := t.TempDir()
	writeFakeExecutable(t, dir, "strace")
	opts := baseOptions(dir)
	rules := []Rule{{Action: ActionDeny, Binary: "rm"}}

	got := EvaluateCommand("strace rm -rf x", rules, opts)
	if got.Action != ActionAsk {
		t.Fatalf("Action = %q, want ask (unrecognised wrapper, deny-rule word in later arg)", got.Action)
	}
}

// TestEvaluateCommand_WrapperAllowRuleStillRequiresUnwrappedMatch proves the
// fix's other direction: an ALLOW rule keyed on the WRAPPER name must not
// be satisfied merely because the wrapper is present — D3 evaluates the
// UNWRAPPED command, so an allow rule on "env" does not allow "env rm -rf
// x" when there is no allow rule on "rm" itself.
func TestEvaluateCommand_WrapperAllowRuleStillRequiresUnwrappedMatch(t *testing.T) {
	dir := t.TempDir()
	writeFakeExecutable(t, dir, "rm")
	writeFakeExecutable(t, dir, "env") // rule's Binary must genuinely resolve, so a non-match proves the unwrap, not a missing executable
	opts := baseOptions(dir)
	rules := []Rule{{Action: ActionAllow, Binary: "env"}}

	got := EvaluateCommand("env rm -rf x", rules, opts)
	if got.Action == ActionAllow {
		t.Errorf("Action = %q, want NOT allow — an allow rule on the wrapper must not auto-approve the unwrapped command", got.Action)
	}
}

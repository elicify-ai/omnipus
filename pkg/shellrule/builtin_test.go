// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package shellrule

import "testing"

// TestEvaluateCommand_HarmlessBuiltin_IsNotBlind is the regression test for
// review finding #12: a plain shell builtin like `cd` has no on-PATH
// executable, so before this fix ResolveBinary failed for it and
// evaluateSegment reported a blind spot (Action=ActionAsk) even with ZERO
// command_rules configured — meaning every `cd /tmp` under Auto needed an
// approval prompt for no security reason at all.
func TestEvaluateCommand_HarmlessBuiltin_IsNotBlind(t *testing.T) {
	dir := t.TempDir() // deliberately empty — no "cd" executable anywhere on PATH
	opts := baseOptions(dir)

	for _, cmd := range []string{"cd /tmp", "export FOO=bar", "set -e", "unset FOO", "pwd"} {
		got := EvaluateCommand(cmd, nil, opts)
		if len(got.Segments) != 1 {
			t.Fatalf("EvaluateCommand(%q): expected 1 segment, got %d", cmd, len(got.Segments))
		}
		if got.Segments[0].Blind {
			t.Errorf("EvaluateCommand(%q).Segments[0].Blind = true, want false (finding #12 regression): reason=%q",
				cmd, got.Segments[0].BlindReason)
		}
	}
}

// TestEvaluateCommand_SourceStaysBlind confirms the deliberate exception:
// `source`/`.` execute an arbitrary file's contents in the current shell —
// the same risk class as running a script — so they are NOT added to
// posixBuiltins and must keep failing PATH resolution (a blind spot, which
// routes to Ask under Auto — the safe direction).
func TestEvaluateCommand_SourceStaysBlind(t *testing.T) {
	dir := t.TempDir()
	opts := baseOptions(dir)

	for _, cmd := range []string{"source /tmp/evil.sh", ". /tmp/evil.sh"} {
		got := EvaluateCommand(cmd, nil, opts)
		if len(got.Segments) != 1 {
			t.Fatalf("EvaluateCommand(%q): expected 1 segment, got %d", cmd, len(got.Segments))
		}
		if !got.Segments[0].Blind {
			t.Errorf("EvaluateCommand(%q).Segments[0].Blind = false, want true (source/. must keep prompting under Auto)", cmd)
		}
	}
}

// TestEvaluateCommand_BuiltinRuleStillMatches confirms resolveHeadOrBuiltin
// is applied symmetrically to an operator rule's own Binary field: a rule
// authored against "cd" must still match a "cd" segment even though
// neither side resolves against a real PATH entry.
func TestEvaluateCommand_BuiltinRuleStillMatches(t *testing.T) {
	dir := t.TempDir()
	opts := baseOptions(dir)
	rules := []Rule{{Action: ActionDeny, Binary: "cd"}}

	got := EvaluateCommand("cd /etc", rules, opts)
	if got.Action != ActionDeny {
		t.Fatalf("EvaluateCommand(%q) with a deny rule on cd: Action = %v, want ActionDeny", "cd /etc", got.Action)
	}
}

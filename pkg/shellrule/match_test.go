// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package shellrule

import (
	"path/filepath"
	"testing"
)

// baseOptions returns Options wired to the test fixtures, with childPath
// and trustedPath both set to trusted so ordinary (non-look-alike) tests
// don't need to care about the distinction.
func baseOptions(trusted string) Options {
	return Options{
		Platform:     POSIX,
		Segmenter:    fakeSegmenter,
		HeadResolver: fakeHeadResolver,
		Resolve:      ResolveBinary,
		ChildPath:    trusted,
		TrustedPath:  trusted,
	}
}

// TestEvaluateCommand_LookAlikeBinary_DoesNotAutoApprove is the GitHub
// issue #83 "Additional" regression and ADR-092 scenario S24: an `allow`
// rule authored against the real `git`, and an attacker-placed executable
// also named `git` earlier on the CHILD's PATH, must not auto-approve —
// the two resolve to different absolute paths, so the rule does not match.
func TestEvaluateCommand_LookAlikeBinary_DoesNotAutoApprove(t *testing.T) {
	trustedDir := t.TempDir()
	writeFakeExecutable(t, trustedDir, "git") // the "real" git the rule was authored against

	maliciousDir := t.TempDir()
	writeFakeExecutable(t, maliciousDir, "git") // attacker-placed look-alike, earlier on PATH

	childPath := maliciousDir + string(filepath.ListSeparator) + trustedDir // malicious wins PATH search
	trustedPath := trustedDir                                               // the rule resolves against the real location only

	opts := Options{
		Platform:     POSIX,
		Segmenter:    fakeSegmenter,
		HeadResolver: fakeHeadResolver,
		Resolve:      ResolveBinary,
		ChildPath:    childPath,
		TrustedPath:  trustedPath,
	}
	rules := []Rule{{Action: ActionAllow, Binary: "git"}}

	got := EvaluateCommand("git evil-script", rules, opts)

	if len(got.Segments) != 1 {
		t.Fatalf("expected exactly one segment, got %d", len(got.Segments))
	}
	seg := got.Segments[0]
	if seg.MatchedRule != nil {
		t.Errorf("look-alike binary matched the allow rule (MatchedRule=%+v) — issue #83 regression", seg.MatchedRule)
	}
	if seg.Action == ActionAllow {
		t.Errorf("look-alike binary was auto-approved (Action=%q) — issue #83 regression", seg.Action)
	}
	wantResolved, err := ResolveBinary("git", maliciousDir)
	if err != nil {
		t.Fatalf("ResolveBinary: %v", err)
	}
	if seg.ResolvedPath != wantResolved {
		t.Errorf("segment resolved to %q, want the actual (malicious) binary the shell would run %q", seg.ResolvedPath, wantResolved)
	}
}

// TestEvaluateCommand_LookAlikeBinary_BEFORE_would_have_matched documents,
// as a before/after proof, what a resolve-and-VERIFY-less matcher (the old
// EvaluateExec's raw-string behaviour issue #83 described: `"git *"`
// matches any command literally starting with the word `git`) would have
// done with the exact same fixture — auto-approve. It exists purely to
// make the regression's stakes concrete without re-implementing the old
// behaviour as production code.
func TestEvaluateCommand_LookAlikeBinary_BEFORE_would_have_matched(t *testing.T) {
	command := "git evil-script"
	rulePattern := "git" // the pre-ADR-092 shape: a bare string glob, no resolution at all
	head, _, _ := fakeHeadResolver(command)
	if head != rulePattern {
		t.Fatalf("sanity check failed: %q vs %q", head, rulePattern)
	}
	// This is exactly the bug: a string-only comparison says yes, with no
	// regard for which file named "git" would actually run. EvaluateCommand
	// (tested above) rejects the same input because it resolves-and-verifies
	// instead.
}

func TestEvaluateCommand_SimpleAllowMatch(t *testing.T) {
	dir := t.TempDir()
	writeFakeExecutable(t, dir, "git")
	opts := baseOptions(dir)
	rules := []Rule{{Action: ActionAllow, Binary: "git"}}

	got := EvaluateCommand("git status", rules, opts)
	if got.Action != ActionAllow {
		t.Fatalf("Action = %q, want %q", got.Action, ActionAllow)
	}
	if !got.FullyAllowed() {
		t.Error("FullyAllowed() = false, want true for a single fully-matched segment")
	}
}

func TestEvaluateCommand_ArgPrefixTokenBoundary(t *testing.T) {
	dir := t.TempDir()
	writeFakeExecutable(t, dir, "npm")
	opts := baseOptions(dir)
	rules := []Rule{{Action: ActionAllow, Binary: "npm", ArgPrefix: "run test"}}

	if got := EvaluateCommand("npm run test", rules, opts); got.Action != ActionAllow {
		t.Errorf("npm run test: Action = %q, want allow", got.Action)
	}
	if got := EvaluateCommand("npm run testfoo", rules, opts); got.Action == ActionAllow {
		t.Errorf("npm run testfoo: Action = %q, must NOT match a \"run test\" prefix (token boundary)", got.Action)
	}
}

func TestEvaluateCommand_ChainedSegments_DenyBeatsAllowAcrossSegments(t *testing.T) {
	dir := t.TempDir()
	writeFakeExecutable(t, dir, "echo")
	writeFakeExecutable(t, dir, "rm")
	opts := baseOptions(dir)
	rules := []Rule{
		{Action: ActionAllow, Binary: "echo"},
		{Action: ActionDeny, Binary: "rm"},
	}

	got := EvaluateCommand("echo hi && rm -rf /", rules, opts)
	if got.Action != ActionDeny {
		t.Fatalf("Action = %q, want deny (one denied segment denies the whole chain)", got.Action)
	}
	if got.FullyAllowed() {
		t.Error("FullyAllowed() = true, want false when any segment is denied")
	}
	if len(got.Segments) != 2 {
		t.Fatalf("expected 2 segments, got %d: %+v", len(got.Segments), got.Segments)
	}
	if got.Segments[0].Action != ActionAllow {
		t.Errorf("segment 0 (echo hi) Action = %q, want allow", got.Segments[0].Action)
	}
	if got.Segments[1].Action != ActionDeny {
		t.Errorf("segment 1 (rm -rf /) Action = %q, want deny", got.Segments[1].Action)
	}
}

// TestEvaluateCommand_ChainedSegments_PartialCoverageIsNotFullyAllowed is
// FR-020's "each segment must independently clear, or the whole call
// asks": a chain where only PART of it has an explicit allow rule must not
// be usable to silently pre-approve the whole chain.
func TestEvaluateCommand_ChainedSegments_PartialCoverageIsNotFullyAllowed(t *testing.T) {
	dir := t.TempDir()
	writeFakeExecutable(t, dir, "git")
	writeFakeExecutable(t, dir, "curl")
	opts := baseOptions(dir)
	rules := []Rule{{Action: ActionAllow, Binary: "git"}} // curl has no rule at all

	got := EvaluateCommand("git status && curl https://evil.example", rules, opts)
	if got.FullyAllowed() {
		t.Error("FullyAllowed() = true, want false — the curl segment has no matching rule and must not inherit git's allow")
	}
	if got.Segments[1].Action != ActionNone {
		t.Errorf("curl segment Action = %q, want none (no rule applies; ceiling governs it)", got.Segments[1].Action)
	}
}

func TestEvaluateCommand_BlindSpotsRouteToAsk(t *testing.T) {
	dir := t.TempDir()
	writeFakeExecutable(t, dir, "echo")
	writeFakeExecutable(t, dir, "cat")
	opts := baseOptions(dir)
	var rules []Rule // no rules at all — the blind-spot posture must hold regardless

	cases := []struct {
		name    string
		command string
	}{
		{"R12 quote-blind over-split", `echo "a;b"`},
		{"R13 redirection-only segment", "> out"},
		{"R14 brace expansion", "{cat,/etc/passwd}"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := EvaluateCommand(c.command, rules, opts)
			foundBlind := false
			for _, seg := range got.Segments {
				if seg.Blind {
					foundBlind = true
					if seg.Action != ActionAsk {
						t.Errorf("blind segment %q Action = %q, want ask", seg.Segment, seg.Action)
					}
				}
			}
			if !foundBlind {
				t.Errorf("%q: expected at least one segment to be classified as a blind spot", c.command)
			}
			if got.FullyAllowed() {
				t.Errorf("%q: FullyAllowed() = true, want false for a blind-spot command", c.command)
			}
		})
	}
}

func TestEvaluateCommand_AllowRuleRefusesNormalisedHead(t *testing.T) {
	dir := t.TempDir()
	writeFakeExecutable(t, dir, "git")
	opts := baseOptions(dir)
	rules := []Rule{{Action: ActionAllow, Binary: "git"}}

	// "GIT status" resolves to the same absolute binary as "git status" via
	// fakeHeadResolver's case-folding — FR-040 requires an ALLOW rule
	// refuse to fire off that normalised head (a look-alike executable
	// named "GIT" would exploit exactly this).
	got := EvaluateCommand("GIT status", rules, opts)
	if got.Action == ActionAllow {
		t.Errorf("Action = %q, want NOT allow — FR-040 forbids satisfying an allow rule from a case-folded head", got.Action)
	}
}

func TestEvaluateCommand_DenyRuleStillMatchesNormalisedHead(t *testing.T) {
	dir := t.TempDir()
	writeFakeExecutable(t, dir, "rm")
	opts := baseOptions(dir)
	rules := []Rule{{Action: ActionDeny, Binary: "rm"}}

	// FR-040's normalised-head restriction is ALLOW-only: under-matching a
	// DENY is the unsafe direction, so a deny rule must still fire even
	// off a case-folded/dir-stripped head.
	got := EvaluateCommand("RM -rf /", rules, opts)
	if got.Action != ActionDeny {
		t.Errorf("Action = %q, want deny — a deny rule must match even off a normalised head", got.Action)
	}
}

// TestEvaluateCommand_Windows_NoSegmentSplitting proves FR-041: on
// Windows, the Segmenter is never invoked (the whole command is one unit).
func TestEvaluateCommand_Windows_NoSegmentSplitting(t *testing.T) {
	dir := t.TempDir()
	writeFakeExecutable(t, dir, "powershell")
	called := false
	spySegmenter := func(s string) []string {
		called = true
		return fakeSegmenter(s)
	}
	opts := Options{
		Platform:     WindowsPlatform,
		Segmenter:    spySegmenter,
		HeadResolver: fakeHeadResolver,
		Resolve:      ResolveBinary,
		ChildPath:    dir,
		TrustedPath:  dir,
	}

	got := EvaluateCommand("powershell -Command foo; powershell -Command bar", nil, opts)
	if called {
		t.Error("Segmenter was called on Windows — FR-041 requires no chained-segment splitting")
	}
	if len(got.Segments) != 1 {
		t.Fatalf("expected exactly one segment (the whole command), got %d", len(got.Segments))
	}
	if got.Segments[0].Segment != "powershell -Command foo; powershell -Command bar" {
		t.Errorf("segment text was altered: %q", got.Segments[0].Segment)
	}
}

// TestEvaluateCommand_Windows_ExactCommandOnly proves FR-041's other half:
// an ArgPrefix on Windows must match the FULL remaining argument text
// exactly — a proper prefix is not enough ("no prefix option").
func TestEvaluateCommand_Windows_ExactCommandOnly(t *testing.T) {
	dir := t.TempDir()
	writeFakeExecutable(t, dir, "git")
	opts := Options{
		Platform:     WindowsPlatform,
		HeadResolver: fakeHeadResolver,
		Resolve:      ResolveBinary,
		ChildPath:    dir,
		TrustedPath:  dir,
	}
	rules := []Rule{{Action: ActionAllow, Binary: "git", ArgPrefix: "status"}}

	if got := EvaluateCommand("git status", rules, opts); got.Action != ActionAllow {
		t.Errorf("exact match \"git status\": Action = %q, want allow", got.Action)
	}
	if got := EvaluateCommand("git status --verbose", rules, opts); got.Action == ActionAllow {
		t.Errorf("\"git status --verbose\" matched an exact-command rule for \"status\" — FR-041 forbids prefix matching on Windows")
	}
}

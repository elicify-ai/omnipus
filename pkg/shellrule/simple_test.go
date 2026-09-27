// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package shellrule

import "testing"

// TestFullyAllowed_RejectsNonSimpleSegments is the regression test for
// review finding #3 (HIGH): an operator ALLOW rule for `ls` must not let
// FullyAllowed() report true for a segment that resolves a clean "ls" head
// but carries a substitution, redirection, subshell, or env-assignment
// prefix alongside it. Every case here was FullyAllowed()==true on
// b7dc66acf; every case must be false after this fix.
func TestFullyAllowed_RejectsNonSimpleSegments(t *testing.T) {
	dir := t.TempDir()
	writeFakeExecutable(t, dir, "ls")
	opts := baseOptions(dir)
	rules := []Rule{{Action: ActionAllow, Binary: "ls"}}

	cases := []string{
		"LD_PRELOAD=/tmp/evil.so ls",
		"PATH=/tmp/evil:$PATH ls",
		"ls $(go run /tmp/x.go)",
		"ls `make -C /tmp/x`",
		"ls <(npm run evil)",
		"ls > /etc/passwd",
		"(ls)",
	}
	for _, cmd := range cases {
		got := EvaluateCommand(cmd, rules, opts)
		if got.FullyAllowed() {
			t.Errorf("FullyAllowed(%q) = true, want false (finding #3 regression)", cmd)
		}
	}
}

// TestFullyAllowed_StillAllowsAnOrdinaryCommand is the positive control for
// TestFullyAllowed_RejectsNonSimpleSegments: a plain, unadorned command
// still gets the fast path — the fix must not turn Auto into Ask for
// everything.
func TestFullyAllowed_StillAllowsAnOrdinaryCommand(t *testing.T) {
	dir := t.TempDir()
	writeFakeExecutable(t, dir, "ls")
	opts := baseOptions(dir)
	rules := []Rule{{Action: ActionAllow, Binary: "ls"}}

	got := EvaluateCommand("ls -la /tmp", rules, opts)
	if !got.FullyAllowed() {
		t.Fatalf("FullyAllowed(%q) = false, want true for a plain allowed command", "ls -la /tmp")
	}
}

// TestFullyAllowed_WindowsRejectsChainedMetacharacters is the Windows half
// of finding #3: EvaluateCommand skips segmentation entirely on Windows
// (Platform==WindowsPlatform judges the whole command as one segment), so
// "ls; Remove-Item -Recurse C:\x" resolved a clean head of "ls" and
// FullyAllowed reported true even though a second, unrelated command rides
// after the separator.
func TestFullyAllowed_WindowsRejectsChainedMetacharacters(t *testing.T) {
	dir := t.TempDir()
	writeFakeExecutable(t, dir, "ls")
	opts := baseOptions(dir)
	opts.Platform = WindowsPlatform
	rules := []Rule{{Action: ActionAllow, Binary: "ls"}}

	cases := []string{
		`ls; Remove-Item -Recurse C:\x`,
		"ls & del /s x",
	}
	for _, cmd := range cases {
		got := EvaluateCommand(cmd, rules, opts)
		if got.FullyAllowed() {
			t.Errorf("Windows FullyAllowed(%q) = true, want false (finding #3 regression)", cmd)
		}
	}
}

// TestIsSimpleSegment_DirectCases pins IsSimpleSegment's own contract
// beyond what the FullyAllowed integration tests above exercise.
func TestIsSimpleSegment_DirectCases(t *testing.T) {
	cases := []struct {
		seg  string
		want bool
	}{
		{"ls -la", true},
		{"git commit -m msg", true},
		{"ls > out", false},
		{"ls < in", false},
		{"(ls)", false},
		{"ls $(x)", false},
		{"ls `x`", false},
		{"VAR=1 ls", false},
		{"FOO=bar BAZ=qux ls", false},
	}
	for _, tc := range cases {
		got := IsSimpleSegment(tc.seg, POSIX)
		if got != tc.want {
			t.Errorf("IsSimpleSegment(%q, POSIX) = %v, want %v", tc.seg, got, tc.want)
		}
	}
}

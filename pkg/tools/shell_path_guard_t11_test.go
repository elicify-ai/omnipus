// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// RED pack for T11 slice A, shell-guard narrow fix (#882).
//
// Plan (elicify-test-writing step 1). Oracles are the slim spec
// docs/internal/specs/t11-documents-and-3p-agents-spec.md §3.1 and §8 FR-1…FR-3.
// The full spec's BDD-10 / SEC-10 / SEC-11 / SEC-12 / SEC-13 datasets are reused
// where they are the same rule. Expectations were not taken from a guard run.
//
// Behaviour: in the outside-working-folder branch, a candidate is not refused
// only when all six §3.1 conditions hold. Everything else, including "../",
// stays refused. The rule is off when the root is writable (always on Windows).
//
// REAL: ExecTool.guardCommand (the production text guard).
// MOCK: none. Root writability is measured with the spec's unix check, not stubbed.
//
// TESTABILITY: the full spec's "root-writable probe injected" seam is not in
// this tree. Forcing "root writable" on a non-root Unix process needs a
// same-package function variable the test can set. This pack does not add
// production code. Windows always takes the off branch (condition 5). A
// non-root Unix host takes the on branch, which is the failing case today.
package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

// t11Literal is one quoted absolute literal and the command text that holds it.
// first is the filesystem object directly under the root ("/api" for
// "/api/v1/users"): spec §3.1 condition 4.
type t11Literal struct {
	name  string
	cmd   string
	first string
}

// t11StandAloneLiterals are the commands the spec says are not refused when
// the other conditions hold (slim §3.1, §8 FR-1, full spec BDD-10 and AD-7).
// The guaranteed-absent top folder is filled in by the caller so condition 4
// does not depend on whether /api happens to exist on the host.
func t11StandAloneLiterals(absent string) []t11Literal {
	heredoc := "python3 <<'PY'\n" +
		"d = {\"api\": \"" + absent + "/v1\"}\n" +
		"PY\n"
	// Condition 6 skips whitespace, including newlines, before applying the
	// allowed-neighbour set. "(" then a newline then the quote is still "(".
	parenNewline := "python3 -c \"(\n'" + absent + "/a'\n)\""
	return []t11Literal{
		{
			name:  "print quoted api route",
			cmd:   `python3 -c "print('/api/v1/users')"`,
			first: "/api",
		},
		{
			name:  "assignment quoted healthz",
			cmd:   `python3 -c "x = '/healthz'"`,
			first: "/healthz",
		},
		{
			name:  "heredoc dict value",
			cmd:   heredoc,
			first: absent,
		},
		{
			name:  "guaranteed absent top folder",
			cmd:   fmt.Sprintf(`python3 -c "print('%s/v1/users')"`, absent),
			first: absent,
		},
		{
			name:  "newline between paren and quote",
			cmd:   parenNewline,
			first: absent,
		},
	}
}

func t11FirstExists(first string) bool {
	_, err := os.Lstat(first)
	return err == nil
}

// t11WantStandAloneRefused is the six-condition rule as a boolean.
// Refused unless EVERY condition that this case can vary holds:
// condition 5 (root not writable) and condition 4 (first component absent).
// The commands in t11StandAloneLiterals are built so conditions 1, 2, 3 and 6 hold.
func t11WantStandAloneRefused(t *testing.T, first string) bool {
	t.Helper()
	if t11RootIsWritable(t) {
		return true
	}
	return t11FirstExists(first)
}

func t11AbsentTop(t *testing.T) string {
	t.Helper()
	first := fmt.Sprintf("/t11-absent-%d", os.Getpid())
	if t11FirstExists(first) {
		t.Fatalf("fixture: %s exists, so condition 4 would not hold for the guaranteed-absent case", first)
	}
	return first
}

func t11AssertStandAlone(t *testing.T, tool *ExecTool, cwd string, c t11Literal) {
	t.Helper()
	got := tool.guardCommand(context.Background(), c.cmd, cwd)
	if t11WantStandAloneRefused(t, c.first) {
		require.Contains(t, got, "path outside working dir",
			"spec §3.1 / FR-3: root writable or first component %s exists, so %s stays refused\ncommand: %s",
			c.first, c.name, c.cmd)
		return
	}
	require.Empty(t, got,
		"spec §3.1 / FR-1: stand-alone quoted literal whose top folder cannot exist must not be refused\ncommand: %s\nrefusal: %s",
		c.cmd, got)
}

// TestGuard_StandAloneQuotedLiteralsNotRefused is FR-1 (and the on-branch of
// FR-3 when this host's root is writable).
func TestGuard_StandAloneQuotedLiteralsNotRefused(t *testing.T) {
	tool, cwd := guardFixture(t)
	for _, c := range t11StandAloneLiterals(t11AbsentTop(t)) {
		t.Run(c.name, func(t *testing.T) {
			t11AssertStandAlone(t, tool, cwd, c)
		})
	}
}

// TestGuard_RootWritableProbeBothWays is FR-3 with the probe forced both
// ways, so the result does not depend on this host. CHECK M03 (the veto
// compiled out) must fail the writable-root case on a normal non-root Mac.
func TestGuard_RootWritableProbeBothWays(t *testing.T) {
	tool, cwd := guardFixture(t)
	absent := t11AbsentTop(t)
	cmd := fmt.Sprintf(`python3 -c "print('%s/v1/users')"`, absent)
	concat := `python3 -c "open('/tm' 'p/x','w')"`

	t.Run("writable root refuses the stand-alone literal", func(t *testing.T) {
		restore := t11ForceRootWritable(t, true)
		defer restore()
		got := tool.guardCommand(context.Background(), cmd, cwd)
		require.Contains(t, got, "path outside working dir",
			"spec §3.1 condition 5: when the root is writable the stand-alone rule is off")
	})
	t.Run("unwritable root allows the stand-alone literal and still refuses concatenation", func(t *testing.T) {
		restore := t11ForceRootWritable(t, false)
		defer restore()
		got := tool.guardCommand(context.Background(), cmd, cwd)
		require.Empty(t, got,
			"spec §3.1 / FR-1: with the root not writable, a stand-alone absent literal is not refused\nrefusal: %s", got)
		t11RequireRefused(t, tool, cwd, concat, "path outside working dir", "adjacent literals stay refused when the exemption is on")
	})
}

// t11ForceRootWritable sets the production probe for one test and returns
// the restore func. Tests that use it must not run in parallel.
func t11ForceRootWritable(t *testing.T, writable bool) func() {
	t.Helper()
	old := rootWritableProbe
	rootWritableProbe = func() bool { return writable }
	return func() { rootWritableProbe = old }
}

// TestGuard_BraceConcatenationStillRefused is S5. A quoted fragment inside a
// shell word that brace-expands into a real outside path stays refused,
// including when that word is nested under bash -c or sh -c. The commands are
// the ones from the gate receipt. The probe is forced off so a writable root
// cannot hide a broken text check, and a plain stand-alone literal stays allowed.
func TestGuard_BraceConcatenationStillRefused(t *testing.T) {
	tool, cwd := guardFixture(t)
	restore := t11ForceRootWritable(t, false)
	defer restore()
	absent := t11AbsentTop(t)
	plain := fmt.Sprintf(`python3 -c "print('%s/v1/users')"`, absent)
	got := tool.guardCommand(context.Background(), plain, cwd)
	require.Empty(t, got, "spec §3.1: a plain stand-alone literal stays allowed\nrefusal: %s", got)

	refused := []string{
		`touch {'/tm',}p/t11-proof`,
		`bash -c "touch {'/tm',}p/t11-proof"`,
		`sh -c "touch {'/tm',}p/t11-proof"`,
		`touch '/tmp/t11-proof'`,
	}
	for _, cmd := range refused {
		t.Run(cmd, func(t *testing.T) {
			t11RequireRefused(t, tool, cwd, cmd, "path outside working dir", "shell concatenation or a real outside path")
		})
	}
}

// TestGuard_RuleOffWhenRootWritable is FR-3. On a non-root Unix host the
// spec's "root is writable" input is not this process, so the assertion is
// the complementary one the same sentence requires: the rule stays on.
func TestGuard_RuleOffWhenRootWritable(t *testing.T) {
	tool, cwd := guardFixture(t)
	c := t11StandAloneLiterals(t11AbsentTop(t))[3] // guaranteed-absent top
	t11AssertStandAlone(t, tool, cwd, c)
	if runtime.GOOS == "windows" && !t11RootIsWritable(t) {
		t.Fatal("spec §3.1 condition 5: on Windows the root always counts as writable")
	}
}

// TestRootWritableProbe_PerPlatform is the per-platform probe the spec names.
// The probe function is not given a Go name; the observable is the guard
// decision against the spec's effective-access check (Unix) or the constant
// "writable" (Windows).
func TestRootWritableProbe_PerPlatform(t *testing.T) {
	if runtime.GOOS == "windows" {
		require.True(t, t11RootIsWritable(t),
			"spec §3.1 condition 5: on Windows the root always counts as writable, so the rule is off")
	}
	tool, cwd := guardFixture(t)
	c := t11StandAloneLiterals(t11AbsentTop(t))[3]
	t11AssertStandAlone(t, tool, cwd, c)
}

func t11RequireRefused(t *testing.T, tool *ExecTool, cwd, cmd, needle, why string) {
	t.Helper()
	got := tool.guardCommand(context.Background(), cmd, cwd)
	require.NotEmpty(t, got, "spec FR-2: %s must stay refused\ncommand: %s", why, cmd)
	require.Contains(t, got, needle,
		"spec FR-2: refusal must stay the existing one (%s)\ncommand: %s\ngot: %s", needle, cmd, got)
}

// TestGuard_RealOutsideWritesStillRefused is FR-2 / SEC-10 / SEC-12.
// These commands are refused today; the new rule must not start allowing them.
func TestGuard_RealOutsideWritesStillRefused(t *testing.T) {
	tool, cwd := guardFixture(t)

	outside := "path outside working dir"
	writes := []struct {
		name, cmd, why string
	}{
		{"open etc", `python3 -c "open('/etc/x','w')"`, "a real write under /etc"},
		{"redirect home", `echo x > ~/x`, "a write through ~/"},
		{"cp tmp", `cp a /tmp/x`, "a copy onto /tmp"},
	}
	if runtime.GOOS == "windows" {
		writes[0] = struct{ name, cmd, why string }{
			"open windows dir",
			`python3 -c "open('C:\\Windows\\t11probe','w')"`,
			"a real write under C:\\Windows",
		}
	}
	for _, c := range writes {
		t.Run(c.name, func(t *testing.T) {
			t11RequireRefused(t, tool, cwd, c.cmd, outside, c.why)
		})
	}

	t.Run("sibling of working folder", func(t *testing.T) {
		sibling := filepath.Join(filepath.Dir(cwd), "t11-sibling", "x")
		cmd := fmt.Sprintf(`python3 -c "open('%s','w')"`, sibling)
		t11RequireRefused(t, tool, cwd, cmd, outside, "a write to a sibling of the working folder")
	})

	t.Run("command position stays refused", func(t *testing.T) {
		// Spec §3.1: command-position handling runs unchanged and first.
		// /bin/ls is an absolute program path; executing it is not a read.
		cmd := "/bin/ls"
		if runtime.GOOS == "windows" {
			cmd = `C:\Windows\System32\cmd.exe`
		}
		t11RequireRefused(t, tool, cwd, cmd, "RUNNING a program", "command position")
	})

	t.Run("unresolvable expansion stays refused", func(t *testing.T) {
		// SEC-12. $FOO is not ~ / $HOME / $OMNIPUS_HOME, so the guard cannot
		// know the real path. Spec: the unresolvable-path refusal stays.
		t11RequireRefused(t, tool, cwd, `cat $FOO/api/v1`, "unresolvable path", "expansion the guard cannot resolve")
	})

	t.Run("protected internal file stays refused", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("OMNIPUS_HOME", home)
		cred := filepath.Join(home, "credentials.json")
		require.NoError(t, os.WriteFile(cred, []byte(`{"k":"v"}`), 0o600))
		cmd := "cat '" + cred + "'"
		t11RequireRefused(t, tool, cwd, cmd, "protected internal files", "credentials.json inside OMNIPUS_HOME")
	})
}

// TestGuard_FragmentsConcatenationTraversalRefused is FR-2 / SEC-13 / AD-7
// condition 6. Each command fails at least one of the six conditions, so the
// outside-folder refusal (or the "../" veto) stays.
func TestGuard_FragmentsConcatenationTraversalRefused(t *testing.T) {
	tool, cwd := guardFixture(t)
	outside := "path outside working dir"
	cases := []struct {
		name, cmd, needle, why string
	}{
		{"adjacent literals", `python3 -c "open('/tm' 'p/x','w')"`, outside, "adjacent literals are not stand-alone"},
		{"adjacent across newline", "python3 -c \"open('/tm'\n 'p/x','w')\"", outside, "adjacent literals across a newline"},
		{"plus concatenation", `python3 -c "open('/tm' + 'p/x','w')"`, outside, "+ is not an allowed neighbour"},
		{"f-string", `python3 -c "print(f'{base}/api/items')"`, outside, "not the entire quoted content"},
		{"percent template", `python3 -c "print('/%s/%d' % ('a',1))"`, outside, "template character %"},
		{"glob", `python3 -c "import glob; open(glob.glob('/Us*/x')[0],'w')"`, outside, "glob character"},
		{"brackets", `python3 -c "print('/api/[a]')"`, outside, "bracket"},
		{"backslash escape", `python3 -c "print('/api\x2fv1')"`, outside, "backslash"},
		{"prose around a path", `python3 -c "print('see /api/v1 docs')"`, outside, "not the entire quoted content"},
		{"format call", `python3 -c "open('/{}'.format('tmp'),'w')"`, outside, "brace and method call"},
		// Spec §3.1: "{" is forbidden in a stand-alone literal, and a "/"
		// immediately followed by "{" is an outside path (shell brace form).
		{"root brace expansion", `cp x /{tmp,etc}/y`, outside, "a slash immediately followed by { is an outside path"},
		{"raw prefix", `python3 -c "open(r'/tmpx','w')"`, outside, "letter before the quote"},
		{"line continuation", "python3 -c \"open('/tm' \\\n'p/x','w')\"", outside, "backslash continuation"},
		{"parent traversal", `python3 -c "print('../shared/x')"`, "path traversal detected", "../ stays refused"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t11RequireRefused(t, tool, cwd, c.cmd, c.needle, c.why)
		})
	}
}

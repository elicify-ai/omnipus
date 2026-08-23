package tools

// Regression tests for the command-TEXT absolute-path scan in
// ExecTool.guardCommand (see absolutePathPattern).
//
// These pin two false positives that made ordinary developer commands
// unrunnable. In one observed real session, 17 of 20 bash calls failed and
// every failure came from this guard or from the chained-command blast radius
// it creates — the agent could not even run `which node` to discover its own
// environment.
//
// Neither false positive was covered by any existing test, which is exactly
// why they shipped. The true-positive cases below are equally important: they
// prove the fix is a precision change, not a weakening of the guard.
//
// A follow-up review found the original boundary-narrowing fix over-narrowed
// detection: `~/…`, `$HOME/…`, attached short flags (`-o/etc/passwd`), and
// shell-list punctuation (`{,}[]!`) all produced NO candidate at all, so the
// command was silently ALLOWED instead of blocked. A separate, pre-existing
// false positive also survived: `PATH=/usr/bin:/usr/local/bin make` was
// blocked because the `:`-joined value was captured as one malformed
// candidate. The additional true-positive cases below cover the former; the
// additional false-positive cases cover the latter (colon-joined path lists)
// plus a regression guard for the attached-flag fix itself (a hyphenated
// relative path segment like `build-x/output` must not be mistaken for a
// `-x` flag with an attached absolute path).
//
// A SECOND follow-up review found that the colon-list carve-out above had
// swung too far the other way: it skipped a matching candidate WHOLESALE,
// so none of its `:`-separated segments was checked against the workspace
// boundary at all. That made `cat /etc/passwd:/etc/hosts`,
// `PATH=/etc/shadow:/usr/bin make`, `PATH=/usr/bin:/etc/shadow make`, and
// `cat ~/.ssh/id_rsa:/dev/null` — every one of them BLOCKED before the
// colon-list carve-out existed — silently ALLOWED. guardCommand now splits a
// colon-list candidate on `:` and checks each segment independently (see
// checkPathSegment), so those four shapes are blocked again below in
// TestGuardCommand_TruePositivesStillBlocked. That fix also flips the
// original `PATH=/usr/bin:/usr/local/bin make` and the compiler-flag
// false-positive case from "allowed" to "blocked": both reference segments
// (`/usr/bin`, `/usr/local/bin`, `/usr/include`, `/usr/local/include`) sit
// outside the test workspace, exactly like the bare `/usr/local/bin/node`
// candidate in TestGuardCommand_TruePositivesStillBlocked — so per-segment
// checking blocks them for the same reason a single such candidate would be
// blocked today. They have moved to TestGuardCommand_TruePositivesStillBlocked
// accordingly; a new false-positive case below proves a colon-list whose
// segments are ALL inside the workspace still passes.
//
// ADR-068 (2026-08-23) RETARGETED SEVEN CASES. The founder ruled (§2.1 option
// A) that reads outside the working directory are allowed and only writes still
// require a mount. Seven cases in TestGuardCommand_TruePositivesStillBlocked
// asserted the opposite — that a READ of an outside path is blocked — which was
// the shipped behaviour ADR-068 §1.1 tabulates as wrong. They were not deleted:
// each one now appears verbatim in TestGuardCommand_ReadsOutsideWorkDirAllowed
// (shell_readwrite_guard_test.go) with the corrected oracle, and its WRITE
// counterpart is pinned below in
// TestGuardCommand_ADR068RetargetedCases_WriteHalfStillBlocked so this file
// keeps proving that the same path is still protected against writes:
//
//	ls /usr/local/bin/node /opt/homebrew/bin/node   (was "absolute path outside workspace")
//	cat /etc/passwd                                 (was "read of a system file")
//	cat ~/.ssh/id_rsa                               (was "home-directory tilde expansion")
//	cat $HOME/.ssh/id_rsa                           (was "HOME variable expansion")
//	cat /etc/passwd:evil                            (was "stray colon suffix …")
//	cat /etc/passwd:/etc/hosts                      (was "colon-joined list …")
//	cat ~/.ssh/id_rsa:/dev/null                     (was "tilde-expanded colon-joined list …")
//
// Everything else in TestGuardCommand_TruePositivesStillBlocked survives
// untouched, and that matters: the `../` traversal case, the two
// command-position cases and the attached-flag cases are the tripwires that
// prove ADR-068 opened reads and nothing else.

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// guardFixture builds an ExecTool restricted to a temp workspace and returns
// the tool plus a cwd inside that workspace, mirroring how the bash tool is
// constructed in production (restrict=true).
func guardFixture(t *testing.T) (*ExecTool, string) {
	t.Helper()
	ws := t.TempDir()
	tool, err := NewExecTool(ws, true)
	require.NoError(t, err)
	// Resolve symlinks: on macOS t.TempDir() lives under /var -> /private/var,
	// and the guard compares against filepath.Abs(cwd). Without this the
	// fixture itself would look like it points outside the workspace.
	resolvedWS, err := filepath.EvalSymlinks(ws)
	require.NoError(t, err)
	return tool, resolvedWS
}

// TestGuardCommand_FalsePositives covers commands that MUST be allowed. Every
// one of these was rejected before the fix.
func TestGuardCommand_FalsePositives(t *testing.T) {
	tool, cwd := guardFixture(t)

	cases := []struct {
		name string
		cmd  string
		why  string
	}{
		{
			name: "redirect followed by semicolon",
			cmd:  `which node 2>/dev/null; echo done`,
			why:  "`2>/dev/null;` used to extract the candidate `/dev/null;` (with the semicolon), missing the safePaths exemption",
		},
		{
			name: "redirect followed by and-and",
			cmd:  `node --version 2>/dev/null && echo ok`,
			why:  "same defect via && instead of ;",
		},
		{
			name: "redirect followed by pipe",
			cmd:  `ls -la 2>/dev/null | head -5`,
			why:  "same defect via a pipe",
		},
		{
			name: "redirect followed by close paren",
			cmd:  `(cat missing 2>/dev/null) || true`,
			why:  "same defect via a subshell close-paren",
		},
		{
			name: "relative output path containing a slash",
			cmd:  `curl -sL -o build/app.min.js https://example.com/app.min.js`,
			why:  "the slash INSIDE the relative path used to be read as the start of an absolute path `/app.min.js`",
		},
		{
			name: "relative path argument with nested directories",
			cmd:  `cp src/index.html dist/index.html`,
			why:  "two relative paths, neither absolute; must not be re-sliced into fabricated root paths",
		},
		{
			name: "chained environment discovery",
			cmd:  `which node npm npx pnpm yarn bun 2>/dev/null; python3 --version 2>/dev/null; go version 2>/dev/null`,
			why:  "the exact discovery command an agent needs to find its toolchain",
		},
		{
			name: "web url is still exempt",
			cmd:  `curl -sS https://unpkg.com/react@18/umd/react.production.min.js -o vendor/react.js`,
			why:  "the http(s) scheme carve-out must survive the boundary-group change",
		},
		{
			name: "attached-flag detection does not misfire on a relative flag arg",
			cmd:  `curl -sL -o build/app.min.js https://unpkg.com/react@18/umd/react.production.min.js`,
			why:  "the space between -o and build/app.min.js must prevent the attached-flag alternative from firing (contrast with -o/etc/passwd, which has no space)",
		},
		{
			name: "hyphenated relative path segment is not mistaken for an attached flag",
			cmd:  `ls build-x/output`,
			why:  "the attached-flag alternative requires the '-X' to sit at a token start (start of command or after whitespace); 'd' precedes '-x' here, not whitespace, so 'build-x/output' must not be re-sliced into a fabricated '/output'",
		},
		{
			name: "colon-joined path list entirely inside the workspace",
			cmd:  `PATH=` + filepath.Join(cwd, "bin") + `:` + filepath.Join(cwd, "lib") + ` make`,
			why:  "a colon-joined list is recognized as that shape (not fragmented into malformed single-path candidates) and, since every segment resolves inside the workspace, must be allowed",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tool.guardCommand(context.Background(), tc.cmd, cwd)
			require.Empty(t, got, "command must be allowed (%s)\ncommand: %s", tc.why, tc.cmd)
		})
	}
}

// TestGuardCommand_TruePositivesStillBlocked proves the fix narrowed precision
// without removing protection. If any of these starts passing, the guard has
// been weakened rather than corrected.
func TestGuardCommand_TruePositivesStillBlocked(t *testing.T) {
	tool, cwd := guardFixture(t)

	cases := []struct {
		name string
		cmd  string
		want string
	}{
		{
			name: "parent traversal",
			cmd:  `cat ../../../../etc/passwd`,
			want: "path traversal detected",
		},
		{
			name: "absolute path after an operator",
			cmd:  `echo hi;/etc/shadow`,
			want: "path outside working dir",
		},
		{
			name: "absolute path after a pipe",
			cmd:  `echo hi|/bin/sh`,
			want: "path outside working dir",
		},
		{
			name: "sudo stays denied",
			cmd:  `sudo -n true`,
			want: "dangerous pattern detected",
		},
		{
			name: "rm -rf stays denied",
			cmd:  `rm -rf /`,
			want: "dangerous pattern detected",
		},
		{
			name: "attached short flag (curl -o)",
			cmd:  `curl -o/etc/passwd https://x`,
			want: "path outside working dir",
		},
		{
			name: "attached short flag (gcc -I)",
			cmd:  `gcc -I/etc/passwd -o x`,
			want: "path outside working dir",
		},
		{
			name: "attached short flag (tar -C)",
			cmd:  `tar -C/etc -cf a.tar .`,
			want: "path outside working dir",
		},
		{
			name: "brace-list with two absolute paths",
			cmd:  `cat {/etc/shadow,/etc/passwd}`,
			want: "path outside working dir",
		},
		{
			name: "brace-list with empty first alternative",
			cmd:  `cat {,/etc/shadow}`,
			want: "path outside working dir",
		},
		{
			name: "comma-separated absolute path",
			cmd:  `echo x,/etc/shadow`,
			want: "path outside working dir",
		},
		{
			name: "bracket-adjacent absolute path",
			cmd:  `cat[/etc/shadow]`,
			want: "path outside working dir",
		},
		{
			name: "PATH assignment with a colon-joined absolute path list, first segment out of workspace",
			cmd:  `PATH=/etc/shadow:/usr/bin make`,
			want: "path outside working dir",
		},
		{
			name: "PATH assignment with a colon-joined absolute path list, second segment out of workspace",
			cmd:  `PATH=/usr/bin:/etc/shadow make`,
			want: "path outside working dir",
		},
		{
			name: "compiler include flag with a colon-joined path list, both segments out of workspace",
			cmd:  `gcc -Ia:b -I/usr/include:/usr/local/include -c foo.c`,
			want: "path outside working dir",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tool.guardCommand(context.Background(), tc.cmd, cwd)
			require.NotEmpty(t, got, "command must be blocked: %s", tc.cmd)
			require.Contains(t, got, tc.want, "wrong rejection reason for: %s", tc.cmd)
		})
	}
}

// TestGuardCommand_ADR068RetargetedCases_WriteHalfStillBlocked is the other
// half of the seven cases ADR-068 retargeted (see this file's header). Each
// command names the SAME outside path as the read case it replaces, in a
// context that writes to it.
//
// The point of keeping these here, next to the cases they came from, is that
// "the read was opened" and "the path is still protected" are two different
// claims and the second one must not quietly stop being tested when the first
// changes. If a case here starts passing, ADR-068 has been implemented as
// "outside paths are open" rather than "outside READS are open".
func TestGuardCommand_ADR068RetargetedCases_WriteHalfStillBlocked(t *testing.T) {
	tool, cwd := guardFixture(t)

	cases := []struct {
		name string
		cmd  string
		was  string
	}{
		{
			name: "write to a path in an outside bin directory",
			cmd:  `printf x > /usr/local/bin/node`,
			was:  "ls /usr/local/bin/node /opt/homebrew/bin/node",
		},
		{
			name: "write to a system file",
			cmd:  `printf x > /etc/passwd`,
			was:  "cat /etc/passwd",
		},
		{
			name: "write through a tilde expansion",
			cmd:  `printf x > ~/.ssh/id_rsa`,
			was:  "cat ~/.ssh/id_rsa",
		},
		{
			name: "write through a HOME expansion",
			cmd:  `printf x > $HOME/.ssh/id_rsa`,
			was:  "cat $HOME/.ssh/id_rsa",
		},
		{
			name: "write to an absolute path with a stray colon suffix",
			cmd:  `printf x > /etc/passwd:evil`,
			was:  "cat /etc/passwd:evil",
		},
		{
			name: "write to every member of a colon-joined list",
			cmd:  `tee /etc/passwd:/etc/hosts`,
			was:  "cat /etc/passwd:/etc/hosts",
		},
		{
			name: "write to a tilde-expanded colon-joined list",
			cmd:  `tee ~/.ssh/id_rsa:/dev/null`,
			was:  "cat ~/.ssh/id_rsa:/dev/null",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tool.guardCommand(context.Background(), tc.cmd, cwd)
			require.NotEmpty(t, got,
				"the write half of the retargeted case %q must stay blocked: %s", tc.was, tc.cmd)
			require.Contains(t, got, "path outside working dir",
				"wrong rejection reason for: %s", tc.cmd)
			require.Contains(t, got, "RestrictToWorkspace",
				"ADR-068 §6: the denial must name the rule that fired, so an operator is not driven to disable the whole boundary")
		})
	}
}

// TestGuardCommand_SafePathsExemptionSurvivesMetacharacters is the narrow unit
// guard for defect 1. It asserts the property directly rather than through a
// whole command, so a future change to the regex that re-glues trailing
// operators onto the path fails here with an obvious message.
func TestGuardCommand_SafePathsExemptionSurvivesMetacharacters(t *testing.T) {
	tool, cwd := guardFixture(t)

	for _, suffix := range []string{"", ";", " ;", "&&  echo x", "| head -1", ")"} {
		cmd := "echo hi 2>/dev/null" + suffix
		require.Empty(t, tool.guardCommand(context.Background(), cmd, cwd),
			"/dev/null must stay exempt when followed by %q", suffix)
	}
}

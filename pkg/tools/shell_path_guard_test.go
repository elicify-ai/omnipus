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
			name: "absolute path outside workspace",
			cmd:  `ls /usr/local/bin/node /opt/homebrew/bin/node`,
			want: "path outside working dir",
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
			name: "read of a system file",
			cmd:  `cat /etc/passwd`,
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
			name: "home-directory tilde expansion",
			cmd:  `cat ~/.ssh/id_rsa`,
			want: "path outside working dir",
		},
		{
			name: "HOME variable expansion",
			cmd:  `cat $HOME/.ssh/id_rsa`,
			want: "path outside working dir",
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
			name: "single absolute path with a stray colon suffix is not exempted as a colon list",
			cmd:  `cat /etc/passwd:evil`,
			want: "path outside working dir",
		},
		{
			name: "colon-joined list of two out-of-workspace paths",
			cmd:  `cat /etc/passwd:/etc/hosts`,
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
			name: "tilde-expanded colon-joined list mixing a real path with a safe device",
			cmd:  `cat ~/.ssh/id_rsa:/dev/null`,
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

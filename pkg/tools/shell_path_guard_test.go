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

import (
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
	real, err := filepath.EvalSymlinks(ws)
	require.NoError(t, err)
	return tool, real
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
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tool.guardCommand(tc.cmd, cwd)
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
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tool.guardCommand(tc.cmd, cwd)
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
		require.Empty(t, tool.guardCommand(cmd, cwd),
			"/dev/null must stay exempt when followed by %q", suffix)
	}
}

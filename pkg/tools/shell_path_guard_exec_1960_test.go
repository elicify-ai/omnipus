// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

// Issue #1960, slice S0 (ADR "Agents run command-line programs with stored
// credentials and guided logins", decision D1; requirements FR-1 and FR-6).
//
// Oracle, from the ADR and the issue, not from the code:
//   - FR-1: running a program by its absolute path is NOT refused only because
//     the file lies outside the workspace. The program word is found after
//     leading `NAME=value` assignments and known command wrappers (env,
//     timeout, nohup, ...), so `VAR=val /path/prog` and `env /path/prog` run.
//   - Unchanged: protected-file checks, write checks on every other operand
//     (assignment values, wrapper flag values, arguments, redirect targets),
//     workspace and mount execution.
//   - FR-6: a path-guard refusal names the guard rule and does not advise
//     copying programs, switching the guard off, or how to hide a path from
//     the text scan. The operator remedy goes to the gateway log instead.
//   - FR-6: a program's own error output passes through unchanged.

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// marker1960 is the outside write target of CHECK F1's probe rows. The guard
// only reads command text; no test here runs these commands.
const marker1960 = "/tmp/omnipus-probe-marker-1960"

func skipOnWindows1960(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("#1960 S0 rows use POSIX absolute paths; Windows program paths are covered by the T11 command-position row")
	}
}

// TestGuard1960_ProgramByAbsolutePathRuns is FR-1: each command runs a
// program by its absolute path from outside the workspace and must pass the
// text guard.
func TestGuard1960_ProgramByAbsolutePathRuns(t *testing.T) {
	skipOnWindows1960(t)
	tool, cwd := guardFixture(t)
	outside := sweepTempDir(t)
	prog := filepath.Join(outside, "bin", "gh")

	cases := []struct{ name, cmd string }{
		{"issue repro: absolute gh, two segments", "/usr/local/bin/gh auth status; /usr/local/bin/gh api user --jq .login"},
		{"same program by name", "gh auth status; gh api user --jq .login"},
		{"outside program with arguments", prog + " --version"},
		{"double-quoted program path", `"` + prog + `" --version`},
		{"single-quoted program path", `'` + prog + `' --version`},
		{"assignment prefix with a workspace value", "GH_CONFIG_DIR=" + filepath.Join(cwd, "ghcfg") + " " + prog + " auth status"},
		{"two assignment prefixes", "A=1 B=2 " + prog + " auth status"},
		{"env wrapper", "env " + prog + " auth status"},
		{"env wrapper with flags and assignment", "env -i FOO=1 " + prog + " auth status"},
		{"wrapper named by absolute path", "/usr/bin/env " + prog + " auth status"},
		{"timeout wrapper with duration", "timeout 5 " + prog + " auth status"},
		{"nohup wrapper in background", "nohup " + prog + " auth status &"},
		{"time wrapper", "time " + prog + " --version"},
		{"nested wrappers", "env timeout 5 nohup " + prog + " --version"},
		{"after a semicolon", "echo hi;" + prog + " --version"},
		{"mid-pipeline", "echo hi | " + prog + " api user"},
		{"after cd and &&", "cd " + cwd + " && " + prog + " --version"},
		{"with stderr redirect to /dev/null", prog + " --version 2>/dev/null"},
		{"stdout redirect into the workspace", prog + " --version > " + filepath.Join(cwd, "out.txt")},
		{"after a shell keyword", "if " + prog + " --version; then echo ok; fi"},
		// CHECK F2 (check-brace-probe.log): quoted braces are not shell brace
		// expansion, so they must not stop the program word from running.
		{"quoted braces in a jq argument (CHECK F2)", "/usr/local/bin/gh api user --jq '{login:.login}'"},
		{"quoted empty braces (CHECK F2)", "/bin/echo '{}'"},
		{"plain echo control (CHECK F2)", "/bin/echo hi"},
		{"same jq argument by name (CHECK F2 control)", "gh api user --jq '{login:.login}'"},
		{"brace expansion after the program word", "/usr/local/bin/gh api {user,orgs}"},
		{"absolute shell mid-pipeline (was pinned refused before #1960)", "echo hi|/bin/sh"},
		{"absolute program after semicolon (was pinned refused before #1960)", "echo hi;/etc/shadow"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := tool.guardCommand(context.Background(), c.cmd, cwd)
			require.Empty(t, got, "FR-1: running a program by absolute path must not be refused only for lying outside the workspace\ncommand: %s", c.cmd)
		})
	}
}

// TestGuard1960_SymlinkedProgramRuns is the S0 "symlinked program" row: a
// program reached through a symlinked folder outside the workspace runs, the
// same as its real path.
func TestGuard1960_SymlinkedProgramRuns(t *testing.T) {
	skipOnWindows1960(t)
	tool, cwd := guardFixture(t)
	outside := sweepTempDir(t)
	realDir := filepath.Join(outside, "real")
	require.NoError(t, os.MkdirAll(realDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(realDir, "tool"), []byte("#!/bin/sh\nexit 0\n"), 0o755))
	link := filepath.Join(outside, "link")
	require.NoError(t, os.Symlink(realDir, link))

	for _, p := range []string{filepath.Join(realDir, "tool"), filepath.Join(link, "tool")} {
		got := tool.guardCommand(context.Background(), p+" --version", cwd)
		require.Empty(t, got, "FR-1: %s must run", p)
	}
}

// TestGuard1960_OtherOperandsStillJudged is D1's "unchanged" list: relaxing
// the program word must not relax any other operand of the same command.
func TestGuard1960_OtherOperandsStillJudged(t *testing.T) {
	skipOnWindows1960(t)
	tool, cwd := guardFixture(t)
	outside := sweepTempDir(t)
	prog := filepath.Join(outside, "bin", "gh")
	other := filepath.Join(outside, "elsewhere")

	cases := []struct {
		name, cmd, path, reason string
	}{
		{"assignment value is an operand, not the program (C4)", "GH_CONFIG_DIR=" + other + " gh auth status", other, "variable assignment"},
		{"assignment value before an absolute program", "GH_CONFIG_DIR=" + other + " " + prog + " auth status", other, "variable assignment"},
		{"colon-joined PATH assignment", "PATH=/etc/shadow:/usr/bin make", "/etc/shadow", "variable assignment"},
		{"argument of the absolute program", prog + " --config " + other, other, "directory prefix"},
		{"output redirect of the absolute program", prog + " --version > " + other, other, ""},
		{"env -C flag value", "env -C " + other + " " + prog + " --version", other, ""},
		{"env assignment value", "env GH_CONFIG_DIR=" + other + " " + prog + " --version", other, "variable assignment"},
		{"argument after a wrapper's program", "timeout 5 " + prog + " " + other, other, ""},
		{"script argument of an interpreter", "bash " + filepath.Join(outside, "x.sh"), filepath.Join(outside, "x.sh"), ""},
		// CHECK F1 (check-envS-probe.log, exact probe rows): a wrapper option
		// whose value is itself a command (env -S) or a combined short-option
		// cluster (time -ao FILE) must not turn the next word into "the
		// program"; the outside operand stays refused.
		{"env -S split string, unquoted (CHECK F1)", "env -S touch " + marker1960, marker1960, ""},
		{"env -S split string, quoted (CHECK F1 control)", "env -S 'touch " + marker1960 + "'", marker1960, ""},
		{"env --split-string (CHECK F1)", "env --split-string touch " + marker1960, marker1960, ""},
		{"env -S inside an option cluster (CHECK F1)", "env -iS touch " + marker1960, marker1960, ""},
		{"plain touch control (CHECK F1)", "touch " + marker1960, marker1960, ""},
		{"time -ao combined cluster (CHECK F1)", "/usr/bin/time -ao " + marker1960 + " true", marker1960, ""},
		{"time -a -o separate (CHECK F1 control)", "/usr/bin/time -a -o " + marker1960 + " true", marker1960, ""},
		{"time -o (CHECK F1 control)", "/usr/bin/time -o " + marker1960 + " true", marker1960, ""},
		{"env -u then touch (CHECK F1 control)", "env -u FOO touch " + marker1960, marker1960, ""},
		{"unquoted brace expansion in the program word", "{" + prog + "," + other + "}", prog, "brace expansion"},
		{"brace glued onto the program word", "/usr/bin/tee{,}", "/usr/bin/tee", "brace expansion"},
		{"path glued into the command word", "cat[" + other + "]", other, "not the program itself"},
		{"second path glued onto the program word", prog + "," + other, other, "not the program itself"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := tool.guardCommand(context.Background(), c.cmd, cwd)
			require.NotEmpty(t, got, "D1: this operand must stay judged and refused\ncommand: %s", c.cmd)
			require.Contains(t, got, "path outside working dir", "command: %s", c.cmd)
			require.Contains(t, got, `"`+c.path+`"`, "the refusal must concern the operand, not the program\ncommand: %s\ngot: %s", c.cmd, got)
			if c.reason != "" {
				require.Contains(t, got, c.reason, "command: %s\ngot: %s", c.cmd, got)
			}
		})
	}

	// Refusals unrelated to the outside-workspace rule keep their own text.
	unresolved := tool.guardCommand(context.Background(), "$TOOLS/bin/gh --version", cwd)
	require.Contains(t, unresolved, "unresolvable path", "an unresolvable expansion in the program word stays refused")
	traversal := tool.guardCommand(context.Background(), prog+"/../../x --version", cwd)
	require.Contains(t, traversal, "path traversal detected", "the '..' veto runs before the program rule")
}

// TestGuard1960_ProtectedAndScopedPrograms covers the S0 rows for protected
// internal paths (still refused), and workspace and mount programs (still
// allowed).
func TestGuard1960_ProtectedAndScopedPrograms(t *testing.T) {
	skipOnWindows1960(t)
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	const wsID = "w-1960"
	const agentID = "agent-1960"
	now := time.Now().UTC().Format(time.RFC3339)
	require.NoError(t, workspace.SaveRecord(home, workspace.Workspace{
		ID: wsID, Name: "t", Status: "active", CreatedAt: now, UpdatedAt: now, CoreTeam: []string{agentID},
	}))
	work, err := workspace.EnsureWorkDir(home, wsID)
	require.NoError(t, err)
	work, err = filepath.EvalSymlinks(work)
	require.NoError(t, err)
	hostDir := t.TempDir()
	_, _, err = workspace.CreateMount(home, wsID, "tools", hostDir)
	require.NoError(t, err)
	hostRoot, err := filepath.EvalSymlinks(hostDir)
	require.NoError(t, err)

	credential := filepath.Join(home, "credentials.json")
	require.NoError(t, os.WriteFile(credential, []byte(`{"fixture":true}`), 0o600))

	tool, err := NewExecTool(work, true)
	require.NoError(t, err)
	ctx := WithWorkspaceID(WithAgentID(context.Background(), agentID), wsID)

	protected := tool.guardCommand(ctx, credential+" --version", work)
	require.Contains(t, protected, "protected internal files", "a protected internal file stays refused as a program")
	foreign := tool.guardCommand(ctx, filepath.Join(home, "agents", "victim", "bin", "tool")+" --version", work)
	require.NotEmpty(t, foreign, "another agent's private folder stays refused as a program")

	require.Empty(t, tool.guardCommand(ctx, filepath.Join(work, "bin", "tool")+" --version", work), "workspace program runs")
	require.Empty(t, tool.guardCommand(ctx, filepath.Join(hostRoot, "bin", "tool")+" --version", work), "mounted program runs")
}

// TestGuard1960_RefusalTextNamesRuleOnly is FR-6 for every outside-workspace
// refusal shape: rule named; no copy advice, no guard-off advice, no hint
// about assembling paths at runtime. The operator remedy reaches the gateway
// log.
func TestGuard1960_RefusalTextNamesRuleOnly(t *testing.T) {
	skipOnWindows1960(t)
	logs := captureLogs(t)
	tool, cwd := guardFixture(t)
	outside := sweepTempDir(t)

	for _, cmd := range []string{
		"printf x > " + filepath.Join(outside, "w.txt"),
		"GH_CONFIG_DIR=" + outside + " gh auth status",
		"cat[" + outside + "]",
		"find " + outside + " -name x",
	} {
		got := tool.guardCommand(context.Background(), cmd, cwd)
		require.NotEmpty(t, got, "fixture: %s is refused", cmd)
		assert.Contains(t, got, "Rule: bash workspace path guard", "FR-6: name the rule\ncommand: %s", cmd)
		for _, banned := range []string{
			"copy or install", "workspace_path_guard=false", "RESTRICT_TO_WORKSPACE",
			"assembled at runtime", "advisory", "RUNNING a program",
		} {
			assert.NotContains(t, got, banned, "FR-6/D1: agent-facing refusal must not carry %q\ncommand: %s", banned, cmd)
		}
	}
	logged := logs()
	assert.Contains(t, logged, "sandbox.workspace_path_guard", "D1: the operator remedy stays in the gateway log")
	assert.Contains(t, logged, "OMNIPUS_AGENTS_DEFAULTS_RESTRICT_TO_WORKSPACE")
}

// TestExecTool1960_ProgramOwnErrorPassesThrough is FR-6's second half: the
// guard lets the program run, and the program's own error text and exit code
// reach the agent unchanged, not relabelled as a guard refusal.
func TestExecTool1960_ProgramOwnErrorPassesThrough(t *testing.T) {
	skipOnWindows1960(t)
	ws := sweepTempDir(t)
	tool, err := NewExecTool(ws, true)
	require.NoError(t, err)
	outside := sweepTempDir(t)
	prog := filepath.Join(outside, "fake-gh")
	script := "#!/bin/sh\necho 'fake-gh: You are not logged into any GitHub hosts. To log in, run: gh auth login' >&2\nexit 4\n"
	require.NoError(t, os.WriteFile(prog, []byte(script), 0o755))

	result := tool.Execute(context.Background(), map[string]any{"action": "run", "command": prog + " auth status"})
	require.NotNil(t, result)
	require.NotContains(t, result.ForLLM, "blocked by safety guard", "FR-1: the guard must not refuse the program")
	// CHECK F3: a launch refusal by the sandbox or the spawn path is a
	// failure of this acceptance row, never a reason to skip it.
	for _, denial := range []string{"permission denied", "operation not permitted"} {
		require.NotContains(t, strings.ToLower(result.ForLLM), denial, "the program must actually launch")
	}
	assert.Contains(t, result.ForLLM, "fake-gh: You are not logged into any GitHub hosts. To log in, run: gh auth login",
		"FR-6: the program's own error passes through unchanged")
	require.NotNil(t, result.ExitCode, "the program's own exit code is reported")
	assert.Equal(t, 4, *result.ExitCode, "the program's own exit code is reported unchanged")
}

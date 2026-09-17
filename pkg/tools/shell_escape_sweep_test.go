package tools

// Regression coverage for UAT 2026-09-13 D-14 (bash workspace path guard
// bypassed by a runtime-assembled path), D-44 / D-66 (refusal messages that
// name what was actually refused) and D-65 (deny-pattern refusals that name
// the offending token).

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/audit"
)

func sweepTempDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	return d
}

// TestSweepEscapingSymlinks_ReportsAndNeverRemoves is the unit oracle for
// the sweep after the Codex review of 2026-09-14 (finding #2): a link that
// points outside every root and whose change time falls inside the
// command's window is REPORTED; a link that stays inside is not; a link
// that escapes but predates the command (outside the slack window) is not
// reported; and — the point of the finding — NOTHING is removed, not even
// a link that looks exactly like the command's own creation, because the
// window reaches two seconds before the command started and cannot tell an
// agent's link from a person's.
func TestSweepEscapingSymlinks_ReportsAndNeverRemoves(t *testing.T) {
	ws := sweepTempDir(t)
	outside := sweepTempDir(t)
	require.NoError(t, os.MkdirAll(filepath.Join(ws, "inside", "deep"), 0o755))

	// A person's escaping link, created moments BEFORE the command started
	// — inside the slack window, so it is indistinguishable from the
	// command's own creation by timestamp. The old sweep deleted it.
	human := filepath.Join(ws, "human-link")
	require.NoError(t, os.Symlink(outside, human))
	started := time.Now()

	// Links created by "the command".
	escAbs := filepath.Join(ws, "inside", "deep", "etclink")
	require.NoError(t, os.Symlink(outside, escAbs))
	escRel := filepath.Join(ws, "inside", "rel-escape")
	require.NoError(t, os.Symlink(filepath.Join("..", "..", "..", filepath.Base(outside)), escRel))
	stays := filepath.Join(ws, "inside", "stays")
	require.NoError(t, os.Symlink(filepath.Join(ws, "inside", "deep"), stays))
	staysRel := filepath.Join(ws, "inside", "stays-rel")
	require.NoError(t, os.Symlink("deep", staysRel))

	// Age gate on REPORTING: a sweep whose command started after the slack
	// window has passed sees none of these links as "appeared during this
	// command" and reports nothing.
	future := sweepEscapingSymlinks([]string{ws}, []string{ws}, time.Now().Add(escapeSweepSlack+3*time.Second))
	assert.Empty(t, future.Found, "a sweep whose command started after every link was created must report nothing")

	res := sweepEscapingSymlinks([]string{ws}, []string{ws}, started)
	assert.False(t, res.Partial)

	got := make([]string, 0, len(res.Found))
	for _, r := range res.Found {
		got = append(got, r.Link)
	}
	assert.ElementsMatch(t, []string{escAbs, escRel, human}, got, "every escaping link inside the window is reported")

	// The finding: every link — the person's AND the command's — still exists.
	for _, p := range []string{human, escAbs, escRel, stays, staysRel} {
		_, err := os.Lstat(p)
		assert.NoError(t, err, "%s must still exist: the sweep reports, it never removes", p)
	}

	notice := escapeSweepNotice(res)
	assert.Contains(t, notice, "found 3 symlink(s)")
	assert.Contains(t, notice, escAbs+" -> "+outside)
	assert.Contains(t, notice, "NOT removed")
	assert.NotContains(t, notice, "removed 3", "the notice must not claim a removal that did not happen")
	assert.Empty(t, escapeSweepNotice(escapeSweepResult{}), "nothing found and a complete walk is silent")
}

// TestSweepEscapingSymlinks_MountRootTargetsAreAllowed pins the one shape a
// sweep must never touch: the work/<name> symlink a mount IS, and links into
// a mounted folder.
func TestSweepEscapingSymlinks_MountRootTargetsAreAllowed(t *testing.T) {
	ws := sweepTempDir(t)
	mount := sweepTempDir(t)
	mountLink := filepath.Join(ws, "vault")
	require.NoError(t, os.Symlink(mount, mountLink))
	intoMount := filepath.Join(ws, "note")
	require.NoError(t, os.Symlink(filepath.Join(mount, "Home.md"), intoMount))

	res := sweepEscapingSymlinks([]string{ws, mount}, []string{ws, mount}, time.Now())
	assert.Empty(t, res.Found)
	for _, p := range []string{mountLink, intoMount} {
		_, err := os.Lstat(p)
		assert.NoError(t, err)
	}
}

// TestExecTool_ReportsSymlinkAssembledAtRuntime is the D-14 reproduction
// under the report-only sweep: the command text names no outside path, so
// the text guard passes it, and the interpreter builds "/etc" at runtime.
// The sweep must NAME what the text scan structurally cannot see — in the
// tool result and in the audit log — and must leave the link in place.
func TestExecTool_ReportsSymlinkAssembledAtRuntime(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	ws := sweepTempDir(t)
	tool, err := NewExecTool(ws, true)
	require.NoError(t, err)
	auditDir := t.TempDir()
	logger, err := audit.NewLogger(audit.LoggerConfig{Dir: auditDir, RetentionDays: 1})
	require.NoError(t, err)
	defer func() { _ = logger.Close() }()
	tool.SetAuditLogger(logger)
	ctx := WithToolContext(context.Background(), "cli", "")

	cmd := `python3 -c "import os; dest = '/' + ['et','c'][0] + 'c'; os.symlink(dest, 'etclink'); print('SYMLINK_CREATED')"`
	require.Empty(t, tool.guardCommand(ctx, cmd, ws), "the text guard must NOT see the assembled path — that is the defect")

	result := tool.Execute(ctx, map[string]any{"action": "run", "command": cmd})
	require.NotNil(t, result)
	if !strings.Contains(result.ForLLM, "SYMLINK_CREATED") {
		t.Skipf("interpreter could not run in this sandbox environment: %s", result.ForLLM)
	}
	assert.Contains(t, result.ForLLM, "found 1 symlink(s)", "the sweep must report the finding to the agent")
	assert.Contains(t, result.ForLLM, "etclink -> /etc")
	assert.Contains(t, result.ForLLM, "NOT removed")
	_, lerr := os.Lstat(filepath.Join(ws, "etclink"))
	assert.NoError(t, lerr, "the sweep must not remove the link — it reports it")

	// The operator's record: an audit entry naming the link, and nothing
	// claiming a removal.
	require.NoError(t, logger.Close())
	files, err := filepath.Glob(filepath.Join(auditDir, "*.jsonl"))
	require.NoError(t, err)
	var all []byte
	for _, f := range files {
		b, rerr := os.ReadFile(f)
		require.NoError(t, rerr)
		all = append(all, b...)
	}
	assert.Contains(t, string(all), `"warning":"escaping_symlinks"`)
	assert.Contains(t, string(all), "etclink")
	assert.NotContains(t, string(all), "removed after the command ran")
}

// TestExecTool_BackgroundRunSweepsOnCompletion is the D-14 reproduction for
// the BACKGROUND path (Claude review 2026-09-14): a run_in_background=true
// command whose text names no outside path assembles an escaping symlink at
// runtime and exits. Before the fix, only the foreground path ran the
// post-command sweep, so this exact command planted its escape and no report
// ever surfaced — not in the completion callback, not in the audit log. The
// completion goroutine must sweep once the process has exited and the pipes
// are drained, fold the notice into the delivered result, and leave the
// link in place.
func TestExecTool_BackgroundRunSweepsOnCompletion(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	ws := sweepTempDir(t)
	tool, err := NewExecTool(ws, true)
	require.NoError(t, err)
	auditDir := t.TempDir()
	logger, err := audit.NewLogger(audit.LoggerConfig{Dir: auditDir, RetentionDays: 1})
	require.NoError(t, err)
	defer func() { _ = logger.Close() }()
	tool.SetAuditLogger(logger)
	ctx := WithToolContext(context.Background(), "cli", "")

	cmd := `python3 -c "import os, time; dest = '/' + ['et','c'][0] + 'c'; os.symlink(dest, 'etclink'); print('SYMLINK_CREATED')"`
	require.Empty(t, tool.guardCommand(ctx, cmd, ws), "the text guard must NOT see the assembled path — that is the defect")

	done := make(chan *ToolResult, 1)
	startResult := tool.ExecuteAsync(ctx, map[string]any{
		"action":            "run",
		"command":           cmd,
		"run_in_background": true,
		"timeout_seconds":   float64(30),
	}, func(_ context.Context, result *ToolResult) { done <- result })
	require.NotNil(t, startResult)
	if startResult.IsError {
		t.Skipf("background command could not start in this sandbox environment: %s", startResult.ForLLM)
	}

	var completion *ToolResult
	select {
	case completion = <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("background completion callback never fired")
	}
	require.NotNil(t, completion)
	if !strings.Contains(completion.ForLLM, "SYMLINK_CREATED") {
		t.Skipf("interpreter could not run in this sandbox environment: %s", completion.ForLLM)
	}
	assert.Contains(t, completion.ForLLM, "finished", "the completion result still reports the session outcome")
	assert.Contains(t, completion.ForLLM, "found 1 symlink(s)", "the sweep must report the finding through the completion callback")
	assert.Contains(t, completion.ForLLM, "etclink -> /etc")
	assert.Contains(t, completion.ForLLM, "NOT removed")
	_, lerr := os.Lstat(filepath.Join(ws, "etclink"))
	assert.NoError(t, lerr, "the sweep must not remove the link — it reports it")

	// The operator's record: the audit entry is written by the completion
	// goroutine's sweep, before the callback fires, so it is already on
	// disk by the time the test observes the callback.
	require.NoError(t, logger.Close())
	files, err := filepath.Glob(filepath.Join(auditDir, "*.jsonl"))
	require.NoError(t, err)
	var all []byte
	for _, f := range files {
		b, rerr := os.ReadFile(f)
		require.NoError(t, rerr)
		all = append(all, b...)
	}
	assert.Contains(t, string(all), `"warning":"escaping_symlinks"`)
	assert.Contains(t, string(all), "etclink")
}

// TestGuardCommand_RefusalNamesWhatWasRefused pins D-44 and D-66: a program
// run by absolute path is described as an EXEC, not a WRITE; an unproven read
// names the command word and why it is not on the allowlist; a proven read is
// allowed; and every refusal states honestly that the guard is a text scan.
func TestGuardCommand_RefusalNamesWhatWasRefused(t *testing.T) {
	tool, cwd := guardFixture(t)
	outside := sweepTempDir(t)
	ctx := context.Background()

	execMsg := tool.guardCommand(ctx, outside+"/bin/omnipus records import-obsidian --dry-run", cwd)
	require.NotEmpty(t, execMsg)
	assert.Contains(t, execMsg, "RUNNING a program")
	assert.NotContains(t, execMsg, "a WRITE outside the working directory needs")

	findMsg := tool.guardCommand(ctx, "find "+outside+" -name '*.md'", cwd)
	require.NotEmpty(t, findMsg)
	assert.Contains(t, findMsg, `"find" is not on the guard's read-only allowlist`)
	assert.Contains(t, findMsg, "read_file and list_directory")

	assert.Empty(t, tool.guardCommand(ctx, "head -n 20 "+outside+"/Home.md", cwd), "head is a proven read")
	assert.Empty(t, tool.guardCommand(ctx, "ls "+outside, cwd), "ls is a proven read")

	for _, msg := range []string{execMsg, findMsg} {
		assert.Contains(t, msg, "advisory", "every path-guard refusal must say what the guard is")
		assert.Contains(t, msg, "assembled at runtime")
	}
}

// TestApplyDenyPatterns_NamesTheOffendingToken pins D-65.
func TestApplyDenyPatterns_NamesTheOffendingToken(t *testing.T) {
	msg := applyDenyPatterns("echo start && rm -rf build && echo done", defaultDenyPatterns, nil)
	require.NotEmpty(t, msg)
	assert.Contains(t, msg, "dangerous pattern detected")
	assert.Contains(t, msg, `"rm -rf"`, "the matched text must be named")
	assert.Contains(t, msg, `\brm\s+-[rf]{1,2}\b`, "the pattern must be named")

	custom := []*regexp.Regexp{regexp.MustCompile(`\bsips\b`)}
	msg = applyDenyPatterns(`ls a | sips -Z 200 b`, custom, nil)
	assert.Contains(t, msg, `"sips"`)
	assert.Empty(t, applyDenyPatterns("ls -la", custom, nil))
}

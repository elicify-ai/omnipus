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
)

func sweepTempDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	return d
}

// TestSweepEscapingSymlinks_RemovesOnlyNewEscapingLinks is the unit oracle
// for the sweep: a link created by "this command" that points outside every
// root is removed; a link that stays inside is kept; a link that escapes but
// predates the command is kept (it is not this command's creation to undo).
func TestSweepEscapingSymlinks_RemovesOnlyNewEscapingLinks(t *testing.T) {
	ws := sweepTempDir(t)
	outside := sweepTempDir(t)
	require.NoError(t, os.MkdirAll(filepath.Join(ws, "inside", "deep"), 0o755))

	// Pre-existing escaping link: created BEFORE the command "started".
	old := filepath.Join(ws, "old-escape")
	require.NoError(t, os.Symlink(outside, old))
	// Give the clock room so the old link's change time is provably before
	// the command start even on a 1-second-resolution filesystem.
	started := time.Now().Add(escapeSweepSlack + 3*time.Second)

	// Links created by "the command".
	escAbs := filepath.Join(ws, "inside", "deep", "etclink")
	require.NoError(t, os.Symlink(outside, escAbs))
	escRel := filepath.Join(ws, "inside", "rel-escape")
	require.NoError(t, os.Symlink(filepath.Join("..", "..", "..", filepath.Base(outside)), escRel))
	stays := filepath.Join(ws, "inside", "stays")
	require.NoError(t, os.Symlink(filepath.Join(ws, "inside", "deep"), stays))
	staysRel := filepath.Join(ws, "inside", "stays-rel")
	require.NoError(t, os.Symlink("deep", staysRel))

	// The "old" link must be judged old: run the sweep with a start time far
	// in the future for it alone to prove the age gate, then the real sweep.
	future := sweepEscapingSymlinks([]string{ws}, []string{ws}, started)
	assert.Empty(t, future.Removed, "a sweep whose command started after every link was created must remove nothing")
	_, err := os.Lstat(old)
	require.NoError(t, err, "the pre-existing link must survive the age-gated sweep")

	res := sweepEscapingSymlinks([]string{ws}, []string{ws}, time.Now())
	assert.False(t, res.Partial)
	assert.Equal(t, 0, res.Errors)

	got := make([]string, 0, len(res.Removed))
	for _, r := range res.Removed {
		got = append(got, r.Link)
	}
	// The pre-existing link is created in this same test run, so with a
	// "now" start it is inside the slack window and IS removed — that is
	// correct for the real sweep (the command start is the real clock). The
	// age gate itself is proven above with the future start.
	assert.ElementsMatch(t, []string{escAbs, escRel, old}, got)
	for _, p := range []string{escAbs, escRel} {
		_, err := os.Lstat(p)
		assert.True(t, os.IsNotExist(err), "%s must be removed", p)
	}
	for _, p := range []string{stays, staysRel} {
		_, err := os.Lstat(p)
		assert.NoError(t, err, "%s points inside the workspace and must be kept", p)
	}
	assert.Contains(t, escapeSweepNotice(res), "removed 3 symlink(s)")
	assert.Contains(t, escapeSweepNotice(res), escAbs+" -> "+outside)
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
	assert.Empty(t, res.Removed)
	for _, p := range []string{mountLink, intoMount} {
		_, err := os.Lstat(p)
		assert.NoError(t, err)
	}
}

// TestExecTool_RemovesSymlinkAssembledAtRuntime is the D-14 reproduction:
// the command text names no outside path, so the text guard passes it, and
// the interpreter builds "/etc" at runtime. The sweep must catch what the
// text scan structurally cannot.
func TestExecTool_RemovesSymlinkAssembledAtRuntime(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	ws := sweepTempDir(t)
	tool, err := NewExecTool(ws, true)
	require.NoError(t, err)
	ctx := WithToolContext(context.Background(), "cli", "")

	cmd := `python3 -c "import os; dest = '/' + ['et','c'][0] + 'c'; os.symlink(dest, 'etclink'); print('SYMLINK_CREATED')"`
	require.Empty(t, tool.guardCommand(ctx, cmd, ws), "the text guard must NOT see the assembled path — that is the defect")

	result := tool.Execute(ctx, map[string]any{"action": "run", "command": cmd})
	require.NotNil(t, result)
	if !strings.Contains(result.ForLLM, "SYMLINK_CREATED") {
		t.Skipf("interpreter could not run in this sandbox environment: %s", result.ForLLM)
	}
	assert.Contains(t, result.ForLLM, "removed 1 symlink(s)", "the sweep must report the removal to the agent")
	assert.Contains(t, result.ForLLM, "etclink -> /etc")
	_, lerr := os.Lstat(filepath.Join(ws, "etclink"))
	assert.True(t, os.IsNotExist(lerr), "the escaping symlink must be gone after the command")
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

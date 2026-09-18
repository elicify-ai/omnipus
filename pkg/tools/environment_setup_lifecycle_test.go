package tools

// environment_setup_lifecycle_test.go — publication-lifecycle tests for the
// generic environment_setup tool (security review MAJ-1/2/3, ADR-090). The
// process boundary is real (real child processes through the shared
// SessionManager), while the storage target is a FAKE behind the tool's own
// EnvironmentSetupStore/EnvironmentSetupTarget seam so Commit/Abort can be
// forced to fail or block deterministically — impossible to force on the real
// storage.
//
// Platform-tagged siblings:
//   - environment_setup_lifecycle_unix_test.go — real process-group survivor
//     fixtures (syscall.Kill).
//   - environment_setup_lifecycle_kernel_test.go — the real-kernel confinement
//     fixture (self-exec isolated).
//   - environment_setup_lifecycle_kernel_darwin_test.go — darwin Seatbelt
//     backend install helper.
//
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -p 1 -run
// '^TestEnvironmentSetup_(Publication|Survivor|TwoStep|Setsid|Confinement)'
// ./pkg/tools/

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/audit"
)

// --- fake store/target behind the existing seam -------------------------------

// lifecycleTarget is the fake EnvironmentSetupTarget: commitFn and abortErr
// let a test force Commit blocking/failure and Abort failure; the call
// counters prove cleanup was attempted after a failed Commit.
type lifecycleTarget struct {
	mu sync.Mutex

	prefix     string
	cache, tmp string

	// commitFn replaces the whole Commit behavior (nil → success).
	commitFn func() (string, string, error)
	// abortErr is returned by every Abort call (nil → success cleanup).
	abortErr error

	commitCalls int
	abortCalls  int
}

func (a *lifecycleTarget) Scope() string           { return environmentSetupScopeShared }
func (a *lifecycleTarget) Prefix() string          { return a.prefix }
func (a *lifecycleTarget) Cache() string           { return a.cache }
func (a *lifecycleTarget) Tmp() string             { return a.tmp }
func (a *lifecycleTarget) WritableRoots() []string { return []string{a.cache, a.tmp} }

func (a *lifecycleTarget) Commit() (string, string, error) {
	a.mu.Lock()
	a.commitCalls++
	fn := a.commitFn
	a.mu.Unlock()
	if fn != nil {
		return fn()
	}
	return "gen-fake-1", filepath.Join(a.prefix, "published"), nil
}

func (a *lifecycleTarget) Abort() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.abortCalls++
	return a.abortErr
}

func (a *lifecycleTarget) calls() (int, int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.commitCalls, a.abortCalls
}

// lifecycleStore hands out one configured target for every BeginInstall.
type lifecycleStore struct {
	target *lifecycleTarget
}

func (s lifecycleStore) BeginInstall(_, _ string, _ string) (EnvironmentSetupTarget, error) {
	return s.target, nil
}

// --- harness ------------------------------------------------------------------

// lifecycleHarness mirrors setupTestHarness but injects the fake store.
type lifecycleHarness struct {
	home     string
	tool     *EnvironmentSetupTool
	auditDir string
}

func newLifecycleHarness(t *testing.T, store EnvironmentSetupStore) *lifecycleHarness {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("environment_setup lifecycle tests use POSIX shell scripts")
	}
	home := t.TempDir()
	agentDir := filepath.Join(home, "agents", "testagent")
	require.NoError(t, os.MkdirAll(agentDir, 0o755))
	tool := NewEnvironmentSetupTool(EnvironmentSetupToolDeps{
		Home:            home,
		AgentWorkDir:    agentDir,
		Admin:           false,
		GodMode:         true, // process-level tests; kernel confinement is the kernel fixture's job
		Store:           store,
		AuditFailClosed: true,
	})
	auditDir := t.TempDir()
	auditLog, err := audit.NewLogger(audit.LoggerConfig{Dir: auditDir, RetentionDays: 1})
	require.NoError(t, err)
	t.Cleanup(func() { _ = auditLog.Close() })
	tool.SetAuditLogger(auditLog)
	return &lifecycleHarness{home: home, tool: tool, auditDir: auditDir}
}

// ctx layers the turn facts (same shape as setupTestHarness.ctx).
func (h *lifecycleHarness) ctx(agentID, wsID, wsDir, transcript string) context.Context {
	ctx := WithToolContext(context.Background(), "cli", "chat")
	ctx = WithAgentID(ctx, agentID)
	ctx = WithWorkspaceID(ctx, wsID)
	if wsDir != "" {
		ctx = WithTurnWorkspaceDir(ctx, wsDir)
	}
	return WithTranscriptSessionID(ctx, transcript)
}

// startAndWait runs action=run through ExecuteAsync and blocks until the
// terminal completion callback fires.
func (h *lifecycleHarness) startAndWait(t *testing.T, ctx context.Context, args map[string]any) (start, completion *ToolResult) {
	t.Helper()
	done := make(chan *ToolResult, 1)
	start = h.tool.ExecuteAsync(ctx, args, func(_ context.Context, result *ToolResult) { done <- result })
	require.NotNil(t, start)
	select {
	case completion = <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("environment_setup completion callback never fired")
	}
	require.NotNil(t, completion)
	return start, completion
}

// auditContents reads back every JSONL entry the harness's logger wrote.
func (h *lifecycleHarness) auditContents(t *testing.T) string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(h.auditDir, "*.jsonl"))
	require.NoError(t, err)
	var all []byte
	for _, f := range files {
		b, rerr := os.ReadFile(f)
		require.NoError(t, rerr)
		all = append(all, b...)
	}
	return string(all)
}

// pollAction runs action=poll and decodes the leading session JSON.
func pollAction(t *testing.T, tool *EnvironmentSetupTool, ctx context.Context, sessionID string) (ExecResponse, *ToolResult) {
	t.Helper()
	res := tool.Execute(ctx, map[string]any{"action": "poll", "session_id": sessionID})
	dec := json.NewDecoder(strings.NewReader(res.ForLLM))
	var resp ExecResponse
	require.NoError(t, dec.Decode(&resp), "poll result must lead with the session JSON; got %q", res.ForLLM)
	return resp, res
}

// waitForArtifact blocks until path exists (the script's own progress
// marker), or fails the test after 15s.
func waitForArtifact(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("artifact never appeared: %s", path)
}

// pathForTempSubdir creates and returns a real directory under t.TempDir()
// for use as a fake target's prefix (the child's cwd must exist).
func pathForTempSubdir(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	return dir
}

// --- MAJ-1: poll during publication, truthful terminal persistence ------------

func TestEnvironmentSetup_Publication_PollDuringCommit_Running(t *testing.T) {
	prefix := pathForTempSubdir(t, "prefix")
	target := &lifecycleTarget{prefix: prefix}
	gate := make(chan struct{})
	target.commitFn = func() (string, string, error) {
		<-gate
		return "gen-blocked", filepath.Join(prefix, "published"), nil
	}
	h := newLifecycleHarness(t, lifecycleStore{target: target})
	ctx := h.ctx("mia", "alpha", "", "t-pub-block-1")

	done := make(chan *ToolResult, 1)
	start := h.tool.ExecuteAsync(ctx, map[string]any{
		"command": `echo installed > "$OMNIPUS_ENV_PREFIX/artifact.txt"`,
		"purpose": "blocked publication probe",
		"scope":   "shared",
	}, func(_ context.Context, result *ToolResult) { done <- result })
	require.False(t, start.IsError, "start refused: %s", start.ForLLM)
	sessionID := extractSessionID(t, start.ForLLM)

	// The command finished writing its artifact — publication is the only
	// thing left. While Commit is blocked the session must NOT report
	// done/exit-0: "running" is the truthful state until publication lands.
	waitForArtifact(t, filepath.Join(prefix, "artifact.txt"))
	time.Sleep(150 * time.Millisecond)
	resp, _ := pollAction(t, h.tool, ctx, sessionID)
	assert.Equal(t, "running", resp.Status,
		"poll must report running while publication is incomplete (MAJ-1)")

	foreignCtx := h.ctx("mia", "alpha", "", "t-pub-block-foreign")
	foreign := h.tool.Execute(foreignCtx, map[string]any{"action": "poll", "session_id": sessionID})
	require.NotNil(t, foreign)
	require.True(t, foreign.IsError, "foreign poll during publication must be refused")
	assert.Contains(t, foreign.ForLLM, "session not found")

	// Release the gate: publication succeeds — only NOW is success shown.
	close(gate)
	var completion *ToolResult
	select {
	case completion = <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("completion callback never fired after commit release")
	}
	require.False(t, completion.IsError, "successful publication must not error: %s", completion.ForLLM)
	assert.Contains(t, completion.ForLLM, "published: generation gen-blocked")
	resp, res := pollAction(t, h.tool, ctx, sessionID)
	assert.Equal(t, "done", resp.Status)
	assert.Contains(t, res.ForLLM, "publication: committed")
	read := h.tool.Execute(ctx, map[string]any{"action": "read", "session_id": sessionID})
	assert.Contains(t, read.ForLLM, "published: generation gen-blocked")
}

// --- MAJ-2: cleanup after failed Commit; no false cleanup claim ---------------

func TestEnvironmentSetup_Publication_CommitFailure_CleanupAttempted(t *testing.T) {
	prefix := pathForTempSubdir(t, "prefix")
	target := &lifecycleTarget{
		prefix: prefix,
		commitFn: func() (string, string, error) {
			return "", "", fmt.Errorf("forced: marker digest mismatch")
		},
	}
	h := newLifecycleHarness(t, lifecycleStore{target: target})
	ctx := h.ctx("mia", "alpha", "", "t-pub-cf-1")

	_, completion := h.startAndWait(t, ctx, map[string]any{
		"command": `echo fine > "$OMNIPUS_ENV_PREFIX/artifact.txt"`,
		"purpose": "forced commit failure",
		"scope":   "shared",
	})

	// The command exited 0 — but the completion must be an ERROR naming the
	// publication cause, never a false-green exit-0.
	require.True(t, completion.IsError,
		"a failed Commit must surface as an error, got: %s", completion.ForLLM)
	assert.Contains(t, completion.ForLLM, "PUBLICATION FAILED")
	assert.Contains(t, completion.ForLLM, "forced: marker digest mismatch",
		"the commit failure cause must reach the agent")

	// Cleanup was ATTEMPTED after the failed commit (MAJ-2).
	commits, aborts := target.calls()
	assert.Equal(t, 1, commits)
	assert.Equal(t, 1, aborts, "Abort must be attempted after a failed Commit")

	// Poll retains the failure; the audit trail records it as an error.
	resp, res := pollAction(t, h.tool, ctx, startSessionID(t, completion))
	assert.Equal(t, "done", resp.Status)
	assert.Contains(t, res.ForLLM, "publication: FAILED")
	assert.Contains(t, h.auditContents(t), `"decision":"error"`)
}

func TestEnvironmentSetup_Publication_AbortFailure_NotClaimed(t *testing.T) {
	prefix := pathForTempSubdir(t, "prefix")
	target := &lifecycleTarget{prefix: prefix, abortErr: fmt.Errorf("forced: locked tree")}
	h := newLifecycleHarness(t, lifecycleStore{target: target})
	ctx := h.ctx("mia", "alpha", "", "t-pub-af-1")

	_, completion := h.startAndWait(t, ctx, map[string]any{
		"command": `echo boom >&2; exit 3`,
		"purpose": "forced abort failure",
		"scope":   "shared",
	})
	require.True(t, completion.IsError, "the command itself failed (exit 3)")

	// No false cleanup claim: "staging removed" appears ONLY when cleanup
	// actually succeeded (MAJ-2).
	assert.NotContains(t, completion.ForLLM, "staging removed")
	assert.Contains(t, completion.ForLLM, "cleanup FAILED")
	assert.Contains(t, completion.ForLLM, "forced: locked tree")

	resp, res := pollAction(t, h.tool, ctx, startSessionID(t, completion))
	assert.Equal(t, "done", resp.Status)
	assert.Contains(t, res.ForLLM, "staging cleanup FAILED")
}

// A natural nonzero exit is still an error when shared cleanup succeeds. The
// session intentionally remains running until publication/cleanup finishes,
// so this guards against checking that pre-persistence status instead of the
// effective natural outcome.
func TestEnvironmentSetup_NonzeroNaturalExitReportsErrorAfterSuccessfulCleanup(t *testing.T) {
	prefix := pathForTempSubdir(t, "prefix")
	target := &lifecycleTarget{prefix: prefix}
	h := newLifecycleHarness(t, lifecycleStore{target: target})
	_, completion := h.startAndWait(t, h.ctx("mia", "alpha", "", "t-nonzero-cleanup-1"), map[string]any{
		"command": `echo failed >&2; exit 7`,
		"purpose": "nonzero exit truthfulness",
		"scope":   "shared",
	})
	require.True(t, completion.IsError, "exit 7 with successful cleanup must be an error: %s", completion.ForLLM)
	assert.Contains(t, completion.ForLLM, "exit code 7")
	commits, aborts := target.calls()
	assert.Equal(t, 0, commits)
	assert.Equal(t, 1, aborts)
}

// Result notes must survive a full captured-output buffer. The real child
// deliberately emits more than the shared session cap before failing; the
// terminal publication note is independent evidence, not part of that buffer.
func TestEnvironmentSetup_ReadRetainsPublicationNoteAfterOutputTruncation(t *testing.T) {
	prefix := pathForTempSubdir(t, "prefix")
	h := newLifecycleHarness(t, lifecycleStore{target: &lifecycleTarget{prefix: prefix}})
	ctx := h.ctx("mia", "alpha", "", "t-read-truncated-1")
	start, completion := h.startAndWait(t, ctx, map[string]any{
		"command": `yes x | head -c 1100000; exit 7`,
		"purpose": "truncated output publication note",
		"scope":   "shared",
	})
	require.True(t, completion.IsError)

	read := h.tool.Execute(ctx, map[string]any{"action": "read", "session_id": extractSessionID(t, start.ForLLM)})
	require.False(t, read.IsError, "read refused: %s", read.ForLLM)
	assert.Contains(t, read.ForLLM, "publication: not published",
		"terminal publication result must remain visible after bounded output truncation")
}

func TestEnvironmentSetup_ReadRetainsCommitFailureNoteAfterOutputTruncation(t *testing.T) {
	prefix := pathForTempSubdir(t, "prefix")
	target := &lifecycleTarget{prefix: prefix, commitFn: func() (string, string, error) {
		return "", "", fmt.Errorf("forced publish marker failure")
	}}
	h := newLifecycleHarness(t, lifecycleStore{target: target})
	ctx := h.ctx("mia", "alpha", "", "t-read-truncated-commit-fail-1")
	start, completion := h.startAndWait(t, ctx, map[string]any{
		"command": `yes x | head -c 1100000`,
		"purpose": "truncated output commit failure note",
		"scope":   "shared",
	})
	require.True(t, completion.IsError)

	read := h.tool.Execute(ctx, map[string]any{"action": "read", "session_id": extractSessionID(t, start.ForLLM)})
	require.False(t, read.IsError, "read refused: %s", read.ForLLM)
	assert.Contains(t, read.ForLLM, "publication: FAILED (forced publish marker failure)")
}

// --- reuse: two-step arbitrary-tool install (helper A, then use A) ------------

func TestEnvironmentSetup_TwoStep_ReuseSamePrefix(t *testing.T) {
	prefix := pathForTempSubdir(t, "prefix")
	target := &lifecycleTarget{prefix: prefix}
	h := newLifecycleHarness(t, lifecycleStore{target: target})
	ctx := h.ctx("mia", "alpha", "", "t-twostep-1")

	// Step 1: install an arbitrary helper with no host dependency.
	_, completion1 := h.startAndWait(t, ctx, map[string]any{
		"command": `set -e
mkdir -p "$OMNIPUS_ENV_PREFIX/bin"
printf '#!/bin/sh\necho helper-A-ready\n' > "$OMNIPUS_ENV_PREFIX/bin/hello"
chmod +x "$OMNIPUS_ENV_PREFIX/bin/hello"
`,
		"purpose": "install helper A",
	})
	require.False(t, completion1.IsError, "step 1 failed: %s", completion1.ForLLM)

	// Step 2: a second approved script uses A by BARE NAME — PATH discovery,
	// not an absolute path (generic-installer reuse contract).
	_, completion2 := h.startAndWait(t, ctx, map[string]any{
		"command": `set -e
hello > "$OMNIPUS_ENV_PREFIX/used.txt"
`,
		"purpose": "use helper A in a second script",
	})
	require.False(t, completion2.IsError, "step 2 failed: %s", completion2.ForLLM)
	data, err := os.ReadFile(filepath.Join(prefix, "used.txt"))
	require.NoError(t, err)
	assert.Equal(t, "helper-A-ready\n", string(data))
}

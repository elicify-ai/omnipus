package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/agent/runner"
	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/session"
)

// denyApprover is a PolicyApprover stub that always denies, recording whether it
// was consulted. Used to prove a permission-request from an external runner is
// routed to the consent layer and that a DENY cancels the run.
type denyApprover struct {
	consulted chan runner.ConsentRequest
}

func (d *denyApprover) RequestApproval(_ context.Context, req PolicyApprovalReq) (bool, string) {
	select {
	case d.consulted <- runner.ConsentRequest{RequestID: req.ToolCallID, ToolName: req.ToolName}:
	default:
	}
	return false, "denied for test"
}

// newExternalTestLoop builds a minimal AgentLoop and a child turnState whose agent
// is configured for executor=external-cli with the given CLI name and workspace.
func newExternalTestLoop(t *testing.T, cli, workspace string) (*AgentLoop, *turnState) {
	t.Helper()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Provider: "mock"},
		},
	}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &simpleMockProviderAPI{response: "ok"})
	agent := &AgentInstance{
		ID:        "ext-agent",
		Name:      "External Agent",
		Workspace: workspace,
		Subagents: &config.SubagentsConfig{
			Executor: &config.ExecutorConfig{Kind: config.ExecutorKindExternalCLI, CLI: cli},
		},
	}
	ts := &turnState{
		agent:               agent,
		agentID:             agent.ID,
		turnID:              "ext-run-1",
		transcriptSessionID: "session_ext_test",
	}
	return al, ts
}

// withFakeDriver swaps the external driver factory for the test, returning a
// restore func and the FakeRunner instance the factory will hand out.
func withFakeDriver(t *testing.T) (*runner.FakeRunner, func()) {
	t.Helper()
	fr := runner.NewFakeRunner()
	prev := newExternalDriver
	newExternalDriver = func(_ string, _ runner.ConsentHandler) (runner.ExternalAgentRunner, error) {
		return fr, nil
	}
	return fr, func() { newExternalDriver = prev }
}

// TestExternalDispatch_StreamsOutput_AndTearsDownWorktree drives the full external
// dispatch flow with a fake driver: a worktree is prepared, the driver's output is
// streamed into the aggregated result, and the worktree is torn down afterwards.
func TestExternalDispatch_StreamsOutput_AndTearsDownWorktree(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	al, ts := newExternalTestLoop(t, "claude-code", "")
	fr, restore := withFakeDriver(t)
	defer restore()

	// Inject the run's events asynchronously, then signal end.
	go func() {
		fr.InjectEvent(runner.RunEvent{Kind: runner.EventKindOutput, Output: &runner.OutputEvent{Text: "hello "}})
		fr.InjectEvent(runner.RunEvent{Kind: runner.EventKindOutput, Output: &runner.OutputEvent{Text: "world"}})
		fr.InjectEvent(runner.RunEvent{Kind: runner.EventKindEnd})
		fr.Cancel() // closes the event channel so the dispatcher/drain loop ends
	}()

	res, err := runExternalCLISubTurn(context.Background(), al, ts, "do the task", 30*time.Second)
	if err != nil {
		t.Fatalf("runExternalCLISubTurn error: %v", err)
	}
	if res == nil || res.ForLLM != "hello world" {
		t.Fatalf("aggregated output = %q, want %q", res.ForLLM, "hello world")
	}

	// The run options passed to the driver carry the worktree dir, input, timeout
	// and turn cap (FR-5.4 bound).
	opts := fr.RecordedRunOpts()
	if len(opts) != 1 {
		t.Fatalf("driver Run called %d times, want 1", len(opts))
	}
	if opts[0].Input != "do the task" {
		t.Errorf("driver input = %q, want %q", opts[0].Input, "do the task")
	}
	if opts[0].WorkDir == "" {
		t.Error("driver WorkDir is empty — worktree/temp dir was not prepared")
	}
	if opts[0].TimeoutSeconds != 30 {
		t.Errorf("driver TimeoutSeconds = %d, want 30", opts[0].TimeoutSeconds)
	}
	if opts[0].MaxTurns != defaultExternalMaxTurns {
		t.Errorf("driver MaxTurns = %d, want %d", opts[0].MaxTurns, defaultExternalMaxTurns)
	}

	// The run dir must be cleaned up (teardown). Its parent root may remain.
	if _, statErr := os.Stat(opts[0].WorkDir); !os.IsNotExist(statErr) {
		t.Errorf("work dir %q still exists after teardown (stat err=%v)", opts[0].WorkDir, statErr)
	}
}

// TestExternalDispatch_RoutesPermissionToConsent_DenyCancels proves a mid-run
// permission-request is routed to the wired PolicyApprover and that a DENY reaches
// the driver as a non-Allow decision (best-effort post-hoc cancellation).
func TestExternalDispatch_RoutesPermissionToConsent_DenyCancels(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	al, ts := newExternalTestLoop(t, "codex", "")
	approver := &denyApprover{consulted: make(chan runner.ConsentRequest, 1)}
	al.SetToolApprover(approver)

	fr, restore := withFakeDriver(t)
	defer restore()

	// Inject ONLY the permission request. The deny decision itself must cancel the
	// run (FakeRunner.Decide calls Cancel() on !Allow, matching the real-driver
	// contract). We deliberately do NOT inject EventKindEnd or call fr.Cancel()
	// here: if the deny did not cancel, the drain would block until the test ctx
	// times out — making the deny-cancel assertions load-bearing.
	go func() {
		fr.InjectEvent(runner.RunEvent{
			Kind: runner.EventKindPermissionRequest,
			PermissionRequest: &runner.PermissionRequestEvent{
				RequestID:   "perm-1",
				ToolName:    "shell",
				Description: "wants to run rm -rf",
				RawInput:    []byte(`{"cmd":"rm -rf /"}`),
			},
		})
	}()

	// Bound the run with a context so a regression (deny no longer cancels) fails
	// fast instead of hanging the suite.
	runCtx, cancelRun := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelRun()
	res, err := runExternalCLISubTurn(runCtx, al, ts, "task", 30*time.Second)
	if err == nil {
		t.Fatalf("expected denial error from a denied permission request, got nil (res=%+v)", res)
	}

	// The approver must have been consulted for the permission request.
	select {
	case got := <-approver.consulted:
		if got.ToolName != "shell" {
			t.Errorf("consent tool = %q, want %q", got.ToolName, "shell")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("approver was not consulted — permission request was not routed to consent")
	}

	// The deny decision must have reached the driver (Decide), and a deny cancels.
	decisions := fr.ReceivedDecisions()
	if len(decisions) == 0 {
		t.Fatal("no decision routed back to the driver")
	}
	if decisions[0].Allow {
		t.Errorf("decision Allow = true, want false (deny)")
	}
	if !fr.IsCancelled() {
		t.Error("driver was not canceled after a deny decision")
	}

	// [MAJOR] The returned ToolResult MUST reflect the denial — not a success — so
	// the delegating agent is told the sub-run was aborted, not completed.
	if res == nil {
		t.Fatal("expected a non-nil ToolResult on denial")
	}
	if res.Err == nil {
		t.Error("ToolResult.Err is nil on denial; the run must surface an error")
	}
	if !strings.Contains(strings.ToLower(res.ForLLM), "denied") {
		t.Errorf("ToolResult.ForLLM = %q, want it to state the run was denied/aborted", res.ForLLM)
	}
	if reason := "denied for test"; res.Err != nil && !strings.Contains(res.Err.Error(), reason) {
		t.Errorf("ToolResult.Err = %v, want it to carry the deny reason %q", res.Err, reason)
	}
}

// TestExternalDispatch_NoConsentHandler_DeniesByDefault proves that when no
// PolicyApprover is wired, the fail-closed nop approver denies the request.
func TestExternalDispatch_NoConsentHandler_DeniesByDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	al, ts := newExternalTestLoop(t, "opencode", "")
	// Deliberately do NOT call SetToolApprover — loadToolApprover returns the
	// fail-closed nopPolicyApprover (deny-by-default, FR-5.1).
	fr, restore := withFakeDriver(t)
	defer restore()

	// Inject only the permission request. With no approver wired the request is
	// denied by default, and the deny (via FakeRunner.Decide) cancels the run — so
	// we must NOT inject EventKindEnd / Cancel ourselves (that would send on the
	// channel the deny already closed).
	go func() {
		fr.InjectEvent(runner.RunEvent{
			Kind: runner.EventKindPermissionRequest,
			PermissionRequest: &runner.PermissionRequestEvent{
				RequestID: "perm-x",
				ToolName:  "write",
			},
		})
	}()

	runCtx, cancelRun := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelRun()
	res, err := runExternalCLISubTurn(runCtx, al, ts, "task", 30*time.Second)
	// Deny-by-default aborts the run: it must surface as an error, not a success.
	if err == nil {
		t.Fatalf("expected deny-by-default to abort the run, got nil error (res=%+v)", res)
	}

	decisions := fr.ReceivedDecisions()
	if len(decisions) == 0 {
		t.Fatal("no decision routed back to the driver (deny-by-default expected)")
	}
	if decisions[0].Allow {
		t.Error("decision Allow = true, want false (deny-by-default with no approver wired)")
	}
	if res == nil || res.Err == nil {
		t.Fatal("expected ToolResult.Err set on deny-by-default")
	}
}

// TestExternalDispatch_ErrorEvent_FailsRun proves a fatal error event from the
// driver surfaces as an error result.
func TestExternalDispatch_ErrorEvent_FailsRun(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	al, ts := newExternalTestLoop(t, "claude-code", "")
	fr, restore := withFakeDriver(t)
	defer restore()

	go func() {
		fr.InjectEvent(runner.RunEvent{
			Kind: runner.EventKindError,
			Err:  &runner.ErrorEvent{Message: "boom", Fatal: true},
		})
		fr.Cancel()
	}()

	res, err := runExternalCLISubTurn(context.Background(), al, ts, "task", 30*time.Second)
	if err == nil {
		t.Fatal("expected error from fatal error event, got nil")
	}
	if res == nil || res.Err == nil {
		t.Fatal("expected ToolResult.Err to be set")
	}
	if !strings.Contains(res.Err.Error(), "boom") {
		t.Errorf("error = %v, want it to contain 'boom'", res.Err)
	}
}

// TestExternalDispatch_UnknownCLI_FailsCleanly proves an unknown/empty CLI fails
// the run cleanly (not a crash) before any process is spawned.
func TestExternalDispatch_UnknownCLI_FailsCleanly(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	// Use the REAL factory (restore default) so NewDriver rejects the unknown CLI.
	al, ts := newExternalTestLoop(t, "", "")
	res, err := runExternalCLISubTurn(context.Background(), al, ts, "task", 30*time.Second)
	if err == nil {
		t.Fatal("expected error for empty CLI, got nil")
	}
	if res != nil {
		t.Errorf("expected nil result on empty-CLI failure, got %+v", res)
	}
}

// TestExternalDispatch_WorktreeForGitRepo prepares the run inside a git worktree
// when the agent workspace is a git repo (FR-5.3), and tears it down.
func TestExternalDispatch_WorktreeForGitRepo(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	// Build a tiny git repo to act as the agent workspace.
	repo := t.TempDir()
	mustGit(t, repo, "init")
	mustGit(t, repo, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "--allow-empty", "-m", "init")
	if err := os.WriteFile(filepath.Join(repo, "f.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	mustGit(t, repo, "add", ".")
	mustGit(t, repo, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-m", "add f")

	al, ts := newExternalTestLoop(t, "claude-code", repo)
	fr, restore := withFakeDriver(t)
	defer restore()

	go func() {
		fr.InjectEvent(runner.RunEvent{Kind: runner.EventKindOutput, Output: &runner.OutputEvent{Text: "done"}})
		fr.InjectEvent(runner.RunEvent{Kind: runner.EventKindEnd})
		fr.Cancel()
	}()

	res, err := runExternalCLISubTurn(context.Background(), al, ts, "task", 30*time.Second)
	if err != nil {
		t.Fatalf("runExternalCLISubTurn error: %v", err)
	}
	if res.ForLLM != "done" {
		t.Errorf("output = %q, want %q", res.ForLLM, "done")
	}
	workDir := fr.RecordedRunOpts()[0].WorkDir
	if _, statErr := os.Stat(workDir); !os.IsNotExist(statErr) {
		t.Errorf("worktree %q not cleaned up", workDir)
	}
}

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// newRecordResultTurnState builds a minimal child turnState wired to a real
// UnifiedStore (in a temp dir) so recordExternalToolResult's append path
// persists a transcript entry we can read back. The in-place update path is
// exercised but finds no matching pending "completed" entry (fresh session), so
// it falls back to the append path — exactly what we assert on.
func newRecordResultTurnState(t *testing.T) (*turnState, *session.UnifiedStore, string) {
	t.Helper()
	baseDir := filepath.Join(t.TempDir(), "sessions")
	store, err := session.NewUnifiedStore(baseDir)
	require.NoError(t, err)
	const sessionID = "session_record_result_test"
	ts := &turnState{
		agentID:             "ext-agent",
		turnID:              "ext-run-result",
		transcriptSessionID: sessionID,
		transcriptStore:     store,
	}
	return ts, store, sessionID
}

// lastToolCall reads the transcript for sessionID and returns the ToolCall on
// the last entry. require-fails when the transcript is empty or the last entry
// has no tool calls.
func lastToolCall(t *testing.T, store *session.UnifiedStore, sessionID string) session.ToolCall {
	t.Helper()
	entries, err := store.ReadTranscript(sessionID)
	require.NoError(t, err)
	require.NotEmpty(t, entries, "expected at least one transcript entry")
	last := entries[len(entries)-1]
	require.NotEmpty(t, last.ToolCalls,
		"last transcript entry must carry a tool call: %+v", last)
	return last.ToolCalls[0]
}

// TestRecordExternalToolResult covers the unexported recordExternalToolResult:
//   - Normal JSON output → Result is parsed, status="success"
//   - IsError=true → status="error"
//   - Non-JSON output → Result == {"output": "<raw string>"}
//   - Empty CallID → generated ID is non-empty and unique across two calls
//
// Traces to: pkg/agent/external_dispatch.go recordExternalToolResult — the
// status/result/id derivation that mirrors an external runner's tool result
// into the sub-agent session transcript.
func TestRecordExternalToolResult(t *testing.T) {
	t.Run("normal JSON output parses into Result with status success", func(t *testing.T) {
		ts, store, sid := newRecordResultTurnState(t)
		recordExternalToolResult(ts, &runner.ToolResultEvent{
			CallID:   "call-json",
			ToolName: "read_file",
			Output:   []byte(`{"path":"/x","bytes":42}`),
		})
		tc := lastToolCall(t, store, sid)
		assert.Equal(t, "success", tc.Status, "a non-error result must be status=success")
		assert.Equal(t, "call-json", string(tc.ID))
		assert.Equal(t, "read_file", tc.Tool)
		require.NotNil(t, tc.Result)
		assert.Equal(t, "/x", tc.Result["path"])
		assert.EqualValues(t, 42, tc.Result["bytes"])
	})

	t.Run("IsError true yields status error", func(t *testing.T) {
		ts, store, sid := newRecordResultTurnState(t)
		recordExternalToolResult(ts, &runner.ToolResultEvent{
			CallID:   "call-err",
			ToolName: "write_file",
			Output:   []byte(`{"message":"disk full"}`),
			IsError:  true,
		})
		tc := lastToolCall(t, store, sid)
		assert.Equal(t, "error", tc.Status, "an IsError result must be status=error")
		require.NotNil(t, tc.Result)
		assert.Equal(t, "disk full", tc.Result["message"])
	})

	t.Run("non-JSON output is wrapped as {output: raw string}", func(t *testing.T) {
		ts, store, sid := newRecordResultTurnState(t)
		raw := "this is not json >>>"
		recordExternalToolResult(ts, &runner.ToolResultEvent{
			CallID:   "call-raw",
			ToolName: "shell",
			Output:   []byte(raw),
		})
		tc := lastToolCall(t, store, sid)
		assert.Equal(t, "success", tc.Status)
		require.NotNil(t, tc.Result, "non-JSON output must still produce a Result map")
		assert.Equal(t, raw, tc.Result["output"],
			`non-JSON output must be stored under the "output" key as a raw string`)
	})

	t.Run("empty CallID generates a non-empty unique ID", func(t *testing.T) {
		ts, store, sid := newRecordResultTurnState(t)

		recordExternalToolResult(ts, &runner.ToolResultEvent{
			CallID:   "",
			ToolName: "shell",
			Output:   []byte(`{"ok":true}`),
		})
		first := lastToolCall(t, store, sid)
		assert.NotEmpty(t, string(first.ID),
			"an empty CallID must yield a generated non-empty ID")

		// A small gap guarantees the UnixNano-derived IDs differ even on
		// platforms with coarse clock resolution.
		time.Sleep(2 * time.Millisecond)

		recordExternalToolResult(ts, &runner.ToolResultEvent{
			CallID:   "",
			ToolName: "shell",
			Output:   []byte(`{"ok":true}`),
		})
		entries, err := store.ReadTranscript(sid)
		require.NoError(t, err)
		require.Len(t, entries, 2, "expected two appended transcript entries")
		require.NotEmpty(t, entries[0].ToolCalls)
		require.NotEmpty(t, entries[1].ToolCalls)
		second := entries[1].ToolCalls[0]
		assert.NotEmpty(t, string(second.ID), "second generated ID must be non-empty")
		assert.NotEqual(t, first.ID, second.ID,
			"two calls with empty CallID must produce distinct generated IDs")
	})
}

// TestExternalDispatch_G2_NeverAttributesTokens is the production-gate test for the
// subagent_3p scope guard (token-usage-tracking-2026-06 / ADR-023 §D4): an
// external-CLI sub-turn must contribute ZERO usage. The external CLI runs its own
// LLM on a separate engine we cannot meter, so runExternalCLISubTurn must NEVER call
// AddTurnStats / AddTurnCacheStats — regardless of what the run emits.
//
// Unlike the session-side simulation (pkg/session TestAppendTranscript_G2_*, which
// only proves a zero-token entry contributes zero), this drives the real dispatch
// path with a fake driver and asserts the turn's accumulated stats stay zero. It
// fails loudly if anyone later wires token attribution into the external path.
func TestExternalDispatch_G2_NeverAttributesTokens(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	al, ts := newExternalTestLoop(t, "claude-code", "")
	fr, restore := withFakeDriver(t)
	defer restore()

	// Emit a normal run (output + end). Even a token-bearing external run must not
	// flow into the turn's stats — the dispatch path reads no usage counters.
	go func() {
		fr.InjectEvent(
			runner.RunEvent{Kind: runner.EventKindOutput, Output: &runner.OutputEvent{Text: "external work done"}},
		)
		fr.InjectEvent(runner.RunEvent{Kind: runner.EventKindEnd})
		fr.Cancel()
	}()

	res, err := runExternalCLISubTurn(context.Background(), al, ts, "task", 30*time.Second)
	require.NoError(t, err)
	require.NotNil(t, res)

	tokens, cost := ts.GetTurnStats()
	assert.Zero(t, tokens, "external-CLI sub-turn must attribute 0 tokens (subagent_3p is not metered)")
	assert.Zero(t, cost, "external-CLI sub-turn must attribute 0 cost")

	cacheRead, cacheWrite := ts.GetTurnCacheStats()
	assert.Zero(t, cacheRead, "external-CLI sub-turn must attribute 0 cache-read tokens")
	assert.Zero(t, cacheWrite, "external-CLI sub-turn must attribute 0 cache-write tokens")
}

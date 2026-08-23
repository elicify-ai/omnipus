package agent

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// denyApprover is a PolicyApprover stub that always denies, recording whether it
// was consulted. Used to prove external-CLI permission requests bypass the
// wired PolicyApprover entirely (issue #488) — even one that would deny
// everything is never consulted.
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
//
// ADR-032: external-CLI runs execute directly in the agent's own workspace dir
// (no isolated worktree/tempdir fallback). Callers that don't care about a
// specific path get a real temp dir here so existing "workspace doesn't
// matter for this test" call sites (workspace="") keep working without every
// test needing its own t.TempDir().
//
// ADR-046 P1 (FR-007/008): execution is always workspace-scoped, for EVERY
// dispatch kind — an agent that is a member of no workspace is now REFUSED
// (ErrAgentNotWorkspaceMember), not fallen back to agent.Home/workspace as
// before. This helper auto-seeds ts.agent.ID into the shared test-harness
// workspace (seedTestWorkspaceMembershipForIDs, test_helpers_test.go) —
// mirroring mustNewAgentLoop's own ensureTestWorkspaceMembership for native
// agents — so the many tests in this file that exercise UNRELATED behavior
// (model auto-set, permission auto-approval, error handling, egress-proxy
// injection, token-attribution scope, ...) are unaffected by the membership
// gate; the `workspace` parameter still sets agent.Home, but agent.Home no
// longer drives WorkDir resolution at all under this gate — WorkDir is
// always the resolved workspace's work/ directory now (see
// resolveTurnWorkDirOrRefuse, workspace_reroot.go).
//
// A test that specifically wants to exercise the "member of no workspace"
// refusal path (or a member whose workspace id/work-dir resolution itself
// fails) must build its own turnState directly instead of using this helper
// — see TestExternalDispatch_WorkspacelessAgentRefused and
// TestExternalDispatch_MemberWithUnsafeWorkspaceID_Refused.
func newExternalTestLoop(t *testing.T, cli, workspace string) (*AgentLoop, *turnState) {
	t.Helper()
	if workspace == "" {
		workspace = t.TempDir()
	}
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{DefaultModel: config.DefaultModel{Provider: "mock"}},
			List:     []config.AgentConfig{{ID: "mia"}},
		},
	}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &simpleMockProviderAPI{response: "ok"})
	agent := &AgentInstance{
		ID:   "ext-agent",
		Name: "External Agent",
		Home: workspace,
		Subagents: &config.SubagentsConfig{
			Executor: &config.ExecutorConfig{Kind: config.ExecutorKindExternalCLI, CLI: cli},
		},
	}
	seedTestWorkspaceMembershipForIDs(t, []string{agent.ID})
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

// TestExternalDispatch_StreamsOutput_RunsInWorkspaceDir drives the full external
// dispatch flow with a fake driver: the driver's output is streamed into the
// aggregated result, and the CLI runs directly in the resolved workspace's
// work/ directory (ADR-032 — no isolated worktree/tempdir copy; ADR-046 P1 —
// always the workspace's work/ dir, never agent.Home), which is left in place
// (not torn down) after the run since it is the workspace's persistent tree.
func TestExternalDispatch_StreamsOutput_RunsInWorkspaceDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	al, ts := newExternalTestLoop(t, "claude-code", "")
	fr, restore := withFakeDriver(t)
	defer restore()

	// ADR-046 P1: newExternalTestLoop seeds ts.agent into a workspace, so the
	// run resolves to THAT workspace's work/ dir, independent of agent.Home —
	// resolve it the same way runExternalCLISubTurn itself does so this
	// assertion doesn't hardcode the harness's internal seed-workspace id.
	wantWorkDir, wsErr := resolveTurnWorkDirOrRefuse(context.Background(), ts.agent.ID, ts.agent.Home, ts.opts.WorkspaceID)
	if wsErr != nil {
		t.Fatalf("resolveTurnWorkDirOrRefuse: %v", wsErr)
	}

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

	// The run options passed to the driver carry the workspace dir, input,
	// timeout and turn cap (FR-5.4 bound).
	opts := fr.RecordedRunOpts()
	if len(opts) != 1 {
		t.Fatalf("driver Run called %d times, want 1", len(opts))
	}
	if opts[0].Input != "do the task" {
		t.Errorf("driver input = %q, want %q", opts[0].Input, "do the task")
	}
	// ADR-046 P1: WorkDir must be EXACTLY the resolved workspace's work/
	// directory — agent.Home is never consulted for this anymore.
	if opts[0].WorkDir != wantWorkDir {
		t.Errorf("driver WorkDir = %q, want the workspace's work/ dir %q (ADR-046 P1)", opts[0].WorkDir, wantWorkDir)
	}
	if opts[0].TimeoutSeconds != 30 {
		t.Errorf("driver TimeoutSeconds = %d, want 30", opts[0].TimeoutSeconds)
	}
	if opts[0].MaxTurns != DefaultExternalMaxTurns {
		t.Errorf("driver MaxTurns = %d, want %d", opts[0].MaxTurns, DefaultExternalMaxTurns)
	}

	// ADR-032: the workspace dir is a PERSISTENT tree — it must NOT be torn
	// down after the run (no more worktree-style cleanup).
	if _, statErr := os.Stat(opts[0].WorkDir); statErr != nil {
		t.Errorf("workspace dir %q must still exist after the run (no teardown, ADR-032): stat err=%v",
			opts[0].WorkDir, statErr)
	}
}

// TestExternalDispatch_WorkspacelessAgentRefused (ADR-046 P1, FR-007/008)
// replaces the old TestExternalDispatch_EmptyWorkspace_HardError and
// TestExternalDispatch_NotCoreTeamMember_FallsBackToAgentWorkspace: an
// external-CLI agent that is a member of NO workspace must have its dispatch
// REFUSED with an error wrapping ErrAgentNotWorkspaceMember — no fallthrough
// to agent.Home (empty or not), matching the native runTurn path exactly (the
// asymmetry a 7-reviewer gate flagged as a BLOCK). The external CLI driver
// must never even be instantiated: this deliberately builds its own
// turnState directly (NOT via newExternalTestLoop, which auto-seeds
// membership) so the agent is genuinely unseeded, and asserts the fake
// driver's Run was never called.
func TestExternalDispatch_WorkspacelessAgentRefused(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{DefaultModel: config.DefaultModel{Provider: "mock"}},
			List:     []config.AgentConfig{{ID: "mia"}},
		},
	}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &simpleMockProviderAPI{response: "ok"})
	agent := &AgentInstance{
		ID:   "ext-agent-no-workspace",
		Name: "External Agent With No Workspace",
		Home: t.TempDir(), // a non-empty Home must NOT rescue this dispatch
		Subagents: &config.SubagentsConfig{
			Executor: &config.ExecutorConfig{Kind: config.ExecutorKindExternalCLI, CLI: "claude-code"},
		},
	}
	ts := &turnState{
		agent:               agent,
		agentID:             agent.ID,
		turnID:              "ext-run-no-workspace",
		transcriptSessionID: "session_ext_no_workspace_test",
		chatID:              "webchat:ext-parent",
	}
	ts.routingSessionID = session.RoutingSessionID("session_ext_no_workspace_test")

	sub := al.SubscribeEvents(32)
	t.Cleanup(func() { al.UnsubscribeEvents(sub.ID) })

	fr, restore := withFakeDriver(t)
	defer restore()

	res, err := runExternalCLISubTurn(context.Background(), al, ts, "task", 30*time.Second)
	if err == nil {
		t.Fatal("expected an error for a workspace-less agent, got nil")
	}
	if !errors.Is(err, ErrAgentNotWorkspaceMember) {
		t.Errorf("error = %v, want it to wrap ErrAgentNotWorkspaceMember", err)
	}
	if res != nil {
		t.Errorf("expected nil result on refusal, got %+v", res)
	}
	if opts := fr.RecordedRunOpts(); len(opts) != 0 {
		t.Errorf(
			"driver Run called %d times, want 0 — the external CLI must never be spawned for a refused dispatch",
			len(opts),
		)
	}

	events := drainEvents(sub.C)
	var errPayloads []ErrorPayload
	for _, e := range events {
		if e.Kind == EventKindError {
			if p, ok := e.Payload.(ErrorPayload); ok {
				errPayloads = append(errPayloads, p)
			}
		}
	}
	if len(errPayloads) == 0 {
		t.Fatalf("external-CLI workspace refusal must emit EventKindError — the sentinel was known and the thread stayed silent; events=%v", eventKinds(events))
	}
	found := false
	for _, p := range errPayloads {
		if p.Code == string(CodeAgentNotConfigured) {
			found = true
			if p.Message != UserMessageForCode(CodeAgentNotConfigured) {
				t.Errorf("message = %q, want catalogue copy", p.Message)
			}
			if p.Stage != "workspace" {
				t.Errorf("stage = %q, want workspace", p.Stage)
			}
			if p.ChatID != "webchat:ext-parent" {
				t.Errorf("ChatID = %q, want webchat:ext-parent", p.ChatID)
			}
			if p.SessionID != "session_ext_no_workspace_test" {
				t.Errorf("SessionID = %q, want session_ext_no_workspace_test", p.SessionID)
			}
		}
	}
	if !found {
		t.Errorf("EventKindError must carry agent_not_configured; payloads=%+v", errPayloads)
	}
}

// TestExternalDispatch_MemberWithUnsafeWorkspaceID_Refused (ADR-046 P1,
// FR-007/008) proves the SECOND refusal branch of resolveTurnWorkDirOrRefuse:
// a MEMBER whose resolved workspace id fails the SafeWorkDir traversal guard
// must also be refused, not warn-and-fall-through to agent.Home (the HIGH a
// 7-reviewer gate found in the old runExternalCLISubTurn, which only warned
// and kept going). A workspace record's own "id" field — not its filename —
// is what FindForAgent uses, so a record stored at a safe filename can still
// carry an unsafe id.
func TestExternalDispatch_MemberWithUnsafeWorkspaceID_Refused(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	agentWorkspace := t.TempDir()
	al, ts := newExternalTestLoop(t, "claude-code", agentWorkspace)
	// newExternalTestLoop already seeded ts.agent.ID into the shared harness
	// workspace; overwrite that record's OWN "id" field (not its filename)
	// with a traversal-unsafe value so FindForAgent resolves an id that fails
	// safeID/SafeWorkDir.
	wsDir := filepath.Join(home, "workspaces")
	wsJSON := `{"id":"../escape","core_team":["` + ts.agent.ID + `"]}`
	if err := os.WriteFile(
		filepath.Join(wsDir, testHarnessWorkspaceMembershipID+".json"),
		[]byte(wsJSON),
		0o644,
	); err != nil {
		t.Fatalf("overwrite harness workspace record: %v", err)
	}

	fr, restore := withFakeDriver(t)
	defer restore()

	res, err := runExternalCLISubTurn(context.Background(), al, ts, "task", 30*time.Second)
	if err == nil {
		t.Fatal("expected an error for a member with an unsafe workspace id, got nil")
	}
	if errors.Is(err, ErrAgentNotWorkspaceMember) {
		t.Errorf(
			"error = %v, want a SafeWorkDir/traversal error, not ErrAgentNotWorkspaceMember (the agent IS a member)",
			err,
		)
	}
	if res != nil {
		t.Errorf("expected nil result on refusal, got %+v", res)
	}
	if opts := fr.RecordedRunOpts(); len(opts) != 0 {
		t.Errorf(
			"driver Run called %d times, want 0 — the external CLI must never be spawned for a refused dispatch",
			len(opts),
		)
	}
}

// TestExternalDispatch_ModelAutoSet proves the delegate's configured Model
// (childTS.agent.Model) flows through to RunOptions.Model, which each driver's
// buildArgs uses to auto-set the CLI's model flag (ADR-032 fix C).
func TestExternalDispatch_ModelAutoSet(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	al, ts := newExternalTestLoop(t, "claude-code", "")
	ts.agent.Model = "claude-sonnet-4.6"
	fr, restore := withFakeDriver(t)
	defer restore()

	go func() {
		fr.InjectEvent(runner.RunEvent{Kind: runner.EventKindOutput, Output: &runner.OutputEvent{Text: "ok"}})
		fr.InjectEvent(runner.RunEvent{Kind: runner.EventKindEnd})
		fr.Cancel()
	}()

	_, err := runExternalCLISubTurn(context.Background(), al, ts, "task", 30*time.Second)
	if err != nil {
		t.Fatalf("runExternalCLISubTurn error: %v", err)
	}

	opts := fr.RecordedRunOpts()
	if len(opts) != 1 {
		t.Fatalf("driver Run called %d times, want 1", len(opts))
	}
	if opts[0].Model != "claude-sonnet-4.6" {
		t.Errorf("RunOptions.Model = %q, want the delegate's configured model %q", opts[0].Model, "claude-sonnet-4.6")
	}
}

// TestExternalDispatch_PermissionRequestAutoApproved proves external-CLI
// permission requests are auto-approved unconditionally (operator decision,
// 2026-07-05, issue #488): even a wired PolicyApprover that denies every
// request is NEVER consulted, the driver receives an unconditional Allow, and
// the run completes successfully instead of being canceled.
func TestExternalDispatch_PermissionRequestAutoApproved(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	al, ts := newExternalTestLoop(t, "codex", "")
	approver := &denyApprover{consulted: make(chan runner.ConsentRequest, 1)}
	al.SetToolApprover(approver)

	fr, restore := withFakeDriver(t)
	defer restore()

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
		fr.InjectEvent(runner.RunEvent{Kind: runner.EventKindOutput, Output: &runner.OutputEvent{Text: "ok"}})
		fr.InjectEvent(runner.RunEvent{Kind: runner.EventKindEnd})
		fr.Cancel()
	}()

	runCtx, cancelRun := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelRun()
	res, err := runExternalCLISubTurn(runCtx, al, ts, "task", 30*time.Second)
	if err != nil {
		t.Fatalf("expected the run to complete despite the permission request, got error: %v (res=%+v)", err, res)
	}

	// The wired approver (which would deny everything) must NEVER be consulted.
	select {
	case got := <-approver.consulted:
		t.Fatalf(
			"approver was consulted (tool=%q) — external-CLI permission requests must auto-approve without consulting any PolicyApprover",
			got.ToolName,
		)
	case <-time.After(200 * time.Millisecond):
		// expected: no consultation.
	}

	// Note: the test's own injection goroutine calls fr.Cancel() to close the
	// event channel and let the drain loop terminate (the same pattern every
	// other FakeRunner-based test in this file uses) — so IsCancelled() is
	// always true here and is not a useful signal either way. The decision
	// itself is what proves auto-approval: FakeRunner.Decide only triggers an
	// internal Cancel on !Allow (mirroring the real deny-cancels-the-run
	// contract), so an Allow=true decision below proves this run was never
	// canceled BY the consent path.
	decisions := fr.ReceivedDecisions()
	if len(decisions) == 0 {
		t.Fatal("no decision routed back to the driver")
	}
	if !decisions[0].Allow {
		t.Error("decision Allow = false, want true (external-CLI auto-approves unconditionally)")
	}

	if res == nil {
		t.Fatal("expected a non-nil ToolResult")
	}
	if res.Err != nil {
		t.Errorf("ToolResult.Err = %v, want nil (auto-approved run must succeed)", res.Err)
	}
}

// TestExternalDispatch_NoApproverWired_StillAutoApproves proves external-CLI
// permission requests auto-approve even when no PolicyApprover has ever been
// wired (SetToolApprover never called) — unlike native ask-policy tools,
// external-CLI consent never falls through to the fail-closed nopPolicyApprover
// (issue #488: this path no longer consults loadToolApprover at all).
func TestExternalDispatch_NoApproverWired_StillAutoApproves(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	al, ts := newExternalTestLoop(t, "opencode", "")
	// Deliberately do NOT call SetToolApprover.
	fr, restore := withFakeDriver(t)
	defer restore()

	go func() {
		fr.InjectEvent(runner.RunEvent{
			Kind: runner.EventKindPermissionRequest,
			PermissionRequest: &runner.PermissionRequestEvent{
				RequestID: "perm-x",
				ToolName:  "write",
			},
		})
		fr.InjectEvent(runner.RunEvent{Kind: runner.EventKindOutput, Output: &runner.OutputEvent{Text: "ok"}})
		fr.InjectEvent(runner.RunEvent{Kind: runner.EventKindEnd})
		fr.Cancel()
	}()

	runCtx, cancelRun := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelRun()
	res, err := runExternalCLISubTurn(runCtx, al, ts, "task", 30*time.Second)
	if err != nil {
		t.Fatalf("expected the run to complete with no approver wired, got error: %v (res=%+v)", err, res)
	}

	decisions := fr.ReceivedDecisions()
	if len(decisions) == 0 {
		t.Fatal("no decision routed back to the driver")
	}
	if !decisions[0].Allow {
		t.Error("decision Allow = false, want true (auto-approve requires no PolicyApprover at all)")
	}
	if res == nil || res.Err != nil {
		t.Fatalf("expected a successful ToolResult with no approver wired; res=%+v", res)
	}
}

// TestExternalDispatch_ErrorEvent_FailsRun proves a fatal error event from the
// driver surfaces as an error result. ADR-051 B2: the raw error message
// ("boom") must NOT appear in the surfaced error — only the sanitized generic
// copy. The raw stays in gateway.log (slog.Warn raw_log_text).
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
	// The error must indicate the run failed, but must NOT carry the raw
	// "boom" text (ADR-051 B2 LogText-leak fix).
	if !strings.Contains(res.Err.Error(), "external-cli run failed") {
		t.Errorf("error = %v, want it to contain 'external-cli run failed'", res.Err)
	}
	if strings.Contains(res.Err.Error(), "boom") {
		t.Errorf("error = %v, must NOT contain raw stderr 'boom' (B2 regression)", res.Err)
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

// TestExternalDispatch_GitRepoWorkspace_RunsInRepoDirDirectly proves that when
// the RESOLVED WORKSPACE work/ directory IS a git repo, the external CLI
// still runs directly IN that directory — NOT a separate `git worktree`
// checkout (ADR-032 removed the FR-5.3 worktree-isolation step for external
// CLIs). The repo directory and its tracked file must remain present and
// untouched after the run (no teardown).
//
// ADR-046 P1: this no longer builds the git repo at agent.Home — WorkDir is
// always the resolved workspace's work/ dir now, independent of agent.Home —
// so the repo is initialized IN PLACE at the dir resolveTurnWorkDirOrRefuse
// itself resolves to (newExternalTestLoop already seeds ts.agent into a
// workspace), proving the no-worktree-isolation property still holds under
// workspace-scoped execution.
func TestExternalDispatch_GitRepoWorkspace_RunsInRepoDirDirectly(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	al, ts := newExternalTestLoop(t, "claude-code", "")
	repo, wsErr := resolveTurnWorkDirOrRefuse(context.Background(), ts.agent.ID, ts.agent.Home, ts.opts.WorkspaceID)
	if wsErr != nil {
		t.Fatalf("resolveTurnWorkDirOrRefuse: %v", wsErr)
	}

	// Turn the resolved workspace work/ dir into a tiny git repo.
	mustGit(t, repo, "init")
	mustGit(t, repo, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "--allow-empty", "-m", "init")
	if err := os.WriteFile(filepath.Join(repo, "f.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	mustGit(t, repo, "add", ".")
	mustGit(t, repo, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-m", "add f")

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

	// ADR-032/ADR-046 P1: WorkDir must be the repo dir itself — no separate
	// worktree copy.
	workDir := fr.RecordedRunOpts()[0].WorkDir
	if workDir != repo {
		t.Errorf("driver WorkDir = %q, want the resolved workspace's git-repo work/ dir %q (no worktree copy)",
			workDir, repo)
	}

	// The repo directory and its tracked file must still be present — the
	// workspace's work/ dir is never torn down.
	if _, statErr := os.Stat(filepath.Join(repo, "f.txt")); statErr != nil {
		t.Errorf("tracked file f.txt missing from workspace after run: %v", statErr)
	}
	// No git-worktree metadata should have been registered against the repo
	// (a stray worktree would show up in `git worktree list` beyond the main one).
	out := mustGitOutput(t, repo, "worktree", "list")
	if strings.Count(strings.TrimSpace(out), "\n") != 0 {
		t.Errorf("expected no additional git worktrees registered against the repo; `git worktree list` = %q", out)
	}
}

// TestExternalDispatch_CoreTeamMember_RunsInWorkspaceSharedDir proves the
// operator-mandated requirement that every agent belonging to a Workspace's
// CoreTeam — native or subagent_3p, no exceptions by kind — actually runs in
// that Workspace's dedicated project-work subdirectory, not its private
// per-agent one. When the dispatching agent's ID is a member of a real,
// on-disk workspace's core_team, RunOptions.WorkDir must be that workspace's
// work/ directory ($OMNIPUS_HOME/workspaces/<id>/work/) instead of
// agent.Home — deliberately not the workspace's own root directory,
// which also holds AGENT.md and the shared memory room.
//
// This builds its AgentLoop/turnState directly (NOT via newExternalTestLoop,
// which auto-seeds ts.agent into the SHARED harness workspace) — this test
// needs precise, single-membership control (an agent seeded into BOTH the
// harness workspace and this test's own "ws-shared" would hit
// FindForAgent's documented ambiguous-multi-membership tiebreak and silently
// resolve the wrong one).
func TestExternalDispatch_CoreTeamMember_RunsInWorkspaceSharedDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{DefaultModel: config.DefaultModel{Provider: "mock"}},
			List:     []config.AgentConfig{{ID: "mia"}},
		},
	}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &simpleMockProviderAPI{response: "ok"})
	agentWorkspace := t.TempDir()
	agent := &AgentInstance{
		ID:   "ext-agent-coreteam",
		Name: "External Agent CoreTeam Member",
		Home: agentWorkspace,
		Subagents: &config.SubagentsConfig{
			Executor: &config.ExecutorConfig{Kind: config.ExecutorKindExternalCLI, CLI: "claude-code"},
		},
	}
	ts := &turnState{
		agent:               agent,
		agentID:             agent.ID,
		turnID:              "ext-run-coreteam",
		transcriptSessionID: "session_ext_coreteam_test",
	}

	wsDir := filepath.Join(home, "workspaces")
	if err := os.MkdirAll(wsDir, 0o755); err != nil {
		t.Fatalf("mkdir workspaces dir: %v", err)
	}
	wsJSON := `{"id":"ws-shared","core_team":["` + agent.ID + `"]}`
	if err := os.WriteFile(filepath.Join(wsDir, "ws-shared.json"), []byte(wsJSON), 0o644); err != nil {
		t.Fatalf("write workspace record: %v", err)
	}

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

	opts := fr.RecordedRunOpts()
	if len(opts) != 1 {
		t.Fatalf("driver Run called %d times, want 1", len(opts))
	}
	wantDir := filepath.Join(home, "workspaces", "ws-shared", "work")
	if opts[0].WorkDir != wantDir {
		t.Errorf("driver WorkDir = %q, want the workspace's dedicated work/ directory %q (CoreTeam membership)",
			opts[0].WorkDir, wantDir)
	}
	if opts[0].WorkDir == agentWorkspace {
		t.Errorf("driver WorkDir must NOT be the agent's private workspace %q when the agent is a CoreTeam member",
			agentWorkspace)
	}
	if opts[0].WorkDir == filepath.Join(home, "workspaces", "ws-shared") {
		t.Errorf("driver WorkDir must NOT be the workspace's own root directory %q — only its work/ subdirectory",
			opts[0].WorkDir)
	}

	// The shared workspace dir must actually exist (MkdirAll is still applied
	// against whichever dir was chosen).
	if _, statErr := os.Stat(wantDir); statErr != nil {
		t.Errorf("expected shared workspace dir to exist: %v", statErr)
	}
}

// Note: the old TestExternalDispatch_NotCoreTeamMember_FallsBackToAgentWorkspace
// pinned a "no membership -> fall back to agent.Home" contract that ADR-046
// P1 retires entirely — see TestExternalDispatch_WorkspacelessAgentRefused
// above, which replaces it: a non-member is now REFUSED, full stop.

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// mustGitOutput runs a git subcommand in dir and returns its combined output,
// failing the test on a non-zero exit.
func mustGitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
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
	// ADR-057 US-1/W3 fixture repair: AppendTranscript is now STRICT — it
	// reads meta FIRST and fails loudly ("session ... does not exist")
	// instead of silently creating an orphan directory (the old behavior
	// this migration deliberately removed). A hand-picked literal session id
	// that was never minted via NewSession no longer works; recordExternalToolResult's
	// underlying AppendTranscript call would fail and lastToolCall would find
	// an empty transcript.
	meta, err := store.NewSession(session.SessionTypeChat, "", "ext-agent")
	require.NoError(t, err)
	sessionID := meta.ID
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

// TestExternalDispatch_SanitizesRunnerError is the Wave 1 regression for
// ADR-051 §RD5 MAJ-005 (REST-executor in-scope) and the new fail-closed
// sanitizer. A fatal runner error event with raw stderr text must NOT
// reach the assistant transcript verbatim — SanitizeRunnerError gates
// the raw message and emits a generic copy. The structured EventKindError
// is still emitted on the bus (with the generic copy in its Message
// field), but the raw stderr stays out of the assistant path.
func TestExternalDispatch_SanitizesRunnerError(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	al, ts := newExternalTestLoop(t, "claude-code", "")

	// Wire a real UnifiedStore so appendIntermediateAssistantTranscript
	// actually persists the sanitized message — we read it back below.
	// ADR-057 U2: both appendIntermediateAssistantTranscript and
	// appendErrorTranscript write through AppendTranscriptStrict, which
	// refuses (WARN-logged, not propagated) a write against a session id
	// with no meta.json on disk rather than minting an orphan directory —
	// see AppendTranscriptStrict's doc comment. A bare, never-created id
	// string here would make every append silently fail, leaving the
	// transcript empty and this test's assertions unfalsifiable-false — so
	// a real session must be minted first.
	store, err := session.NewUnifiedStore(t.TempDir() + "/sessions")
	require.NoError(t, err)
	meta, err := store.NewSession(session.SessionTypeChat, "test", ts.agentID)
	require.NoError(t, err)
	sessionID := meta.ID
	ts.transcriptStore = store
	ts.transcriptSessionID = sessionID

	// Subscribe to the bus so we can capture EventKindError events.
	sub := al.SubscribeEvents(16)
	defer al.UnsubscribeEvents(sub.ID)

	fr, restore := withFakeDriver(t)
	defer restore()

	rawStderr := "fatal: failed to write to /home/user/.config/claude/secrets.json: permission denied"
	go func() {
		fr.InjectEvent(runner.RunEvent{
			Kind: runner.EventKindError,
			Err: &runner.ErrorEvent{
				Message: rawStderr,
				Fatal:   true,
			},
		})
		fr.Cancel()
	}()

	res, err := runExternalCLISubTurn(context.Background(), al, ts, "task", 30*time.Second)
	require.Error(t, err, "fatal error event must surface as a run failure")
	require.NotNil(t, res)
	require.NotNil(t, res.Err)
	assert.Contains(t, res.Err.Error(), "external-cli run failed",
		"the returned error must indicate the run failed (sanitized)")

	// ADR-051 B2 fix: ForLLM must NOT carry the raw stderr — it flows
	// directly into the parent agent's tool-result context. Only the
	// sanitized generic copy is safe for the LLM to see.
	assert.NotContains(t, res.ForLLM, rawStderr,
		"ForLLM must NOT contain raw stderr (B2 LogText-leak regression)")
	assert.NotContains(t, res.Err.Error(), rawStderr,
		"the returned error string must NOT contain raw stderr (B2 regression)")

	// Read the transcript back. The assistant line written by
	// appendIntermediateAssistantTranscript must be the SANITIZED
	// "[external-cli error] <generic>" copy — NOT the raw stderr.
	entries, err := store.ReadTranscript(sessionID)
	require.NoError(t, err)

	var sanitizedAssistant string
	for _, e := range entries {
		if e.Role == "assistant" && strings.Contains(e.Content, "[external-cli error]") {
			sanitizedAssistant = e.Content
			break
		}
	}
	require.NotEmpty(t, sanitizedAssistant, "an assistant line for the external-cli error must be written")
	assert.NotContains(t, sanitizedAssistant, rawStderr,
		"raw stderr must NEVER reach the assistant transcript (sanitizer regression)")
	assert.Contains(t, sanitizedAssistant, "[external-cli error]",
		"the marker prefix must be preserved")

	// The bus must have carried an EventKindError (sanitized generic
	// copy in Message). The structured event is needed for the WS
	// forwarder so the SPA can render the error live.
	var foundErrEvent bool
drain:
	for i := 0; i < 16; i++ {
		select {
		case ev := <-sub.C:
			if ev.Kind != EventKindError {
				continue
			}
			ep, ok := ev.Payload.(ErrorPayload)
			if !ok {
				continue
			}
			if ep.Stage != "external_cli" {
				continue
			}
			foundErrEvent = true
			assert.NotContains(t, ep.Message, rawStderr,
				"EventKindError Message must also be sanitized (no raw stderr leak)")
		default:
			break drain
		}
	}
	assert.True(t, foundErrEvent,
		"EventKindError for external_cli stage must be emitted on the bus")
}

// TestExternalDispatch_ForLLM_NoProviderShapedLeak is the ADR-051 B2
// regression for provider-shaped raw stderr in the fatal-error path.
// When the runner emits a fatal error whose raw text looks like a provider
// response body (e.g. the classic "Downloaded response does not contain a
// valid JPG" body-parse failure), neither ToolResult.ForLLM nor the
// returned error string may carry that raw text — only the generic
// classifier copy. The raw stays in gateway.log (slog.Warn raw_log_text).
func TestExternalDispatch_ForLLM_NoProviderShapedLeak(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	al, ts := newExternalTestLoop(t, "codex", "")

	store, err := session.NewUnifiedStore(t.TempDir() + "/sessions")
	require.NoError(t, err)
	ts.transcriptStore = store
	ts.transcriptSessionID = "session_ext_provider_leak"

	fr, restore := withFakeDriver(t)
	defer restore()

	// Provider-shaped raw text — the kind a CLI stderr carries when the
	// upstream provider returns a non-JSON body or a malformed image.
	rawStderr := "Downloaded response does not contain a valid JPG"
	go func() {
		fr.InjectEvent(runner.RunEvent{
			Kind: runner.EventKindError,
			Err: &runner.ErrorEvent{
				Message: rawStderr,
				Fatal:   true,
			},
		})
		fr.Cancel()
	}()

	res, err := runExternalCLISubTurn(context.Background(), al, ts, "task", 30*time.Second)
	require.Error(t, err, "fatal error must surface as a run failure")
	require.NotNil(t, res)

	// ForLLM reaches the parent agent's context window — raw provider text
	// must NEVER appear here. Only the sanitized generic copy is safe.
	assert.NotContains(t, res.ForLLM, rawStderr,
		"ForLLM must NOT contain provider-shaped raw stderr (B2 LogText-leak regression)")
	assert.NotContains(t, res.ForLLM, "JPG",
		"ForLLM must not carry any snippet of the raw provider body")
	assert.NotContains(t, res.Err.Error(), rawStderr,
		"returned error string must NOT carry raw provider stderr")

	// Sanity: ForLLM is non-empty and carries the generic "external-cli run
	// failed" marker so the parent agent knows the run failed.
	assert.Contains(t, res.ForLLM, "External CLI run",
		"ForLLM must carry the generic failure marker")
}

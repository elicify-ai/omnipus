package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	"github.com/elicify-ai/omnipus/pkg/agent/runner/runnertest"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// The process-edge fake signals the real dispatch's Run call. No stopwatch or
// mock of create_agent, delegate, Launch/Dispatch or their admission gate.
type l11274NotifyingRunner struct {
	*runnertest.FakeRunner
	runs chan runner.RunOptions
}

func (r *l11274NotifyingRunner) Run(ctx context.Context, opts runner.RunOptions) (<-chan runner.RunEvent, error) {
	stream, err := r.FakeRunner.Run(ctx, opts)
	r.runs <- opts
	return stream, err
}

func l11274DriverSeam(t *testing.T, f *l11274Fixture) (*l11274NotifyingRunner, chan string) {
	t.Helper()
	fake := &l11274NotifyingRunner{FakeRunner: runnertest.NewFakeRunner(), runs: make(chan runner.RunOptions, 2)}
	clis := make(chan string, 2)
	previous := newExternalDriver
	newExternalDriver = func(cli string, _ runner.ConsentHandler) (runner.ExternalAgentRunner, error) {
		clis <- cli
		return fake, nil
	}
	t.Cleanup(func() {
		fake.Cancel()
		// Join the real dispatch before restoring a package-wide factory or
		// permitting t.TempDir cleanup. A recorded option alone is not a join.
		f.al.drainSteeredTurns(30 * time.Second)
		newExternalDriver = previous
	})
	return fake, clis
}

// An ordinary workspace trust edge is explicit test input. Omnipus's own
// helpers remain self-only; Jim is the shipped delegation-capable colleague.
func l11274DelegatingContext(t *testing.T, f *l11274Fixture, workerID string, allow bool) context.Context {
	t.Helper()
	state, err := workspace.ReadState(f.home, l11274WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	state.Workspace.CoreTeam = append(state.Workspace.CoreTeam, "jim")
	data, err := json.Marshal(state.Workspace)
	if err != nil {
		t.Fatal(err)
	}
	if writeErr := os.WriteFile(filepath.Join(f.home, "workspaces", l11274WorkspaceID+".json"), data, 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}
	if allow {
		state.Delegation = append(state.Delegation, workspace.DelegationEdge{FromAgent: "jim", ToAgent: workerID})
		if saveErr := workspace.SaveDelegation(f.home, l11274WorkspaceID, state.Delegation); saveErr != nil {
			t.Fatal(saveErr)
		}
	}
	main, err := f.al.GetSessionStore().GetOrCreateMainSession(l11274WorkspaceID, "jim")
	if err != nil {
		t.Fatal(err)
	}
	ctx := tools.WithWorkspaceID(context.Background(), l11274WorkspaceID)
	ctx = tools.WithTranscriptSessionID(ctx, main.ID)
	ctx = tools.WithSessionKey(ctx, main.ID)
	ctx = tools.WithSessionOwner(ctx, "l11274-tester")
	return tools.WithToolCallID(ctx, "1274-delegate-"+workerID)
}

func l11274Delegate(t *testing.T, f *l11274Fixture, id, task string, allow bool) *tools.ToolResult {
	t.Helper()
	ctx := l11274DelegatingContext(t, f, id, allow)
	jim, ok := f.al.GetRegistry().GetAgent("jim")
	if !ok || jim == nil {
		t.Fatal("SETUP: seeded native delegator Jim is missing")
	}
	tool, ok := jim.Tools.Get("delegate")
	if !ok {
		t.Fatal("BLOCKED: delegate is not registered — required by #1274")
	}
	if policy := tools.EffectiveToolPolicy(jim.LoadToolPolicy(), tool.Scope(), jim.AgentType, "delegate"); policy != "allow" {
		t.Fatalf("SETUP: Jim delegate effective policy = %q, want allow", policy)
	}
	return jim.Tools.ExecuteWithContext(ctx, "delegate", map[string]any{"action": "run", "agent_id": id, "task": task}, "webchat", "1274-chat", nil)
}

func l11274AssertExternalDelegate(t *testing.T, f *l11274Fixture, id, cli, path string) {
	t.Helper()
	fake, clis := l11274DriverSeam(t, f)
	const task = "Produce the 1274 delegated worker result."
	result := l11274Delegate(t, f, id, task, true)
	if result == nil || result.IsError {
		t.Fatalf("new %s worker must be delegatable, result=%+v", cli, result)
	}
	var response generated.DelegateSessionResponse
	if err := json.Unmarshal([]byte(result.ForLLM), &response); err != nil {
		t.Fatal(err)
	}
	if response.SessionId == "" || !response.Is3p || response.State != "running" {
		t.Fatalf("delegate must admit a third-party worker session, got %s", result.ForLLM)
	}
	var opts runner.RunOptions
	select {
	case opts = <-fake.runs:
	case <-time.After(5 * time.Second):
		t.Fatal("real delegate never reached the external driver's Run boundary")
	}
	select {
	case gotCLI := <-clis:
		if gotCLI != cli {
			t.Errorf("selected driver = %q, want the saved CLI %q", gotCLI, cli)
		}
	default:
		t.Error("Run was reached without selecting a CLI driver")
	}
	if opts.CLIPath != path || opts.Model != l11274Model || opts.WorkDir != filepath.Join(f.home, "workspaces", l11274WorkspaceID, "work") {
		t.Errorf("driver options lost saved runtime/model/workspace: got path=%q model=%q dir=%q; want path=%q model=%q dir=%q",
			opts.CLIPath, opts.Model, opts.WorkDir, path, l11274Model, filepath.Join(f.home, "workspaces", l11274WorkspaceID, "work"))
	}
	// Context framing is deliberately not frozen; the exact supplied task must
	// be present in the instructions delivered across the real process boundary.
	if !strings.Contains(opts.Input, task) {
		t.Errorf("driver input %q lost the delegated task %q", opts.Input, task)
	}
	rec, err := f.al.GetSessionLifecycleStore().Load(response.SessionId)
	if err != nil || rec == nil || rec.AgentID != id || !rec.Is3P {
		t.Fatalf("real lifecycle record did not stamp the selected worker: record=%+v err=%v", rec, err)
	}
	if err := fake.InjectEvent(runner.RunEvent{Kind: runner.EventKindOutput, Output: &runner.OutputEvent{Text: "1274 external result"}}); err != nil {
		t.Fatal(err)
	}
	if err := fake.InjectEvent(runner.RunEvent{Kind: runner.EventKindEnd}); err != nil {
		t.Fatal(err)
	}
	fake.Cancel()
	f.al.drainSteeredTurns(30 * time.Second)
	if got := f.provider.calls.Load(); got != 0 {
		t.Errorf("external delegation called the native provider %d times, want 0", got)
	}
	if got := fake.RecordedRunOpts(); len(got) != 1 || !reflect.DeepEqual(got[0], opts) {
		// RunOptions.Env can contain process environment values: never print it.
		t.Errorf("external Run call count/options differ from the one signalled dispatch (calls=%d)", len(got))
	}
	select {
	case extra := <-clis:
		t.Errorf("external delegation selected an extra driver %q; want exactly one selection", extra)
	default:
	}
}

// Oracle: #1274 R4. A failed create is a failed end-to-end journey; this test
// never manually inserts a worker to paper over creation failure.
func TestL11274CreatedExternalWorkerCanBeDelegatedToEachDriver(t *testing.T) {
	for _, cli := range []string{"claude-code", "codex", "opencode"} {
		t.Run(cli, func(t *testing.T) {
			f := newL11274Fixture(t, "", false)
			path := l11274Executable(t, f.home, cli, true)
			result := f.execute(t, "create_agent", l11274CreateArgs("Created "+cli, cli, path))
			id := l11274AssertCreated(t, f, result, cli, path, "#6E8BA8")
			l11274AssertExternalDelegate(t, f, id, cli, path)
		})
	}
}

// Instrument controls: these start with an explicitly persisted worker fixture,
// NOT one falsely reported as created. They prove that a broken driver path
// would be detected independently of the unconditional-soul creation blocker.
func TestL11274ExistingExternalWorkerDriverControls(t *testing.T) {
	for _, cli := range []string{"claude-code", "codex", "opencode"} {
		t.Run(cli, func(t *testing.T) {
			f := newL11274Fixture(t, "", false)
			path := l11274Executable(t, f.home, cli, true)
			id := "existing-" + cli
			l11274SeedWorker(t, f, id, &config.ExecutorConfig{Kind: config.ExecutorKindExternalCLI, CLI: cli, CLIPath: path})
			l11274AssertExternalDelegate(t, f, id, cli, path)
		})
	}
}

func l11274SeedWorker(t *testing.T, f *l11274Fixture, id string, executor *config.ExecutorConfig) {
	t.Helper()
	worker := config.AgentConfig{ID: id, Name: id, Description: "Independently seeded driver control",
		Type: config.AgentTypeWorker, Color: "#6E8BA8", Model: &config.AgentModelConfig{Primary: l11274Model},
		Tools: coreagent.NewCustomAgentToolsCfg(), Subagents: &config.SubagentsConfig{Executor: executor}}
	coreagent.RestrictForWorkerTier(worker.Tools)
	if executor.Kind == config.ExecutorKindNative {
		// This independent native control must be able to report its work.
		// An explicit operator grant is fixture input, not a claimed create
		// default. External workers report through the driver stream instead.
		worker.Tools.Builtin.Policies["message_parent"] = config.ToolPolicyAllow
	}
	if _, err := agentstore.New(f.home).CreateState(id, &worker, ""); err != nil {
		t.Fatal(err)
	}
	state, err := workspace.ReadState(f.home, l11274WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	state.Workspace.CoreTeam = append(state.Workspace.CoreTeam, id)
	data, err := json.Marshal(state.Workspace)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.home, "workspaces", l11274WorkspaceID+".json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := l11274Publish(f.al, f.home, id); err != nil {
		t.Fatalf("SETUP: publish independent driver-control worker: %v", err)
	}
}

func TestL11274DelegateWithoutTrustEdgeRefusesBeforeDriver(t *testing.T) {
	f := newL11274Fixture(t, "", false)
	path := l11274Executable(t, f.home, "codex", true)
	l11274SeedWorker(t, f, "untrusted-worker", &config.ExecutorConfig{Kind: config.ExecutorKindExternalCLI, CLI: "codex", CLIPath: path})
	fake, clis := l11274DriverSeam(t, f)
	result := l11274Delegate(t, f, "untrusted-worker", "must not execute", false)
	if !result.IsError || !strings.Contains(result.ForLLM, "untrusted-worker") || !strings.Contains(result.ForLLM, "not permitted") {
		t.Errorf("missing-edge refusal must be visible and identify the worker/reason: %+v", result)
	}
	if got := len(fake.RecordedRunOpts()); got != 0 {
		t.Errorf("refused delegate reached driver Run %d times, want 0", got)
	}
	select {
	case cli := <-clis:
		t.Errorf("refused delegate selected driver %q", cli)
	default:
	}
}

func TestL11274NativeDelegateNeverUsesExternalDriver(t *testing.T) {
	f := newL11274Fixture(t, "", false)
	l11274SeedWorker(t, f, "native-worker", &config.ExecutorConfig{Kind: config.ExecutorKindNative})
	fake, clis := l11274DriverSeam(t, f)
	result := l11274Delegate(t, f, "native-worker", "native control task", true)
	if result.IsError {
		t.Fatalf("native delegate control refused: %s", result.ForLLM)
	}
	f.al.drainSteeredTurns(30 * time.Second)
	if got := f.provider.calls.Load(); got != 1 {
		t.Errorf("native control must make exactly one provider call, got %d", got)
	}
	if got := len(fake.RecordedRunOpts()); got != 0 {
		t.Errorf("native delegation reached external Run %d times, want 0", got)
	}
	select {
	case cli := <-clis:
		t.Errorf("native delegate selected external driver %q", cli)
	default:
	}
}

package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

const p3ProofFile = "p3-proof.txt"
const p3ProofText = "p3-verifier-proof-marker"

type p3DispatchObservation struct {
	sessionID string
	meta      *session.UnifiedMeta
	err       error
}

type p3VerifierFixture struct {
	al           *AgentLoop
	judge        *AgentInstance
	provider     *fakeJudgeProvider
	readFile     *p3ReadFileObserver
	registry     *p3RegistryObserver
	unitID       string
	workDir      string
	factoryCalls atomic.Int32
	mu           sync.Mutex
	runners      []*runner.FakeRunner
	dispatches   []p3DispatchObservation
}

func newP3VerifierFixture(t *testing.T, external bool, criterionID string) *p3VerifierFixture {
	t.Helper()
	f := &p3VerifierFixture{registry: p3ObserveRegistry(t), provider: &fakeJudgeProvider{}}
	f.al, f.judge = newGoalLoopTestLoop(t, f.provider, func(cfg *config.Config) {
		cfg.Sandbox.ToolPolicies["read_file"] = "allow"
		if external {
			for i := range cfg.Agents.List {
				if cfg.Agents.List[i].ID == string(coreagent.IDJudge) {
					cfg.Agents.List[i].Subagents = &config.SubagentsConfig{Executor: &config.ExecutorConfig{
						Kind: config.ExecutorKindExternalCLI, CLI: "claude-code",
					}}
				}
			}
		}
	})
	f.judge.Provider = f.provider
	f.judge.MaxIterations = extTestEffectiveLimit
	allowReadFilePolicy(f.judge)
	registered, ok := f.al.GetRegistry().GetAgent(string(coreagent.IDJudge))
	if !ok || registered != f.judge || registered.Provider == nil || f.al.GetAgentStore(f.judge.ID) == nil {
		t.Fatal("P3 premise: a configured, resolvable Judge/provider/real UnifiedStore is required")
	}

	tool, ok := f.judge.Tools.Get("read_file")
	if !ok {
		t.Fatal("P3 premise: real read_file tool is not registered")
	}
	realReadFile, ok := tool.(*tools.ReadFileTool)
	if !ok {
		t.Fatalf("P3 premise: read_file is %T, want the real *tools.ReadFileTool", tool)
	}
	f.readFile = &p3ReadFileObserver{ReadFileTool: realReadFile}
	f.judge.Tools.RegisterReplacing(f.readFile)
	var err error
	f.workDir, err = resolveTurnWorkDirOrRefuse(context.Background(), f.judge.ID, f.judge.Home, "")
	if err != nil {
		t.Fatalf("P3 premise: Judge workspace resolution: %v", err)
	}
	if err := os.WriteFile(filepath.Join(f.workDir, p3ProofFile), []byte(p3ProofText), 0o600); err != nil {
		t.Fatalf("P3 premise: write tool proof artifact: %v", err)
	}

	verdict := fmt.Sprintf(`{"met":true,"criteria":[{"id":%q,"met":true,"reason":"read proof artifact","evidence_quote":%q}]}`, criterionID, p3ProofText)
	f.provider.chatFn = func(call int) (*providers.LLMResponse, error) {
		f.observeDispatch()
		if call%2 == 1 {
			return &providers.LLMResponse{ToolCalls: []providers.ToolCall{{
				ID: "p3-read-proof", Name: "read_file", Arguments: map[string]any{"path": p3ProofFile},
			}}}, nil
		}
		return &providers.LLMResponse{Content: verdict}, nil
	}

	previousFactory := newExternalDriver
	t.Cleanup(func() { newExternalDriver = previousFactory })
	newExternalDriver = func(_ string, _ runner.ConsentHandler) (runner.ExternalAgentRunner, error) {
		f.factoryCalls.Add(1)
		f.observeDispatch()
		fr := runner.NewFakeRunner()
		fr.InjectEvent(runner.RunEvent{Kind: runner.EventKindOutput, Output: &runner.OutputEvent{Text: verdict}})
		fr.InjectEvent(runner.RunEvent{Kind: runner.EventKindEnd})
		fr.Cancel() // Close after the buffered terminal events; no real CLI or goroutine.
		f.mu.Lock()
		f.runners = append(f.runners, fr)
		f.mu.Unlock()
		return fr, nil
	}
	return f
}

// Called at the network/process edge, before returning any canned output.
// A healthy control must see real verifier metadata already persisted here.
func (f *p3VerifierFixture) observeDispatch() {
	id, exists := f.registry.Lookup(f.unitID)
	observation := p3DispatchObservation{sessionID: id}
	if !exists || id == "" {
		observation.err = errors.New("no real verifier session registered before dispatch")
	} else {
		observation.meta, observation.err = f.al.GetAgentStore(f.judge.ID).GetMeta(id)
	}
	f.mu.Lock()
	f.dispatches = append(f.dispatches, observation)
	f.mu.Unlock()
}

func (f *p3VerifierFixture) runOptions() []runner.RunOptions {
	f.mu.Lock()
	defer f.mu.Unlock()
	var options []runner.RunOptions
	for _, fr := range f.runners {
		options = append(options, fr.RecordedRunOpts()...)
	}
	return options
}

// Use the actual Judge's owned store, not a fake constructor or an absent Judge.
// A regular file in place of the directory fails even when tests run as root.
func p3FaultVerifierCreation(t *testing.T, al *AgentLoop) *os.PathError {
	t.Helper()
	store := al.GetAgentStore(string(coreagent.IDJudge))
	if store == nil {
		t.Fatal("P3 premise: real Judge UnifiedStore missing")
	}
	meta, err := store.NewVerifierSession(string(coreagent.IDJudge))
	if err != nil || meta == nil || meta.ID == "" || meta.Type != session.SessionTypeVerifier {
		t.Fatalf("P3 premise: healthy real verifier creation failed: meta=%+v err=%v", meta, err)
	}
	base := store.BaseDir()
	backup := filepath.Join(t.TempDir(), "healthy-verifier-store")
	if err := os.Rename(base, backup); err != nil {
		t.Fatalf("P3 premise: isolate owned verifier directory: %v", err)
	}
	// Registered after the loop's cleanup so LIFO restores before store Close.
	t.Cleanup(func() {
		if err := os.Remove(base); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Errorf("P3 cleanup: remove directory blocker: %v", err)
		}
		if err := os.Rename(backup, base); err != nil {
			t.Errorf("P3 cleanup: restore real verifier store: %v", err)
		}
	})
	if err := os.WriteFile(base, []byte("not a session directory"), 0o600); err != nil {
		t.Fatalf("P3 premise: install directory-as-file fault: %v", err)
	}
	failedMeta, creationErr := store.NewVerifierSession(string(coreagent.IDJudge))
	var cause *os.PathError
	if failedMeta != nil || creationErr == nil || !errors.As(creationErr, &cause) {
		t.Fatalf("P3 fault not established: meta=%+v error=%v; want nil metadata and a real PathError", failedMeta, creationErr)
	}
	if cause.Path != base {
		t.Fatalf("P3 fault path = %q, want the blocked real store %q", cause.Path, base)
	}
	t.Logf("P3 real storage fault: configured Judge=%s op=%s path=%s cause=%v", coreagent.IDJudge, cause.Op, cause.Path, cause.Err)
	return cause
}

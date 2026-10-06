package agent

// D1's fixture substitutes only external model providers. Session/goal stores,
// the registered delegate and goal_claim tools, boot mint, admission, keeper,
// bus intake, wake reconstruction, Judge and upward completion all stay real.
// No production test hook or manufactured running/execution state is used.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// These strings are authored scenario inputs, not observed implementation output.
const (
	uatD1Task            = "Explain DNS in one sentence."
	uatD1Criterion       = "The answer explains what DNS does."
	uatD1DoD             = "The answer is one sentence without unrelated material."
	uatD1Answer          = "DNS translates human-readable domain names into IP addresses."
	uatD1ProviderFailure = "D1 permanent provider rejection"
	// ADR-084 D13 specifies a 60-second quiet window. The watchdog below
	// bounds a missing asynchronous event; it is not a product latency oracle.
	uatD1QuietWindow = time.Minute
	uatD1Watchdog    = 10 * time.Second
)

type uatD1WorkerProvider struct {
	mu              sync.Mutex
	requests        [][]providers.Message
	claimOnFirst    bool
	failOnReminder  bool
	firstEntered    chan struct{}
	reminderEntered chan struct{}
	firstRelease    chan struct{}
	releaseOnce     sync.Once
}

func (p *uatD1WorkerProvider) Chat(ctx context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	p.mu.Lock()
	index := len(p.requests)
	p.requests = append(p.requests, append([]providers.Message(nil), messages...))
	p.mu.Unlock()
	if index == 0 {
		close(p.firstEntered)
		select {
		case <-p.firstRelease:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		if p.claimOnFirst {
			return uatD1ClaimResponse(), nil
		}
		return &providers.LLMResponse{Content: uatD1Answer, FinishReason: "stop"}, nil
	}
	if index == 1 && !p.claimOnFirst {
		close(p.reminderEntered)
		if p.failOnReminder {
			return nil, errors.New(uatD1ProviderFailure)
		}
		return uatD1ClaimResponse(), nil
	}
	if (index == 1 && p.claimOnFirst) || (index == 2 && !p.failOnReminder) {
		return &providers.LLMResponse{Content: uatD1Answer, FinishReason: "stop"}, nil
	}
	return nil, fmt.Errorf("D1 unexpected worker provider request %d", index+1)
}

func uatD1ClaimResponse() *providers.LLMResponse {
	return &providers.LLMResponse{FinishReason: "tool_calls", ToolCalls: []providers.ToolCall{{
		ID: "uat-d1-claim", Name: "goal_claim", Type: "function",
		Arguments: map[string]any{"status": "met", "evidence": uatD1Answer},
	}}}
}

func (*uatD1WorkerProvider) GetDefaultModel() string { return "uat-d1-worker" }
func (p *uatD1WorkerProvider) openFirst() {
	p.releaseOnce.Do(func() { close(p.firstRelease) })
}
func (p *uatD1WorkerProvider) Requests() [][]providers.Message {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([][]providers.Message(nil), p.requests...)
}

type uatD1ParentProvider struct {
	mu       sync.Mutex
	requests [][]providers.Message
	entered  chan struct{}
	once     sync.Once
}

func (p *uatD1ParentProvider) Chat(_ context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	p.mu.Lock()
	p.requests = append(p.requests, append([]providers.Message(nil), messages...))
	p.mu.Unlock()
	p.once.Do(func() { close(p.entered) })
	return &providers.LLMResponse{Content: "Helper outcome received.", FinishReason: "stop"}, nil
}
func (*uatD1ParentProvider) GetDefaultModel() string { return "uat-d1-parent" }
func (p *uatD1ParentProvider) Requests() [][]providers.Message {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([][]providers.Message(nil), p.requests...)
}

type uatD1Fixture struct {
	al       *AgentLoop
	worker   *uatD1WorkerProvider
	parent   *uatD1ParentProvider
	judge    *b6ScriptedJudge
	parentID string
	home     string
	delegate *tools.DelegateTool
}

func newUATD1Fixture(t *testing.T, claimOnFirst, failOnReminder bool) *uatD1Fixture {
	t.Helper()
	worker := &uatD1WorkerProvider{
		claimOnFirst: claimOnFirst, failOnReminder: failOnReminder,
		firstEntered: make(chan struct{}), reminderEntered: make(chan struct{}), firstRelease: make(chan struct{}),
	}
	parent := &uatD1ParentProvider{entered: make(chan struct{})}
	al, judgeInst := newGoalLoopTestLoop(t, parent, func(cfg *config.Config) {
		cfg.Agents.Defaults.MaxTokens = 4096
		cfg.Agents.Defaults.MaxToolIterations = 6
		cfg.Performance.MaxParallelAgents = 2
		cfg.Sandbox.ToolPolicies = config.DefaultConfig().Sandbox.ToolPolicies
		cfg.Sandbox.ToolPolicies["delegate"] = "allow"
		cfg.Sandbox.ToolPolicies["goal_claim"] = "allow"
		cfg.Agents.List = append(cfg.Agents.List, config.AgentConfig{
			ID: "worker", Name: "General Purpose", Type: config.AgentTypeWorker, Home: t.TempDir(),
		})
	})
	home := config.OmnipusHomeDir()
	al.SetSessionMessagingStores(session.NewMessageInboxStore(filepath.Join(home, "session_messages")), session.NewLifecycleStore(filepath.Join(home, "session_lifecycle")))
	wireSteerCompletionDeps(t, al)
	al.SetSteerSessionLauncher(NewSteerLauncher(al)) // same real dependency injection as gateway boot
	boot := session.NewBootEpochStore(home)
	if epoch, err := boot.Mint(); err != nil || epoch == 0 {
		t.Fatalf("SETUP real boot mint: epoch=%d error=%v", epoch, err)
	}
	al.SetBootEpochStore(boot)
	workerInst, ok := al.GetRegistry().GetAgent("worker")
	if !ok {
		t.Fatal("SETUP registered worker missing")
	}
	workerInst.Provider = worker
	judge := &b6ScriptedJudge{metFromCall: 1}
	judgeInst.Provider = judge
	uatD1WriteWorkspace(t, home)
	meta, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "native-agent")
	if err != nil {
		t.Fatalf("SETUP real parent session: %v", err)
	}
	wsID, owner := "uat-d1", "uat-d1-tester"
	if err := al.GetSessionStore().SetMeta(meta.ID, session.MetaPatch{WorkspaceID: &wsID, Owner: &owner}); err != nil {
		t.Fatalf("SETUP parent workspace: %v", err)
	}
	parentInst, ok := al.GetRegistry().GetAgent("native-agent")
	if !ok {
		t.Fatal("SETUP registered parent missing")
	}
	registered, ok := parentInst.Tools.Get("delegate")
	if !ok {
		t.Fatal("BLOCKED: registered delegate tool missing — required by ADR-091 D1")
	}
	delegate, ok := registered.(*tools.DelegateTool)
	if !ok {
		t.Fatalf("SETUP delegate type=%T, want real *tools.DelegateTool", registered)
	}
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- al.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-runDone:
			if err != nil {
				t.Errorf("real bus intake exited with error: %v", err)
			}
		case <-time.After(uatD1Watchdog):
			t.Error("real bus intake did not join on cancellation")
		}
	})
	t.Cleanup(worker.openFirst)
	return &uatD1Fixture{al: al, worker: worker, parent: parent, judge: judge, parentID: meta.ID, home: home, delegate: delegate}
}

func uatD1WriteWorkspace(t *testing.T, home string) {
	t.Helper()
	// Only static operator configuration is seeded. Authorization reads the
	// real protected delegation store; no deny checker is replaced.
	ws := workspace.Workspace{ID: "uat-d1", Name: "D1 workspace", Status: "active", CoreTeam: []string{"native-agent", "worker"}}
	data, err := json.Marshal(ws)
	if err != nil {
		t.Fatalf("SETUP workspace JSON: %v", err)
	}
	if mkdirErr := os.MkdirAll(filepath.Join(home, "workspaces", ws.ID, "work"), 0o700); mkdirErr != nil {
		t.Fatalf("SETUP workspace directory: %v", mkdirErr)
	}
	if writeErr := os.WriteFile(filepath.Join(home, "workspaces", ws.ID+".json"), data, 0o600); writeErr != nil {
		t.Fatalf("SETUP workspace record: %v", writeErr)
	}
	unlock := workspace.LockID(ws.ID)
	err = workspace.SaveDelegation(home, ws.ID, []workspace.DelegationEdge{{FromAgent: "native-agent", ToAgent: "worker", Modes: []workspace.DelegationMode{workspace.ModeDirect}}})
	unlock()
	if err != nil {
		t.Fatalf("SETUP real delegation store: %v", err)
	}
}

func (f *uatD1Fixture) launch(t *testing.T) *session.LifecycleRecord {
	t.Helper()
	ctx := tools.WithTranscriptSessionID(context.Background(), f.parentID)
	ctx = tools.WithAgentID(ctx, "native-agent")
	ctx = tools.WithWorkspaceID(ctx, "uat-d1")
	ctx = tools.WithToolCallID(ctx, "uat-d1-delegate")
	result := f.delegate.Execute(ctx, map[string]any{
		"action": "run", "agent_id": "worker", "task": uatD1Task,
		"criteria": []any{map[string]any{"text": uatD1Criterion, "kind": "prose", "judgment": "boolean"}},
		"dod":      []any{map[string]any{"text": uatD1DoD, "kind": "prose", "judgment": "boolean"}},
	})
	if result == nil || result.IsError {
		t.Fatalf("SETUP real delegate(run) failed: %+v", result)
	}
	var response generated.DelegateSessionResponse
	if err := json.Unmarshal([]byte(result.ForLLM), &response); err != nil || string(response.State) != "running" {
		t.Fatalf("SETUP real delegate response=%q error=%v, want admitted running child", result.ForLLM, err)
	}
	select {
	case <-f.worker.firstEntered:
	case <-time.After(uatD1Watchdog):
		t.Fatal("SETUP real child admission never reached its provider")
	}
	rec := uatD1ReadLifecycle(t, f.al, response.SessionId)
	if rec.ExecutionID == nil || rec.ExecutionID.RunID == "" || rec.ExecutionID.BootSeq != f.al.bootEpochFor() || rec.GoalRef == "" || rec.SteeringSessionID() != f.parentID {
		t.Fatalf("SETUP real launch/admission missing child goal, edge or boot execution identity: %+v", rec)
	}
	return rec
}

func uatD1ReadLifecycle(t *testing.T, al *AgentLoop, id string) *session.LifecycleRecord {
	t.Helper()
	rec, err := al.GetSessionLifecycleStore().Load(id)
	if err != nil {
		t.Fatalf("read real lifecycle %q: %v", id, err)
	}
	return rec
}

func uatD1ReadGoal(t *testing.T, id string) *goal.Goal {
	t.Helper()
	g, err := resolveGoalRecordStore().Get(id)
	if err != nil {
		t.Fatalf("read real goal %q: %v", id, err)
	}
	return g
}

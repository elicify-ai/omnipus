package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/require"
)

// Oracles are literal amendment §1.2 R4 text, not production constants.
const (
	l11263Reason     = "auto-denied: ask-policy tool not allowed in a task or plan run"
	l11263Model      = "Task and plan runs never ask a person for approval. This call needed approval and was refused. Do not retry; finish what you can and report what was blocked."
	l11263Transcript = "Not run: needs approval, and task and plan runs never ask."
	l11263Agent      = "approval-worker"
	l11263Middle     = "approval-middle"
	l11263Leaf       = "approval-leaf"
	l11263File       = "approval-probe.txt"
	l11263Content    = "1263-B: real file tool executed\n"
)

// The only fake is the paid model-provider edge. It requests real tools and
// records the model-visible results. Policy, Auto, dispatch, storage and
// approvals are never replaced. A step may await a child's observed result to
// prevent the parent finishing and stopping it before the probe has run.
type l11263ModelStep struct {
	call providers.ToolCall
	wait <-chan struct{}
}

type l11263Provider struct {
	mu       sync.Mutex
	steps    []l11263ModelStep
	next     int
	results  map[string]string
	observed chan struct{}
	once     sync.Once
}

func l11263Script(calls ...providers.ToolCall) *l11263Provider {
	p := &l11263Provider{results: make(map[string]string), observed: make(chan struct{})}
	for _, call := range calls {
		p.steps = append(p.steps, l11263ModelStep{call: call})
	}
	return p
}

func (p *l11263Provider) Chat(ctx context.Context, msgs []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	p.mu.Lock()
	for _, msg := range msgs {
		if msg.Role == "tool" {
			p.results[msg.ToolCallID] = msg.Content
			if msg.ToolCallID == "approval-write" || msg.ToolCallID == "approval-shell" {
				p.once.Do(func() { close(p.observed) })
			}
		}
	}
	if p.next == len(p.steps) {
		p.mu.Unlock()
		return &providers.LLMResponse{Content: "Approval probe finished."}, nil
	}
	step := p.steps[p.next]
	p.next++
	p.mu.Unlock()
	if step.wait != nil {
		select {
		case <-step.wait:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return &providers.LLMResponse{ToolCalls: []providers.ToolCall{step.call}}, nil
}

func (p *l11263Provider) GetDefaultModel() string { return "test-model" }
func (p *l11263Provider) result(id string) (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	value, ok := p.results[id]
	return value, ok
}

func l11263Call(id, tool string, args map[string]any) providers.ToolCall {
	return providers.ToolCall{ID: id, Type: "function", Name: tool, Arguments: args}
}
func l11263Write() providers.ToolCall {
	return l11263Call("approval-write", "write_file", map[string]any{"path": l11263File, "content": l11263Content})
}
func l11263Claim() providers.ToolCall {
	// Deliberately terminate via the existing blocked pathway (R4/Q2 A).
	// Execution controls assert the file, not a made-up successful Judge verdict.
	return l11263Call("approval-claim", "goal_claim", map[string]any{"status": "blocked", "evidence": "write_file approval probe finished"})
}
func l11263Delegate(target string) providers.ToolCall {
	return l11263Call("delegate-"+target, "delegate", map[string]any{
		"agent_id": target, "task": "Run the approval probe", "critical": true,
	})
}

type l11263Harness struct {
	api     *restAPI
	reg     *approvalRegistryV2
	ws      *WSHandler
	tab     *wsConn
	pe      *agent.PlanEngine
	workDir string
}

func newL11263Harness(t *testing.T, auto bool, policy config.ToolPolicy, scripts map[string]*l11263Provider, configure ...func(*config.Config)) *l11263Harness {
	t.Helper()
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	cfg := &config.Config{}
	cfg.Agents.Defaults.Home = filepath.Join(home, "workspace")
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "test-model"}
	cfg.Agents.Defaults.MaxTokens = 4096
	cfg.Agents.Defaults.MaxToolIterations = 10
	cfg.Sandbox.AutoApprove = auto
	cfg.Sandbox.AuditLog = true
	cfg.Sandbox.ToolPolicies = config.DefaultConfig().Sandbox.ToolPolicies
	for name, value := range map[string]string{
		"write_file": "ask", "bash": "ask", "goal_claim": "allow",
		"delegate": "allow", "message_parent": "allow", "run_task": "allow", "execute_plan": "allow", "send_message": "allow",
	} {
		cfg.Sandbox.ToolPolicies[name] = value
	}
	for _, apply := range configure {
		apply(cfg)
	}
	for _, agentCfg := range []struct{ id, name string }{
		{"mia", "Mia"}, {l11263Agent, "Approval Worker"}, {l11263Middle, "Approval Middle"}, {l11263Leaf, "Approval Leaf"},
	} {
		cfg.Agents.List = append(cfg.Agents.List, config.AgentConfig{
			ID: agentCfg.id, Name: agentCfg.name, Type: config.AgentTypeCustom, Home: filepath.Join(home, "agents", agentCfg.id),
			Tools: &config.AgentToolsCfg{Builtin: config.AgentBuiltinToolsCfg{
				Policies: map[string]config.ToolPolicy{"write_file": policy},
			}},
		})
	}
	require.NoError(t, os.MkdirAll(cfg.Agents.Defaults.Home, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(home, "config.json"), []byte(`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[]}`), 0o600))
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	al := mustAgentLoop(t, cfg, msgBus, l11263Script())
	for id, script := range scripts {
		inst, ok := al.GetRegistry().GetAgent(id)
		require.True(t, ok, "fixture agent %s must exist", id)
		inst.Provider = script
	}
	// The common REST fixture installs the launcher on TaskExecutor only.
	// Production also installs it on delegate tools (gateway_boot.go).
	al.SetSteerSessionLauncher(agent.NewSteerLauncher(al))
	classifier := agent.NewSteerRecordClassifier(al.GetSessionLifecycleStore(), al.GetSessionStore())
	al.SetSteerAudienceDeps(agent.NewSteerAudienceResolver(classifier), steer.NopBoundaryObserver{}, agent.NewSteerUpwardDeliverer())
	ps := plan.New(filepath.Join(home, "plans"))
	al.SetPlanStore(ps)
	te := agent.GetTaskExecutor(al)
	te.SetPlanStore(ps)
	api := &restAPI{agentLoop: al, homePath: home, taskStore: agent.GetTaskStore(al), taskLock: task.TaskFileLock, taskExecutor: te, planStore: ps}
	pe := agent.NewPlanEngine(al, ps, api.taskStore, te)
	al.SetPlanEngine(pe)
	reg := newApprovalRegistryV2(64, 10*time.Minute)
	ws := newWSHandler(msgBus, al, "http://localhost:3000")
	ws.approvalRegV2 = reg
	al.SetToolApprover(newPolicyApproverAdapter(reg, ws))
	// Production wires the registry's resolution listener to the WS handler
	// (gateway_boot.go: setResolutionListener(broadcastToolApprovalResolved));
	// without it no tool_approval_resolved frame ever reaches the tab.
	reg.setResolutionListener(ws.broadcastToolApprovalResolved)
	tab := apprResAttachConns(t, ws, 1)[0]
	tab.sendCh = make(chan []byte, 256)
	workDir, err := workspace.EnsureWorkDir(home, testHarnessWorkspaceMembershipID)
	require.NoError(t, err)
	h := &l11263Harness{api: api, reg: reg, ws: ws, tab: tab, pe: pe, workDir: workDir}
	t.Cleanup(func() {
		// Cancellation is cleanup AFTER the assertion, never a verdict source.
		for _, entry := range reg.pendingApprovals() {
			reg.resolve(entry.ApprovalID, ApprovalActionCancel, false)
		}
		pe.Stop()
		te.Drain(10 * time.Second)
	})
	return h
}

func (h *l11263Harness) createTask(t *testing.T, planID string) gen.Task {
	t.Helper()
	body := fmt.Sprintf(`{"title":"Approval probe","action":"llm","agent_id":%q,"workspace_id":%q,"criteria":%s,"dod":%s`,
		l11263Agent, testHarnessWorkspaceMembershipID, validCriteriaJSON, validDoDJSON)
	if planID != "" {
		body += fmt.Sprintf(`,"plan_id":%q`, planID)
	}
	w := postTask(t, h.api, body+"}")
	require.Equal(t, http.StatusCreated, w.Code, "real task creation: %s", w.Body.String())
	var created gen.Task
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))
	advanceTaskToNext(t, h.api, created.Id)
	return created
}

func (h *l11263Harness) startBoard(t *testing.T, created gen.Task) {
	t.Helper()
	w := patchTask(t, h.api, created.Id, `{"status":"in_progress"}`)
	require.Equal(t, http.StatusOK, w.Code, "real Board start: %s", w.Body.String())
}

func (h *l11263Harness) startPlan(t *testing.T) gen.Task {
	t.Helper()
	body := fmt.Sprintf(`{"workspace_id":%q,"title":"Approval plan","owner_agent_id":"mia","dod":%s}`, testHarnessWorkspaceMembershipID, validDoDJSON)
	w := postPlan(t, h.api, testHarnessWorkspaceMembershipID, body)
	require.Equal(t, http.StatusCreated, w.Code, "real plan creation: %s", w.Body.String())
	var created gen.Plan
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))
	member := h.createTask(t, created.Id)
	inst, ok := h.api.agentLoop.GetRegistry().GetAgent("mia")
	require.True(t, ok)
	ctx := toolsContextL11263(testHarnessWorkspaceMembershipID)
	res := inst.Tools.Execute(ctx, "execute_plan", map[string]any{"plan_id": created.Id})
	require.NotNil(t, res)
	require.False(t, res.IsError, "real execute_plan refused: %s", res.ForLLM)
	// Drive exactly one production engine tick, not a fake dispatcher. No
	// automatic timer/judging loop is needed to test the member's own tool call.
	h.pe.Tick(context.Background())
	return member
}

func (h *l11263Harness) forbidPrompt(t *testing.T) {
	t.Helper()
	if pending := h.reg.pendingApprovals(); len(pending) != 0 {
		e := pending[0]
		t.Fatalf("R1/R5 violated: approval prompt raised and Ask left pending in task/plan chain: tool=%s agent=%s session=%s approval=%s; expected immediate refusal, never a prompt", e.ToolName, e.AgentID, e.SessionID, e.ApprovalID)
	}
}

func (h *l11263Harness) awaitTask(t *testing.T, id string) *task.Task {
	t.Helper()
	deadline := time.NewTimer(15 * time.Second) // deadlock guard, never an absence oracle
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		h.forbidPrompt(t)
		current, err := h.api.taskStore.Get(id)
		require.NoError(t, err)
		if task.IsTerminal(current.Status) {
			h.api.taskExecutor.Drain(10 * time.Second)
			h.requireNoApprovals(t)
			return current
		}
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatalf("task did not settle; status=%s; no approval observed (inconclusive harness/dispatch failure, not no-prompt RED)", current.Status)
		}
	}
}

func (h *l11263Harness) requireNoApprovals(t *testing.T) {
	t.Helper()
	h.forbidPrompt(t)
	h.reg.mu.Lock()
	count := len(h.reg.entries)
	h.reg.mu.Unlock()
	require.Zero(t, count, "no approval may have been raised, even if it resolved before inspection")
	for {
		select {
		case raw := <-h.tab.sendCh:
			var frame map[string]any
			require.NoError(t, json.Unmarshal(raw, &frame))
			require.NotEqual(t, "tool_approval_required", frame["type"], "task/plan must never emit an approval frame: %s", raw)
		default:
			return
		}
	}
}

func (h *l11263Harness) requireTaskOrigin(t *testing.T, id string) {
	t.Helper()
	meta, err := h.api.agentLoop.GetSessionStore().GetMeta(id)
	require.NoError(t, err)
	require.NotEmpty(t, meta.TaskID, "actual task creator must stamp TaskID (R1 cross-check)")
	rec, err := h.api.agentLoop.GetSessionLifecycleStore().Load(id)
	require.NoError(t, err)
	require.NotNil(t, rec.Origin)
	require.Equal(t, session.OriginKindTask, rec.Origin.Kind, "TaskID must agree with unattended lifecycle origin")
}

func (h *l11263Harness) startCalendar(t *testing.T, id string) {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/"+id+"/runs", strings.NewReader(`{}`))
	h.api.HandleTasks(w, r)
	require.Equal(t, http.StatusAccepted, w.Code, "real Calendar Run now: %s", w.Body.String())
}

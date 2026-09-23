// auto_approve_gate_helpers_test.go: shared fixtures for the ADR-092 D9
// loop-wiring tests (auto_approve_gate_test.go, auto_approve_headless_test.go).

package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// autoRecordingApprover records every approval request it receives. The
// request count is the thing under test: a prompt shown to a human is exactly
// one RequestApproval call on this seam.
type autoRecordingApprover struct {
	mu      sync.Mutex
	reqs    []PolicyApprovalReq
	approve bool
}

func (a *autoRecordingApprover) RequestApproval(_ context.Context, req PolicyApprovalReq) (bool, string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.reqs = append(a.reqs, req)
	if a.approve {
		return true, "", false
	}
	return false, "user", false
}

func (a *autoRecordingApprover) requests() []PolicyApprovalReq {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]PolicyApprovalReq(nil), a.reqs...)
}

func (a *autoRecordingApprover) countFor(tool string) int {
	n := 0
	for _, r := range a.requests() {
		if r.ToolName == tool {
			n++
		}
	}
	return n
}

// autoStubTool stands in for a catalog tool by name. It records whether it
// ran, whether the loop pinned an Auto decision on its context, and the
// acting session/agent it ran under.
type autoStubTool struct {
	tools.BaseTool
	name    string
	calls   atomic.Int32
	pinned  atomic.Bool
	mu      sync.Mutex
	session string
	agent   string
}

func (s *autoStubTool) Name() string        { return s.name }
func (s *autoStubTool) Description() string { return "ADR-092 D9 loop test stub for " + s.name }
func (s *autoStubTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (s *autoStubTool) Scope() tools.ToolScope { return tools.ScopeGeneral }
func (s *autoStubTool) Execute(ctx context.Context, _ map[string]any) *tools.ToolResult {
	s.calls.Add(1)
	if _, ok := tools.AutoPinFrom(ctx); ok {
		s.pinned.Store(true)
	}
	s.mu.Lock()
	s.session = tools.ToolTranscriptSessionID(ctx)
	s.agent = tools.ToolAgentID(ctx)
	s.mu.Unlock()
	return &tools.ToolResult{ForLLM: "stub " + s.name + " ran"}
}

func (s *autoStubTool) identity() (session, agent string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.session, s.agent
}

// newAutoTestLoop builds a production-wired AgentLoop (NewAgentLoop) for the
// single agent "mia", with the global Auto-approve switch set as asked and
// mutate applied last.
func newAutoTestLoop(t *testing.T, provider providers.LLMProvider, autoApprove bool, mutate func(*config.Config)) *AgentLoop {
	t.Helper()
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	cfg, _ := baseLoopDenialTestConfig(t)
	cfg.Sandbox.AutoApprove = autoApprove
	if mutate != nil {
		mutate(cfg)
	}
	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	t.Cleanup(func() { al.Close() })
	return al
}

// installAutoStubs replaces each named tool on agentID with a stub and sets
// the agent's policy to "ask" for every stub plus every extra real tool.
func installAutoStubs(t *testing.T, al *AgentLoop, agentID string, names []string, extraAsk ...string) map[string]*autoStubTool {
	t.Helper()
	inst, ok := al.GetRegistry().GetAgent(agentID)
	require.True(t, ok, "agent %s must be registered", agentID)
	policies := map[string]config.ToolPolicy{}
	stubs := map[string]*autoStubTool{}
	for _, name := range names {
		inst.Tools.Unregister(name)
		stub := &autoStubTool{name: name}
		inst.Tools.Register(stub)
		got, found := inst.Tools.Get(name)
		require.True(t, found)
		require.Same(t, stub, got, "stub for %s must replace the real tool", name)
		stubs[name] = stub
		policies[name] = config.ToolPolicyAsk
	}
	for _, name := range extraAsk {
		policies[name] = config.ToolPolicyAsk
	}
	inst.StoreToolPolicy(&tools.ToolPolicyCfg{Policies: policies})
	return stubs
}

// stubToolCalls scripts one model response calling every named tool once.
func stubToolCalls(names []string) []providers.ToolCall {
	calls := make([]providers.ToolCall, 0, len(names))
	for _, name := range names {
		fc := providers.FunctionCall{Name: name, Arguments: "{}"}
		calls = append(calls, providers.ToolCall{ID: "call-" + name, Function: &fc})
	}
	return calls
}

// autoToolCall builds one scripted tool call with explicit JSON arguments.
func autoToolCall(id, name, argsJSON string) providers.ToolCall {
	fc := providers.FunctionCall{Name: name, Arguments: argsJSON}
	return providers.ToolCall{ID: id, Function: &fc}
}

// toolResultText returns the content of the tool-result message the loop
// sent back to the model for callID, from the provider's last request.
func toolResultText(t *testing.T, provider *testutil.ScenarioProvider, callID string) string {
	t.Helper()
	for _, m := range provider.LastMessages() {
		if m.Role == "tool" && m.ToolCallID == callID {
			return m.Content
		}
	}
	t.Fatalf("no tool result for call %s in the model's last request", callID)
	return ""
}

// swapAuditLogger points the loop's audit logger at a fresh temp dir so a
// test can read back exactly the rows its own turn wrote. The returned
// function closes the logger (flushing it) and returns the decoded rows.
func swapAuditLogger(t *testing.T, al *AgentLoop) func() []map[string]any {
	t.Helper()
	dir := t.TempDir()
	logger, err := audit.NewLogger(audit.LoggerConfig{Dir: dir, RetentionDays: 90})
	require.NoError(t, err)
	al.auditLogger = logger
	return func() []map[string]any {
		require.NoError(t, logger.Close())
		data, err := os.ReadFile(filepath.Join(dir, "audit.jsonl"))
		if os.IsNotExist(err) {
			return nil
		}
		require.NoError(t, err)
		var rows []map[string]any
		for _, line := range strings.Split(string(data), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			var row map[string]any
			require.NoError(t, json.Unmarshal([]byte(line), &row))
			rows = append(rows, row)
		}
		return rows
	}
}

// auditRowsFor filters rows to one event name (and one tool, when set). It
// reads both audit row shapes: an Entry names its tool in "tool", a Record
// (audit.Emit) in "fields.tool_name".
func auditRowsFor(rows []map[string]any, event, tool string) []map[string]any {
	var out []map[string]any
	for _, r := range rows {
		if r["event"] != event {
			continue
		}
		if tool != "" && auditRowTool(r) != tool {
			continue
		}
		out = append(out, r)
	}
	return out
}

func auditRowTool(r map[string]any) string {
	if name, ok := r["tool"].(string); ok && name != "" {
		return name
	}
	fields, _ := r["fields"].(map[string]any)
	name, _ := fields["tool_name"].(string)
	return name
}

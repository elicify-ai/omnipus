package agent

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func TestADR090_MCPPolicySnapshotPreservesAssignments(t *testing.T) {
	tool := tools.NewMCPTool(nil, "connector", &mcp.Tool{Name: "read"})
	global := &config.Config{}
	global.Sandbox.ToolPolicies = map[string]string{tool.Name(): "allow"}
	cfg := &config.AgentToolsCfg{MCP: config.AgentMCPToolsCfg{Servers: []config.AgentMCPServerBinding{{ID: "connector", Tools: []string{"read"}, ToolsSpecified: true}}}}
	snapshot := agentToolsCfgToPolicy(global, cfg)
	got, _ := tools.FilterToolsByPolicy([]tools.Tool{tool}, "core", snapshot)
	if len(got) != 1 {
		t.Fatal("assigned named tool was not carried into runtime policy")
	}
	cfg.MCP.Servers[0].ID = "other"
	cfg.MCP.Servers[0].Tools[0] = "write"
	got, _ = tools.FilterToolsByPolicy([]tools.Tool{tool}, "core", snapshot)
	if len(got) != 1 {
		t.Fatal("editing config mutated previously published policy snapshot")
	}
	next := agentToolsCfgToPolicy(global, cfg)
	got, _ = tools.FilterToolsByPolicy([]tools.Tool{tool}, "core", next)
	if len(got) != 0 {
		t.Fatal("new unbound snapshot still grants previous connector")
	}
}

// A running turn holds the old instance after fast publication swaps the registry.
// Execution must consult the new assignment snapshot, not that retained pointer.
func TestADR090_MCPExecUsesCurrentRegistryAssignments(t *testing.T) {
	tool := tools.NewMCPTool(nil, "connector", &mcp.Tool{Name: "read"})
	registryTools := tools.NewToolRegistry()
	registryTools.Register(tool)
	old := &AgentInstance{ID: "mia", AgentType: "core", Tools: registryTools}
	policies := map[string]config.ToolPolicy{tool.Name(): config.ToolPolicyAllow}
	old.StoreToolPolicy(&tools.ToolPolicyCfg{GlobalPolicies: policies, MCPServers: []config.AgentMCPServerBinding{{ID: "connector"}}})
	registry := &AgentRegistry{agents: map[string]*AgentInstance{"mia": old}}
	loop := &AgentLoop{registry: registry}
	turn := &turnState{agent: old, agentID: "mia"}
	if got := loop.resolveSingleToolPolicy(turn, tool.Name()); got != "allow" {
		t.Fatalf("positive control: got %s", got)
	}
	replacement := &AgentInstance{ID: "mia", AgentType: "core", Tools: tools.NewToolRegistry()}
	replacement.StoreToolPolicy(&tools.ToolPolicyCfg{GlobalPolicies: policies})
	registry.mu.Lock()
	registry.agents["mia"] = replacement
	registry.mu.Unlock()
	if got := loop.resolveSingleToolPolicy(turn, tool.Name()); got != "deny" {
		t.Fatalf("retained old instance bypassed unbinding: %s", got)
	}
	registry.mu.Lock()
	delete(registry.agents, "mia")
	registry.mu.Unlock()
	if got := loop.resolveSingleToolPolicy(turn, tool.Name()); got != "deny" {
		t.Fatalf("deleted agent still has execution authority: %s", got)
	}
}

type adr090CountingMCP struct{ namedStubTool }

func (*adr090CountingMCP) MCPSource() (string, string) { return "connector", "read" }

type adr090UnbindOnApproval struct {
	loop  *AgentLoop
	calls int
}

func (h *adr090UnbindOnApproval) RequestApproval(context.Context, PolicyApprovalReq) (bool, string) {
	h.calls++
	for _, id := range h.loop.GetRegistry().ListAgentIDs() {
		agent, _ := h.loop.GetRegistry().GetAgent(id)
		agent.StoreToolPolicy(&tools.ToolPolicyCfg{GlobalPolicies: map[string]config.ToolPolicy{"mcp_connector_read": config.ToolPolicyAllow}})
	}
	return true, ""
}

func TestADR090_MCPUnboundDuringApprovalNeverDispatches(t *testing.T) {
	cfg, _ := baseLoopDenialTestConfig(t)
	const name = "mcp_connector_read"
	provider := testutil.NewScenario().WithToolCalls(distinctToolCalls(name, 1)).WithText("handled")
	loop := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), provider)
	defer loop.Close()
	stub := &adr090CountingMCP{namedStubTool: namedStubTool{name: name}}
	loop.RegisterTool(stub)
	for _, id := range loop.GetRegistry().ListAgentIDs() {
		agent, _ := loop.GetRegistry().GetAgent(id)
		agent.StoreToolPolicy(&tools.ToolPolicyCfg{GlobalPolicies: map[string]config.ToolPolicy{name: config.ToolPolicyAsk}, MCPServers: []config.AgentMCPServerBinding{{ID: "connector"}}})
	}
	hook := &adr090UnbindOnApproval{loop: loop}
	loop.SetToolApprover(hook)
	result, err := loop.ProcessDirect(context.Background(), "read connector", "adr090-unbind-approval")
	require.NoError(t, err)
	require.Equal(t, "handled", result)
	require.Equal(t, 1, hook.calls, "positive control: initially assigned call reached approval")
	require.False(t, stub.wasCalled.Load(), "connector unbound during approval reached remote execution")
}

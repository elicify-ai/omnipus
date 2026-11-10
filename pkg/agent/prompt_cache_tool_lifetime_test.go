package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// lifetimeTool is a non-core registry entry. RegisterHidden leaves IsCore
// false, so today's TickTTL expires it. Its category is not MCP.
type lifetimeTool struct {
	name string
	body string
}

func (t *lifetimeTool) Name() string               { return t.name }
func (t *lifetimeTool) Description() string        { return "non-core session tool" }
func (t *lifetimeTool) Parameters() map[string]any { return map[string]any{"type": "object"} }
func (t *lifetimeTool) Scope() tools.ToolScope     { return tools.ScopeGeneral }
func (t *lifetimeTool) Category() tools.ToolCategory {
	return tools.CategoryCore
}
func (t *lifetimeTool) Execute(context.Context, map[string]any) *tools.ToolResult {
	return tools.SilentResult(t.body)
}

// lifetimeMCPManager is the process edge for one non-core MCP tool. The
// registry and the turn stay real.
type lifetimeMCPManager struct{}

func (lifetimeMCPManager) CallTool(context.Context, string, string, map[string]any) (*mcp.CallToolResult, error) {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "mcp-ran"}}}, nil
}

// loadedLifetime is one agent, one session, two loaded non-core tools.
// The TTL is the shipped discovery lifetime (ADR-071, default 5 turns).
type loadedLifetime struct {
	al     *AgentLoop
	agent  *AgentInstance
	owner  string
	sticky string
	mcp    string
	ttl    int
}

func newLoadedLifetime(t *testing.T) loadedLifetime {
	t.Helper()
	ttl := config.DefaultConfig().Tools.MCP.Discovery.TTL
	if ttl != 5 {
		t.Fatalf("shipped discovery TTL = %d, ADR-071 documents 5", ttl)
	}
	cfg := newCompressedCfg(t)
	cfg.Tools.MCP.Discovery.TTL = ttl
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	t.Cleanup(al.Close)

	agent, ok := al.registry.GetAgent("worker")
	if !ok {
		t.Fatal("worker agent is not registered")
	}
	sticky := &lifetimeTool{name: "sticky_widget", body: "sticky-ran"}
	mcpTool := tools.NewMCPTool(lifetimeMCPManager{}, "widgetsrv", &mcp.Tool{
		Name:        "widget",
		Description: "non-core mcp tool",
	})
	agent.Tools.RegisterHidden(sticky)
	agent.Tools.RegisterHiddenMCP(mcpTool)
	agent.StoreToolPolicy(&tools.ToolPolicyCfg{
		Policies: map[string]config.ToolPolicy{
			sticky.Name():  "allow",
			mcpTool.Name(): "allow",
		},
		MCPServers: []config.AgentMCPServerBinding{{ID: "widgetsrv"}},
	})
	names := []string{sticky.Name(), mcpTool.Name()}
	agent.Tools.PromoteTools(names, ttl)
	const owner = "sess-owner"
	al.markToolsLoaded(bucketFor(agent, owner), names)
	return loadedLifetime{al: al, agent: agent, owner: owner, sticky: sticky.Name(), mcp: mcpTool.Name(), ttl: ttl}
}

// providerToolList is the compressed branch of prepareToolSurface: GetAll,
// the policy filter, then buildCompressedToolDefs for that session.
func providerToolList(t *testing.T, fx loadedLifetime, sessionID string) ([]providers.ToolDefinition, map[string]string) {
	t.Helper()
	filtered, policy := tools.FilterToolsByPolicy(fx.agent.Tools.GetAll(), fx.agent.AgentType, fx.agent.LoadToolPolicy())
	defs, err := fx.al.buildCompressedToolDefs(fakeTurnState(fx.agent, sessionID), filtered)
	if err != nil {
		t.Fatalf("buildCompressedToolDefs(%s): %v", sessionID, err)
	}
	return defs, policy
}

func defNameSet(defs []providers.ToolDefinition) map[string]bool {
	out := make(map[string]bool, len(defs))
	for _, d := range defs {
		out[d.Function.Name] = true
	}
	return out
}

func requireRuns(t *testing.T, fx loadedLifetime, name, want string) {
	t.Helper()
	res := fx.agent.Tools.Execute(context.Background(), name, map[string]any{})
	if res == nil || res.IsError || res.ForLLM != want {
		got := ""
		if res != nil {
			got = res.ForLLM
		}
		t.Fatalf("fixture: %s did not run before the TTL (result %q)", name, got)
	}
}

func tickPastTTL(fx loadedLifetime) {
	for i := 0; i < fx.ttl; i++ {
		fx.agent.Tools.TickTTL()
	}
}

// TestLoadedTool_SurvivesTime is TDD row 14. Both loaded tools stay listed
// and executable after the discovery TTL, and the list bytes do not change.
func TestLoadedTool_SurvivesTime(t *testing.T) {
	fx := newLoadedLifetime(t)
	before, _ := providerToolList(t, fx, fx.owner)
	names := defNameSet(before)
	if !names[fx.sticky] || !names[fx.mcp] {
		t.Fatalf("fixture: provider list does not contain the loaded tools before the TTL (sticky %v mcp %v)", names[fx.sticky], names[fx.mcp])
	}
	requireRuns(t, fx, fx.sticky, "sticky-ran")
	requireRuns(t, fx, fx.mcp, "mcp-ran")

	tickPastTTL(fx)
	after, _ := providerToolList(t, fx, fx.owner)
	got := defNameSet(after)
	if !got[fx.sticky] || !got[fx.mcp] {
		t.Errorf("after %d TTL ticks the provider tool list dropped sticky=%v mcp=%v; the tool expired", fx.ttl, got[fx.sticky], got[fx.mcp])
	}
	for _, name := range []string{fx.sticky, fx.mcp} {
		res := fx.agent.Tools.Execute(context.Background(), name, map[string]any{})
		if res == nil || res.IsError {
			detail := ""
			if res != nil {
				detail = res.ForLLM
			}
			t.Errorf("%s is not executable after the TTL (%q); a loaded tool must still run", name, detail)
		}
	}
	beforeBytes, err := json.Marshal(before)
	if err != nil {
		t.Fatal(err)
	}
	afterBytes, err := json.Marshal(after)
	if err != nil {
		t.Fatal(err)
	}
	if string(beforeBytes) != string(afterBytes) {
		t.Errorf("provider tool list bytes changed merely because time passed")
	}
}

// TestLoadedMcpTool_ExecutesOnlyInThatSession is TDD row 26. The loaded MCP
// tool still runs in the owning session after the TTL, is absent from a
// second session, and leaves on Deny, assignment removal, or disconnect.
func TestLoadedMcpTool_ExecutesOnlyInThatSession(t *testing.T) {
	fx := newLoadedLifetime(t)
	otherDefs, otherPolicy := providerToolList(t, fx, "sess-other")
	other := defNameSet(otherDefs)
	if other[fx.sticky] || other[fx.mcp] {
		t.Errorf("second session lists sticky=%v mcp=%v", other[fx.sticky], other[fx.mcp])
	}
	otherTurn := fakeTurnState(fx.agent, "sess-other")
	for _, name := range []string{fx.sticky, fx.mcp} {
		if policy := fx.al.resolveToolPolicyAtExec(otherTurn, name, otherPolicy); policy != "deny" {
			t.Errorf("second session can run %s (policy %q); a session that did not load it must not", name, policy)
		}
	}

	tickPastTTL(fx)
	owner, _ := providerToolList(t, fx, fx.owner)
	if !defNameSet(owner)[fx.mcp] {
		t.Errorf("MCP tool left the owning session's provider list after %d TTL ticks; it expired", fx.ttl)
	}
	res := fx.agent.Tools.Execute(context.Background(), fx.mcp, map[string]any{})
	if res == nil || res.IsError {
		detail := ""
		if res != nil {
			detail = res.ForLLM
		}
		t.Errorf("MCP tool is not executable in the owning session after the TTL (%q)", detail)
	}
}

// TestLoadedTool_RemovedOnDenyAssignmentDisconnect is the revoke half of
// rows 14 and 26. Each revoke is applied while the TTL is still live, so a
// pass is the revoke itself and not an already-expired tool.
func TestLoadedTool_RemovedOnDenyAssignmentDisconnect(t *testing.T) {
	t.Run("deny", func(t *testing.T) {
		fx := newLoadedLifetime(t)
		fx.agent.StoreToolPolicy(&tools.ToolPolicyCfg{
			Policies: map[string]config.ToolPolicy{
				fx.sticky: "deny",
				fx.mcp:    "deny",
			},
			MCPServers: []config.AgentMCPServerBinding{{ID: "widgetsrv"}},
		})
		defs, policy := providerToolList(t, fx, fx.owner)
		got := defNameSet(defs)
		if got[fx.sticky] || got[fx.mcp] {
			t.Errorf("Deny left sticky=%v mcp=%v in the provider list", got[fx.sticky], got[fx.mcp])
		}
		turn := fakeTurnState(fx.agent, fx.owner)
		for _, name := range []string{fx.sticky, fx.mcp} {
			if got := fx.al.resolveToolPolicyAtExec(turn, name, policy); got != "deny" {
				t.Errorf("Deny did not stop %s (policy %q)", name, got)
			}
		}
	})

	t.Run("assignment", func(t *testing.T) {
		fx := newLoadedLifetime(t)
		fx.agent.StoreToolPolicy(&tools.ToolPolicyCfg{
			Policies: map[string]config.ToolPolicy{
				fx.sticky: "allow",
				fx.mcp:    "allow",
			},
		})
		defs, _ := providerToolList(t, fx, fx.owner)
		got := defNameSet(defs)
		if got[fx.mcp] {
			t.Errorf("removed MCP assignment left %s in the provider list", fx.mcp)
		}
		if !got[fx.sticky] {
			t.Errorf("removing an MCP assignment also dropped the non-MCP tool")
		}
	})

	t.Run("disconnect", func(t *testing.T) {
		fx := newLoadedLifetime(t)
		fx.agent.Tools.Unregister(fx.mcp)
		defs, _ := providerToolList(t, fx, fx.owner)
		if defNameSet(defs)[fx.mcp] {
			t.Errorf("server disconnect left %s in the provider list", fx.mcp)
		}
		res := fx.agent.Tools.Execute(context.Background(), fx.mcp, map[string]any{})
		if res == nil || !res.IsError {
			t.Errorf("disconnected MCP tool still ran")
		}
	})
}

// TestLoadedTool_NotInheritedByNextSession is TDD row 12b. Ending the
// session and opening another one does not carry the loaded tools.
func TestLoadedTool_NotInheritedByNextSession(t *testing.T) {
	fx := newLoadedLifetime(t)
	owner, _ := providerToolList(t, fx, fx.owner)
	if !defNameSet(owner)[fx.sticky] || !defNameSet(owner)[fx.mcp] {
		t.Fatal("fixture: owning session does not list the tools it loaded")
	}
	fx.al.forgetSession(fx.owner)
	next, _ := providerToolList(t, fx, "sess-next")
	got := defNameSet(next)
	if got[fx.sticky] || got[fx.mcp] {
		t.Fatalf("new session lists sticky=%v mcp=%v; a finished session's tools carried over", got[fx.sticky], got[fx.mcp])
	}
}

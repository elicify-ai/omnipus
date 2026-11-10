package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// SDK API checked from the installed go-sdk v1.4.1 server/toolschemas and
// streamable examples. This is the network edge only: Omnipus's MCP manager,
// executable registration, REST assignment validation and policy gate are real.
func issue1296MCPServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "assignment-probe", Version: "1"}, nil)
	for _, name := range []string{"read", "write"} {
		server.AddTool(&mcp.Tool{Name: name, InputSchema: json.RawMessage(`{"type":"object"}`)},
			func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "mcp-assignment-network-control"}}}, nil
			})
	}
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{
		Stateless: true, JSONResponse: true,
	})
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

func issue1296MCPSetup(endpoint string, names []string) func(*config.Config) {
	return func(cfg *config.Config) {
		cfg.Tools.MCP.Enabled = true
		cfg.Tools.MCP.Discovery.Enabled = false
		cfg.Tools.MCP.Servers = map[string]config.MCPServerConfig{"permprobe": {Enabled: true, Type: "http", URL: endpoint}}
		cfg.Sandbox.ToolPolicies["mcp_permprobe_*"] = "allow"
		for index := range cfg.Agents.List {
			if cfg.Agents.List[index].ID == "policy-receiver" && names != nil {
				cfg.Agents.List[index].Tools.MCP.Servers = []config.AgentMCPServerBinding{{ID: "permprobe", Tools: names, ToolsSpecified: true}}
			}
		}
	}
}

func issue1296MCPDecision(t *testing.T, h *issue1296Harness, remoteName string) (tools.Tool, string) {
	t.Helper()
	receiver, present := h.loop.GetRegistry().GetAgent("policy-receiver")
	require.True(t, present)
	name := "mcp_permprobe_" + remoteName // documented lossless runtime name, not observed output
	tool, registered := receiver.Tools.Get(name)
	require.True(t, registered, "real boot MCP registration must discover the network fixture")
	allowed, policies := tools.FilterToolsByPolicy([]tools.Tool{tool}, receiver.AgentType, receiver.LoadToolPolicy())
	if len(allowed) == 0 {
		assert.Empty(t, policies, "unassigned tool has no admitted policy entry")
		return tool, "deny"
	}
	require.Len(t, allowed, 1)
	assert.Equal(t, name, allowed[0].Name())
	assert.Equal(t, map[string]string{name: "allow"}, policies)
	return tool, "allow"
}

func issue1296MCPNetworkControl(t *testing.T, h *issue1296Harness, remoteName string) {
	t.Helper()
	tool, policy := issue1296MCPDecision(t, h, remoteName)
	require.Equal(t, "allow", policy, "network positive control must be actually assigned")
	result := tool.Execute(context.Background(), map[string]any{})
	require.NotNil(t, result)
	require.False(t, result.IsError, "the connected MCP tool must really answer: %s", result.ForLLM)
	assert.Contains(t, result.ForLLM, "mcp-assignment-network-control", "registration alone cannot fake a live connector")
}

func TestIssue1296RESTMCPAssignmentOnly_ClearGrantAndReplaceAreImmediate(t *testing.T) {
	for _, row := range []struct {
		name        string
		initial     []string
		next        []string
		beforeRead  string
		beforeWrite string
		afterRead   string
		afterWrite  string
	}{
		{"clear", []string{"read"}, nil, "allow", "deny", "deny", "deny"},
		{"grant", nil, []string{"read"}, "deny", "deny", "allow", "deny"},
		{"replace_selected_tools", []string{"read"}, []string{"write"}, "allow", "deny", "deny", "allow"},
	} {
		t.Run(row.name, func(t *testing.T) {
			connector := issue1296MCPServer(t)
			h := newIssue1296Harness(t, "allow", issue1296MCPSetup(connector.URL, row.initial))
			t.Run("initial_assignment_control", func(t *testing.T) {
				_, readPolicy := issue1296MCPDecision(t, h, "read")
				_, writePolicy := issue1296MCPDecision(t, h, "write")
				assert.Equal(t, row.beforeRead, readPolicy)
				assert.Equal(t, row.beforeWrite, writePolicy)
				if row.beforeRead == "allow" {
					issue1296MCPNetworkControl(t, h, "read")
				}
			})
			bindings := []any{}
			if row.next != nil {
				bindings = append(bindings, map[string]any{"id": "permprobe", "tools": row.next})
			}
			stored := h.put(t, map[string]any{"mcp_servers": bindings})
			wantStored := []config.AgentMCPServerBinding(nil)
			if row.next != nil {
				wantStored = []config.AgentMCPServerBinding{{ID: "permprobe", Tools: row.next, ToolsSpecified: true}}
			}
			assert.Equal(t, wantStored, stored.Agent.Tools.MCP.Servers, "REST assignment must be durably correct before checking runtime")
			t.Run("very_next_assignment_decision", func(t *testing.T) {
				_, readPolicy := issue1296MCPDecision(t, h, "read")
				_, writePolicy := issue1296MCPDecision(t, h, "write")
				assert.Equal(t, row.afterRead, readPolicy, "#1296: next MCP read grant must use the just-saved assignment, not the old snapshot")
				assert.Equal(t, row.afterWrite, writePolicy, "#1296: next selected-tool grant must hot-apply")
			})
			t.Run("restart_positive_control", func(t *testing.T) {
				h.restart(t)
				_, readPolicy := issue1296MCPDecision(t, h, "read")
				_, writePolicy := issue1296MCPDecision(t, h, "write")
				assert.Equal(t, row.afterRead, readPolicy)
				assert.Equal(t, row.afterWrite, writePolicy)
				if row.afterRead == "allow" {
					issue1296MCPNetworkControl(t, h, "read")
				}
				if row.afterWrite == "allow" {
					issue1296MCPNetworkControl(t, h, "write")
				}
			})
		})
	}
}

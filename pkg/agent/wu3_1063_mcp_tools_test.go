package agent

import (
	"encoding/json"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/tools"
)

// #1063 F1 regression (WU-3 ruling Q3): an agent that holds the MCP
// management tools can load and call them — the effective policy readback and
// the enforcement path agree. Real registry, compositor policy and argument
// validator (ExecuteWithContext); no stubbed policy.
func TestWU3_1063_MCPToolsCallableByHolder(t *testing.T) {
	f := newL11274Fixture(t, "", false)
	omnipus, ok := f.al.GetRegistry().GetAgent("omnipus")
	if !ok || omnipus == nil {
		t.Fatal("instrument: Omnipus is not registered in the fixture")
	}
	for name, want := range map[string]string{"list_mcp_servers": "allow", "add_mcp_server": "ask"} {
		tool, ok := omnipus.Tools.Get(name)
		if !ok {
			t.Fatalf("#1063: %s is not registered on Omnipus although its policy is %s", name, want)
		}
		if got := tools.EffectiveToolPolicy(omnipus.LoadToolPolicy(), tool.Scope(), omnipus.AgentType, name); got != want {
			t.Fatalf("#1063: Omnipus's effective policy for %s = %q, want %q", name, got, want)
		}
	}

	res := omnipus.Tools.ExecuteWithContext(tools.WithToolCallID(f.ctx, "1063-list"), "list_mcp_servers",
		map[string]any{}, "webchat", "1063-chat", nil)
	if res == nil || res.IsError {
		t.Fatalf("#1063: list_mcp_servers must be callable by its holder, got %+v", res)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(res.ForLLM), &body); err != nil {
		t.Fatalf("list_mcp_servers result is not JSON: %v (%s)", err, res.ForLLM)
	}
	if _, ok := body["servers"].([]any); !ok {
		t.Fatalf("#1063: list_mcp_servers must return a servers array, got %s", res.ForLLM)
	}

	// Negative control: the same policy readback reports deny for a tool
	// Omnipus does not hold (set_config is an explicit per-agent deny, spec
	// §6.5), so the "allow" above is not a readback that says allow for
	// everything. (The registry's ExecuteWithContext does not apply policy —
	// the agent loop gates calls on exactly this effective policy.)
	if tool, registered := omnipus.Tools.Get("set_config"); registered {
		if got := tools.EffectiveToolPolicy(omnipus.LoadToolPolicy(), tool.Scope(), omnipus.AgentType, "set_config"); got != "deny" {
			t.Fatalf("instrument: Omnipus's effective policy for set_config = %q, want deny", got)
		}
	}
}

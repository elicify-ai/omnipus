// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

// Regression coverage for the godmode-policy-freeze fix (2026-09-25 UAT
// finding on release/v0.1.1): under God Mode, a tool that is on "ask" ONLY in
// the global policies (e.g. delete_task) must run WITHOUT an approval card —
// the compositor contract in pkg/tools/compositor.go (global ask -> allow
// under God Mode; an agent's own ask survives).
//
// Root cause (confirmed): agent.NewAgentLoop builds every agent instance
// eagerly — each instance's tool-policy snapshot (agentToolsCfgToPolicy) and
// its bash tool (wireExecToolDepsOn) read agent.GodModeActive(cfg) AT THAT
// CONSTRUCTION TIME. The gateway used to publish god-mode AVAILABILITY (the
// package-level godModeAvailable atomic GodModeActive consults) only via
// AgentLoop.SetAllowGodMode, called from pkg/gateway/gateway_boot.go deep
// inside setupAndStartServices — AFTER agent.NewAgentLoop had already run.
// So even a persisted sandbox.god_mode=true (e.g. right after a God-Mode
// restart) left every already-built consumer frozen at GodMode=false until
// the next TriggerReload — exactly the UAT evidence: a delete_task call on an
// agent with no per-agent delete_task entry still produced an approval card
// (resolved_policy: "ask") right after the restart.
//
// The fix: pkg/gateway/gateway.go's loadConfigAndProvider now calls the new
// agent.SetGodModeAvailable BEFORE initializeAgentLoop/NewAgentLoop runs, so
// every consumer NewAgentLoop constructs already observes the correct
// availability at construction time — no reload required. These tests
// reproduce that exact ordering directly against the agent package (no
// gateway HTTP surface needed) and would not compile against 8c4892c29,
// which has no SetGodModeAvailable function at all.
import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// globalAskOnlyTool and agentOwnAskTool are tool names that are never
// registered in the real catalog — ResolveApprovalToolPolicy falls back to
// ScopeGeneral for an unresolved name (see its own doc comment), which always
// passes the scope gate regardless of agent type, so the test can probe the
// global x agent policy merge in isolation without needing a real tool.
const (
	globalAskOnlyTool = "godmode_test_global_ask_only"
	agentOwnAskTool   = "godmode_test_agent_own_ask"
)

// buildGodModeBootCfg returns a config shaped like a real post-restart boot:
// God Mode persisted ON (cfg.Sandbox.GodMode = true, as if a prior UI toggle
// already wrote it to config.json), one agent, one tool policy that is "ask"
// ONLY at the global layer (no per-agent entry — the delete_task shape from
// the UAT finding), and one tool policy that is "ask" on the AGENT's own
// ceiling (must survive God Mode, ADR-092/G-07).
func buildGodModeBootCfg(agentID string) *config.Config {
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
			List: []config.AgentConfig{
				{
					ID:   agentID,
					Name: "GodMode Boot Order Test Agent",
					Tools: &config.AgentToolsCfg{
						Builtin: config.AgentBuiltinToolsCfg{
							Policies: map[string]config.ToolPolicy{
								agentOwnAskTool: config.ToolPolicyAsk,
							},
						},
					},
				},
			},
		},
	}
	cfg.Sandbox.GodMode = true
	cfg.Sandbox.ToolPolicies = map[string]string{
		globalAskOnlyTool: string(config.ToolPolicyAsk),
	}
	return cfg
}

// TestGodMode_BootOrder_AvailabilityPublishedBeforeConstruction proves the
// fix end to end at the compositor layer (pkg/tools/compositor.go): with
// SetGodModeAvailable called BEFORE NewAgentLoop — the new gateway boot
// order — a tool on "ask" only in the global policies resolves "allow" (zero
// approver calls) immediately after construction, with NO reload, while a
// tool on the agent's OWN "ask" ceiling still resolves "ask" (survives God
// Mode, per the founder ruling and ADR-092/G-07).
func TestGodMode_BootOrder_AvailabilityPublishedBeforeConstruction(t *testing.T) {
	if !sandbox.GodModeAvailable {
		t.Skip("skipping: requires GodModeAvailable=true (default build)")
	}
	// Simulate a fresh process: nothing has published availability yet.
	withGodModeAvailable(t, false)

	const agentID = "godmode-boot-order-agent"
	cfg := buildGodModeBootCfg(agentID)

	// THE FIX under test: the gateway now calls this before agent.NewAgentLoop.
	SetGodModeAvailable(true)

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})

	// No al.SetAllowGodMode, no al.WireTier13Deps, no al.TriggerReload call
	// anywhere below — the assertions must hold from construction alone.

	if got := al.ResolveApprovalToolPolicy(agentID, globalAskOnlyTool); got != string(config.ToolPolicyAllow) {
		t.Errorf("global-ask-only tool under God Mode, no reload: got policy %q, want %q (zero approver calls)",
			got, config.ToolPolicyAllow)
	}
	if got := al.ResolveApprovalToolPolicy(agentID, agentOwnAskTool); got != string(config.ToolPolicyAsk) {
		t.Errorf("agent's OWN ask tool under God Mode, no reload: got policy %q, want %q (must still prompt)",
			got, config.ToolPolicyAsk)
	}

	// Same invariants must hold through the registry's live snapshot too
	// (ResolveRegisteredToolPolicy — the path management inventory uses).
	if got, ok := al.ResolveRegisteredToolPolicy(agentID, globalAskOnlyTool); !ok || got != string(config.ToolPolicyAllow) {
		t.Errorf("ResolveRegisteredToolPolicy global-ask-only: got (%q, %v), want (%q, true)",
			got, ok, config.ToolPolicyAllow)
	}
}

// shellGodModeForAgentNoRewire reads the bash tool's resolved GodMode flag
// straight off the registry AS BUILT — unlike shellGodModeForAgent (used
// elsewhere in this package), it deliberately does NOT call
// al.WireTier13Deps first, so it proves the flag was already correct at
// agent.NewAgentLoop construction time, with no extra wiring pass.
func shellGodModeForAgentNoRewire(t *testing.T, al *AgentLoop, agentID string) bool {
	t.Helper()
	reg := al.GetRegistry()
	if reg == nil {
		t.Fatal("GetRegistry returned nil")
	}
	ag, ok := reg.GetAgent(agentID)
	if !ok || ag == nil {
		t.Fatalf("%s not found in registry", agentID)
	}
	rawTool, found := ag.Tools.Get("bash")
	if !found {
		t.Fatal("bash tool not registered")
	}
	shellTool, ok := rawTool.(*tools.ExecTool)
	if !ok {
		t.Fatalf("bash is not *ExecTool; got %T", rawTool)
	}
	return shellTool.GodModeForTest()
}

// TestGodMode_BootOrder_BashAuditGodModeMatchesMode is the audit-layer proof:
// the UAT evidence showed a bash exec audit row with "mode":"god" (the live
// ADR-092 D1 label, ShellPermissionGate.liveMode, which was already reading
// agent.GodModeActive(cfg) live) but "god_mode":false (ExecTool's own GodMode
// flag, frozen at wireExecToolDepsOn construction time from the SAME stale
// availability read as the compositor bug above). With availability
// published before construction, both must now agree — the SAME real
// wiring path (agent.NewAgentLoop, no extra reload), and a REAL command
// execution whose audit entry is read back off disk.
func TestGodMode_BootOrder_BashAuditGodModeMatchesMode(t *testing.T) {
	if !sandbox.GodModeAvailable {
		t.Skip("skipping: requires GodModeAvailable=true (default build)")
	}
	withGodModeAvailable(t, false)

	const agentID = "godmode-boot-order-shell-agent"
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
			List: []config.AgentConfig{{ID: agentID, Name: "GodMode Boot Order Shell Agent"}},
		},
	}
	cfg.Sandbox.GodMode = true
	// Force a real audit logger (default-off in a bare struct literal — see
	// initializeAudit's cfg.Sandbox.AuditLog gate) so the exec audit row this
	// test asserts on is actually written to disk.
	cfg.Sandbox.AuditLog = true

	// THE FIX under test: publish availability before NewAgentLoop.
	SetGodModeAvailable(true)

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	// mustNewAgentLoop's ensureTestIsolatedAgentHome populates
	// cfg.Agents.Defaults.Home (a fresh t.TempDir()) when left unset above;
	// NewAgentLoop itself then derives homePath = filepath.Dir(that path) and
	// writes the audit tree to <homePath>/system — read the same value back
	// from cfg (now mutated) rather than assuming a directory shape.
	require.NotEmpty(t, cfg.Agents.Defaults.Home, "ensureTestIsolatedAgentHome must have populated this")
	homePath := filepath.Dir(cfg.Agents.Defaults.Home)

	// Sanity: the bash tool's own GodMode flag (ExecTool.godMode, fed
	// straight into the "god_mode" audit field) must already be true, with
	// no WireTier13Deps rewiring.
	if !shellGodModeForAgentNoRewire(t, al, agentID) {
		t.Fatal("bash tool GodMode must be true immediately after NewAgentLoop, with no reload")
	}

	reg := al.GetRegistry()
	ag, ok := reg.GetAgent(agentID)
	if !ok || ag == nil {
		t.Fatalf("%s not found in registry", agentID)
	}
	rawTool, found := ag.Tools.Get("bash")
	if !found {
		t.Fatal("bash tool not registered")
	}
	shellTool, ok := rawTool.(*tools.ExecTool)
	if !ok {
		t.Fatalf("bash is not *ExecTool; got %T", rawTool)
	}

	ctx := tools.WithAgentID(tools.WithToolContext(t.Context(), "cli", ""), agentID)
	result := shellTool.Execute(ctx, map[string]any{"command": "echo godmode-audit-proof"})
	if result == nil || result.IsError {
		var forLLM string
		if result != nil {
			forLLM = result.ContentForLLM()
		}
		t.Fatalf("bash Execute under God Mode must succeed, got IsError=true ForLLM=%q", forLLM)
	}

	// Read the real audit entry back off disk — the "Audit proof" this
	// package's own operating rules require: a policy decision must be
	// demonstrated by the actual written row, not just the in-memory flag.
	entry := lastAuditEntryForEvent(t, filepath.Join(homePath, "system", "audit.jsonl"), "exec")
	mode, _ := entry.Details["mode"].(string)
	godMode, _ := entry.Details["god_mode"].(bool)
	if mode != "god" {
		t.Fatalf("exec audit mode: got %q, want \"god\"", mode)
	}
	if !godMode {
		t.Fatalf("exec audit god_mode: got %v, want true (must match mode=%q) — details=%#v",
			godMode, mode, entry.Details)
	}
}

// auditEntryProbe mirrors the fields of audit.Entry this test needs, decoded
// independently of the audit package's own type so a change to Entry's JSON
// shape cannot silently make this oracle agree with a broken write.
type auditEntryProbe struct {
	Event   string         `json:"event"`
	Tool    string         `json:"tool"`
	Details map[string]any `json:"details"`
}

// lastAuditEntryForEvent scans path (a JSONL audit log) and returns the LAST
// entry whose Event equals wantEvent. Fails the test if the file cannot be
// read or no matching entry is found.
func lastAuditEntryForEvent(t *testing.T, path, wantEvent string) auditEntryProbe {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open audit log %s: %v", path, err)
	}
	defer f.Close()

	var last auditEntryProbe
	found := false
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var e auditEntryProbe
		if err := json.Unmarshal(line, &e); err != nil {
			continue
		}
		if e.Event == wantEvent {
			last = e
			found = true
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan audit log %s: %v", path, err)
	}
	if !found {
		t.Fatalf("no audit entry with event=%q found in %s", wantEvent, path)
	}
	return last
}

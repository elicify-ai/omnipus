// Regression tests for the tool-enablement refactor.
//
// Before this refactor, cfg.Tools.<Name>.Enabled was a second enablement layer
// read by IsToolEnabled() that could silently prevent tool registration while
// the UI policy layer (allow/ask/deny) showed the tool as enabled. The two
// layers were redundant. This file locks in the one-layer contract: every
// implemented tool registers unconditionally; policy decides invocation.
//
// If these tests fail, a regression has reintroduced a pre-registration gate.

package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// TestAllImplementedToolsRegistered_DefaultConfig proves the headline contract:
// with a brand-new config that has no tools.*.enabled fields set (the exact
// shape Omnipus writes on fresh onboarding), every implemented tool ends up in
// each agent's registry.
//
// Previously this test would have failed for browser, mcp, task_*, etc. —
// their Enabled default was false, so IsToolEnabled returned false and the
// agent loop silently skipped registration.
func TestAllImplementedToolsRegistered_DefaultConfig(t *testing.T) {
	cfg := &config.Config{}
	cfg.Agents.Defaults.Home = t.TempDir()
	// No "main" sentinel to fall back to anymore — a bare cfg.Agents.List
	// now yields a registry with ZERO agents. This test only needs SOME
	// registered agent to check tool registration against, so an ordinary
	// one suffices.
	cfg.Agents.List = []config.AgentConfig{{ID: "mia", Home: cfg.Agents.Defaults.Home}}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	require.NotNil(t, al)
	defer al.Close()

	reg := al.GetRegistry()
	require.NotNil(t, reg, "agent loop must have a registry")

	ids := reg.ListAgentIDs()
	require.NotEmpty(t, ids, "at least the default agent must be registered")

	// Pick the first agent — the same set of tools registers for every agent
	// in the default seeded registry.
	agent, ok := reg.GetAgent(ids[0])
	require.True(t, ok, "first listed agent must be retrievable")
	require.NotNil(t, agent)
	require.NotNil(t, agent.Tools)

	// Every implemented tool must be present regardless of any cfg.Tools.*.Enabled flag.
	expected := []string{
		// File-system tools
		"read_file", "write_file", "edit_file", "append_file", "list_directory",
		// Execution (ADR-036: exec/workspace_shell/workspace_shell_bg merged into "bash")
		"bash",
		// Web
		"fetch_url",
		// Communication
		"send_message", "send_file",
		// Skills
		// remove_skill is deliberately NOT checked here: it is a ScopeCore
		// management tool (systools.SkillRemoveTool) registered by
		// WireSysagentDeps (pkg/gateway/gateway.go), a gateway-boot-only wiring
		// step this unit-test harness's mustNewAgentLoop never calls — so it is
		// legitimately absent from agent.Tools in this narrow construction path.
		"find_skills", "install_skill",
		// Agent orchestration
		"delegate", "switch_agent",
		// Browser automation — the headline bug being fixed
		"browser_navigate", "browser_click", "browser_type",
		"browser_screenshot", "browser_get_text", "browser_wait",
		// browser_evaluate stays registered; policy denies it by default.
		"browser_evaluate",
	}

	for _, name := range expected {
		_, found := agent.Tools.Get(name)
		assert.True(t, found,
			"tool %q must be registered on a default-config agent (enablement is policy-gated, not registration-gated)",
			name)
	}
}

// TestDeprecatedEnableFlagScanDoesNotPanic exercises the warn-once path on a
// synthetic legacy config. It's a smoke test — the full behavior lives in
// pkg/config, but we want to make sure the code path is wired into LoadConfig
// and doesn't crash when real tool_list entries are present.
func TestDeprecatedEnableFlagScanDoesNotPanic(t *testing.T) {
	cfg := &config.Config{}
	// Simulate an operator who used the old path to "disable" exec.
	cfg.Tools.Exec.Enabled = false
	cfg.Tools.Browser.Enabled = false
	// Method must be safe on zero and partially-populated structs.
	cfg.Tools.Exec.Enabled = false
	// We can't easily assert on the log output without a logger fixture;
	// the real contract (warning emitted exactly once) is covered by a
	// test in pkg/config. Here we only confirm no crash.
	assert.NotPanics(t, func() {
		// Build a loop with this cfg so we exercise the downstream paths
		// that previously consulted IsToolEnabled.
		al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
		_ = al
	})
}

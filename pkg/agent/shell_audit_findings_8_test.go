// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// readAuditEvents (JSONL reader, tolerating a not-yet-created file) is
// already defined in memory_reload_wiring_test.go — reused here rather than
// duplicated.

// TestEmitShellRuleSettledAudit_WritesApprovalDecision is the regression
// test for review finding #8(c) (LOW, 2026-09-23 security fix lane): a
// prompt an operator D3 ALLOW rule fully settled (bashRuleVerdict.settlesPrompt)
// left NO audit trail at all before this fix — indistinguishable in the
// log from an ordinary unprompted "allow"-ceiling execution.
func TestEmitShellRuleSettledAudit_WritesApprovalDecision(t *testing.T) {
	al, _, _, _, cleanup := newTestAgentLoop(t) //nolint:dogsled // only al+cleanup used here
	defer cleanup()

	dir := t.TempDir()
	lg, err := audit.NewLogger(audit.LoggerConfig{Dir: dir, MaxSizeBytes: 1024 * 1024, RetentionDays: 1})
	require.NoError(t, err)
	defer lg.Close()
	al.auditLogger = lg

	ts := &turnState{agentID: testDefaultAgentID, transcriptSessionID: "sess-rule-settled"}
	al.emitShellRuleSettledAudit(ts, map[string]any{"command": "git status"})
	require.NoError(t, lg.Close())

	entries := readAuditEvents(t, dir+"/audit.jsonl")
	require.NotEmpty(t, entries, "finding #8(c) regression: a rule-settled prompt must write an audit entry")

	found := false
	for _, e := range entries {
		if e["event"] == "shell.approval_decision" {
			found = true
			require.Equal(t, "allow", e["decision"])
			require.Equal(t, testDefaultAgentID, e["agent_id"])
			require.Equal(t, "sess-rule-settled", e["session_id"])
			require.Equal(t, "bash", e["tool"])
			require.Equal(t, "git status", e["command"])
			details, ok := e["details"].(map[string]any)
			require.True(t, ok, "details must be present")
			require.Equal(t, "rule_fully_allowed", details["kind"])
		}
	}
	require.True(t, found, "expected a shell.approval_decision event")
}

// TestEmitShellClassicAskDecisionAudit_WritesApprovalDecision is the
// regression test for review finding #8(b) (LOW): the ordinary classic
// ask-policy human decision (bash tool policy == "ask", non-Auto) never
// emitted shell.approval_decision before this fix — only the NEW ADR-092
// D3/D7/D8 call sites inside pkg/tools did.
func TestEmitShellClassicAskDecisionAudit_WritesApprovalDecision(t *testing.T) {
	al, _, _, _, cleanup := newTestAgentLoop(t) //nolint:dogsled // only al+cleanup used here
	defer cleanup()

	dir := t.TempDir()
	lg, err := audit.NewLogger(audit.LoggerConfig{Dir: dir, MaxSizeBytes: 1024 * 1024, RetentionDays: 1})
	require.NoError(t, err)
	al.auditLogger = lg

	ts := &turnState{agentID: testDefaultAgentID, transcriptSessionID: "sess-classic-ask"}
	al.emitShellClassicAskDecisionAudit(ts, map[string]any{"command": "rm -rf /tmp/x"}, "classic_ask", false, "user")
	require.NoError(t, lg.Close())

	entries := readAuditEvents(t, dir+"/audit.jsonl")
	found := false
	for _, e := range entries {
		if e["event"] == "shell.approval_decision" {
			found = true
			require.Equal(t, "deny", e["decision"])
			require.Equal(t, "bash", e["tool"])
			require.Equal(t, "rm -rf /tmp/x", e["command"])
			details, ok := e["details"].(map[string]any)
			require.True(t, ok)
			require.Equal(t, "classic_ask", details["kind"])
			require.Equal(t, "user", details["reason"])
		}
	}
	require.True(t, found, "finding #8(b) regression: a classic Ask-mode human decision must write an audit entry")
}

// TestApprovalGrants_AuditLoggerWiredAtConstruction is the regression test
// for review finding #8(a) (LOW): the grant store used to be built with no
// audit logger at all (loop_construct.go's initializeRuntime), only gaining
// one lazily when the FIRST bash command ran
// (pkg/tools/shell_permission_mode.go::enforceShellPermissionMode's own
// SetAuditLogger call) — so a grant recorded by any OTHER path BEFORE the
// first bash call (e.g. a classic exact "Always Allow" on a non-bash tool
// via rest_tool_registry.go) emitted no shell.grant_recorded event.
//
// This drives a real boot (mustNewAgentLoop -> NewAgentLoop ->
// initializeRuntime) with audit enabled, then records a grant for a
// NON-bash tool directly — never touching the bash tool or
// enforceShellPermissionMode at all — and asserts the event still fired,
// proving the logger was wired at construction, not lazily.
func TestApprovalGrants_AuditLoggerWiredAtConstruction(t *testing.T) {
	home := t.TempDir()
	cfg := &config.Config{
		Sandbox: config.OmnipusSandboxConfig{AuditLog: true},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              home,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: testDefaultAgentID, Home: home}},
		},
	}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})

	// A bash grant recorded directly against the store — the classic
	// exact-fingerprint "Always Allow" path (rest_tool_registry.go's own
	// grants.Record call), never through enforceShellPermissionMode or any
	// other bash-tool-owned call site. ApprovalGrantStore.Record only emits
	// shell.grant_recorded for tool=="bash" (its own bashToolName gate), so
	// this exercises exactly the "grant recorded before the first bash call
	// runs" scenario finding #8(a) describes.
	require.True(t, al.ApprovalGrants().Record("sess-8a", "agent-8a", "bash", map[string]any{"command": "ls"}),
		"setup: Record must succeed")

	// al.homePath (set during construction) is the actual root NewAgentLoop
	// resolved audit under — not necessarily cfg.Agents.Defaults.Home
	// verbatim (NewAgentLoop computes it as filepath.Dir(cfg.
	// AgentHomeBasePath()), which is not simply Home again) — so locate
	// audit.jsonl by walking from there instead of assuming a specific
	// relative path.
	var auditPath string
	require.NoError(t, filepath.WalkDir(al.homePath, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil //nolint:nilerr // best-effort search
		}
		if d.Name() == "audit.jsonl" {
			auditPath = path
		}
		return nil
	}))
	require.NotEmpty(t, auditPath, "audit.jsonl must exist somewhere under %s — audit_log=true was requested", al.homePath)

	entries := readAuditEvents(t, auditPath)
	found := false
	for _, e := range entries {
		if e["event"] == "shell.grant_recorded" {
			found = true
		}
	}
	require.True(t, found,
		"finding #8(a) regression: a grant recorded before any bash call must still emit shell.grant_recorded")
}

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// TestGrepTool_ExecuteAndPolicy is spec test 22
// (docs/internal/specs/unified-search-and-grep-spec.md, US-3 AS-1/5):
// grep is registered on every real AgentInstance and executes for real
// against a seeded workspace when policy allows it, and — the sibling
// case this file exists to prove — is refused and audited exactly like any
// other tool when policy denies it.
//
// The deny half mirrors TestRunTurn_ScriptedToolCall_PolicyDeniesAndAudits
// (scenario_runturn_test.go) almost verbatim: same tmpHome layout, same
// ScenarioProvider harness, same StoreToolPolicy + audit-file assertions —
// swapped from the test-only dangerous_tool stub onto the real grep tool,
// which is the actual thing this suite needs proven end to end.
package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// seedGrepPolicyTestWorkspace makes agentID a CoreTeam member of its own,
// dedicated workspace (so pkg/agent's test-harness auto-join skips it — see
// the call site's own comment) and writes files (name -> content) into that
// workspace's real work/ directory, i.e. the exact directory a real turn's
// TurnWorkspaceDir re-root will point grep's own search at.
func seedGrepPolicyTestWorkspace(t *testing.T, home, agentID, wsID string, files map[string]string) error {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	if err := workspace.SaveRecord(home, workspace.Workspace{
		ID: wsID, Name: "test", Status: "active", CreatedAt: now, UpdatedAt: now,
		CoreTeam: []string{agentID},
	}); err != nil {
		return err
	}
	work, err := workspace.EnsureWorkDir(home, wsID)
	if err != nil {
		return err
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(work, name), []byte(content), 0o600); err != nil {
			return err
		}
	}
	return nil
}

func TestGrepTool_ExecuteAndPolicy(t *testing.T) {
	t.Run("registered by default and executes for real", func(t *testing.T) {
		tmpHome := t.TempDir()
		// GrepTool's own mount-resolution step (grepRoots) calls
		// config.OmnipusHomeDir() directly, independent of cfg.Agents.Defaults.Home
		// above — without this, it would resolve against whatever
		// $OMNIPUS_HOME the test PROCESS inherited (e.g. a real ~/.omnipus on
		// the machine running this suite), not this test's own tmpHome, and
		// could wrongly re-root the turn into a stale real workspace if one
		// happens to name "mia" as a CoreTeam member. t.Setenv keeps this
		// test hermetic, matching every other mount-consuming test in this
		// codebase (e.g. pkg/tools/mount_coreteam_turn_test.go).
		t.Setenv(config.EnvHome, tmpHome)
		workspaceDir := filepath.Join(tmpHome, "workspace")
		require.NoError(t, os.MkdirAll(workspaceDir, 0o755))

		// pkg/agent's own test harness (mustNewAgentLoop ->
		// ensureTestWorkspaceMembership) auto-joins every agent id in cfg
		// that is NOT ALREADY a member of some workspace to one SHARED
		// "test-harness-default" workspace, purely so ordinary turn tests
		// (that never touch real files) don't need a workspace fixture of
		// their own. For THIS test that auto-join is exactly wrong: it would
		// re-root the turn's TurnWorkspaceDir into that shared workspace's
		// own work/ directory instead of workspaceDir above, and grep would
		// (correctly, per FR-020) search THAT directory — silently missing
		// notes.md. Pre-seeding "mia" as a CoreTeam member of its OWN
		// workspace here makes ensureTestWorkspaceMembership skip it
		// (already covered — see that function's own doc comment), so the
		// real re-root target is a workspace THIS test controls; notes.md is
		// seeded into that workspace's actual work/ directory, exactly where
		// the turn will really search.
		require.NoError(t, seedGrepPolicyTestWorkspace(t, tmpHome, "mia", "ws-grep-allow", map[string]string{
			"notes.md": "hello\nneedle-in-a-haystack\nbye\n",
		}))

		provider := testutil.NewScenario().
			WithToolCall("grep", `{"pattern":"needle"}`).
			WithText("found it.")

		cfg := &config.Config{
			Agents: config.AgentsConfig{
				Defaults: config.AgentDefaults{
					Home:              workspaceDir,
					DefaultModel:      config.DefaultModel{Model: "scripted-model"},
					MaxTokens:         4096,
					MaxToolIterations: 10,
				},
				List: []config.AgentConfig{{ID: "mia", Home: workspaceDir}},
			},
		}

		msgBus := bus.NewMessageBus()
		al := mustNewAgentLoop(t, cfg, msgBus, provider)
		defer al.Close()

		// Proves REGISTRATION first, independent of running a turn: the
		// pkg/agent/instance.go call site under test
		// (toolsRegistry.Register(tools.NewGrepTool(workspace, readRestrict)))
		// runs unconditionally for every agent at construction time — this
		// is not something this test wires up itself.
		defaultAgent := al.GetRegistry().GetDefaultAgent()
		require.NotNil(t, defaultAgent, "default agent must exist after boot")
		_, registered := defaultAgent.Tools.Get("grep")
		require.True(t, registered, "grep must be registered on every agent by default (Constraint #6: registration, not permission)")

		// This test's bare config.Config{} seeds NO tool policy at all — per
		// Constraint #6 (no default-policy fallback), every tool with no
		// explicit policy entry fails closed to deny, grep included. Real
		// installs seed grep's policy explicitly to "allow" for every tier
		// (ADR-081 FR-009, owned by governance, not this file); this test
		// grants the SAME explicit "allow" directly, exactly as a real seed
		// would, so it exercises Execute() rather than the policy gate.
		defaultAgent.StoreToolPolicy(&tools.ToolPolicyCfg{
			Policies: map[string]config.ToolPolicy{"grep": config.ToolPolicyAllow},
		})

		ctx := context.Background()
		_, err := al.ProcessDirect(ctx, "please grep for needle", "test-session-grep-allow")
		require.NoError(t, err)

		const resolvedSessionKey = "agent:mia:main"
		history := defaultAgent.Sessions.GetHistory(resolvedSessionKey)
		require.NotEmpty(t, history, "session history must not be empty after a completed turn")

		var toolContent string
		for _, msg := range history {
			if msg.Role == "tool" && strings.Contains(msg.Content, "needle-in-a-haystack") {
				toolContent = msg.Content
				break
			}
		}
		require.NotEmpty(t, toolContent,
			"expected the REAL grep result (containing the matched line) persisted in history — "+
				"a stub or hardcoded response would fail this; history: %+v", history)
		assert.Contains(t, toolContent, "notes.md:2:",
			"the persisted tool result must name the real file and line number")
	})

	t.Run("policy deny: refused and audited", func(t *testing.T) {
		tmpHome := t.TempDir()
		t.Setenv(config.EnvHome, tmpHome) // hermetic — see the allow subtest's comment
		workspaceDir := filepath.Join(tmpHome, "workspace")
		require.NoError(t, os.MkdirAll(workspaceDir, 0o755))
		require.NoError(t, os.WriteFile(
			filepath.Join(workspaceDir, "notes.md"), []byte("needle\n"), 0o600,
		))

		provider := testutil.NewScenario().
			WithToolCall("grep", `{"pattern":"needle"}`).
			WithText("I cannot search right now.")

		cfg := &config.Config{
			Agents: config.AgentsConfig{
				Defaults: config.AgentDefaults{
					Home:              workspaceDir,
					DefaultModel:      config.DefaultModel{Model: "scripted-model"},
					MaxTokens:         4096,
					MaxToolIterations: 10,
				},
				List: []config.AgentConfig{{ID: "mia", Home: workspaceDir}},
			},
			Sandbox: config.OmnipusSandboxConfig{
				// Enable audit logging so emitPolicyDenyAudit writes to audit.jsonl.
				AuditLog: true,
			},
		}

		msgBus := bus.NewMessageBus()
		al := mustNewAgentLoop(t, cfg, msgBus, provider)
		defer al.Close()

		// Apply deny policy for grep on every agent in the registry — the
		// atomic-pointer setter the TOCTOU re-check (FR-079) reads, exactly
		// as TestRunTurn_ScriptedToolCall_PolicyDeniesAndAudits does for
		// dangerous_tool.
		for _, agentID := range al.GetRegistry().ListAgentIDs() {
			agentInst, ok := al.GetRegistry().GetAgent(agentID)
			if !ok {
				continue
			}
			agentInst.StoreToolPolicy(&tools.ToolPolicyCfg{
				Policies: map[string]config.ToolPolicy{
					"grep": "deny",
				},
			})
		}

		ctx := context.Background()
		_, err := al.ProcessDirect(ctx, "please grep for needle", "test-session-grep-deny")
		require.NoError(t, err, "policy deny is a loop-level action, not an agent error")

		auditPath := filepath.Join(tmpHome, "system", "audit.jsonl")
		require.FileExists(t, auditPath, "audit.jsonl must exist — AuditLog=true and a deny event was expected")

		entries, err := readAuditEntries(auditPath)
		require.NoError(t, err, "audit.jsonl must be readable and contain valid JSONL")
		require.NotEmpty(t, entries, "audit.jsonl must contain at least one entry")

		var denyEntries []map[string]any
		for _, e := range entries {
			if e["event"] == audit.EventToolPolicyDenyAttempted && e["decision"] == audit.DecisionDeny {
				denyEntries = append(denyEntries, e)
			}
		}
		require.NotEmpty(t, denyEntries,
			"audit log must contain at least one %q (decision=%q) entry; got entries: %v",
			audit.EventToolPolicyDenyAttempted, audit.DecisionDeny, entries)

		found := false
		for _, e := range denyEntries {
			if e["tool"] == "grep" {
				found = true
				break
			}
		}
		assert.True(t, found, `deny audit entry must have tool="grep"; deny entries: %v`, denyEntries)

		// Persistence test, mirroring the sibling test: the synthetic deny
		// result actually reached session history, not just an audit line.
		defaultAgent, ok := al.GetRegistry().GetAgent("mia")
		require.True(t, ok)
		history := defaultAgent.Sessions.GetHistory("agent:mia:main")
		require.NotEmpty(t, history)

		var denyMsgFound bool
		for _, msg := range history {
			if msg.Role == "tool" && strings.Contains(msg.Content, "permission_denied") {
				denyMsgFound = true
				break
			}
		}
		assert.True(t, denyMsgFound, "session history must contain a role=tool permission_denied message; history: %+v", history)
	})
}

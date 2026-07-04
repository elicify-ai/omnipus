// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// Contract tests for ADR-036's exec/workspace_shell/workspace_shell_bg -> bash
// tool-policy migration (bash-tool-spec.md User Story 4, FR-M1..FR-M4).
// Traces to: pkg/config/shell_tool_policy_migration.go, pkg/config/migration.go
// (toolEnableToPolicy's {"exec","bash"} row, FR-M2), pkg/config/config.go
// (loadConfigInternal wiring).

package config

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureSlog redirects slog's default logger to a buffer for the duration of
// the test, restoring the previous default on cleanup. Mirrors the pattern
// used by TestMigrateDeprecatedToolEnableFlags_MigrateOnce
// (deprecation_warn_once_test.go).
func captureSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	handler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})
	oldDefault := slog.Default()
	slog.SetDefault(slog.New(handler))
	t.Cleanup(func() { slog.SetDefault(oldDefault) })
	return &buf
}

// --- Dataset: migration key combinations (bash-tool-spec.md) ---
// Direct, in-memory unit tests against migrateShellToolPolicyKeys — fast and
// precise, exercising both scopes (per-agent AgentBuiltinToolsCfg.Policies
// and the global OmnipusSandboxConfig.ToolPolicies) the function must handle.

// TestMigrateShellToolPolicyKeys_ExecDenySurvives covers dataset row 1:
// {"exec": "deny"} -> bash: deny, exec key gone. Exercised at both scopes.
func TestMigrateShellToolPolicyKeys_ExecDenySurvives(t *testing.T) {
	t.Run("PerAgent", func(t *testing.T) {
		cfg := &Config{
			Agents: AgentsConfig{List: []AgentConfig{
				{
					ID: "research-bot",
					Tools: &AgentToolsCfg{Builtin: AgentBuiltinToolsCfg{
						Policies: map[string]ToolPolicy{"exec": ToolPolicyDeny},
					}},
				},
			}},
		}

		touched := migrateShellToolPolicyKeys(cfg)

		assert.True(t, touched)
		policies := cfg.Agents.List[0].Tools.Builtin.Policies
		assert.Equal(t, ToolPolicyDeny, policies["bash"])
		_, hasExec := policies["exec"]
		assert.False(t, hasExec, "legacy \"exec\" key must be gone after migration")
	})

	t.Run("Global", func(t *testing.T) {
		cfg := &Config{Sandbox: OmnipusSandboxConfig{
			ToolPolicies: map[string]string{"exec": "deny"},
		}}

		touched := migrateShellToolPolicyKeys(cfg)

		assert.True(t, touched)
		assert.Equal(t, "deny", cfg.Sandbox.ToolPolicies["bash"])
		_, hasExec := cfg.Sandbox.ToolPolicies["exec"]
		assert.False(t, hasExec, "legacy \"exec\" key must be gone after migration")
	})
}

// TestMigrateShellToolPolicyKeys_SingleKeyAskAndAllow covers dataset rows 2
// and 3 in ISOLATION: {"exec": "ask"} -> bash: ask, and {"exec": "allow"} ->
// bash: allow. These two boundary rows previously had no standalone test —
// only TestMigrateShellToolPolicyKeys_StricterWins exercised "ask"/"allow"
// values, and only ever in combination with a conflicting stricter key, which
// cannot distinguish "the strictest-wins comparison happens to produce ask/
// allow as a side effect" from "a lone ask/allow value is itself preserved
// correctly". A regression that, say, always resolved a single legacy key to
// "deny" (or dropped a lone non-deny value entirely) would still pass
// StricterWins (since deny always wins there) but would be caught here.
func TestMigrateShellToolPolicyKeys_SingleKeyAskAndAllow(t *testing.T) {
	t.Run("SingleKey_Ask", func(t *testing.T) {
		cfg := &Config{Sandbox: OmnipusSandboxConfig{
			ToolPolicies: map[string]string{"exec": "ask"},
		}}

		touched := migrateShellToolPolicyKeys(cfg)

		require.True(t, touched)
		assert.Equal(t, "ask", cfg.Sandbox.ToolPolicies["bash"])
		_, hasExec := cfg.Sandbox.ToolPolicies["exec"]
		assert.False(t, hasExec, "legacy \"exec\" key must be gone after migration")
	})

	t.Run("SingleKey_Allow", func(t *testing.T) {
		cfg := &Config{Sandbox: OmnipusSandboxConfig{
			ToolPolicies: map[string]string{"exec": "allow"},
		}}

		touched := migrateShellToolPolicyKeys(cfg)

		require.True(t, touched)
		assert.Equal(t, "allow", cfg.Sandbox.ToolPolicies["bash"])
		_, hasExec := cfg.Sandbox.ToolPolicies["exec"]
		assert.False(t, hasExec, "legacy \"exec\" key must be gone after migration")
	})

	// Same two rows at the per-agent scope, mirroring
	// TestMigrateShellToolPolicyKeys_ExecDenySurvives's dual-scope coverage.
	t.Run("SingleKey_Ask_PerAgent", func(t *testing.T) {
		cfg := &Config{
			Agents: AgentsConfig{List: []AgentConfig{
				{
					ID: "ask-bot",
					Tools: &AgentToolsCfg{Builtin: AgentBuiltinToolsCfg{
						Policies: map[string]ToolPolicy{"exec": ToolPolicyAsk},
					}},
				},
			}},
		}

		touched := migrateShellToolPolicyKeys(cfg)

		require.True(t, touched)
		policies := cfg.Agents.List[0].Tools.Builtin.Policies
		assert.Equal(t, ToolPolicyAsk, policies["bash"])
		_, hasExec := policies["exec"]
		assert.False(t, hasExec, "legacy \"exec\" key must be gone after migration")
	})

	t.Run("SingleKey_Allow_PerAgent", func(t *testing.T) {
		cfg := &Config{
			Agents: AgentsConfig{List: []AgentConfig{
				{
					ID: "allow-bot",
					Tools: &AgentToolsCfg{Builtin: AgentBuiltinToolsCfg{
						Policies: map[string]ToolPolicy{"exec": ToolPolicyAllow},
					}},
				},
			}},
		}

		touched := migrateShellToolPolicyKeys(cfg)

		require.True(t, touched)
		policies := cfg.Agents.List[0].Tools.Builtin.Policies
		assert.Equal(t, ToolPolicyAllow, policies["bash"])
		_, hasExec := policies["exec"]
		assert.False(t, hasExec, "legacy \"exec\" key must be gone after migration")
	})
}

// TestMigrateShellToolPolicyKeys_StricterWins covers dataset rows 5 and 6:
// contradictory legacy keys resolve to the strictest present value
// (deny > ask > allow), for both the 2-key and 3-key combinations.
func TestMigrateShellToolPolicyKeys_StricterWins(t *testing.T) {
	t.Run("TwoKeys_AskAndDeny", func(t *testing.T) {
		cfg := &Config{Sandbox: OmnipusSandboxConfig{
			ToolPolicies: map[string]string{"exec": "ask", "workspace_shell": "deny"},
		}}

		touched := migrateShellToolPolicyKeys(cfg)

		require.True(t, touched)
		assert.Equal(t, "deny", cfg.Sandbox.ToolPolicies["bash"])
		for _, k := range []string{"exec", "workspace_shell", "workspace_shell_bg"} {
			_, has := cfg.Sandbox.ToolPolicies[k]
			assert.False(t, has, "legacy key %q must be gone", k)
		}
	})

	t.Run("ThreeKeys_AllowAllowAsk", func(t *testing.T) {
		cfg := &Config{
			Agents: AgentsConfig{List: []AgentConfig{
				{
					ID: "ops-bot",
					Tools: &AgentToolsCfg{Builtin: AgentBuiltinToolsCfg{
						Policies: map[string]ToolPolicy{
							"exec":               ToolPolicyAllow,
							"workspace_shell":    ToolPolicyAllow,
							"workspace_shell_bg": ToolPolicyAsk,
						},
					}},
				},
			}},
		}

		touched := migrateShellToolPolicyKeys(cfg)

		require.True(t, touched)
		policies := cfg.Agents.List[0].Tools.Builtin.Policies
		assert.Equal(t, ToolPolicyAsk, policies["bash"], "the strictest of allow/allow/ask is ask")
		for _, k := range []string{"exec", "workspace_shell", "workspace_shell_bg"} {
			_, has := policies[k]
			assert.False(t, has, "legacy key %q must be gone", k)
		}
	})
}

// TestMigrateShellToolPolicyKeys_ExistingBashKeyIsAuthoritative covers
// dataset row 7: {"bash": "deny", "exec": "allow"} -> bash stays "deny" (the
// already-present value wins, it is NOT recomputed from the legacy key), and
// the stale "exec" key is still deleted as cleanup, not merely ignored.
func TestMigrateShellToolPolicyKeys_ExistingBashKeyIsAuthoritative(t *testing.T) {
	cfg := &Config{Sandbox: OmnipusSandboxConfig{
		ToolPolicies: map[string]string{"bash": "deny", "exec": "allow"},
	}}

	touched := migrateShellToolPolicyKeys(cfg)

	require.True(t, touched, "a stale legacy key alongside an existing bash key still counts as a change (cleanup)")
	assert.Equal(t, "deny", cfg.Sandbox.ToolPolicies["bash"],
		"the pre-existing bash value is authoritative; must not be recomputed as \"allow\" from the stale exec key")
	_, hasExec := cfg.Sandbox.ToolPolicies["exec"]
	assert.False(t, hasExec, "the stale exec key must be deleted as cleanup even though bash already existed")
}

// TestMigrateShellToolPolicyKeys_NoLegacyKeysIsNoOp covers dataset row 4:
// {} (no legacy keys present) -> no bash key is invented anywhere; the
// migration must never create a policy where none existed.
func TestMigrateShellToolPolicyKeys_NoLegacyKeysIsNoOp(t *testing.T) {
	cfg := &Config{
		Sandbox: OmnipusSandboxConfig{ToolPolicies: map[string]string{"search_web": "deny"}},
		Agents: AgentsConfig{List: []AgentConfig{
			{
				ID: "clean-bot",
				Tools: &AgentToolsCfg{Builtin: AgentBuiltinToolsCfg{
					Policies: map[string]ToolPolicy{"read_file": ToolPolicyAllow},
				}},
			},
		}},
	}

	touched := migrateShellToolPolicyKeys(cfg)

	assert.False(t, touched)
	_, hasBash := cfg.Sandbox.ToolPolicies["bash"]
	assert.False(t, hasBash, "no bash key must be invented globally when no legacy key was present")
	_, hasBashAgent := cfg.Agents.List[0].Tools.Builtin.Policies["bash"]
	assert.False(t, hasBashAgent, "no bash key must be invented per-agent when no legacy key was present")
}

// TestMigrateShellToolPolicyKeys_MalformedValueTreatedAsDeny covers dataset
// row 4b: {"exec": "Disabled"} -> bash: deny (fail-safe), never silently
// coerced to "allow", plus a WARN log line naming the offending agent, key,
// and value (FR-M4 / MAJ-005).
func TestMigrateShellToolPolicyKeys_MalformedValueTreatedAsDeny(t *testing.T) {
	buf := captureSlog(t)

	cfg := &Config{
		Agents: AgentsConfig{List: []AgentConfig{
			{
				ID: "sloppy-config-bot",
				Tools: &AgentToolsCfg{Builtin: AgentBuiltinToolsCfg{
					Policies: map[string]ToolPolicy{"exec": "Disabled"},
				}},
			},
		}},
	}

	touched := migrateShellToolPolicyKeys(cfg)

	require.True(t, touched)
	policies := cfg.Agents.List[0].Tools.Builtin.Policies
	assert.Equal(t, ToolPolicyDeny, policies["bash"], "a malformed legacy value must fail-safe to deny, never allow")
	_, hasExec := policies["exec"]
	assert.False(t, hasExec)

	logOutput := buf.String()
	assert.Contains(t, logOutput, "sloppy-config-bot", "WARN must name the offending agent")
	assert.Contains(t, logOutput, "exec", "WARN must name the offending key")
	assert.Contains(t, logOutput, "Disabled", "WARN must name the offending value")
}

// --- Full boot-pipeline (disk round-trip) tests ---

// baseAgentConfigJSON returns a minimal, valid config.json body (matching the
// shape used by the sibling tool_enable_migration_test.go tests) with one
// agent whose tools.builtin.policies carries legacyPoliciesJSON, plus an
// optional global sandbox.tool_policies block.
func shellMigrationFixture(t *testing.T, dir string, agentPoliciesJSON, globalPoliciesJSON string) string {
	t.Helper()
	configPath := filepath.Join(dir, "config.json")
	sandboxSection := ""
	if globalPoliciesJSON != "" {
		sandboxSection = `,"sandbox": {"tool_policies": ` + globalPoliciesJSON + `}`
	}
	raw := `{
		"version": 1,
		"agents": {"defaults": {"workspace": "./workspace"}, "list": [
			{"id": "research-bot", "tools": {"builtin": {"policies": ` + agentPoliciesJSON + `}}}
		]},
		"providers": []` + sandboxSection + `
	}`
	require.NoError(t, os.WriteFile(configPath, []byte(raw), 0o600))
	return configPath
}

// TestMigrateShellToolPolicyKeys_DeletesLegacyKeyAfterConversion (FR-M3):
// after a full boot load, the persisted config.json no longer contains the
// exec/workspace_shell/workspace_shell_bg keys anywhere — per-agent or
// global — proving they were converted, not merely superseded.
func TestMigrateShellToolPolicyKeys_DeletesLegacyKeyAfterConversion(t *testing.T) {
	deprecatedToolEnableMigrateOnce = sync.Once{}
	dir := t.TempDir()
	configPath := shellMigrationFixture(t, dir,
		`{"exec": "deny"}`,
		`{"workspace_shell": "ask"}`,
	)

	cfg, err := LoadConfigWithStoreAndSelfHealHook(configPath, nil, nil)
	require.NoError(t, err)

	// In-memory.
	assert.Equal(t, ToolPolicyDeny, cfg.Agents.List[0].Tools.Builtin.Policies["bash"])
	assert.Equal(t, "ask", cfg.Sandbox.ToolPolicies["bash"])

	// On disk.
	onDisk, readErr := os.ReadFile(configPath)
	require.NoError(t, readErr)
	var m map[string]any
	require.NoError(t, json.Unmarshal(onDisk, &m))

	agentsRaw := m["agents"].(map[string]any)
	list := agentsRaw["list"].([]any)
	require.Len(t, list, 1)
	agentEntry := list[0].(map[string]any)
	agentPolicies := agentEntry["tools"].(map[string]any)["builtin"].(map[string]any)["policies"].(map[string]any)
	assert.Equal(t, "deny", agentPolicies["bash"])
	for _, k := range []string{"exec", "workspace_shell", "workspace_shell_bg"} {
		_, has := agentPolicies[k]
		assert.False(t, has, "agent policies must not retain legacy key %q on disk", k)
	}

	sandboxPolicies := m["sandbox"].(map[string]any)["tool_policies"].(map[string]any)
	assert.Equal(t, "ask", sandboxPolicies["bash"])
	for _, k := range []string{"exec", "workspace_shell", "workspace_shell_bg"} {
		_, has := sandboxPolicies[k]
		assert.False(t, has, "global tool_policies must not retain legacy key %q on disk", k)
	}
}

// TestMigrateShellToolPolicyKeys_WritesBackupBeforeDelete (FR-M4 / CRIT-002):
// a timestamped backup of the pre-migration config.json is written before
// the legacy keys are stripped, and the boot log line naming the migration
// also names the backup file's path.
func TestMigrateShellToolPolicyKeys_WritesBackupBeforeDelete(t *testing.T) {
	deprecatedToolEnableMigrateOnce = sync.Once{}
	dir := t.TempDir()
	configPath := shellMigrationFixture(t, dir, `{"exec": "deny"}`, "")

	preMigrationBytes, err := os.ReadFile(configPath)
	require.NoError(t, err)

	// Capture the boot log via the established file-logging test hook
	// (logger has no exported io.Writer sink for the console logger).
	logFile := filepath.Join(t.TempDir(), "boot.log")
	prevLevel := logger.GetLevel()
	logger.DisableConsole()
	logger.SetLevel(logger.INFO)
	require.NoError(t, logger.EnableFileLogging(logFile))
	t.Cleanup(func() {
		logger.DisableFileLogging()
		logger.SetLevel(prevLevel)
	})

	_, loadErr := LoadConfigWithStoreAndSelfHealHook(configPath, nil, nil)
	require.NoError(t, loadErr)

	// A single "config.json.pre-bash-migration.<ts>.bak" file must exist
	// alongside config.json, containing the untouched pre-migration bytes.
	matches, globErr := filepath.Glob(configPath + ".pre-bash-migration.*.bak")
	require.NoError(t, globErr)
	require.Len(t, matches, 1, "expected exactly one pre-migration backup file")

	backupBytes, readErr := os.ReadFile(matches[0])
	require.NoError(t, readErr)
	assert.Equal(t, string(preMigrationBytes), string(backupBytes),
		"the backup must contain the exact pre-migration bytes")

	// The legacy key must actually be gone post-migration (the deletion this
	// backup exists to make recoverable).
	postMigrationBytes, readErr := os.ReadFile(configPath)
	require.NoError(t, readErr)
	assert.NotEqual(t, string(preMigrationBytes), string(postMigrationBytes))

	logContent, readErr := os.ReadFile(logFile)
	require.NoError(t, readErr)
	assert.Contains(t, string(logContent), filepath.Base(matches[0]),
		"the boot log line must name the backup file's path")
}

// TestMigrateShellToolPolicyKeys_Idempotent (SC-004, relaxed to structural
// comparison per MAJ-006): loading the same config.json twice (a genuine
// second boot against the already-migrated on-disk file) must not change the
// resolved policy on the second load, and must not perform a further
// self-heal write.
func TestMigrateShellToolPolicyKeys_Idempotent(t *testing.T) {
	deprecatedToolEnableMigrateOnce = sync.Once{}
	dir := t.TempDir()
	configPath := shellMigrationFixture(t, dir, `{"exec": "deny"}`, "")

	var firstHookCalls, secondHookCalls int
	cfg1, err := LoadConfigWithStoreAndSelfHealHook(configPath, nil, func([]byte) { firstHookCalls++ })
	require.NoError(t, err)
	require.Equal(t, ToolPolicyDeny, cfg1.Agents.List[0].Tools.Builtin.Policies["bash"])
	assert.Equal(t, 1, firstHookCalls, "the first boot must perform exactly one self-heal write")

	deprecatedToolEnableMigrateOnce = sync.Once{}
	cfg2, err := LoadConfigWithStoreAndSelfHealHook(configPath, nil, func([]byte) { secondHookCalls++ })
	require.NoError(t, err)

	// Structural comparison of resolved policy values (not byte-identical —
	// SC-004 relaxed 2026-07-04, MAJ-006).
	assert.Equal(t, cfg1.Agents.List[0].Tools.Builtin.Policies, cfg2.Agents.List[0].Tools.Builtin.Policies)
	assert.Equal(t, ToolPolicyDeny, cfg2.Agents.List[0].Tools.Builtin.Policies["bash"])
	_, hasExec := cfg2.Agents.List[0].Tools.Builtin.Policies["exec"]
	assert.False(t, hasExec)

	assert.Equal(t, 0, secondHookCalls, "the second boot must find nothing left to migrate and perform no write")

	// No second backup file should have been created either.
	matches, globErr := filepath.Glob(configPath + ".pre-bash-migration.*.bak")
	require.NoError(t, globErr)
	assert.Len(t, matches, 1, "exactly one backup file total, from the first boot only")
}

// TestMigrateDeprecatedToolEnableFlags_ExecFalseStillDenies (FR-M2): the OLD
// tools.exec.enabled=false boolean-flag shape (pre-ToolPolicyCfg) must still
// resolve to bash: deny after migration — the legacy toolEnableToPolicy row
// now targets the "bash" glob, not the retired "exec" name.
func TestMigrateDeprecatedToolEnableFlags_ExecFalseStillDenies(t *testing.T) {
	deprecatedToolEnableMigrateOnce = sync.Once{}
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	raw := `{
		"version": 1,
		"agents": {"defaults": {"workspace": "./workspace"}, "list": []},
		"providers": [],
		"tools": {
			"exec": {"enabled": false}
		}
	}`
	require.NoError(t, os.WriteFile(configPath, []byte(raw), 0o600))

	cfg, err := LoadConfigWithStoreAndSelfHealHook(configPath, nil, nil)
	require.NoError(t, err)

	require.NotNil(t, cfg.Sandbox.ToolPolicies)
	assert.Equal(t, "deny", cfg.Sandbox.ToolPolicies["bash"],
		"legacy tools.exec.enabled=false must resolve to bash: deny, not exec: deny")
	_, hasExec := cfg.Sandbox.ToolPolicies["exec"]
	assert.False(t, hasExec, "no \"exec\" policy entry should exist — the glob is \"bash\" post ADR-036")

	onDisk, readErr := os.ReadFile(configPath)
	require.NoError(t, readErr)
	var m map[string]any
	require.NoError(t, json.Unmarshal(onDisk, &m))
	toolsRaw := m["tools"].(map[string]any)
	execRaw := toolsRaw["exec"].(map[string]any)
	_, hasEnabled := execRaw["enabled"]
	assert.False(t, hasEnabled, "the legacy tools.exec.enabled flag must be removed on disk")

	sandboxRaw := m["sandbox"].(map[string]any)
	policies := sandboxRaw["tool_policies"].(map[string]any)
	assert.Equal(t, "deny", policies["bash"])
}

// TestMigrateShellToolPolicyKeys_NilConfigDoesNotPanic is a defensive guard:
// a nil *Config must not panic the migration (matches the nil-receiver
// pattern used throughout this migration family, e.g.
// AgentBuiltinToolsCfg.ResolvePolicy).
func TestMigrateShellToolPolicyKeys_NilConfigDoesNotPanic(t *testing.T) {
	require.NotPanics(t, func() {
		touched := migrateShellToolPolicyKeys(nil)
		assert.False(t, touched)
	})
}

// TestWriteShellToolPolicyMigrationOnDisk_NoOpWhenNothingToStrip verifies
// writeShellToolPolicyMigrationOnDisk is a true no-op (no write, no backup)
// when the on-disk config carries no legacy shell-tool-policy keys at all.
func TestWriteShellToolPolicyMigrationOnDisk_NoOpWhenNothingToStrip(t *testing.T) {
	dir := t.TempDir()
	configPath := shellMigrationFixture(t, dir, `{"read_file": "allow"}`, "")

	before, err := os.ReadFile(configPath)
	require.NoError(t, err)

	cfg := &Config{Agents: AgentsConfig{List: []AgentConfig{
		{ID: "research-bot", Tools: &AgentToolsCfg{Builtin: AgentBuiltinToolsCfg{
			Policies: map[string]ToolPolicy{"read_file": ToolPolicyAllow},
		}}},
	}}}

	written, backupPath, err := writeShellToolPolicyMigrationOnDisk(cfg, configPath)
	require.NoError(t, err)
	assert.Nil(t, written)
	assert.Empty(t, backupPath)

	after, err := os.ReadFile(configPath)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after))

	matches, globErr := filepath.Glob(configPath + ".pre-bash-migration.*.bak")
	require.NoError(t, globErr)
	assert.Empty(t, matches, "no backup file should be created when there is nothing to strip")
}

// TestPolicyStrictnessRank_OrderingSanity documents and locks the deny > ask
// > allow ordering, including the fail-safe treatment of unrecognized values.
func TestPolicyStrictnessRank_OrderingSanity(t *testing.T) {
	assert.Greater(t, policyStrictnessRank("deny"), policyStrictnessRank("ask"))
	assert.Greater(t, policyStrictnessRank("ask"), policyStrictnessRank("allow"))
	assert.Equal(t, policyStrictnessRank("deny"), policyStrictnessRank("garbage"),
		"an unrecognized value must rank as strictly as deny (fail-safe)")
}

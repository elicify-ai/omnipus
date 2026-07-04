// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// Contract tests for ADR-036's spawn/run_subagent/check_spawn_status ->
// delegate tool-policy migration (docs/internal/specs/agent-delegation-spec.md).
// Traces to: pkg/config/delegate_tool_policy_migration.go,
// pkg/config/legacy_tool_policy_migration.go (shared core, also exercised by
// shell_tool_policy_migration_test.go), pkg/config/migration.go
// (toolEnableToPolicy's {"spawn","delegate"}/{"spawn_status","delegate"}/
// {"subagent","delegate"} rows), pkg/config/config.go (loadConfigInternal
// wiring).

package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Dataset: migration key combinations ---
// Direct, in-memory unit tests against migrateDelegateToolPolicyKeys — fast
// and precise, exercising both scopes (per-agent AgentBuiltinToolsCfg.Policies
// and the global OmnipusSandboxConfig.ToolPolicies) the function must handle.

// TestMigrateDelegateToolPolicyKeys_ExactKeySurvives: a single legacy key
// ({"run_subagent": "deny"}) survives the rename as {"delegate": "deny"},
// with the legacy key gone. Exercised at both scopes.
func TestMigrateDelegateToolPolicyKeys_ExactKeySurvives(t *testing.T) {
	t.Run("PerAgent", func(t *testing.T) {
		cfg := &Config{
			Agents: AgentsConfig{List: []AgentConfig{
				{
					ID: "research-bot",
					Tools: &AgentToolsCfg{Builtin: AgentBuiltinToolsCfg{
						Policies: map[string]ToolPolicy{"run_subagent": ToolPolicyDeny},
					}},
				},
			}},
		}

		touched := migrateDelegateToolPolicyKeys(cfg)

		assert.True(t, touched)
		policies := cfg.Agents.List[0].Tools.Builtin.Policies
		assert.Equal(t, ToolPolicyDeny, policies["delegate"])
		_, hasLegacy := policies["run_subagent"]
		assert.False(t, hasLegacy, "legacy \"run_subagent\" key must be gone after migration")
	})

	t.Run("Global", func(t *testing.T) {
		cfg := &Config{Sandbox: OmnipusSandboxConfig{
			ToolPolicies: map[string]string{"run_subagent": "deny"},
		}}

		touched := migrateDelegateToolPolicyKeys(cfg)

		assert.True(t, touched)
		assert.Equal(t, "deny", cfg.Sandbox.ToolPolicies["delegate"])
		_, hasLegacy := cfg.Sandbox.ToolPolicies["run_subagent"]
		assert.False(t, hasLegacy, "legacy \"run_subagent\" key must be gone after migration")
	})
}

// TestMigrateDelegateToolPolicyKeys_StricterWins covers the 2-key and 3-key
// combinations: contradictory legacy keys resolve to the strictest present
// value (deny > ask > allow).
func TestMigrateDelegateToolPolicyKeys_StricterWins(t *testing.T) {
	t.Run("TwoKeys_AskAndDeny", func(t *testing.T) {
		cfg := &Config{Sandbox: OmnipusSandboxConfig{
			ToolPolicies: map[string]string{"spawn": "ask", "run_subagent": "deny"},
		}}

		touched := migrateDelegateToolPolicyKeys(cfg)

		require.True(t, touched)
		assert.Equal(t, "deny", cfg.Sandbox.ToolPolicies["delegate"])
		for _, k := range []string{"spawn", "run_subagent", "check_spawn_status"} {
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
							"spawn":              ToolPolicyAllow,
							"run_subagent":       ToolPolicyAllow,
							"check_spawn_status": ToolPolicyAsk,
						},
					}},
				},
			}},
		}

		touched := migrateDelegateToolPolicyKeys(cfg)

		require.True(t, touched)
		policies := cfg.Agents.List[0].Tools.Builtin.Policies
		assert.Equal(t, ToolPolicyAsk, policies["delegate"], "the strictest of allow/allow/ask is ask")
		for _, k := range []string{"spawn", "run_subagent", "check_spawn_status"} {
			_, has := policies[k]
			assert.False(t, has, "legacy key %q must be gone", k)
		}
	})
}

// TestMigrateDelegateToolPolicyKeys_ExistingDelegateKeyIsAuthoritative:
// {"delegate": "deny", "spawn": "allow"} -> delegate stays "deny" (the
// already-present value wins, it is NOT recomputed from the legacy key), and
// the stale "spawn" key is still deleted as cleanup, not merely ignored.
func TestMigrateDelegateToolPolicyKeys_ExistingDelegateKeyIsAuthoritative(t *testing.T) {
	cfg := &Config{Sandbox: OmnipusSandboxConfig{
		ToolPolicies: map[string]string{"delegate": "deny", "spawn": "allow"},
	}}

	touched := migrateDelegateToolPolicyKeys(cfg)

	require.True(t, touched, "a stale legacy key alongside an existing delegate key still counts as a change (cleanup)")
	assert.Equal(t, "deny", cfg.Sandbox.ToolPolicies["delegate"],
		"the pre-existing delegate value is authoritative; must not be recomputed as \"allow\" from the stale spawn key")
	_, hasSpawn := cfg.Sandbox.ToolPolicies["spawn"]
	assert.False(t, hasSpawn, "the stale spawn key must be deleted as cleanup even though delegate already existed")
}

// TestMigrateDelegateToolPolicyKeys_NoLegacyKeysIsNoOp: {} (no legacy keys
// present) -> no delegate key is invented anywhere; the migration must never
// create a policy where none existed.
func TestMigrateDelegateToolPolicyKeys_NoLegacyKeysIsNoOp(t *testing.T) {
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

	touched := migrateDelegateToolPolicyKeys(cfg)

	assert.False(t, touched)
	_, hasDelegate := cfg.Sandbox.ToolPolicies["delegate"]
	assert.False(t, hasDelegate, "no delegate key must be invented globally when no legacy key was present")
	_, hasDelegateAgent := cfg.Agents.List[0].Tools.Builtin.Policies["delegate"]
	assert.False(t, hasDelegateAgent, "no delegate key must be invented per-agent when no legacy key was present")
}

// TestMigrateDelegateToolPolicyKeys_MalformedValueTreatedAsDeny:
// {"spawn": "Disabled"} -> delegate: deny (fail-safe), never silently
// coerced to "allow", plus a WARN log line naming the offending agent, key,
// and value.
func TestMigrateDelegateToolPolicyKeys_MalformedValueTreatedAsDeny(t *testing.T) {
	buf := captureSlog(t)

	cfg := &Config{
		Agents: AgentsConfig{List: []AgentConfig{
			{
				ID: "sloppy-config-bot",
				Tools: &AgentToolsCfg{Builtin: AgentBuiltinToolsCfg{
					Policies: map[string]ToolPolicy{"spawn": "Disabled"},
				}},
			},
		}},
	}

	touched := migrateDelegateToolPolicyKeys(cfg)

	require.True(t, touched)
	policies := cfg.Agents.List[0].Tools.Builtin.Policies
	assert.Equal(t, ToolPolicyDeny, policies["delegate"], "a malformed legacy value must fail-safe to deny, never allow")
	_, hasSpawn := policies["spawn"]
	assert.False(t, hasSpawn)

	logOutput := buf.String()
	assert.Contains(t, logOutput, "sloppy-config-bot", "WARN must name the offending agent")
	assert.Contains(t, logOutput, "spawn", "WARN must name the offending key")
	assert.Contains(t, logOutput, "Disabled", "WARN must name the offending value")
}

// --- Full boot-pipeline (disk round-trip) tests ---

// delegateMigrationFixture returns a minimal, valid config.json body with one
// agent whose tools.builtin.policies carries agentPoliciesJSON, plus an
// optional global sandbox.tool_policies block. Mirrors shellMigrationFixture.
func delegateMigrationFixture(t *testing.T, dir string, agentPoliciesJSON, globalPoliciesJSON string) string {
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

// TestMigrateDelegateToolPolicyKeys_DeletesLegacyKeysAfterConversion: after a
// full boot load, the persisted config.json no longer contains the
// spawn/run_subagent/check_spawn_status keys anywhere — per-agent or global
// — proving they were converted, not merely superseded.
func TestMigrateDelegateToolPolicyKeys_DeletesLegacyKeysAfterConversion(t *testing.T) {
	deprecatedToolEnableMigrateOnce = sync.Once{}
	dir := t.TempDir()
	configPath := delegateMigrationFixture(t, dir,
		`{"run_subagent": "deny"}`,
		`{"spawn": "ask"}`,
	)

	cfg, err := LoadConfigWithStoreAndSelfHealHook(configPath, nil, nil)
	require.NoError(t, err)

	// In-memory.
	assert.Equal(t, ToolPolicyDeny, cfg.Agents.List[0].Tools.Builtin.Policies["delegate"])
	assert.Equal(t, "ask", cfg.Sandbox.ToolPolicies["delegate"])

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
	assert.Equal(t, "deny", agentPolicies["delegate"])
	for _, k := range []string{"spawn", "run_subagent", "check_spawn_status"} {
		_, has := agentPolicies[k]
		assert.False(t, has, "agent policies must not retain legacy key %q on disk", k)
	}

	sandboxPolicies := m["sandbox"].(map[string]any)["tool_policies"].(map[string]any)
	assert.Equal(t, "ask", sandboxPolicies["delegate"])
	for _, k := range []string{"spawn", "run_subagent", "check_spawn_status"} {
		_, has := sandboxPolicies[k]
		assert.False(t, has, "global tool_policies must not retain legacy key %q on disk", k)
	}
}

// TestMigrateDelegateToolPolicyKeys_WritesBackupBeforeDelete: a timestamped
// backup of the pre-migration config.json is written before the legacy keys
// are stripped, and the boot log line naming the migration also names the
// backup file's path.
func TestMigrateDelegateToolPolicyKeys_WritesBackupBeforeDelete(t *testing.T) {
	deprecatedToolEnableMigrateOnce = sync.Once{}
	dir := t.TempDir()
	configPath := delegateMigrationFixture(t, dir, `{"spawn": "deny"}`, "")

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

	// A single "config.json.pre-delegate-migration.<ts>.bak" file must exist
	// alongside config.json, containing the untouched pre-migration bytes.
	matches, globErr := filepath.Glob(configPath + ".pre-delegate-migration.*.bak")
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

// TestMigrateDelegateToolPolicyKeys_Idempotent: loading the same config.json
// twice (a genuine second boot against the already-migrated on-disk file)
// must not change the resolved policy on the second load, and must not
// perform a further self-heal write.
func TestMigrateDelegateToolPolicyKeys_Idempotent(t *testing.T) {
	deprecatedToolEnableMigrateOnce = sync.Once{}
	dir := t.TempDir()
	configPath := delegateMigrationFixture(t, dir, `{"spawn": "deny"}`, "")

	var firstHookCalls, secondHookCalls int
	cfg1, err := LoadConfigWithStoreAndSelfHealHook(configPath, nil, func([]byte) { firstHookCalls++ })
	require.NoError(t, err)
	require.Equal(t, ToolPolicyDeny, cfg1.Agents.List[0].Tools.Builtin.Policies["delegate"])
	assert.Equal(t, 1, firstHookCalls, "the first boot must perform exactly one self-heal write")

	deprecatedToolEnableMigrateOnce = sync.Once{}
	cfg2, err := LoadConfigWithStoreAndSelfHealHook(configPath, nil, func([]byte) { secondHookCalls++ })
	require.NoError(t, err)

	// Structural comparison of resolved policy values (not byte-identical).
	assert.Equal(t, cfg1.Agents.List[0].Tools.Builtin.Policies, cfg2.Agents.List[0].Tools.Builtin.Policies)
	assert.Equal(t, ToolPolicyDeny, cfg2.Agents.List[0].Tools.Builtin.Policies["delegate"])
	_, hasSpawn := cfg2.Agents.List[0].Tools.Builtin.Policies["spawn"]
	assert.False(t, hasSpawn)

	assert.Equal(t, 0, secondHookCalls, "the second boot must find nothing left to migrate and perform no write")

	// No second backup file should have been created either.
	matches, globErr := filepath.Glob(configPath + ".pre-delegate-migration.*.bak")
	require.NoError(t, globErr)
	assert.Len(t, matches, 1, "exactly one backup file total, from the first boot only")
}

// TestMigrateDelegateToolPolicyKeys_NilConfigDoesNotPanic is a defensive
// guard: a nil *Config must not panic the migration.
func TestMigrateDelegateToolPolicyKeys_NilConfigDoesNotPanic(t *testing.T) {
	require.NotPanics(t, func() {
		touched := migrateDelegateToolPolicyKeys(nil)
		assert.False(t, touched)
	})
}

// TestWriteDelegateToolPolicyMigrationOnDisk_NoOpWhenNothingToStrip verifies
// writeDelegateToolPolicyMigrationOnDisk is a true no-op (no write, no
// backup) when the on-disk config carries no legacy delegate-tool-policy
// keys at all.
func TestWriteDelegateToolPolicyMigrationOnDisk_NoOpWhenNothingToStrip(t *testing.T) {
	dir := t.TempDir()
	configPath := delegateMigrationFixture(t, dir, `{"read_file": "allow"}`, "")

	before, err := os.ReadFile(configPath)
	require.NoError(t, err)

	cfg := &Config{Agents: AgentsConfig{List: []AgentConfig{
		{ID: "research-bot", Tools: &AgentToolsCfg{Builtin: AgentBuiltinToolsCfg{
			Policies: map[string]ToolPolicy{"read_file": ToolPolicyAllow},
		}}},
	}}}

	written, backupPath, err := writeDelegateToolPolicyMigrationOnDisk(cfg, configPath)
	require.NoError(t, err)
	assert.Nil(t, written)
	assert.Empty(t, backupPath)

	after, err := os.ReadFile(configPath)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after))

	matches, globErr := filepath.Glob(configPath + ".pre-delegate-migration.*.bak")
	require.NoError(t, globErr)
	assert.Empty(t, matches, "no backup file should be created when there is nothing to strip")
}

// TestMigrateDeprecatedToolEnableFlags_SpawnFalseStillDenies: the OLD
// tools.spawn.enabled=false boolean-flag shape (pre-ToolPolicyCfg) must
// still resolve to delegate: deny after migration — the legacy
// toolEnableToPolicy row targets the "delegate" glob, not the retired
// "spawn" name.
func TestMigrateDeprecatedToolEnableFlags_SpawnFalseStillDenies(t *testing.T) {
	deprecatedToolEnableMigrateOnce = sync.Once{}
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	raw := `{
		"version": 1,
		"agents": {"defaults": {"workspace": "./workspace"}, "list": []},
		"providers": [],
		"tools": {
			"spawn": {"enabled": false}
		}
	}`
	require.NoError(t, os.WriteFile(configPath, []byte(raw), 0o600))

	cfg, err := LoadConfigWithStoreAndSelfHealHook(configPath, nil, nil)
	require.NoError(t, err)

	require.NotNil(t, cfg.Sandbox.ToolPolicies)
	assert.Equal(t, "deny", cfg.Sandbox.ToolPolicies["delegate"],
		"legacy tools.spawn.enabled=false must resolve to delegate: deny, not spawn: deny")
	_, hasSpawn := cfg.Sandbox.ToolPolicies["spawn"]
	assert.False(t, hasSpawn, "no \"spawn\" policy entry should exist — the glob is \"delegate\" post ADR-036")

	onDisk, readErr := os.ReadFile(configPath)
	require.NoError(t, readErr)
	var m map[string]any
	require.NoError(t, json.Unmarshal(onDisk, &m))
	toolsRaw := m["tools"].(map[string]any)
	spawnRaw := toolsRaw["spawn"].(map[string]any)
	_, hasEnabled := spawnRaw["enabled"]
	assert.False(t, hasEnabled, "the legacy tools.spawn.enabled flag must be removed on disk")

	sandboxRaw := m["sandbox"].(map[string]any)
	policies := sandboxRaw["tool_policies"].(map[string]any)
	assert.Equal(t, "deny", policies["delegate"])
}

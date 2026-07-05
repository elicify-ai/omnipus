// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// Shared, name-agnostic test scenarios for ADR-036's two legacy-tool-policy-
// key migrations: bash's exec/workspace_shell/workspace_shell_bg
// (shell_tool_policy_migration_test.go) and delegate's spawn/run_subagent/
// check_spawn_status (delegate_tool_policy_migration_test.go). Both
// migrations are thin wrappers around the exact same shared core
// (resolveLegacyPolicyMigration / migrateLegacyToolPolicyKeys /
// writeLegacyToolPolicyMigrationOnDisk in legacy_tool_policy_migration.go),
// parameterized by (legacyKeys []string, newKey string) — so the tests are
// parameterized the same way here, written once and exercised twice, instead
// of copy-pasted per migration (dupl).
//
// Each runXxx function below is one test scenario, called from a
// Test-prefixed function of the same original name in both sibling files so
// `go test -run` and CI dashboards keep exact per-scenario, per-migration
// traceability; only the duplicated bodies are gone.

package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/logger"
)

// legacyPolicyMigrationSuite bundles everything that varies between the bash
// and delegate legacy-tool-policy-key migrations so the shared scenarios
// below can be written once and exercised twice.
type legacyPolicyMigrationSuite struct {
	// NewKey is the consolidated tool-policy key (e.g. "bash", "delegate").
	NewKey string
	// LegacyKeys are the retired keys this migration collapses into NewKey.
	LegacyKeys []string
	// BackupTag names the on-disk backup file
	// ("<path>.pre-<BackupTag>-migration.<unix ts>.bak").
	BackupTag string
	// MigrateFn is the in-memory migration entrypoint under test
	// (migrateShellToolPolicyKeys / migrateDelegateToolPolicyKeys).
	MigrateFn func(cfg *Config) bool
	// WriteFn is the on-disk migration entrypoint under test
	// (writeShellToolPolicyMigrationOnDisk / writeDelegateToolPolicyMigrationOnDisk).
	WriteFn func(cfg *Config, path string) (written []byte, backupPath string, err error)
}

// shellPolicyMigrationSuite parameterizes the shared scenarios for the
// exec/workspace_shell/workspace_shell_bg -> bash migration
// (bash-tool-spec.md User Story 4, FR-M1..FR-M4).
var shellPolicyMigrationSuite = legacyPolicyMigrationSuite{
	NewKey:     "bash",
	LegacyKeys: []string{"exec", "workspace_shell", "workspace_shell_bg"},
	BackupTag:  "bash",
	MigrateFn:  migrateShellToolPolicyKeys,
	WriteFn:    writeShellToolPolicyMigrationOnDisk,
}

// delegatePolicyMigrationSuite parameterizes the shared scenarios for the
// spawn/run_subagent/check_spawn_status -> delegate migration
// (docs/internal/specs/agent-delegation-spec.md).
var delegatePolicyMigrationSuite = legacyPolicyMigrationSuite{
	NewKey:     "delegate",
	LegacyKeys: []string{"spawn", "run_subagent", "check_spawn_status"},
	BackupTag:  "delegate",
	MigrateFn:  migrateDelegateToolPolicyKeys,
	WriteFn:    writeDelegateToolPolicyMigrationOnDisk,
}

// legacyPolicyMigrationFixture returns a minimal, valid config.json body with
// one agent ("research-bot") whose tools.builtin.policies carries
// agentPoliciesJSON, plus an optional global sandbox.tool_policies block.
// Shared by both migration suites (previously duplicated as
// shellMigrationFixture / delegateMigrationFixture).
func legacyPolicyMigrationFixture(t *testing.T, dir string, agentPoliciesJSON, globalPoliciesJSON string) string {
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

// runExactKeySurvives: a single legacy key (s.LegacyKeys[0]) survives the
// rename as {s.NewKey: "deny"}, with the legacy key gone. Exercised at both
// scopes.
func runExactKeySurvives(t *testing.T, s legacyPolicyMigrationSuite) {
	legacyKey := s.LegacyKeys[0]

	t.Run("PerAgent", func(t *testing.T) {
		cfg := &Config{
			Agents: AgentsConfig{List: []AgentConfig{
				{
					ID: "research-bot",
					Tools: &AgentToolsCfg{Builtin: AgentBuiltinToolsCfg{
						Policies: map[string]ToolPolicy{legacyKey: ToolPolicyDeny},
					}},
				},
			}},
		}

		touched := s.MigrateFn(cfg)

		assert.True(t, touched)
		policies := cfg.Agents.List[0].Tools.Builtin.Policies
		assert.Equal(t, ToolPolicyDeny, policies[s.NewKey])
		_, hasLegacy := policies[legacyKey]
		assert.False(t, hasLegacy, "legacy %q key must be gone after migration", legacyKey)
	})

	t.Run("Global", func(t *testing.T) {
		cfg := &Config{Sandbox: OmnipusSandboxConfig{
			ToolPolicies: map[string]string{legacyKey: "deny"},
		}}

		touched := s.MigrateFn(cfg)

		assert.True(t, touched)
		assert.Equal(t, "deny", cfg.Sandbox.ToolPolicies[s.NewKey])
		_, hasLegacy := cfg.Sandbox.ToolPolicies[legacyKey]
		assert.False(t, hasLegacy, "legacy %q key must be gone after migration", legacyKey)
	})
}

// runStricterWins covers the 2-key and 3-key combinations: contradictory
// legacy keys resolve to the strictest present value (deny > ask > allow).
func runStricterWins(t *testing.T, s legacyPolicyMigrationSuite) {
	t.Run("TwoKeys_AskAndDeny", func(t *testing.T) {
		cfg := &Config{Sandbox: OmnipusSandboxConfig{
			ToolPolicies: map[string]string{s.LegacyKeys[0]: "ask", s.LegacyKeys[1]: "deny"},
		}}

		touched := s.MigrateFn(cfg)

		require.True(t, touched)
		assert.Equal(t, "deny", cfg.Sandbox.ToolPolicies[s.NewKey])
		for _, k := range s.LegacyKeys {
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
							s.LegacyKeys[0]: ToolPolicyAllow,
							s.LegacyKeys[1]: ToolPolicyAllow,
							s.LegacyKeys[2]: ToolPolicyAsk,
						},
					}},
				},
			}},
		}

		touched := s.MigrateFn(cfg)

		require.True(t, touched)
		policies := cfg.Agents.List[0].Tools.Builtin.Policies
		assert.Equal(t, ToolPolicyAsk, policies[s.NewKey], "the strictest of allow/allow/ask is ask")
		for _, k := range s.LegacyKeys {
			_, has := policies[k]
			assert.False(t, has, "legacy key %q must be gone", k)
		}
	})
}

// runExistingKeyIsAuthoritative: {s.NewKey: "deny", s.LegacyKeys[0]: "allow"}
// -> s.NewKey stays "deny" (the already-present value wins, it is NOT
// recomputed from the legacy key), and the stale legacy key is still deleted
// as cleanup, not merely ignored.
func runExistingKeyIsAuthoritative(t *testing.T, s legacyPolicyMigrationSuite) {
	legacyKey := s.LegacyKeys[0]
	cfg := &Config{Sandbox: OmnipusSandboxConfig{
		ToolPolicies: map[string]string{s.NewKey: "deny", legacyKey: "allow"},
	}}

	touched := s.MigrateFn(cfg)

	require.True(
		t,
		touched,
		"a stale legacy key alongside an existing new-key value still counts as a change (cleanup)",
	)
	assert.Equal(t, "deny", cfg.Sandbox.ToolPolicies[s.NewKey],
		"the pre-existing new-key value is authoritative; must not be recomputed from the stale legacy key")
	_, hasLegacy := cfg.Sandbox.ToolPolicies[legacyKey]
	assert.False(
		t,
		hasLegacy,
		"the stale legacy key must be deleted as cleanup even though the new key already existed",
	)
}

// runNoLegacyKeysIsNoOp: {} (no legacy keys present) -> no new key is
// invented anywhere; the migration must never create a policy where none
// existed.
func runNoLegacyKeysIsNoOp(t *testing.T, s legacyPolicyMigrationSuite) {
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

	touched := s.MigrateFn(cfg)

	assert.False(t, touched)
	_, hasNewKey := cfg.Sandbox.ToolPolicies[s.NewKey]
	assert.False(t, hasNewKey, "no %q key must be invented globally when no legacy key was present", s.NewKey)
	_, hasNewKeyAgent := cfg.Agents.List[0].Tools.Builtin.Policies[s.NewKey]
	assert.False(t, hasNewKeyAgent, "no %q key must be invented per-agent when no legacy key was present", s.NewKey)
}

// runMalformedValueTreatedAsDeny: {s.LegacyKeys[0]: "Disabled"} -> new key:
// deny (fail-safe), never silently coerced to "allow", plus a WARN log line
// naming the offending agent, key, and value.
func runMalformedValueTreatedAsDeny(t *testing.T, s legacyPolicyMigrationSuite) {
	buf := captureSlog(t)
	legacyKey := s.LegacyKeys[0]

	cfg := &Config{
		Agents: AgentsConfig{List: []AgentConfig{
			{
				ID: "sloppy-config-bot",
				Tools: &AgentToolsCfg{Builtin: AgentBuiltinToolsCfg{
					Policies: map[string]ToolPolicy{legacyKey: "Disabled"},
				}},
			},
		}},
	}

	touched := s.MigrateFn(cfg)

	require.True(t, touched)
	policies := cfg.Agents.List[0].Tools.Builtin.Policies
	assert.Equal(t, ToolPolicyDeny, policies[s.NewKey], "a malformed legacy value must fail-safe to deny, never allow")
	_, hasLegacy := policies[legacyKey]
	assert.False(t, hasLegacy)

	logOutput := buf.String()
	assert.Contains(t, logOutput, "sloppy-config-bot", "WARN must name the offending agent")
	assert.Contains(t, logOutput, legacyKey, "WARN must name the offending key")
	assert.Contains(t, logOutput, "Disabled", "WARN must name the offending value")
}

// runDeletesLegacyKeysAfterConversion: after a full boot load, the persisted
// config.json no longer contains any of s.LegacyKeys anywhere — per-agent or
// global — proving they were converted, not merely superseded.
func runDeletesLegacyKeysAfterConversion(t *testing.T, s legacyPolicyMigrationSuite) {
	deprecatedToolEnableMigrateOnce = sync.Once{}
	dir := t.TempDir()
	configPath := legacyPolicyMigrationFixture(t, dir,
		`{"`+s.LegacyKeys[0]+`": "deny"}`,
		`{"`+s.LegacyKeys[1]+`": "ask"}`,
	)

	cfg, err := LoadConfigWithStoreAndSelfHealHook(configPath, nil, nil)
	require.NoError(t, err)

	// In-memory.
	assert.Equal(t, ToolPolicyDeny, cfg.Agents.List[0].Tools.Builtin.Policies[s.NewKey])
	assert.Equal(t, "ask", cfg.Sandbox.ToolPolicies[s.NewKey])

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
	assert.Equal(t, "deny", agentPolicies[s.NewKey])
	for _, k := range s.LegacyKeys {
		_, has := agentPolicies[k]
		assert.False(t, has, "agent policies must not retain legacy key %q on disk", k)
	}

	sandboxPolicies := m["sandbox"].(map[string]any)["tool_policies"].(map[string]any)
	assert.Equal(t, "ask", sandboxPolicies[s.NewKey])
	for _, k := range s.LegacyKeys {
		_, has := sandboxPolicies[k]
		assert.False(t, has, "global tool_policies must not retain legacy key %q on disk", k)
	}
}

// runWritesBackupBeforeDelete: a timestamped backup of the pre-migration
// config.json is written before the legacy keys are stripped, and the boot
// log line naming the migration also names the backup file's path.
func runWritesBackupBeforeDelete(t *testing.T, s legacyPolicyMigrationSuite) {
	deprecatedToolEnableMigrateOnce = sync.Once{}
	dir := t.TempDir()
	configPath := legacyPolicyMigrationFixture(t, dir, `{"`+s.LegacyKeys[0]+`": "deny"}`, "")

	preMigrationBytes, err := os.ReadFile(configPath)
	require.NoError(t, err)

	// Capture the boot log via the established file-logging test hook (logger
	// has no exported io.Writer sink for the console logger).
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

	// A single "config.json.pre-<BackupTag>-migration.<ts>.bak" file must
	// exist alongside config.json, containing the untouched pre-migration
	// bytes.
	matches, globErr := filepath.Glob(configPath + ".pre-" + s.BackupTag + "-migration.*.bak")
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

// runIdempotent: loading the same config.json twice (a genuine second boot
// against the already-migrated on-disk file) must not change the resolved
// policy on the second load, and must not perform a further self-heal write.
func runIdempotent(t *testing.T, s legacyPolicyMigrationSuite) {
	deprecatedToolEnableMigrateOnce = sync.Once{}
	dir := t.TempDir()
	configPath := legacyPolicyMigrationFixture(t, dir, `{"`+s.LegacyKeys[0]+`": "deny"}`, "")

	var firstHookCalls, secondHookCalls int
	cfg1, err := LoadConfigWithStoreAndSelfHealHook(configPath, nil, func([]byte) { firstHookCalls++ })
	require.NoError(t, err)
	require.Equal(t, ToolPolicyDeny, cfg1.Agents.List[0].Tools.Builtin.Policies[s.NewKey])
	assert.Equal(t, 1, firstHookCalls, "the first boot must perform exactly one self-heal write")

	deprecatedToolEnableMigrateOnce = sync.Once{}
	cfg2, err := LoadConfigWithStoreAndSelfHealHook(configPath, nil, func([]byte) { secondHookCalls++ })
	require.NoError(t, err)

	// Structural comparison of resolved policy values (not byte-identical —
	// SC-004 relaxed 2026-07-04, MAJ-006).
	assert.Equal(t, cfg1.Agents.List[0].Tools.Builtin.Policies, cfg2.Agents.List[0].Tools.Builtin.Policies)
	assert.Equal(t, ToolPolicyDeny, cfg2.Agents.List[0].Tools.Builtin.Policies[s.NewKey])
	_, hasLegacy := cfg2.Agents.List[0].Tools.Builtin.Policies[s.LegacyKeys[0]]
	assert.False(t, hasLegacy)

	assert.Equal(t, 0, secondHookCalls, "the second boot must find nothing left to migrate and perform no write")

	// No second backup file should have been created either.
	matches, globErr := filepath.Glob(configPath + ".pre-" + s.BackupTag + "-migration.*.bak")
	require.NoError(t, globErr)
	assert.Len(t, matches, 1, "exactly one backup file total, from the first boot only")
}

// runNilConfigDoesNotPanic is a defensive guard: a nil *Config must not panic
// the migration.
func runNilConfigDoesNotPanic(t *testing.T, s legacyPolicyMigrationSuite) {
	require.NotPanics(t, func() {
		touched := s.MigrateFn(nil)
		assert.False(t, touched)
	})
}

// runWriteOnDiskNoOpWhenNothingToStrip verifies s.WriteFn is a true no-op (no
// write, no backup) when the on-disk config carries no legacy tool-policy
// keys at all.
func runWriteOnDiskNoOpWhenNothingToStrip(t *testing.T, s legacyPolicyMigrationSuite) {
	dir := t.TempDir()
	configPath := legacyPolicyMigrationFixture(t, dir, `{"read_file": "allow"}`, "")

	before, err := os.ReadFile(configPath)
	require.NoError(t, err)

	cfg := &Config{Agents: AgentsConfig{List: []AgentConfig{
		{ID: "research-bot", Tools: &AgentToolsCfg{Builtin: AgentBuiltinToolsCfg{
			Policies: map[string]ToolPolicy{"read_file": ToolPolicyAllow},
		}}},
	}}}

	written, backupPath, err := s.WriteFn(cfg, configPath)
	require.NoError(t, err)
	assert.Nil(t, written)
	assert.Empty(t, backupPath)

	after, err := os.ReadFile(configPath)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after))

	matches, globErr := filepath.Glob(configPath + ".pre-" + s.BackupTag + "-migration.*.bak")
	require.NoError(t, globErr)
	assert.Empty(t, matches, "no backup file should be created when there is nothing to strip")
}

// runDeprecatedToolEnableFlagStillDenies: the OLD tools.<legacy>.enabled=false
// boolean-flag shape (pre-ToolPolicyCfg) must still resolve to
// {s.NewKey: deny} after migration — the legacy toolEnableToPolicy row
// targets the consolidated glob, not the retired legacy name.
func runDeprecatedToolEnableFlagStillDenies(t *testing.T, s legacyPolicyMigrationSuite) {
	deprecatedToolEnableMigrateOnce = sync.Once{}
	legacyKey := s.LegacyKeys[0]
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	raw := `{
		"version": 1,
		"agents": {"defaults": {"workspace": "./workspace"}, "list": []},
		"providers": [],
		"tools": {
			"` + legacyKey + `": {"enabled": false}
		}
	}`
	require.NoError(t, os.WriteFile(configPath, []byte(raw), 0o600))

	cfg, err := LoadConfigWithStoreAndSelfHealHook(configPath, nil, nil)
	require.NoError(t, err)

	require.NotNil(t, cfg.Sandbox.ToolPolicies)
	assert.Equal(t, "deny", cfg.Sandbox.ToolPolicies[s.NewKey],
		"legacy tools.%s.enabled=false must resolve to %s: deny, not %s: deny", legacyKey, s.NewKey, legacyKey)
	_, hasLegacy := cfg.Sandbox.ToolPolicies[legacyKey]
	assert.False(t, hasLegacy, "no %q policy entry should exist — the glob is %q post ADR-036", legacyKey, s.NewKey)

	onDisk, readErr := os.ReadFile(configPath)
	require.NoError(t, readErr)
	var m map[string]any
	require.NoError(t, json.Unmarshal(onDisk, &m))
	toolsRaw := m["tools"].(map[string]any)
	legacyRaw := toolsRaw[legacyKey].(map[string]any)
	_, hasEnabled := legacyRaw["enabled"]
	assert.False(t, hasEnabled, "the legacy tools.%s.enabled flag must be removed on disk", legacyKey)

	sandboxRaw := m["sandbox"].(map[string]any)
	policies := sandboxRaw["tool_policies"].(map[string]any)
	assert.Equal(t, "deny", policies[s.NewKey])
}

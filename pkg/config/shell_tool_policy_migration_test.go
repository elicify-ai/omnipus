// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// Contract tests for ADR-036's exec/workspace_shell/workspace_shell_bg -> bash
// tool-policy migration (bash-tool-spec.md User Story 4, FR-M1..FR-M4).
// Traces to: pkg/config/shell_tool_policy_migration.go, pkg/config/migration.go
// (toolEnableToPolicy's {"exec","bash"} row, FR-M2), pkg/config/config.go
// (loadConfigInternal wiring).
//
// Most scenario bodies here are shared with
// delegate_tool_policy_migration_test.go (both migrations are thin wrappers
// over the exact same generalized core) — see
// legacy_tool_policy_migration_shared_test.go for the parameterized
// implementations; this file wires shellPolicyMigrationSuite into each named
// scenario so `go test -run` and CI dashboards keep exact per-migration
// traceability. TestMigrateShellToolPolicyKeys_SingleKeyAskAndAllow and
// TestPolicyStrictnessRank_OrderingSanity have no delegate counterpart and
// stay bash-specific.

package config

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureSlog redirects slog's default logger to a buffer for the duration of
// the test, restoring the previous default on cleanup. Mirrors the pattern
// used by TestMigrateDeprecatedToolEnableFlags_MigrateOnce
// (deprecation_warn_once_test.go). Shared with
// legacy_tool_policy_migration_shared_test.go's runMalformedValueTreatedAsDeny.
func captureSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	handler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})
	oldDefault := slog.Default()
	slog.SetDefault(slog.New(handler))
	t.Cleanup(func() { slog.SetDefault(oldDefault) })
	return &buf
}

// TestMigrateShellToolPolicyKeys_ExecDenySurvives covers dataset row 1:
// {"exec": "deny"} -> bash: deny, exec key gone. Exercised at both scopes.
func TestMigrateShellToolPolicyKeys_ExecDenySurvives(t *testing.T) {
	runExactKeySurvives(t, shellPolicyMigrationSuite)
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
//
// No delegate counterpart — bash-specific coverage.
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
	runStricterWins(t, shellPolicyMigrationSuite)
}

// TestMigrateShellToolPolicyKeys_ExistingBashKeyIsAuthoritative covers
// dataset row 7: {"bash": "deny", "exec": "allow"} -> bash stays "deny" (the
// already-present value wins, it is NOT recomputed from the legacy key), and
// the stale "exec" key is still deleted as cleanup, not merely ignored.
func TestMigrateShellToolPolicyKeys_ExistingBashKeyIsAuthoritative(t *testing.T) {
	runExistingKeyIsAuthoritative(t, shellPolicyMigrationSuite)
}

// TestMigrateShellToolPolicyKeys_NoLegacyKeysIsNoOp covers dataset row 4:
// {} (no legacy keys present) -> no bash key is invented anywhere; the
// migration must never create a policy where none existed.
func TestMigrateShellToolPolicyKeys_NoLegacyKeysIsNoOp(t *testing.T) {
	runNoLegacyKeysIsNoOp(t, shellPolicyMigrationSuite)
}

// TestMigrateShellToolPolicyKeys_MalformedValueTreatedAsDeny covers dataset
// row 4b: {"exec": "Disabled"} -> bash: deny (fail-safe), never silently
// coerced to "allow", plus a WARN log line naming the offending agent, key,
// and value (FR-M4 / MAJ-005).
func TestMigrateShellToolPolicyKeys_MalformedValueTreatedAsDeny(t *testing.T) {
	runMalformedValueTreatedAsDeny(t, shellPolicyMigrationSuite)
}

// TestMigrateShellToolPolicyKeys_DeletesLegacyKeyAfterConversion (FR-M3):
// after a full boot load, the persisted config.json no longer contains the
// exec/workspace_shell/workspace_shell_bg keys anywhere — per-agent or
// global — proving they were converted, not merely superseded.
func TestMigrateShellToolPolicyKeys_DeletesLegacyKeyAfterConversion(t *testing.T) {
	runDeletesLegacyKeysAfterConversion(t, shellPolicyMigrationSuite)
}

// TestMigrateShellToolPolicyKeys_WritesBackupBeforeDelete (FR-M4 / CRIT-002):
// a timestamped backup of the pre-migration config.json is written before
// the legacy keys are stripped, and the boot log line naming the migration
// also names the backup file's path.
func TestMigrateShellToolPolicyKeys_WritesBackupBeforeDelete(t *testing.T) {
	runWritesBackupBeforeDelete(t, shellPolicyMigrationSuite)
}

// TestMigrateShellToolPolicyKeys_Idempotent (SC-004, relaxed to structural
// comparison per MAJ-006): loading the same config.json twice (a genuine
// second boot against the already-migrated on-disk file) must not change the
// resolved policy on the second load, and must not perform a further
// self-heal write.
func TestMigrateShellToolPolicyKeys_Idempotent(t *testing.T) {
	runIdempotent(t, shellPolicyMigrationSuite)
}

// TestMigrateDeprecatedToolEnableFlags_ExecFalseStillDenies (FR-M2): the OLD
// tools.exec.enabled=false boolean-flag shape (pre-ToolPolicyCfg) must still
// resolve to bash: deny after migration — the legacy toolEnableToPolicy row
// now targets the "bash" glob, not the retired "exec" name.
func TestMigrateDeprecatedToolEnableFlags_ExecFalseStillDenies(t *testing.T) {
	runDeprecatedToolEnableFlagStillDenies(t, shellPolicyMigrationSuite)
}

// TestMigrateShellToolPolicyKeys_NilConfigDoesNotPanic is a defensive guard:
// a nil *Config must not panic the migration (matches the nil-receiver
// pattern used throughout this migration family, e.g.
// AgentBuiltinToolsCfg.ResolvePolicy).
func TestMigrateShellToolPolicyKeys_NilConfigDoesNotPanic(t *testing.T) {
	runNilConfigDoesNotPanic(t, shellPolicyMigrationSuite)
}

// TestWriteShellToolPolicyMigrationOnDisk_NoOpWhenNothingToStrip verifies
// writeShellToolPolicyMigrationOnDisk is a true no-op (no write, no backup)
// when the on-disk config carries no legacy shell-tool-policy keys at all.
func TestWriteShellToolPolicyMigrationOnDisk_NoOpWhenNothingToStrip(t *testing.T) {
	runWriteOnDiskNoOpWhenNothingToStrip(t, shellPolicyMigrationSuite)
}

// TestPolicyStrictnessRank_OrderingSanity documents and locks the deny > ask
// > allow ordering, including the fail-safe treatment of unrecognized values.
//
// No delegate counterpart — exercises the shared policyStrictnessRank
// function directly and only needs to run once.
func TestPolicyStrictnessRank_OrderingSanity(t *testing.T) {
	assert.Greater(t, policyStrictnessRank("deny"), policyStrictnessRank("ask"))
	assert.Greater(t, policyStrictnessRank("ask"), policyStrictnessRank("allow"))
	assert.Equal(t, policyStrictnessRank("deny"), policyStrictnessRank("garbage"),
		"an unrecognized value must rank as strictly as deny (fail-safe)")
}

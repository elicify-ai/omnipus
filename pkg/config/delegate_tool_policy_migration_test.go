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
//
// The scenario bodies here are shared with shell_tool_policy_migration_test.go
// (both migrations are thin wrappers over the exact same generalized core) —
// see legacy_tool_policy_migration_shared_test.go for the parameterized
// implementations; this file just wires delegatePolicyMigrationSuite into
// each named scenario so `go test -run` and CI dashboards keep exact
// per-migration traceability.

package config

import (
	"testing"
)

// TestMigrateDelegateToolPolicyKeys_ExactKeySurvives: a single legacy key
// ({"run_subagent": "deny"}) survives the rename as {"delegate": "deny"},
// with the legacy key gone. Exercised at both scopes.
func TestMigrateDelegateToolPolicyKeys_ExactKeySurvives(t *testing.T) {
	runExactKeySurvives(t, delegatePolicyMigrationSuite)
}

// TestMigrateDelegateToolPolicyKeys_StricterWins covers the 2-key and 3-key
// combinations: contradictory legacy keys resolve to the strictest present
// value (deny > ask > allow).
func TestMigrateDelegateToolPolicyKeys_StricterWins(t *testing.T) {
	runStricterWins(t, delegatePolicyMigrationSuite)
}

// TestMigrateDelegateToolPolicyKeys_ExistingDelegateKeyIsAuthoritative:
// {"delegate": "deny", "spawn": "allow"} -> delegate stays "deny" (the
// already-present value wins, it is NOT recomputed from the legacy key), and
// the stale "spawn" key is still deleted as cleanup, not merely ignored.
func TestMigrateDelegateToolPolicyKeys_ExistingDelegateKeyIsAuthoritative(t *testing.T) {
	runExistingKeyIsAuthoritative(t, delegatePolicyMigrationSuite)
}

// TestMigrateDelegateToolPolicyKeys_NoLegacyKeysIsNoOp: {} (no legacy keys
// present) -> no delegate key is invented anywhere; the migration must never
// create a policy where none existed.
func TestMigrateDelegateToolPolicyKeys_NoLegacyKeysIsNoOp(t *testing.T) {
	runNoLegacyKeysIsNoOp(t, delegatePolicyMigrationSuite)
}

// TestMigrateDelegateToolPolicyKeys_MalformedValueTreatedAsDeny:
// {"spawn": "Disabled"} -> delegate: deny (fail-safe), never silently
// coerced to "allow", plus a WARN log line naming the offending agent, key,
// and value.
func TestMigrateDelegateToolPolicyKeys_MalformedValueTreatedAsDeny(t *testing.T) {
	runMalformedValueTreatedAsDeny(t, delegatePolicyMigrationSuite)
}

// TestMigrateDelegateToolPolicyKeys_DeletesLegacyKeysAfterConversion: after a
// full boot load, the persisted config.json no longer contains the
// spawn/run_subagent/check_spawn_status keys anywhere — per-agent or global
// — proving they were converted, not merely superseded.
func TestMigrateDelegateToolPolicyKeys_DeletesLegacyKeysAfterConversion(t *testing.T) {
	runDeletesLegacyKeysAfterConversion(t, delegatePolicyMigrationSuite)
}

// TestMigrateDelegateToolPolicyKeys_WritesBackupBeforeDelete: a timestamped
// backup of the pre-migration config.json is written before the legacy keys
// are stripped, and the boot log line naming the migration also names the
// backup file's path.
func TestMigrateDelegateToolPolicyKeys_WritesBackupBeforeDelete(t *testing.T) {
	runWritesBackupBeforeDelete(t, delegatePolicyMigrationSuite)
}

// TestMigrateDelegateToolPolicyKeys_Idempotent: loading the same config.json
// twice (a genuine second boot against the already-migrated on-disk file)
// must not change the resolved policy on the second load, and must not
// perform a further self-heal write.
func TestMigrateDelegateToolPolicyKeys_Idempotent(t *testing.T) {
	runIdempotent(t, delegatePolicyMigrationSuite)
}

// TestMigrateDelegateToolPolicyKeys_NilConfigDoesNotPanic is a defensive
// guard: a nil *Config must not panic the migration.
func TestMigrateDelegateToolPolicyKeys_NilConfigDoesNotPanic(t *testing.T) {
	runNilConfigDoesNotPanic(t, delegatePolicyMigrationSuite)
}

// TestWriteDelegateToolPolicyMigrationOnDisk_NoOpWhenNothingToStrip verifies
// writeDelegateToolPolicyMigrationOnDisk is a true no-op (no write, no
// backup) when the on-disk config carries no legacy delegate-tool-policy
// keys at all.
func TestWriteDelegateToolPolicyMigrationOnDisk_NoOpWhenNothingToStrip(t *testing.T) {
	runWriteOnDiskNoOpWhenNothingToStrip(t, delegatePolicyMigrationSuite)
}

// TestMigrateDeprecatedToolEnableFlags_SpawnFalseStillDenies: the OLD
// tools.spawn.enabled=false boolean-flag shape (pre-ToolPolicyCfg) must
// still resolve to delegate: deny after migration — the legacy
// toolEnableToPolicy row targets the "delegate" glob, not the retired
// "spawn" name.
func TestMigrateDeprecatedToolEnableFlags_SpawnFalseStillDenies(t *testing.T) {
	runDeprecatedToolEnableFlagStillDenies(t, delegatePolicyMigrationSuite)
}

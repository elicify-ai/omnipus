// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// ADR-036 spawn/run_subagent/check_spawn_status -> delegate
// tool-policy key migration (docs/internal/specs/agent-delegation-spec.md).
//
// ADR-036 consolidated three separate tools (spawn, run_subagent,
// check_spawn_status) into one ("delegate"), registered elsewhere
// (pkg/tools/delegate.go). This file is purely the CONFIG side of that
// consolidation: any operator's persisted tool-policy entry for one of the
// three retired names — per-agent (AgentBuiltinToolsCfg.Policies) or global
// (OmnipusSandboxConfig.ToolPolicies) — must survive the rename as the
// equivalent "delegate" entry, taking the strictest value when more than one
// of the three legacy keys is present (deny > ask > allow), and the legacy
// keys themselves must not linger afterward (no permanent dual-key backward
// compatibility).
//
// Without this migration, an operator who explicitly denied
// spawn/run_subagent/check_spawn_status for a specific agent (a normal,
// UI-reachable pre-ADR-036 configuration) would have that key silently stop
// matching after upgrade and fall through to the compositor's default
// (typically "allow") — an explicit deny silently becoming an allow. Note
// this is DISTINCT from migration.go's toolEnableToPolicy table: that table
// only migrates the legacy BOOLEAN tools.<name>.enabled=false flag shape;
// this file migrates the newer ToolPolicyCfg-shape map entries
// (tool_policies: {"run_subagent": "deny"}), which is the shape an operator
// actually produces via the Settings UI or a direct PUT /api/v1/agents/{id}.
//
// The actual migration logic is shared (parameterized by legacyKeys/newKey)
// with the sibling exec/workspace_shell/workspace_shell_bg -> bash migration
// in shell_tool_policy_migration.go — see legacy_tool_policy_migration.go for
// the common core.
//
// Call-site ordering (loadConfigInternal, config.go): this migration MUST
// run BEFORE migrateDeprecatedToolEnableFlags, for the same reason the bash
// migration must: migration.go's toolEnableToPolicy table has
// {"spawn","delegate"}/{"spawn_status","delegate"}/{"subagent","delegate"}
// rows whose no-clobber guard only checks for a pre-existing entry under the
// "delegate" glob — running this migration first ensures a real
// operator-set spawn/run_subagent/check_spawn_status policy value is already
// sitting under "delegate" by the time the boolean-flag migration looks, so
// it is never downgraded to "deny".

package config

// delegateLegacyPolicyKeys lists the spawn/run_subagent/check_spawn_status
// tool-policy keys ADR-036 consolidates into delegatePolicyKey. Order
// carries no meaning here — strictness is resolved by policyStrictnessRank,
// not by position in this slice.
var delegateLegacyPolicyKeys = []string{"spawn", "run_subagent", "check_spawn_status"}

// delegatePolicyKey is the ADR-036 consolidated tool-policy key that
// spawn/run_subagent/check_spawn_status are migrated into.
const delegatePolicyKey = "delegate"

// migrateDelegateToolPolicyKeys implements the in-memory half: for the
// global OmnipusSandboxConfig.ToolPolicies map and every agent's
// AgentBuiltinToolsCfg.Policies map, converts any present
// spawn/run_subagent/check_spawn_status entry into a "delegate" entry
// (taking the strictest value when more than one legacy key is present) and
// deletes the legacy keys from the in-memory map.
//
// Idempotent: a config with no legacy keys anywhere is a complete no-op
// (returns false, mutates nothing) — running this twice in a row on the same
// cfg produces no further change on the second call, since by then the
// legacy keys are already gone.
//
// Returns true if anything was converted/deleted anywhere (global or any
// agent), signaling the caller (loadConfigInternal) that the on-disk phase
// (writeDelegateToolPolicyMigrationOnDisk) needs to run.
//
// Thin wrapper over the shared migrateLegacyToolPolicyKeys
// (legacy_tool_policy_migration.go).
func migrateDelegateToolPolicyKeys(cfg *Config) bool {
	return migrateLegacyToolPolicyKeys(cfg, delegateLegacyPolicyKeys, delegatePolicyKey)
}

// writeDelegateToolPolicyMigrationOnDisk implements the on-disk half:
// re-reads config.json at path, and for the global sandbox.tool_policies map
// and every agents.list[].tools.builtin.policies map that still carries a
// legacy spawn/run_subagent/check_spawn_status key on disk, writes a
// timestamped backup ("<path>.pre-delegate-migration.<unix ts>.bak") of the
// untouched pre-migration bytes, then persists the same "delegate" value cfg
// already holds in memory (set by migrateDelegateToolPolicyKeys) and deletes
// the legacy keys.
//
// Returns (nil, "", nil) — a true no-op, no write performed — when there is
// nothing left to strip on disk (e.g. a concurrent load already self-healed
// the file). On a successful write, returns the exact bytes written to
// config.json (never nil) and the absolute path of the backup file, which
// was written before the strip and must be named in the caller's boot-time
// log line.
//
// Best-effort by design, matching writeShellToolPolicyMigrationOnDisk: on
// any read/write/parse failure the caller logs a warning and leaves the
// legacy shape on disk. The in-memory cfg is already correct regardless (the
// derived "delegate" policy was set by migrateDelegateToolPolicyKeys before
// this ever runs), so runtime enforcement via the policy engine is
// unaffected by a failed self-heal.
//
// Thin wrapper over the shared writeLegacyToolPolicyMigrationOnDisk
// (legacy_tool_policy_migration.go).
func writeDelegateToolPolicyMigrationOnDisk(cfg *Config, path string) (written []byte, backupPath string, err error) {
	return writeLegacyToolPolicyMigrationOnDisk(cfg, path, delegateLegacyPolicyKeys, delegatePolicyKey, "delegate")
}

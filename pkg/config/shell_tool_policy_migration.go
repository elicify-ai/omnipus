// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// ADR-036 exec/workspace_shell/workspace_shell_bg -> bash
// tool-policy key migration (bash-tool-spec.md User Story 4, FR-M1..FR-M4).
//
// ADR-036 consolidates three separate tools (exec, workspace_shell,
// workspace_shell_bg) into one ("bash"), registered elsewhere. This file is
// purely the CONFIG side of that consolidation: any operator's persisted
// tool-policy entry for one of the three retired names — per-agent
// (AgentBuiltinToolsCfg.Policies) or global (OmnipusSandboxConfig.
// ToolPolicies) — must survive the rename as the equivalent "bash" entry,
// taking the strictest value when more than one of the three legacy keys is
// present (deny > ask > allow), and the legacy keys themselves must not
// linger afterward (no permanent dual-key backward compatibility).
//
// The actual migration logic is shared (parameterized by legacyKeys/newKey)
// with the sibling spawn/run_subagent/check_spawn_status -> delegate
// migration in delegate_tool_policy_migration.go — see
// legacy_tool_policy_migration.go for the common core. This file is now just
// the bash-specific naming (shellLegacyPolicyKeys, bashPolicyKey) plus thin
// wrappers so every other call site (config.go's loadConfigInternal, and
// this migration's own tests) keeps its pre-existing function names and
// signatures.
//
// Call-site ordering (loadConfigInternal, config.go): phase 1 MUST run
// BEFORE migrateDeprecatedToolEnableFlags. FR-M2 retargets that older
// migration's {"exec", ...} row at the "bash" glob instead of "exec", and its
// no-clobber guard only checks for a pre-existing entry under that glob —
// running this migration first ensures a real operator-set
// exec/workspace_shell/workspace_shell_bg policy value is already sitting
// under "bash" by the time the boolean-flag migration looks, so it is never
// downgraded to "deny".

package config

// shellLegacyPolicyKeys lists the exec/workspace_shell/workspace_shell_bg
// tool-policy keys ADR-036 consolidates into bashPolicyKey. Order carries no
// meaning here — strictness is resolved by policyStrictnessRank, not by
// position in this slice.
var shellLegacyPolicyKeys = []string{"exec", "workspace_shell", "workspace_shell_bg"}

// bashPolicyKey is the ADR-036 consolidated tool-policy key that
// exec/workspace_shell/workspace_shell_bg are migrated into.
const bashPolicyKey = "bash"

// migrateShellToolPolicyKeys implements FR-M1/FR-M3 (in-memory half): for
// the global OmnipusSandboxConfig.ToolPolicies map and every agent's
// AgentBuiltinToolsCfg.Policies map, converts any present
// exec/workspace_shell/workspace_shell_bg entry into a "bash" entry (taking
// the strictest value when more than one legacy key is present) and deletes
// the legacy keys from the in-memory map.
//
// Idempotent: a config with no legacy keys anywhere is a complete no-op
// (returns false, mutates nothing) — running this twice in a row on the same
// cfg produces no further change on the second call, since by then the
// legacy keys are already gone.
//
// Returns true if anything was converted/deleted anywhere (global or any
// agent), signaling the caller (loadConfigInternal) that the on-disk phase
// (writeShellToolPolicyMigrationOnDisk) needs to run.
//
// Thin wrapper over the shared migrateLegacyToolPolicyKeys
// (legacy_tool_policy_migration.go).
func migrateShellToolPolicyKeys(cfg *Config) bool {
	return migrateLegacyToolPolicyKeys(cfg, shellLegacyPolicyKeys, bashPolicyKey)
}

// writeShellToolPolicyMigrationOnDisk implements FR-M3/FR-M4 (on-disk half):
// re-reads config.json at path, and for the global sandbox.tool_policies map
// and every agents.list[].tools.builtin.policies map that still carries a
// legacy exec/workspace_shell/workspace_shell_bg key on disk, writes a
// timestamped backup ("<path>.pre-bash-migration.<unix ts>.bak") of the
// untouched pre-migration bytes, then persists the same "bash" value cfg
// already holds in memory (set by migrateShellToolPolicyKeys) and deletes
// the legacy keys.
//
// Returns (nil, "", nil) — a true no-op, no write performed — when there is
// nothing left to strip on disk (e.g. a concurrent load already self-healed
// the file). On a successful write, returns the exact bytes written to
// config.json (never nil) and the absolute path of the backup file, which
// was written before the strip and must be named in the caller's boot-time
// log line (FR-M4).
//
// Best-effort by design, matching stripDeprecatedToolEnableFlagsOnDisk: on
// any read/write/parse failure the caller logs a warning and leaves the
// legacy shape on disk. The in-memory cfg is already correct regardless (the
// derived "bash" policy was set by migrateShellToolPolicyKeys before this
// ever runs), so runtime enforcement via the policy engine is unaffected by
// a failed self-heal.
//
// Thin wrapper over the shared writeLegacyToolPolicyMigrationOnDisk
// (legacy_tool_policy_migration.go).
func writeShellToolPolicyMigrationOnDisk(cfg *Config, path string) (written []byte, backupPath string, err error) {
	return writeLegacyToolPolicyMigrationOnDisk(cfg, path, shellLegacyPolicyKeys, bashPolicyKey, "bash")
}

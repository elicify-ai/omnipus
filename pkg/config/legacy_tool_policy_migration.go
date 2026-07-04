// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// Shared, name-agnostic core for ADR-036's "N legacy tool
// names collapse into one consolidated tool" migrations.
//
// ADR-036 performed this exact shape of consolidation twice:
//   - exec / workspace_shell / workspace_shell_bg -> bash (bash-tool-spec.md
//     User Story 4, FR-M1..FR-M4) — see shell_tool_policy_migration.go.
//   - spawn / run_subagent / check_spawn_status -> delegate
//     (agent-delegation-spec.md) — see delegate_tool_policy_migration.go.
//
// Both migrations must, for any operator's persisted tool-policy entry under
// a retired legacy name — per-agent (AgentBuiltinToolsCfg.Policies) or global
// (OmnipusSandboxConfig.ToolPolicies) — carry that entry forward as the
// equivalent entry under the new consolidated name, taking the strictest
// value when more than one legacy key is present (deny > ask > allow), and
// must not leave the legacy keys lingering afterward (no permanent dual-key
// backward compatibility). This file factors that logic out into helpers
// parameterized by (legacyKeys []string, newKey string) so it is written
// once and shared by both call sites rather than duplicated per
// consolidation.
//
// Two-phase shape (mirrors migrateDeprecatedToolEnableFlags /
// stripDeprecatedToolEnableFlagsOnDisk):
//
//  1. migrateLegacyToolPolicyKeys mutates cfg in memory only (no disk I/O),
//     computing/writing the new-key entry and deleting the legacy keys from
//     the in-memory maps. Returns whether anything changed, so the caller
//     knows whether the (more expensive, disk-touching) phase 2 is needed.
//  2. writeLegacyToolPolicyMigrationOnDisk performs the on-disk half: it
//     re-reads config.json fresh, writes a timestamped backup of the
//     untouched pre-migration bytes (the operator's only recovery path once
//     the legacy keys are gone), then patches and persists the same
//     conversion phase 1 already decided, atomically.

package config

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/fileutil"
)

// policyStrictnessRank ranks a policy VALUE string from loosest (0) to
// strictest (2): allow=0, ask=1, deny=2. A value that is not one of the three
// recognized strings is fail-safe ranked as strictest (deny) — a malformed
// legacy value must never resolve to the loosest outcome. Shared by every
// legacy-tool-policy-key migration (bash and delegate today).
func policyStrictnessRank(v string) int {
	switch ToolPolicy(v) {
	case ToolPolicyAllow:
		return 0
	case ToolPolicyAsk:
		return 1
	case ToolPolicyDeny:
		return 2
	default:
		return 2
	}
}

// resolveLegacyPolicyMigration inspects one tool-policy map (either the
// global sandbox.tool_policies map or a single agent's
// tools.builtin.policies map, via the get closure) for the given legacyKeys.
//
// agentID is used only for WARN log context on a malformed value; pass ""
// for the global scope.
//
// Returns:
//   - newValue: the value that must end up under newKey. Empty when
//     hadLegacy is false (nothing to migrate — a policy must never be
//     invented where none existed).
//   - hadLegacy: true when at least one of the legacyKeys was present,
//     meaning the caller must delete them regardless of whether the resolved
//     value actually changed anything.
//
// Authoritative-new-key rule: if newKey is ALREADY present, its value is
// never recomputed from the legacy keys — it wins outright — but any legacy
// key found alongside it is still reported via hadLegacy so the caller
// cleans it up.
func resolveLegacyPolicyMigration(
	agentID string,
	legacyKeys []string,
	newKey string,
	get func(key string) (string, bool),
) (newValue string, hadLegacy bool) {
	newVal, newPresent := get(newKey)

	strictestValue := ""
	strictestRank := -1
	for _, key := range legacyKeys {
		val, present := get(key)
		if !present {
			continue
		}
		hadLegacy = true

		if !ValidPolicy(ToolPolicy(val)) {
			slog.Warn("config: malformed legacy tool-policy value treated as deny (fail-safe)",
				"component", "config",
				"agent_id", agentID,
				"key", key,
				"value", val,
				"new_key", newKey,
			)
		}

		rank := policyStrictnessRank(val)
		if rank > strictestRank {
			strictestRank = rank
			if ValidPolicy(ToolPolicy(val)) {
				strictestValue = val
			} else {
				strictestValue = string(ToolPolicyDeny)
			}
		}
	}

	if !hadLegacy {
		return "", false
	}
	if newPresent {
		// Already-migrated (or hand-set) entry under newKey is authoritative
		// — a stale legacy key alongside it is deleted as cleanup, not merged.
		return newVal, true
	}
	return strictestValue, true
}

// migrateLegacyToolPolicyKeys implements the in-memory half: for the global
// OmnipusSandboxConfig.ToolPolicies map and every agent's
// AgentBuiltinToolsCfg.Policies map, converts any present legacyKeys entry
// into a newKey entry (taking the strictest value when more than one legacy
// key is present) and deletes the legacy keys from the in-memory map.
//
// Idempotent: a config with no legacy keys anywhere is a complete no-op
// (returns false, mutates nothing) — running this twice in a row on the same
// cfg produces no further change on the second call, since by then the
// legacy keys are already gone.
//
// Returns true if anything was converted/deleted anywhere (global or any
// agent), signaling the caller (loadConfigInternal) that the on-disk phase
// (writeLegacyToolPolicyMigrationOnDisk) needs to run.
func migrateLegacyToolPolicyKeys(cfg *Config, legacyKeys []string, newKey string) bool {
	if cfg == nil {
		return false
	}
	touched := false

	// Global scope: OmnipusSandboxConfig.ToolPolicies.
	if newVal, had := resolveLegacyPolicyMigration("", legacyKeys, newKey, func(key string) (string, bool) {
		v, ok := cfg.Sandbox.ToolPolicies[key]
		return v, ok
	}); had {
		if cfg.Sandbox.ToolPolicies == nil {
			cfg.Sandbox.ToolPolicies = make(map[string]string)
		}
		cfg.Sandbox.ToolPolicies[newKey] = newVal
		for _, key := range legacyKeys {
			delete(cfg.Sandbox.ToolPolicies, key)
		}
		touched = true
	}

	// Per-agent scope: AgentBuiltinToolsCfg.Policies.
	for i := range cfg.Agents.List {
		agent := &cfg.Agents.List[i]
		if agent.Tools == nil || len(agent.Tools.Builtin.Policies) == 0 {
			continue
		}
		policies := agent.Tools.Builtin.Policies

		newVal, had := resolveLegacyPolicyMigration(agent.ID, legacyKeys, newKey, func(key string) (string, bool) {
			v, ok := policies[key]
			return string(v), ok
		})
		if !had {
			continue
		}
		policies[newKey] = ToolPolicy(newVal)
		for _, key := range legacyKeys {
			delete(policies, key)
		}
		touched = true
	}

	return touched
}

// writeLegacyToolPolicyMigrationOnDisk implements the on-disk half: re-reads
// config.json at path, and for the global sandbox.tool_policies map and
// every agents.list[].tools.builtin.policies map that still carries a legacy
// key on disk, writes a timestamped backup of the untouched pre-migration
// bytes, then persists the same newKey value cfg already holds in memory
// (set by migrateLegacyToolPolicyKeys) and deletes the legacy keys.
//
// backupTag names the backup file: "<path>.pre-<backupTag>-migration.<unix
// ts>.bak".
//
// Returns (nil, "", nil) — a true no-op, no write performed — when there is
// nothing left to strip on disk (e.g. a concurrent load already self-healed
// the file). On a successful write, returns the exact bytes written to
// config.json (never nil) and the absolute path of the backup file, which
// was written before the strip.
//
// Best-effort by design: on any read/write/parse failure the caller logs a
// warning and leaves the legacy shape on disk. The in-memory cfg is already
// correct regardless (the derived policy was set by
// migrateLegacyToolPolicyKeys before this ever runs), so runtime enforcement
// via the policy engine is unaffected by a failed self-heal.
func writeLegacyToolPolicyMigrationOnDisk(
	cfg *Config,
	path string,
	legacyKeys []string,
	newKey string,
	backupTag string,
) (written []byte, backupPath string, err error) {
	if cfg == nil {
		return nil, "", nil
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("read config for %s tool-policy migration: %w", backupTag, err)
	}
	var m map[string]any
	if unmarshalErr := json.Unmarshal(raw, &m); unmarshalErr != nil {
		return nil, "", fmt.Errorf("parse config for %s tool-policy migration: %w", backupTag, unmarshalErr)
	}

	changed := false

	// Global scope: sandbox.tool_policies.
	if sandboxRaw, ok := m["sandbox"].(map[string]any); ok {
		if policies, ok := sandboxRaw["tool_policies"].(map[string]any); ok {
			if stripLegacyPolicyKeysOnRawMap(policies, legacyKeys, newKey, cfg.Sandbox.ToolPolicies[newKey]) {
				changed = true
			}
		}
	}

	// Per-agent scope: agents.list[].tools.builtin.policies, matched by "id"
	// (falling back to positional index if an entry is missing/blank "id",
	// which should not happen in practice but keeps this defensive).
	if agentsRaw, ok := m["agents"].(map[string]any); ok {
		if list, ok := agentsRaw["list"].([]any); ok {
			for i, entryAny := range list {
				entry, ok := entryAny.(map[string]any)
				if !ok {
					continue
				}
				toolsRaw, ok := entry["tools"].(map[string]any)
				if !ok {
					continue
				}
				builtinRaw, ok := toolsRaw["builtin"].(map[string]any)
				if !ok {
					continue
				}
				policies, ok := builtinRaw["policies"].(map[string]any)
				if !ok {
					continue
				}

				id, _ := entry["id"].(string)
				newVal := legacyToolPolicyValue(cfg, id, i, newKey)
				if stripLegacyPolicyKeysOnRawMap(policies, legacyKeys, newKey, newVal) {
					changed = true
				}
			}
		}
	}

	if !changed {
		return nil, "", nil
	}

	// Write the timestamped backup of the untouched pre-migration bytes
	// BEFORE persisting the strip, so an operator has a recoverable path
	// back to the exact pre-migration policy maps if a bug is later
	// discovered in this migration.
	backupPath = fmt.Sprintf("%s.pre-%s-migration.%d.bak", path, backupTag, time.Now().Unix())
	if backupErr := fileutil.WriteFileAtomic(backupPath, raw, 0o600); backupErr != nil {
		return nil, "", fmt.Errorf("write pre-migration backup for %s tool-policy migration: %w", backupTag, backupErr)
	}

	out, marshalErr := json.MarshalIndent(m, "", "  ")
	if marshalErr != nil {
		return nil, backupPath, fmt.Errorf("serialize config for %s tool-policy migration: %w", backupTag, marshalErr)
	}
	if writeErr := fileutil.WriteFileAtomic(path, out, 0o600); writeErr != nil {
		return nil, backupPath, fmt.Errorf("write config for %s tool-policy migration: %w", backupTag, writeErr)
	}
	return out, backupPath, nil
}

// stripLegacyPolicyKeysOnRawMap deletes any of legacyKeys present in a raw
// JSON map[string]any tool-policy object, setting newKey to newVal when
// newVal is non-empty. Returns whether any legacy key was actually present
// (and therefore deleted) — a true no-op (no legacy key present) returns
// false and leaves the map untouched, including leaving newKey alone even if
// newVal is non-empty, since there is nothing to migrate.
func stripLegacyPolicyKeysOnRawMap(policies map[string]any, legacyKeys []string, newKey string, newVal string) bool {
	hadLegacy := false
	for _, key := range legacyKeys {
		if _, present := policies[key]; present {
			hadLegacy = true
			delete(policies, key)
		}
	}
	if !hadLegacy {
		return false
	}
	if newVal != "" {
		policies[newKey] = newVal
	}
	return true
}

// legacyToolPolicyValue returns the newKey policy value
// migrateLegacyToolPolicyKeys already computed in memory for the agent
// identified by id (falling back to positional index idx when id is empty),
// or "" if the agent has no such entry (e.g. it never carried a legacy key).
func legacyToolPolicyValue(cfg *Config, id string, idx int, newKey string) string {
	if cfg == nil {
		return ""
	}
	if id != "" {
		for i := range cfg.Agents.List {
			if cfg.Agents.List[i].ID == id {
				return agentPolicyValueForKey(&cfg.Agents.List[i], newKey)
			}
		}
		return ""
	}
	if idx >= 0 && idx < len(cfg.Agents.List) {
		return agentPolicyValueForKey(&cfg.Agents.List[idx], newKey)
	}
	return ""
}

// agentPolicyValueForKey returns agent's in-memory tool-policy value for
// key, or "" if unset.
func agentPolicyValueForKey(agent *AgentConfig, key string) string {
	if agent.Tools == nil || agent.Tools.Builtin.Policies == nil {
		return ""
	}
	return string(agent.Tools.Builtin.Policies[key])
}

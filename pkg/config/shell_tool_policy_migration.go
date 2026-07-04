// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// Package config — ADR-036 exec/workspace_shell/workspace_shell_bg -> bash
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
// This mirrors the two-phase shape of migrateDeprecatedToolEnableFlags
// (migration.go) / stripDeprecatedToolEnableFlagsOnDisk
// (tool_enable_migration.go):
//
//  1. migrateShellToolPolicyKeys mutates cfg in memory only (no disk I/O),
//     computing/writing the "bash" entry and deleting the legacy keys from
//     the in-memory maps. Returns whether anything changed, so the caller
//     knows whether the (more expensive, disk-touching) phase 2 is needed.
//  2. writeShellToolPolicyMigrationOnDisk performs the on-disk half: it
//     re-reads config.json fresh (the raw-JSON-map read/patch/write
//     technique shared with migrateCLITokenOnDisk/
//     stripDeprecatedToolEnableFlagsOnDisk), writes a timestamped backup of
//     those untouched pre-migration bytes (FR-M4 — the operator's only
//     recovery path once the legacy keys are gone, since post-deletion they
//     are otherwise unrecoverable from the running system), then patches and
//     persists the same conversion phase 1 already decided, atomically.
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

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/fileutil"
)

// shellLegacyPolicyKeys lists the exec/workspace_shell/workspace_shell_bg
// tool-policy keys ADR-036 consolidates into bashPolicyKey. Order carries no
// meaning here — strictness is resolved by policyStrictnessRank, not by
// position in this slice.
var shellLegacyPolicyKeys = []string{"exec", "workspace_shell", "workspace_shell_bg"}

// bashPolicyKey is the ADR-036 consolidated tool-policy key that
// exec/workspace_shell/workspace_shell_bg are migrated into.
const bashPolicyKey = "bash"

// policyStrictnessRank ranks a policy VALUE string from loosest (0) to
// strictest (2): allow=0, ask=1, deny=2. A value that is not one of the three
// recognized strings is fail-safe ranked as strictest (deny) per FR-M4 — a
// malformed legacy value must never resolve to the loosest outcome.
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

// resolveShellPolicyMigration inspects one tool-policy map (either the
// global sandbox.tool_policies map or a single agent's
// tools.builtin.policies map, via the get closure) for the legacy
// exec/workspace_shell/workspace_shell_bg keys.
//
// agentID is used only for WARN log context on a malformed value; pass ""
// for the global scope.
//
// Returns:
//   - bashValue: the value that must end up under bashPolicyKey. Empty when
//     hadLegacy is false (nothing to migrate — a policy must never be
//     invented where none existed, per the spec's dataset row 4).
//   - hadLegacy: true when at least one of the three legacy keys was present,
//     meaning the caller must delete them (FR-M3) regardless of whether the
//     resolved value actually changed anything.
//
// FR-M3's authoritative-bash rule: if bashPolicyKey is ALREADY present, its
// value is never recomputed from the legacy keys — it wins outright — but
// any legacy key found alongside it is still reported via hadLegacy so the
// caller cleans it up.
func resolveShellPolicyMigration(agentID string, get func(key string) (string, bool)) (bashValue string, hadLegacy bool) {
	bashVal, bashPresent := get(bashPolicyKey)

	strictestValue := ""
	strictestRank := -1
	for _, key := range shellLegacyPolicyKeys {
		val, present := get(key)
		if !present {
			continue
		}
		hadLegacy = true

		if !ValidPolicy(ToolPolicy(val)) {
			slog.Warn("config: malformed legacy shell tool-policy value treated as deny (fail-safe)",
				"component", "config",
				"agent_id", agentID,
				"key", key,
				"value", val,
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
	if bashPresent {
		// Already-migrated (or hand-set) "bash" entry is authoritative — a
		// stale legacy key alongside it is deleted as cleanup, not merged.
		return bashVal, true
	}
	return strictestValue, true
}

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
func migrateShellToolPolicyKeys(cfg *Config) bool {
	if cfg == nil {
		return false
	}
	touched := false

	// Global scope: OmnipusSandboxConfig.ToolPolicies.
	if bashVal, had := resolveShellPolicyMigration("", func(key string) (string, bool) {
		v, ok := cfg.Sandbox.ToolPolicies[key]
		return v, ok
	}); had {
		if cfg.Sandbox.ToolPolicies == nil {
			cfg.Sandbox.ToolPolicies = make(map[string]string)
		}
		cfg.Sandbox.ToolPolicies[bashPolicyKey] = bashVal
		for _, key := range shellLegacyPolicyKeys {
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

		bashVal, had := resolveShellPolicyMigration(agent.ID, func(key string) (string, bool) {
			v, ok := policies[key]
			return string(v), ok
		})
		if !had {
			continue
		}
		policies[bashPolicyKey] = ToolPolicy(bashVal)
		for _, key := range shellLegacyPolicyKeys {
			delete(policies, key)
		}
		touched = true
	}

	return touched
}

// writeShellToolPolicyMigrationOnDisk implements FR-M3/FR-M4 (on-disk half):
// re-reads config.json at path, and for the global sandbox.tool_policies map
// and every agents.list[].tools.builtin.policies map that still carries a
// legacy exec/workspace_shell/workspace_shell_bg key on disk, writes a
// timestamped backup of the untouched pre-migration bytes, then persists the
// same "bash" value cfg already holds in memory (set by
// migrateShellToolPolicyKeys) and deletes the legacy keys.
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
func writeShellToolPolicyMigrationOnDisk(cfg *Config, path string) (written []byte, backupPath string, err error) {
	if cfg == nil {
		return nil, "", nil
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("read config for bash tool-policy migration: %w", err)
	}
	var m map[string]any
	if unmarshalErr := json.Unmarshal(raw, &m); unmarshalErr != nil {
		return nil, "", fmt.Errorf("parse config for bash tool-policy migration: %w", unmarshalErr)
	}

	changed := false

	// Global scope: sandbox.tool_policies.
	if sandboxRaw, ok := m["sandbox"].(map[string]any); ok {
		if policies, ok := sandboxRaw["tool_policies"].(map[string]any); ok {
			if stripLegacyShellPolicyKeysOnRawMap(policies, cfg.Sandbox.ToolPolicies[bashPolicyKey]) {
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
				bashVal := agentBashPolicyValue(cfg, id, i)
				if stripLegacyShellPolicyKeysOnRawMap(policies, bashVal) {
					changed = true
				}
			}
		}
	}

	if !changed {
		return nil, "", nil
	}

	// FR-M4: write the timestamped backup of the untouched pre-migration
	// bytes BEFORE persisting the strip, so an operator has a recoverable
	// path back to the exact pre-migration policy maps if a bug is later
	// discovered in this migration.
	backupPath = fmt.Sprintf("%s.pre-bash-migration.%d.bak", path, time.Now().Unix())
	if backupErr := fileutil.WriteFileAtomic(backupPath, raw, 0o600); backupErr != nil {
		return nil, "", fmt.Errorf("write pre-migration backup for bash tool-policy migration: %w", backupErr)
	}

	out, marshalErr := json.MarshalIndent(m, "", "  ")
	if marshalErr != nil {
		return nil, backupPath, fmt.Errorf("serialize config for bash tool-policy migration: %w", marshalErr)
	}
	if writeErr := fileutil.WriteFileAtomic(path, out, 0o600); writeErr != nil {
		return nil, backupPath, fmt.Errorf("write config for bash tool-policy migration: %w", writeErr)
	}
	return out, backupPath, nil
}

// stripLegacyShellPolicyKeysOnRawMap deletes any of the three legacy
// exec/workspace_shell/workspace_shell_bg keys present in a raw JSON
// map[string]any tool-policy object, setting bashPolicyKey to bashVal when
// bashVal is non-empty. Returns whether any legacy key was actually present
// (and therefore deleted) — a true no-op (no legacy key present) returns
// false and leaves the map untouched, including leaving bashPolicyKey alone
// even if bashVal is non-empty, since there is nothing to migrate.
func stripLegacyShellPolicyKeysOnRawMap(policies map[string]any, bashVal string) bool {
	hadLegacy := false
	for _, key := range shellLegacyPolicyKeys {
		if _, present := policies[key]; present {
			hadLegacy = true
			delete(policies, key)
		}
	}
	if !hadLegacy {
		return false
	}
	if bashVal != "" {
		policies[bashPolicyKey] = bashVal
	}
	return true
}

// agentBashPolicyValue returns the "bash" policy value migrateShellToolPolicyKeys
// already computed in memory for the agent identified by id (falling back to
// positional index idx when id is empty), or "" if the agent has no such
// entry (e.g. it never carried a legacy shell-tool key).
func agentBashPolicyValue(cfg *Config, id string, idx int) string {
	if cfg == nil {
		return ""
	}
	if id != "" {
		for i := range cfg.Agents.List {
			if cfg.Agents.List[i].ID == id {
				return agentPolicyValue(&cfg.Agents.List[i])
			}
		}
		return ""
	}
	if idx >= 0 && idx < len(cfg.Agents.List) {
		return agentPolicyValue(&cfg.Agents.List[idx])
	}
	return ""
}

// agentPolicyValue returns agent's in-memory "bash" tool-policy value, or ""
// if unset.
func agentPolicyValue(agent *AgentConfig) string {
	if agent.Tools == nil || agent.Tools.Builtin.Policies == nil {
		return ""
	}
	return string(agent.Tools.Builtin.Policies[bashPolicyKey])
}

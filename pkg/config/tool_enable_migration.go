// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// Deprecated tools.<name>.enabled=false self-heal.
//
// migrateDeprecatedToolEnableFlags (migration.go) translates a legacy
// tools.<name>.enabled=false flag into an in-memory sandbox.tool_policies
// deny entry on every config load, but on its own never removed the legacy
// flag or persisted the derived policy. That made the migration re-fire on
// every hot-reload: an operator who used the UI to set the tool back to
// "allow" would have the PUT handler correctly delete the (now-redundant)
// tool_policies override, only for the next reload to re-derive "deny" from
// the still-present legacy flag — silently bouncing the operator's choice
// back (issue: tool-policy allow doesn't stick across reload).
//
// stripDeprecatedToolEnableFlagsOnDisk closes that loop: once the legacy
// flags have been translated in memory, this rewrites config.json to (a)
// remove the legacy tools.<name>.enabled key so it can never re-fire the
// migration, and (b) persist the derived policy as a real
// sandbox.tool_policies entry so operator intent survives on disk, not just
// in memory for the lifetime of this process. After this runs once, a
// subsequent operator "allow" (which deletes the tool_policies override,
// since allow is the default) reloads into a config with no legacy flag
// left to re-derive "deny" from, so the allow sticks.
//
// This mirrors migrateCLITokenOnDisk's raw-JSON-map read/patch/write
// technique (cli_token_migration.go's header comment) so the on-disk
// file is patched byte-for-byte rather than round-tripped through
// SaveConfig, which silently drops any field carrying an explicit zero value
// under an `omitempty` JSON tag.

package config

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/dapicom-ai/omnipus/pkg/fileutil"
)

// stripDeprecatedToolEnableFlagsOnDisk removes the legacy tools.<jsonKey>.enabled
// key for every entry in legacy from config.json at path, and ensures
// sandbox.tool_policies.<policyGlob> is present on disk with the value
// migrateDeprecatedToolEnableFlags already decided for cfg (deny for a
// freshly migrated tool, or whatever stricter-or-equal policy was already
// there). cfg.Sandbox.ToolPolicies is the single source of truth for what
// this load decided — this function only mirrors that decision onto disk, it
// never re-derives or downgrades a policy.
//
// Returns (nil, nil) — a true no-op, no write performed — when there is
// nothing left to strip on disk (e.g. a concurrent load already self-healed
// the file, or none of the legacy keys are actually present in the on-disk
// JSON). On a successful write, returns the exact bytes written (never nil).
//
// Best-effort by design, matching migrateCLITokenOutOfUsers: on any read/
// write/parse failure the caller logs a warning and leaves the legacy shape
// on disk. The in-memory cfg is already correct regardless (the deny/preserved
// policy was set by migrateDeprecatedToolEnableFlags before this ever runs),
// so runtime enforcement via the policy engine is unaffected by a failed
// self-heal — only the next load's re-derivation is not yet suppressed.
func stripDeprecatedToolEnableFlagsOnDisk(cfg *Config, path string, legacy []legacyToolEnableFlag) ([]byte, error) {
	if cfg == nil || len(legacy) == 0 {
		return nil, nil
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config for tool-enable-flag migration: %w", err)
	}
	var m map[string]any
	if unmarshalErr := json.Unmarshal(raw, &m); unmarshalErr != nil {
		return nil, fmt.Errorf("parse config for tool-enable-flag migration: %w", unmarshalErr)
	}
	toolsRaw, ok := m["tools"].(map[string]any)
	if !ok {
		return nil, nil // no tools section on disk — nothing to strip
	}

	var policies map[string]any
	sandboxRaw, hasSandbox := m["sandbox"].(map[string]any)
	if hasSandbox {
		if p, policyOK := sandboxRaw["tool_policies"].(map[string]any); policyOK {
			policies = p
		}
	}

	changed := false
	for _, flag := range legacy {
		subsys, subsysOK := toolsRaw[flag.jsonKey].(map[string]any)
		if !subsysOK {
			continue
		}
		if _, hasEnabled := subsys["enabled"]; !hasEnabled {
			continue // already stripped on disk (e.g. a concurrent self-heal)
		}

		// cfg.Sandbox.ToolPolicies is guaranteed to hold a value for this glob:
		// migrateDeprecatedToolEnableFlags only returns a legacyToolEnableFlag
		// after setting it (fresh deny) or confirming it already existed
		// (stricter-or-equal policy preserved). Guard defensively anyway
		// rather than ever writing an empty/missing policy value.
		policyVal, hasPolicy := cfg.Sandbox.ToolPolicies[flag.policyGlob]
		if !hasPolicy {
			continue
		}

		delete(subsys, "enabled")
		if policies == nil {
			policies = make(map[string]any)
		}
		policies[flag.policyGlob] = policyVal
		changed = true
	}

	if !changed {
		return nil, nil
	}

	if !hasSandbox {
		sandboxRaw = make(map[string]any)
		m["sandbox"] = sandboxRaw
	}
	sandboxRaw["tool_policies"] = policies

	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("serialize config for tool-enable-flag migration: %w", err)
	}
	if writeErr := fileutil.WriteFileAtomic(path, out, 0o600); writeErr != nil {
		return nil, fmt.Errorf("write config for tool-enable-flag migration: %w", writeErr)
	}
	return out, nil
}

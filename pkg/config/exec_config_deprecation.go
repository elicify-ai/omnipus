// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

import (
	"encoding/json"
	"log/slog"
	"sync"
)

// deprecatedExecConfigWarnOnce guards the one-time boot Warn for legacy
// tools.exec.* fields. Mirrors deprecatedToolEnableMigrateOnce's contract
// (migration.go): a sync.Once so the noisy warning fires at most once per
// process lifetime; tests reset it via `deprecatedExecConfigWarnOnce =
// sync.Once{}`, matching the sibling migrations' test convention.
var deprecatedExecConfigWarnOnce sync.Once

// execConfigDefaultTimeoutSeconds and execConfigDefaultMaxBackgroundSeconds
// mirror the ExecConfig defaults seeded by DefaultConfig() (defaults.go).
// config.json persists the FULL defaulted struct (these fields lack
// `omitempty`), so every fresh/onboarded install's tools.exec block already
// contains "enable_deny_patterns":true, "timeout_seconds":60, and
// "max_background_seconds":300 whether or not the operator ever touched
// them. A naive "key present" check would therefore fire on essentially
// every config.json in existence. These constants let
// warnDeprecatedExecConfigFields distinguish "operator explicitly
// customized this field" from "this is just the seeded default" — only the
// former is worth a WARN. Keep these in lock-step with defaults.go; drifting
// out of sync only shifts the false-positive/negative window, it is not a
// correctness bug (the fields are dead either way).
const (
	execConfigDefaultTimeoutSeconds       = 60
	execConfigDefaultMaxBackgroundSeconds = 300
)

// warnDeprecatedExecConfigFields inspects the raw tools.exec JSON object for
// legacy fields that ExecConfig (config.go) still declares but that the
// ADR-036 `bash` tool (pkg/tools/shell.go) never reads —
// NewExecToolWithConfig discards its *config.Config parameter entirely.
//
// This is advisory-only, unlike migrateDeprecatedToolEnableFlags: there is
// no in-memory translation to perform here.
//   - enable_deny_patterns / custom_deny_patterns have a live successor
//     (Sandbox.ShellDenyPatterns + per-agent AgentConfig.ShellPolicy) that is
//     an independent config surface the operator sets directly — not
//     something this function can safely infer and rewrite for a native
//     Omnipus config (the OpenClaw importer performs the one-way migration
//     for imported configs at conversion time, see
//     pkg/migrate/sources/openclaw/openclaw_config.go's ToStandardConfig).
//   - timeout_seconds / max_background_seconds have NO config-level
//     successor at all: the bash tool's timeout is caller-controlled per
//     invocation via the tool call's timeout_seconds argument.
//   - custom_allow_patterns has NO successor whatsoever in the merged tool —
//     a deliberate, flagged capability gap, not a silent drop.
//
// Only fields whose persisted value differs from the seeded default are
// treated as operator intent (see the execConfigDefault* constants above).
func warnDeprecatedExecConfigFields(raw []byte) {
	if len(raw) == 0 {
		return
	}

	var top struct {
		Tools struct {
			Exec json.RawMessage `json:"exec"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(raw, &top); err != nil || len(top.Tools.Exec) == 0 {
		return
	}

	var exec map[string]json.RawMessage
	if err := json.Unmarshal(top.Tools.Exec, &exec); err != nil {
		return
	}

	var found []string

	if rawPatterns, ok := exec["custom_deny_patterns"]; ok {
		var patterns []string
		if err := json.Unmarshal(rawPatterns, &patterns); err == nil && len(patterns) > 0 {
			found = append(found, "custom_deny_patterns")
		}
	}
	if rawPatterns, ok := exec["custom_allow_patterns"]; ok {
		var patterns []string
		if err := json.Unmarshal(rawPatterns, &patterns); err == nil && len(patterns) > 0 {
			found = append(found, "custom_allow_patterns")
		}
	}
	// Only the explicit-false case is interesting: true is indistinguishable
	// from the seeded default and is not operator intent on its own.
	if rawVal, ok := exec["enable_deny_patterns"]; ok && string(rawVal) == "false" {
		found = append(found, "enable_deny_patterns")
	}
	if rawVal, ok := exec["timeout_seconds"]; ok {
		var v int
		if err := json.Unmarshal(rawVal, &v); err == nil && v != 0 && v != execConfigDefaultTimeoutSeconds {
			found = append(found, "timeout_seconds")
		}
	}
	if rawVal, ok := exec["max_background_seconds"]; ok {
		var v int
		if err := json.Unmarshal(rawVal, &v); err == nil && v != 0 && v != execConfigDefaultMaxBackgroundSeconds {
			found = append(found, "max_background_seconds")
		}
	}

	if len(found) == 0 {
		return
	}

	// Always emit an Info-level log on every load (including hot-reloads) so
	// operators see this in the log even after the Once has fired — mirrors
	// migrateDeprecatedToolEnableFlags' two-tier Info/Warn-once convention.
	slog.Info("tools.exec.* legacy fields are set but no longer read by the bash tool (ADR-036 exec/workspace_shell consolidation)",
		"component", "config",
		"deprecated_fields", found)

	deprecatedExecConfigWarnOnce.Do(func() {
		slog.Warn("tools.exec.* config fields are deprecated and permanently ignored; "+
			"remove them from config.json — deny patterns now live in security.shell_deny_patterns "+
			"(global) plus agents[].shell_policy (per-agent, opt-in); timeout/max_background_seconds "+
			"have no config equivalent (bash's timeout_seconds tool argument controls both foreground "+
			"and background calls per-invocation); custom_allow_patterns has no successor",
			"component", "config",
			"deprecated_fields", found)
	})
}

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools_test

// config_settings_redesign_test.go — Settings redesign #1519, RED.
//
// Test Implementation Order rows 6b and 16 (spec docs/internal/specs/
// settings-redesign-spec.md):
//
//   - TestSetConfig_RefusesPerformanceValues — Dataset D-16 rows 5–8 (C-7,
//     #936, FR-100): the Performance values have ONE write path (Settings →
//     Limits, PUT /api/v1/performance, password). set_config must refuse all
//     of them. Rows 5 and 6 are refused today ("already refused today") —
//     preserved behaviour, pinned so a knownConfigPrefixes change cannot
//     silently open them. Rows 7 and 8 are the NEW refusals
//     (tools.manifest.compressed, and its parent tools.manifest "at or under
//     the key") — RED today, because tools. is a writable prefix and the
//     value guard only watches the two agents.defaults keys.
//   - TestSettingsAgentWriteParity — FR-101: every value this spec lets a
//     person edit has either an agent setter or a listed user-only reason
//     with a passing refusal through the REAL set_config tool. The table
//     below IS that enumeration: a Settings value added without an entry
//     here — or an entry with neither an agent setter nor a refusal that
//     actually fires — fails this test. Per spec (Hard-constraint line
//     "The system MUST NOT add an agent tool or a new set_config write path
//     for any value in this spec"), no row has an agent setter today; every
//     row is a refusal row.
//
// Oracle: the spec's C-7 table, the agent-tool field table, D-16 and the BDD
// scenario "Performance values cannot be changed around Settings" (refused
// "with a message naming Settings → Limits or PUT /api/v1/performance").
// Expected values come from the spec, never from the tool's current output.

import (
	"context"
	"strings"
	"testing"

	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
)

func TestSetConfig_RefusesPerformanceValues(t *testing.T) {
	t.Run("D16 row 5: performance.max_parallel_agents is an unknown section", func(t *testing.T) {
		deps, cfg := newTestDeps(t)
		stored := cfg.Performance.MaxParallelAgents
		res := systools.NewConfigSetTool(deps).Execute(context.Background(),
			map[string]any{"key": "performance.max_parallel_agents", "value": float64(64)})
		// Preserved behaviour (spec D-16 row 5: "already refused today") —
		// pinned: performance. is not in knownConfigPrefixes.
		if !res.IsError {
			t.Fatalf("set_config accepted performance.max_parallel_agents — the pin fired: "+
				"performance. must stay outside the writable prefixes (#936 C-7); got: %s", res.ForLLM)
		}
		if !strings.Contains(res.ForLLM, "unknown config key") {
			t.Errorf("refusal must say the section is not settable via this tool: %s", res.ForLLM)
		}
		if cfg.Performance.MaxParallelAgents != stored {
			t.Errorf("refused write changed the config: %d → %d", stored, cfg.Performance.MaxParallelAgents)
		}
	})

	t.Run("D16 row 6: planning.goal_max_rounds is an unknown section", func(t *testing.T) {
		deps, cfg := newTestDeps(t)
		stored := cfg.Planning.GoalMaxRounds
		res := systools.NewConfigSetTool(deps).Execute(context.Background(),
			map[string]any{"key": "planning.goal_max_rounds", "value": float64(99)})
		// Preserved behaviour (spec D-16 row 6: "already refused today").
		if !res.IsError {
			t.Fatalf("set_config accepted planning.goal_max_rounds — the pin fired: "+
				"planning. must stay outside the writable prefixes (#936 C-7); got: %s", res.ForLLM)
		}
		if !strings.Contains(res.ForLLM, "unknown config key") {
			t.Errorf("refusal must say the section is not settable via this tool: %s", res.ForLLM)
		}
		if cfg.Planning.GoalMaxRounds != stored {
			t.Errorf("refused write changed the config: %d → %d", stored, cfg.Planning.GoalMaxRounds)
		}
	})

	t.Run("D16 row 7: tools.manifest.compressed is newly refused, message names Settings → Limits", func(t *testing.T) {
		deps, cfg := newTestDeps(t)
		if !cfg.Tools.Manifest.Compressed {
			t.Fatalf("fixture: DefaultConfig().Tools.Manifest.Compressed must be true (pkg/config pins this default)")
		}
		res := systools.NewConfigSetTool(deps).Execute(context.Background(),
			map[string]any{"key": "tools.manifest.compressed", "value": false})
		// RED: the spec's new refusal (FR-100, D-16 row 7). Today tools. is a
		// writable prefix and the value guard watches only agents.defaults.
		if !res.IsError {
			t.Fatalf("RED (D-16 row 7, FR-100): set_config accepted tools.manifest.compressed — "+
				"the #936 refusal does not exist yet; required: refusal whose message names Settings → Limits. Got: %s", res.ForLLM)
		}
		if !strings.Contains(res.ForLLM, "Settings → Limits") {
			t.Errorf("refusal must name Settings → Limits (spec D-16 row 7): %s", res.ForLLM)
		}
		if !cfg.Tools.Manifest.Compressed {
			t.Errorf("refused write changed the config: Compressed must stay true")
		}
	})

	t.Run("D16 row 8: parent tools.manifest is refused at or under the key", func(t *testing.T) {
		deps, cfg := newTestDeps(t)
		if !cfg.Tools.Manifest.Compressed {
			t.Fatalf("fixture: DefaultConfig().Tools.Manifest.Compressed must be true")
		}
		res := systools.NewConfigSetTool(deps).Execute(context.Background(),
			map[string]any{"key": "tools.manifest", "value": map[string]any{}})
		// RED: D-16 row 8 ("refused — at or under the key"): a write at the
		// parent of a performance-only key is refused too, whatever the value
		// carries.
		if !res.IsError {
			t.Fatalf("RED (D-16 row 8, FR-100): set_config accepted a write AT tools.manifest, the parent of a "+
				"performance-only key — required by the spec: refused at or under the key. Got: %s", res.ForLLM)
		}
		if !strings.Contains(res.ForLLM, "Settings →") {
			t.Errorf("refusal must point at Settings (scenario: names Settings → Limits or PUT /api/v1/performance): %s", res.ForLLM)
		}
		if !cfg.Tools.Manifest.Compressed {
			t.Errorf("refused write changed the config: Compressed must stay true")
		}
	})
}

// TestSettingsAgentWriteParity — FR-101, order 16. The enumeration of every
// value the Settings redesign lets a person edit, each with its listed
// user-only reason, each proven refused through the REAL set_config tool.
// No row has an agent setter: the spec forbids adding one ("The system MUST
// NOT add an agent tool or a new set_config write path for any value in this
// spec"). A future Settings value must be added to this table; one with
// neither an agent setter nor a firing refusal fails the BDD scenario
// "Every editable value has an agent path or a tested refusal".
func TestSettingsAgentWriteParity(t *testing.T) {
	// wantInRefusal pins the refusal family: "unknown config key" for the
	// not-a-known-prefix keys (spec: "not in knownConfigPrefixes"),
	// "Settings → Limits" for the #936 keys (D-16 row 7), "Settings →" where
	// the existing #904 refusal already names the screen. reason is the
	// spec's listed user-only reason (C-7 table + the agent-tool field
	// table); value is a write that would change the stored value — the
	// threat the refusals exist for (an agent or a stolen token raising its
	// own limits, or deleting data on a schedule).
	rows := []struct {
		name          string
		key           string
		value         any
		reason        string
		wantInRefusal string
	}{
		// Retention — user-only, "deletes data on a schedule" (spec agent-tool
		// field table); set_config cannot write storage.* (not in
		// knownConfigPrefixes), refusal pinned here per FR-101.
		{"session_days", "storage.retention.session_days", float64(30), "user-only: deletes chat history on a schedule", "unknown config key"},
		{"memory_retros_days", "storage.retention.memory_retros_days", float64(400), "user-only: deletes memories on a schedule", "unknown config key"},
		{"memory_retros_forever", "storage.retention.memory_retros_forever", true, "user-only: deletes data on a schedule", "unknown config key"},
		{"legacy disabled", "storage.retention.disabled", true, "config-file only; no write path at all", "unknown config key"},
		// Performance values (#936, C-7) — user-only: they bound how much an
		// agent may run; rows 5/6 are "already refused today", pinned.
		{"performance.max_parallel_agents", "performance.max_parallel_agents", float64(64), "user-only: bounds what an agent may run", "unknown config key"},
		{"planning.goal_max_rounds", "planning.goal_max_rounds", float64(99), "user-only: bounds what an agent may run", "unknown config key"},
		{"planning.goal_reminder_quiet_seconds", "planning.goal_reminder_quiet_seconds", float64(45), "user-only: bounds what an agent may run", "unknown config key"},
		{"planning.goal_stalled_checkin_enabled", "planning.goal_stalled_checkin_enabled", true, "user-only: bounds what an agent may run", "unknown config key"},
		{"planning.goal_stalled_checkin_minutes", "planning.goal_stalled_checkin_minutes", float64(30), "user-only: bounds what an agent may run", "unknown config key"},
		{"tools.manifest.compressed", "tools.manifest.compressed", false, "user-only: bounds what an agent may run (#936, NEW refusal)", "Settings → Limits"},
		// Max tool calls per turn (#904) — user-only with lowering consent;
		// existing refusal, preserved.
		{"agents.defaults.max_tool_iterations", "agents.defaults.max_tool_iterations", float64(900), "user-only with lowering consent (#904)", "Settings →"},
	}

	for _, tc := range rows {
		t.Run(tc.name, func(t *testing.T) {
			deps, cfg := newTestDeps(t)
			compressedBefore := cfg.Tools.Manifest.Compressed
			iterationsBefore := cfg.Agents.Defaults.MaxToolIterations

			res := systools.NewConfigSetTool(deps).Execute(context.Background(),
				map[string]any{"key": tc.key, "value": tc.value})

			if !res.IsError {
				t.Fatalf("PARITY (FR-101): set_config accepted %q — a Settings value with neither an agent setter "+
					"nor a refusal. Listed reason: %s. Response: %s", tc.key, tc.reason, res.ForLLM)
			}
			if !strings.Contains(res.ForLLM, tc.wantInRefusal) {
				t.Errorf("refusal for %q must contain %q (reason: %s): %s",
					tc.key, tc.wantInRefusal, tc.reason, res.ForLLM)
			}
			// The refusal happens before dotSet, so nothing can land; assert
			// the two representable fields anyway (belt and braces).
			if cfg.Tools.Manifest.Compressed != compressedBefore {
				t.Errorf("refused write changed tools.manifest.compressed")
			}
			if cfg.Agents.Defaults.MaxToolIterations != iterationsBefore {
				t.Errorf("refused write changed agents.defaults.max_tool_iterations")
			}
		})
	}
}

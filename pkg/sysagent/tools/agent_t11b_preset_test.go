// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// RED pack for T11 slice B, "Agents: cannot edit presets by any route"
// (t11 spec §4 Agents row; §8 FR-11).
//
// Spec oracle: "update_agent/create_agent have no path or preset fields, and
// set_config already refuses sandbox.* (Verified: pkg/sysagent/tools/
// config.go::blockedConfigKeys). Agents may read the effective path."
//
// RED today: create_agent still ADVERTISES a cli_path parameter (the legacy
// per-agent program path T11/T8 removes — a person sets the program in
// Settings, never an agent). TestAgentCreateTool_NoPathOrPresetParameters
// fails on it. The update_agent and set_config assertions are pins: they were
// already true before this pack existed and cannot go red without a
// production regression; they are labelled as pins, not counted as RED.
//
// NOTE for the T8 implementer: the create tool's Description() text also
// still tells the model "cli_path is optional" (and agent_type's description
// repeats it) — that text must go with the parameter.
package systools

import (
	"strings"
	"testing"
)

// t11bPathOrPresetKeys returns the tool's advertised parameters that carry a
// program path or preset parameters — the fields an agent must have no route
// to (spec §4 Agents row).
func t11bPathOrPresetKeys(t *testing.T, params map[string]any) []string {
	t.Helper()
	props, _ := params["properties"].(map[string]any)
	var found []string
	for key := range props {
		low := strings.ToLower(key)
		if low == "cli_path" || strings.Contains(low, "preset") {
			found = append(found, key)
		}
	}
	return found
}

func TestAgentCreateTool_NoPathOrPresetParameters(t *testing.T) {
	tool := &AgentCreateTool{}
	found := t11bPathOrPresetKeys(t, tool.Parameters())
	if len(found) > 0 {
		t.Fatalf("create_agent must have no path or preset fields (spec §4 Agents row / FR-11); "+
			"found %v — the program and its parameters are set by a person in Settings → "+
			"Third-party agents, never by an agent", found)
	}
}

// Pin (not RED): update_agent already has no executor/path/preset parameters.
// If this ever fails, an agent-facing preset write path was re-introduced.
func TestAgentUpdateTool_NoPathOrPresetParameters(t *testing.T) {
	tool := &AgentUpdateTool{}
	found := t11bPathOrPresetKeys(t, tool.Parameters())
	if len(found) > 0 {
		t.Fatalf("update_agent must have no path or preset fields (spec §4 Agents row / FR-11); found %v", found)
	}
}

// Pin (not RED): the sandbox subtree — third_party_agents included — is
// already write-blocked for set_config (blockedConfigKeys' "sandbox" subtree
// root). This pins the settings key specifically against a future carve-out.
func TestSetConfig_BlocksThirdPartyAgentSettingsKeys(t *testing.T) {
	for _, key := range []string{
		"sandbox.third_party_agents",
		"sandbox.third_party_agents.clis.codex",
		"sandbox.third_party_agents.clis.codex.path",
		"sandbox.third_party_agents.clis.codex.preset_args",
	} {
		if err := validateConfigKey(nil, key); err == nil {
			t.Errorf("set_config must refuse %q (spec §4 Agents row / FR-11): the preset store is person-only", key)
		}
	}
}

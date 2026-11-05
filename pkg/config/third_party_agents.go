// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

// ThirdPartyAgentsConfig is config.json sandbox.third_party_agents (r17
// §3.4). Greenfield: no migration, nothing seeded, detection never saved.
type ThirdPartyAgentsConfig struct {
	// CLIs is keyed by the agent `cli` enum (claude-code, codex, opencode).
	CLIs map[string]ThirdPartyCLIConfig `json:"clis,omitempty"`
}

// ThirdPartyCLIConfig is one CLI's saved row: the program path exactly as
// the person typed it and the preset parameters exactly as typed. "" means
// nothing saved.
type ThirdPartyCLIConfig struct {
	Path       string `json:"path"`
	PresetArgs string `json:"preset_args"`
}

// ThirdPartyCLI returns the saved row for cli; the zero row when nothing is
// saved. Safe on a nil receiver.
func (s *OmnipusSandboxConfig) ThirdPartyCLI(cli string) ThirdPartyCLIConfig {
	if s == nil || s.ThirdPartyAgents == nil {
		return ThirdPartyCLIConfig{}
	}
	return s.ThirdPartyAgents.CLIs[cli]
}

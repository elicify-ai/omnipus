// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// rest_config_max_tool_iterations.go — keeps the #904 global tool-iteration
// limit and its env-import marker intact across the generic
// PUT /api/v1/config write (spec D8/D15, founder decision D21).
//
// blockedPaths refuses a body that names either leaf, but updateConfig merges
// only ONE level deep: {"agents":{"defaults":{"default_agent_id":"x"}}}
// replaces the whole agents.defaults map, which would silently drop the saved
// global (the next load then runs the shipped default and reports "missing")
// and the marker (the next boot would re-import the retired env var over the
// admin's value). The SPA's own Settings save sends exactly that shape
// (src/lib/api/config.ts::frontendToRawConfig), so this is the everyday path,
// not an edge case.

import (
	"fmt"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// protectedAgentDefaultsLeaves are the agents.defaults members no generic
// config write may change or drop (the leaf names of the matching
// blockedPaths entries).
var protectedAgentDefaultsLeaves = []string{
	strings.TrimPrefix(string(config.AgentsDefaultsMaxToolIterations), "agents.defaults."),
	strings.TrimPrefix(string(config.AgentsDefaultsMaxToolIterationsEnvImported), "agents.defaults."),
}

// agentsDefaultsMap returns config.json's agents.defaults object, or nil
// when agents or agents.defaults is absent or not an object.
func agentsDefaultsMap(m map[string]any) map[string]any {
	agents, _ := m["agents"].(map[string]any)
	if agents == nil {
		return nil
	}
	defaults, _ := agents["defaults"].(map[string]any)
	return defaults
}

// snapshotProtectedAgentDefaults records the protected leaves present in
// config.json before a generic write.
func snapshotProtectedAgentDefaults(m map[string]any) map[string]any {
	saved := map[string]any{}
	defaults := agentsDefaultsMap(m)
	for _, leaf := range protectedAgentDefaultsLeaves {
		if v, ok := defaults[leaf]; ok {
			saved[leaf] = v
		}
	}
	return saved
}

// preserveProtectedAgentDefaults puts the snapshotted leaves back after the
// merge. A write that turned agents or agents.defaults into a non-object
// (null, a scalar, an array) while a protected leaf existed is refused as a
// 400 — it would erase them — and, through safeUpdateConfigJSON's error
// path, writes nothing.
func preserveProtectedAgentDefaults(m map[string]any, saved map[string]any) error {
	if len(saved) == 0 {
		return nil
	}
	defaults := agentsDefaultsMap(m)
	if defaults == nil {
		return &requestRefusalError{msg: fmt.Sprintf(
			"agents and agents.defaults must stay JSON objects: this write would erase %s, "+
				"which is changed only in Settings → Performance",
			config.AgentsDefaultsMaxToolIterations)}
	}
	for leaf, v := range saved {
		defaults[leaf] = v
	}
	return nil
}

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
	"bytes"
	"encoding/json"
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

// protectedAgentDefaultsValues is what config.json's agents.defaults
// carries for the two #904 leaves AS encoding/json BINDS THEM — decoded
// through struct tags identical to config.AgentDefaults', so every key
// spelling the loader would accept (case variants, Unicode simple folds like
// U+017F 'ſ' ≡ 's') lands here exactly as it would on boot. Raw bytes, so an
// invalid stored value (a string, a float) is compared as-is instead of
// failing the decode.
type protectedAgentDefaultsValues struct { // not-wire-format: decode-only probe of config.json on disk; never crosses the gateway/SPA boundary.
	Agents struct {
		Defaults struct {
			MaxToolIterations            json.RawMessage `json:"max_tool_iterations"`
			MaxToolIterationsEnvImported json.RawMessage `json:"max_tool_iterations_env_imported"`
		} `json:"defaults"`
	} `json:"agents"`
}

// protectedAgentDefaultsFingerprint marshals m the way updateConfigJSONLocked
// writes it (sorted keys — the order a later duplicate-fold key wins in) and
// decodes the two protected values out of it.
func protectedAgentDefaultsFingerprint(m map[string]any) (protectedAgentDefaultsValues, error) {
	var v protectedAgentDefaultsValues
	raw, err := json.Marshal(m)
	if err != nil {
		return v, err
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return v, err
	}
	return v, nil
}

// checkProtectedAgentDefaultsUnchanged refuses (400, nothing written) a
// generic config write whose merged result would change the global
// tool-iteration limit or its env-import marker — value-based, so it holds
// even for a key spelling blockedPaths does not recognise (precedent:
// pkg/sysagent/tools/config.go::checkPerformanceOnlyConfigUnchanged).
func checkProtectedAgentDefaultsUnchanged(before protectedAgentDefaultsValues, merged map[string]any) error {
	after, err := protectedAgentDefaultsFingerprint(merged)
	if err != nil {
		return &requestRefusalError{msg: "the agents section of this write does not decode: " + err.Error()}
	}
	b, a := before.Agents.Defaults, after.Agents.Defaults
	if !bytes.Equal(b.MaxToolIterations, a.MaxToolIterations) ||
		!bytes.Equal(b.MaxToolIterationsEnvImported, a.MaxToolIterationsEnvImported) {
		return &requestRefusalError{msg: fmt.Sprintf(
			"this write would change %s or %s, which are changed only in Settings → Performance",
			config.AgentsDefaultsMaxToolIterations, config.AgentsDefaultsMaxToolIterationsEnvImported)}
	}
	return nil
}

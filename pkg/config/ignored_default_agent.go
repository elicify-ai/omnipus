package config

import (
	"encoding/json"

	"github.com/elicify-ai/omnipus/pkg/logger"
)

// ignoredDefaultAgentKey is the config key of the removed default-agent
// setting, now ignored with a warning (omnipus-system-agent FR-120, K17).
const ignoredDefaultAgentKey = "default_agent_id"

// warnIgnoredDefaultAgentKey logs a warning when the raw config still carries
// agents.defaults.default_agent_id. The key has no effect — there is no
// default agent — and it is neither migrated nor rejected (greenfield rule);
// the warning repeats on every load and reload so an operator sees it. A body
// that does not decode is left to the loader, which reports it.
func warnIgnoredDefaultAgentKey(data []byte) {
	var probe struct {
		Agents struct {
			Defaults map[string]json.RawMessage `json:"defaults"`
		} `json:"agents"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return
	}
	raw, present := probe.Agents.Defaults[ignoredDefaultAgentKey]
	if !present {
		return
	}
	logger.WarnCF("config",
		"agents.defaults.default_agent_id is ignored: there is no default agent any more — "+
			"each channel is bound to a workspace and its main agent, and every new chat names its agent",
		map[string]any{"key": "agents.defaults." + ignoredDefaultAgentKey, "value": string(raw)})
}

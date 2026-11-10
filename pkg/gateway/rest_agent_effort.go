// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// rest_agent_effort.go — the agent REST writers' effort fields
// (effort-levels spec FR-029, FR-048, S-50, S-55, S-56, S-58, S-94).

import (
	"strings"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// agentPrimaryPair is the stored (provider, model) of the agent primary.
func agentPrimaryPair(ac *config.AgentConfig) (string, string) {
	if ac == nil || ac.Model == nil {
		return "", ""
	}
	return strings.TrimSpace(ac.Model.Provider), strings.TrimSpace(ac.Model.Primary)
}

// updateEffort stores a sent primary effort as written (never catalog
// checked), or maps the stored effort onto a changed pair when none was sent
// (S-58); a target that cannot turn reasoning off refuses the whole save.
func (rp *restAPIUpdateAgentPersistAgent) updateEffort(agentRec *config.AgentConfig, providerBefore, modelBefore string) error {
	if sent := rp.ru.req.ReasoningEffort; sent != nil {
		agentRec.ReasoningEffort = *sent
		return nil
	}
	provider, model := agentPrimaryPair(agentRec)
	if provider == providerBefore && model == modelBefore {
		return nil
	}
	mapped, refusal := mapEffortOnModelChange(rp.ru.a.effortCatalog(), agentRec.ReasoningEffort, provider, model)
	if refusal != "" {
		return &requestRefusalError{msg: refusal}
	}
	agentRec.ReasoningEffort = mapped
	return nil
}

// fallbackWithEffort builds one stored fallback entry: a sent effort is kept
// as written; an omitted one keeps the effort stored for the same pair
// (omission never clears, S-94).
func fallbackWithEffort(model, provider string, sent *string, stored config.FallbackModelSlice) config.FallbackModel {
	entry := config.FallbackModel{Model: model, Provider: provider}
	if sent != nil {
		entry.ReasoningEffort = *sent
		return entry
	}
	for _, old := range stored {
		if old.Model == model && old.Provider == provider {
			entry.ReasoningEffort = old.ReasoningEffort
			break
		}
	}
	return entry
}

// wireEffort is the read echo of a stored effort: nil when Not set.
func wireEffort(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}

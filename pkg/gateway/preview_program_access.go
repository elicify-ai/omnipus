// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/preview"
)

// refuseNewProgramAccess is the save-time check of office preview spec §3.2:
// candidate is measured against the configuration in force, so an entry
// already in force is not refused. The refusal is a requestRefusalError, so a
// handler returns its text to the person instead of "could not save config".
func (a *restAPI) refuseNewProgramAccess(candidate *config.Config) error {
	var published *config.Config
	if a.agentLoop != nil {
		published = a.agentLoop.GetConfig()
	}
	return a.refuseProgramAccessAgainst(published, candidate)
}

// refuseProgramAccessAgainst is refuseNewProgramAccess measured against a
// configuration the caller already holds. A caller inside
// AgentLoop.MutateConfig must use it: GetConfig would wait forever on the lock
// that call holds.
func (a *restAPI) refuseProgramAccessAgainst(published, candidate *config.Config) error {
	if err := preview.RefuseNewProgramAccess(a.homePath, published, candidate); err != nil {
		return &requestRefusalError{msg: err.Error()}
	}
	return nil
}

// refuseReloadProgramAccess is the reload-time check of the same rule. With no
// running REST API there is nothing to measure against, so every entry counts
// as new.
func refuseReloadProgramAccess(candidate *config.Config, running *services) error {
	if running != nil && running.restAPIRef != nil {
		return running.restAPIRef.refuseNewProgramAccess(candidate)
	}
	return preview.RefuseNewProgramAccess(config.OmnipusHomeDir(), nil, candidate)
}

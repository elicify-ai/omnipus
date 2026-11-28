// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

import (
	"errors"
	"slices"

	"github.com/elicify-ai/omnipus/pkg/agentmutation"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/programs"
)

// applyProgramsArg sets the connected programs switched on for the agent
// (connected-programs FR-007, AG-07): the full list replaces the stored one,
// with the same conflict refusals as the agent profile (S-12 private
// variable, S-85 git host). Without a credential store there are no private
// variables to conflict with.
func (t *AgentPermissionsTool) applyProgramsArg(a *config.AgentConfig, args map[string]any) error {
	raw, present := args["programs"]
	if !present {
		return nil
	}
	vals, err := requiredStringArray(raw, "programs")
	if err != nil {
		return err
	}
	next := make([]string, 0, len(vals))
	for _, id := range vals {
		if !slices.Contains(next, id) {
			next = append(next, id)
		}
	}
	var cfg *config.Config
	if t.deps != nil && t.deps.GetCfg != nil {
		cfg = t.deps.GetCfg()
	}
	var privateVars []string
	if t.deps != nil && t.deps.CredStore != nil {
		inventory, ierr := t.deps.CredStore.CardInventory()
		if ierr != nil {
			return fieldErr("programs", "the credential store could not be read: "+ierr.Error())
		}
		privateVars = programs.SelectedPrivateVariables(inventory, a.ID)
	}
	if cerr := programs.SwitchConflict(programsConfigFor(cfg, a), a.ID, a.Programs, next, privateVars); cerr != nil {
		var verr *programs.ValidationError
		if errors.As(cerr, &verr) {
			return &agentmutation.FieldError{Code: agentmutation.InvalidInput, Fields: []string{"programs"},
				Reason: verr.Message, Message: verr.Message}
		}
		return cerr
	}
	if len(next) == 0 {
		a.Programs = nil
		return nil
	}
	a.Programs = next
	return nil
}

// programsConfigFor is cfg with the agent's stored record in its roster, so
// the conflict messages name the agent even when the live roster has not
// loaded it yet.
func programsConfigFor(cfg *config.Config, a *config.AgentConfig) *config.Config {
	view := config.Config{}
	if cfg != nil {
		view.Programs = cfg.Programs
		view.Agents.List = slices.Clone(cfg.Agents.List)
	}
	idx := slices.IndexFunc(view.Agents.List, func(x config.AgentConfig) bool { return x.ID == a.ID })
	if idx < 0 {
		view.Agents.List = append(view.Agents.List, *a)
	} else {
		view.Agents.List[idx] = *a
	}
	return &view
}

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package programs

import (
	"slices"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
)

// SelectedPrivateVariables lists the names of the agent's selected private
// variables, in any workspace (the S-12 Given), from the store inventory.
func SelectedPrivateVariables(inventory []credentials.CardInventoryEntry, agentID string) []string {
	var out []string
	for _, item := range inventory {
		r := item.Secret.Recipient
		if item.Plain || r.Kind != credentials.KindAgentPrivate || r.AgentID != agentID || !item.Selected {
			continue
		}
		if !slices.Contains(out, r.Field) {
			out = append(out, r.Field)
		}
	}
	return out
}

// SwitchConflict checks the agent's next programs list against the conflict
// rules of FR-042 (S-12, S-85) and returns a conflict ValidationError with the
// spec message, or nil. Only entries being switched on are checked against
// private variables; every pair of switched-on entries is checked for a shared
// git host, naming the entry that is being added. Unknown ids are refused as
// invalid input.
func SwitchConflict(cfg *config.Config, agentID string, current, next []string, privateVars []string) error {
	entries, err := Entries(cfg)
	if err != nil {
		return err
	}
	byID := map[string]ProgramEntry{}
	for _, e := range entries {
		byID[e.ID] = e
	}
	agent := AgentName(cfg, agentID)
	if len(next) > MaxProgramsOnAgent {
		return invalid("%s can have at most %d programs switched on.", agent, MaxProgramsOnAgent)
	}
	var on []ProgramEntry
	for _, id := range next {
		e, ok := byID[id]
		if !ok {
			return invalid("%s is not a program in Settings → Programs.", id)
		}
		on = append(on, e)
	}
	for _, e := range on {
		if slices.Contains(current, e.ID) {
			continue
		}
		for _, v := range e.TokenVariables {
			if slices.Contains(privateVars, v) {
				return conflict("%s cannot be switched on for %s: %s already has a private variable %s. Remove that variable, or leave %s off.",
					e.DisplayName, agent, agent, v, e.DisplayName)
			}
		}
	}
	return GitHostConflict(on, current, agent)
}

// GitHostConflict refuses two switched-on entries that supply the git login
// for the same canonical host (FR-042). The message names the entry that is
// not already on as the one being switched on.
func GitHostConflict(on []ProgramEntry, current []string, agent string) error {
	for i, a := range on {
		if a.Git == nil {
			continue
		}
		for _, b := range on[i+1:] {
			if b.Git == nil || a.Git.Host != b.Git.Host {
				continue
			}
			kept, added := a, b
			if !slices.Contains(current, b.ID) && slices.Contains(current, a.ID) {
				kept, added = a, b
			} else if slices.Contains(current, b.ID) && !slices.Contains(current, a.ID) {
				kept, added = b, a
			}
			return conflict("%s cannot be switched on for %s: %s already supplies the git login for %s to %s. Switch one of them off.",
				added.DisplayName, agent, kept.DisplayName, a.Git.Host, agent)
		}
	}
	return nil
}

// EditGitHostConflict refuses an edit that gives an entry the git host of
// another entry switched on for one of the same agents (FR-042, S-85).
func EditGitHostConflict(cfg *config.Config, edited config.ProgramEntryConfig) error {
	if edited.Git == nil {
		return nil
	}
	entries, err := Entries(cfg)
	if err != nil {
		return err
	}
	for _, agentID := range EnabledAgentIDs(cfg, edited.ID) {
		for _, a := range cfg.Agents.List {
			if a.ID != agentID {
				continue
			}
			for _, other := range entries {
				if other.ID == edited.ID || other.Git == nil || other.Git.Host != edited.Git.Host ||
					!slices.Contains(a.Programs, other.ID) {
					continue
				}
				agent := AgentName(cfg, agentID)
				return conflict("%s cannot use the git host %s: %s already supplies the git login for %s to %s. Switch one of them off first.",
					edited.DisplayName, edited.Git.Host, other.DisplayName, edited.Git.Host, agent)
			}
		}
	}
	return nil
}

// PrivateVariableConflict refuses a private variable whose name is a token
// variable of a program switched on for the agent (S-13).
func PrivateVariableConflict(cfg *config.Config, agentID, variable string) error {
	if cfg == nil {
		return nil
	}
	entries, err := Entries(cfg)
	if err != nil {
		return err
	}
	var enabled []string
	for _, a := range cfg.Agents.List {
		if a.ID == agentID {
			enabled = a.Programs
		}
	}
	for _, e := range entries {
		if !slices.Contains(enabled, e.ID) || !slices.Contains(e.TokenVariables, variable) {
			continue
		}
		return conflict("%s is used by the program %s, which is switched on for this agent. Switch %s off first, or use another name.",
			variable, e.DisplayName, e.DisplayName)
	}
	return nil
}

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agentstore

// Skills spec r15 §5.13a (SD-30) one write rule: every agent create/update
// persist writes skill_grants wholesale from its own persist-time
// observation of <home>/skills — an installed granted name binds its
// folder's generation (an unstamped folder is stamped fresh, then bound), an
// absent name persists unbound. Requests stay name-only; any skill_grants a
// caller supplies is replaced.
//
// Lock order: the skills mutation lock is taken BEFORE the entity lock, on
// every path (withSkillsLock wraps the whole entity write). Code that already
// holds the skills mutation lock (the skills pipeline's best-effort grant
// removal, built-in retirement) must therefore never call these persist
// paths: the lock is not re-entrant.

import (
	"path/filepath"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/skills"
)

// skillsRoot is the registry folder the bindings observe.
func (s *Store) skillsRoot() string { return filepath.Join(s.home, "skills") }

// withSkillsLock runs fn under the skills mutation lock of this home.
func (s *Store) withSkillsLock(fn func() error) error {
	return skills.WithMutationLock(s.skillsRoot(), fn)
}

// bindSkillGrants replaces agent.SkillGrants with the persist-time
// observation. The caller holds the skills mutation lock.
func (s *Store) bindSkillGrants(agent *config.AgentConfig) error {
	bindings, err := skills.BindGrants(s.skillsRoot(), agent.Skills)
	if err != nil {
		return err
	}
	agent.SkillGrants = bindings
	return nil
}

// bindingMutate wraps a caller's mutate so the wholesale binding runs on the
// mutated record, inside the same entity write.
func (s *Store) bindingMutate(mutate func(*config.AgentConfig) error) func(*config.AgentConfig) error {
	return func(agent *config.AgentConfig) error {
		if mutate != nil {
			if err := mutate(agent); err != nil {
				return err
			}
		}
		return s.bindSkillGrants(agent)
	}
}

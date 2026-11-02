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
	"fmt"
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

// RemoveSkillGrant removes name (by skill key) from the agent's skills list
// and drops its binding, changing nothing else (§8.2 GrantEditor.RemoveGrant,
// §5.6 Q-S3 grant hygiene). It does NOT rebind the remaining names and does
// NOT take the skills mutation lock, so it is the one agent persist the
// skills pipeline and built-in retirement may call while holding that lock.
// changed is false (and nothing is written) when the agent holds no grant
// for the key.
func (s *Store) RemoveSkillGrant(id, name string) (changed bool, err error) {
	current, err := s.Get(id)
	if err != nil {
		return false, err
	}
	if !holdsSkill(current, name) {
		return false, nil
	}
	key := skills.SkillKey(name)
	updated, err := s.inner.Update(id, func(agent *config.AgentConfig) error {
		kept := agent.Skills[:0:0]
		for _, granted := range agent.Skills {
			if skills.SkillKey(granted) == key {
				changed = true
				continue
			}
			kept = append(kept, granted)
		}
		agent.Skills = kept
		if _, bound := agent.SkillGrants[key]; bound {
			changed = true
			delete(agent.SkillGrants, key)
		}
		if len(agent.SkillGrants) == 0 {
			agent.SkillGrants = nil
		}
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("agentstore: remove skill grant %q from %q: %w", name, id, err)
	}
	if s.notifier != nil {
		s.notifier.AgentUpserted(id, updated)
	}
	return changed, nil
}

// AddSkillBindings binds the given skill keys (key → generation) for agent
// id, changing nothing else: a key is bound only when the agent's list still
// holds the name and the record has no binding for it yet. Like
// RemoveSkillGrant it does NOT rebind the remaining names (so a user-skill
// grant saved while its folder was absent stays unbound, SD-30) and does NOT
// take the skills mutation lock. It returns the bindings it added (empty,
// and no write, when there is nothing to add).
func (s *Store) AddSkillBindings(id string, add map[string]string) (map[string]string, error) {
	current, err := s.Get(id)
	if err != nil {
		return nil, err
	}
	if len(missingBindings(current, add)) == 0 {
		return map[string]string{}, nil
	}
	var added map[string]string
	updated, err := s.inner.Update(id, func(agent *config.AgentConfig) error {
		added = missingBindings(agent, add)
		if len(added) == 0 {
			return nil
		}
		if agent.SkillGrants == nil {
			agent.SkillGrants = map[string]string{}
		}
		for key, gen := range added {
			agent.SkillGrants[key] = gen
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("agentstore: bind skill grants of %q: %w", id, err)
	}
	if len(added) > 0 && s.notifier != nil {
		s.notifier.AgentUpserted(id, updated)
	}
	return added, nil
}

// missingBindings is the subset of add whose key agent's list holds and whose
// binding the agent does not have yet.
func missingBindings(agent *config.AgentConfig, add map[string]string) map[string]string {
	var out map[string]string
	for key, gen := range add {
		if gen == "" || !holdsSkill(agent, key) {
			continue
		}
		if _, bound := agent.SkillGrants[key]; bound {
			continue
		}
		if out == nil {
			out = map[string]string{}
		}
		out[key] = gen
	}
	return out
}

// holdsSkill reports whether agent holds name by skill key, in its list or
// its bindings.
func holdsSkill(agent *config.AgentConfig, name string) bool {
	key := skills.SkillKey(name)
	for _, granted := range agent.Skills {
		if skills.SkillKey(granted) == key {
			return true
		}
	}
	_, bound := agent.SkillGrants[key]
	return bound
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

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

// Skills spec r15 §5.13a (SD-30) authorization: the construction-time
// allowlist gates a registry/builtin candidate by name, and the identity half
// at the same point requires the agent's binding for the key to equal the
// candidate folder's current generation. A binding that matches nothing (the
// skill was deleted or recreated, or the record predates the field) and an
// unstamped folder authorize nothing. The menu, resolution and the skill-name
// list all use the same predicate, so a stale grant is neither advertised nor
// resolvable. Project-shelf resolution is untouched (the mount is the grant).

import (
	"path/filepath"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/skills"
)

// HoldsSkillName reports whether name is in the agent's construction-time
// allowlist — the name half alone. The Skill load uses it to tell a stale
// grant (held by name, refused by identity: SKILL_GRANT_STALE) from a skill
// the agent was never granted.
func (cb *ContextBuilder) HoldsSkillName(name string) bool { return cb.skillAllowed(name) }

// grantedPredicate returns the registry/builtin authorization predicate for
// one scan of the installed skills. A ContextBuilder without an installed
// binding table (WithSkillBindings never called — no agent identity) applies
// the allowlist alone; every agent instance installs its table.
func (cb *ContextBuilder) grantedPredicate(all []skills.SkillInfo) func(string) bool {
	if cb.skillBindings == nil {
		return cb.skillAllowed
	}
	dirs := make(map[string]string, len(all))
	for _, s := range all {
		key := skills.SkillKey(s.ID)
		if _, seen := dirs[key]; !seen {
			dirs[key] = filepath.Dir(s.Path)
		}
	}
	return func(name string) bool {
		if !cb.skillAllowed(name) {
			return false
		}
		key := skills.SkillKey(name)
		dir, ok := dirs[key]
		if !ok {
			return false
		}
		gen, err := skills.ReadGeneration(dir)
		if err != nil {
			logger.WarnCF("agent", "skill identity unreadable; skill not authorized",
				map[string]any{"agent_id": cb.agentID, "skill": name, "error": err.Error()})
			return false
		}
		return gen != "" && cb.skillBindings[key] == gen
	}
}

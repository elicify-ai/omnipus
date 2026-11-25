// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

// The installed-skill outcome of one agent (C-11) with its standing consent
// (C-12): a timed allowance in the agent-keyed store settles the permission
// part, a person's approval of the current revision settles the trust part.
// File authority (E6/E7) and Full injection (E8) read it from disk and the
// process store only, so the answer is the same for every session of the
// agent.

import (
	"errors"
	"path/filepath"
	"sync/atomic"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/skills"
)

var skillAllowanceStore atomic.Pointer[security.ApprovalGrantStore]

// SetSkillAllowanceStore registers the process-wide timed allowance store
// (the agent loop's ApprovalGrantStore) so skill decisions made outside a
// session see its skill allowances. nil unregisters it.
func SetSkillAllowanceStore(s *security.ApprovalGrantStore) {
	skillAllowanceStore.Store(s)
}

// SkillAllowanceActive reports a running timed allowance of skillKey for
// agentID in the registered store.
func SkillAllowanceActive(agentID, skillKey string) bool {
	return skillAllowanceStore.Load().IsSkillAllowed(agentID, config.NormalizeSkillKey(skillKey))
}

// InstalledSkillDecision is the C-11 decision for one installed skill and
// one agent, read from home: the stored values, the C-5 verification and
// the trust approval of the current revision.
func InstalledSkillDecision(home string, settings config.PersistedSkillSettings, agentSkills *config.AgentSkillsCfg, slug string, granted bool) (SkillDecision, error) {
	key := config.NormalizeSkillKey(slug)
	skillsDir := filepath.Join(home, "skills")
	verified, revision, err := skills.SkillVerified(skillsDir, slug)
	if err != nil {
		return SkillDecision{Outcome: SkillOutcomeDeny, Reason: SkillDenyPermission}, err
	}
	approved := false
	if !verified {
		if approved, err = skills.TrustApproved(skillsDir, key, revision); err != nil {
			return SkillDecision{Outcome: SkillOutcomeDeny, Reason: SkillDenyTrust}, err
		}
	}
	in := SkillDecisionInput{
		Key: key, Granted: granted, Global: config.ToolPolicy(settings.Policies[key]),
		Trust: settings.Trust, Verified: verified, GodMode: settings.GodMode, TrustApproved: approved,
	}
	if agentSkills != nil {
		in.Agent = agentSkills.Policies[key]
	}
	d := ResolveSkillDecision(in)
	d.Revision, d.Verified = revision, verified
	return d, nil
}

// SkillLoadDecision is the C-11 decision of one skill load: a project
// skill (folder is its directory) or an installed one.
func SkillLoadDecision(home string, settings config.PersistedSkillSettings, agentSkills *config.AgentSkillsCfg, key, slug, folder string, project bool) (SkillDecision, error) {
	if project {
		return projectSkillDecision(home, settings, agentSkills, key, folder)
	}
	return InstalledSkillDecision(home, settings, agentSkills, slug, true)
}

// skillUsableWithoutCard reports whether an agent may use the skill now
// without a new answer: the outcome is allow, or the only open check is the
// permission part and the agent holds a running timed allowance for it.
func skillUsableWithoutCard(agentID, key string, d SkillDecision) bool {
	switch {
	case d.Outcome == SkillOutcomeAllow:
		return true
	case d.Outcome != SkillOutcomeAskPending || d.TrustOpen:
		return false
	default:
		return SkillAllowanceActive(agentID, key)
	}
}

// filterLoadableByOutcome keeps the granted skills whose folders the agent
// may read and run this turn (E6/E7): outcome allowed, or an Ask settled by
// standing consent. A home with no skill values on either side (no
// sandbox.skill_policies: not upgraded yet) keeps the grant-only rule. An unreadable decision leaves the folder
// out (fail closed) and is logged.
func filterLoadableByOutcome(home, agentID string, settings config.PersistedSkillSettings, agentSkills *config.AgentSkillsCfg, granted []string) []string {
	if settings.Policies == nil && (agentSkills == nil || len(agentSkills.Policies) == 0) {
		return granted
	}
	var out []string
	for _, slug := range granted {
		d, err := InstalledSkillDecision(home, settings, agentSkills, slug, true)
		if errors.Is(err, skills.ErrNotFound) {
			continue // not installed: LoadableFolders would skip it too
		}
		if err != nil {
			logger.WarnCF("tools", "skills authority: skill decision unreadable; its folder is not readable this turn",
				map[string]any{"agent_id": agentID, "skill": slug, "error": err.Error()})
			continue
		}
		if skillUsableWithoutCard(agentID, config.NormalizeSkillKey(slug), d) {
			out = append(out, slug)
		}
	}
	return out
}

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

// Skills & Tools C-11 E4 and C-12 row 5: the person's /name settles an
// ordinary Ask (asking by name is the consent), but not Deny and not an
// open trust check. Before the turn's first model call each skill /name
// forced goes through the skill's own decision: Deny keeps its
// instructions out; an open trust check raises the trust card first and
// keeps them out unless the person approves this version.

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/skills"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// settleSlashSkills drops from ts.opts.ForcedSkills every skill the
// person's /name may not load this turn. ctx is the turn's context.
func (al *AgentLoop) settleSlashSkills(ctx context.Context, ts *turnState) {
	if ts == nil || ts.agent == nil || ts.agent.ContextBuilder == nil || len(ts.opts.ForcedSkills) == 0 {
		return
	}
	cfg := al.GetConfig()
	if cfg == nil {
		return
	}
	agentSkills := agentSkillsCfg(cfg, ts.agentID)
	if cfg.Sandbox.SkillPolicies == nil && (agentSkills == nil || len(agentSkills.Policies) == 0) {
		return // not upgraded yet: the grant decides, as for a Skill load
	}
	kept := make([]string, 0, len(ts.opts.ForcedSkills))
	for _, name := range ts.opts.ForcedSkills {
		if al.slashSkillLoads(ctx, ts, cfg, agentSkills, name) {
			kept = append(kept, name)
		}
	}
	ts.opts.ForcedSkills = kept
}

// slashSkillLoads reports whether the skill /name forced may load.
func (al *AgentLoop) slashSkillLoads(
	ctx context.Context, ts *turnState, cfg *config.Config, agentSkills *config.AgentSkillsCfg, name string,
) bool {
	resolved, ok := ts.agent.ContextBuilder.ResolveSkillFullForWorkspace(ts.opts.WorkspaceID, name)
	if !ok {
		return true // not granted or not found: the context build leaves it out, as before
	}
	project := resolved.Shelf == skills.ShelfProject
	key := config.NormalizeSkillKey(resolved.Slug)
	if project {
		key = config.ProjectSkillKey(ts.opts.WorkspaceID, resolved.Slug)
	}
	fields := map[string]any{"agent_id": ts.agentID, "skill": key}
	d, err := tools.SkillLoadDecision(omnipusHome(), skillSettingsOf(cfg), agentSkills, key, resolved.Slug, filepath.Dir(resolved.Path), project)
	if err != nil {
		fields["error"] = err.Error()
		logger.ErrorCF("agent", "/name skill: decision unreadable; the skill is not loaded", fields)
		return false
	}
	switch d.Outcome {
	case tools.SkillOutcomeDeny:
		fields["reason"] = d.Reason
		logger.InfoCF("agent", "/name skill: set to Deny; the skill is not loaded", fields)
		return false
	case tools.SkillOutcomeAskPending:
		if !d.TrustOpen {
			return true // the person asking by name settles the permission check
		}
		if ts.opts.AutoDenyAsk {
			logger.InfoCF("agent", "/name skill: trust check open in a run nobody can answer; the skill is not loaded", fields)
			return false
		}
		card := newSkillLoadCard(al, ts.agent.ContextBuilder, ts.agentID, resolved, key, project, d, false)
		callID := fmt.Sprintf("slash-skill-%s-%s", ts.turnID, resolved.Slug)
		approved, denialReason, _ := al.CheckGrantOrRequestApproval(
			withSkillCard(ctx, card), ts.transcriptSessionID, ts.agentID, "Skill", callID, ts.turnID,
			map[string]any{"name": resolved.Slug},
		)
		if !approved {
			fields["denial_reason"] = denialReason
			logger.InfoCF("agent", "/name skill: trust card not approved; the skill is not loaded", fields)
			return false
		}
	}
	return true
}

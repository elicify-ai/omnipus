// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

// Skills & Tools C-11 E1 and C-12: a Skill load goes through the skill's own
// decision for the acting agent before the generic approval path. Deny
// refuses the load; an open permission check becomes an Ask that the usual
// order settles (Auto-approve, a timed allowance, the card, or the
// unattended refusal); an open trust check is an Ask that only a person's
// trust approval settles, so Auto-approve and allowances never run it.

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/skills"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

const skillLoadDeniedReason = "skill_load_denied"

// composeSkillLoadPolicy folds the C-11 decision of the skill a Skill load
// names into ex.toctouPolicy. It returns the refusal text when the decision
// is deny ("" otherwise).
func (ex *agentLoopRunTurnToolsExecute) composeSkillLoadPolicy() string {
	ex.skillTrustOpen = false
	ex.skillCard = nil
	if ex.toolName != "Skill" {
		return ""
	}
	rt := ex.rx.rr.rq.ri.rf.rt
	slug, _ := ex.toolArgs["name"].(string)
	if strings.TrimSpace(slug) == "" || rt.ts == nil || rt.ts.agent == nil || rt.ts.agent.ContextBuilder == nil {
		return "" // a search, or nothing to decide: the load reports it
	}
	cfg := rt.al.GetConfig()
	if cfg == nil {
		return ""
	}
	agentSkills := agentSkillsCfg(cfg, rt.ts.agentID)
	if cfg.Sandbox.SkillPolicies == nil && (agentSkills == nil || len(agentSkills.Policies) == 0) {
		return "" // not upgraded yet: the grant and the Skill tool decide, as before
	}
	resolved, ok := rt.ts.agent.ContextBuilder.ResolveSkillFullForWorkspace(rt.ts.opts.WorkspaceID, slug)
	if !ok {
		return "" // not granted or not found: the load refuses it with its own text
	}
	settings := skillSettingsOf(cfg)
	project := resolved.Shelf == skills.ShelfProject
	key := config.NormalizeSkillKey(resolved.Slug)
	if project {
		key = config.ProjectSkillKey(rt.ts.opts.WorkspaceID, resolved.Slug)
	}
	d, err := tools.SkillLoadDecision(omnipusHome(), settings, agentSkills, key, resolved.Slug, filepath.Dir(resolved.Path), project)
	if err != nil {
		logger.ErrorCF("agent", "skill load: decision unreadable; the load is refused",
			map[string]any{"agent_id": rt.ts.agentID, "skill": resolved.Slug, "error": err.Error()})
		ex.toctouPolicy = string(config.ToolPolicyDeny)
		return fmt.Sprintf("`%s` could not be checked, so it was not loaded.", resolved.Slug)
	}
	switch d.Outcome {
	case tools.SkillOutcomeDeny:
		ex.toctouPolicy = string(config.ToolPolicyDeny)
		if project {
			return tools.ProjectSkillRefusalText(resolved.Slug, tools.WorkspaceLabel(omnipusHome(), rt.ts.opts.WorkspaceID), d.Reason)
		}
		return tools.SkillRefusalText(resolved.Slug, d.Reason)
	case tools.SkillOutcomeAskPending:
		// A running timed allowance settles the permission check.
		permissionOpen := d.PermissionOpen && !rt.al.ApprovalGrants().IsSkillAllowed(rt.ts.agentID, config.NormalizeSkillKey(key))
		if !d.TrustOpen && !permissionOpen {
			return ""
		}
		ex.skillTrustOpen = d.TrustOpen
		ex.skillCard = ex.skillLoadCard(resolved, key, project, d, permissionOpen)
		ex.toctouPolicy = stricterPolicy(ex.toctouPolicy, string(config.ToolPolicyAsk))
	}
	return ""
}

// resolvedSkillDecision is the C-11 decision of a resolved skill for agentID
// in workspaceID, read from the live config exactly as a Skill load reads it
// (composeSkillLoadPolicy): tools.SkillLoadDecision, so the one decision
// function tools.ResolveSkillDecision. decided is false with no loaded config
// or when neither side carries skill values yet (not upgraded): the grant
// alone decides, as for a Skill load.
func (al *AgentLoop) resolvedSkillDecision(agentID, workspaceID string, resolved skills.ResolvedSkill) (d tools.SkillDecision, decided bool, err error) {
	cfg := al.GetConfig()
	if cfg == nil {
		return tools.SkillDecision{}, false, nil
	}
	agentSkills := agentSkillsCfg(cfg, agentID)
	if cfg.Sandbox.SkillPolicies == nil && (agentSkills == nil || len(agentSkills.Policies) == 0) {
		return tools.SkillDecision{}, false, nil
	}
	project := resolved.Shelf == skills.ShelfProject
	key := config.NormalizeSkillKey(resolved.Slug)
	if project {
		key = config.ProjectSkillKey(workspaceID, resolved.Slug)
	}
	d, err = tools.SkillLoadDecision(omnipusHome(), skillSettingsOf(cfg), agentSkills, key, resolved.Slug, filepath.Dir(resolved.Path), project)
	return d, true, err
}

// skillSearchInputs gives ToolSearch's skill search (C-17) the live values a
// Skill load decides from (composeSkillLoadPolicy): the global skill
// settings and the agent's own skill values. ok is false with no loaded
// config.
func (al *AgentLoop) skillSearchInputs(agentID string) (config.PersistedSkillSettings, *config.AgentSkillsCfg, bool) {
	cfg := al.GetConfig()
	if cfg == nil {
		return config.PersistedSkillSettings{}, nil, false
	}
	return skillSettingsOf(cfg), agentSkillsCfg(cfg, agentID), true
}

// agentSkillsCfg returns the agent's own skill values from the live config.
func agentSkillsCfg(cfg *config.Config, agentID string) *config.AgentSkillsCfg {
	for i := range cfg.Agents.List {
		a := &cfg.Agents.List[i]
		if a.ID == agentID && a.Tools != nil {
			return a.Tools.Skills
		}
	}
	return nil
}

// denySkillLoad records a refused Skill load (deny outcome) for the model.
func (ex *agentLoopRunTurnToolsExecute) denySkillLoad(tc providers.ToolCall, text string) agentLoopRunTurnToolsExecuteFlow {
	rt := ex.rx.rr.rq.ri.rf.rt
	cls, _ := ClassifyDenial("policy_denied")
	cls.Reason = skillLoadDeniedReason
	cls.ModelMessage = text + " " + cls.ModelMessage
	denyMsg := denialPayloadJSON(ex.toolName, skillLoadDeniedReason, cls)
	rt.al.emitPolicyDenyAudit(rt.ts, ex.toolName, "deny", skillLoadDeniedReason)
	if err := ex.admitAndCheckpoint(toolResultAdmission{
		Tool: tc.Name, ToolCallID: tc.ID, Content: denyMsg, IsError: true, ParallelN: len(ex.rx.rr.normalizedToolCalls),
	}); err != nil {
		return ex.contextWindowExit(err)
	}
	rt.al.emitEvent(
		EventKindToolExecSkipped,
		rt.ts.eventMeta("runTurn", "turn.tool.skipped"),
		ToolExecSkippedPayload{Tool: ex.toolName, Reason: "permission_denied (" + skillLoadDeniedReason + ")"},
	)
	if used, exhausted := rt.ts.recordToolDenial(ex.ledgerToolName, skillLoadDeniedReason, false, denyMsg); exhausted {
		ex.rx.rr.rq.ri.turnStatus = TurnEndStatusAborted
		ex.rx.ret0, ex.rx.ret1 = rt.al.abortTurnForToolDenialBudget(rt.ts, ex.ledgerToolName, skillLoadDeniedReason, used)
		ex.ret0 = agentLoopRunTurnToolsReturn
		return agentLoopRunTurnToolsExecuteReturn
	}
	return agentLoopRunTurnToolsExecuteContinue
}

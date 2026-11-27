// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

// Skills & Tools C-11 E5 and C-12: a delegation's requested_skill is a load
// the delegation does FOR the child, so it consumes the child's one outcome
// (team-lead ruling, Reading A). SteerLauncher.Launch runs this gate after
// the grant check and before any write:
//
//	deny          refused; the refusal names the setting
//	ask_pending   a timed allowance or Auto-approve settles the permission
//	              check, never the trust check; what stays open raises the
//	              Skill card in the steering chat (so the root chat), and a
//	              card nobody approves refuses the delegation. An unattended
//	              chain never asks: refused.
//	allowed       launched
//
// An allowed or approved skill is loaded for the child: its instructions
// are part of the child's first message, so its first turn begins with the
// skill loaded (the delegate tool's requested_skill promise).

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/skills"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// requestedSkillPreload is a requested skill loaded for the child before
// its session exists: the instructions and what the load records once the
// child's first message is written.
type requestedSkillPreload struct {
	agentID     string
	workspaceID string
	slug        string
	shelf       skills.SkillShelf
	revision    string
	content     string
}

// firstMessage returns the child's first message with the skill's
// instructions after the task.
func (p *requestedSkillPreload) firstMessage(task string) string {
	if p == nil {
		return task
	}
	section := fmt.Sprintf("## Skill `%s`, loaded for this task\n\n%s", p.slug, strings.TrimSpace(p.content))
	if strings.TrimSpace(task) == "" {
		return section
	}
	return task + "\n\n" + section
}

// recordLoaded records the load for the child's session once its first
// message is written: the seen revision (as a Skill load records it) and
// the audit row.
func (p *requestedSkillPreload) recordLoaded(al *AgentLoop, childID string) {
	if p == nil {
		return
	}
	if p.shelf == skills.ShelfRegistry && p.revision != "" {
		skills.RecordSeen(childID, p.slug, p.revision)
	} else {
		skills.ForgetSeen(childID, p.slug)
	}
	audit.EmitSkillCall(al.auditLogger, p.agentID, p.workspaceID, p.slug,
		audit.SkillCallModeLoad, audit.SkillCallOutcomeLoaded, string(p.shelf))
}

// requestedSkillRefused is the refusal of a delegation by its requested
// skill: text names the reason and says nothing was started.
func requestedSkillRefused(targetAgentID, text string) error {
	return &tools.RequestedSkillRefusalError{
		Text: fmt.Sprintf("delegate: requested_skill for %q refused: %s Nothing was started.", targetAgentID, text),
	}
}

// requestedSkillGate decides the requested skill resolved (granted to the
// child) for the child from its one C-11 outcome. It returns the load to
// seed into the child, or the refusal. The workspace is the launch's own,
// or the steering session's for a steered launch (launchSteered inherits
// it the same way).
func (l *SteerLauncher) requestedSkillGate(
	ctx context.Context, req steer.LaunchRequest, child *AgentInstance, resolved skills.ResolvedSkill,
) (*requestedSkillPreload, error) {
	workspaceID := req.WorkspaceID
	if req.SteeringSessionID != "" {
		meta, err := l.steeringMeta(req.SteeringSessionID)
		if err != nil {
			return nil, err
		}
		workspaceID = meta.WorkspaceID
	}
	project := resolved.Shelf == skills.ShelfProject
	key := config.NormalizeSkillKey(resolved.Slug)
	if project {
		key = config.ProjectSkillKey(workspaceID, resolved.Slug)
	}
	d, decided, err := l.al.resolvedSkillDecision(req.TargetAgentID, workspaceID, resolved)
	if err != nil {
		logger.ErrorCF("agent", "delegate: requested_skill decision unreadable; the delegation is refused",
			map[string]any{"agent_id": req.TargetAgentID, "skill": resolved.Slug, "error": err.Error()})
		return nil, fmt.Errorf("steer: launch: %w: %w",
			requestedSkillRefused(req.TargetAgentID, fmt.Sprintf("`%s` could not be checked.", resolved.Slug)), err)
	}
	if decided {
		switch d.Outcome {
		case tools.SkillOutcomeDeny:
			text := tools.SkillRefusalText(resolved.Slug, d.Reason)
			if project {
				text = tools.ProjectSkillRefusalText(resolved.Slug, tools.WorkspaceLabel(omnipusHome(), workspaceID), d.Reason)
			}
			return nil, requestedSkillRefused(req.TargetAgentID, text)
		case tools.SkillOutcomeAskPending:
			if err := l.settleRequestedSkillAsk(ctx, req, child, resolved, key, project, d); err != nil {
				return nil, err
			}
		}
	}
	return l.preloadRequestedSkill(ctx, req.TargetAgentID, workspaceID, child, resolved)
}

// steeringMeta reads the steering session's metadata.
func (l *SteerLauncher) steeringMeta(steeringSessionID string) (*session.UnifiedMeta, error) {
	sessions := l.al.GetSessionStore()
	if sessions == nil {
		return nil, fmt.Errorf("steer: launch: %w: session stores not wired", steer.ErrStoreWrite)
	}
	meta, err := sessions.GetMeta(steeringSessionID)
	if err != nil {
		return nil, fmt.Errorf("steer: launch: %w: resolve steering session %q: %w",
			steer.ErrInvalidEdge, steeringSessionID, err)
	}
	return meta, nil
}

// settleRequestedSkillAsk settles an ask_pending requested skill the way a
// Skill load settles it (C-12): a timed allowance and Auto-approve settle
// only the permission check; what stays open asks a person in the steering
// chat, and an unattended chain, or a launch with no steering chat, refuses.
func (l *SteerLauncher) settleRequestedSkillAsk(
	ctx context.Context, req steer.LaunchRequest, child *AgentInstance,
	resolved skills.ResolvedSkill, key string, project bool, d tools.SkillDecision,
) error {
	autoSettles := req.SteeringSessionID != "" && l.al.autoApproveActive(req.SteeringSessionID)
	permissionOpen := d.PermissionOpen && !autoSettles &&
		!l.al.ApprovalGrants().IsSkillAllowed(req.TargetAgentID, config.NormalizeSkillKey(key))
	if !permissionOpen && !d.TrustOpen {
		return nil
	}
	fields := map[string]any{"agent_id": req.TargetAgentID, "skill": key, "steering_session_id": req.SteeringSessionID}
	if tools.ToolAutoDenyAsk(ctx) || req.SteeringSessionID == "" {
		logger.InfoCF("agent", "delegate: requested_skill needs approval in a run nobody can answer; the delegation is refused", fields)
		return requestedSkillRefused(req.TargetAgentID,
			fmt.Sprintf("`%s` needs approval, and task and plan runs never ask.", resolved.Slug))
	}
	card := newSkillLoadCard(l.al, child.ContextBuilder, req.TargetAgentID, resolved, key, project, d, permissionOpen)
	turnID := ""
	if ts := turnStateFromContext(ctx); ts != nil {
		turnID = ts.turnID
	}
	callID := fmt.Sprintf("delegate-skill-%s-%s", req.Origin.CallID, resolved.Slug)
	approved, denialReason, _ := l.al.CheckGrantOrRequestApproval(
		withSkillCard(ctx, card), req.SteeringSessionID, req.TargetAgentID, "Skill", callID, turnID,
		map[string]any{"name": resolved.Slug},
	)
	if approved {
		return nil
	}
	fields["denial_reason"] = denialReason
	logger.InfoCF("agent", "delegate: requested_skill card not approved; the delegation is refused", fields)
	return requestedSkillRefused(req.TargetAgentID, fmt.Sprintf("`%s` was not approved.", resolved.Slug))
}

// preloadRequestedSkill loads the instructions of resolved for the child,
// as the Skill tool loads them (loadSkillActivation for a registry or
// built-in skill, the instruction file for a project skill). A skill that
// cannot be served refuses the delegation.
func (l *SteerLauncher) preloadRequestedSkill(
	ctx context.Context, agentID, workspaceID string, child *AgentInstance, resolved skills.ResolvedSkill,
) (*requestedSkillPreload, error) {
	p := &requestedSkillPreload{agentID: agentID, workspaceID: workspaceID, slug: resolved.Slug, shelf: resolved.Shelf}
	if resolved.Shelf == skills.ShelfProject {
		content, ok := skills.LoadSkillFile(resolved.Path)
		if !ok {
			return nil, l.requestedSkillUnserved(agentID, workspaceID, resolved, errors.New("the instruction file could not be read"))
		}
		p.content = content
		return p, nil
	}
	res, err := skills.LoadActivation(filepath.Dir(filepath.Dir(resolved.Path)), resolved.Slug, skills.LoadActivationOptions{
		EditAllowed: tools.ResolveEffectivePolicy(child.LoadToolPolicy(), "edit_skill") != string(config.ToolPolicyDeny),
		PATHEnv:     tools.BashChildPATH(ctx, child.Tools),
		Bindings:    child.ContextBuilder.SkillBindings(),
	})
	if err != nil {
		return nil, l.requestedSkillUnserved(agentID, workspaceID, resolved, err)
	}
	p.slug, p.shelf, p.revision, p.content = res.Slug, skills.SkillShelf(res.Shelf), res.Revision, res.Content
	return p, nil
}

// requestedSkillUnserved logs and audits a requested skill that could not
// be served and returns the refusal.
func (l *SteerLauncher) requestedSkillUnserved(agentID, workspaceID string, resolved skills.ResolvedSkill, cause error) error {
	logger.WarnCF("agent", "delegate: requested_skill could not be served; the delegation is refused",
		map[string]any{"agent_id": agentID, "skill": resolved.Slug, "error": cause.Error()})
	audit.EmitSkillCall(l.al.auditLogger, agentID, workspaceID, resolved.Slug,
		audit.SkillCallModeLoad, audit.SkillCallOutcomeDenied, string(resolved.Shelf))
	return fmt.Errorf("steer: launch: %w: %w",
		requestedSkillRefused(agentID, fmt.Sprintf("`%s` could not be loaded.", resolved.Slug)), cause)
}

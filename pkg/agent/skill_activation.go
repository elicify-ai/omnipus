// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

// Skills spec r15 §5.7 / §7.3: a registry or built-in Skill load renders the
// activation header through skills.LoadActivation — outside-change detection
// first, the bounded instruction read, the SD-30 binding check — and records
// the session's "seen" revision. A load that certifies no revision (a
// built-in, a degraded load) or fails removes any earlier record.

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/skills"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// loadSkillActivation serves an already-resolved registry/builtin skill.
func (al *AgentLoop) loadSkillActivation(
	ctx context.Context, agent *AgentInstance, callerID, workspaceID string, resolved skills.ResolvedSkill,
) tools.SkillLoadOutcome {
	session := tools.ToolTranscriptSessionID(ctx)
	root := filepath.Dir(filepath.Dir(resolved.Path))
	opts := skills.LoadActivationOptions{
		EditAllowed: tools.ResolveEffectivePolicy(agent.LoadToolPolicy(), "edit_skill") != string(config.ToolPolicyDeny),
		PATHEnv:     os.Getenv("PATH"),
		Bindings:    agent.ContextBuilder.SkillBindings(),
	}
	res, err := skills.LoadActivation(root, resolved.Slug, opts)
	if err != nil {
		skills.ForgetSeen(session, resolved.Slug)
		if errors.Is(err, skills.ErrNotFound) {
			audit.EmitSkillCall(al.auditLogger, callerID, workspaceID, resolved.Slug,
				audit.SkillCallModeLoad, audit.SkillCallOutcomeNotFound, "")
			return tools.SkillLoadOutcome{Status: tools.SkillLoadNotFound}
		}
		logger.WarnCF("agent", "skill could not be served",
			map[string]any{"agent_id": callerID, "skill": resolved.Slug, "error": err.Error()})
		audit.EmitSkillCall(al.auditLogger, callerID, workspaceID, resolved.Slug,
			audit.SkillCallModeLoad, audit.SkillCallOutcomeDenied, string(resolved.Shelf))
		return tools.SkillLoadOutcome{Status: tools.SkillLoadRefused, Message: err.Error()}
	}
	if res.Shelf == string(skills.ShelfRegistry) && res.Revision != "" {
		skills.RecordSeen(session, res.Slug, res.Revision)
	} else {
		skills.ForgetSeen(session, res.Slug)
	}
	shelf := skills.SkillShelf(res.Shelf)
	audit.EmitSkillCall(al.auditLogger, callerID, workspaceID, res.Slug,
		audit.SkillCallModeLoad, audit.SkillCallOutcomeLoaded, string(shelf))
	return tools.SkillLoadOutcome{
		Status: tools.SkillLoadLoaded, Content: res.Content, Shelf: shelf, CanonicalSlug: res.Slug,
		Folder: filepath.Dir(resolved.Path),
	}
}

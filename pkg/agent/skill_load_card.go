// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

// Skills & Tools C-9, C-12 and C-24: the skill an approval card is about.
// A Skill load and an install_skill call carry the skill's facts (key,
// name, display name, revision, unverified, open checks) to the approver,
// and a re-check the server runs when the card is answered. Which checks
// are open decides the card's answers (pkg/gateway::offeredActions).

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/skills"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// The open checks a skill card answers (C-12).
const (
	skillCheckPermission = "permission"
	skillCheckTrust      = "trust"
)

// The C-24 re-check refusals: the same sentence answers the person (409)
// and refuses the waiting call.
const (
	skillChangedText = "The skill changed after this request."
	skillNowDenyText = "This skill is now set to Deny."
)

// skillCard is the skill an approval request names and its answer-time
// re-check (nil for an install, whose card binds the staged copy).
type skillCard struct {
	facts   *generated.ToolApprovalSkill
	recheck func() string
}

func (c *skillCard) trustOpen() bool {
	return c != nil && slices.Contains(c.facts.OpenChecks, skillCheckTrust)
}

// settlePermission removes the permission check, which Auto-approve has
// settled; the trust check stays open (C-12 row 14).
func (c *skillCard) settlePermission() {
	if c == nil {
		return
	}
	c.facts.OpenChecks = slices.DeleteFunc(c.facts.OpenChecks, func(s string) bool { return s == skillCheckPermission })
}

type skillCardKey struct{}

func withSkillCard(ctx context.Context, c *skillCard) context.Context {
	if c == nil {
		return ctx
	}
	return context.WithValue(ctx, skillCardKey{}, c)
}

func skillCardFrom(ctx context.Context) *skillCard {
	c, _ := ctx.Value(skillCardKey{}).(*skillCard)
	return c
}

func openChecks(permission, trust bool) []string {
	checks := []string{}
	if permission {
		checks = append(checks, skillCheckPermission)
	}
	if trust {
		checks = append(checks, skillCheckTrust)
	}
	return checks
}

// skillSettingsOf is the global skill values a decision reads.
func skillSettingsOf(cfg *config.Config) config.PersistedSkillSettings {
	return config.PersistedSkillSettings{
		Policies: cfg.Sandbox.SkillPolicies,
		Trust:    cfg.Sandbox.EffectiveSkillTrust(),
		GodMode:  cfg.Sandbox.GodMode,
	}
}

// skillDisplayName is the label the skills list shows for the resolved
// folder, or the slug when the list does not have it.
func skillDisplayName(cb *ContextBuilder, resolved skills.ResolvedSkill) string {
	if cb != nil && cb.skillsLoader != nil {
		for _, info := range cb.skillsLoader.ListSkills() {
			if info.ID == resolved.Slug && filepath.Clean(info.Path) == filepath.Clean(resolved.Path) && info.Name != "" {
				return info.Name
			}
		}
	}
	return resolved.Slug
}

// skillLoadCard is the card of a Skill load whose decision asks (C-12).
func (ex *agentLoopRunTurnToolsExecute) skillLoadCard(
	resolved skills.ResolvedSkill, key string, project bool, d tools.SkillDecision, permissionOpen bool,
) *skillCard {
	rt := ex.rx.rr.rq.ri.rf.rt
	return newSkillLoadCard(rt.al, rt.ts.agent.ContextBuilder, rt.ts.agentID, resolved, key, project, d, permissionOpen)
}

// newSkillLoadCard is the card of a load of resolved by agentID whose
// decision d asks: a Skill call or the person's /name (C-12).
func newSkillLoadCard(
	al *AgentLoop, cb *ContextBuilder, agentID string,
	resolved skills.ResolvedSkill, key string, project bool, d tools.SkillDecision, permissionOpen bool,
) *skillCard {
	slug, dir, revision := resolved.Slug, filepath.Dir(resolved.Path), d.Revision
	return &skillCard{
		facts: &generated.ToolApprovalSkill{
			DisplayName: skillDisplayName(cb, resolved),
			Key:         key,
			Name:        slug,
			OpenChecks:  openChecks(permissionOpen, d.TrustOpen),
			Revision:    revision,
			Unverified:  !d.Verified,
		},
		recheck: func() string {
			cfg := al.GetConfig()
			if cfg == nil {
				return skillNowDenyText
			}
			now, err := tools.SkillLoadDecision(omnipusHome(), skillSettingsOf(cfg), agentSkillsCfg(cfg, agentID), key, slug, dir, project)
			if err != nil {
				logger.ErrorCF("agent", "skill card: the skill could not be checked again; the answer is refused",
					map[string]any{"agent_id": agentID, "skill": key, "error": err.Error()})
				return skillChangedText
			}
			if now.Revision != revision {
				return skillChangedText
			}
			if now.Outcome == tools.SkillOutcomeDeny {
				return skillNowDenyText
			}
			return ""
		},
	}
}

// installSkillCard is the card of an install_skill call from the facts
// of its staged copy (C-12 rows 6–8): the trust check is open when the
// copy is unverified, trust is Ask and its revision has no person's
// approval. An unreadable approval record refuses the install.
func (ex *agentLoopRunTurnToolsExecute) installSkillCard(facts map[string]any, permissionOpen bool) (*skillCard, error) {
	slug, _ := facts["skill"].(string)
	revision, _ := facts["revision"].(string)
	unverified, _ := facts["unverified"].(bool)
	if slug == "" || revision == "" {
		return nil, fmt.Errorf("install_skill: staged copy has no skill or revision")
	}
	key := config.NormalizeSkillKey(slug)
	trustOpen := false
	if cfg := ex.rx.rr.rq.ri.rf.rt.al.GetConfig(); unverified && cfg != nil && cfg.Sandbox.EffectiveSkillTrust() == config.SkillTrustAsk {
		approved, err := skills.TrustApproved(filepath.Join(omnipusHome(), "skills"), key, revision)
		if err != nil {
			return nil, fmt.Errorf("install_skill: read trust approvals: %w", err)
		}
		trustOpen = !approved
	}
	return &skillCard{facts: &generated.ToolApprovalSkill{
		DisplayName: slug,
		Key:         key,
		Name:        slug,
		OpenChecks:  openChecks(permissionOpen, trustOpen),
		Revision:    revision,
		Unverified:  unverified,
	}}, nil
}

// stageInstallOnAllow stages an install_skill call whose permission is
// Allow before it runs (C-12 row 8): an unverified copy whose revision has
// no approval under trust Ask still needs a person's trust approval, so
// the call asks with a trust card. Otherwise the staged copy is what the
// call publishes. It reports whether the call now asks.
func (ex *agentLoopRunTurnToolsExecute) stageInstallOnAllow(tc providers.ToolCall) bool {
	rt := ex.rx.rr.rq.ri.rf.rt
	if ex.toolName != "install_skill" || ex.toctouPolicy != string(config.ToolPolicyAllow) {
		return false
	}
	if cfg := rt.al.GetConfig(); cfg == nil || cfg.Sandbox.EffectiveSkillTrust() != config.SkillTrustAsk {
		return false
	}
	args, refused := ex.prepareApprovalRequest(tc, ex.recordArgs)
	if refused || !ex.skillCard.trustOpen() {
		return false // a refusal is the call's result; otherwise the pin is published
	}
	ex.preparedArgs = args
	ex.skillTrustOpen = true
	ex.toctouPolicy = string(config.ToolPolicyAsk)
	return true
}

// skillLoadUnattendedResult is what an unattended Skill load that needs a
// person gets (C-12): the plain sentence as its result and in the
// transcript.
func skillLoadUnattendedResult() (transcriptReason, content string) {
	cls, _ := ClassifyDenial(autoDenyTaskPlanReason)
	return autoDenyTaskPlanReason, cls.TranscriptText
}

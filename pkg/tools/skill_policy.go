// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

// The per-skill decision of Skills & Tools C-11, computed once per agent and
// turn beside resolveEffectivePolicyWith: grant (or project authority) ->
// global skill value x agent override (strictest wins) -> God Mode floor on
// the global side -> trust floor for an unverified skill. Its outcome is one
// of deny, ask_pending, allow; every entry point (load, menu, search, /name,
// delegation, folder authority, Full injection, unified search) consumes it.
// The trust floor and the God Mode floor are resolution-time rules, not
// stored layers (C-0).

import (
	"log/slog"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// SkillOutcome is the C-11 outcome of one skill for one agent.
type SkillOutcome string

const (
	SkillOutcomeDeny       SkillOutcome = "deny"
	SkillOutcomeAskPending SkillOutcome = "ask_pending"
	SkillOutcomeAllow      SkillOutcome = "allow"
)

// SkillDenyReason names which setting refused a skill.
type SkillDenyReason string

const (
	SkillDenyNone       SkillDenyReason = ""
	SkillDenyNotGranted SkillDenyReason = "not_granted"
	SkillDenyPermission SkillDenyReason = "permission"
	SkillDenyTrust      SkillDenyReason = "trust"
)

// SkillDecisionInput is everything the decision reads. Global and Agent are
// the stored values ("" = no entry on that side).
type SkillDecisionInput struct {
	Key           string
	Granted       bool
	Global        config.ToolPolicy
	Agent         config.ToolPolicy
	Trust         config.SkillTrustLevel
	Verified      bool
	GodMode       bool
	TrustApproved bool // the current revision has a person's trust approval (C-24)
}

// SkillDecision is the outcome plus the two consent checks of C-12 that are
// still open (before the session's settlers: Auto-approve, a timed
// allowance, the person's own /name, Approve Once).
type SkillDecision struct {
	Outcome        SkillOutcome
	Reason         SkillDenyReason
	PermissionOpen bool // effective permission is ask
	TrustOpen      bool // unverified, trust ask, revision not approved
	// Revision and Verified are the facts the decision was read from (C-5),
	// set by the readers that look at the skill's files (InstalledSkillDecision,
	// SkillLoadDecision); an approval card names them (C-9).
	Revision string
	Verified bool
}

// ResolveSkillDecision is the one C-11 decision function.
func ResolveSkillDecision(in SkillDecisionInput) SkillDecision {
	if !in.Granted {
		return SkillDecision{Outcome: SkillOutcomeDeny, Reason: SkillDenyNotGranted}
	}
	g := in.Global
	if in.GodMode {
		g = config.ToolPolicyAllow
	}
	if g == "" && in.Agent == "" {
		// The C-2 self-heal keeps the global skill map complete, so this is
		// a coverage gap, never a normal path: fail closed and say so.
		slog.Error("tools: no permission entry (global or agent) for skill; failing closed to deny",
			"skill", in.Key)
	}
	merged := config.MergeStoredPolicyLayers(g, in.Agent)
	if merged == config.ToolPolicyDeny {
		return SkillDecision{Outcome: SkillOutcomeDeny, Reason: SkillDenyPermission}
	}
	d := SkillDecision{Outcome: SkillOutcomeAllow, PermissionOpen: merged == config.ToolPolicyAsk}
	if !in.Verified {
		switch trust, _ := config.MigrateLegacySkillTrust(string(in.Trust)); config.SkillTrustLevel(trust) {
		case config.SkillTrustDeny:
			return SkillDecision{Outcome: SkillOutcomeDeny, Reason: SkillDenyTrust}
		case config.SkillTrustAsk:
			d.TrustOpen = !in.TrustApproved
		}
	}
	if d.PermissionOpen || d.TrustOpen {
		d.Outcome = SkillOutcomeAskPending
	}
	return d
}

// SkillOutcomeOf is ResolveSkillDecision over the stored vocabulary of one
// skill's inputs (C-11, Dataset D1): grant, global value, the agent's own
// value, trust ("" = no entry), verification and God Mode, with no trust
// approval recorded. It returns the outcome (deny, ask_pending or allow)
// and, when the grant gate refused, the refusal "not granted"; any other
// deny names its setting at the entry point (SkillRefusalText).
func SkillOutcomeOf(granted bool, global, agentValue, trust string, verified, godMode bool) (outcome, refusal string) {
	d := ResolveSkillDecision(SkillDecisionInput{
		Granted:  granted,
		Global:   config.ToolPolicy(global),
		Agent:    config.ToolPolicy(agentValue),
		Trust:    config.SkillTrustLevel(trust),
		Verified: verified,
		GodMode:  godMode,
	})
	if d.Reason == SkillDenyNotGranted {
		refusal = "not granted"
	}
	return string(d.Outcome), refusal
}

// SkillRefusalText words a deny for the person and the model (C-3, C-11).
func SkillRefusalText(skill string, reason SkillDenyReason) string {
	switch reason {
	case SkillDenyNotGranted:
		return "`" + skill + "` is not granted to this agent."
	case SkillDenyTrust:
		return "`" + skill + "` is unverified, and unverified skills are set to Deny in Skills & Tools."
	case SkillDenyPermission:
		return "`" + skill + "` is set to Deny in Skills & Tools."
	}
	return ""
}

// ProjectSkillRefusalText words a deny of a project skill (C-11, S-102).
// Refused by the project skill's own Deny, it names the project skill and
// its workspace (workspace is its name, or its id when it has none), so the
// person finds the row that refused it; any other reason is worded as
// SkillRefusalText words it.
func ProjectSkillRefusalText(skill, workspace string, reason SkillDenyReason) string {
	if reason != SkillDenyPermission {
		return SkillRefusalText(skill, reason)
	}
	return "The project skill `" + skill + "` in workspace \"" + workspace +
		"\" is set to Deny in Skills & Tools. Change its permission there to use it."
}

// SkillPolicyValues returns the stored global value of key and the agent's
// own override ("" when absent).
func SkillPolicyValues(cfg *config.Config, agentSkills *config.AgentSkillsCfg, key string) (global, agent config.ToolPolicy) {
	key = config.NormalizeSkillKey(key)
	if cfg != nil {
		global = config.ToolPolicy(cfg.Sandbox.SkillPolicies[key])
	}
	if agentSkills != nil {
		agent = agentSkills.Policies[key]
	}
	return global, agent
}

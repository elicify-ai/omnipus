package systools

// Skills & Tools C-21/C-23: skill_policy_changes on create_agent ({set}
// only) and set_agent_permissions ({set}, {remove}, {clear}), the skill
// clamp note (S-70) and get_agent's skill_permissions rows (C-9, S-74).

import (
	"fmt"
	"sort"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/agentmutation"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/skills"
	"github.com/elicify-ai/omnipus/pkg/tools"
	wsstore "github.com/elicify-ai/omnipus/pkg/workspace"
)

const skillPolicyChangesField = "skill_policy_changes"

// decodeSkillPolicyPatch decodes skill_policy_changes. setOnly is the
// create_agent shape; null anywhere is refused naming the place.
func decodeSkillPolicyPatch(raw any, setOnly bool) (agentmutation.SkillPolicyChanges, error) {
	p := agentmutation.SkillPolicyChanges{}
	if raw == nil {
		return p, fieldErr(skillPolicyChangesField, "skill_policy_changes must not be null")
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return p, fieldErr(skillPolicyChangesField, "skill_policy_changes must be an object")
	}
	for key, v := range m {
		allowed := key == "set" || (!setOnly && (key == "remove" || key == "clear"))
		if !allowed {
			if setOnly {
				return p, fieldErr(skillPolicyChangesField, fmt.Sprintf("unknown skill_policy_changes field %q: create_agent takes set only", key))
			}
			return p, fieldErr(skillPolicyChangesField, fmt.Sprintf("unknown skill_policy_changes field %q", key))
		}
		if v == nil {
			return p, fieldErr(skillPolicyChangesField, fmt.Sprintf("skill_policy_changes.%s must not be null", key))
		}
	}
	if setRaw, present := m["set"]; present {
		setMap, ok := setRaw.(map[string]any)
		if !ok {
			return p, fieldErr(skillPolicyChangesField, "skill_policy_changes.set must be an object")
		}
		p.Set = map[string]config.ToolPolicy{}
		for key, v := range setMap {
			if v == nil {
				return p, fieldErr(skillPolicyChangesField, fmt.Sprintf("skill_policy_changes.set.%s must not be null", key))
			}
			s, ok := v.(string)
			if !ok {
				return p, fieldErr(skillPolicyChangesField, fmt.Sprintf("policy for %s must be a string", key))
			}
			p.Set[key] = config.ToolPolicy(s)
		}
	}
	if remRaw, present := m["remove"]; present {
		vals, err := requiredStringArray(remRaw, "skill_policy_changes.remove")
		if err != nil {
			return p, err
		}
		p.Remove = vals
	}
	if clearRaw, present := m["clear"]; present {
		b, ok := clearRaw.(bool)
		if !ok {
			return p, fieldErr(skillPolicyChangesField, "skill_policy_changes.clear must be a boolean")
		}
		p.Clear = b
	}
	return p, nil
}

// applySkillPolicyArgs applies skill_policy_changes onto a with the C-23
// per-agent key rule. workspaces are the workspaces the agent is mounted on
// (create: the destination workspace, when given).
func applySkillPolicyArgs(a *config.AgentConfig, args map[string]any, home string, global map[string]string,
	workspaces map[string]bool, setOnly bool,
) error {
	raw, present := args[skillPolicyChangesField]
	if !present {
		return nil
	}
	if a.IsExternalCLIWorker() {
		return fieldErr(skillPolicyChangesField, "field is unknown or unsupported by this runtime")
	}
	patch, err := decodeSkillPolicyPatch(raw, setOnly)
	if err != nil {
		return err
	}
	scope := tools.GlobalSkillKeyScope(home, global)
	if workspaces == nil {
		workspaces = map[string]bool{}
	}
	scope.AgentWorkspaces = workspaces
	if a.Tools == nil {
		a.Tools = &config.AgentToolsCfg{}
	}
	var current map[string]config.ToolPolicy
	if a.Tools.Skills != nil {
		current = a.Tools.Skills.Policies
	}
	next, err := agentmutation.ApplySkillPolicyChanges(current, patch, func(key string) (string, error) {
		return skills.CheckSkillPolicyKey(scope, key)
	})
	if err != nil {
		return err
	}
	a.Tools.Skills = &config.AgentSkillsCfg{Policies: next}
	return nil
}

// skillClamp is one requested per-agent skill value the global value makes
// stricter (S-70): the stored value is kept, the effective one runs.
type skillClamp struct {
	Skill     string `json:"skill"`
	Requested string `json:"requested"`
	Global    string `json:"global"`
	Effective string `json:"effective"`
}

// skillClamps lists, sorted by skill, every requested skill value that the
// global value clamps under strictest-wins (God Mode lifts the global side).
func skillClamps(global map[string]string, requested map[string]config.ToolPolicy, godMode bool) []skillClamp {
	out := []skillClamp{}
	for key, want := range requested {
		key = config.NormalizeSkillKey(key)
		g := config.ToolPolicy(global[key])
		if godMode {
			g = config.ToolPolicyAllow
		}
		if g == "" {
			continue
		}
		effective := config.MergeStoredPolicyLayers(g, want)
		if effective != want {
			out = append(out, skillClamp{Skill: key, Requested: string(want), Global: string(g), Effective: string(effective)})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Skill < out[j].Skill })
	return out
}

// skillClampSentence is the human-readable form of clamped ("" when none).
func skillClampSentence(clamped []skillClamp) string {
	if len(clamped) == 0 {
		return ""
	}
	parts := make([]string, 0, len(clamped))
	for _, c := range clamped {
		parts = append(parts, fmt.Sprintf("%s requested %s, but the global value %s applies", c.Skill, c.Requested, c.Global))
	}
	return "The global skill permission clamps " + strings.Join(parts, "; ") +
		" (the stored value is kept and applies if the global value is loosened)."
}

// skillPermissionRows is get_agent's skill_permissions (C-9). It reads the
// skill settings from config.json, as the turn's skill authority does, so
// the reported value is the one that runs.
func (d *Deps) skillPermissionRows(home string, a *config.AgentConfig) ([]tools.AgentSkillPermissionRow, error) {
	settings, err := config.ReadPersistedSkillSettings(home)
	if err != nil {
		return nil, err
	}
	settings.GodMode = settings.GodMode || d.godModeActive()
	var agentSkills *config.AgentSkillsCfg
	if a.Tools != nil {
		agentSkills = a.Tools.Skills
	}
	return tools.AgentSkillPermissions(home, settings, a.ID, a.Skills, agentSkills), nil
}

// globalSkillPolicies is the live global skill map (C-2).
func (d *Deps) globalSkillPolicies() map[string]string {
	if d == nil || d.GetCfg == nil {
		return map[string]string{}
	}
	cfg := d.GetCfg()
	if cfg == nil || cfg.Sandbox.SkillPolicies == nil {
		return map[string]string{}
	}
	return cfg.Sandbox.SkillPolicies
}

// skillPolicyChangesParameters is the JSON schema of skill_policy_changes:
// create_agent takes set only (C-21).
func skillPolicyChangesParameters(setOnly bool) map[string]any {
	props := map[string]any{
		"set": map[string]any{
			"type":                 "object",
			"additionalProperties": map[string]any{"type": "string", "enum": []any{"allow", "ask", "deny"}},
			"description":          "Skill keys to set: an installed skill id or project:<workspace>/<slug>. Unknown skills are rejected.",
		},
	}
	desc := "Per-agent skill permissions. set maps skill key to allow|ask|deny."
	if !setOnly {
		props["remove"] = map[string]any{
			"type": "array", "items": map[string]any{"type": "string"},
			"description": "Skill override keys to delete.",
		}
		props["clear"] = map[string]any{"type": "boolean", "description": "true deletes every skill override first."}
		desc += " remove lists override keys to delete; clear: true deletes them all."
	}
	return map[string]any{"type": "object", "additionalProperties": false, "description": desc, "properties": props}
}

// agentWorkspaceSet is the set of workspaces the agent is mounted on.
func agentWorkspaceSet(home, agentID string) map[string]bool {
	out := map[string]bool{}
	ids, _ := wsstore.FindAllForAgent(home, agentID)
	for _, id := range ids {
		out[id] = true
	}
	return out
}

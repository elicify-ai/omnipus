package systools

// RED scaffolding — landing/skills (train T9, #1458): the new `get_skill`
// tool (spec §5.2, test rows 1–3). Exists ONLY so the RED tests compile and
// fail on behaviour. The implementing worker replaces the bodies with the
// real §5.2 implementation (keeping the declared names), then removes the
// RED-SCAFFOLD markers. No other file was touched by the RED phase.

import (
	"context"

	"github.com/elicify-ai/omnipus/pkg/tools"
)

// GetSkillTool is §5.2's one reader of one installed skill: required `fields`,
// T8's shared field-selection helper, version paging (§5.2), the §5.2
// authorization table and the §7.3 seen-record side effect.
type GetSkillTool struct{ deps *Deps }

// NewGetSkillTool builds the get_skill tool over the system-tool deps.
func NewGetSkillTool(d *Deps) *GetSkillTool { return &GetSkillTool{deps: d} }

// Name implements tools.Tool.
func (t *GetSkillTool) Name() string { return "get_skill" }

// Scope implements tools.Tool.
func (t *GetSkillTool) Scope() tools.ToolScope { return tools.ScopeCore }

// Category implements tools.Tool (CategorySkills, as the other skill tools).
func (t *GetSkillTool) Category() tools.ToolCategory { return tools.CategorySkills }

// Description implements tools.Tool.
func (t *GetSkillTool) Description() string {
	return "Read one installed skill's details. fields is required: pass [\"all\"] or one or more of the selectable properties. " +
		"If versions make the result too large, lower versions_limit."
}

// Parameters implements tools.Tool (§5.2: id, fields, versions_before,
// versions_limit).
func (t *GetSkillTool) Parameters() map[string]any {
	return map[string]any{ // RED scaffold — shape declared, not validated yet
		"type": "object",
		"properties": map[string]any{
			"id":              map[string]any{"type": "string"},
			"fields":          map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"versions_before": map[string]any{"type": "string"},
			"versions_limit":  map[string]any{"type": "integer"},
		},
	}
}

// Execute implements tools.Tool. RED scaffold: always refuses — every
// behavioural assertion in the RED tests fails against this until the real
// §5.2 reader lands.
func (t *GetSkillTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	return tools.ErrorResult("get_skill: RED scaffold — not implemented (landing/skills T9)")
}

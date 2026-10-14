package systools

// Agent visual identity (#1303 / WU-25): create_agent, update_agent and
// get_agent carry the same figure / role / color catalogs the SPA agent editor
// uses — contracts/components/schemas/Agent{Figure,Role,Color}.yaml, generated
// into pkg/api/generated. The choice lists below are derived from those
// generated enums (never invented here) and the canonicalizers are
// coreagent.CanonicalFigure / CanonicalRole / CanonicalColor — the same
// functions the REST paths use (rest_agents_create.go and
// rest_agents_update.go::acceptAgentIdentityWrite), so the tool API and the
// REST API cannot drift on what an acceptable visual is.

import (
	"fmt"
	"strings"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// visualFields is the identity triple, in a fixed order for validation,
// normalization and messages.
var visualFields = []string{"figure", "role", "color"}

// figureChoices lists the AgentFigure catalog: exact product words.
func figureChoices() []string {
	return []string{
		string(generated.AgentFigureRobot),
		string(generated.AgentFigureMan),
		string(generated.AgentFigureWoman),
		string(generated.AgentFigureOmnipus),
		string(generated.AgentFigureMonogram),
	}
}

// roleChoices lists the AgentRole catalog: 31 curated slugs. The role is
// presentation-only — it selects the avatar's badge icon artwork and never
// changes an agent's runtime type, tools, or permissions.
func roleChoices() []string {
	return []string{
		string(generated.AgentRoleWriter),
		string(generated.AgentRoleDesigner),
		string(generated.AgentRoleImage),
		string(generated.AgentRoleVideo),
		string(generated.AgentRoleAudio),
		string(generated.AgentRoleSocial),
		string(generated.AgentRoleDeveloper),
		string(generated.AgentRoleData),
		string(generated.AgentRoleAnalyst),
		string(generated.AgentRoleItops),
		string(generated.AgentRoleAutomation),
		string(generated.AgentRoleSecurity),
		string(generated.AgentRoleQuality),
		string(generated.AgentRoleScience),
		string(generated.AgentRoleOrchestrator),
		string(generated.AgentRoleProject),
		string(generated.AgentRoleProduct),
		string(generated.AgentRoleSales),
		string(generated.AgentRoleMarketing),
		string(generated.AgentRoleFinance),
		string(generated.AgentRoleLegal),
		string(generated.AgentRoleSupport),
		string(generated.AgentRoleDocuments),
		string(generated.AgentRoleResearcher),
		string(generated.AgentRolePeople),
		string(generated.AgentRoleTutor),
		string(generated.AgentRoleKnowledge),
		string(generated.AgentRoleTranslator),
		string(generated.AgentRoleGeneral),
		string(generated.AgentRolePersonal),
		string(generated.AgentRoleOffice),
	}
}

// canonicalVisual maps a supplied value to its stored canonical form: figure
// and role are exact-case words/slugs, color accepts any letter-case and is
// uppercased. Anything outside the catalog is not canonical.
func canonicalVisual(field, raw string) (string, bool) {
	switch field {
	case "figure":
		return coreagent.CanonicalFigure(raw)
	case "role":
		return coreagent.CanonicalRole(raw)
	default:
		return coreagent.CanonicalColor(raw)
	}
}

// visualRefusal is the model-visible refusal for a non-catalog identity
// value: it names the rejected field and lists every valid choice, so the
// model can correct its proposal without another discovery round-trip. It is
// a validation result, never a panic and never a missing-tool error.
func visualRefusal(field, raw string) *tools.ToolResult {
	switch field {
	case "figure":
		return tools.ErrorResult(errorJSON("INVALID_INPUT",
			fmt.Sprintf("invalid figure %q: must be one of the catalog body words %s",
				raw, strings.Join(figureChoices(), ", ")),
			`Use an exact product word, e.g. "Robot"; omitted, null or empty selects the default on create`))
	case "role":
		return tools.ErrorResult(errorJSON("INVALID_INPUT",
			fmt.Sprintf("invalid role %q: must be one of the curated role slugs %s",
				raw, strings.Join(roleChoices(), ", ")),
			`Use an exact role slug, e.g. "writer"; the role is presentation-only and picks the badge icon`))
	default:
		return tools.ErrorResult(errorJSON("INVALID_COLOR",
			fmt.Sprintf("invalid color %q: use one of the palette colors %s",
				raw, strings.Join(coreagent.PaletteColors(), ", ")),
			"Use a palette color, e.g. #6E8BA8"))
	}
}

// resolveCreateVisual resolves one identity field for create_agent. Chief Q1 A
// (2026-10-14): an omitted, null (NormalizeArgs renders it "") or empty value
// requests the shipped default def — exactly like REST create; a catalog
// value returns its canonical form; anything else is refused with the full
// choice list. The resolved value is written back into args so the shared
// apply step re-reads the canonical form and an explicit empty cannot
// overwrite the default — the same pattern the color argument uses.
func resolveCreateVisual(args map[string]any, field, def string) (string, *tools.ToolResult) {
	raw, present := args[field]
	if !present || raw == "" {
		args[field] = def
		return def, nil
	}
	s, ok := raw.(string)
	if !ok {
		return "", visualRefusal(field, fmt.Sprint(raw))
	}
	canon, valid := canonicalVisual(field, s)
	if !valid {
		return "", visualRefusal(field, s)
	}
	args[field] = canon
	return canon, nil
}

// validateVisualUpdateArgs rejects a present figure, role, or color that is
// empty or outside its catalog, BEFORE any field is read or written — the
// tool path's counterpart of rest_agents_update.go::acceptAgentIdentityWrite.
// An omitted field keeps the stored value; there is no empty or null default
// on update (NormalizeArgs has already rendered a JSON null to ""), so the
// whole call is refused, never partially persisted.
func validateVisualUpdateArgs(args map[string]any) *tools.ToolResult {
	for _, field := range visualFields {
		raw, present := args[field]
		if !present {
			continue
		}
		s, ok := raw.(string)
		if !ok {
			return visualRefusal(field, fmt.Sprint(raw))
		}
		if s == "" {
			return visualRefusal(field, s)
		}
		if _, valid := canonicalVisual(field, s); !valid {
			return visualRefusal(field, s)
		}
	}
	return nil
}

// normalizeVisualArgs implements the registry's argsNormalizer seam
// (pkg/tools/registry.go) for create_agent and update_agent: it renders the
// three identity arguments to plain strings BEFORE validateToolArgs runs, so
// that (a) a JSON null reaches the tool as "" — the create default request /
// the update refusal — instead of dying in the registry's generic type check,
// and (b) a mistyped value (number, boolean, array, object) is rendered with
// fmt.Sprint and refused by the tool's own validator together with the full
// choice list, rather than by the registry with a message the model cannot
// act on. The rendered form is never a catalog value, so both are still
// refusals — only the explanation gets better.
//
// Only the literal keys "figure", "role" and "color" are touched; every other
// argument — including the nulls that max_tool_iterations and
// context_window_override legitimately carry — passes through untouched.
// args is mutated in place and returned, matching AskUserQuestionTool's
// precedent: every call receives its own freshly decoded map that the
// registry does not reuse.
func normalizeVisualArgs(args map[string]any) map[string]any {
	if args == nil {
		return args
	}
	for _, field := range visualFields {
		raw, present := args[field]
		if !present {
			continue
		}
		if raw == nil {
			args[field] = ""
			continue
		}
		if _, isString := raw.(string); !isString {
			args[field] = fmt.Sprint(raw)
		}
	}
	return args
}

// NormalizeArgs implements the registry's argsNormalizer seam for
// create_agent — see normalizeVisualArgs.
func (t *AgentCreateTool) NormalizeArgs(args map[string]any) map[string]any {
	return normalizeVisualArgs(args)
}

// NormalizeArgs implements the registry's argsNormalizer seam for
// update_agent — see normalizeVisualArgs.
func (t *AgentUpdateTool) NormalizeArgs(args map[string]any) map[string]any {
	return normalizeVisualArgs(args)
}

// applyVisualIdentityArgs applies figure and role args onto a during the
// shared apply step (applyAgentToolArgs). Color is applied by
// applyIdentityAndModelArgs's existing color branch. An empty or non-catalog
// value is refused here too: on update the early validateVisualUpdateArgs has
// already refused it before any read, and on create resolveCreateVisual has
// already resolved omissions to the default, so this is the last gate for
// paths that call Execute directly without the registry's normalizer.
func applyVisualIdentityArgs(a *config.AgentConfig, args map[string]any) error {
	if err := applyOptionalString(args, "figure", func(v string) error {
		canon, ok := coreagent.CanonicalFigure(v)
		if !ok {
			return fieldErr("figure", fmt.Sprintf("invalid figure %q: must be one of the catalog body words %s",
				v, strings.Join(figureChoices(), ", ")))
		}
		a.Figure = canon
		return nil
	}); err != nil {
		return err
	}
	return applyOptionalString(args, "role", func(v string) error {
		canon, ok := coreagent.CanonicalRole(v)
		if !ok {
			return fieldErr("role", fmt.Sprintf("invalid role %q: must be one of the curated role slugs %s",
				v, strings.Join(roleChoices(), ", ")))
		}
		a.Role = canon
		return nil
	})
}

// Property schema helpers for the identity fields. R4/#1303: the full choice
// list lives in each property description — deliberately NOT as a schema
// enum, because the registry's enum check would refuse an unknown value with
// a message that does not list the choices, leaving the model nothing to
// correct its proposal with.

func figureCreateProperty() map[string]any {
	return map[string]any{
		"type": "string",
		"description": "Optional body of the agent's visual mark: Robot, Man, Woman, Omnipus, or Monogram " +
			"(exact product words; Monogram draws the agent name's first character in its color). " +
			"Omitted, null or empty creates the agent with the default figure Omnipus.",
	}
}

func figureUpdateProperty() map[string]any {
	return map[string]any{
		"type": "string",
		"description": "New body of the agent's visual mark: Robot, Man, Woman, Omnipus, or Monogram " +
			"(exact product words). Omitted leaves the stored figure unchanged; null or empty is refused.",
	}
}

func roleCreateProperty() map[string]any {
	return map[string]any{
		"type": "string",
		"description": "Optional presentation role: the curated slug that picks the avatar's badge icon artwork. " +
			"Visual only — it never changes the agent's runtime type, tools, or permissions. One of: " +
			strings.Join(roleChoices(), ", ") +
			". Omitted, null or empty creates the agent with the default role general.",
	}
}

func roleUpdateProperty() map[string]any {
	return map[string]any{
		"type": "string",
		"description": "New presentation role: the curated slug that picks the avatar's badge icon artwork. " +
			"Visual only — it never changes the agent's runtime type, tools, or permissions. One of: " +
			strings.Join(roleChoices(), ", ") +
			". Omitted leaves the stored role unchanged; null or empty is refused.",
	}
}

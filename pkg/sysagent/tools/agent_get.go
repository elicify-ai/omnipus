// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

// agent_get.go — get_agent, the single complete agent reader (tool-parity
// spec r17 §7.12.1, §7.5 AG-16, §3.19). It replaces get_agent_tools (the
// tool inventory is the `tool_permissions` property; a not-yet-created
// agent's defaults are a preview by new_agent_type) and read_agent_metadata
// (`soul`, `agent_md`; no selector = the calling agent). Field selection,
// projection and the size bound are the shared helper's (§7.13).

import (
	"context"
	"path/filepath"
	"slices"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/skills"
	"github.com/elicify-ai/omnipus/pkg/thirdpartycli"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// agentFieldSpec is get_agent's selectable object for an existing agent.
var agentFieldSpec = tools.FieldSpec{Object: "agent", Identity: []string{"id", "revision"}, Valid: tools.AgentFieldNames}

// agentPreviewFieldSpec is get_agent's selectable object for a new-agent
// preview: no id and no revision, the agent does not exist.
var agentPreviewFieldSpec = tools.FieldSpec{
	Object: tools.AgentPreviewObject, Identity: []string{"preview_of"}, Valid: tools.AgentPreviewFieldNames,
}

// agentPreviewTypes are the new_agent_type values, in schema order.
var agentPreviewTypes = []string{"Main", "Subagent", "subagent_3p"}

const agentReadRecovery = "Retry once. If it persists, report this error code and the requested agent or preview type to the operator. No settings were changed."

// AgentGetTool implements get_agent.
type AgentGetTool struct{ deps *Deps }

func NewAgentGetTool(d *Deps) *AgentGetTool    { return &AgentGetTool{deps: d} }
func (t *AgentGetTool) Name() string           { return "get_agent" }
func (t *AgentGetTool) Scope() tools.ToolScope { return tools.ScopeCore }
func (t *AgentGetTool) Description() string {
	return "Read one agent: pass `fields` with the properties you need, or [\"all\"]. Select it by exact id or exact name; " +
		"with neither you read yourself. Covers its model and effective model chain, instructions (soul, agent_md), " +
		"skills (granted names; stale_skill_grants lists the granted names whose skill was replaced and must be re-granted), tool permissions (effective policy per tool, plus its stored overrides), connectors, external-worker " +
		"executor (env values masked) and state. With new_agent_type (Main, Subagent, subagent_3p) it previews the " +
		"tool_permissions and connectors a new agent of that type would get, without creating one."
}

func (t *AgentGetTool) Parameters() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"fields": tools.FieldsParameter(tools.AgentFieldNames),
			"id": map[string]any{"type": "string", "minLength": 1, "maxLength": 128,
				"description": "The agent's exact id. Omit id, name and new_agent_type to read yourself."},
			"name": map[string]any{"type": "string", "minLength": 1, "maxLength": 256,
				"description": "The agent's exact display name, instead of id. Several agents with that name are refused as ambiguous."},
			"new_agent_type": map[string]any{"type": "string", "enum": agentPreviewTypes,
				"description": "Preview a new agent's defaults (tool_permissions, connectors) without creating it."},
		},
		"required": []string{"fields"},
	}
}

// FieldSpecFor implements tools.FieldSelecting: a preview has its own spec.
func (t *AgentGetTool) FieldSpecFor(args map[string]any) tools.FieldSpec {
	if previewType(args) != "" {
		return agentPreviewFieldSpec
	}
	return agentFieldSpec
}

// previewType is the new_agent_type argument; an empty string counts as
// absent (#893).
func previewType(args map[string]any) string {
	s, _ := args["new_agent_type"].(string)
	return s
}

// presentSelector reports whether a selector argument is given: present
// and not an empty string (#893).
func presentSelector(args map[string]any, key string) bool {
	v, ok := args[key]
	if !ok || v == nil {
		return false
	}
	s, isString := v.(string)
	return !isString || s != ""
}

func (t *AgentGetTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	selectors := 0
	for _, key := range []string{"id", "name", "new_agent_type"} {
		if presentSelector(args, key) {
			selectors++
		}
	}
	if selectors > 1 {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "Supply at most one of: id, name, new_agent_type", ""))
	}
	spec := t.FieldSpecFor(args)
	selected, refusal := tools.ValidateFieldsArg(args, spec)
	if refusal != nil {
		return refusal
	}
	if t.deps == nil {
		return tools.ErrorResult(errorJSON("CONFIG_UNAVAILABLE", "Agent configuration is unavailable", agentReadRecovery))
	}
	var full map[string]any
	if presentSelector(args, "new_agent_type") {
		full, refusal = t.preview(args["new_agent_type"], selected)
	} else {
		full, refusal = t.detail(ctx, args, selected)
	}
	if refusal != nil {
		return refusal
	}
	out, refusal := tools.Project(full, spec, selected)
	if refusal != nil {
		return refusal
	}
	return tools.NewToolResult(successJSON(out))
}

// preview builds the AgentPreview a new agent of kind would get, entirely in
// memory: the same Main/Worker defaults both creation paths use, no
// agent-store reads or writes.
func (t *AgentGetTool) preview(raw any, selected []string) (map[string]any, *tools.ToolResult) {
	kind, _ := raw.(string)
	if !slices.Contains(agentPreviewTypes, kind) {
		return nil, tools.ErrorResult(errorJSON("INVALID_INPUT",
			"new_agent_type must be one of: "+strings.Join(agentPreviewTypes, ", "), ""))
	}
	defaults := coreagent.NewCustomAgentToolsCfg()
	agentType := config.AgentTypeCustom
	if kind != "Main" {
		coreagent.RestrictForWorkerTier(defaults)
		agentType = config.AgentTypeWorker
	}
	a := &config.AgentConfig{Type: agentType, Tools: defaults}
	full := map[string]any{"preview_of": kind, "connectors": storedMCPBindings(a)}
	if slices.Contains(selected, "tool_permissions") {
		perms, refusal := t.toolPermissions(a)
		if refusal != nil {
			return nil, refusal
		}
		full["tool_permissions"] = perms
	}
	return full, nil
}

// detail reads the selected agent (id, exact name, or the caller) and builds
// its full AgentDetail. Properties that need the tool catalog are built only
// when selected, so a missing catalog never fails an unrelated read.
func (t *AgentGetTool) detail(ctx context.Context, args map[string]any, selected []string) (map[string]any, *tools.ToolResult) {
	home, err := resolveOmnipusHome(t.deps.Home)
	if err != nil {
		return nil, tools.ErrorResult(errorJSON("WORKSPACE_ERROR", err.Error(), ""))
	}
	var id string
	switch {
	case presentSelector(args, "name"):
		var refusal *tools.ToolResult
		if id, refusal = agentIDByExactName(home, args["name"]); refusal != nil {
			return nil, refusal
		}
	case presentSelector(args, "id"):
		id, _ = args["id"].(string)
	default:
		if id = tools.ToolAgentID(ctx); id == "" {
			return nil, tools.ErrorResult(errorJSON("INVALID_INPUT",
				"id is required when the calling agent is not known", "Pass id or name"))
		}
	}
	state, refusal := readAgentStateExact(home, id)
	if refusal != nil {
		return nil, refusal
	}
	var cfg *config.Config
	if t.deps.GetCfg != nil {
		cfg = t.deps.GetCfg()
	}
	full := t.storedDetail(home, state, cfg)
	if slices.Contains(selected, "stale_skill_grants") {
		// Same function and root as the REST agent detail (UAT-1 F-9).
		stale, err := skills.StaleGrants(filepath.Join(home, "skills"), state.Agent.Skills, state.Agent.SkillGrants)
		if err != nil {
			// The cause (it carries filesystem paths) goes to the server log
			// only, as the REST agent detail does.
			logger.ErrorCF("sysagent", "get_agent: skill grant check failed", map[string]any{
				"agent_id": state.Agent.ID, "error": err.Error(),
			})
			return nil, tools.ErrorResult(errorJSON("SKILL_STATUS_UNAVAILABLE",
				"could not check the agent's skill grants: server storage or configuration failed. Details are in the server log.",
				agentReadRecovery))
		}
		full["stale_skill_grants"] = nonNilStrings(stale)
	}
	if slices.Contains(selected, "tool_permissions") {
		perms, refusal := t.toolPermissions(state.Agent)
		if refusal != nil {
			return nil, refusal
		}
		full["tool_permissions"] = perms
	}
	return full, nil
}

// storedDetail is every AgentDetail property that needs no tool catalog.
func (t *AgentGetTool) storedDetail(home string, state *agentstore.State, cfg *config.Config) map[string]any {
	a := state.Agent
	activation := agentstore.ActivationNotAttempted
	if t.deps.AgentActiveRevision != nil && t.deps.AgentActiveRevision(a.ID) == state.Revision {
		activation = agentstore.ActivationActive
	}
	// Visual identity readback (#1303): the same read backstop as REST.
	figure, role, color := coreagent.WireIdentity(a.Figure, a.Role, a.Color)
	model, provider := "", ""
	if a.Model != nil {
		model, provider = a.Model.Primary, a.Model.Provider
	}
	full := map[string]any{
		"id": a.ID, "revision": state.Revision,
		"name": a.Name, "description": a.Description, "type": wireAgentType(*a), "locked": a.Locked,
		"figure": string(figure), "role": string(role), "color": string(color), "voice": a.Voice,
		"model": model, "provider": provider, "fallback_models": nonNilFallbacks(a.FallbackModels),
		"reasoning_effort": storedEffortView(*a),
		"model_chain":      agentModelChain(a, cfg), "max_tool_iterations": nil,
		"context_window_override": a.ContextWindowOverride, "memory_enabled": a.MemoryEnabledEffective(),
		"skills": nonNilStrings(a.Skills), "stored_overrides": storedOverrides(a),
		"connectors": storedMCPBindings(a), "activation_status": activation,
		"editable_fields": editableDescriptors(*a), "has_main_chat": a.IsChatTarget(),
	}
	if a.MaxToolIterations > 0 {
		full["max_tool_iterations"] = a.MaxToolIterations
	}
	if a.ModelParams != nil {
		full["model_params"] = a.ModelParams
	}
	// A third-party agent has no soul and no AGENT.md (the shared field
	// rules do not support them): the full object does not carry them.
	if !a.IsExternalCLIWorker() {
		// Read under the same lock as the record and SOUL.md, so the three
		// match the revision returned beside them (r17 §7.10).
		full["soul"] = state.Soul
		full["agent_md"] = state.AgentMD
	}
	runnable := true
	if executor, warning := agentExecutorView(a, cfg, home); executor != nil {
		full["executor"] = executor
		if warning != "" {
			full["program_warning"] = warning
			runnable = false
		}
	}
	full["runnable"] = runnable
	return full
}

// agentExecutorView is the ExecutorConfig read model for an agent with an
// executor block, as REST returns it: env values masked, the Settings
// program and preset read-only, and the text a run would be refused with
// (r17 §3.8). nil when the agent has no executor block.
func agentExecutorView(a *config.AgentConfig, cfg *config.Config, home string) (map[string]any, string) {
	if a.Subagents == nil || a.Subagents.Executor == nil {
		return nil, ""
	}
	ec := a.Subagents.Executor
	view := map[string]any{"kind": string(ec.EffectiveKind())}
	if ec.CLI != "" {
		view["cli"] = ec.CLI
	}
	if ec.CLIArgs != "" {
		view["cli_args"] = ec.CLIArgs
	}
	if masked := thirdpartycli.MaskEnv(ec.EnvOverrides); masked != nil {
		view["env_overrides"] = masked
	}
	if ec.EffectiveKind() != config.ExecutorKindExternalCLI || ec.CLI == "" {
		return view, ""
	}
	var row config.ThirdPartyCLIConfig
	if cfg != nil {
		row = cfg.Sandbox.ThirdPartyCLI(ec.CLI)
	}
	if row.Path != "" {
		view["effective_cli_path"] = row.Path
	}
	if row.PresetArgs != "" {
		view["effective_preset_args"] = row.PresetArgs
	}
	writable, rootsErr := thirdpartycli.AgentWritableRoots(home, cfg)
	warning := thirdpartycli.ProgramWarning(ec.CLI, row.Path, writable, rootsErr)
	if warning != "" {
		view["program_warning"] = warning
	}
	return view, warning
}

func nonNilFallbacks(in config.FallbackModelSlice) []config.FallbackModel {
	if in == nil {
		return []config.FallbackModel{}
	}
	return in
}

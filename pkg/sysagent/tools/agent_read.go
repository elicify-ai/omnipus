package systools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/agentmutation"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/entity"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

type AgentGetTool struct{ deps *Deps }

func NewAgentGetTool(d *Deps) *AgentGetTool    { return &AgentGetTool{deps: d} }
func (t *AgentGetTool) Name() string           { return "get_agent" }
func (t *AgentGetTool) Scope() tools.ToolScope { return tools.ScopeCore }
func (t *AgentGetTool) Description() string {
	return "Read one agent's sanitized configuration, instructions, editable fields, stored assignments, revision, and activation status. Use get_agent_tools for the full tool catalog."
}
func (t *AgentGetTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string"}}, "required": []string{"id"}}
}

func (t *AgentGetTool) Execute(_ context.Context, args map[string]any) *tools.ToolResult {
	id, _ := args["id"].(string)
	if err := validateID(id); err != nil {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", err.Error(), ""))
	}
	home, err := resolveOmnipusHome(t.deps.Home)
	if err != nil {
		return tools.ErrorResult(errorJSON("WORKSPACE_ERROR", err.Error(), ""))
	}
	state, err := agentstore.New(home).ReadState(id)
	if err != nil {
		if errors.Is(err, entity.ErrNotFound) {
			return tools.ErrorResult(errorJSON("AGENT_NOT_FOUND", fmt.Sprintf("No agent with ID %q", id), "Use list_agents"))
		}
		return tools.ErrorResult(errorJSON("READ_FAILED", err.Error(), ""))
	}
	overrides := storedOverrideNames(state.Agent)
	activation := agentstore.ActivationNotAttempted
	if t.deps != nil && t.deps.AgentActiveRevision != nil && t.deps.AgentActiveRevision(id) == state.Revision {
		activation = agentstore.ActivationActive
	}
	// Visual identity readback (#1303): the stored figure/role/color, through
	// the same read backstop the REST surface uses — a stored value in the
	// catalog is emitted (color letter-case normalized); anything outside the
	// catalog reads as the shipped default. It never rewrites the stored file.
	readFigure, readRole, readColor := coreagent.WireIdentity(state.Agent.Figure, state.Agent.Role, state.Agent.Color)
	resp := map[string]any{
		"id": state.Agent.ID, "name": state.Agent.Name, "description": state.Agent.Description,
		"type": wireAgentType(*state.Agent), "soul": state.Soul, "skills": nonNilStrings(state.Agent.Skills),
		"figure": string(readFigure), "role": string(readRole), "color": string(readColor),
		"mcp_servers": storedMCPBindings(state.Agent), "stored_tool_overrides": storedOverrides(state.Agent),
		"override_names": overrides, "model": state.Agent.Model, "revision": state.Revision,
		"activation_status": activation, "editable_fields": editableDescriptors(*state.Agent),
		"memory_enabled": state.Agent.MemoryEnabled, "voice": state.Agent.Voice,
		"context_window_override": state.Agent.ContextWindowOverride,
		"model_params":            state.Agent.ModelParams,
	}
	return tools.NewToolResult(successJSON(resp))
}

type AgentGetToolsTool struct{ deps *Deps }

func NewAgentGetToolsTool(d *Deps) *AgentGetToolsTool { return &AgentGetToolsTool{deps: d} }
func (t *AgentGetToolsTool) Name() string             { return "get_agent_tools" }
func (t *AgentGetToolsTool) Scope() tools.ToolScope   { return tools.ScopeCore }
func (t *AgentGetToolsTool) Description() string {
	return "Read tool inventory and effective policies. Supply either id for an existing agent or new_agent_type (Main or Subagent) to preview native creation defaults before proposing a new agent. Preview is read-only, has no persisted id/revision, and includes no connector assignments. In preview, stored_tool_overrides contains creation defaults before proposed changes, not user-applied overrides. Main and Subagent share these defaults. Sparse creation patches modify only named policies; omitted policies retain their defaults. This does not grant or execute target tools."
}
func (t *AgentGetToolsTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object", "additionalProperties": false,
		"properties": map[string]any{
			"id":             map[string]any{"type": "string", "description": "Existing agent ID; omit for creation preview."},
			"name":           map[string]any{"type": "string", "description": "Existing agent's exact display name, instead of id (#1032). Several agents with that name are refused as ambiguous."},
			"new_agent_type": map[string]any{"type": "string", "enum": []string{"Main", "Subagent"}, "description": "Preview native creation defaults without creating an agent; mutually exclusive with id and name."},
		},
	}
}
func (t *AgentGetToolsTool) Execute(_ context.Context, args map[string]any) *tools.ToolResult {
	rawType, preview := args["new_agent_type"]
	_, hasID := args["id"]
	_, hasName := args["name"]
	selectors := 0
	for _, present := range []bool{preview, hasID, hasName} {
		if present {
			selectors++
		}
	}
	if selectors != 1 {
		return tools.ErrorResult(errorJSON("INVALID_INPUT",
			"Supply exactly one of: an existing agent's id, its exact name, or new_agent_type Main/Subagent", ""))
	}
	var state *agentstore.State
	var id string
	if preview {
		kind, ok := rawType.(string)
		if !ok || (kind != "Main" && kind != "Subagent") {
			return tools.ErrorResult(errorJSON("INVALID_INPUT", "Supply exactly one existing id or new_agent_type Main/Subagent", ""))
		}
		// Both native types use this same constructor in agentCreateToolExecute.
		// Preview in memory without touching the agent store.
		state = &agentstore.State{Agent: &config.AgentConfig{Tools: coreagent.NewCustomAgentToolsCfg()}}
	} else {
		home, err := resolveOmnipusHome(t.deps.Home)
		if err != nil {
			return tools.ErrorResult(errorJSON("WORKSPACE_ERROR", err.Error(), ""))
		}
		if hasName {
			var refusal *tools.ToolResult
			if id, refusal = agentIDByExactName(home, args["name"]); refusal != nil {
				return refusal
			}
		} else {
			id, _ = args["id"].(string)
		}
		var refusal *tools.ToolResult
		if state, refusal = readAgentStateExact(home, id); refusal != nil {
			return refusal
		}
	}

	global := map[string]string{}
	if t.deps.GetCfg != nil {
		if cfg := t.deps.GetCfg(); cfg != nil {
			for name, policy := range cfg.Sandbox.ToolPolicies {
				global[name] = policy
			}
		}
	}
	// One resolver for effective_policies and clamped: the runtime compositor
	// (wildcards and the God Mode floor), so the two never disagree (WU-3 R2-04).
	resolver := runtimePolicyResolver(global, storedOverrides(state.Agent), t.deps.godModeActive())
	effective := make(map[string]string, len(global))
	for name := range global {
		effective[name] = effectivePolicy(resolver, name)
	}
	for name := range resolver.Policies {
		effective[name] = effectivePolicy(resolver, name)
	}
	// Discovery is always available, even when stored policy says deny.
	effective["ToolSearch"] = effectivePolicy(resolver, "ToolSearch")
	connectorCatalog := map[string][]string{}
	if t.deps != nil {
		for server, names := range t.deps.currentInventory().LiveMCPTools {
			connectorCatalog[server] = sortedSetKeys(names)
		}
	}
	response := map[string]any{
		"id": id, "revision": state.Revision, "override_names": storedOverrideNames(state.Agent),
		"stored_tool_overrides": storedOverrides(state.Agent), "mcp_servers": storedMCPBindings(state.Agent),
		"effective_policies": effective, "static_catalog": sortedStringKeys(global),
		// #1064 A5: every stored policy the global ceiling makes stricter.
		"clamped":           policyClamps(global, storedOverrides(state.Agent), t.deps.godModeActive()),
		"connector_catalog": connectorCatalog,
	}
	if preview {
		delete(response, "id")
		delete(response, "revision")
		response["preview"] = true
		response["new_agent_type"] = rawType
	}
	return tools.NewToolResult(successJSON(response))
}

func sortedSetKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for name := range m {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func storedOverrides(a *config.AgentConfig) map[string]config.ToolPolicy {
	if a.Tools == nil || a.Tools.Builtin.Policies == nil {
		return map[string]config.ToolPolicy{}
	}
	return a.Tools.Builtin.Policies
}
func storedOverrideNames(a *config.AgentConfig) []string {
	names := make([]string, 0, len(storedOverrides(a)))
	for name := range storedOverrides(a) {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
func storedMCPBindings(a *config.AgentConfig) []config.AgentMCPServerBinding {
	if a.Tools == nil || a.Tools.MCP.Servers == nil {
		return []config.AgentMCPServerBinding{}
	}
	return a.Tools.MCP.Servers
}
func nonNilStrings(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}
func sortedStringKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
func wireAgentType(a config.AgentConfig) string {
	if a.IsExternalCLIWorker() {
		return "subagent_3p"
	}
	if a.IsWorker() {
		return "Subagent"
	}
	if a.IsSystem() {
		return "system"
	}
	if a.Type == config.AgentTypeCore {
		return "core"
	}
	return "Main"
}

func editableDescriptors(a config.AgentConfig) []map[string]any {
	// update_agent also writes AGENT.md (#1064).
	descriptors := append(agentmutation.FieldDescriptors(a), agentmutation.MetadataFileDescriptors(a)...)
	out := make([]map[string]any, 0, len(descriptors))
	for _, descriptor := range descriptors {
		editable, reason := descriptor.Editable, descriptor.Reason
		// FR-024: update_agent refuses the permission fields for every agent
		// (permissionFieldRefusal) — set_agent_permissions owns them on an
		// existing agent. The shared ADR-090 matrix still accepts them (and
		// the REST PUT surface still takes them), but this surface's
		// editable_fields must list exactly what update_agent accepts, so a
		// field the tool would refuse is never advertised as editable here.
		if editable && slices.Contains(permissionFieldNames, descriptor.Name) {
			editable = false
			reason = descriptor.Name + " cannot be changed with update_agent; per-agent tool policies and connector assignments change only through set_agent_permissions"
		}
		d := map[string]any{"name": descriptor.Name, "editable": editable}
		if reason != "" {
			d["reason"] = reason
		}
		out = append(out, d)
	}
	return out
}

// readAgentStateExact reads an agent's state by its EXACT id (#1032 F-2,
// chief ruling 2026-10-14): the id must be a valid agent id, and agentstore
// compares it with the stored id, so "OMNIPUS" never opens omnipus.json on a
// case-insensitive filesystem. Refusals are tool results.
func readAgentStateExact(home, id string) (*agentstore.State, *tools.ToolResult) {
	if refusal := agentIDRefusal(id); refusal != nil {
		return nil, refusal
	}
	// agentstore compares the stored id exactly, so a case variant is not found.
	state, err := agentstore.New(home).ReadState(id)
	if err != nil {
		if errors.Is(err, entity.ErrNotFound) {
			return nil, tools.ErrorResult(errorJSON("AGENT_NOT_FOUND", fmt.Sprintf("No agent with ID %q", id), "Use list_agents"))
		}
		return nil, tools.ErrorResult(errorJSON("READ_FAILED", err.Error(), "Use list_agents"))
	}
	return state, nil
}

// agentIDRefusal applies one id rule to every agent read surface (#1032 F-2):
// an unsafe id (empty, a path separator, "..", NUL) is INVALID_INPUT; a safe
// string that no agent id can have (spaces, say) is AGENT_NOT_FOUND — there
// is no such agent — rather than a validation error. nil when id may name an agent.
func agentIDRefusal(id string) *tools.ToolResult {
	if err := validateID(id); err != nil || strings.TrimSpace(id) == "" {
		msg := "id is required"
		if err != nil {
			msg = err.Error()
		}
		return tools.ErrorResult(errorJSON("INVALID_INPUT", msg, "Use an id from list_agents"))
	}
	if agentstore.ValidateAgentID(id) != nil {
		return tools.ErrorResult(errorJSON("AGENT_NOT_FOUND", fmt.Sprintf("No agent with ID %q", id), "Use list_agents"))
	}
	return nil
}

// agentRecordExistsExact reports whether the agent entity store holds a record
// whose file name is exactly id + ".json" (byte-exact, whatever the
// filesystem's case rules).
func agentRecordExistsExact(home, id string) (bool, error) {
	entries, err := os.ReadDir(agentstore.New(home).Dir())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("read the agent store: %w", err)
	}
	want := id + ".json"
	for _, e := range entries {
		if !e.IsDir() && e.Name() == want {
			return true, nil
		}
	}
	return false, nil
}

// agentIDByExactName resolves an agent's exact display name to its id
// (#1032; ruling Q4): no fuzzy or case-insensitive match. Names are free
// labels, so two agents may share one — that is refused as AMBIGUOUS with
// the candidate ids.
func agentIDByExactName(home string, raw any) (string, *tools.ToolResult) {
	name, ok := raw.(string)
	if !ok || strings.TrimSpace(name) == "" {
		return "", tools.ErrorResult(errorJSON("INVALID_INPUT", "name must be a non-empty string", "Use a name from list_agents"))
	}
	agents, skipped, err := agentstore.New(home).List()
	if err != nil {
		return "", tools.ErrorResult(errorJSON("READ_FAILED", fmt.Sprintf("the agent list could not be read: %v", err), "Retry list_agents"))
	}
	if len(skipped) > 0 {
		// An unreadable record may carry this name: absence or uniqueness
		// cannot be claimed from a partial list.
		return "", tools.ErrorResult(errorJSON("READ_FAILED",
			"agent names cannot be resolved reliably; unreadable agent records: "+strings.Join(skipped, ", "),
			"Repair the listed records, or call get_agent_tools with the agent's exact id"))
	}
	var ids []string
	for _, a := range agents {
		if a.Name == name {
			ids = append(ids, a.ID)
		}
	}
	sort.Strings(ids)
	switch len(ids) {
	case 0:
		return "", tools.ErrorResult(errorJSON("AGENT_NOT_FOUND", fmt.Sprintf("No agent is named %q", name),
			"Use list_agents to see the agents' names and ids"))
	case 1:
		return ids[0], nil
	}
	return "", tools.ErrorResult(errorJSON("AMBIGUOUS",
		fmt.Sprintf("%d agents are named %q: %s", len(ids), name, strings.Join(ids, ", ")),
		"Call get_agent_tools with one of these ids"))
}

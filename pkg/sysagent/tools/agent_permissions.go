package systools

import (
	"context"
	"sort"

	"github.com/elicify-ai/omnipus/pkg/agentmutation"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// AgentPermissionsTool implements set_agent_permissions: the ONLY agent-tool
// path that changes an existing agent's per-agent tool policies and connector
// assignments (omnipus-system-agent FR-024). It ships "ask" on the global
// ceiling and sits on the Auto ask-list (FR-021), so every permission change
// is approved by a person.
//
// Input vocabulary (chief ruling 2026-10-13): required id and revision (same
// revision refusal as update_agent), tool_policy_changes and mcp_servers with
// update_agent's former shapes and validation (the external-CLI worker
// refusal included), at least one of the two present, any other field
// refused. Omnipus never takes a per-agent "ask" (FR-022); one such member
// refuses the whole change.
type AgentPermissionsTool struct{ deps *Deps }

// NewAgentPermissionsTool returns the set_agent_permissions tool.
func NewAgentPermissionsTool(d *Deps) *AgentPermissionsTool { return &AgentPermissionsTool{deps: d} }

var agentPermissionsArgNames = map[string]struct{}{
	"id": {}, "revision": {}, "tool_policy_changes": {}, "mcp_servers": {},
}

func (t *AgentPermissionsTool) Name() string                 { return "set_agent_permissions" }
func (t *AgentPermissionsTool) Scope() tools.ToolScope       { return tools.ScopeCore }
func (t *AgentPermissionsTool) Category() tools.ToolCategory { return tools.CategoryAgents }

// Description documents the FR-024 permission split and the tool's inputs.
func (t *AgentPermissionsTool) Description() string {
	return "Change an existing agent's per-agent tool policies and connector (MCP) assignments. This is the only agent-tool path for those changes — update_agent refuses both. The shipped policy asks the user for approval before it runs; Omnipus skips approvals in God Mode. " +
		"Send id, revision (the opaque revision returned by get_agent), and at least one of tool_policy_changes or mcp_servers. A stale revision or any other input field is refused; re-read the agent before retrying a stale write. " +
		"tool_policy_changes is a sparse patch: set maps tool names to allow|ask|deny, and remove lists per-agent override names to delete. Unknown tools and invalid policy values are rejected; per-agent policies cannot widen the global tool policy. " +
		"mcp_servers replaces the full connector-assignment list; [] clears all bindings. Each entry is {id, tools?}: omitted tools grants all tools of that assigned server, an empty tools list grants none. Unknown servers or unknown/unavailable tool names are rejected. " +
		"External CLI workers reject these capability fields; Judge and Plan Supervisor keep fixed capabilities. Omnipus refuses any per-agent ask as a whole — use deny, or set ask on the global tool policy."
}

func (t *AgentPermissionsTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":                  map[string]any{"type": "string", "description": "Agent ID"},
			"revision":            map[string]any{"type": "string", "description": "Opaque revision returned by get_agent"},
			"tool_policy_changes": toolPolicyChangesParameters(),
			"mcp_servers":         mcpServersParameters(),
		},
		"required":             []string{"id", "revision"},
		"additionalProperties": false,
	}
}

func (t *AgentPermissionsTool) Execute(_ context.Context, args map[string]any) (res *tools.ToolResult) {
	defer func() { res = finalizeChangeResult(t.Name(), res) }()
	id, _ := args["id"].(string)
	revision, _ := args["revision"].(string)
	if err := validateID(id); err != nil {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", err.Error(), ""))
	}
	if revision == "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "revision is required", "Call get_agent and retry with its revision"))
	}
	if err := rejectUnknownAgentFields(args, agentPermissionsArgNames); err != nil {
		return fieldErrorResult(err)
	}
	fields := make([]string, 0, len(permissionFieldNames))
	for _, name := range permissionFieldNames {
		if _, present := args[name]; present {
			fields = append(fields, name)
		}
	}
	if len(fields) == 0 {
		return tools.ErrorResult(errorJSON("INVALID_INPUT",
			"at least one of tool_policy_changes or mcp_servers is required", ""))
	}
	sort.Strings(fields)
	if raw, present := args["tool_policy_changes"]; present {
		patch, err := decodePolicyPatch(raw)
		if err != nil {
			return fieldErrorResult(err)
		}
		if _, bad := coreagent.OmnipusPerAgentAskViolation(id, patch.Set); bad {
			return tools.ErrorResult(errorJSON("OMNIPUS_PER_AGENT_ASK", coreagent.OmnipusPerAgentAskRefusal,
				"Use deny for Omnipus, or set Ask on the global tool policy"))
		}
	}
	if raw, present := args["mcp_servers"]; present {
		if _, err := decodeMCPBindings(raw); err != nil {
			return fieldErrorResult(err)
		}
	}
	home, err := resolveOmnipusHome(t.deps.Home)
	if err != nil {
		return tools.ErrorResult(errorJSON("WORKSPACE_ERROR", err.Error(), ""))
	}
	known := knownToolPolicies(t.deps)
	inv := t.deps.currentInventory()
	store := agentstore.New(home)
	result, mutateErr := store.MutateState(id, revision, func(a *config.AgentConfig) error {
		if err := agentmutation.ValidateFields(*a, fields); err != nil {
			return err
		}
		return applyCapabilityArgs(a, args, known, inv)
	}, nil)
	if mutateErr != nil {
		return (&AgentUpdateTool{deps: t.deps}).mutateErrorResult(id, result, mutateErr)
	}
	activation, message := publishAgentActivation(t.deps, id)
	return changeSuccess(map[string]any{
		"id":                 id,
		"revision":           result.Revision,
		"persistence_status": agentstore.PersistenceComplete,
		"activation_status":  activation,
		"changed_fields":     fields,
		"message":            message,
	}, updateVerdict(activation, message))
}

var _ tools.Tool = (*AgentPermissionsTool)(nil)

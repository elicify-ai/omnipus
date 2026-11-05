package systools

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/agentmutation"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/entity"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

func storedOverrides(a *config.AgentConfig) map[string]config.ToolPolicy {
	if a.Tools == nil || a.Tools.Builtin.Policies == nil {
		return map[string]config.ToolPolicy{}
	}
	return a.Tools.Builtin.Policies
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
			"Repair the listed records, or call get_agent with the agent's exact id"))
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
		"Call get_agent with one of these ids"))
}

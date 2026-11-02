package agentmutation

import (
	"fmt"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/config"
)

type ErrorCode string

const (
	ProtectedField ErrorCode = "PROTECTED_FIELD"
	InvalidInput   ErrorCode = "INVALID_INPUT"
)

type FieldError struct {
	Code   ErrorCode
	Fields []string
	Reason string
}

func (e *FieldError) Error() string {
	return fmt.Sprintf("%s: fields %s: %s", e.Code, strings.Join(e.Fields, ", "), e.Reason)
}

var knownFields = map[string]struct{}{
	"name": {}, "description": {}, "figure": {}, "role": {}, "color": {}, "type": {}, "locked": {}, "soul": {},
	"skills": {}, "mcp_servers": {}, "tools_cfg": {}, "tool_policy_changes": {}, "model": {}, "provider": {},
	"fallback_models": {}, "context_window_override": {}, "model_params": {}, "max_tool_iterations": {},
	"memory_enabled": {}, "default": {}, "voice": {}, "executor": {}, "cli_path": {},
	// AGENT.md beside SOUL.md (#1064), written by the update_agent tool; it
	// follows soul's protection rules.
	"agent_md": {},
}

var alwaysImmutable = map[string]struct{}{"type": {}, "locked": {}}
var ordinaryProtected = map[string]struct{}{"name": {}, "description": {}, "figure": {}, "role": {}, "color": {}, "type": {}, "locked": {}, "soul": {}, "executor": {}, "agent_md": {}}
var hiddenProtected = map[string]struct{}{
	"name": {}, "description": {}, "figure": {}, "role": {}, "color": {}, "type": {}, "locked": {}, "skills": {}, "mcp_servers": {},
	"tools_cfg": {}, "tool_policy_changes": {}, "memory_enabled": {}, "executor": {}, "default": {}, "voice": {},
}
var externalUnsupported = map[string]struct{}{
	"soul": {}, "skills": {}, "mcp_servers": {}, "tools_cfg": {}, "tool_policy_changes": {}, "memory_enabled": {},
	"voice": {}, "context_window_override": {}, "model_params": {}, "fallback_models": {},
	"agent_md": {},
	// max_tool_iterations is supported (#904 D14): an external CLI worker
	// follows the global tool-iteration limit and may lower it; the
	// effective value is the CLI's turn cap.
}

var ordinaryBuiltins = map[string]struct{}{
	"mia": {}, "jim": {}, "omnipus": {}, "planner": {}, "researcher": {}, "worker": {},
}

type FieldDescriptor struct {
	Name     string
	Editable bool
	Reason   string
}

var describedFields = []string{"name", "description", "figure", "role", "color", "soul", "skills", "mcp_servers", "tool_policy_changes", "model", "provider", "fallback_models", "context_window_override", "model_params", "max_tool_iterations", "memory_enabled", "voice", "type"}

// metadataFileFields are the agent metadata files the update_agent tool writes
// beside SOUL.md (#1064). They are described separately because the REST
// agent update does not take them.
var metadataFileFields = []string{"agent_md"}

// MetadataFileDescriptors reports whether the update_agent tool may write the
// agent's AGENT.md (same rules as soul).
func MetadataFileDescriptors(agent config.AgentConfig) []FieldDescriptor {
	out := make([]FieldDescriptor, 0, len(metadataFileFields))
	for _, field := range metadataFileFields {
		err := ValidateFields(agent, []string{field})
		descriptor := FieldDescriptor{Name: field, Editable: err == nil}
		if err != nil {
			descriptor.Reason = err.Error()
		}
		out = append(out, descriptor)
	}
	return out
}

// FieldDescriptors reports which fields may be changed on agent.
func FieldDescriptors(agent config.AgentConfig) []FieldDescriptor {
	return describeFields(agent, ValidateFields)
}

func describeFields(agent config.AgentConfig, validate func(config.AgentConfig, []string) error) []FieldDescriptor {
	out := make([]FieldDescriptor, 0, len(describedFields))
	for _, field := range describedFields {
		err := validate(agent, []string{field})
		descriptor := FieldDescriptor{Name: field, Editable: err == nil}
		if err != nil {
			descriptor.Reason = err.Error()
		}
		out = append(out, descriptor)
	}
	return out
}

// ValidateFields validates a write to supplied agent fields: the ADR-090
// role matrix.
func ValidateFields(agent config.AgentConfig, supplied []string) error {
	var invalid []string
	for _, field := range supplied {
		if _, ok := knownFields[field]; !ok {
			invalid = append(invalid, field)
			continue
		}
		if agent.IsExternalCLIWorker() {
			if _, bad := externalUnsupported[field]; bad {
				invalid = append(invalid, field)
			}
		}
	}
	if len(invalid) > 0 {
		return &FieldError{Code: InvalidInput, Fields: invalid, Reason: "field is unknown or unsupported by this runtime"}
	}
	protected := make([]string, 0)
	_, ordinary := ordinaryBuiltins[agent.ID]
	for _, field := range supplied {
		if agent.IsSystem() {
			if _, bad := hiddenProtected[field]; bad {
				protected = append(protected, field)
			}
			continue
		}
		if ordinary || (agent.Locked && agent.Type == config.AgentTypeCore) {
			if _, bad := ordinaryProtected[field]; bad {
				protected = append(protected, field)
			}
			continue
		}
		if _, bad := alwaysImmutable[field]; bad {
			protected = append(protected, field)
		}
	}
	if len(protected) > 0 {
		return &FieldError{Code: ProtectedField, Fields: protected, Reason: "field is protected for this agent"}
	}
	return nil
}

// Table T-AGENT (tool-parity spec r17 §7.1, §7.5): the shared agent field
// rule rows, REST↔tool mappings and exclusions. Read by REST create and
// update, create_agent, update_agent, the descriptors above and the parity
// guard. Paths are canonical (r17 §7.1 rule 1).

// Operation names which write a row applies to (r17 §7.1 rule 4).
const (
	OpCreate = "create"
	OpUpdate = "update"
	OpBoth   = "both"
)

// FieldRule is one row of Table T-AGENT.
type FieldRule struct {
	Row      string
	Property string
	Rule     string
	Reason   string
}

// FieldMapping pairs a REST request leaf with a tool parameter leaf.
type FieldMapping struct {
	RESTPath string
	Tool     string
	ToolPath string
	Ops      string
}

// FieldExclusion is a one-sided leaf with its reason.
type FieldExclusion struct {
	Side   string
	Path   string
	Ops    string
	Reason string
}

var agentFieldRules = []FieldRule{
	{Row: "AG-01", Property: "id, type, locked", Rule: "immutable after create"},
	{Row: "AG-02", Property: "name … skills (ordinary fields)", Rule: "one rule table: ValidateFields"},
	{Row: "AG-03", Property: "fallback_models", Rule: "REST create stores the validated entries (#1440)"},
	{Row: "AG-04", Property: "soul", Rule: "not accepted for subagent_3p on any surface"},
	{Row: "AG-05", Property: "agent_md", Rule: "update_agent only", Reason: "a file beside the record; no screen edits it"},
	{Row: "AG-07", Property: "tools_cfg, tool_policy_changes, mcp_servers", Rule: "creation inputs; on existing agents only through set_agent_permissions"},
	{Row: "AG-08", Property: "executor.cli", Rule: "fixed list; refused on any update"},
	{Row: "AG-12", Property: "executor.env_overrides (+ _remove)", Rule: "create: map; update: merge + remove list; masked read"},
	{Row: "AG-13", Property: "executor.cli_args", Rule: "create, update"},
	{Row: "AG-14", Property: "rate_limits", Rule: "removed", Reason: "no per-agent rate limit exists; global limits in Settings → Security apply"},
}

// FieldRules returns Table T-AGENT in row order (a copy).
func FieldRules() []FieldRule {
	return append([]FieldRule(nil), agentFieldRules...)
}

// subagent3pForbiddenUpdateFields is every AgentUpdateRequest property a
// subagent_3p (External CLI) agent refuses on update: the external runner
// owns these (folded in from gateway/agent_field_rules.go, r17 §7.1). soul is
// here by r17 AG-04 / FR-110: an external worker has no soul at all.
var subagent3pForbiddenUpdateFields = []string{"tools_cfg", "skills", "fallback_models", "model_params", "soul"}

// Subagent3pForbiddenUpdateFields returns that list in refusal order (a copy).
func Subagent3pForbiddenUpdateFields() []string {
	return append([]string(nil), subagent3pForbiddenUpdateFields...)
}

var sharedAgentLeaves = []string{
	"color", "description", "fallback_models[].model", "fallback_models[].provider", "figure", "max_tool_iterations",
	"model", "model_params.max_tokens", "model_params.temperature", "name", "provider", "role", "skills[]", "soul", "voice",
}

const agentPermissionsReason = "AG-07: on an existing agent these change only through set_agent_permissions; update_agent refuses them"

// MappingRows returns the REST↔tool leaf mappings for agents. Create rows
// cover all three AgentCreateRequest variants.
func MappingRows() []FieldMapping {
	rows := make([]FieldMapping, 0, 2*len(sharedAgentLeaves)+14)
	for _, leaf := range sharedAgentLeaves {
		rows = append(rows,
			FieldMapping{RESTPath: leaf, Tool: "create_agent", ToolPath: leaf, Ops: OpCreate},
			FieldMapping{RESTPath: leaf, Tool: "update_agent", ToolPath: leaf, Ops: OpUpdate})
	}
	for _, leaf := range []string{"mcp_servers[].id", "mcp_servers[].tools[]", "tool_policy_changes.remove[]", "tool_policy_changes.set{}"} {
		rows = append(rows, FieldMapping{RESTPath: leaf, Tool: "create_agent", ToolPath: leaf, Ops: OpCreate})
	}
	for _, leaf := range []string{"context_window_override", "memory_enabled", "revision"} {
		rows = append(rows, FieldMapping{RESTPath: leaf, Tool: "update_agent", ToolPath: leaf, Ops: OpUpdate})
	}
	// Flattened tool parameters (r17 §7.1 rule 4).
	rows = append(rows,
		FieldMapping{RESTPath: "type", Tool: "create_agent", ToolPath: "agent_type", Ops: OpCreate},
		FieldMapping{RESTPath: "executor.cli", Tool: "create_agent", ToolPath: "cli", Ops: OpCreate},
		FieldMapping{RESTPath: "executor.cli_args", Tool: "create_agent", ToolPath: "cli_args", Ops: OpCreate},
		FieldMapping{RESTPath: "executor.env_overrides{}", Tool: "create_agent", ToolPath: "env_overrides{}", Ops: OpCreate},
		FieldMapping{RESTPath: "executor.cli_args", Tool: "update_agent", ToolPath: "cli_args", Ops: OpUpdate},
		FieldMapping{RESTPath: "executor.env_overrides{}", Tool: "update_agent", ToolPath: "env_overrides{}", Ops: OpUpdate},
		FieldMapping{RESTPath: "executor.env_overrides_remove[]", Tool: "update_agent", ToolPath: "env_overrides_remove[]", Ops: OpUpdate})
	return rows
}

// ExcludedRows returns the agent leaves that deliberately have no counterpart.
func ExcludedRows() []FieldExclusion {
	rows := make([]FieldExclusion, 0, 15)
	rows = append(rows, []FieldExclusion{
		{Side: "tool", Path: "agent_md", Ops: OpUpdate, Reason: "AG-05: a file beside the record; no screen edits it"},
		{Side: "tool", Path: "id", Ops: OpUpdate, Reason: "addresses the agent; REST carries it in the path /agents/{id}"},
		{Side: "tool", Path: "workspace_id", Ops: OpCreate, Reason: "the workspace the new agent joins (FR-040/FR-041); REST create joins no workspace"},
		{Side: "tool", Path: "context_window_override", Ops: OpCreate, Reason: "REST create has no such field; a person sets it after create through the REST update"},
		{Side: "tool", Path: "memory_enabled", Ops: OpCreate, Reason: "REST create has no such field; a person sets it after create through the REST update"},
	}...)
	for _, leaf := range []string{"tools_cfg.builtin.policies{}", "tools_cfg.mcp.servers[].id", "tools_cfg.mcp.servers[].tools[]"} {
		rows = append(rows,
			FieldExclusion{Side: "REST", Path: leaf, Ops: OpCreate, Reason: "AG-07: the tool's creation permission inputs are tool_policy_changes and mcp_servers"},
			FieldExclusion{Side: "REST", Path: leaf, Ops: OpUpdate, Reason: agentPermissionsReason})
	}
	for _, leaf := range []string{"mcp_servers[].id", "mcp_servers[].tools[]", "tool_policy_changes.remove[]", "tool_policy_changes.set{}"} {
		rows = append(rows, FieldExclusion{Side: "REST", Path: leaf, Ops: OpUpdate, Reason: agentPermissionsReason})
	}
	return rows
}

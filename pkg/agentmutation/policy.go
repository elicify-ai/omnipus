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
	"name": {}, "description": {}, "color": {}, "icon": {}, "type": {}, "locked": {}, "soul": {},
	"skills": {}, "mcp_servers": {}, "tools_cfg": {}, "tool_policy_changes": {}, "model": {}, "provider": {},
	"fallback_models": {}, "context_window_override": {}, "model_params": {}, "max_tool_iterations": {},
	"memory_enabled": {}, "default": {}, "voice": {}, "executor": {}, "cli_path": {},
	"auto_approve_disabled": {},
}

// operatorOnly fields may be written by the operator (REST, the SPA) but
// never through an agent's own mutation path (sysagent update/create tools,
// which call ValidateFields). auto_approve_disabled is ADR-092's per-agent
// "Never auto-approve" safety switch: setting it only tightens, but an agent
// able to write it could also turn it back off and loosen its own
// Auto-approve past what the operator chose.
var operatorOnly = map[string]struct{}{"auto_approve_disabled": {}}

// operatorOnlyReason is the refusal an agent-path write of an operatorOnly
// field gets, and the reason its agent-view descriptor carries.
const operatorOnlyReason = "only the operator can change this safety setting; an agent cannot change it for itself or another agent"

var alwaysImmutable = map[string]struct{}{"type": {}, "locked": {}}
var ordinaryProtected = map[string]struct{}{"name": {}, "description": {}, "color": {}, "icon": {}, "type": {}, "locked": {}, "soul": {}, "executor": {}}
var hiddenProtected = map[string]struct{}{
	"name": {}, "description": {}, "color": {}, "icon": {}, "type": {}, "locked": {}, "skills": {}, "mcp_servers": {},
	"tools_cfg": {}, "tool_policy_changes": {}, "memory_enabled": {}, "executor": {}, "default": {}, "voice": {},
}
var externalUnsupported = map[string]struct{}{
	"soul": {}, "skills": {}, "mcp_servers": {}, "tools_cfg": {}, "tool_policy_changes": {}, "memory_enabled": {},
	"voice": {}, "context_window_override": {}, "model_params": {}, "max_tool_iterations": {}, "fallback_models": {},
	// An external CLI worker runs its own CLI's tools, never Omnipus's
	// ask-policy tools, so there is no Auto-approve to switch off.
	"auto_approve_disabled": {},
}

var ordinaryBuiltins = map[string]struct{}{
	"mia": {}, "jim": {}, "ava": {}, "admin": {}, "planner": {}, "researcher": {}, "worker": {},
}

type FieldDescriptor struct {
	Name     string
	Editable bool
	Reason   string
}

var describedFields = []string{"name", "description", "color", "icon", "soul", "skills", "mcp_servers", "tool_policy_changes", "model", "provider", "fallback_models", "context_window_override", "model_params", "max_tool_iterations", "memory_enabled", "default", "voice", "type", "auto_approve_disabled"}

// FieldDescriptors is the AGENT view of which fields may be changed (the
// sysagent read tool): operatorOnly fields are listed as not editable.
func FieldDescriptors(agent config.AgentConfig) []FieldDescriptor {
	return describeFields(agent, ValidateFields)
}

// OperatorFieldDescriptors is the OPERATOR view (the REST Agent
// editable_fields the SPA's buildAgentUpdate checks): operatorOnly fields
// are editable wherever the agent's runtime supports them.
func OperatorFieldDescriptors(agent config.AgentConfig) []FieldDescriptor {
	return describeFields(agent, ValidateOperatorFields)
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

// ValidateFields validates a write arriving through an AGENT's own mutation
// path (sysagent tools): the ADR-090 role matrix, plus a refusal of every
// operatorOnly field.
func ValidateFields(agent config.AgentConfig, supplied []string) error {
	return validateFields(agent, supplied, false)
}

// ValidateOperatorFields validates a write by the operator (REST PUT
// /agents/{id}): the same role matrix, with operatorOnly fields allowed.
func ValidateOperatorFields(agent config.AgentConfig, supplied []string) error {
	return validateFields(agent, supplied, true)
}

func validateFields(agent config.AgentConfig, supplied []string, operator bool) error {
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
	if !operator {
		var reserved []string
		for _, field := range supplied {
			if _, ok := operatorOnly[field]; ok {
				reserved = append(reserved, field)
			}
		}
		if len(reserved) > 0 {
			return &FieldError{Code: ProtectedField, Fields: reserved, Reason: operatorOnlyReason}
		}
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

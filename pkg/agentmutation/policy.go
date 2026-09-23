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
}

var alwaysImmutable = map[string]struct{}{"type": {}, "locked": {}}
var ordinaryProtected = map[string]struct{}{"name": {}, "description": {}, "color": {}, "icon": {}, "type": {}, "locked": {}, "soul": {}, "executor": {}}
var hiddenProtected = map[string]struct{}{
	"name": {}, "description": {}, "color": {}, "icon": {}, "type": {}, "locked": {}, "skills": {}, "mcp_servers": {},
	"tools_cfg": {}, "tool_policy_changes": {}, "memory_enabled": {}, "executor": {}, "default": {}, "voice": {},
}
var externalUnsupported = map[string]struct{}{
	"soul": {}, "skills": {}, "mcp_servers": {}, "tools_cfg": {}, "tool_policy_changes": {}, "memory_enabled": {},
	"voice": {}, "context_window_override": {}, "model_params": {}, "max_tool_iterations": {}, "fallback_models": {},
}

var ordinaryBuiltins = map[string]struct{}{
	"mia": {}, "jim": {}, "ava": {}, "admin": {}, "planner": {}, "researcher": {}, "worker": {},
}

type FieldDescriptor struct {
	Name     string
	Editable bool
	Reason   string
}

var describedFields = []string{"name", "description", "color", "icon", "soul", "skills", "mcp_servers", "tool_policy_changes", "model", "provider", "fallback_models", "context_window_override", "model_params", "max_tool_iterations", "memory_enabled", "default", "voice", "type"}

func FieldDescriptors(agent config.AgentConfig) []FieldDescriptor {
	out := make([]FieldDescriptor, 0, len(describedFields))
	for _, field := range describedFields {
		err := ValidateFields(agent, []string{field})
		descriptor := FieldDescriptor{Name: field, Editable: err == nil}
		if err != nil {
			descriptor.Reason = err.Error()
		}
		out = append(out, descriptor)
	}
	return out
}

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

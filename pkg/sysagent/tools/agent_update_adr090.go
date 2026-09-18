package systools

import (
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/agentmutation"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/entity"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

func (t *AgentUpdateTool) executeADR090(args map[string]any) *tools.ToolResult {
	id, _ := args["id"].(string)
	revision, _ := args["revision"].(string)
	if err := validateID(id); err != nil {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", err.Error(), ""))
	}
	if revision == "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "revision is required", "Call get_agent and retry with its revision"))
	}
	if err := rejectUnknownAgentFields(args, agentUpdateArgNames); err != nil {
		return fieldErrorResult(err)
	}
	fields := suppliedAgentFields(args)
	if len(fields) == 0 {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "at least one editable field is required", ""))
	}
	if err := validateColorIconArgs(args); err != nil {
		return err
	}
	home, err := resolveOmnipusHome(t.deps.Home)
	if err != nil {
		return tools.ErrorResult(errorJSON("WORKSPACE_ERROR", err.Error(), ""))
	}
	store := agentstore.New(home)
	soul, err := optionalSoul(args)
	if err != nil {
		return fieldErrorResult(err)
	}
	if _, present := args["default"]; present {
		if err := requireDefaultWriter(t.deps); err != nil {
			return fieldErrorResult(err)
		}
	}
	known := knownToolPolicies(t.deps)
	inv := t.deps.currentInventory()
	result, mutateErr := store.MutateState(id, revision, func(a *config.AgentConfig) error {
		if err := agentmutation.ValidateFields(*a, fields); err != nil {
			return err
		}
		return applyAgentToolArgs(a, args, known, inv)
	}, soul)
	if mutateErr != nil {
		return t.mutateErrorResult(id, result, mutateErr)
	}
	if raw, present := args["default"]; present {
		if want, ok := raw.(bool); ok {
			if err := writeDefaultSingleton(t.deps, id, want); err != nil {
				slog.Warn("sysagent: default singleton write failed", "id", id, "error", err)
				return defaultSingletonPartialResult(id, result.Revision, fields)
			}
		}
	}
	activation, message := publishAgentActivation(t.deps, id)
	if _, hasVoice := args["voice"]; hasVoice && message == "" {
		message = "voice is persisted; playback is inactive in this release"
	}
	return tools.NewToolResult(successJSON(map[string]any{
		"id":                 id,
		"revision":           result.Revision,
		"persistence_status": agentstore.PersistenceComplete,
		"activation_status":  activation,
		"changed_fields":     fields,
		"message":            message,
	}))
}

func (t *AgentUpdateTool) mutateErrorResult(id string, result agentstore.MutationResult, mutateErr error) *tools.ToolResult {
	var fe *agentmutation.FieldError
	switch {
	case errors.Is(mutateErr, entity.ErrNotFound):
		return tools.ErrorResult(errorJSON("AGENT_NOT_FOUND", fmt.Sprintf("No agent with ID %q", id), "Use list_agents"))
	case errors.Is(mutateErr, agentstore.ErrInvalidRevision):
		return tools.ErrorResult(errorJSON("INVALID_INPUT", mutateErr.Error(), "Call get_agent"))
	case errors.Is(mutateErr, agentstore.ErrRevisionConflict):
		return tools.ErrorResult(errorJSON("CONFLICT", mutateErr.Error(), "Re-read with get_agent and revise the proposal"))
	case errors.As(mutateErr, &fe):
		return fieldErrorResult(fe)
	default:
		if result.PersistenceStatus == "" {
			result.PersistenceStatus = agentstore.PersistenceNone
		}
		if result.ErrorStage == "" {
			result.ErrorStage = "prepare"
		}
		slog.Warn("sysagent: save updated agent failed", "id", id, "stage", result.ErrorStage, "error", mutateErr)
		payload := map[string]any{
			"code":               "SAVE_FAILED",
			"message":            "Agent configuration could not be saved. Read its current state before retrying.",
			"persistence_status": result.PersistenceStatus,
			"activation_status":  result.ActivationStatus,
			"changed_fields":     append([]string{}, result.ChangedFields...),
			"error_stage":        result.ErrorStage,
		}
		if result.Revision != "" {
			payload["revision"] = result.Revision
		}
		return tools.ErrorResult(successJSON(payload))
	}
}

func fieldErrorResult(err error) *tools.ToolResult {
	var fe *agentmutation.FieldError
	if errors.As(err, &fe) {
		return tools.ErrorResult(errorJSON(string(fe.Code), fe.Error(), ""))
	}
	return tools.ErrorResult(errorJSON("INVALID_INPUT", err.Error(), ""))
}

func validateColorIconArgs(args map[string]any) *tools.ToolResult {
	if color, ok := args["color"].(string); ok {
		if err := validateAgentColor(color); err != nil {
			return tools.ErrorResult(errorJSON("INVALID_COLOR", err.Error(), "Use a 6-digit hex color"))
		}
	}
	if icon, ok := args["icon"].(string); ok {
		if err := validateAgentIcon(icon); err != nil {
			return tools.ErrorResult(errorJSON("INVALID_ICON", err.Error(), "Use a Phosphor icon name"))
		}
	}
	return nil
}

func optionalSoul(args map[string]any) (*string, error) {
	raw, present := args["soul"]
	if !present {
		return nil, nil
	}
	v, ok := raw.(string)
	if !ok || strings.TrimSpace(v) == "" {
		return nil, fieldErr("soul", "soul must be a nonblank string")
	}
	return &v, nil
}

func suppliedAgentFields(args map[string]any) []string {
	fields := mutationFieldNames(args)
	sort.Strings(fields)
	return fields
}

var _ tools.Tool = (*AgentUpdateTool)(nil)

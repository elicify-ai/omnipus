package systools

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/agentmutation"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/datamodel"
	"github.com/elicify-ai/omnipus/pkg/entity"
	"github.com/elicify-ai/omnipus/pkg/fileutil"
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
	if refusal := permissionFieldRefusal(args); refusal != nil {
		return refusal
	}
	if err := rejectUnknownAgentFields(args, agentUpdateArgNames); err != nil {
		return fieldErrorResult(err)
	}
	if name, ok := args["name"].(string); ok && coreagent.IsReservedAgentName(name) {
		return reservedNameResult()
	}
	fields := suppliedAgentFields(args)
	meta, metaErr := agentMetadataWrites(args)
	if metaErr != nil {
		return fieldErrorResult(metaErr)
	}
	if len(fields) == 0 && len(meta) == 0 {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "at least one editable field is required", ""))
	}
	if refusal := validateVisualUpdateArgs(args); refusal != nil {
		return refusal
	}
	home, err := resolveOmnipusHome(t.deps.Home)
	if err != nil {
		return tools.ErrorResult(errorJSON("WORKSPACE_ERROR", err.Error(), ""))
	}
	store := agentstore.New(home)
	if len(meta) > 0 {
		if refusal := checkMetadataWriteAllowed(store, id, meta); refusal != nil {
			return refusal
		}
	}
	newRevision := revision
	if len(fields) > 0 {
		result, refusal := t.mutateRecord(store, id, revision, fields, args)
		if refusal != nil {
			return refusal
		}
		newRevision = result.Revision
	} else if refusal := checkRevisionUnchanged(store, id, revision); refusal != nil {
		return refusal
	}
	written, unverified, metaFailure := writeAgentMetadataFiles(home, id, meta)
	durable := append(append([]string{}, fields...), written...)
	if metaFailure != nil {
		if len(durable) == 0 {
			return tools.ErrorResult(errorJSON("SAVE_FAILED", metaFailure.Error(), "Read the agent with get_agent before retrying"))
		}
		missing := missingMetadata(meta, written)
		if unverified != "" {
			missing = append(missing, "read-back of "+unverified)
		}
		return changeErrorWithOutcome(map[string]any{
			"success":            false,
			"error":              map[string]any{"code": "SAVE_FAILED", "message": metaFailure.Error()},
			"id":                 id,
			"revision":           newRevision,
			"persistence_status": agentstore.PersistencePartial,
			"changed_fields":     durable,
		}, partlySaved(metaFailure.Error(), "", missing))
	}
	activation, message := publishAgentActivation(t.deps, id)
	if _, hasVoice := args["voice"]; hasVoice && message == "" {
		message = "voice is persisted; playback is inactive in this release"
	}
	return changeSuccess(map[string]any{
		"id":                 id,
		"revision":           newRevision,
		"persistence_status": agentstore.PersistenceComplete,
		"activation_status":  activation,
		"changed_fields":     durable,
		"message":            message,
	}, updateVerdict(activation, message))
}

// mutateRecord applies the record and SOUL.md fields under the store's
// revision check. A refusal or failure comes back as the tool result.
func (t *AgentUpdateTool) mutateRecord(store *agentstore.Store, id, revision string, fields []string,
	args map[string]any,
) (agentstore.MutationResult, *tools.ToolResult) {
	// An absent soul is a normal "field not supplied" case, not an error: the
	// pointer stays nil and MutateState leaves the persisted soul untouched.
	var soul *string
	if raw, present := args["soul"]; present {
		v, ok := raw.(string)
		if !ok || strings.TrimSpace(v) == "" {
			return agentstore.MutationResult{}, fieldErrorResult(fieldErr("soul", "soul must be a nonblank string"))
		}
		soul = &v
	}
	known := knownToolPolicies(t.deps)
	inv := t.deps.currentInventory()
	result, mutateErr := store.MutateState(id, revision, func(a *config.AgentConfig) error {
		if err := agentmutation.ValidateFields(*a, fields); err != nil {
			return err
		}
		// #904 D10/D15: read the global in force INSIDE the closure, under
		// the agent store's per-entity lock, so the check runs against the
		// config current at write time rather than at call start. Residual
		// window: this path does not hold the gateway's configMu, so a
		// PUT /performance lowering that commits between this read and the
		// record write does not see this value — the agent then ends up
		// above the new global, which the resolver caps and flags (D1).
		// Never unbounded, and visible on the profile.
		return applyAgentToolArgs(a, args, known, inv, t.deps.agentDefaults())
	}, soul)
	if mutateErr != nil {
		return result, t.mutateErrorResult(id, result, mutateErr)
	}
	return result, nil
}

// updateVerdict is update_agent's FR-030 verdict from the existing read-back:
// the saved record (MutateState re-reads under its lock) and the live
// activation of the new revision.
func updateVerdict(activation agentstore.ActivationStatus, message string) changeOutcome {
	const scope = "saved agent record and files; live activation of the new revision"
	notTested := []string{"a conversation turn with the changed agent"}
	if activation != agentstore.ActivationActive {
		reason := fmt.Sprintf("the change is saved but live activation was not confirmed (%s)", activation)
		if message != "" {
			reason += ": " + message
		}
		return partlySaved(reason, scope, []string{"live activation"}, notTested...)
	}
	return working(scope, notTested...)
}

// agentMetadataFile maps update_agent's metadata arguments to their files
// (#1064). SOUL.md stays with the record (soul, under the revision check).
// HEARTBEAT.md is deliberately absent: heartbeats are per workspace
// (member_configs, pkg/gateway/heartbeat_schedule.go) and no runtime path
// reads an agent's HEARTBEAT.md, so writing it would report a change that
// does nothing.
var agentMetadataFile = []struct{ arg, file string }{
	{"agent_md", "AGENT.md"},
}

// agentMetadataWrite is one requested metadata file write.
type agentMetadataWrite struct{ arg, file, content string }

// agentMetadataWrites returns the requested metadata writes in a fixed order.
// Each value must be a string; an empty string empties the file.
func agentMetadataWrites(args map[string]any) ([]agentMetadataWrite, error) {
	var out []agentMetadataWrite
	for _, m := range agentMetadataFile {
		raw, present := args[m.arg]
		if !present {
			continue
		}
		v, ok := raw.(string)
		if !ok {
			return nil, fieldErr(m.arg, m.arg+" must be a string")
		}
		out = append(out, agentMetadataWrite{arg: m.arg, file: m.file, content: v})
	}
	return out, nil
}

// checkMetadataWriteAllowed applies the shared field rules to the metadata
// files (they follow soul's: ordinary built-ins protect these instruction
// files, hidden system agents keep them editable, external CLI workers do not
// support them).
func checkMetadataWriteAllowed(store *agentstore.Store, id string, writes []agentMetadataWrite) *tools.ToolResult {
	a, err := store.Get(id)
	if err != nil {
		if errors.Is(err, entity.ErrNotFound) {
			return tools.ErrorResult(errorJSON("AGENT_NOT_FOUND", fmt.Sprintf("No agent with ID %q", id), "Use list_agents"))
		}
		return tools.ErrorResult(errorJSON("READ_FAILED", fmt.Sprintf("agent %q could not be read: %v", id, err), ""))
	}
	fields := make([]string, 0, len(writes))
	for _, w := range writes {
		fields = append(fields, w.arg)
	}
	if err := agentmutation.ValidateFields(*a, fields); err != nil {
		return fieldErrorResult(err)
	}
	return nil
}

// checkRevisionUnchanged refuses a metadata-only update whose entity/SOUL
// revision is stale. The revision covers only the entity record and SOUL.md,
// not AGENT.md, so it does not detect a concurrent AGENT.md-only change
// (tracked as #1321).
func checkRevisionUnchanged(store *agentstore.Store, id, revision string) *tools.ToolResult {
	current, err := store.PeekRevision(id)
	if err != nil {
		if errors.Is(err, entity.ErrNotFound) {
			return tools.ErrorResult(errorJSON("AGENT_NOT_FOUND", fmt.Sprintf("No agent with ID %q", id), "Use list_agents"))
		}
		return tools.ErrorResult(errorJSON("READ_FAILED", fmt.Sprintf("agent %q could not be read: %v", id, err), ""))
	}
	if current != revision {
		return tools.ErrorResult(errorJSON("CONFLICT",
			fmt.Sprintf("agent %q changed: expected revision %q, current %q", id, revision, current),
			"Re-read with get_agent and revise the proposal"))
	}
	return nil
}

// writeAgentMetadataFiles writes each requested file atomically (0600) in the
// agent's home and reads it back. It stops at the first failure. written lists
// the arguments whose file was published — recorded as soon as the atomic
// write succeeds, because publication is durable whether or not the read-back
// then works (WU-3 SF-05). unverified names the file whose read-back failed or
// differed, if that was the failure.
func writeAgentMetadataFiles(home, id string, writes []agentMetadataWrite) (written []string, unverified string, err error) {
	dir := datamodel.AgentHomePath(home, id)
	for _, w := range writes {
		path := filepath.Join(dir, w.file)
		if werr := fileutil.WriteFileAtomic(path, []byte(w.content), 0o600); werr != nil {
			return written, "", fmt.Errorf("%s could not be written: %w", w.file, werr)
		}
		written = append(written, w.arg)
		back, rerr := readAgentMetadataBack(path)
		if rerr != nil {
			return written, w.file, fmt.Errorf("%s was saved but could not be read back: %w", w.file, rerr)
		}
		if string(back) != w.content {
			return written, w.file, fmt.Errorf("%s was saved but reads back differently", w.file)
		}
	}
	return written, "", nil
}

// readAgentMetadataBack reads a just-written metadata file back. A variable
// only so a test can fail the read-back (FailAgentMetadataReadBackForTest);
// production never reassigns it.
var readAgentMetadataBack = os.ReadFile

// missingMetadata lists the requested metadata files that were not written.
func missingMetadata(writes []agentMetadataWrite, written []string) []string {
	done := map[string]bool{}
	for _, w := range written {
		done[w] = true
	}
	var missing []string
	for _, w := range writes {
		if !done[w.arg] {
			missing = append(missing, w.file)
		}
	}
	return missing
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
		if result.PersistenceStatus == agentstore.PersistencePartial {
			return changeErrorWithOutcome(payload, partlySaved(
				fmt.Sprintf("the agent update was partly written (stage %s): %v", result.ErrorStage, mutateErr), "",
				[]string{"stage " + result.ErrorStage}))
		}
		return tools.ErrorResult(successJSON(payload))
	}
}

// reservedNameResult is the FR-002 refusal in this package's error shape.
func reservedNameResult() *tools.ToolResult {
	return tools.ErrorResult(errorJSON("RESERVED_NAME", coreagent.ReservedNameRefusal, "Choose a different name"))
}

func fieldErrorResult(err error) *tools.ToolResult {
	var fe *agentmutation.FieldError
	if errors.As(err, &fe) {
		return tools.ErrorResult(errorJSON(string(fe.Code), fe.Error(), ""))
	}
	return tools.ErrorResult(errorJSON("INVALID_INPUT", err.Error(), ""))
}

func suppliedAgentFields(args map[string]any) []string {
	fields := mutationFieldNames(args)
	sort.Strings(fields)
	return fields
}

var _ tools.Tool = (*AgentUpdateTool)(nil)

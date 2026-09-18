// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"errors"
	"log/slog"
	"net/http"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// readWorkspaceInstructionsAfterWrite is the post-write readback used by
// instructions PUT. Tests replace it to inject a verify failure after the
// bytes have already been written.
var readWorkspaceInstructionsAfterWrite = workspace.ReadInstructionsForManagement

func instructionsMutationState(persistence gen.ConfigurationMutationStatePersistenceStatus, activation gen.ConfigurationMutationStateActivationStatus, revision string, changed []string, stage, message string) gen.ConfigurationMutationState {
	state := gen.ConfigurationMutationState{
		PersistenceStatus: persistence,
		ActivationStatus:  activation,
		Revision:          revision,
		ChangedFields:     changed,
	}
	if stage != "" {
		state.ErrorStage = &stage
	}
	if message != "" {
		state.Message = &message
	}
	return state
}

// handleWorkspaceInstructionsGet returns the content of the workspace's AGENT.md
// (Project Instructions) and the opaque revision of those bytes.
//
// GET /api/v1/workspaces/{id}/instructions
func (a *restAPI) handleWorkspaceInstructionsGet(w http.ResponseWriter, _ *http.Request, id string) {
	if err := validateEntityID(id); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid workspace ID")
		return
	}
	unlock := workspace.LockID(id)
	defer unlock()

	if !workspace.Exists(a.homePath, id) {
		jsonErr(w, http.StatusNotFound, "workspace not found")
		return
	}
	content, err := workspace.ReadInstructionsForManagement(a.homePath, id)
	if err != nil {
		slog.Error("rest: read workspace instructions", "error", err, "id", id)
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return
	}
	jsonOK(w, gen.WorkspaceInstructionsResponse{
		Content:  content,
		Revision: workspace.RevisionForInstructions(content),
	})
}

// handleWorkspaceInstructionsPut replaces the workspace's AGENT.md with the
// supplied content when the caller's revision still matches. An empty string
// clears the file. Conflict is zero-write; success readback uses the same lock.
//
// PUT /api/v1/workspaces/{id}/instructions
func (a *restAPI) handleWorkspaceInstructionsPut(w http.ResponseWriter, r *http.Request, id string) {
	if err := validateEntityID(id); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid workspace ID")
		return
	}

	unlock := workspace.LockID(id)
	defer unlock()

	if !workspace.Exists(a.homePath, id) {
		jsonErr(w, http.StatusNotFound, "workspace not found")
		return
	}

	var req gen.WorkspaceInstructionsRequest
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "WorkspaceInstructionsRequest", &req, validateEnabled) {
		return
	}
	if err := workspace.ValidateRevision(req.Revision); err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}

	current, err := workspace.ReadInstructionsForManagement(a.homePath, id)
	if err != nil {
		slog.Error("rest: read workspace instructions before write", "error", err, "id", id)
		writeJSON(w, http.StatusInternalServerError, gen.ConfigurationMutationFailureState{
			PersistenceStatus: gen.ConfigurationMutationFailureStatePersistenceStatusNone,
			ActivationStatus:  gen.ConfigurationMutationFailureStateActivationStatusNotAttempted,
			ChangedFields:     []string{},
			ErrorStage:        "read_instructions",
			Message:           "workspace instructions could not be read",
		})
		return
	}
	currentRevision := workspace.RevisionForInstructions(current)
	if currentRevision != req.Revision {
		jsonErr(w, http.StatusConflict, "workspace instructions revision conflict")
		return
	}

	if err = workspace.WriteInstructions(a.homePath, id, req.Content); err != nil {
		if errors.Is(err, workspace.ErrInstructionsTooLarge) || errors.Is(err, workspace.ErrInvalidWorkspaceID) {
			jsonErr(w, http.StatusBadRequest, err.Error())
			return
		}
		slog.Error("rest: write workspace instructions", "error", err, "id", id)
		writeJSON(w, http.StatusInternalServerError, gen.ConfigurationMutationFailureState{
			PersistenceStatus: gen.ConfigurationMutationFailureStatePersistenceStatusNone,
			ActivationStatus:  gen.ConfigurationMutationFailureStateActivationStatusNotAttempted,
			Revision:          &currentRevision,
			ChangedFields:     []string{},
			ErrorStage:        "write_instructions",
			Message:           "workspace instructions could not be saved",
		})
		return
	}

	actual, err := readWorkspaceInstructionsAfterWrite(a.homePath, id)
	if err != nil {
		slog.Error("rest: read workspace instructions after write", "error", err, "id", id)
		writeJSON(w, http.StatusInternalServerError, gen.ConfigurationMutationFailureState{
			PersistenceStatus: gen.ConfigurationMutationFailureStatePersistenceStatusPartial,
			ActivationStatus:  gen.ConfigurationMutationFailureStateActivationStatusNotAttempted,
			ChangedFields:     []string{"instructions"},
			ErrorStage:        "verify_instructions",
			Message:           "workspace instructions were written but could not be read back; revision is unknown",
		})
		return
	}
	changed := []string{}
	if actual != current {
		changed = []string{"instructions"}
	}
	writeJSON(w, http.StatusOK, instructionsMutationState(
		gen.ConfigurationMutationStatePersistenceStatusComplete,
		gen.ConfigurationMutationStateActivationStatusActive,
		workspace.RevisionForInstructions(actual),
		changed, "", "",
	))
}

// HandleWorkspaceInstructions dispatches GET and PUT for
// /api/v1/workspaces/{id}/instructions. The id path segment has already been
// extracted by HandleWorkspaces before this function is called.
func (a *restAPI) HandleWorkspaceInstructions(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		a.handleWorkspaceInstructionsGet(w, r, id)
	case http.MethodPut:
		a.handleWorkspaceInstructionsPut(w, r, id)
	default:
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

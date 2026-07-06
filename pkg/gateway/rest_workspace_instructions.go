//go:build !cgo

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

// handleWorkspaceInstructionsGet returns the content of the workspace's AGENT.md
// (Project Instructions).
//
// GET /api/v1/workspaces/{id}/instructions
//
// 200 — WorkspaceInstructionsResponse{Content: "..."} (empty string when absent)
// 400 — invalid workspace ID
// 404 — workspace metadata file not found
// 500 — read error
func (a *restAPI) handleWorkspaceInstructionsGet(w http.ResponseWriter, _ *http.Request, id string) {
	if err := validateEntityID(id); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid workspace ID")
		return
	}
	if !workspace.Exists(a.homePath, id) {
		jsonErr(w, http.StatusNotFound, "workspace not found")
		return
	}
	content, err := workspace.ReadInstructions(a.homePath, id)
	if err != nil {
		slog.Error("rest: read workspace instructions", "error", err, "id", id)
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return
	}
	jsonOK(w, gen.WorkspaceInstructionsResponse{Content: content})
}

// handleWorkspaceInstructionsPut replaces the workspace's AGENT.md with the
// supplied content. An empty string clears the file.
//
// PUT /api/v1/workspaces/{id}/instructions
//
// 200 — WorkspaceInstructionsResponse{Content: req.Content} echoed back
// 400 — invalid workspace ID, bad JSON, or body exceeds 256 KB
// 404 — workspace metadata file not found
// 500 — write error
func (a *restAPI) handleWorkspaceInstructionsPut(w http.ResponseWriter, r *http.Request, id string) {
	if err := validateEntityID(id); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid workspace ID")
		return
	}
	if !workspace.Exists(a.homePath, id) {
		jsonErr(w, http.StatusNotFound, "workspace not found")
		return
	}

	var req gen.WorkspaceInstructionsRequest
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "WorkspaceInstructionsRequest", &req, validateEnabled) {
		return
	}

	if err := workspace.WriteInstructions(a.homePath, id, req.Content); err != nil {
		// Oversized content is a client error (shrink the payload). An invalid id
		// is unreachable here (validateEntityID + Exists already gate it) but is
		// classified 400 for safety. Anything else is a server-side I/O failure.
		if errors.Is(err, workspace.ErrInstructionsTooLarge) || errors.Is(err, workspace.ErrInvalidWorkspaceID) {
			jsonErr(w, http.StatusBadRequest, err.Error())
			return
		}
		slog.Error("rest: write workspace instructions", "error", err, "id", id)
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return
	}
	jsonOK(w, gen.WorkspaceInstructionsResponse{Content: req.Content})
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

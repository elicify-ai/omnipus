//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/media/library"
)

// HandleWorkspaceMedia dispatches /api/v1/workspaces/{id}/media* requests.
func (a *restAPI) HandleWorkspaceMedia(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSuffix(r.URL.Path, "/")
	rest := strings.TrimPrefix(path, "/api/v1/workspaces")

	// rest is /{id}/media[/{media_id}]
	parts := strings.SplitN(strings.TrimPrefix(rest, "/"), "/", 4)
	if len(parts) < 2 || parts[1] != "media" {
		http.NotFound(w, r)
		return
	}
	workspaceID := parts[0]
	if err := validateEntityID(workspaceID); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid workspace ID")
		return
	}

	// GET /workspaces/{id}/media
	if len(parts) == 2 {
		if r.Method != http.MethodGet {
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		a.handleWorkspaceMediaList(w, r, workspaceID)
		return
	}

	// POST /workspaces/{id}/media/attachments
	if len(parts) == 3 && parts[2] == "attachments" {
		if r.Method != http.MethodPost {
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		a.handleWorkspaceMediaAttach(w, r, workspaceID)
		return
	}

	// GET /workspaces/{id}/media/{media_id} or DELETE /workspaces/{id}/media/{media_id}
	if len(parts) == 3 && parts[2] != "" {
		mediaID := parts[2]
		switch r.Method {
		case http.MethodGet:
			a.handleWorkspaceMediaGet(w, r, workspaceID, mediaID)
		case http.MethodDelete:
			a.handleWorkspaceMediaDelete(w, r, workspaceID, mediaID)
		default:
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}

	http.NotFound(w, r)
}

// openLibraryForWorkspace opens the media library for the given workspace.
// Returns nil, false on error after writing the HTTP response.
func (a *restAPI) openLibraryForWorkspace(w http.ResponseWriter, workspaceID string) (*library.Library, bool) {
	lib, err := library.New(a.homePath, workspaceID)
	if err != nil {
		slog.Error("rest: workspace media: open library", "workspace_id", workspaceID, "error", err)
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return nil, false
	}
	return lib, true
}

func (a *restAPI) handleWorkspaceMediaList(w http.ResponseWriter, r *http.Request, workspaceID string) {
	lib, ok := a.openLibraryForWorkspace(w, workspaceID)
	if !ok {
		return
	}
	entries := lib.List()
	if entries == nil {
		entries = []gen.MediaLibraryEntry{}
	}
	jsonOK(w, entries)
}

func (a *restAPI) handleWorkspaceMediaGet(w http.ResponseWriter, r *http.Request, workspaceID, mediaID string) {
	lib, ok := a.openLibraryForWorkspace(w, workspaceID)
	if !ok {
		return
	}
	entry, err := lib.Get(mediaID)
	if err != nil {
		if errors.Is(err, library.ErrNotFound) || errors.Is(err, library.ErrInvalidMediaID) {
			jsonErr(w, http.StatusNotFound, "media entry not found")
			return
		}
		slog.Error("rest: workspace media: get", "workspace_id", workspaceID, "media_id", mediaID, "error", err)
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return
	}
	jsonOK(w, entry)
}

func (a *restAPI) handleWorkspaceMediaAttach(w http.ResponseWriter, r *http.Request, workspaceID string) {
	var req gen.MediaAttachmentRequest
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "MediaAttachmentRequest", &req, validateEnabled) {
		return
	}
	mediaID := req.MediaId.String()

	lib, ok := a.openLibraryForWorkspace(w, workspaceID)
	if !ok {
		return
	}
	// Verify the entry exists and increment its refcount.
	_, err := lib.Get(mediaID)
	if err != nil {
		if errors.Is(err, library.ErrNotFound) || errors.Is(err, library.ErrInvalidMediaID) {
			jsonErr(w, http.StatusNotFound, "media entry not found")
			return
		}
		slog.Error("rest: workspace media: attach get", "workspace_id", workspaceID, "media_id", mediaID, "error", err)
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if _, incErr := lib.IncrementRefcount(mediaID); incErr != nil {
		slog.Error("rest: workspace media: attach increment",
			"workspace_id", workspaceID, "media_id", mediaID, "error", incErr)
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return
	}
	// Re-read to return the updated entry.
	entry, getErr := lib.Get(mediaID)
	if getErr != nil {
		slog.Error("rest: workspace media: attach re-read",
			"workspace_id", workspaceID, "media_id", mediaID, "error", getErr)
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return
	}
	jsonOK(w, entry)
}

func (a *restAPI) handleWorkspaceMediaDelete(w http.ResponseWriter, r *http.Request, workspaceID, mediaID string) {
	lib, ok := a.openLibraryForWorkspace(w, workspaceID)
	if !ok {
		return
	}
	entry, err := lib.Delete(mediaID)
	if err != nil {
		if errors.Is(err, library.ErrNotFound) || errors.Is(err, library.ErrInvalidMediaID) {
			jsonErr(w, http.StatusNotFound, "media entry not found")
			return
		}
		slog.Error("rest: workspace media: delete", "workspace_id", workspaceID, "media_id", mediaID, "error", err)
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return
	}

	// FR-033 / EventMediaDelete: emit audit event with bytes_freed, media_id, filename, workspace_id, actor.
	actor := a.callerIdentity(r).Username
	bytesFreed := int64(0)
	if entry.Size != nil {
		bytesFreed = *entry.Size
	}
	filename := entry.Filename
	if a.auditor != nil {
		if auditErr := a.auditor.Log(&audit.Entry{
			Event:    audit.EventMediaDelete,
			Decision: audit.DecisionAllow,
			Details: map[string]any{
				"actor":        actor,
				"workspace_id": workspaceID,
				"media_id":     mediaID,
				"filename":     filename,
				"bytes_freed":  bytesFreed,
			},
		}); auditErr != nil {
			slog.Warn("rest: workspace media: audit write failed",
				"event", audit.EventMediaDelete, "workspace_id", workspaceID, "media_id", mediaID, "error", auditErr)
		}
	}
	slog.Info("rest: workspace media: deleted",
		"workspace_id", workspaceID, "media_id", mediaID,
		"filename", filename, "bytes_freed", bytesFreed, "actor", actor,
	)

	// Return the deleted entry as confirmation.
	jsonOK(w, entry)
}

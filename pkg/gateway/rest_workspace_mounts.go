// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// REST lifecycle for workspace mounts (spec
// docs/internal/specs/unified-file-access-and-mounts-spec.md FR-7.1/FR-7.2/
// FR-7.3, FR-8.1; ADR-063 D4/D6). The actual mount logic — name validation,
// realpath resolution, the $OMNIPUS_HOME refusal/warn classification, the
// symlink materialization, and the workspace record read-modify-write
// (including its own per-workspace locking) — all lives in
// pkg/workspace/mount.go (workspace.CreateMount/DeleteMount). This file is
// purely the wire-shape adapter: decode the request, call the package
// function, and map its sentinel errors onto HTTP status codes.

package gateway

import (
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strings"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// HandleWorkspaceMounts dispatches /api/v1/workspaces/{id}/mounts[/{name}]
// requests. Called from HandleWorkspaces once it has recognised the second
// path segment as "mounts" (mirrors the /media dispatch precedent in
// rest_workspace_media.go).
func (a *restAPI) HandleWorkspaceMounts(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSuffix(r.URL.Path, "/")
	rest := strings.TrimPrefix(path, "/api/v1/workspaces")

	// rest is /{id}/mounts[/{name}]
	parts := strings.SplitN(strings.TrimPrefix(rest, "/"), "/", 4)
	if len(parts) < 2 || parts[1] != "mounts" {
		http.NotFound(w, r)
		return
	}
	id := parts[0]
	if err := validateEntityID(id); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid workspace ID")
		return
	}

	switch len(parts) {
	case 2:
		// POST /workspaces/{id}/mounts
		if r.Method != http.MethodPost {
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		a.handleWorkspaceMountCreate(w, r, id)
	case 3:
		// DELETE /workspaces/{id}/mounts/{name}
		if r.Method != http.MethodDelete {
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		a.handleWorkspaceMountDelete(w, r, id, parts[2])
	default:
		http.NotFound(w, r)
	}
}

// mountToCreateResponse builds the POST response body from the just-created
// Mount plus the warning workspace.CreateMount returned (FR-7.4/FR-7.6 — the
// warning must cross the wire, not just land in a server log). Status is
// always "ok" here: CreateMount only ever returns success after the symlink
// and the workspace record both exist, so the target resolves by
// construction at this instant — mirrors WorkspaceMountCreateResponse.yaml's
// "always ok immediately after a successful create" note.
func mountToCreateResponse(m workspace.Mount, warning string) gen.WorkspaceMountCreateResponse {
	resp := gen.WorkspaceMountCreateResponse{
		Name:     m.Name,
		HostPath: m.HostPath,
		Status:   gen.Ok,
	}
	if warning != "" {
		resp.Warning = &warning
	}
	return resp
}

// handleWorkspaceMountCreate implements POST /workspaces/{id}/mounts.
//
// Status-code mapping (deliberate, not incidental — see the doc comments on
// each workspace.Err* sentinel in pkg/workspace/mount.go):
//   - 400: invalid name shape (ErrInvalidMountName), a name collision
//     (ErrMountNameCollision), or host_path not resolving to an existing
//     real directory (ErrMountTargetInvalid) — all malformed-input classes.
//   - 403: the resolved target IS or lies inside $OMNIPUS_HOME
//     (ErrMountRefused) — a policy refusal, not malformed input.
//   - 404: id does not name a known workspace.
//   - 500: anything else (I/O failure, symlink creation failure, etc).
func (a *restAPI) handleWorkspaceMountCreate(w http.ResponseWriter, r *http.Request, id string) {
	var req gen.WorkspaceMountCreateRequest
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "WorkspaceMountCreateRequest", &req, validateEnabled) {
		return
	}

	m, warning, err := workspace.CreateMount(a.homePath, id, req.Name, req.HostPath)
	if err != nil {
		a.writeMountCreateError(w, id, req.Name, err)
		return
	}

	if a.auditor != nil {
		if aErr := a.auditor.Log(&audit.Entry{
			Event:    "workspace.mount.create",
			Decision: audit.DecisionAllow,
			Details: map[string]any{
				"workspace_id": id, "name": m.Name, "host_path": m.HostPath,
				"warning": warning,
			},
		}); aErr != nil {
			slog.Warn("audit write failed", "event", "workspace.mount.create", "id", id, "error", aErr)
		}
	}

	jsonCreated(w, mountToCreateResponse(m, warning))
}

// writeMountCreateError maps a workspace.CreateMount error to the correct
// HTTP status and writes the response. Extracted from
// handleWorkspaceMountCreate so the classification is unit-testable in
// isolation from HTTP plumbing if ever needed, and so the ordering of these
// checks (most-specific sentinel first) is easy to audit in one place.
//
// workspace.loadWorkspaceRecord (the unexported helper CreateMount uses to
// load the workspace before doing anything else) wraps the bare os.ErrNotExist
// sentinel directly (not this package's own errWorkspaceNotFound, which is a
// distinct sentinel used only by readWorkspaceFile/writeWorkspaceFile) — so an
// unknown workspace ID is detected here via errors.Is(err, os.ErrNotExist).
func (a *restAPI) writeMountCreateError(w http.ResponseWriter, id, name string, err error) {
	switch {
	case errors.Is(err, workspace.ErrMountRefused):
		// FR-7.5: the resolved target IS or lies inside $OMNIPUS_HOME. A
		// policy refusal, not malformed input — 403, not 400.
		jsonErr(w, http.StatusForbidden, err.Error())
	case errors.Is(err, workspace.ErrInvalidMountName),
		errors.Is(err, workspace.ErrMountNameCollision),
		errors.Is(err, workspace.ErrMountTargetInvalid):
		jsonErr(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, os.ErrNotExist):
		jsonErr(w, http.StatusNotFound, "workspace not found")
	default:
		slog.Error("rest: create workspace mount", "error", err, "workspace_id", id, "name", name)
		jsonErr(w, http.StatusInternalServerError, "internal server error")
	}
}

// handleWorkspaceMountDelete implements DELETE /workspaces/{id}/mounts/{name}.
//
// Status-code mapping:
//   - 404: id does not name a known workspace, OR name does not name any
//     mount on it (workspace.ErrMountNotFound) — both collapse to 404
//     because from the caller's perspective there is nothing to distinguish:
//     either way, the mount they asked to delete does not exist.
//   - 500: anything else (I/O failure, on-disk tampering guard tripped,
//     etc — see DeleteMount's own doc comment for the tampering case).
func (a *restAPI) handleWorkspaceMountDelete(w http.ResponseWriter, _ *http.Request, id, name string) {
	err := workspace.DeleteMount(a.homePath, id, name)
	if err != nil {
		switch {
		case errors.Is(err, workspace.ErrMountNotFound):
			jsonErr(w, http.StatusNotFound, "mount not found")
		case errors.Is(err, os.ErrNotExist):
			jsonErr(w, http.StatusNotFound, "workspace not found")
		default:
			slog.Error("rest: delete workspace mount", "error", err, "workspace_id", id, "name", name)
			jsonErr(w, http.StatusInternalServerError, "internal server error")
		}
		return
	}

	// ADR-067 FR-003d — a revoked mount must not keep serving bytes through a
	// preview token minted while it was attached.
	//
	// Workspace-wide, because a grant records the workspace and the work-tree-
	// relative scope root and NOT which mount backs it, so the store cannot be
	// asked "was this path inside the mount that just went away". The cost of
	// over-revoking is a re-mint the SPA does transparently; the cost of
	// under-revoking is an unauthenticated read grant over a folder the
	// operator believes they just detached.
	a.revokePreviewTokensForWorkspace(id)

	if a.auditor != nil {
		if aErr := a.auditor.Log(&audit.Entry{
			Event:    "workspace.mount.delete",
			Decision: audit.DecisionAllow,
			Details:  map[string]any{"workspace_id": id, "name": name},
		}); aErr != nil {
			slog.Warn("audit write failed", "event", "workspace.mount.delete", "id", id, "error", aErr)
		}
	}

	w.WriteHeader(http.StatusNoContent)
}

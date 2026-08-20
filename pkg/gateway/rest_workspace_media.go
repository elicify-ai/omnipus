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
	"github.com/elicify-ai/omnipus/pkg/logger"
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

// openLibraryForWorkspace returns the shared workspace media library for the
// given workspace (same instance the agent loop + media-store resolver use).
// Returns nil, false on error after writing the HTTP response.
func (a *restAPI) openLibraryForWorkspace(w http.ResponseWriter, workspaceID string) (*library.Library, bool) {
	// Prefer the AgentLoop cache so REST list/get/delete and chat resolve
	// share one in-memory manifest (upload already goes through
	// GetWorkspaceLibrary). Falling back to library.New only if the loop
	// seam is unavailable keeps boot-order edge cases from 500ing.
	if a.agentLoop != nil {
		if lib := a.agentLoop.GetWorkspaceLibrary(workspaceID); lib != nil {
			return lib, true
		}
	}
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
		if errors.Is(err, library.ErrEntryStranded) {
			// See handleWorkspaceMediaDelete's identical branch for the
			// full HTTP-mapping rationale: this is a server-side manifest/
			// disk inconsistency, not a routine absent id, so it must not
			// collapse into the same 404 an ordinary ErrNotFound gets.
			logger.ErrorCF("rest", "workspace media: get: entry stranded (manifest/disk diverged)",
				map[string]any{"workspace_id": workspaceID, "media_id": mediaID, "error": err.Error()})
			jsonErr(w, http.StatusInternalServerError, "media entry is in an inconsistent state")
			return
		}
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
		if errors.Is(err, library.ErrEntryStranded) {
			logger.ErrorCF("rest", "workspace media: attach: entry stranded (manifest/disk diverged)",
				map[string]any{"workspace_id": workspaceID, "media_id": mediaID, "error": err.Error()})
			jsonErr(w, http.StatusInternalServerError, "media entry is in an inconsistent state")
			return
		}
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

// mediaDeleteOutcome enumerates the four outcomes
// library.Library.Delete's (entry, error) return can represent, per its
// own doc comment — see classifyMediaDeleteOutcome.
type mediaDeleteOutcome int

const (
	// mediaDeleteOutcomeSuccess: err == nil, entry is the deleted entry.
	mediaDeleteOutcomeSuccess mediaDeleteOutcome = iota
	// mediaDeleteOutcomeNotFound: ErrNotFound / ErrInvalidMediaID — nothing
	// was deleted.
	mediaDeleteOutcomeNotFound
	// mediaDeleteOutcomeStraggler (re-review FIX 3): the manifest entry was
	// committed-removed (entry.Id != nil, the real previousEntry.projection()
	// — every other error path returns the zero-value gen.MediaLibraryEntry{}),
	// but the final on-disk unlink of the already-quarantined file failed.
	// From the client's perspective the item IS gone (a follow-up GET
	// 404s); per Delete's own doc, this must be reported as a degraded
	// success, never a 500, and its audit event must not claim bytes_freed
	// for bytes that are not actually free yet.
	mediaDeleteOutcomeStraggler
	// mediaDeleteOutcomeStranded: library.ErrEntryStranded — the id is
	// already known to be in a manifest/disk-diverged state (a PRIOR
	// compound rollback failure left it that way; see library.go's
	// ErrEntryStranded doc) or a compound rollback-of-rollback failure
	// happened during THIS very call. This is a server-side inconsistency,
	// distinct from both mediaDeleteOutcomeNotFound (never a manifest/disk
	// mismatch, just a routine absent id) and mediaDeleteOutcomeStraggler
	// (a benign disk-cleanup lag with the manifest correctly already
	// reflecting the delete) — nothing was deleted here, and the manifest
	// itself cannot be trusted for this id until OrphanGC's straggler
	// reconciliation runs.
	mediaDeleteOutcomeStranded
	// mediaDeleteOutcomeHardFailure: nothing was deleted; a genuine failure.
	mediaDeleteOutcomeHardFailure
)

// classifyMediaDeleteOutcome maps a library.Library.Delete result to a
// mediaDeleteOutcome. It is a pure function (no I/O) taking only Delete's
// own return values, specifically so the discriminator is unit-testable
// with synthetic gen.MediaLibraryEntry values — forcing a REAL
// *library.Library into the "committed, final-unlink straggler" state
// requires library.go's package-private removeFile injection seam
// (library.WithFileRemover), which neither of this handler's
// library-acquisition paths (agentLoop.GetWorkspaceLibrary, or the
// library.New(home, id) fallback in openLibraryForWorkspace) expose — see
// rest_workspace_media_delete_test.go for both this function's unit
// coverage and a separate integration test proving library.go's real
// Delete does return exactly this shape under a genuine final-unlink
// failure.
func classifyMediaDeleteOutcome(entry gen.MediaLibraryEntry, err error) mediaDeleteOutcome {
	if err == nil {
		return mediaDeleteOutcomeSuccess
	}
	if errors.Is(err, library.ErrEntryStranded) {
		return mediaDeleteOutcomeStranded
	}
	if errors.Is(err, library.ErrNotFound) || errors.Is(err, library.ErrInvalidMediaID) {
		return mediaDeleteOutcomeNotFound
	}
	if entry.Id != nil {
		return mediaDeleteOutcomeStraggler
	}
	return mediaDeleteOutcomeHardFailure
}

// logMediaDeleteAudit emits the FR-033 media.delete audit event (re-review
// FIX 4: previously only emitted on a clean success, leaving a failed
// delete with NO audit trail — the same repudiation gap FR-009 closed for
// the cascade-delete case). decision/claimBytesFreed are chosen by the
// caller per outcome: a straggler must not claim bytes_freed for bytes
// that are not actually free yet (library.Library.Delete's own doc), so it
// passes DecisionError + claimBytesFreed=false despite being a
// client-visible success. Audit write failures are logged, never surfaced
// to the HTTP caller — an audit gap must not block an otherwise-successful
// delete response.
func (a *restAPI) logMediaDeleteAudit(
	workspaceID, mediaID, actor string,
	entry gen.MediaLibraryEntry,
	decision string,
	claimBytesFreed bool,
	opErr error,
) {
	if a.auditor == nil {
		return
	}
	bytesFreed := int64(0)
	if claimBytesFreed && entry.Size != nil {
		bytesFreed = *entry.Size
	}
	details := map[string]any{
		"actor":        actor,
		"workspace_id": workspaceID,
		"media_id":     mediaID,
		"filename":     entry.Filename,
		"bytes_freed":  bytesFreed,
	}
	if opErr != nil {
		details["error"] = opErr.Error()
	}
	if auditErr := a.auditor.Log(&audit.Entry{
		Event:    audit.EventMediaDelete,
		Decision: decision,
		Details:  details,
	}); auditErr != nil {
		logger.WarnCF("rest", "workspace media: audit write failed", map[string]any{
			"event": string(
				audit.EventMediaDelete,
			), "workspace_id": workspaceID, "media_id": mediaID, "error": auditErr.Error(),
		})
	}
}

func (a *restAPI) handleWorkspaceMediaDelete(w http.ResponseWriter, r *http.Request, workspaceID, mediaID string) {
	lib, ok := a.openLibraryForWorkspace(w, workspaceID)
	if !ok {
		return
	}
	actor := a.callerIdentity(r).Username
	entry, err := lib.Delete(mediaID)

	switch classifyMediaDeleteOutcome(entry, err) {
	case mediaDeleteOutcomeNotFound:
		jsonErr(w, http.StatusNotFound, "media entry not found")
		return

	case mediaDeleteOutcomeStranded:
		// See handleWorkspaceMediaGet's identical branch: a 404 here would
		// be a lie (this is not a routine absent id — the manifest and
		// disk have diverged for a reason internal to this server), and a
		// bare "internal server error" would bury the distinction the rest
		// of this fix exists to surface. 500 is still the right status
		// family (server-side inconsistency, not a client error), just
		// with an attributable message and its own log line.
		logger.ErrorCF("rest", "workspace media: delete: entry stranded (manifest/disk diverged)",
			map[string]any{"workspace_id": workspaceID, "media_id": mediaID, "error": err.Error()})
		a.logMediaDeleteAudit(workspaceID, mediaID, actor, entry, audit.DecisionError, false, err)
		jsonErr(w, http.StatusInternalServerError, "media entry is in an inconsistent state")
		return

	case mediaDeleteOutcomeHardFailure:
		logger.ErrorCF("rest", "workspace media: delete",
			map[string]any{"workspace_id": workspaceID, "media_id": mediaID, "error": err.Error()})
		a.logMediaDeleteAudit(workspaceID, mediaID, actor, entry, audit.DecisionError, false, err)
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return

	case mediaDeleteOutcomeStraggler:
		// Re-review FIX 3: library.Delete's final-unlink step can fail AFTER
		// the manifest entry is already committed-removed. Reporting 500
		// here (the prior blanket behavior for any non-404 error) would be
		// wrong — the delete fully succeeded from the manifest's and the
		// client's point of view — but the disk-cleanup anomaly (a
		// quarantined raw file leaked permanently; unlike the cascade-delete
		// case, nothing later cleans it up) is still worth an operator-
		// visible signal.
		logger.WarnCF(
			"rest",
			"workspace media: delete succeeded but disk cleanup incomplete (quarantined file leaked)",
			map[string]any{
				"workspace_id": workspaceID,
				"media_id":     mediaID,
				"filename":     entry.Filename,
				"error":        err.Error(),
			},
		)
		a.logMediaDeleteAudit(workspaceID, mediaID, actor, entry, audit.DecisionError, false, err)
		jsonOK(w, entry)
		return

	case mediaDeleteOutcomeSuccess:
		a.logMediaDeleteAudit(workspaceID, mediaID, actor, entry, audit.DecisionAllow, true, nil)
		bytesFreed := int64(0)
		if entry.Size != nil {
			bytesFreed = *entry.Size
		}
		logger.InfoCF("rest", "workspace media: deleted", map[string]any{
			"workspace_id": workspaceID, "media_id": mediaID,
			"filename": entry.Filename, "bytes_freed": bytesFreed, "actor": actor,
		})
		jsonOK(w, entry)
	}
}

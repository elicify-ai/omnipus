// Omnipus — REST Retry of a private, verified view-membership receipt.
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package gateway

import (
	"errors"
	"net/http"
	"strings"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/knowledge"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

func (a *restAPI) handleLibraryRetryMove(w http.ResponseWriter, r *http.Request, workspaceID string) {
	if !workspace.Exists(a.homePath, workspaceID) {
		jsonErr(w, http.StatusNotFound, "workspace not found")
		return
	}
	var req gen.RetryMoveRequest
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "RetryMoveRequest", &req, validateEnabled) {
		return
	}
	id := strings.TrimSpace(req.PendingMoveId)
	if id == "" {
		jsonErr(w, http.StatusBadRequest, "pending_move_id is required")
		return
	}
	collectionRoot, relPaths, err := knowledge.FindViewMoveCollection(a.homePath, workspaceID, id)
	if err != nil {
		mapLibraryRetryMoveErr(w, err, workspaceID, id, nil)
		return
	}
	paths, err := knowledge.MapViewRetryLibraryPaths(a.homePath, workspaceID, collectionRoot, relPaths)
	if err != nil {
		mapLibraryRetryMoveErr(w, err, workspaceID, id, nil)
		return
	}
	// Mounts can be revoked between private-record discovery and path mapping.
	// A fresh grant check is required before any mutation.
	if !knowledge.ResolveScope(a.homePath, workspaceID).Contains(collectionRoot) {
		mapLibraryRetryMoveErr(w, knowledge.ErrViewMoveRetryNotFound, workspaceID, id, nil)
		return
	}
	collection, err := knowledge.NewCollectionRoot(knowledge.OSLinkFS(), collectionRoot)
	if err != nil {
		mapLibraryRetryMoveErr(w, err, workspaceID, id, paths)
		return
	}
	lockDir, err := knowledge.LockDirFor(a.homePath, collectionRoot)
	if err != nil {
		mapLibraryRetryMoveErr(w, err, workspaceID, id, paths)
		return
	}
	renamer := &knowledge.Renamer{
		FS: knowledge.OSLinkFS(), Root: collection,
		Lock: knowledge.NoteLockConfig{CollectionRoot: collectionRoot, LockDir: lockDir},
		Audit: func(ev knowledge.RenameAuditEvent) {
			a.logLibraryAudit(r, "library.retry_move.knowledge", workspaceID, map[string]any{
				"pending_move_id": id, "outcome": ev.Outcome, "reason": ev.Reason,
				"journal_id": ev.JournalID, "paths": ev.Paths,
			})
		},
	}
	res, err := knowledge.RetryViewMembershipMove(a.homePath, renamer, id)
	if err != nil {
		mapLibraryRetryMoveErr(w, err, workspaceID, id, paths)
		return
	}
	if res == nil {
		mapLibraryRetryMoveErr(w, errors.New("Retry returned no result"), workspaceID, id, paths)
		return
	}
	outcome := gen.RetryMoveResultOutcomeReEnrolled
	if res.NoOp {
		outcome = gen.RetryMoveResultOutcomeAlreadyComplete
		paths = []string{}
	}
	a.logLibraryAudit(r, "library.retry_move", workspaceID, map[string]any{
		"pending_move_id": id, "outcome": outcome, "paths": paths,
	})
	writeJSON(w, http.StatusOK, gen.RetryMoveResult{Outcome: outcome, ReEnrolledPaths: paths})
}

func mapLibraryRetryMoveErr(w http.ResponseWriter, err error, workspaceID, id string, paths []string) {
	var timeout *knowledge.LockTimeoutError
	var nested *knowledge.ViewMoveRetryTrackedNestedKBError
	var incomplete *knowledge.ViewMembershipMoveIncompleteError
	body := gen.RetryMoveError{PendingMoveId: id}
	status := http.StatusConflict
	switch {
	case errors.Is(err, knowledge.ErrViewMoveRetryNotFound):
		status, body.Code, body.Error = http.StatusNotFound, gen.RetryMoveErrorCodeRetryNotFound, "pending move not found"
	case errors.Is(err, knowledge.ErrViewMoveRetryExpired):
		status, body.Code, body.Error = http.StatusGone, gen.RetryMoveErrorCodeRetryExpired, "retry receipt expired"
	case errors.As(err, &timeout):
		status, body.Code, body.Error = http.StatusServiceUnavailable, gen.RetryMoveErrorCodeRetryLocked, "collection is locked; retry later"
	case errors.As(err, &nested) && len(nested.Paths) > 0:
		body.Code = gen.RetryMoveErrorCodeRetryPreflightFailed
		body.Error = "move cannot be replayed safely: " + strings.Join(nested.Paths, ", ") + " is a nested knowledge base with tracked views"
		body.Paths = &nested.Paths
	case errors.Is(err, knowledge.ErrViewMoveRetryPreflight):
		body.Code, body.Error = gen.RetryMoveErrorCodeRetryPreflightFailed, "move cannot be replayed safely; the paths may have changed"
	case errors.Is(err, knowledge.ErrViewMoveIdentityMismatch):
		body.Code, body.Error = gen.RetryMoveErrorCodeRetryIdentityMismatch, "the revoked view identity no longer matches; authority could not be confirmed"
	case errors.As(err, &incomplete):
		body.Code, body.Error = gen.RetryMoveErrorCodeRetryPreflightFailed, "Retry remains incomplete; authority could not be confirmed"
	default:
		logger.ErrorCF("rest", "library: retry view membership failed",
			map[string]any{"workspace_id": workspaceID, "pending_move_id": id, "error": err.Error()})
		jsonErr(w, http.StatusInternalServerError, "Retry failed; authority could not be confirmed")
		return
	}
	if status == http.StatusConflict && body.Paths == nil && len(paths) != 0 {
		body.Paths = &paths
	}
	writeJSON(w, status, body)
}

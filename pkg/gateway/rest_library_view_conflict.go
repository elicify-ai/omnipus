// Omnipus — generated, discriminated Library move and trash conflicts.
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package gateway

import (
	"errors"
	"net/http"
	"path"
	"sort"
	"strconv"
	"strings"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/knowledge"
	"github.com/elicify-ai/omnipus/pkg/library"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

// conflictPaths prefixes collection-relative paths for a workspace-scoped
// Library response. Move errors from plain Library transfers already carry
// workspace-relative paths and pass an empty prefix.
func conflictPaths(collectionRel string, rels []string) []string {
	paths := make([]string, 0, len(rels))
	for _, rel := range rels {
		paths = append(paths, path.Join(collectionRel, rel))
	}
	sort.Strings(paths)
	return paths
}

// mapLibraryMoveConflict writes only the approved typed 409 cases. Callers
// retain their existing mapper for all other statuses and error causes.
func mapLibraryMoveConflict(w http.ResponseWriter, err error, collectionRel string) bool {
	var tracked *knowledge.TrackedViewTransferError
	var incomplete *knowledge.ViewMembershipMoveIncompleteError
	switch {
	case errors.As(err, &tracked):
		paths := conflictPaths(collectionRel, tracked.Paths)
		message := "moving tracked derived views or a .base is refused; tracked paths: " + strings.Join(paths, ", ")
		if len(tracked.PendingMoveIDs) > 0 {
			message += "; pending move IDs: " + strings.Join(tracked.PendingMoveIDs, ", ")
		}
		writeJSON(w, http.StatusConflict, gen.LibraryMoveConflictError{
			Code: gen.LibraryMoveConflictErrorCodeViewTrackedTransferRefused, Error: message,
			TrackedPaths: &paths,
		})
	case errors.As(err, &incomplete):
		paths := conflictPaths(collectionRel, incomplete.Paths)
		if incomplete.RetryID == "" || len(paths) == 0 {
			logger.ErrorCF("rest", "library: incomplete move lacks a trusted retry receipt",
				map[string]any{"error": err.Error()})
			jsonErr(w, http.StatusInternalServerError, "view move incomplete; contact the operator")
			return true
		}
		visible := paths
		message := "view move incomplete after authority was revoked; retry using pending_move_id"
		if len(paths) > 50 {
			visible = paths[:50]
			message += "; showing 50 of " + strconv.Itoa(len(paths)) + " affected paths"
			logger.WarnCF("rest", "library: incomplete move affects more paths than the response lists",
				map[string]any{"pending_move_id": incomplete.RetryID, "paths": paths})
		}
		writeJSON(w, http.StatusConflict, gen.LibraryMoveConflictError{
			Code: gen.LibraryMoveConflictErrorCodeMoveIncomplete, Error: message,
			Paths: &visible, PendingMoveId: &incomplete.RetryID,
		})
	case errors.Is(err, library.ErrAlreadyExists),
		errors.Is(err, knowledge.ErrRenameDestinationExists),
		errors.Is(err, knowledge.ErrNoteExists):
		writeJSON(w, http.StatusConflict, gen.LibraryMoveConflictError{
			Code:  gen.LibraryMoveConflictErrorCodeAlreadyExists,
			Error: "an entry already exists at the destination path",
		})
	case errors.Is(err, library.ErrIsMountRoot):
		writeJSON(w, http.StatusConflict, gen.LibraryMoveConflictError{
			Code:  gen.LibraryMoveConflictErrorCodeIsMountRoot,
			Error: "that is a mounted folder — remove the mount instead of moving it",
		})
	default:
		return false
	}
	return true
}

// mapLibraryTrashIncomplete reports the post-revocation failure without
// exposing filesystem errors or inventing a rename Retry for a trash.
func mapLibraryTrashIncomplete(w http.ResponseWriter, err error, collectionRel string) bool {
	var incomplete *knowledge.ViewMembershipTrashIncompleteError
	if !errors.As(err, &incomplete) {
		return false
	}
	paths := conflictPaths(collectionRel, incomplete.Paths)
	if len(paths) == 0 {
		logger.ErrorCF("rest", "library: incomplete trash lacks released paths",
			map[string]any{"error": err.Error()})
		jsonErr(w, http.StatusInternalServerError, "trash incomplete; contact the operator")
		return true
	}
	writeJSON(w, http.StatusConflict, gen.LibraryMoveConflictError{
		Code:  gen.LibraryMoveConflictErrorCodeTrashIncomplete,
		Error: "trash incomplete after view authority was released; trash the same path again",
		Paths: &paths,
	})
	return true
}

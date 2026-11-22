// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// rest_packages_cancel.go: DELETE /api/v1/packages/import/{staging_id}, the
// B1 cancel of an unused upload (epic #1094 slice B1; spec
// docs/internal/specs/export-import-b1-spec.md "Cancel lifecycle", FR-010).
// Not audited: no data enters or leaves.

package gateway

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/elicify-ai/omnipus/pkg/omnpkg"
)

// handlePackageImportCancel cancels a ready upload through the registry's
// one-step Cancel: 204 when the folder is gone; 500 staging_cleanup_failed
// when the upload is cancelled but its folder stays for a later removal;
// 409 staging_busy for an upload being received, applied or kept for
// clean-up; 404 staging_not_found for a done, unknown or malformed ID. Only
// /import/{id} itself is a cancel target: DELETE on any deeper path (such as
// /import/{id}/apply) is 405.
func (a *restAPI) handlePackageImportCancel(w http.ResponseWriter, r *http.Request) {
	rest, _ := strings.CutPrefix(r.URL.Path, packageApplyPrefix)
	if strings.Contains(rest, "/") {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if _, err := uuid.Parse(rest); err != nil || strings.ContainsAny(rest, `\.`) {
		writePackageError(w, packageErr(omnpkg.CodeStagingNotFound), nil)
		return
	}
	err := a.packageRegistry().Cancel(rest)
	if err == nil || omnpkg.CodeOf(err) == omnpkg.CodeStagingCleanupFailed {
		// The upload can no longer be applied either way: drop what its
		// preview showed.
		a.forgetPreview(rest)
	}
	if err == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if omnpkg.CodeOf(err) == omnpkg.CodeStagingCleanupFailed {
		slog.Warn("packages: upload cancelled but its folder could not be removed; the next upload retries",
			"staging_id", rest, "error", err)
	}
	writePackageErr(w, "cancel", err)
}

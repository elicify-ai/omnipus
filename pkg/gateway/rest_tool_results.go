// NOTE: this tag applies to every file in pkg/gateway — it is a package-wide
// constraint enforcing CGO_ENABLED=0 for the single-binary open-source build.

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
)

// HandleToolResults handles GET /api/v1/sessions/{session_id}/tool-results/{ref}.
// Auth: withAuth (applied at route registration). Response: 200 OK with raw JSON,
// 400 invalid input, 404 not found, 500 on unexpected I/O error.
func (a *restAPI) HandleToolResults(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Parse /api/v1/sessions/{session_id}/tool-results/{ref}.
	path := r.URL.Path
	const prefix = "/api/v1/sessions/"
	if !strings.HasPrefix(path, prefix) {
		jsonErr(w, http.StatusBadRequest, "invalid path")
		return
	}
	rest := strings.TrimPrefix(path, prefix)
	// rest is now "{session_id}/tool-results/{ref}"
	const toolResultsInfix = "/tool-results/"
	infixIdx := strings.Index(rest, toolResultsInfix)
	if infixIdx < 0 {
		jsonErr(w, http.StatusBadRequest, "invalid path")
		return
	}
	sessionID := rest[:infixIdx]
	ref := rest[infixIdx+len(toolResultsInfix):]
	ref = strings.TrimSuffix(ref, "/")

	if sessionID == "" {
		jsonErr(w, http.StatusBadRequest, "session_id is required")
		return
	}
	if err := validateEntityID(sessionID); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid session_id")
		return
	}
	if ref == "" {
		jsonErr(w, http.StatusBadRequest, "ref is required")
		return
	}
	if err := validateEntityID(ref); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid ref")
		return
	}

	store := newToolResultStore(a.homePath)
	data, err := store.readByRef(sessionID, ref)
	if err != nil {
		if errors.Is(err, ErrToolResultNotFound) {
			jsonErr(w, http.StatusNotFound, "tool result not found")
			return
		}
		slog.Error("rest: tool-results: read failed",
			"session_id", sessionID,
			"ref", ref,
			"error", err,
		)
		jsonErr(w, http.StatusInternalServerError, "could not read tool result")
		return
	}

	// Bound response size to replayMaxResultBytes (1 MiB) so a corrupt on-disk
	// file cannot OOM the response writer.
	if len(data) > replayMaxResultBytes {
		data = data[:replayMaxResultBytes]
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if _, werr := w.Write(data); werr != nil {
		slog.Warn("rest: tool-results: write response failed",
			"session_id", sessionID,
			"ref", ref,
			"error", werr,
		)
	}
}

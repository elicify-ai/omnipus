// rest_preview_retry.go: POST /api/v1/sessions/{session_id}/previews/{tool_call_id}/retry
// (office-preview spec §5.2, FR-038; contract operationId retrySessionPreview).

package gateway

import (
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/session"
)

const (
	previewRetryPrefix = "previews/"
	previewRetrySuffix = "/retry"
	previewToolName    = "preview_document"
)

// previewRetryToolCallID returns the tool call id of a
// "previews/{tool_call_id}/retry" sub-path, and whether subPath names the
// retry sub-resource at all.
func previewRetryToolCallID(subPath string) (string, bool) {
	if !strings.HasPrefix(subPath, previewRetryPrefix) || !strings.HasSuffix(subPath, previewRetrySuffix) {
		return "", false
	}
	id := strings.TrimSuffix(strings.TrimPrefix(subPath, previewRetryPrefix), previewRetrySuffix)
	return id, true
}

// handlePreviewRetry serves the retry route. The session is resolved exactly
// as getSession resolves it (a refusal is a 404, never a leak of the
// session's existence); an id that names no preview_document call in that
// session is a 404 "unknown preview".
func (a *restAPI) handlePreviewRetry(w http.ResponseWriter, r *http.Request, sessionID, toolCallID string) {
	if r.Method != http.MethodPost {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if toolCallID == "" || len(toolCallID) > 255 || strings.Contains(toolCallID, "/") {
		jsonErr(w, http.StatusNotFound, "preview not found")
		return
	}
	mainOK, mainErr := a.resolveMainSessionForRead(sessionID, requestUsername(r))
	if mainErr != nil {
		jsonErr(w, http.StatusInternalServerError, mainErr.Error())
		return
	}
	if !mainOK {
		jsonErr(w, http.StatusNotFound, "session not found")
		return
	}
	store := a.resolveSessionStore(sessionID)
	if store == nil {
		jsonErr(w, http.StatusNotFound, "session not found")
		return
	}
	if _, err := store.GetMeta(sessionID); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			jsonErr(w, http.StatusNotFound, "session not found")
			return
		}
		jsonSessionStorageError(w, "read", sessionID, err)
		return
	}
	entries, err := store.ReadTranscript(sessionID)
	if err != nil {
		jsonSessionStorageError(w, "read", sessionID, err)
		return
	}
	if _, found := findPreviewToolCall(entries, toolCallID); !found {
		jsonErr(w, http.StatusNotFound, "preview not found")
		return
	}
	// The re-render of a known preview needs the source authorization the
	// contract names ("the requesting person's access to the session AND the
	// preview's source file"); how a person's access to an agent-read source
	// is decided outside that agent's turn is an open design question
	// (reported to the dispatcher). Until it is decided the route refuses
	// loudly instead of re-rendering under a guessed policy.
	slog.Warn("rest: preview retry refused: source authorization for a person-initiated retry is not implemented",
		"session_id", sessionID, "tool_call_id", toolCallID)
	jsonErrCode(w, http.StatusNotImplemented, "preview retry is not available yet", "preview_retry_not_implemented")
}

// findPreviewToolCall returns the preview_document call with id in the
// transcript (the call whose MediaFrame group carries that tool_call_id).
func findPreviewToolCall(entries []session.TranscriptEntry, id string) (session.ToolCall, bool) {
	for _, entry := range entries {
		for _, call := range entry.ToolCalls {
			if string(call.ID) == id && call.Tool == previewToolName {
				return call, true
			}
		}
	}
	return session.ToolCall{}, false
}

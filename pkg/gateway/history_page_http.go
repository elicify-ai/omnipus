package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

const historyUnavailableCopy = "Session history is unavailable. Retry after the operator restores its index."
const historyStorageCopy = "This session could not be read because session storage failed. Check disk space and permissions, then retry. Details are in the server log."

func historyStateForError(err error) generated.SessionHistoryState {
	if err == nil {
		return generated.SessionHistoryState{Status: "available"}
	}
	reason := "storage_error"
	message := historyStorageCopy
	var unavailable *session.ChatHistoryUnavailableError
	if errors.As(err, &unavailable) {
		reason = unavailable.Reason
	}
	if errors.Is(err, session.ErrHistoryProjection) && !historyStorageFailure(err) {
		reason = "projection_error"
	}
	switch reason {
	case "index_missing", "index_inconsistent", "catchup_limit":
		message = historyUnavailableCopy
	case "projection_error", "storage_error":
	default:
		reason = "storage_error"
	}
	return generated.SessionHistoryState{Status: "unavailable", Reason: &reason, Message: &message}
}
func historyStorageFailure(err error) bool {
	var unavailable *session.ChatHistoryUnavailableError
	return errors.As(err, &unavailable) && unavailable.Reason == "storage_error"
}
func historyHTTPFailure(ctx context.Context, w http.ResponseWriter, id string, err error) {
	// Cleanup failure is primary even when its private cause also retains the
	// read's cancellation or selector error. Keep both causes, but do not let
	// the secondary cause suppress the storage log or select a misleading 404.
	if historyStorageFailure(err) {
		if ctx.Err() != nil {
			// Transport cancellation changes delivery, not storage observability.
			slog.Error("rest: session storage failure", "op", "read", "session_id", id, "error", err)
			return
		}
		jsonSessionStorageError(w, "read", id, err)
		return
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return
	}
	var input *historyQueryError
	switch {
	case errors.As(err, &input):
		jsonErr(w, 400, input.message)
	case errors.Is(err, session.ErrInvalidHistoryPosition):
		jsonErr(w, 400, "invalid history position")
	case errors.Is(err, session.ErrInvalidHistoryPageRange):
		jsonErr(w, 400, "invalid history page range")
	case errors.Is(err, session.ErrHistoryPositionNotFound):
		jsonErr(w, 404, "history position not found")
	case errors.Is(err, session.ErrHistoryEntryExpired):
		jsonErr(w, 410, "history entry expired")
	default:
		state := historyStateForError(err)
		if state.Message != nil && *state.Message == historyUnavailableCopy {
			jsonErr(w, 500, historyUnavailableCopy)
		} else {
			jsonSessionStorageError(w, "read", id, err)
		}
	}
}
func (a *restAPI) historyProjectionDeps(store *session.UnifiedStore, id string) historyPageProjectionDeps {
	deps := historyPageProjectionDeps{ToolStore: newToolResultStore(a.homePath), TerminalAsk: loadTerminalAskRecord(store, id), MediaStore: a.agentLoop.GetMediaStore()}
	if deps.MediaStore == nil {
		deps.MediaStore = a.mediaStore
	}
	if ls := a.agentLoop.GetSessionLifecycleStore(); ls != nil {
		deps.Lifecycle = ls
	}
	return deps
}

// readableSessionStore applies the history page's session access order
// after the registered auth wrapper and the session-id check: main-session
// eligibility, then store resolution and session metadata. It writes the
// refusal and returns nil when the session may not be read.
func (a *restAPI) readableSessionStore(w http.ResponseWriter, id string) *session.UnifiedStore {
	if !a.gateMainRead(w, id) {
		return nil
	}
	store := a.resolveSessionStore(id)
	if store == nil {
		jsonErr(w, 404, "session not found")
		return nil
	}
	if _, err := store.GetMeta(id); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			jsonErr(w, 404, "session not found")
		} else {
			jsonSessionStorageError(w, "read", id, err)
		}
		return nil
	}
	return store
}

// serveSessionHistoryEntry is GET /sessions/{id}/messages/{history_position}
// (operationId getSessionHistoryEntry; chat-history paging spec, Amendment
// 2026-10-27 D2, A2.2). Session refusals precede any position work; the full
// public projection after effects comes from the same projector as a page
// entry, with display shortening off. Errors keep historyHTTPFailure's
// storage-primary precedence.
func (a *restAPI) serveSessionHistoryEntry(w http.ResponseWriter, r *http.Request, id, rawPosition string) {
	store := a.readableSessionStore(w, id)
	if store == nil {
		return
	}
	position, err := session.ParseHistoryPosition(rawPosition)
	if err != nil {
		historyHTTPFailure(r.Context(), w, id, err)
		return
	}
	entry, err := store.ReadChatHistoryEntry(r.Context(), id, position)
	if err != nil {
		historyHTTPFailure(r.Context(), w, id, err)
		return
	}
	// A2.2: the full projection is never a HistoryResultPreview, and an
	// ordinary (unshortened) result must not carry the reserved key. A saved
	// result that does — valid marker shape or not — fails the server-side
	// marker check and becomes the D4 per-item placeholder (#1452 SEC-3).
	if historySavedMarkerPresent(entry.Entry) {
		logsafeWarn("history: saved item replaced by its placeholder", "event", "history_item_placeholder", "reason", "saved_preview_marker")
		entry.Entry = historyPlaceholderSource(entry.Entry)
		entry.NestedDelegateParents = nil
	}
	_, encoded, err := historyWireMessage(id, entry)
	if err != nil {
		historyHTTPFailure(r.Context(), w, id, historyProjectionError(err))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if _, err = w.Write(encoded); err != nil {
		slog.Debug("rest: write response body failed", "error", err)
		return
	}
	if _, err = w.Write([]byte("\n")); err != nil {
		slog.Debug("rest: write newline failed", "error", err)
	}
}

func (a *restAPI) serveSessionHistory(w http.ResponseWriter, r *http.Request, id string) {
	store := a.readableSessionStore(w, id)
	if store == nil {
		return
	}
	attempt := newHistoryPageAttempt("rest_messages")
	defer attempt.finish()
	opts, requested, err := parseChatHistoryQuery(r.URL.RawQuery)
	attempt.requested = requested
	if err != nil {
		attempt.err = err
		attempt.preparationComplete()
		historyHTTPFailure(r.Context(), w, id, err)
		return
	}
	attempt.validated(opts, requested)
	request, err := newHistoryPageRequest(id, opts, a.historyProjectionDeps(store, id))
	if err != nil {
		attempt.err = err
		attempt.preparationComplete()
		historyHTTPFailure(r.Context(), w, id, err)
		return
	}
	prepared, err := request.Read(attempt.context(r.Context()), store)
	attempt.err = err
	if err != nil {
		attempt.preparationComplete()
		historyHTTPFailure(r.Context(), w, id, err)
		return
	}
	attempt.page = &prepared.Page
	attempt.preparationComplete()
	// Reuse exactly the generated Message encodings prepared by the meter.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if _, err = w.Write(prepared.encodedResponse); err != nil {
		slog.Debug("rest: write response body failed", "error", err)
		return
	}
	if _, err = w.Write([]byte("\n")); err != nil {
		slog.Debug("rest: write newline failed", "error", err)
	}
}
func (a *restAPI) writePagedSessionDetail(w http.ResponseWriter, r *http.Request, meta *session.UnifiedMeta, agentRemoved bool) {
	store := a.resolveSessionStore(meta.ID)
	attempt := newHistoryPageAttempt("rest_detail")
	defer attempt.finish()
	opts := session.ChatHistoryPageOptions{Limit: 40, MinRows: 20}
	attempt.validated(opts, nil)
	request, err := newHistoryPageRequest(meta.ID, opts, a.historyProjectionDeps(store, meta.ID))
	var prepared preparedHistoryPage
	if err == nil {
		prepared, err = request.Read(attempt.context(r.Context()), store)
	}
	attempt.err = err
	storageFailure := historyStorageFailure(err)
	if storageFailure {
		slog.Error("rest: session storage failure", "op", "read", "session_id", meta.ID, "error", err)
	}
	if r.Context().Err() != nil || !storageFailure && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
		return
	}
	detail := generated.SessionDetail{HistoryState: historyStateForError(err)}
	metadata := unifiedMetaToGenSession(a.withDisplayTitle(meta))
	metadata.NeedsAttention = mainNeedsAttention(meta, a.withPendingSecretCards(a.pendingApprovalMains()))
	metadata.Protected = computeSessionProtected(meta)
	if failure := attachSessionRuntimeFields(&metadata, a.agentLoop.GetSessionLifecycleStore(), meta.ID, a.agentLoop.CurrentBootEpoch()); failure != nil {
		attempt.err = failure
		attempt.preparationComplete()
		jsonErr(w, 500, "session lifecycle unavailable")
		return
	}
	raw, failure := json.Marshal(metadata)
	if failure == nil {
		failure = json.Unmarshal(raw, &detail.Session)
	}
	if failure != nil {
		attempt.err = failure
		attempt.preparationComplete()
		jsonSessionStorageError(w, "read", meta.ID, failure)
		return
	}
	if agentRemoved {
		v := true
		detail.AgentRemoved = &v
	}
	if err == nil {
		attempt.page = &prepared.Page
		response := prepared.Response
		detail.Messages = &response.Entries
		detail.PageInfo = &response.PageInfo
		detail.HasEarlier = &response.HasEarlier
		detail.HasLater = &response.HasLater
		detail.NextBefore = response.NextBefore
		detail.NextAfter = response.NextAfter
	}
	raw, failure = json.Marshal(detail)
	if failure != nil {
		attempt.err = failure
		attempt.preparationComplete()
		jsonSessionStorageError(w, "read", meta.ID, failure)
		return
	}
	if message, _ := validateBodyAgainstSchema("SessionDetail", raw); message != "" {
		attempt.err = errHistoryPageProtocol
		attempt.preparationComplete()
		jsonSessionStorageError(w, "read", meta.ID, errHistoryPageProtocol)
		return
	}
	attempt.preparationComplete()
	jsonOK(w, detail)
}

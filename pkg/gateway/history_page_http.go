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
	if errors.Is(err, session.ErrHistoryProjection) {
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
func historyHTTPFailure(w http.ResponseWriter, id string, err error) {
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
func (a *restAPI) serveSessionHistory(w http.ResponseWriter, r *http.Request, id string) {
	if !a.gateMainRead(w, id) {
		return
	}
	store := a.resolveSessionStore(id)
	if store == nil {
		jsonErr(w, 404, "session not found")
		return
	}
	if _, err := store.GetMeta(id); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			jsonErr(w, 404, "session not found")
		} else {
			jsonSessionStorageError(w, "read", id, err)
		}
		return
	}
	attempt := newHistoryPageAttempt("rest_messages")
	defer attempt.finish()
	opts, requested, err := parseChatHistoryQuery(r.URL.RawQuery)
	attempt.requested = requested
	if err != nil {
		attempt.err = err
		historyHTTPFailure(w, id, err)
		return
	}
	attempt.validated(opts, requested)
	request, err := newHistoryPageRequest(id, opts, a.historyProjectionDeps(store, id))
	if err != nil {
		attempt.err = err
		historyHTTPFailure(w, id, err)
		return
	}
	prepared, err := request.Read(attempt.context(r.Context()), store)
	attempt.err = err
	if err != nil {
		historyHTTPFailure(w, id, err)
		return
	}
	attempt.page = &prepared.Page
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
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return
	}
	detail := generated.SessionDetail{HistoryState: historyStateForError(err)}
	metadata := unifiedMetaToGenSession(a.withDisplayTitle(meta))
	metadata.NeedsAttention = mainNeedsAttention(meta, a.pendingApprovalMains())
	metadata.Protected = computeSessionProtected(meta)
	if failure := attachSessionRuntimeFields(&metadata, a.agentLoop.GetSessionLifecycleStore(), meta.ID, a.agentLoop.CurrentBootEpoch()); failure != nil {
		attempt.err = failure
		jsonErr(w, 500, "session lifecycle unavailable")
		return
	}
	raw, failure := json.Marshal(metadata)
	if failure == nil {
		failure = json.Unmarshal(raw, &detail.Session)
	}
	if failure != nil {
		attempt.err = failure
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
		jsonSessionStorageError(w, "read", meta.ID, failure)
		return
	}
	if message, _ := validateBodyAgainstSchema("SessionDetail", raw); message != "" {
		attempt.err = errHistoryPageProtocol
		jsonSessionStorageError(w, "read", meta.ID, errHistoryPageProtocol)
		return
	}
	jsonOK(w, detail)
}

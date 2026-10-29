// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// rest_rooms.go: the group-chat REST surface (omnipus-system-agent K23, K24,
// K26; contracts/openapi.yaml tag "Rooms"):
//
//	GET    /api/v1/workspaces/{id}/rooms   listWorkspaceRooms
//	POST   /api/v1/workspaces/{id}/rooms   createRoom
//	GET    /api/v1/rooms/{id}              getRoom
//	DELETE /api/v1/rooms/{id}              deleteRoom
//	PATCH  /api/v1/rooms/{id}/members      updateRoomMembers
//	GET    /api/v1/rooms/{id}/messages     getRoomHistory (#1332)
//	POST   /api/v1/rooms/{id}/messages     postRoomMessage
//
// Every body is a generated type, validated against its inbound schema when
// validation is on, and the semantic rules hold with validation off. A
// storage or read failure is an explicit 500 with the error body - the room
// list is never answered with an empty list it could not read (#1314).

package gateway

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/rooms"
)

// roomService returns the live room service, or writes a 500 when the
// gateway has none.
func (a *restAPI) roomService(w http.ResponseWriter) *rooms.Service {
	if hub := a.rooms.Load(); hub != nil && hub.svc != nil {
		return hub.svc
	}
	jsonErr(w, http.StatusInternalServerError, "group chats are not available on this gateway")
	return nil
}

// writeRoomError writes a room refusal with its own status and text; the
// cause of a server-side failure goes to the log only.
func writeRoomError(w http.ResponseWriter, op string, err error) {
	status := rooms.StatusOf(err)
	if status >= http.StatusInternalServerError {
		slog.Error("rooms: request failed", "op", op, "error", err)
	}
	jsonErr(w, status, err.Error())
}

func requestUsername(r *http.Request) string {
	if user, ok := r.Context().Value(UserContextKey{}).(*config.UserConfig); ok && user != nil {
		return user.Username
	}
	return ""
}

func writeRoomJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// The status is already sent; this line is the only signal that the
		// client got a broken body, so it is logged at a level that is kept.
		slog.Error("rest: write room response failed; the client received an incomplete body", "status", status, "error", err)
	}
}

// handleWorkspaceRooms serves /api/v1/workspaces/{id}/rooms.
func (a *restAPI) handleWorkspaceRooms(w http.ResponseWriter, r *http.Request, workspaceID string) {
	switch r.Method {
	case http.MethodGet:
		svc := a.roomService(w)
		if svc == nil {
			return
		}
		list, err := svc.List(workspaceID)
		if err != nil {
			writeRoomError(w, "list", err)
			return
		}
		writeRoomJSON(w, http.StatusOK, list)
	case http.MethodPost:
		var req generated.RoomCreateRequest
		if !decodeAndValidateStrict(w, r, "RoomCreateRequest", &req, a.agentLoop.GetConfig().Gateway.ValidateInbound) {
			return
		}
		if req.Kind == "" || req.AgentIds == nil {
			jsonErr(w, http.StatusBadRequest, "kind and agent_ids are required")
			return
		}
		svc := a.roomService(w)
		if svc == nil {
			return
		}
		room, err := svc.Create(workspaceID, req, requestUsername(r))
		if err != nil {
			writeRoomError(w, "create", err)
			return
		}
		writeRoomJSON(w, http.StatusCreated, room)
	default:
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// HandleRooms serves /api/v1/rooms/{id}, /members and /messages (GET pages
// history, POST sends).
func (a *restAPI) HandleRooms(w http.ResponseWriter, r *http.Request) {
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/rooms"), "/")
	segs := strings.Split(rest, "/")
	if rest == "" || len(segs) > 2 || segs[0] == "" {
		http.NotFound(w, r)
		return
	}
	roomID := segs[0]
	sub := ""
	if len(segs) == 2 {
		sub = segs[1]
	}
	switch {
	case sub == "" && r.Method == http.MethodGet:
		a.getRoom(w, roomID)
	case sub == "" && r.Method == http.MethodDelete:
		a.deleteRoom(w, r, roomID)
	case sub == "members" && r.Method == http.MethodPatch:
		a.updateRoomMembers(w, r, roomID)
	case sub == "messages" && r.Method == http.MethodPost:
		a.postRoomMessage(w, r, roomID)
	case sub == "messages" && r.Method == http.MethodGet:
		a.getRoomHistory(w, r, roomID)
	case sub == "" || sub == "members" || sub == "messages":
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
	default:
		http.NotFound(w, r)
	}
}

func (a *restAPI) getRoom(w http.ResponseWriter, roomID string) {
	svc := a.roomService(w)
	if svc == nil {
		return
	}
	room, err := svc.Get(roomID)
	if err != nil {
		writeRoomError(w, "get", err)
		return
	}
	writeRoomJSON(w, http.StatusOK, room)
}

// getRoomHistory serves GET /api/v1/rooms/{id}/messages (#1332,
// ROOM-HISTORY-PAGING-1332-20261018): one older page of a room's entries.
// Order (D3): authentication (withAuth, already passed), limit, cursor
// syntax, then the room read, which applies the room-dependent cursor bound.
// The rules hold with inbound validation on and off — a GET has no body.
func (a *restAPI) getRoomHistory(w http.ResponseWriter, r *http.Request, roomID string) {
	query, malformed := parseHistoryQuery(r.URL.RawQuery)
	limit := rooms.HistoryDefaultLimit
	if malformed["limit"] {
		jsonErr(w, http.StatusBadRequest, "limit must be an integer from 1 to 1000")
		return
	}
	if values, present := query["limit"]; present {
		n, ok := parseHistoryInt(values)
		if !ok || n < 1 || n > rooms.HistoryMaxLimit {
			jsonErr(w, http.StatusBadRequest, "limit must be an integer from 1 to 1000")
			return
		}
		limit = int(n)
	}
	var beforeSeq *int64
	if malformed["before_seq"] {
		jsonErr(w, http.StatusBadRequest, "before_seq must be a positive int64 integer")
		return
	}
	if values, present := query["before_seq"]; present {
		n, ok := parseHistoryInt(values)
		if !ok || n < 1 {
			jsonErr(w, http.StatusBadRequest, "before_seq must be a positive int64 integer")
			return
		}
		beforeSeq = &n
	}
	svc := a.roomService(w)
	if svc == nil {
		return
	}
	page, err := svc.History(roomID, beforeSeq, limit)
	if err != nil {
		writeRoomError(w, "history", err)
		return
	}
	writeRoomJSON(w, http.StatusOK, page)
}

// parseHistoryQuery decodes the raw query like url.ParseQuery, but reports a
// malformed pair instead of silently dropping it (url.URL.Query discards it,
// which would turn a broken before_seq into "omitted" and serve the newest
// page — gate round 1, SF-04). malformed is keyed by field: a pair whose
// value cannot be decoded marks its own field; a pair whose KEY cannot be
// decoded cannot be attributed and marks "limit" (validated first, D3). Pairs
// for other keys are ignored, as before.
func parseHistoryQuery(raw string) (url.Values, map[string]bool) {
	values := url.Values{}
	malformed := map[string]bool{}
	for _, pair := range strings.Split(raw, "&") {
		if pair == "" {
			continue
		}
		rawKey, rawValue, _ := strings.Cut(pair, "=")
		key, err := url.QueryUnescape(rawKey)
		if err != nil {
			malformed["limit"] = true
			continue
		}
		value, err := url.QueryUnescape(rawValue)
		if err != nil {
			malformed[key] = true
			continue
		}
		values[key] = append(values[key], value)
	}
	return values, malformed
}

// parseHistoryInt parses one query value as a non-negative decimal int64:
// exactly one occurrence, digits only (no sign, space, point or exponent),
// leading zeroes allowed, overflow refused (ruling D3).
func parseHistoryInt(values []string) (int64, bool) {
	if len(values) != 1 || values[0] == "" {
		return 0, false
	}
	for _, c := range values[0] {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseInt(values[0], 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

func (a *restAPI) deleteRoom(w http.ResponseWriter, r *http.Request, roomID string) {
	svc := a.roomService(w)
	if svc == nil {
		return
	}
	if err := svc.Delete(roomID, requestUsername(r)); err != nil {
		writeRoomError(w, "delete", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *restAPI) updateRoomMembers(w http.ResponseWriter, r *http.Request, roomID string) {
	var req generated.RoomMembersUpdateRequest
	if !decodeAndValidateStrict(w, r, "RoomMembersUpdateRequest", &req, a.agentLoop.GetConfig().Gateway.ValidateInbound) {
		return
	}
	if !req.Action.Valid() || len(req.AgentIds) == 0 {
		jsonErr(w, http.StatusBadRequest, "action must be add, remove, mute or unmute, with at least one agent id")
		return
	}
	svc := a.roomService(w)
	if svc == nil {
		return
	}
	room, err := svc.UpdateMembers(roomID, req, requestUsername(r))
	if err != nil {
		writeRoomError(w, "members", err)
		return
	}
	writeRoomJSON(w, http.StatusOK, room)
}

func (a *restAPI) postRoomMessage(w http.ResponseWriter, r *http.Request, roomID string) {
	// A missing or null required field is a malformed body (400) in both
	// validation modes; an empty text is a room rule refusal (422).
	if !requirePresentNonNull(w, r, "client_message_id", "text", "mentions") {
		return
	}
	svc := a.roomService(w)
	if svc == nil {
		return
	}
	// #1449 A-12, FR-074: the byte limit in force is checked before any
	// schema check, so an over-limit message is 422 naming the limit with
	// inbound validation on and off.
	if !refuseOversizeRoomText(w, r, svc.TextLimit()) {
		return
	}
	var req generated.RoomMessageRequest
	if !decodeAndValidateStrict(w, r, "RoomMessageRequest", &req, a.agentLoop.GetConfig().Gateway.ValidateInbound) {
		return
	}
	if err := svc.PostMessage(roomID, req, requestUsername(r)); err != nil {
		writeRoomError(w, "message", err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// refuseOversizeRoomText answers 422 when the text of the body is longer than
// limit UTF-8 bytes, then restores the body for the decoder. A body whose
// text cannot be read is left to the decoder.
func refuseOversizeRoomText(w http.ResponseWriter, r *http.Request, limit int) bool {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "could not read request body")
		return false
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	var probe struct { // not-wire-format: reads only the text length before validation
		Text *string `json:"text"`
	}
	if json.Unmarshal(raw, &probe) != nil || probe.Text == nil {
		return true
	}
	if err := rooms.CheckTextSize(len(*probe.Text), limit); err != nil {
		writeRoomError(w, "message", err)
		return false
	}
	return true
}

// requirePresentNonNull refuses (400) a JSON object body that lacks one of the
// named fields or sets it to null, then restores the body for the decoder.
// A body that is not a JSON object is left to the decoder's own 400.
func requirePresentNonNull(w http.ResponseWriter, r *http.Request, fields ...string) bool {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "could not read request body")
		return false
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil {
		return true
	}
	for _, f := range fields {
		v, ok := obj[f]
		if !ok || bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			jsonErr(w, http.StatusBadRequest, f+" is required and must not be null")
			return false
		}
	}
	return true
}

// roomAdmissionAwaitLimit bounds how long an approval answer waits for the
// room admission it unblocked.
const roomAdmissionAwaitLimit = 10 * time.Second

// awaitRoomAdmission lets the approval of a group chat's pre-admission card
// (FR-251) return once the request it approved is admitted (or refused by the
// re-read authorization), so the person's next look shows the result.
func (a *restAPI) awaitRoomAdmission(entry *approvalEntry) {
	if entry == nil || entry.RoomRef == nil || !entry.RoomPreAdmission {
		return
	}
	if hub := a.rooms.Load(); hub != nil && hub.svc != nil {
		hub.svc.AwaitAdmission(entry.RoomRef.RequestId, roomAdmissionAwaitLimit)
	}
}

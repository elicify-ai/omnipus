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
		slog.Debug("rest: write room response failed", "error", err)
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

// HandleRooms serves /api/v1/rooms/{id}, /members and /messages.
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
	var req generated.RoomMessageRequest
	if !decodeAndValidateStrict(w, r, "RoomMessageRequest", &req, a.agentLoop.GetConfig().Gateway.ValidateInbound) {
		return
	}
	svc := a.roomService(w)
	if svc == nil {
		return
	}
	if err := svc.PostMessage(roomID, req, requestUsername(r)); err != nil {
		writeRoomError(w, "message", err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
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
	if entry == nil || entry.RoomRef == nil {
		return
	}
	if hub := a.rooms.Load(); hub != nil && hub.svc != nil {
		hub.svc.AwaitAdmission(entry.RoomRef.RequestId, roomAdmissionAwaitLimit)
	}
}

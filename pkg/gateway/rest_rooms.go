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
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

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
	var req generated.RoomMessageRequest
	if !decodeAndValidateStrict(w, r, "RoomMessageRequest", &req, a.agentLoop.GetConfig().Gateway.ValidateInbound) {
		return
	}
	if req.Mentions == nil {
		jsonErr(w, http.StatusBadRequest, "mentions is required (an empty list addresses everyone)")
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

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ws_rooms.go: attach_room (omnipus-system-agent K45). A room is attached on
// the existing hub connection without an agent session: the server answers
// with room_snapshot (entries after the client's cursor, members, mutes,
// states, pause, generation, cost) and then sends the room's live frames
// (room_entry, room_member_state, room_meta), each with its room seq.

package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

// handleAttachRoom answers one attach_room frame. A refusal (unknown room,
// storage failure) is an error frame naming the room, never a silent drop.
func (h *WSHandler) handleAttachRoom(wc *wsConn, data []byte) {
	f, err := decodeAttachRoom(data)
	if err != nil || f.RoomId == "" || f.AfterSeq < 0 {
		wc.inboundDropped.Add(1)
		sendConnGenFrame(wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
			Type: string(generated.WsFrameTypeError), Message: "attach_room requires room_id and after_seq >= 0",
		})
		return
	}
	if h.rooms == nil || h.rooms.svc == nil {
		sendConnGenFrame(wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
			Type: string(generated.WsFrameTypeError), Message: "group chats are not available on this gateway",
		})
		return
	}
	err = h.rooms.svc.Attach(f.RoomId, f.AfterSeq, func(snap generated.RoomSnapshotFrame) {
		sendConnGenFrame(wc, string(generated.WsFrameTypeRoomSnapshot), snap)
		h.rooms.subscribe(f.RoomId, wc)
	})
	if err != nil {
		slog.Warn("ws: attach_room refused", "room_id", f.RoomId, "error", err)
		sendConnGenFrame(wc, string(generated.WsFrameTypeError), generated.ErrorFrame{
			Type: string(generated.WsFrameTypeError), Message: "Could not open the group chat: " + err.Error(),
		})
	}
}

// decodeAttachRoom decodes an attach_room frame strictly, whatever the
// inbound validation setting (§7.2): room_id and after_seq must be present
// and non-null, and no other field is allowed.
func decodeAttachRoom(data []byte) (generated.AttachRoomFrame, error) {
	var f generated.AttachRoomFrame
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return f, err
	}
	for _, key := range []string{"room_id", "after_seq"} {
		v, ok := fields[key]
		if !ok || bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return f, fmt.Errorf("%s is required", key)
		}
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return f, err
	}
	return f, nil
}

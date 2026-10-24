// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"encoding/json"
	"log/slog"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/secretrequest"
)

func (h *WSHandler) wireSecretRequests() {
	reg := h.agentLoop.SecretRequests()
	if reg == nil {
		return
	}
	reg.SetSink(h.broadcastSecretRequest)
	if err := reg.Recover(); err != nil {
		slog.Error("secret requests: saved state could not be recovered; card service is unavailable", "error_type", "saved_state_unavailable")
		return
	}
	if err := reg.RetryResumes(); err != nil {
		slog.Error("secret requests: submitted continuation not delivered; boot or attach will retry", "error_type", "resume_unavailable")
	}
}

func (h *WSHandler) broadcastSecretRequest(rec secretrequest.Record) {
	card := rec.Card
	var payload any
	if card.State == "pending" {
		payload = gen.SecretRequestFrame{
			Type: string(gen.WsFrameTypeSecretRequest), RequestId: card.RequestId,
			SessionId: card.SessionId, RootSessionId: card.RootSessionId, WorkspaceId: card.WorkspaceId,
			ActingAgentId: card.ActingAgentId, ActingAgentName: card.ActingAgentName,
			Purpose: card.Purpose, Recipient: card.Recipient, Fields: card.Fields,
			Owner: card.Owner, OriginLabel: card.OriginLabel,
		}
	} else {
		sid := card.SessionId
		payload = gen.SecretRequestResolvedFrame{Type: string(gen.WsFrameTypeSecretRequestResolved), RequestId: card.RequestId, SessionId: &sid, RootSessionId: card.RootSessionId, State: card.State}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		slog.Error("secret requests: could not encode value-free card frame")
		return
	}
	h.mu.Lock()
	conns := make([]*wsConn, 0, len(h.sessions))
	for _, wc := range h.sessions {
		if rec.Owner == "" || wc.userID == rec.Owner {
			conns = append(conns, wc)
		}
	}
	h.mu.Unlock()
	for _, wc := range conns {
		if !wc.enqueue(raw) {
			slog.Warn("secret request frame not queued; reconnect restores saved state", "request_id", card.RequestId)
		}
	}
}

func (h *WSHandler) secretRequestSnapshot(wc *wsConn, sessionID string) []gen.SessionStateSecretRequest {
	if h == nil || h.agentLoop == nil || wc == nil {
		return []gen.SessionStateSecretRequest{}
	}
	reg := h.agentLoop.SecretRequests()
	if reg == nil {
		return []gen.SessionStateSecretRequest{}
	}
	if err := reg.RetryResumes(); err != nil {
		slog.Error("secret requests: submitted continuation not delivered; attach will retry", "error_type", "resume_unavailable")
	}
	return reg.Snapshot(wc.userID, sessionID)
}

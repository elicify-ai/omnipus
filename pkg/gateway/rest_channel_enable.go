// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// pairingRetryConflictText is the contract's fixed 409 text
// (ChannelEnableRequest, code pairing_retry_conflict). It names no ids.
const pairingRetryConflictText = "This chat can no longer retry this pairing. " +
	"Open the WhatsApp connector to start a new pairing."

// channelEnableBodyLimit bounds the optional enable body; the contract's
// largest valid body is a few hundred bytes.
const channelEnableBodyLimit = 64 << 10

// enableChannel serves PUT /channels/{id}/enable. An absent body or {} is an
// ordinary enable that grants no chat ownership. A pairing_retry body is an
// explicit chat Retry (Train 2 gate r2 T2-R2-S1 / R2-A02 / CR-T2-04 /
// T2-SF-04, architect ruling arch-retry-contract.md): the caller must own the
// session, and the request/session pair must be the channel's recorded
// pairing owner; the next attempt is claimed for that chat before anything is
// enabled. Any refusal enables nothing and changes no ownership.
func (a *restAPI) enableChannel(w http.ResponseWriter, r *http.Request, channelID string) {
	req, ok := decodeChannelEnableRequest(w, r)
	if !ok {
		return
	}
	if req == nil || req.PairingRetry == nil {
		_ = a.setChannelEnabled(w, channelID, true)
		return
	}
	requestID, sessionID := req.PairingRetry.RequestId, req.PairingRetry.SessionId
	if !a.admitPairingRetry(w, r, channelID, requestID, sessionID) {
		return
	}
	if !a.setChannelEnabled(w, channelID, true) {
		a.agentLoop.ReleasePairingClaim(channelID, requestID, sessionID)
		return
	}
	// The enable waited for the reload, so an attempt it started has bound the
	// claim; a claim no attempt consumed must not pass to a later one.
	a.agentLoop.SettlePairingRequest(channelID, requestID, sessionID)
}

// decodeChannelEnableRequest reads the optional body. It validates against
// the contract schema in every inbound-validation mode, because a malformed
// Retry must never be downgraded to an ordinary enable: null, a null or
// partial pairing_retry, unknown fields and blank or oversized ids are 400.
// It returns (nil, true) for an absent body.
func decodeChannelEnableRequest(w http.ResponseWriter, r *http.Request) (*gen.ChannelEnableRequest, bool) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, channelEnableBodyLimit+1))
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "could not read the request body")
		return nil, false
	}
	if len(raw) > channelEnableBodyLimit {
		jsonErr(w, http.StatusBadRequest, "request body too large")
		return nil, false
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, true
	}
	if msg, serverErr := validateBodyAgainstSchema("ChannelEnableRequest", raw); msg != "" {
		if serverErr {
			slog.Error("rest: ChannelEnableRequest schema unavailable", "error", msg)
			jsonErr(w, http.StatusInternalServerError, "request validation is unavailable")
			return nil, false
		}
		jsonErr(w, http.StatusBadRequest, "invalid enable request: "+msg)
		return nil, false
	}
	var req gen.ChannelEnableRequest
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid JSON body")
		return nil, false
	}
	return &req, true
}

// admitPairingRetry authenticates the Retry and claims the channel's next
// pairing attempt for that chat. It writes the refusal and returns false on
// any failure, before any configuration or ownership change.
func (a *restAPI) admitPairingRetry(w http.ResponseWriter, r *http.Request, channelID, requestID, sessionID string) bool {
	baseType, _ := config.ParseInstanceKey(channelID)
	if !validChannelIDs[baseType] {
		jsonErr(w, http.StatusNotFound, "channel not found")
		return false
	}
	if baseType != "whatsapp" && baseType != "whatsapp_native" {
		jsonErr(w, http.StatusBadRequest, "pairing_retry applies only to WhatsApp QR pairing")
		return false
	}
	if a.agentLoop == nil {
		jsonErr(w, http.StatusServiceUnavailable, "pairing ownership is unavailable; nothing was enabled")
		return false
	}
	store := a.resolveSessionStore(sessionID)
	if store == nil {
		jsonErr(w, http.StatusNotFound, "session not found; nothing was enabled")
		return false
	}
	meta, err := store.GetMeta(sessionID)
	if err != nil || meta == nil {
		slog.Error("rest: pairing retry could not read the session", "channel", channelID, "error", err)
		jsonErr(w, http.StatusServiceUnavailable, "the chat session could not be read; nothing was enabled")
		return false
	}
	if meta.Owner != requestUsername(r) {
		jsonErr(w, http.StatusForbidden, "this chat belongs to another user; nothing was enabled")
		return false
	}
	if err := a.agentLoop.ClaimPairingRetry(channelID, requestID, sessionID); err != nil {
		if errors.Is(err, agent.ErrPairingRetryConflict) {
			jsonErrCode(w, http.StatusConflict, pairingRetryConflictText, "pairing_retry_conflict")
			return false
		}
		slog.Error("rest: pairing retry claim failed", "channel", channelID, "error", err)
		jsonErr(w, http.StatusServiceUnavailable, "pairing ownership is unavailable; nothing was enabled")
		return false
	}
	return true
}

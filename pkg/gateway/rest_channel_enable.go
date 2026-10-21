// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// pairingRetryConflictText is the contract's fixed 409 text
// (ChannelEnableRequest, code pairing_retry_conflict). It names no ids.
const pairingRetryConflictText = "This chat can no longer retry this pairing. " +
	"Open the WhatsApp connector to start a new pairing."

// pairingRetryUnconfirmedText answers a Retry whose enable saved but whose
// reload did not confirm the start in time: the admitted attempt is kept,
// because its start can still happen (gate r3 M10).
const pairingRetryUnconfirmedText = "The WhatsApp connection is enabled, but its restart could not be " +
	"confirmed yet. The new code appears in this chat when the pairing starts."

// pairingCancelledText answers an enable whose configuration saved but whose
// reserved pairing was cancelled (a Stop, disable or delete) before it
// started: the enabled setting stays saved and no fallback pairing starts
// (gate r4 T2-SF-15, R4-A06, M14). It is not the effect-free 409.
const pairingCancelledText = "The WhatsApp connection is turned on, but its pairing was cancelled before it started. " +
	"Press Retry to start a new one."

// ordinaryUnconfirmedText answers an ordinary (Connectors) enable whose
// configuration saved but whose reload did not confirm the start in time: its
// unowned reservation is kept, because that start can still happen (gate r5
// R5-A04).
const ordinaryUnconfirmedText = "The WhatsApp connection is enabled, but its restart could not be " +
	"confirmed yet. The new code appears here when the pairing starts."

// pairingInProgressText refuses an ordinary enable while another pairing of
// the connector is reserved, running or stopping: nothing is saved (gate r4
// T2-SF-13, R4-A02, M2). Code pairing_in_progress.
const pairingInProgressText = "Another WhatsApp pairing of this connection is starting, running or stopping, " +
	"so nothing was changed. Wait for it to finish, or stop it from the chat that started it."

// channelEnableBodyLimit bounds the optional enable body; the contract's
// largest valid body is a few hundred bytes.
const channelEnableBodyLimit = 64 << 10

// errChannelAbsent refuses to create a missing channel entry where the request
// requires an existing one.
var errChannelAbsent = errors.New("channel instance does not exist")

// enableChannel serves PUT /channels/{id}/enable. An absent body or {} is an
// ordinary enable that grants no chat ownership; turning a WhatsApp connector
// on starts a new unowned pairing attempt. A pairing_retry body is an explicit
// chat Retry (gate r3, binding pairing-ownership model M1–M4, M10, M12, M14):
// the connector must exist, the caller must own the session, and the chat
// must own the latest ended attempt; a NEW attempt is reserved for it before
// anything is enabled. Any refusal enables nothing and changes no ownership.
func (a *restAPI) enableChannel(w http.ResponseWriter, r *http.Request, channelID string) {
	req, ok := decodeChannelEnableRequest(w, r)
	if !ok {
		return
	}
	if req == nil || req.PairingRetry == nil {
		a.enableChannelOrdinary(w, channelID)
		return
	}
	requestID, sessionID := req.PairingRetry.RequestId, req.PairingRetry.SessionId
	var attemptID string
	outcome := a.applyChannelEnabled(w, channelID, true, channelEnableOptions{
		requireExisting: true, retryRequest: r, requestID: requestID, sessionID: sessionID, attemptID: &attemptID,
	})
	switch outcome {
	case enableFailed:
		a.agentLoop.ReleasePairing(channelID, attemptID)
	case enableUnconfirmed:
		jsonErr(w, http.StatusInternalServerError, pairingRetryUnconfirmedText)
	default:
		// The reload completed: an admission no start consumed is closed and
		// its chat told; one the start took (or a linked device closed) is
		// left as it is (gate r5 R5-A05).
		a.agentLoop.SettlePairing(channelID, attemptID)
		jsonOK(w, gen.ChannelEnabledResponse{Id: channelID, Enabled: true})
	}
}

// enableChannelOrdinary is the bodyless enable. A QR-paired (WhatsApp)
// connector must already exist — it is added through Configure, so an enable
// racing its deletion never re-creates it (gate r4 DOCS R4-D2, M12). Turning
// one on reserves an unowned pairing attempt for its start (M1, M2): a
// refusal changes nothing (409 pairing_in_progress, or 404 while it is being
// deleted). An enable of a connector already on is idempotent and reserves
// nothing.
func (a *restAPI) enableChannelOrdinary(w http.ResponseWriter, channelID string) {
	var attemptID string
	opts := channelEnableOptions{requireExisting: pairingCapable(channelID), attemptID: &attemptID}
	outcome := a.applyChannelEnabled(w, channelID, true, opts)
	switch {
	case attemptID == "":
		if outcome != enableFailed {
			jsonOK(w, gen.ChannelEnabledResponse{Id: channelID, Enabled: true})
		}
	case outcome == enableFailed:
		a.agentLoop.ReleasePairing(channelID, attemptID)
	case outcome == enableUnconfirmed:
		// The start can still happen: keep the reservation and say the restart
		// is unconfirmed, never a plain success (gate r5 R5-A04, M10, M14).
		jsonErr(w, http.StatusInternalServerError, ordinaryUnconfirmedText)
	default:
		a.agentLoop.SettlePairing(channelID, attemptID)
		jsonOK(w, gen.ChannelEnabledResponse{Id: channelID, Enabled: true})
	}
}

// admitOrdinaryPairing reserves the unowned attempt of a QR-paired connector
// an ordinary enable turns on. It writes the refusal and returns false before
// any effect. With no channel manager wired nothing can start, so there is
// nothing to reserve.
func (a *restAPI) admitOrdinaryPairing(w http.ResponseWriter, channelID string, order *channels.PairingOrder, alreadyEnabled bool) (string, bool) {
	if a.agentLoop == nil || !pairingCapable(channelID) || alreadyEnabled {
		return "", true
	}
	var attemptID string
	var err error
	if order != nil {
		rec, admitErr := order.Admit(channels.PairingAdmissionEnable, nil)
		attemptID, err = rec.AttemptID, admitErr
	} else {
		attemptID, err = a.agentLoop.AdmitEnablePairing(channelID)
	}
	switch {
	case err == nil, errors.Is(err, agent.ErrPairingUnavailable):
		return attemptID, true
	case errors.Is(err, channels.ErrPairingDeleting):
		jsonErr(w, http.StatusNotFound, fmt.Sprintf("channel %q not found", channelID))
	case errors.Is(err, channels.ErrPairingConflict), errors.Is(err, channels.ErrPairingStopping):
		jsonErrCode(w, http.StatusConflict, pairingInProgressText, "pairing_in_progress")
	default:
		slog.Error("rest: ordinary enable pairing admission failed", "channel", channelID, "error", err)
		jsonErr(w, http.StatusServiceUnavailable, "pairing ownership is unavailable; nothing was enabled")
	}
	return "", false
}

// disableChannel serves PUT /channels/{id}/disable. A not-yet-started pairing
// admission of a connector being turned off can never start late (gate r3
// M2), and a missing connector is a 404, never created (M12).
func (a *restAPI) disableChannel(w http.ResponseWriter, channelID string) {
	opts := channelEnableOptions{requireExisting: true}
	if a.applyChannelEnabled(w, channelID, false, opts) != enableFailed {
		jsonOK(w, gen.ChannelEnabledResponse{Id: channelID, Enabled: false})
	}
}

func pairingCapable(channelID string) bool {
	baseType, _ := config.ParseInstanceKey(channelID)
	return baseType == "whatsapp" || baseType == "whatsapp_native"
}

// channelEnabledNow reports whether channelID is enabled in the live config.
func (a *restAPI) channelEnabledNow(channelID string) bool {
	cfg := a.agentLoop.GetConfig()
	if cfg == nil {
		return false
	}
	inst, ok := cfg.Channels[channelID]
	return ok && inst.Enabled
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

// admitPairingRetry authenticates the Retry and reserves a new pairing attempt
// for that chat. It writes the refusal and returns false on any failure,
// before any configuration or ownership change.
func (a *restAPI) admitPairingRetry(w http.ResponseWriter, r *http.Request, channelID, requestID, sessionID string, orders ...*channels.PairingOrder) (string, bool) {
	if len(orders) == 0 {
		store := a.pairingOrderStore()
		if store == nil {
			jsonErr(w, http.StatusServiceUnavailable, "pairing ownership is unavailable; nothing was enabled")
			return "", false
		}
		a.configMu.Lock()
		defer a.configMu.Unlock()
		order := store.LockChannel(channelID)
		defer order.Unlock()
		if cfg := a.agentLoop.GetConfig(); cfg == nil || !channelInstanceExists(cfg, channelID) {
			jsonErr(w, http.StatusNotFound, "this WhatsApp connection no longer exists; nothing was enabled")
			return "", false
		}
		return a.admitPairingRetry(w, r, channelID, requestID, sessionID, order)
	}
	order := orders[0]
	baseType, _ := config.ParseInstanceKey(channelID)
	if !validChannelIDs[baseType] {
		jsonErr(w, http.StatusNotFound, "channel not found")
		return "", false
	}
	if !pairingCapable(channelID) {
		jsonErr(w, http.StatusBadRequest, "pairing_retry applies only to WhatsApp QR pairing")
		return "", false
	}
	if a.agentLoop == nil {
		jsonErr(w, http.StatusServiceUnavailable, "pairing ownership is unavailable; nothing was enabled")
		return "", false
	}
	store := a.resolveSessionStore(sessionID)
	if store == nil {
		jsonErr(w, http.StatusNotFound, "session not found; nothing was enabled")
		return "", false
	}
	meta, err := store.GetMeta(sessionID)
	if err != nil || meta == nil {
		slog.Error("rest: pairing retry could not read the session", "channel", channelID, "error", err)
		jsonErr(w, http.StatusServiceUnavailable, "the chat session could not be read; nothing was enabled")
		return "", false
	}
	if meta.Owner != requestUsername(r) {
		jsonErr(w, http.StatusForbidden, "this chat belongs to another user; nothing was enabled")
		return "", false
	}
	var attemptID string
	if order == nil {
		err = agent.ErrPairingUnavailable
	} else {
		rec, admitErr := order.AdmitContext(r.Context(), channels.PairingAdmissionRetry,
			&channels.PairingOwner{RequestID: requestID, SessionID: sessionID})
		attemptID, err = rec.AttemptID, admitErr
	}
	switch {
	case errors.Is(err, channels.ErrPairingDeleting):
		// The connector is being deleted (M12): it is not recreated.
		jsonErr(w, http.StatusNotFound, "this WhatsApp connection no longer exists; nothing was enabled")
		return "", false
	case errors.Is(err, channels.ErrPairingConflict):
		jsonErrCode(w, http.StatusConflict, pairingRetryConflictText, "pairing_retry_conflict")
		return "", false
	case errors.Is(err, agent.ErrPairingUnavailable):
		jsonErr(w, http.StatusServiceUnavailable, "pairing ownership is unavailable; nothing was enabled")
		return "", false
	case err != nil:
		slog.Error("rest: pairing retry admission failed", "channel", channelID, "error", err)
		jsonErr(w, http.StatusServiceUnavailable, "pairing ownership is unavailable; nothing was enabled")
		return "", false
	}
	return attemptID, true
}

func channelInstanceExists(cfg *config.Config, channelID string) bool {
	_, ok := cfg.Channels[channelID]
	return ok
}

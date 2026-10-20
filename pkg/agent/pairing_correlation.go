// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"time"

	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

// pairingStopTimeout bounds how long a Stop waits for the pairing client to
// release; Stop must not hang on a stuck platform connection.
const pairingStopTimeout = 10 * time.Second

// pairingCorrelation is the chat request that started a channel pairing.
type pairingCorrelation struct {
	requestID string
	sessionID string
}

// NotePairingRequest records that an approved chat request (connect_channel)
// starts the pairing of channelID, so the next pairing attempt's QR and
// progress frames carry its request and session ids (K10, FR-081) and that
// chat's Stop ends it. It never takes over another chat's claim still waiting
// for its attempt (pairing_ownership.go).
func (al *AgentLoop) NotePairingRequest(channelID, requestID, sessionID string) {
	if channelID == "" || requestID == "" || sessionID == "" {
		return
	}
	al.pairingOwners.claimNext(channelID, pairingCorrelation{requestID: requestID, sessionID: sessionID})
}

// SettlePairingRequest is called once the start a claim was made for has
// finished: a claim no pairing attempt consumed (the device was already
// linked, or nothing started) is disarmed so a later, unrelated attempt
// cannot inherit it.
func (al *AgentLoop) SettlePairingRequest(channelID, requestID, sessionID string) {
	al.pairingOwners.settleClaim(channelID, pairingCorrelation{requestID: requestID, sessionID: sessionID})
}

// ClaimPairingRetry is the atomic ownership admission for a chat Retry
// (PUT /channels/{id}/enable with pairing_retry): requestID/sessionID must be
// the channel's recorded last owner and no other chat may hold a claim; then
// that chat owns the next attempt. The caller has already proved the session
// belongs to the requesting user. ErrPairingRetryConflict refuses it.
func (al *AgentLoop) ClaimPairingRetry(channelID, requestID, sessionID string) error {
	if channelID == "" || requestID == "" || sessionID == "" {
		return ErrPairingRetryConflict
	}
	return al.pairingOwners.claimRetry(channelID, pairingCorrelation{requestID: requestID, sessionID: sessionID})
}

// ReleasePairingClaim returns a Retry claim whose enable failed: the chat is
// the last owner again and may retry. Another chat's newer claim is kept.
func (al *AgentLoop) ReleasePairingClaim(channelID, requestID, sessionID string) {
	al.pairingOwners.releaseClaim(channelID, pairingCorrelation{requestID: requestID, sessionID: sessionID})
}

// channelLiveStatus reports whether a configured channel instance is running
// after the last reload, and a value-free failure reason when it is not.
func (al *AgentLoop) channelLiveStatus(id string) (running bool, failure string, known bool) {
	mgr := al.GetChannelManager()
	if mgr == nil {
		return false, "", false
	}
	if ch, ok := mgr.GetChannel(id); ok {
		return ch.IsRunning(), "", true
	}
	for _, failed := range mgr.FailedChannels() {
		if failed.Name == id {
			if failed.CredentialScoped {
				return false, "a configured credential reference is missing from the vault", true
			}
			return false, "the channel did not start: the platform rejected the credentials or could not be reached", true
		}
	}
	return false, "the channel is not running", true
}

// stopPairingsForSession ends every in-flight pairing that sessionID owns
// (BDD-8.8: Stop ends pairing with "stopped"). Each ended pairing emits a
// "stopped" frame carrying its request and session ids. A Stop that finds no
// pairing in flight keeps the chat as the channel's last owner instead of
// deleting the only ownership record (gate r2 CR-T2-04), so the chat's later
// Retry is still correlated.
func (al *AgentLoop) stopPairingsForSession(sessionID string) {
	if sessionID == "" {
		return
	}
	for channelID, corr := range al.pairingOwners.ownedBy(sessionID) {
		if al.stopChannelPairing(channelID) {
			al.EmitWhatsAppPairing(channelID, channels.PairingStatusStopped, "", "pairing stopped from the chat")
			continue
		}
		al.pairingOwners.endWithoutAttempt(channelID, corr)
	}
}

// stopChannelPairing asks the channel to end its in-flight pairing.
func (al *AgentLoop) stopChannelPairing(channelID string) bool {
	mgr := al.GetChannelManager()
	if mgr == nil {
		return false
	}
	ch, ok := mgr.GetChannel(channelID)
	if !ok {
		return false
	}
	stoppable, ok := ch.(channels.PairingStoppable)
	if !ok {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), pairingStopTimeout)
	defer cancel()
	stopped := stoppable.StopPairing(ctx)
	if stopped {
		logger.InfoCF("agent", "Stopped an in-flight channel pairing on Stop", map[string]any{"channel_id": channelID})
	}
	return stopped
}

// ensureChannelStarted starts a registered channel that is not running, for
// an approved connect_channel (gate r1 architect F04). A channel the manager
// does not hold is left to the reload's own start and failure reporting.
func (al *AgentLoop) ensureChannelStarted(id string) (bool, error) {
	mgr := al.GetChannelManager()
	if mgr == nil {
		return false, nil
	}
	if _, registered := mgr.GetChannel(id); !registered {
		return false, nil
	}
	return mgr.StartChannel(id)
}

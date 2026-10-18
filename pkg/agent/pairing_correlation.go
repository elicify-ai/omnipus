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

// NotePairingRequest records that a chat request started the pairing of
// channelID, so the pairing's QR and progress frames carry its request and
// session ids (K10, FR-081) until the pairing links, fails or times out.
func (al *AgentLoop) NotePairingRequest(channelID, requestID, sessionID string) {
	if channelID == "" || requestID == "" || sessionID == "" {
		return
	}
	al.pairingRequests.Store(channelID, pairingCorrelation{requestID: requestID, sessionID: sessionID})
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

// stopPairingsForSession ends every in-flight pairing that sessionID started
// from its chat (BDD-8.8: Stop ends pairing with "stopped"). Each ended
// pairing emits a "stopped" frame carrying its request and session ids, which
// also clears the correlation. A pairing that is no longer in flight (already
// linked, or the channel is gone) only drops its correlation.
func (al *AgentLoop) stopPairingsForSession(sessionID string) {
	if sessionID == "" {
		return
	}
	al.pairingRequests.Range(func(key, value any) bool {
		channelID, _ := key.(string)
		corr, ok := value.(pairingCorrelation)
		if !ok || corr.sessionID != sessionID {
			return true
		}
		if al.stopChannelPairing(channelID) {
			al.EmitWhatsAppPairing(channelID, channels.PairingStatusStopped, "", "pairing stopped from the chat")
			return true
		}
		al.pairingRequests.CompareAndDelete(channelID, corr)
		return true
	})
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

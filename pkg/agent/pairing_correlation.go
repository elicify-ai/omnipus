// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

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

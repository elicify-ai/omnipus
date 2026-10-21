package agent

import "github.com/elicify-ai/omnipus/pkg/session"

// wakeStreamAddress resolves only the turn's destination. Stored metadata is
// never rewritten: main/empty channels and sessions without a peer belong to
// their own web chat, while a real external address retains both components.
func wakeStreamAddress(meta *session.UnifiedMeta) (channel, chatID string) {
	if meta.Channel == "main" || meta.Channel == "" || meta.PeerID == "" {
		return "webchat", meta.ID
	}
	return meta.Channel, meta.PeerID
}

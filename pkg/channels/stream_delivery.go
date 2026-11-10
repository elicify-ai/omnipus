package channels

import "github.com/elicify-ai/omnipus/pkg/bus"

// SettleStreamedResponse preserves the bus -> Manager -> gateway fallback
// hand-off used by GetStreamer. Webchat owns its already-delivered outcome;
// connector streams without this capability retain their existing behavior.
func (m *Manager) SettleStreamedResponse(channel, chatID string) bool {
	settler, ok := m.streamFallback.(bus.StreamedResponseSettler)
	return ok && settler.SettleStreamedResponse(channel, chatID)
}

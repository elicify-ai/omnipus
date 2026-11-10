package gateway

// SettleStreamedResponse is called after a turn completes, before its worker
// drains another turn. Its live outcome is already published: consume only its
// pending duplicate-suppression flag, without queueing an asynchronous Send that
// could instead consume the following turn's flag. Forget its old identity too.
func (h *WSHandler) SettleStreamedResponse(channel, chatID string) bool {
	if channel != "webchat" {
		return false
	}
	h.mu.Lock()
	webchat := h.webchatCh
	h.mu.Unlock()
	if webchat == nil {
		return false
	}
	webchat.mu.Lock()
	defer webchat.mu.Unlock()
	if !webchat.streamed[chatID] {
		return false
	}
	delete(webchat.streamed, chatID)
	delete(webchat.turnIdentity, chatID)
	return true
}

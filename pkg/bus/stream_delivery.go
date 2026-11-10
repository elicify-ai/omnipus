package bus

// StreamedResponseSettler acknowledges an outcome already delivered by a
// stream, synchronously before its owner starts another turn. It is internal
// delivery bookkeeping, not a gateway wire message or another send.
type StreamedResponseSettler interface {
	SettleStreamedResponse(channel, chatID string) bool
}

func (mb *MessageBus) SettleStreamedResponse(channel, chatID string) bool {
	delegate := mb.streamDelegate.Load()
	if delegate == nil {
		return false
	}
	settler, ok := (*delegate).(StreamedResponseSettler)
	return ok && settler.SettleStreamedResponse(channel, chatID)
}

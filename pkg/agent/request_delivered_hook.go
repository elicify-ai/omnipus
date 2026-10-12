// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// request_delivered_hook.go: a held request that is finally delivered moves its
// sender's row on. The gateway tells the sender "held" when a person's @-request
// meets a stopped agent; without a follow-up that row would say "waiting" for
// ever (combined-review round 2, R2-S2-02).

package agent

// RequestDeliveredFunc receives the sender's chat and message id for a HELD
// request that has just been given to a running turn of its receiver.
type RequestDeliveredFunc func(senderSessionID, senderMessageID string)

// SetRequestDeliveredHook installs the single receiver (the gateway's WS handler,
// at construction). Nil removes it.
func (al *AgentLoop) SetRequestDeliveredHook(fn RequestDeliveredFunc) {
	if al == nil {
		return
	}
	if fn == nil {
		al.requestDeliveredHook.Store(nil)
		return
	}
	al.requestDeliveredHook.Store(&fn)
}

// MarkRequestDeliveredForTest drives the delivery mark the way a receiver's turn
// does, for the gateway's wiring test.
func (al *AgentLoop) MarkRequestDeliveredForTest(receiverSessionID, requestID string) {
	al.markRequestDelivered(receiverSessionID, requestID)
}

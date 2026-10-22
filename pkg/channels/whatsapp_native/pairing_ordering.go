// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package whatsapp

import (
	"context"

	"github.com/elicify-ai/omnipus/pkg/channels"
)

// finishReplacement advertises Retry only after this captured resource's
// cleanup completed. A later writer/Stop cannot have its barrier released here.
func (c *WhatsAppNativeChannel) finishReplacement(store *channels.PairingAttempts, captured channels.PairingAttempt) {
	if done, finished := store.FinishReplacement(captured.Instance, captured.AttemptID, captured.StopGeneration, nil); finished {
		c.emitAttempt(done.AttemptID, channels.PairingStatusError, "", channels.PairingInterruptedMessage)
	}
}

// pinStart records the resource's originating generation before its local or
// platform effects. Activation still rechecks its exact attempt later. No
// manager/native lock is acquired from inside the ordering guard.
func (c *WhatsAppNativeChannel) pinStart(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.attempts == nil {
		return nil
	}
	order := c.attempts.LockChannel(c.instance)
	defer order.Unlock()
	if c.originConfig != nil && !order.MatchesConfig(c.originConfig) {
		return channels.ErrPairingConflict
	}
	if stamp, hasStamp := channels.PairingStartStampFrom(ctx); hasStamp {
		token, _ := channels.PairingStartFrom(ctx)
		if !order.AcceptsStartStamp(token, stamp) {
			return channels.ErrPairingConflict
		}
		c.resourceGeneration = stamp.Generation
	} else {
		c.resourceGeneration = order.Generation()
	}
	return nil
}

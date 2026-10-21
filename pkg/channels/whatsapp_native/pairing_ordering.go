// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package whatsapp

import "github.com/elicify-ai/omnipus/pkg/channels"

// pinStart records the resource's originating generation before its local or
// platform effects. Activation still rechecks its exact attempt later. No
// manager/native lock is acquired from inside the ordering guard.
func (c *WhatsAppNativeChannel) pinStart() error {
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
	c.resourceGeneration = order.Generation()
	return nil
}

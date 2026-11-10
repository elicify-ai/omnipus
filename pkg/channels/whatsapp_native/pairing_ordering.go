// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package whatsapp

import (
	"context"

	"github.com/elicify-ai/omnipus/pkg/channels"
)

// restoreUnconnectedStart runs after the failed linked client's final disposal;
// only the same unfenced admission can await the manager's failed-start result.
func (c *WhatsAppNativeChannel) restoreUnconnectedStart(token string) {
	c.mu.Lock()
	store, instance := c.attempts, c.instance
	c.mu.Unlock()
	if store != nil {
		store.RestoreUnconnectedStart(instance, token)
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

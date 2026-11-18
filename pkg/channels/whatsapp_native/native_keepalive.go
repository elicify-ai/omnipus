//go:build !mipsle && !netbsd && !(freebsd && arm)

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package whatsapp

import (
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types/events"

	"github.com/elicify-ai/omnipus/pkg/logger"
)

// handleKeepAliveTimeout takes over the keepalive reconnect the library gives
// up when EnableAutoReconnect is off (#1687 R3). whatsmeow's keepAliveLoop
// forces a reconnect only with that flag on, once time.Since(LastSuccess) is
// strictly greater than KeepAliveMaxFailTime, and emits no Disconnected on
// that path. Past the same threshold, this channel disconnects the client and
// runs its own reconnectWithBackoff, so every dial goes through connectNative
// and counts in the #1681 connectsInFlight/nativeStopMark pair.
//
// One reconnect per dead connection: the reconnecting flag drops further
// timeouts while a reconnect runs, and keepAliveFailedSince drops late
// timeouts from the connection that was already replaced (their LastSuccess is
// not newer than the one that triggered). KeepAliveRestored needs no handling:
// below the threshold nothing started, and a reconnect already pending simply
// completes.
func (c *WhatsAppNativeChannel) handleKeepAliveTimeout(ev *events.KeepAliveTimeout) {
	if time.Since(ev.LastSuccess) <= whatsmeow.KeepAliveMaxFailTime {
		return
	}
	c.reconnectMu.Lock()
	if !ev.LastSuccess.After(c.keepAliveFailedSince) {
		c.reconnectMu.Unlock()
		return
	}
	c.keepAliveFailedSince = ev.LastSuccess
	// stopping is read under reconnectMu so this check and wg.Add are atomic
	// with respect to Stop setting the flag and waiting (as for Disconnected).
	if c.stopping.Load() || c.reconnecting {
		c.reconnectMu.Unlock()
		return
	}
	c.reconnecting = true
	c.wg.Add(1)
	c.reconnectMu.Unlock()
	logger.WarnCF("whatsapp", "WhatsApp keepalive failed past the limit; reconnecting", map[string]any{
		"error_count":  ev.ErrorCount,
		"last_success": ev.LastSuccess,
	})
	go func() {
		defer c.wg.Done()
		c.mu.Lock()
		client := c.client
		c.mu.Unlock()
		if client != nil {
			// Close the dead socket first; ConnectContext refuses a client that
			// still holds one (ErrAlreadyConnected). Disconnect marks the drop
			// as expected, so the library emits no Disconnected for it.
			client.Disconnect()
		}
		c.reconnectWithBackoff()
	}()
}

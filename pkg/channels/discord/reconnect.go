package discord

import (
	"context"
	"errors"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/elicify-ai/omnipus/pkg/logger"
)

// Reconnect back-off, mirroring discordgo v0.29.0's own loop: the first
// attempt is immediate, then the wait doubles from 1 s up to 10 minutes.
const (
	reconnectFirstWait = time.Second
	reconnectMaxWait   = 10 * time.Minute
)

// The adapter owns gateway reconnection (ghost-session fix). discordgo's
// built-in loop (Session.ShouldReconnectOnError) has no stop signal: a
// session stopped while that loop waits between attempts later resumed the
// old gateway session with the old token and ran the stopped adapter's
// handlers next to its replacement. NewDiscordChannel therefore turns the
// built-in loop off, and this loop runs instead. It starts on the session's
// Disconnect event and is tied to the adapter's run context: Stop cancels
// that context, under lifecycleMu, before closing the session, so after
// Stop no attempt starts and a waiting loop exits. An attempt already in
// Open is ended by the run-bound dialer (dialer.go), which refuses new dials
// and closes its connections once the run context is cancelled.

// onDisconnect is the session's Disconnect handler. discordgo emits the
// event from every Close — a read or heartbeat failure, a gateway Op 7
// reconnect request, and Stop's own Close. It starts at most one reconnect
// loop; a disconnect while a loop runs is recorded so that loop checks the
// connection again before it ends.
func (c *DiscordChannel) onDisconnect(_ *discordgo.Session, _ *discordgo.Disconnect) {
	c.lifecycleMu.Lock()
	defer c.lifecycleMu.Unlock()
	if c.cancel == nil || c.ctx.Err() != nil {
		return // never started, or stopped: Stop's own Close
	}
	if c.reconnecting {
		c.reconnectPending = true
		return
	}
	c.reconnecting = true
	go c.reconnectLoop(c.ctx)
}

// reconnectLoop reopens the gateway connection with back-off until it is
// open again or ctx (the adapter's run context) is cancelled.
func (c *DiscordChannel) reconnectLoop(ctx context.Context) {
	wait := time.Duration(0)
	for {
		if wait > 0 {
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
				c.endReconnect()
				return
			case <-timer.C:
			}
		}
		connected, stopped := c.reconnectOnce(ctx)
		if stopped {
			c.endReconnect()
			return
		}
		if connected {
			if c.finishReconnect() {
				return
			}
			// A disconnect arrived while this loop was finishing: check the
			// connection again at once.
			wait = 0
			continue
		}
		if wait == 0 {
			wait = reconnectFirstWait
		} else {
			wait = min(wait*2, reconnectMaxWait)
		}
	}
}

// reconnectOnce makes one Open attempt unless the adapter was stopped. It
// does not hold lifecycleMu across Open, so Stop never waits for a stalled
// attempt: if Stop cancels after the check, the run-bound dialer refuses or
// closes the attempt's connection and Open fails (CHECK F3).
func (c *DiscordChannel) reconnectOnce(ctx context.Context) (connected, stopped bool) {
	if ctx.Err() != nil {
		return false, true
	}
	logger.InfoC("discord", "Reconnecting to the Discord gateway")
	err := c.session.Open()
	if err == nil {
		logger.InfoC("discord", "Reconnected to the Discord gateway")
		return true, false
	}
	if errors.Is(err, discordgo.ErrWSAlreadyOpen) {
		return true, false
	}
	logger.WarnCF("discord", "Discord gateway reconnect failed; retrying", map[string]any{"error": err.Error()})
	return false, false
}

// finishReconnect ends the loop after a successful attempt. It reports false
// when a disconnect arrived meanwhile, so the loop must check again.
func (c *DiscordChannel) finishReconnect() bool {
	c.lifecycleMu.Lock()
	defer c.lifecycleMu.Unlock()
	if c.reconnectPending {
		c.reconnectPending = false
		return false
	}
	c.reconnecting = false
	return true
}

func (c *DiscordChannel) endReconnect() {
	c.lifecycleMu.Lock()
	defer c.lifecycleMu.Unlock()
	c.reconnecting = false
	c.reconnectPending = false
}

package discord

import (
	"context"
	"net"
	"sync"

	"github.com/gorilla/websocket"
)

// The gateway dialer is tied to the adapter's run context (CHECK F3).
// discordgo's Session.Open holds the session lock for the whole dial and
// WebSocket handshake and takes no context, so a reconnect whose handshake
// stalls would keep Stop's Close waiting for that lock until the handshake
// timeout. installStoppableDialer wraps the session's dialer so that every
// network connection it makes is closed when the run context ends: Stop's
// cancel then ends a stalled Open at once, and a dial that starts after Stop
// is refused. Everything else about the dialer, including its proxy and
// handshake timeout, is kept.

// installStoppableDialer replaces the session's dialer with a copy whose
// network dials end with the adapter's run context. It is called by Start,
// under lifecycleMu, after any caller has set the session's dialer; a dialer
// it installed earlier is not wrapped twice.
func (c *DiscordChannel) installStoppableDialer() {
	if c.session.Dialer == nil || c.session.Dialer != c.dialInstalled {
		c.dialBase = c.session.Dialer
	}
	var wrapped websocket.Dialer
	if c.dialBase != nil {
		wrapped = *c.dialBase
	} else {
		wrapped = *websocket.DefaultDialer
	}
	dial := wrapped.NetDialContext
	if dial == nil && wrapped.NetDial != nil {
		plain := wrapped.NetDial
		dial = func(_ context.Context, network, address string) (net.Conn, error) {
			return plain(network, address)
		}
	}
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	wrapped.NetDial = nil
	wrapped.NetDialContext = c.stoppableDial(dial)
	if wrapped.NetDialTLSContext != nil {
		wrapped.NetDialTLSContext = c.stoppableDial(wrapped.NetDialTLSContext)
	}
	c.dialInstalled = &wrapped
	c.session.Dialer = &wrapped
}

type dialFunc = func(ctx context.Context, network, address string) (net.Conn, error)

// stoppableDial wraps dial so the dial and the connection it returns both
// end when the adapter's run context ends.
func (c *DiscordChannel) stoppableDial(dial dialFunc) dialFunc {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		run := c.runContext()
		if err := run.Err(); err != nil {
			return nil, err // stopped: no new gateway connection
		}
		dialCtx, cancel := context.WithCancel(ctx)
		stopDial := context.AfterFunc(run, cancel)
		conn, err := dial(dialCtx, network, address)
		stopDial()
		cancel()
		if err != nil {
			return nil, err
		}
		owned := &runBoundConn{Conn: conn}
		owned.stop = context.AfterFunc(run, func() { _ = conn.Close() })
		return owned, nil
	}
}

// runContext returns the adapter's current run context.
func (c *DiscordChannel) runContext() context.Context {
	c.lifecycleMu.Lock()
	defer c.lifecycleMu.Unlock()
	return c.ctx
}

// runBoundConn is a gateway connection that the run context's end closes;
// closing it first releases that registration.
type runBoundConn struct {
	net.Conn
	stopOnce sync.Once
	stop     func() bool
}

func (r *runBoundConn) Close() error {
	r.stopOnce.Do(func() { r.stop() })
	return r.Conn.Close()
}

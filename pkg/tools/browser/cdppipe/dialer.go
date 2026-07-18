package cdppipe

import (
	"context"
	"net"
	"sync"

	"github.com/gobwas/ws"
)

// The cdppipe transport must feed chromedp, whose *Browser can only be built by
// dialing a ws:// URL (Browser.conn is unexported and NewBrowser hardcodes a
// gobwas dial). We route that dial through an IN-MEMORY net.Pipe — never a
// socket — so it stays invisible to every other process and preserves the
// "no TCP surface" guarantee.
//
// gobwas' Dialer exposes a NetDial hook. chromedp.DialContext uses the package
// global ws.DefaultDialer, so we install a process-wide dispatcher on it exactly
// once. The dispatcher returns the in-memory client conn for our synthetic
// per-launch hosts and TRANSPARENTLY falls through to the real net dialer (or a
// previously configured NetDial) for every other address, so it is a no-op for
// any other ws.Dial caller in the binary.

// magicHostSuffix is appended to a random per-launch id to form a synthetic,
// non-resolvable host. Using the reserved .invalid TLD guarantees the address
// can never collide with a real host or accidentally hit the network.
const magicHostSuffix = ".cdppipe.omnipus.invalid"

var (
	dialerOnce sync.Once
	registry   = &connRegistry{conns: map[string]chan net.Conn{}}
)

// connRegistry maps a synthetic dial address to a one-shot in-memory conn.
type connRegistry struct {
	mu    sync.Mutex
	conns map[string]chan net.Conn
}

// register associates addr with a single client conn to be taken exactly once.
func (r *connRegistry) register(addr string, c net.Conn) {
	ch := make(chan net.Conn, 1)
	ch <- c
	r.mu.Lock()
	r.conns[addr] = ch
	r.mu.Unlock()
}

// take returns the conn registered for addr, consuming the registration. It is
// one-shot: a second take for the same addr returns (nil, false).
func (r *connRegistry) take(addr string) (net.Conn, bool) {
	r.mu.Lock()
	ch, ok := r.conns[addr]
	if ok {
		delete(r.conns, addr)
	}
	r.mu.Unlock()
	if !ok {
		return nil, false
	}
	select {
	case c := <-ch:
		return c, true
	default:
		return nil, false
	}
}

// drop removes any pending registration for addr (teardown / failed launch).
func (r *connRegistry) drop(addr string) {
	r.mu.Lock()
	delete(r.conns, addr)
	r.mu.Unlock()
}

// installDialer wires the dispatcher onto ws.DefaultDialer exactly once, chaining
// to any prior NetDial (or the stdlib dialer) for addresses it does not own.
func installDialer() {
	dialerOnce.Do(func() {
		prev := ws.DefaultDialer.NetDial
		ws.DefaultDialer.NetDial = func(ctx context.Context, network, addr string) (net.Conn, error) {
			if c, ok := registry.take(addr); ok {
				return c, nil
			}
			if prev != nil {
				return prev(ctx, network, addr)
			}
			var d net.Dialer
			return d.DialContext(ctx, network, addr)
		}
	})
}

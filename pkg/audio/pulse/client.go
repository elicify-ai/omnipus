package pulse

import (
	"context"
	"fmt"
	"net"
	"os"
	"sync"
	"time"
)

// noDeadline clears a previously-set connection deadline (net.Conn.SetDeadline
// treats the zero Time as "no deadline").
var noDeadline time.Time

// protocolVersion is the client protocol version advertised in AUTH. 32
// matches noisetorch/pulseaudio's proven value; PulseAudio negotiates down to
// min(client, server) so an older client version is always safe, and none of
// the commands this package uses (AUTH, SET_CLIENT_NAME, LOAD_MODULE) are
// gated on a newer version.
const protocolVersion = 32

// versionMask strips the "shared memory enabled" high bit PulseAudio ORs
// into the negotiated version.
const versionMask = 0x0000ffff

// Client is a minimal, synchronous PulseAudio native-protocol client. It
// serializes one request at a time (Omnipus's audio sidecar never needs
// concurrent in-flight requests — see pkg/tools/browser/audiosink_linux.go),
// which lets this client skip the tag-multiplexing/pending-request-map
// machinery a general-purpose client needs.
type Client struct {
	mu   sync.Mutex
	conn net.Conn
	tag  uint32
}

// Dial connects to the PulseAudio native-protocol Unix socket at socketPath
// and completes the AUTH + SET_CLIENT_NAME handshake. If cookiePath is
// non-empty and readable, its raw contents are sent as the auth cookie;
// otherwise Dial falls back to anonymous auth (a zero-length cookie), which a
// PulseAudio server accepts when cookie authentication isn't required for the
// socket (e.g. a private per-sidecar instance nobody else can reach). The
// whole handshake is bounded by ctx — Dial always returns by ctx's deadline.
func Dial(ctx context.Context, socketPath string, cookiePath string) (*Client, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("pulse: dial %s: %w", socketPath, err)
	}
	c := &Client{conn: conn}

	if err := c.applyDeadline(ctx); err != nil {
		conn.Close()
		return nil, err
	}
	if err := c.auth(ctx, readCookie(cookiePath)); err != nil {
		conn.Close()
		return nil, fmt.Errorf("pulse: auth: %w", err)
	}
	if err := c.setClientName(ctx); err != nil {
		conn.Close()
		return nil, fmt.Errorf("pulse: set-client-name: %w", err)
	}
	return c, nil
}

func readCookie(path string) []byte {
	if path == "" {
		return nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return b
}

func (c *Client) applyDeadline(ctx context.Context) error {
	if dl, ok := ctx.Deadline(); ok {
		if err := c.conn.SetDeadline(dl); err != nil {
			return fmt.Errorf("pulse: set deadline: %w", err)
		}
		return nil
	}
	// No deadline on ctx: clear any previously-set deadline so this call
	// can't inherit a stale one from an earlier, shorter-lived ctx.
	return c.conn.SetDeadline(noDeadline)
}

// request sends one command with the given tagstruct-encoded arguments,
// waits for the matching reply, and returns its payload (everything after
// the reply's own command+tag prefix) — or an error decoded from a
// COMMAND_ERROR reply. Callers hold c.mu for the duration (single
// connection, single in-flight request).
func (c *Client) request(ctx context.Context, cmd command, args *tagWriter) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.applyDeadline(ctx); err != nil {
		return nil, err
	}

	c.tag++
	tag := c.tag
	payload := buildRequest(cmd, tag, args)

	if err := writePacket(c.conn, payload); err != nil {
		return nil, fmt.Errorf("pulse: %s: %w", cmd, err)
	}
	respPayload, err := readPacket(c.conn)
	if err != nil {
		return nil, fmt.Errorf("pulse: %s: %w", cmd, err)
	}

	r := newTagReader(respPayload)
	var rspCmd, rspTag uint32
	if err := r.getUint32(&rspCmd); err != nil {
		return nil, fmt.Errorf("pulse: %s: decode reply command: %w", cmd, err)
	}
	if err := r.getUint32(&rspTag); err != nil {
		return nil, fmt.Errorf("pulse: %s: decode reply tag: %w", cmd, err)
	}
	if rspTag != tag {
		return nil, fmt.Errorf("pulse: %s: reply tag mismatch: got %d want %d", cmd, rspTag, tag)
	}
	switch command(rspCmd) {
	case commandError:
		var code uint32
		if err := r.getUint32(&code); err != nil {
			return nil, fmt.Errorf("pulse: %s: decode error code: %w", cmd, err)
		}
		return nil, &ProtocolError{Code: code}
	case commandReply:
		return r.rest(), nil
	default:
		return nil, fmt.Errorf("pulse: %s: unexpected reply command %s", cmd, command(rspCmd))
	}
}

func (c *Client) auth(ctx context.Context, cookie []byte) error {
	var args tagWriter
	args.putUint32(protocolVersion)
	args.putArbitrary(cookie)
	resp, err := c.request(ctx, commandAuth, &args)
	if err != nil {
		return err
	}
	r := newTagReader(resp)
	var serverVersion uint32
	if err := r.getUint32(&serverVersion); err != nil {
		return fmt.Errorf("decode server version: %w", err)
	}
	_ = serverVersion & versionMask // negotiated version; unused beyond validating the reply decodes
	return nil
}

func (c *Client) setClientName(ctx context.Context) error {
	var args tagWriter
	args.putPropList(map[string]string{
		"application.name":       "omnipus",
		"application.process.id": fmt.Sprintf("%d", os.Getpid()),
	})
	resp, err := c.request(ctx, commandSetClientName, &args)
	if err != nil {
		return err
	}
	r := newTagReader(resp)
	var clientIndex uint32
	return r.getUint32(&clientIndex)
}

// LoadModule requests the PulseAudio server load the named module with the
// given argument string and returns the loaded module's index — e.g.
// LoadModule(ctx, "module-null-sink", "sink_name=vspk"). ctx bounds the
// round trip; LoadModule always returns by ctx's deadline.
func (c *Client) LoadModule(ctx context.Context, name string, args string) (uint32, error) {
	var w tagWriter
	w.putString(name)
	w.putString(args)
	resp, err := c.request(ctx, commandLoadModule, &w)
	if err != nil {
		return 0, fmt.Errorf("pulse: load-module %s: %w", name, err)
	}
	r := newTagReader(resp)
	var idx uint32
	if err := r.getUint32(&idx); err != nil {
		return 0, fmt.Errorf("pulse: load-module %s: decode index: %w", name, err)
	}
	return idx, nil
}

// Close closes the underlying connection. The Client is unusable afterward.
func (c *Client) Close() error {
	return c.conn.Close()
}

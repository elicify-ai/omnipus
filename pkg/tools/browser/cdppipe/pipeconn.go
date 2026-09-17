package cdppipe

import (
	"bufio"
	"context"
	"errors"
	"io"
	"sync"

	"github.com/chromedp/cdproto"
	"github.com/chromedp/chromedp"
	jsonv2 "github.com/go-json-experiment/json"
)

// pipeReadBufferSize is the initial bufio buffer for draining fd 4. It grows as
// needed for large messages (e.g. base64 screencast frames), so this is only a
// starting size, not a cap.
const pipeReadBufferSize = 64 * 1024

// PipeConn implements chromedp.Transport over a Chromium --remote-debugging-pipe
// connection: NUL-delimited JSON CDP frames written to the browser's fd 3 and
// read from the browser's fd 4.
//
// It carries NO network endpoint — no TCP port, no /json HTTP surface, no ws://
// URL. CDP is reachable only through the inherited file descriptors, which a
// co-tenant process does not possess. This is the property EC-3/CRIT-001 relies
// on (see doc.go).
//
// Write is safe for concurrent callers (frames are serialized under a mutex, so
// a message can never be interleaved on the wire). Read must be driven from a
// single goroutine, matching chromedp.Browser's single-reader run loop and the
// contract of the underlying bufio.Reader.
type PipeConn struct {
	wmu sync.Mutex    // serializes writeRaw so frames are never interleaved
	w   io.Writer     // parent-side writer feeding the browser's fd 3
	wc  io.Closer     // close handle for w
	br  *bufio.Reader // buffered reader draining the browser's fd 4
	rc  io.Closer     // close handle for the fd-4 reader

	closeOnce sync.Once
	closeErr  error

	dbgf func(string, ...any) // optional per-frame protocol logger
}

// Ensure PipeConn satisfies chromedp's transport contract at compile time.
var _ chromedp.Transport = (*PipeConn)(nil)

// NewPipeConn builds a PipeConn from the parent-side ends of the browser pipe:
//
//	out — writer feeding the browser's fd 3 (messages INTO the browser)
//	in  — reader draining the browser's fd 4 (messages OUT of the browser)
//
// dbgf, if non-nil, logs every frame in each direction (very verbose).
func NewPipeConn(out io.WriteCloser, in io.ReadCloser, dbgf func(string, ...any)) *PipeConn {
	return &PipeConn{
		w:    out,
		wc:   out,
		br:   bufio.NewReaderSize(in, pipeReadBufferSize),
		rc:   in,
		dbgf: dbgf,
	}
}

// writeRaw frames and writes an already-encoded CDP JSON payload. Safe for
// concurrent callers.
func (c *PipeConn) writeRaw(payload []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if c.dbgf != nil {
		c.dbgf("-> %s", payload)
	}
	return writeFrame(c.w, payload)
}

// readRaw reads the next NUL-delimited CDP JSON payload. Single-goroutine only.
func (c *PipeConn) readRaw() ([]byte, error) {
	payload, err := readFrame(c.br)
	if err != nil {
		return nil, err
	}
	if c.dbgf != nil {
		c.dbgf("<- %s", payload)
	}
	return payload, nil
}

// Write implements chromedp.Transport. It marshals msg with chromedp's own
// options (jsonv2, invalid-UTF-8 tolerant) so the wire bytes are byte-identical
// to what chromedp would send over a websocket.
func (c *PipeConn) Write(_ context.Context, msg *cdproto.Message) error {
	buf, err := jsonv2.Marshal(msg, chromedp.DefaultMarshalOptions)
	if err != nil {
		return err
	}
	return c.writeRaw(buf)
}

// Read implements chromedp.Transport, decoding the next frame into msg using
// chromedp's own unmarshal options.
func (c *PipeConn) Read(_ context.Context, msg *cdproto.Message) error {
	payload, err := c.readRaw()
	if err != nil {
		return err
	}
	return jsonv2.Unmarshal(payload, msg, chromedp.DefaultUnmarshalOptions)
}

// Close implements io.Closer, closing both pipe ends exactly once. Closing the
// writer (fd 3) is what makes Chrome observe EOF on its input and exit.
func (c *PipeConn) Close() error {
	c.closeOnce.Do(func() {
		var errs []error
		if c.wc != nil {
			if err := c.wc.Close(); err != nil {
				errs = append(errs, err)
			}
		}
		if c.rc != nil {
			if err := c.rc.Close(); err != nil {
				errs = append(errs, err)
			}
		}
		c.closeErr = errors.Join(errs...)
	})
	return c.closeErr
}

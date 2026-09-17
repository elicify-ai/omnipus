package cdppipe

import (
	"bufio"
	"bytes"
	"errors"
	"io"
)

// frameDelimiter is the NUL byte (0x00) that terminates every CDP message on
// Chromium's --remote-debugging-pipe transport. Chromium writes each JSON
// message followed by a single 0x00 on fd 4 and reads the same framing on fd 3.
//
// Threat/correctness note: a raw NUL never appears inside a well-formed JSON
// document — the JSON grammar requires control characters (including U+0000) to
// be escaped as  , so 0x00 is an unambiguous, injection-safe record
// separator. writeFrame enforces this invariant by rejecting any payload that
// already contains a NUL rather than silently corrupting the stream.
const frameDelimiter = 0x00

// errNULInPayload is returned when a caller attempts to frame a payload that
// already contains a NUL byte, which would corrupt message boundaries.
var errNULInPayload = errors.New("cdppipe: CDP payload contains a NUL byte; refusing to frame")

// writeFrame writes payload to w followed by the NUL delimiter. It fails closed
// on a payload that already contains a NUL byte.
func writeFrame(w io.Writer, payload []byte) error {
	if bytes.IndexByte(payload, frameDelimiter) >= 0 {
		return errNULInPayload
	}
	if _, err := w.Write(payload); err != nil {
		return err
	}
	if _, err := w.Write([]byte{frameDelimiter}); err != nil {
		return err
	}
	return nil
}

// readFrame reads a single NUL-delimited frame from r and returns the payload
// with the delimiter stripped.
//
// A clean end of stream at a frame boundary is reported as io.EOF. Bytes read
// without a terminating delimiter (a truncated final frame) are reported as
// io.ErrUnexpectedEOF, never returned as a silently-complete message.
func readFrame(r *bufio.Reader) ([]byte, error) {
	data, err := r.ReadBytes(frameDelimiter)
	if err != nil {
		if len(data) == 0 {
			return nil, err // clean boundary: typically io.EOF
		}
		if err == io.EOF {
			return nil, io.ErrUnexpectedEOF
		}
		return nil, err
	}
	return data[:len(data)-1], nil // strip the trailing delimiter
}

package mcp

// ingest_bound.go — ADR-066 D10 (FR-038): the MCP transport is bounded at
// `ingest_bound_bytes` PER JSON-RPC MESSAGE, enforced on the transport read
// rather than after the fact:
//
//   - stdio: boundedMessageReader wraps the child's stdout. It counts bytes
//     since the last newline (the JSON-RPC line delimiter) and fails the read
//     the moment one message passes the bound. The SDK's decoder therefore
//     never buffers more than bound+1 bytes of a runaway message.
//   - HTTP/SSE: ingestBoundTransport wraps every POST response body in
//     http.MaxBytesReader. (The standalone GET notification stream is a
//     long-lived cumulative stream and is deliberately NOT bounded — a
//     per-message bound has no meaning for it.)
//
// Both record the violation on the server's ingestGuard so Manager.CallTool
// can turn the SDK's generic "connection closed" into a failure that names
// the bound.

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
)

// IngestBoundError is the tool failure for a message larger than the bound.
type IngestBoundError struct {
	Server string
	Bound  int64
}

func (e *IngestBoundError) Error() string {
	return fmt.Sprintf("MCP server %q sent a message larger than the ingest bound of %d bytes "+
		"(ingest_bound_bytes); the read was aborted on the transport, not truncated", e.Server, e.Bound)
}

// ingestGuard is shared by a server's transport and its ServerConnection.
type ingestGuard struct {
	server   string
	bound    int64
	exceeded atomic.Bool
}

func newIngestGuard(server string, bound int64) *ingestGuard {
	return &ingestGuard{server: server, bound: bound}
}

func (g *ingestGuard) err() error {
	return &IngestBoundError{Server: g.server, Bound: g.bound}
}

// trip records the violation and returns the typed error.
func (g *ingestGuard) trip() error {
	g.exceeded.Store(true)
	return g.err()
}

// boundedMessageReader bounds each newline-delimited message read from r.
type boundedMessageReader struct {
	r     io.Reader
	guard *ingestGuard
	inMsg int64 // bytes of the current message seen so far (excluding the delimiter)
}

func (b *boundedMessageReader) Read(p []byte) (int, error) {
	if b.guard.exceeded.Load() {
		return 0, b.guard.err()
	}
	// Never pull more than one byte past the bound for the current message,
	// so the abort happens at bound+1 regardless of the caller's buffer.
	if room := b.guard.bound - b.inMsg + 1; room < int64(len(p)) {
		p = p[:room]
	}
	n, err := b.r.Read(p)
	for _, c := range p[:n] {
		if c == '\n' {
			b.inMsg = 0
			continue
		}
		b.inMsg++
		if b.inMsg > b.guard.bound {
			return 0, b.guard.trip()
		}
	}
	return n, err
}

// ingestBoundTransport bounds POST response bodies with http.MaxBytesReader.
type ingestBoundTransport struct {
	base  http.RoundTripper
	guard *ingestGuard
}

func (t *ingestBoundTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil || resp == nil || resp.Body == nil || req.Method != http.MethodPost {
		return resp, err
	}
	resp.Body = &boundedBody{
		ReadCloser: http.MaxBytesReader(nil, resp.Body, t.guard.bound),
		guard:      t.guard,
	}
	return resp, nil
}

// boundedBody translates MaxBytesReader's limit error into the typed
// IngestBoundError and records it on the guard.
type boundedBody struct {
	io.ReadCloser
	guard *ingestGuard
}

func (b *boundedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	var mbe *http.MaxBytesError
	if err != nil && errors.As(err, &mbe) {
		return n, b.guard.trip()
	}
	return n, err
}

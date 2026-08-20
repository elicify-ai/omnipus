package cdppipe

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chromedp/cdproto"
	"github.com/go-json-experiment/json/jsontext"
)

// newLinkedPipeConns builds two PipeConns wired to each other through a pair of
// os.Pipe channels, simulating chromedp (client) and Chrome (server) over the
// real NUL-delimited framing without launching a browser.
func newLinkedPipeConns(t *testing.T) (client, server *PipeConn, closeAll func()) {
	t.Helper()
	aR, aW, err := os.Pipe() // client -> server
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	bR, bW, err := os.Pipe() // server -> client
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	client = NewPipeConn(aW, bR, nil) // writes to server via aW, reads from server via bR
	server = NewPipeConn(bW, aR, nil) // writes to client via bW, reads from client via aR
	closeAll = func() {
		client.Close()
		server.Close()
	}
	return client, server, closeAll
}

// TestPipeConn_FramingRoundTrip streams a sequence of representative CDP
// messages — commands, results, an event (no id), a session-scoped message, and
// an error — back-to-back through the pipe and verifies each decodes intact on
// the far end, proving frame boundaries and field fidelity.
func TestPipeConn_FramingRoundTrip(t *testing.T) {
	msgs := []*cdproto.Message{
		{ID: 1, Method: "Page.navigate", Params: jsontext.Value(`{"url":"https://example.com"}`)},
		{ID: 1, Result: jsontext.Value(`{"frameId":"F1","loaderId":"L1"}`)},
		// Event: has Method, no ID — the shape a Page.screencastFrame arrives as.
		{Method: "Page.screencastFrame", SessionID: "S1", Params: jsontext.Value(`{"data":"QUFBQQ==","sessionId":7}`)},
		{
			ID:        42,
			SessionID: "S2",
			Method:    "Input.dispatchMouseEvent",
			Params:    jsontext.Value(`{"type":"mousePressed","x":10,"y":20}`),
		},
		{ID: 7, Error: &cdproto.Error{Code: -32000, Message: "boom"}},
	}

	client, server, closeAll := newLinkedPipeConns(t)
	defer closeAll()

	go func() {
		for _, m := range msgs {
			if err := client.Write(context.Background(), m); err != nil {
				t.Errorf("write: %v", err)
				return
			}
		}
	}()

	for i, want := range msgs {
		var got cdproto.Message
		if err := server.Read(context.Background(), &got); err != nil {
			t.Fatalf("read msg %d: %v", i, err)
		}
		assertMessageEqual(t, i, want, &got)
	}
}

// TestPipeConn_ConcurrentRequests fires many concurrent request writes at a
// single fake-Chrome responder and confirms each response is matched back to its
// request by id, while interleaved events are dispatched to the event path. This
// exercises the write mutex (frame atomicity under concurrency), request/response
// id matching, and event dispatch — all over the real framing.
func TestPipeConn_ConcurrentRequests(t *testing.T) {
	const n = 64

	client, server, closeAll := newLinkedPipeConns(t)
	defer closeAll()

	var wg sync.WaitGroup

	// Fake Chrome: read each request, echo a result with the same id, and emit
	// an interleaved event after every request.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			var req cdproto.Message
			if err := server.Read(context.Background(), &req); err != nil {
				return // pipe closed at end of test
			}
			if req.ID == 0 {
				continue
			}
			resp := &cdproto.Message{ID: req.ID, Result: jsontext.Value(fmt.Sprintf(`{"echo":%d}`, req.ID))}
			if err := server.Write(context.Background(), resp); err != nil {
				return
			}
			ev := &cdproto.Message{Method: "Test.tick", Params: jsontext.Value(`{}`)}
			if err := server.Write(context.Background(), ev); err != nil {
				return
			}
		}
	}()

	// Client demux: single reader routes responses by id and counts events.
	var (
		mu       sync.Mutex
		pending  = make(map[int64]chan *cdproto.Message)
		eventCnt int64
		readerWG sync.WaitGroup
	)
	register := func(id int64) chan *cdproto.Message {
		ch := make(chan *cdproto.Message, 1)
		mu.Lock()
		pending[id] = ch
		mu.Unlock()
		return ch
	}
	readerWG.Add(1)
	go func() {
		defer readerWG.Done()
		for {
			msg := new(cdproto.Message)
			if err := client.Read(context.Background(), msg); err != nil {
				return
			}
			if msg.ID != 0 {
				mu.Lock()
				ch := pending[msg.ID]
				delete(pending, msg.ID)
				mu.Unlock()
				if ch != nil {
					ch <- msg
				}
				continue
			}
			if msg.Method != "" {
				atomic.AddInt64(&eventCnt, 1)
			}
		}
	}()

	// Concurrent request senders, each with a unique id.
	var reqWG sync.WaitGroup
	errs := make(chan error, n)
	for i := 1; i <= n; i++ {
		reqWG.Add(1)
		go func(id int64) {
			defer reqWG.Done()
			ch := register(id)
			req := &cdproto.Message{ID: id, Method: "Test.echo", Params: jsontext.Value(`{}`)}
			if err := client.Write(context.Background(), req); err != nil {
				errs <- fmt.Errorf("id %d write: %w", id, err)
				return
			}
			select {
			case resp := <-ch:
				if resp.ID != id {
					errs <- fmt.Errorf("id %d got response id %d", id, resp.ID)
					return
				}
				var body struct {
					Echo int64 `json:"echo"`
				}
				if err := json.Unmarshal(resp.Result, &body); err != nil {
					errs <- fmt.Errorf("id %d bad result %q: %w", id, resp.Result, err)
					return
				}
				if body.Echo != id {
					errs <- fmt.Errorf("id %d echo mismatch: got %d", id, body.Echo)
				}
			case <-time.After(10 * time.Second):
				errs <- fmt.Errorf("id %d timed out waiting for response", id)
			}
		}(int64(i))
	}

	reqWG.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	// Tear down the pipes so the reader + fake-Chrome goroutines exit.
	closeAll()
	readerWG.Wait()
	wg.Wait()

	if got := atomic.LoadInt64(&eventCnt); got == 0 {
		t.Errorf("expected interleaved events to be dispatched, got %d", got)
	}
}

// TestFrame_RoundTripAndNULRejection covers the low-level NUL framing directly:
// a multi-frame round trip, a clean EOF at a boundary, a truncated final frame,
// and rejection of a payload containing a NUL byte.
func TestFrame_RoundTripAndNULRejection(t *testing.T) {
	payloads := [][]byte{
		[]byte(`{"id":1}`),
		[]byte(`{"method":"A.b","params":{"k":"v"}}`),
		[]byte(``), // empty payload frames cleanly
		[]byte(`{"id":2,"result":{"x":true}}`),
	}

	var buf bytes.Buffer
	for _, p := range payloads {
		if err := writeFrame(&buf, p); err != nil {
			t.Fatalf("writeFrame(%q): %v", p, err)
		}
	}

	r := bufio.NewReader(bytes.NewReader(buf.Bytes()))
	for i, want := range payloads {
		got, err := readFrame(r)
		if err != nil {
			t.Fatalf("readFrame %d: %v", i, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("readFrame %d = %q, want %q", i, got, want)
		}
	}
	// Clean EOF at the boundary.
	if _, err := readFrame(r); err != io.EOF {
		t.Fatalf("readFrame at EOF = %v, want io.EOF", err)
	}

	// Truncated final frame (no delimiter) => ErrUnexpectedEOF.
	tr := bufio.NewReader(bytes.NewReader([]byte(`{"id":9}`)))
	if _, err := readFrame(tr); err != io.ErrUnexpectedEOF {
		t.Fatalf("truncated readFrame = %v, want io.ErrUnexpectedEOF", err)
	}

	// A payload with an embedded NUL must be rejected, not silently corrupt framing.
	if err := writeFrame(io.Discard, []byte("a\x00b")); err != errNULInPayload {
		t.Fatalf("writeFrame(NUL payload) = %v, want errNULInPayload", err)
	}
}

func assertMessageEqual(t *testing.T, idx int, want, got *cdproto.Message) {
	t.Helper()
	if got.ID != want.ID {
		t.Errorf("msg %d: ID = %d, want %d", idx, got.ID, want.ID)
	}
	if got.SessionID != want.SessionID {
		t.Errorf("msg %d: SessionID = %q, want %q", idx, got.SessionID, want.SessionID)
	}
	if got.Method != want.Method {
		t.Errorf("msg %d: Method = %q, want %q", idx, got.Method, want.Method)
	}
	if !jsonEqual(t, want.Params, got.Params) {
		t.Errorf("msg %d: Params = %q, want %q", idx, got.Params, want.Params)
	}
	if !jsonEqual(t, want.Result, got.Result) {
		t.Errorf("msg %d: Result = %q, want %q", idx, got.Result, want.Result)
	}
	switch {
	case want.Error == nil && got.Error != nil:
		t.Errorf("msg %d: unexpected Error %+v", idx, got.Error)
	case want.Error != nil && got.Error == nil:
		t.Errorf("msg %d: missing Error, want %+v", idx, want.Error)
	case want.Error != nil && got.Error != nil:
		if got.Error.Code != want.Error.Code || got.Error.Message != want.Error.Message {
			t.Errorf("msg %d: Error = %+v, want %+v", idx, got.Error, want.Error)
		}
	}
}

// jsonEqual compares two raw JSON byte slices for semantic equality (whitespace
// and key order insensitive). Empty on both sides is equal.
func jsonEqual(t *testing.T, a, b []byte) bool {
	t.Helper()
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	var av, bv any
	if len(a) > 0 {
		if err := json.Unmarshal(a, &av); err != nil {
			t.Fatalf("invalid json %q: %v", a, err)
		}
	}
	if len(b) > 0 {
		if err := json.Unmarshal(b, &bv); err != nil {
			t.Fatalf("invalid json %q: %v", b, err)
		}
	}
	return reflect.DeepEqual(av, bv)
}

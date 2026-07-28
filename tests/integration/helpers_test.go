package integration

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
)

// mockLLMServer mirrors tests/perf/mock_openrouter_test.go::mockOpenRouterServer.
// Returns an httptest.Server that responds to POST /chat/completions with a
// deterministic streaming response. Registered for cleanup automatically.
func mockLLMServer(tb testing.TB, replyText string) *httptest.Server {
	tb.Helper()
	if replyText == "" {
		replyText = "integration test deterministic reply"
	}
	var requests atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.NotFound(w, r)
			return
		}
		var body struct {
			Stream bool `json:"stream"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Stream {
			writeMockStream(w, replyText)
			return
		}
		writeMockJSON(w, replyText)
	}))
	tb.Cleanup(srv.Close)
	return srv
}

func writeMockJSON(w http.ResponseWriter, content string) {
	w.Header().Set("Content-Type", "application/json")
	resp := map[string]any{
		"id":      "mock-cmpl-1",
		"object":  "chat.completion",
		"model":   "mock-model",
		"created": 1700000000,
		"choices": []map[string]any{{
			"index": 0,
			"message": map[string]any{
				"role":    "assistant",
				"content": content,
			},
			"finish_reason": "stop",
		}},
		"usage": map[string]any{
			"prompt_tokens":     1,
			"completion_tokens": len(strings.Fields(content)),
			"total_tokens":      1 + len(strings.Fields(content)),
		},
	}
	_ = json.NewEncoder(w).Encode(resp)
}

func writeMockStream(w http.ResponseWriter, content string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, _ := w.(http.Flusher)

	const chunkSize = 16
	for i := 0; i < len(content); i += chunkSize {
		end := i + chunkSize
		if end > len(content) {
			end = len(content)
		}
		chunk := map[string]any{
			"id": "mock-cmpl-1", "object": "chat.completion.chunk",
			"model": "mock-model", "created": 1700000000,
			"choices": []map[string]any{{
				"index": 0,
				"delta": map[string]any{"content": content[i:end]},
			}},
		}
		data, _ := json.Marshal(chunk)
		fmt.Fprintf(w, "data: %s\n\n", data)
		if flusher != nil {
			flusher.Flush()
		}
	}

	finalChunk := map[string]any{
		"id": "mock-cmpl-1", "object": "chat.completion.chunk",
		"model": "mock-model", "created": 1700000000,
		"choices": []map[string]any{{
			"index": 0, "delta": map[string]any{},
			"finish_reason": "stop",
		}},
		"usage": map[string]any{
			"prompt_tokens": 1, "completion_tokens": len(strings.Fields(content)),
			"total_tokens": 1 + len(strings.Fields(content)),
		},
	}
	data, _ := json.Marshal(finalChunk)
	fmt.Fprintf(w, "data: %s\n\n", data)
	fmt.Fprintf(w, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}

// startIntegrationGateway boots a full gateway backed by a mock LLM and returns
// the TestGateway. Uses testutil.StartTestGateway + WithAPIBase so the full
// gateway/agent pipeline runs but no real OpenRouter key is needed.
//
// WithBearerAuth is included so that REST routes wrapped with withAuth pass the
// CSRF middleware (which exempts requests that carry an Authorization: Bearer
// header) and the checkBearerAuth gate. Tests that POST/PUT state-changing
// endpoints would otherwise receive 403 "csrf cookie missing" when DevModeBypass
// is true and no bearer header is present.
func startIntegrationGateway(t *testing.T) *testutil.TestGateway {
	t.Helper()
	mock := mockLLMServer(t, "")
	return testutil.StartTestGateway(t, testutil.WithAPIBase(mock.URL), testutil.WithBearerAuth())
}

// wsConnect dials the gateway's WS endpoint and sends the mandatory auth frame.
// Returns the open connection, which the caller must Close when done.
// Traces to: Bug-3 (concurrent sessions), Bug-5 (replay ordering).
func wsConnect(tb testing.TB, gw *testutil.TestGateway) *websocket.Conn {
	tb.Helper()
	wsURL := strings.Replace(gw.URL, "http://", "ws://", 1) + "/api/v1/chat/ws"
	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	header := http.Header{}
	header.Set("Origin", gw.URL)

	conn, resp, err := dialer.Dial(wsURL, header)
	if err != nil {
		if resp != nil {
			_ = resp.Body.Close()
		}
		tb.Fatalf("wsConnect: dial %s: %v", wsURL, err)
	}
	tb.Cleanup(func() { _ = conn.Close() })

	// Gateway requires an auth frame before any other message.
	// Use gw.Token() so the frame carries the real bearer token when the
	// gateway was started with WithBearerAuth (OMNIPUS_BEARER_TOKEN must
	// constant-time-equal the auth frame token). When Token() is empty the
	// gateway is in DevModeBypass mode and accepts any non-empty value.
	tok := gw.Token()
	if tok == "" {
		tok = "dev-token"
	}
	authFrame := fmt.Sprintf(`{"type":"auth","token":%s}`, jsonQuote(tok))
	if err := conn.WriteMessage(websocket.TextMessage, []byte(authFrame)); err != nil {
		tb.Fatalf("wsConnect: send auth frame: %v", err)
	}
	return conn
}

// sendMessage sends a "message" frame over conn and returns immediately.
// When sessionID is non-empty it is included in the frame so the gateway
// continues an existing session rather than minting a fresh one for each
// message (a fresh session would silently lose mid-turn handoff state).
//
// MUST be called from the test goroutine only. It calls tb.Fatalf, and Go's
// testing contract forbids FailNow (and therefore Fatalf) from any goroutine
// other than the one running the test. From a spawned goroutine, use
// sendMessageErr and report the error back to the test goroutine.
//
// Traces to: Bug-3 (concurrent sessions).
func sendMessage(tb testing.TB, conn *websocket.Conn, content string, sessionID ...string) {
	tb.Helper()
	if err := sendMessageErr(conn, content, sessionID...); err != nil {
		tb.Fatalf("sendMessage: %v", err)
	}
}

// sendMessageErr is the goroutine-safe form of sendMessage: it returns the
// write error instead of calling tb.Fatalf, so it is legal to call from a
// goroutine other than the test's own.
//
// Traces to: Bug-3 (concurrent sessions).
func sendMessageErr(conn *websocket.Conn, content string, sessionID ...string) error {
	var frame string
	if len(sessionID) > 0 && sessionID[0] != "" {
		frame = fmt.Sprintf(`{"type":"message","content":%s,"session_id":%s}`,
			jsonQuote(content), jsonQuote(sessionID[0]))
	} else {
		frame = fmt.Sprintf(`{"type":"message","content":%s}`, jsonQuote(content))
	}
	return conn.WriteMessage(websocket.TextMessage, []byte(frame))
}

// extractSessionID scans frames for the first session_started frame and
// returns its session_id. Used by tests that need to thread session_id
// through subsequent sendMessage calls so they target the SAME session
// rather than each minting a fresh one.
func extractSessionID(frames []map[string]any) string {
	for _, f := range frames {
		if tp, _ := f["type"].(string); tp == "session_started" {
			if sid, _ := f["session_id"].(string); sid != "" {
				return sid
			}
		}
	}
	return ""
}

// wsWaitError is the classified failure of waitForFirstTokenErr. Its whole
// reason to exist is to keep two failure modes apart that a concurrency test
// MUST NOT confuse:
//
//   - ReadTimeout=true  — the read deadline expired while the connection was
//     still healthy. Nothing arrived. This is the evidence a starvation
//     assertion is entitled to rely on.
//   - ReadTimeout=false — the transport itself broke (close, reset, EOF,
//     protocol error). This says NOTHING about starvation: the connection
//     died before the agent ever got the chance to reply. A test that reports
//     this as a starvation bug sends its reader hunting for a concurrency bug
//     that does not exist.
//
// FramesSeen records the non-assistant frames read before the failure, which
// further separates "the session was never even acknowledged" from "the
// session started but no token ever came".
type wsWaitError struct {
	ReadTimeout bool
	Waited      time.Duration
	FramesSeen  []string
	Err         error
}

func (e *wsWaitError) Error() string {
	kind := "TRANSPORT ERROR — the WebSocket broke (this is NOT evidence of starvation)"
	if e.ReadTimeout {
		kind = "READ TIMEOUT — connection stayed healthy, no assistant frame arrived"
	}
	return fmt.Sprintf("%s after %v: %v (non-assistant frames seen first: %v)",
		kind, e.Waited, e.Err, e.FramesSeen)
}

// isReadDeadlineExceeded reports whether err is this connection's own read
// deadline expiring, as opposed to the transport failing. A deadline expiry
// surfaces as os.ErrDeadlineExceeded / a net.Error with Timeout()==true;
// close, reset and EOF errors (including *websocket.CloseError) do not
// implement that, so they correctly classify as transport failures.
func isReadDeadlineExceeded(err error) bool {
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}
	var ne net.Error
	if errors.As(err, &ne) {
		return ne.Timeout()
	}
	return false
}

// waitForFirstTokenErr reads frames from conn until it sees a token/done/content
// frame (indicating the LLM responded) or the deadline is reached, returning a
// classified *wsWaitError rather than calling tb.Fatalf. That makes it legal to
// call from a goroutine other than the test's own: a Fatalf runs runtime.Goexit
// on the CALLING goroutine, so a collector goroutine would die before it could
// deliver its result and the waiting test would report whatever its own timeout
// branch says — historically, a transport error was announced as same-agent
// starvation.
//
// The concrete *wsWaitError return (rather than error) is deliberate: it keeps
// callers from tripping the typed-nil interface trap on the success path.
//
// Traces to: Bug-3 (concurrent sessions).
func waitForFirstTokenErr(conn *websocket.Conn, timeout time.Duration) (string, *wsWaitError) {
	deadline := time.Now().Add(timeout)
	var seen []string
	for {
		_ = conn.SetReadDeadline(deadline)
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return "", &wsWaitError{
				ReadTimeout: isReadDeadlineExceeded(err),
				Waited:      timeout,
				FramesSeen:  seen,
				Err:         err,
			}
		}
		var frame struct {
			Type string `json:"type"`
		}
		if jsonErr := json.Unmarshal(msg, &frame); jsonErr != nil {
			continue
		}
		switch frame.Type {
		case "session_started", "session_state", "status":
			seen = append(seen, frame.Type)
			continue
		case "token", "content", "text", "assistant_message", "done":
			return frame.Type, nil
		}
	}
}

// wsReply is one collector goroutine's outcome, delivered back to the test
// goroutine so that IT — never the collector — decides what the failure means
// and calls Fatalf. waitErr==nil means frameType holds the frame that arrived.
type wsReply struct {
	idx       int
	frameType string
	waitErr   *wsWaitError
}

// collectFirstToken runs waitForFirstTokenErr and delivers the classified
// outcome to out. It never touches testing.T, so it is safe in a goroutine.
// out must be buffered enough that this never blocks after the test returns.
func collectFirstToken(out chan<- wsReply, idx int, conn *websocket.Conn, timeout time.Duration) {
	ft, waitErr := waitForFirstTokenErr(conn, timeout)
	out <- wsReply{idx: idx, frameType: ft, waitErr: waitErr}
}

// collectAllFrames reads all frames from conn until the connection closes or
// timeout is reached. Returns decoded frames as generic maps.
// Traces to: Bug-5 (replay ordering).
func collectAllFrames(tb testing.TB, conn *websocket.Conn, timeout time.Duration) []map[string]any {
	tb.Helper()
	var frames []map[string]any
	deadline := time.Now().Add(timeout)
	for {
		_ = conn.SetReadDeadline(deadline)
		_, msg, err := conn.ReadMessage()
		if err != nil {
			// Deadline or close — stop collecting.
			break
		}
		var f map[string]any
		if jsonErr := json.Unmarshal(msg, &f); jsonErr == nil {
			frames = append(frames, f)
		}
	}
	return frames
}

// slowMockLLMServer returns an httptest.Server whose handler waits delay
// before returning the LLM response. This simulates a slow provider so
// concurrency tests can measure wall-clock time to prove that sessions run
// in parallel rather than sequentially.
//
// The delay applies only to POST /chat/completions — other endpoints (e.g.,
// /models probe) are answered immediately with 404 as usual.
func slowMockLLMServer(tb testing.TB, replyText string, delay time.Duration) *httptest.Server {
	tb.Helper()
	if replyText == "" {
		replyText = "slow integration test reply"
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.NotFound(w, r)
			return
		}
		// Block for the configured delay to simulate a slow LLM.
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return
		}
		var body struct {
			Stream bool `json:"stream"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Stream {
			writeMockStream(w, replyText)
			return
		}
		writeMockJSON(w, replyText)
	}))
	tb.Cleanup(srv.Close)
	return srv
}

// startSlowIntegrationGateway boots a gateway backed by a slow mock LLM that
// delays each LLM call by delay. Used for timing-based concurrency proofs.
func startSlowIntegrationGateway(t *testing.T, delay time.Duration) *testutil.TestGateway {
	t.Helper()
	mock := slowMockLLMServer(t, "", delay)
	return testutil.StartTestGateway(t, testutil.WithAPIBase(mock.URL), testutil.WithBearerAuth())
}

// jsonQuote returns a JSON-encoded string literal, e.g. `"hello world"`.
func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

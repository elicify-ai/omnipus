//go:build !cgo

package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/dapicom-ai/omnipus/pkg/agent/testutil"
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
// Traces to: Bug-3 (concurrent sessions).
func sendMessage(tb testing.TB, conn *websocket.Conn, content string) {
	tb.Helper()
	frame := fmt.Sprintf(`{"type":"message","content":%s}`, jsonQuote(content))
	if err := conn.WriteMessage(websocket.TextMessage, []byte(frame)); err != nil {
		tb.Fatalf("sendMessage: %v", err)
	}
}

// waitForFirstToken reads frames from conn until it sees a token/done/content
// frame (indicating the LLM responded) or the deadline is reached.
// Returns the first assistant frame type received.
// Traces to: Bug-3 (concurrent sessions).
func waitForFirstToken(tb testing.TB, conn *websocket.Conn, timeout time.Duration) string {
	tb.Helper()
	deadline := time.Now().Add(timeout)
	for {
		_ = conn.SetReadDeadline(deadline)
		_, msg, err := conn.ReadMessage()
		if err != nil {
			tb.Fatalf("waitForFirstToken: read error (deadline=%v): %v", timeout, err)
		}
		var frame struct {
			Type string `json:"type"`
		}
		if jsonErr := json.Unmarshal(msg, &frame); jsonErr != nil {
			continue
		}
		switch frame.Type {
		case "session_started", "session_state", "status":
			continue
		case "token", "content", "text", "assistant_message", "done":
			return frame.Type
		}
	}
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

//go:build !cgo

// This test file uses //go:build !cgo so it compiles when CGO is disabled.

package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// testConfig returns a config suitable for SSE handler tests.
// When OMNIPUS_BEARER_TOKEN is set to "", the auth check falls back to the env var
// (which returns AuthResult{Authenticated: true, Role: admin} per auth.go).
func testConfig() *config.Config {
	return &config.Config{
		Gateway: config.GatewayConfig{
			Users:         nil, // no per-user list → falls back to env var
			Token:         "",
			DevModeBypass: true, // allow unauthenticated access in test mode
		},
	}
}

// captureWriteHeaderRecorder wraps httptest.ResponseRecorder to signal when
// WriteHeader is called. This allows SSE streaming tests to know when the
// response status and headers have been committed without polling.
type captureWriteHeaderRecorder struct {
	*httptest.ResponseRecorder
	mu            sync.Mutex
	headerWritten chan struct{}
	once          sync.Once
}

func newCaptureRecorder() *captureWriteHeaderRecorder {
	return &captureWriteHeaderRecorder{
		ResponseRecorder: httptest.NewRecorder(),
		headerWritten:    make(chan struct{}),
	}
}

func (r *captureWriteHeaderRecorder) WriteHeader(code int) {
	r.ResponseRecorder.WriteHeader(code)
	r.once.Do(func() { close(r.headerWritten) })
}

// Flush implements http.Flusher so the SSE handler does not return 500
// "streaming not supported".
func (r *captureWriteHeaderRecorder) Flush() {
	r.mu.Lock()
	defer r.mu.Unlock()
}

// --- E2: SSE handler tests ---

// TestSSEHandlerOPTIONS verifies that OPTIONS returns 204 with CORS headers.
// BDD: Given a browser CORS preflight,
// When OPTIONS /api/v1/chat is sent,
// Then 204 No Content with Access-Control-Allow-Origin header.
// Traces to: wave5a-wire-ui-spec.md — Scenario: SSE CORS preflight (E2)
func TestSSEHandlerOPTIONS(t *testing.T) {
	os.Unsetenv("OMNIPUS_BEARER_TOKEN")
	msgBus := bus.NewMessageBus()
	h := newSSEHandler(msgBus, nil, "http://localhost:3000", testConfig)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodOptions, "/api/v1/chat", nil)
	h.ServeHTTP(w, r)

	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.NotEmpty(t, w.Header().Get("Access-Control-Allow-Origin"),
		"OPTIONS must set Access-Control-Allow-Origin")
	assert.NotEmpty(t, w.Header().Get("Access-Control-Allow-Methods"),
		"OPTIONS must set Access-Control-Allow-Methods")
}

// TestSSEHandlerGET verifies that GET returns 405 Method Not Allowed.
// BDD: Given a GET request,
// When GET /api/v1/chat is sent,
// Then 405 Method Not Allowed.
// Traces to: wave5a-wire-ui-spec.md — Scenario: SSE method validation (E2)
func TestSSEHandlerGET(t *testing.T) {
	os.Unsetenv("OMNIPUS_BEARER_TOKEN")
	msgBus := bus.NewMessageBus()
	h := newSSEHandler(msgBus, nil, "", testConfig)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/chat", nil)
	h.ServeHTTP(w, r)

	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

// TestSSEHandlerPOSTEmptyMessage verifies that POST with empty message returns 400.
// BDD: Given a POST with an empty message field,
// When POST /api/v1/chat {"message":""} is sent,
// Then 400 Bad Request.
// Traces to: wave5a-wire-ui-spec.md — Scenario: SSE empty message rejected (E2)
func TestSSEHandlerPOSTEmptyMessage(t *testing.T) {
	os.Unsetenv("OMNIPUS_BEARER_TOKEN")
	msgBus := bus.NewMessageBus()
	h := newSSEHandler(msgBus, nil, "", testConfig)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/chat", strings.NewReader(`{"message":""}`))
	r.Header.Set("Content-Type", "application/json")
	// Dev mode requires "Bearer " prefix; token value is ignored when OMNIPUS_BEARER_TOKEN is unset.
	r.Header.Set("Authorization", "Bearer dev-token")
	h.ServeHTTP(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestSSEHandlerPOSTInvalidJSON verifies that POST with invalid JSON body returns 400.
// Traces to: wave5a-wire-ui-spec.md — Scenario: SSE invalid JSON (E2)
func TestSSEHandlerPOSTInvalidJSON(t *testing.T) {
	os.Unsetenv("OMNIPUS_BEARER_TOKEN")
	msgBus := bus.NewMessageBus()
	h := newSSEHandler(msgBus, nil, "", testConfig)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/chat", strings.NewReader(`{bad json`))
	r.Header.Set("Content-Type", "application/json")
	// Dev mode requires "Bearer " prefix; token value is ignored when OMNIPUS_BEARER_TOKEN is unset.
	r.Header.Set("Authorization", "Bearer dev-token")
	h.ServeHTTP(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestSSEHandlerAuthMissingToken verifies that with OMNIPUS_BEARER_TOKEN set,
// a POST without Authorization returns 401.
// BDD: Given OMNIPUS_BEARER_TOKEN is set,
// When POST /api/v1/chat is sent without Authorization header,
// Then 401 Unauthorized.
// Traces to: wave5a-wire-ui-spec.md — Scenario: SSE auth required (E2)
func TestSSEHandlerAuthMissingToken(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "sse-test-secret")
	msgBus := bus.NewMessageBus()

	// CRITICAL: this test MUST use a config with DevModeBypass=false. With the
	// shared testConfig() (DevModeBypass=true), checkBearerAuth's dev-bypass
	// short-circuit (auth.go:64 — "no Bearer header AND bypass" → authenticated
	// as admin) fires BEFORE the OMNIPUS_BEARER_TOKEN check, so the request is
	// authenticated despite the missing header. The handler then proceeds past
	// the auth gate into the streaming select (sse.go:210) and BLOCKS FOREVER —
	// no context cancel, no bus producer — which is exactly what wedged the
	// serial test phase and tripped the 10-min pkg/gateway package timeout.
	// With bypass off, the auth gate correctly returns 401 and the handler
	// returns immediately, so we test the real auth-required path and never
	// reach the stream.
	noBypassCfg := func() *config.Config {
		return &config.Config{
			Gateway: config.GatewayConfig{
				Users:         nil,
				Token:         "",
				DevModeBypass: false,
			},
		}
	}
	h := newSSEHandler(msgBus, nil, "", noBypassCfg)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/chat", strings.NewReader(`{"message":"hello"}`))
	r.Header.Set("Content-Type", "application/json")
	// No Authorization header.
	h.ServeHTTP(w, r)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestSSEHandlerPOSTValidBody verifies that POST with a valid message body
// returns 200 with Content-Type text/event-stream.
// BDD: Given a valid POST body,
// When POST /api/v1/chat {"message":"hello"} is sent,
// Then 200 OK with Content-Type: text/event-stream.
// Traces to: wave5a-wire-ui-spec.md — Scenario: SSE streaming starts on valid message (E2)
func TestSSEHandlerPOSTValidBody(t *testing.T) {
	os.Unsetenv("OMNIPUS_BEARER_TOKEN")
	msgBus := bus.NewMessageBus()
	h := newSSEHandler(msgBus, nil, "http://localhost:3000", testConfig)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	body := strings.NewReader(`{"message":"hello sse"}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "/api/v1/chat", body)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	// Dev mode requires "Bearer " prefix; token value is ignored when OMNIPUS_BEARER_TOKEN is unset.
	req.Header.Set("Authorization", "Bearer dev-token")

	w := newCaptureRecorder()

	// Run the SSE handler in a goroutine; it will block at the streaming select.
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.ServeHTTP(w, req)
	}()

	// Wait for the handler to commit the response headers (WriteHeader(200)).
	select {
	case <-w.headerWritten:
		// Headers written — cancel context to stop the streaming loop.
		cancel()
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("SSE handler did not write response headers within 2 seconds")
	}

	// Wait for handler goroutine to finish.
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("SSE handler did not stop after context cancel")
	}

	assert.Equal(t, http.StatusOK, w.Code,
		"valid POST must return 200")
	assert.Equal(t, "text/event-stream", w.Header().Get("Content-Type"),
		"valid POST must set Content-Type: text/event-stream")
}

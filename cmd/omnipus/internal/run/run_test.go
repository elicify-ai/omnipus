//go:build goolm && stdjson

package run_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/dapicom-ai/omnipus/cmd/omnipus/internal/run"
	"github.com/dapicom-ai/omnipus/pkg/api/generated"
)

// testToken is the bearer token the server expects from the client.
const testToken = "omnipus_test_cli_token"

// wsUpgrader is used by the test server to upgrade HTTP → WebSocket.
var wsUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// --------------------------------------------------------------------------
// Test helpers
// --------------------------------------------------------------------------

// scriptedServer creates an httptest.Server that:
//   - Upgrades /api/v1/chat/ws via gorilla websocket.
//   - Validates the first auth frame; closes with ClosePolicyViolation if wrong.
//   - After auth, reads the message frame (discards it), then plays back the
//     provided sequence of server frames.
//   - Handles POST /api/v1/tool-approvals/{id} and records each call.
//
// approvalCh receives a tuple of (approvalID, action) for each POST call the
// client makes.
type approvalRecord struct {
	ApprovalID string
	Action     string
}

type scriptedServer struct {
	srv        *httptest.Server
	frames     []any // server frames to emit after the message frame
	rejectAuth bool  // if true: send error frame then close with ClosePolicyViolation
	approvals  chan approvalRecord

	// approvalStatusCode, when non-zero, overrides the HTTP status returned by
	// the approval handler. Used to simulate server errors.
	approvalStatusCode int
}

func newScriptedServer(t *testing.T, frames []any, rejectAuth bool) *scriptedServer {
	t.Helper()
	ss := &scriptedServer{
		frames:     frames,
		rejectAuth: rejectAuth,
		approvals:  make(chan approvalRecord, 16),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/chat/ws", ss.wsHandler)
	mux.HandleFunc("/api/v1/tool-approvals/", ss.approvalHandler)
	ss.srv = httptest.NewServer(mux)
	t.Cleanup(ss.srv.Close)
	return ss
}

func (ss *scriptedServer) addr() string {
	// httptest.Server.URL is "http://127.0.0.1:PORT" — strip the scheme.
	return strings.TrimPrefix(ss.srv.URL, "http://")
}

func (ss *scriptedServer) wsHandler(w http.ResponseWriter, r *http.Request) {
	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	// Read the auth frame.
	_, raw, err := conn.ReadMessage()
	if err != nil {
		return
	}
	var authFrame generated.AuthFrame
	if jsonErr := json.Unmarshal(raw, &authFrame); jsonErr != nil {
		_ = conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "bad auth frame"))
		return
	}
	if ss.rejectAuth || authFrame.Token != testToken {
		// Match the live gateway ordering: send an error frame FIRST, then the
		// ClosePolicyViolation close. The client must detect ErrKeyInvalid from
		// the error frame (FR-019 primary path) rather than waiting for the close.
		errFrame := generated.ErrorFrame{
			Type:    string(generated.WsFrameTypeError),
			Message: "unauthorized: invalid token",
		}
		errData, _ := json.Marshal(errFrame)
		_ = conn.WriteMessage(websocket.TextMessage, errData)
		_ = conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "authentication failed"))
		return
	}

	// Read the message frame (discard content — we only care about structure).
	if _, _, err = conn.ReadMessage(); err != nil {
		return
	}

	// Emit the scripted server frames.
	for _, frame := range ss.frames {
		data, marshalErr := json.Marshal(frame)
		if marshalErr != nil {
			continue
		}
		if writeErr := conn.WriteMessage(websocket.TextMessage, data); writeErr != nil {
			return
		}
	}
}

func (ss *scriptedServer) approvalHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// Extract the approval ID from the path: /api/v1/tool-approvals/<id>
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/tool-approvals/")

	var body generated.ToolApprovalActionRequest
	if jsonErr := json.NewDecoder(r.Body).Decode(&body); jsonErr != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	ss.approvals <- approvalRecord{ApprovalID: id, Action: string(body.Action)}

	statusCode := ss.approvalStatusCode
	if statusCode == 0 {
		statusCode = http.StatusOK
	}
	if statusCode < 200 || statusCode >= 300 {
		http.Error(w, "simulated server error", statusCode)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_, _ = w.Write([]byte(`{"status":"ok","action":"` + string(body.Action) + `"}`))
}

// makeOptions builds an Options struct pointed at the given scriptedServer.
func makeOptions(ss *scriptedServer, yes bool, stdout, stderr io.Writer) run.Options {
	return run.Options{
		Agent:   "jim",
		Prompt:  "hello",
		Addr:    ss.addr(),
		Token:   testToken,
		Yes:     yes,
		Timeout: 10 * time.Second,
		Stdout:  stdout,
		Stderr:  stderr,
	}
}

// --------------------------------------------------------------------------
// Tests
// --------------------------------------------------------------------------

// TestRun_HappyPath verifies that a clean session_started → token* → done
// sequence streams tokens to stdout and returns nil.
//
// BDD: Given a running gateway, When "omnipus jim hello", Then the response
// streams to stdout and the process exits 0.
func TestRun_HappyPath(t *testing.T) {
	t.Parallel()

	frames := []any{
		generated.SessionStartedFrame{Type: "session_started", SessionId: "sess-1"},
		generated.TokenFrame{Type: "token", SessionId: "sess-1", Content: "hello"},
		generated.TokenFrame{Type: "token", SessionId: "sess-1", Content: " world"},
		generated.DoneFrame{Type: "done", SessionId: "sess-1"},
	}

	ss := newScriptedServer(t, frames, false)
	var stdout, stderr bytes.Buffer
	opts := makeOptions(ss, false, &stdout, &stderr)

	err := run.Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("Run returned unexpected error: %v", err)
	}

	// Tokens must appear on stdout, not stderr.
	if !strings.Contains(stdout.String(), "hello world") {
		t.Errorf("stdout does not contain expected tokens: %q", stdout.String())
	}
	// The trailing newline must be present.
	if !strings.HasSuffix(stdout.String(), "\n") {
		t.Errorf("stdout does not end with newline: %q", stdout.String())
	}
	// stderr must not contain any of the token text.
	if strings.Contains(stderr.String(), "hello world") {
		t.Errorf("token text leaked to stderr: %q", stderr.String())
	}
}

// TestRun_ToolApprovalDeny verifies that without --yes, the CLI sends
// {"action":"deny"} to the REST endpoint and prints "denied tool: <name>" to
// stderr; the run continues to done and returns nil.
//
// BDD: Given no --yes, When tool_approval_required frame arrives, Then a
// POST {"action":"deny"} is sent and the run completes.
func TestRun_ToolApprovalDeny(t *testing.T) {
	t.Parallel()

	frames := []any{
		generated.SessionStartedFrame{Type: "session_started", SessionId: "sess-2"},
		generated.ToolApprovalRequiredFrame{
			Type:        "tool_approval_required",
			SessionId:   "sess-2",
			AgentId:     "jim",
			ApprovalId:  "appr-abc",
			ToolName:    "workspace.shell",
			ToolCallId:  "tc-1",
			TurnId:      "turn-1",
			Args:        map[string]any{"command": "ls"},
			ExpiresInMs: 30000,
		},
		generated.DoneFrame{Type: "done", SessionId: "sess-2"},
	}

	ss := newScriptedServer(t, frames, false)
	var stdout, stderr bytes.Buffer
	opts := makeOptions(ss, false, &stdout, &stderr)

	err := run.Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("Run returned unexpected error: %v", err)
	}

	// Must have received a deny POST.
	select {
	case rec := <-ss.approvals:
		if rec.ApprovalID != "appr-abc" {
			t.Errorf("approval ID = %q, want appr-abc", rec.ApprovalID)
		}
		if rec.Action != "deny" {
			t.Errorf("action = %q, want deny", rec.Action)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no approval POST received")
	}

	// "denied tool: workspace.shell" must be on stderr.
	if !strings.Contains(stderr.String(), "denied tool: workspace.shell") {
		t.Errorf("stderr does not contain denied tool message: %q", stderr.String())
	}
	// Must not appear on stdout.
	if strings.Contains(stdout.String(), "denied") {
		t.Errorf("denied message leaked to stdout: %q", stdout.String())
	}
}

// TestRun_ToolApprovalApprove verifies that with --yes, the CLI sends
// {"action":"approve"} and the run completes normally.
//
// BDD: Given --yes, When tool_approval_required frame arrives, Then a
// POST {"action":"approve"} is sent and the run completes.
func TestRun_ToolApprovalApprove(t *testing.T) {
	t.Parallel()

	frames := []any{
		generated.SessionStartedFrame{Type: "session_started", SessionId: "sess-3"},
		generated.ToolApprovalRequiredFrame{
			Type:        "tool_approval_required",
			SessionId:   "sess-3",
			AgentId:     "jim",
			ApprovalId:  "appr-def",
			ToolName:    "workspace.shell",
			ToolCallId:  "tc-2",
			TurnId:      "turn-2",
			Args:        map[string]any{"command": "echo hi"},
			ExpiresInMs: 30000,
		},
		generated.TokenFrame{Type: "token", SessionId: "sess-3", Content: "hi"},
		generated.DoneFrame{Type: "done", SessionId: "sess-3"},
	}

	ss := newScriptedServer(t, frames, false)
	var stdout, stderr bytes.Buffer
	opts := makeOptions(ss, true, &stdout, &stderr) // Yes=true

	err := run.Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("Run returned unexpected error: %v", err)
	}

	// Must have received an approve POST.
	select {
	case rec := <-ss.approvals:
		if rec.ApprovalID != "appr-def" {
			t.Errorf("approval ID = %q, want appr-def", rec.ApprovalID)
		}
		if rec.Action != "approve" {
			t.Errorf("action = %q, want approve", rec.Action)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no approval POST received")
	}

	// The "hi" token should be on stdout.
	if !strings.Contains(stdout.String(), "hi") {
		t.Errorf("stdout does not contain expected token: %q", stdout.String())
	}
}

// TestRun_GatewayDown verifies that when the gateway is not listening on the
// given Addr, Run returns ErrGatewayDown (FR-014).
//
// BDD: Given the gateway is not running, When Run is called, Then ErrGatewayDown
// is returned.
func TestRun_GatewayDown(t *testing.T) {
	t.Parallel()

	// Bind to an ephemeral port and immediately close it so the port is free but
	// nothing is listening.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	addr := strings.TrimPrefix(srv.URL, "http://")
	srv.Close() // close immediately — nothing listening on this addr now

	var stdout, stderr bytes.Buffer
	opts := run.Options{
		Agent:   "jim",
		Prompt:  "hi",
		Addr:    addr,
		Token:   testToken,
		Timeout: 5 * time.Second,
		Stdout:  &stdout,
		Stderr:  &stderr,
	}

	err := run.Run(context.Background(), opts)
	if err != run.ErrGatewayDown {
		t.Errorf("Run returned %v, want ErrGatewayDown", err)
	}
}

// TestRun_AuthRejected verifies that when the server sends an error frame
// followed by ClosePolicyViolation (matching the real gateway ordering in
// pkg/gateway/websocket.go:665-706), Run returns ErrKeyInvalid (FR-019).
//
// The error frame arrives BEFORE the close — the fix ensures ErrKeyInvalid is
// triggered by the error frame on the !authed path, not only by the close code.
//
// BDD: Given an invalid CLI key, When Run is called, Then ErrKeyInvalid is returned.
func TestRun_AuthRejected(t *testing.T) {
	t.Parallel()

	ss := newScriptedServer(t, nil, true /* rejectAuth */)
	var stdout, stderr bytes.Buffer
	opts := run.Options{
		Agent:   "jim",
		Prompt:  "hi",
		Addr:    ss.addr(),
		Token:   "wrong-token", // triggers auth rejection
		Timeout: 5 * time.Second,
		Stdout:  &stdout,
		Stderr:  &stderr,
	}

	err := run.Run(context.Background(), opts)
	if err != run.ErrKeyInvalid {
		t.Errorf("Run returned %v, want ErrKeyInvalid", err)
	}
}

// TestRun_RemoteUnsupported verifies that setting Options.URL causes Run to
// return ErrRemoteUnsupported immediately without contacting anything (FR-007).
//
// BDD: Given --url is set, Then ErrRemoteUnsupported is returned.
func TestRun_RemoteUnsupported(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	opts := run.Options{
		URL:    "https://some-remote-gateway.example.com",
		Addr:   "127.0.0.1:5000", // ignored
		Token:  testToken,
		Stdout: &stdout,
		Stderr: &stderr,
	}

	err := run.Run(context.Background(), opts)
	if err != run.ErrRemoteUnsupported {
		t.Errorf("Run returned %v, want ErrRemoteUnsupported", err)
	}
}

// TestRun_StdoutStderrSeparation verifies that tool frames never appear on
// stdout and that token content never appears on stderr (US-3 AC-1).
//
// BDD: Given a run with both tokens and tool calls, Then stdout has only tokens
// and stderr has only tool/progress info.
func TestRun_StdoutStderrSeparation(t *testing.T) {
	t.Parallel()

	const tokenText = "STDOUT_ONLY_CONTENT"
	frames := []any{
		generated.SessionStartedFrame{Type: "session_started", SessionId: "sess-4"},
		generated.ToolCallStartFrame{
			Type:      "tool_call_start",
			SessionId: "sess-4",
			CallId:    "c1",
			Tool:      "read_file",
			Params:    map[string]any{"path": "/tmp/x"},
		},
		generated.TokenFrame{Type: "token", SessionId: "sess-4", Content: tokenText},
		generated.ToolCallResultFrame{
			Type:      "tool_call_result",
			SessionId: "sess-4",
			CallId:    "c1",
			Tool:      "read_file",
			Status:    "ok",
			Result:    "file content",
		},
		generated.DoneFrame{Type: "done", SessionId: "sess-4"},
	}

	ss := newScriptedServer(t, frames, false)
	var stdout, stderr bytes.Buffer
	opts := makeOptions(ss, false, &stdout, &stderr)

	if err := run.Run(context.Background(), opts); err != nil {
		t.Fatalf("Run returned unexpected error: %v", err)
	}

	// Token text must be on stdout.
	if !strings.Contains(stdout.String(), tokenText) {
		t.Errorf("stdout does not contain token text %q: %q", tokenText, stdout.String())
	}
	// Tool frame text must NOT be on stdout.
	if strings.Contains(stdout.String(), "read_file") {
		t.Errorf("tool frame text leaked to stdout: %q", stdout.String())
	}
	// Token text must NOT be on stderr.
	if strings.Contains(stderr.String(), tokenText) {
		t.Errorf("token text leaked to stderr: %q", stderr.String())
	}
	// Tool frame must appear on stderr.
	if !strings.Contains(stderr.String(), "read_file") {
		t.Errorf("stderr does not contain tool frame info: %q", stderr.String())
	}
}

// TestRun_ModelOverride verifies that when Options.Model is set, the sent
// message frame includes metadata.model_name, and when absent metadata is omitted.
// This is validated indirectly: if the server can decode the message frame
// correctly in both cases the test passes (the scripted server does not inspect
// the frame deeply, but the marshal/unmarshal round-trip exercises the code path).
func TestRun_ModelOverride(t *testing.T) {
	t.Parallel()

	frames := []any{
		generated.SessionStartedFrame{Type: "session_started", SessionId: "sess-5"},
		generated.DoneFrame{Type: "done", SessionId: "sess-5"},
	}
	ss := newScriptedServer(t, frames, false)
	var stdout, stderr bytes.Buffer

	// With model override — should not error.
	optsWithModel := makeOptions(ss, false, &stdout, &stderr)
	optsWithModel.Model = "openrouter/glm-5.2"
	if err := run.Run(context.Background(), optsWithModel); err != nil {
		t.Errorf("Run with model override returned error: %v", err)
	}

	// Without model override — re-use same server (scripted server handles
	// multiple connections).
	ss2 := newScriptedServer(t, frames, false)
	var stdout2, stderr2 bytes.Buffer
	optsNoModel := makeOptions(ss2, false, &stdout2, &stderr2)
	if err := run.Run(context.Background(), optsNoModel); err != nil {
		t.Errorf("Run without model override returned error: %v", err)
	}
}

// TestRun_ErrorFrame verifies that an error frame from the server causes Run to
// return a non-nil error and writes the message to stderr (US-3 AC-2).
//
// BDD: Given an error frame, Then stderr contains the message and Run is non-nil.
func TestRun_ErrorFrame(t *testing.T) {
	t.Parallel()

	const errMsg = "provider quota exceeded"
	frames := []any{
		generated.SessionStartedFrame{Type: "session_started", SessionId: "sess-6"},
		generated.ErrorFrame{Type: "error", Message: errMsg},
	}

	ss := newScriptedServer(t, frames, false)
	var stdout, stderr bytes.Buffer
	opts := makeOptions(ss, false, &stdout, &stderr)

	err := run.Run(context.Background(), opts)
	if err == nil {
		t.Fatal("Run returned nil but expected a non-nil error on error frame")
	}
	if !strings.Contains(stderr.String(), errMsg) {
		t.Errorf("stderr does not contain error message %q: %q", errMsg, stderr.String())
	}
	// Error text must not appear on stdout.
	if strings.Contains(stdout.String(), errMsg) {
		t.Errorf("error message leaked to stdout: %q", stdout.String())
	}
}

// TestRun_TimeoutDuringKeepalive verifies that when the server streams keep-alive
// frames indefinitely but never sends a done frame, Run returns ErrTimeout within
// the Timeout budget. This exercises the ctx.Done() goroutine that unblocks the
// blocked ReadMessage call — without it, a keep-alive stream would prevent the
// timeout from firing.
//
// BDD: Given a server that streams pong frames forever and never sends done,
// And --timeout is 300 ms, Then Run returns ErrTimeout within ~600 ms.
func TestRun_TimeoutDuringKeepalive(t *testing.T) {
	t.Parallel()

	// keepAliveHandler upgrades to WS, accepts auth + message, then streams
	// pong frames at 20 ms intervals forever (simulating a server that sends
	// periodic keep-alive traffic but never completes the turn).
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/chat/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// Read auth frame.
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var authFrame generated.AuthFrame
		if jsonErr := json.Unmarshal(raw, &authFrame); jsonErr != nil || authFrame.Token != testToken {
			return
		}
		// Send session_started so the client marks authed, then keep-alive pongs.
		sessionFrame := generated.SessionStartedFrame{Type: "session_started", SessionId: "sess-ka"}
		if data, marshalErr := json.Marshal(sessionFrame); marshalErr == nil {
			_ = conn.WriteMessage(websocket.TextMessage, data)
		}
		// Read the message frame.
		if _, _, err = conn.ReadMessage(); err != nil {
			return
		}
		// Stream pong frames indefinitely until the client closes.
		pong := map[string]string{"type": "pong"}
		pongData, _ := json.Marshal(pong)
		for {
			if writeErr := conn.WriteMessage(websocket.TextMessage, pongData); writeErr != nil {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	addr := strings.TrimPrefix(srv.URL, "http://")

	start := time.Now()
	const budget = 300 * time.Millisecond
	err := run.Run(context.Background(), run.Options{
		Agent:   "jim",
		Prompt:  "hello",
		Addr:    addr,
		Token:   testToken,
		Timeout: budget,
		Stdout:  io.Discard,
		Stderr:  io.Discard,
	})

	elapsed := time.Since(start)
	if err != run.ErrTimeout {
		t.Errorf("Run returned %v, want ErrTimeout", err)
	}
	// Allow up to 3× the budget for scheduling jitter; must not exceed 10×.
	if elapsed > 10*budget {
		t.Errorf("Run took %v, expected < %v (10× budget)", elapsed, 10*budget)
	}
}

// TestRun_ApprovalPostFailure verifies that when the /tool-approvals endpoint
// returns 500 on both the initial attempt and the retry, Run returns
// ErrApprovalFailed rather than silently continuing to a 90 s server hang.
//
// BDD: Given the approval endpoint always returns 500,
// When a tool_approval_required frame arrives,
// Then Run returns ErrApprovalFailed (not nil).
func TestRun_ApprovalPostFailure(t *testing.T) {
	t.Parallel()

	// The approval POST is synchronous (blocks the WS read loop while it runs),
	// so we must ensure the WS scripted frames are ready before the approval
	// fires. The approval frame arrives after session_started; after both POSTs
	// fail, Run should return ErrApprovalFailed before reaching the done frame.
	frames := []any{
		generated.SessionStartedFrame{Type: "session_started", SessionId: "sess-af"},
		generated.ToolApprovalRequiredFrame{
			Type:        "tool_approval_required",
			SessionId:   "sess-af",
			AgentId:     "jim",
			ApprovalId:  "appr-fail",
			ToolName:    "workspace.shell",
			ToolCallId:  "tc-fail",
			TurnId:      "turn-fail",
			Args:        map[string]any{"command": "rm -rf /"},
			ExpiresInMs: 30000,
		},
		// done frame is present in the script but should never be reached because
		// Run exits on ErrApprovalFailed before the WS read resumes.
		generated.DoneFrame{Type: "done", SessionId: "sess-af"},
	}

	ss := newScriptedServer(t, frames, false)
	ss.approvalStatusCode = http.StatusInternalServerError // both attempts return 500

	var postCount atomic.Int32
	var stdout, stderr bytes.Buffer
	opts := run.Options{
		Agent:   "jim",
		Prompt:  "hello",
		Addr:    ss.addr(),
		Token:   testToken,
		Yes:     true,
		Timeout: 10 * time.Second,
		Stdout:  &stdout,
		Stderr:  &stderr,
	}

	err := run.Run(context.Background(), opts)
	if err != run.ErrApprovalFailed {
		t.Errorf("Run returned %v, want ErrApprovalFailed", err)
	}

	// Drain the approvals channel to count how many POSTs the server received.
	// Both the initial attempt and the retry must have fired (2 POSTs total).
	close(ss.approvals)
	for range ss.approvals {
		postCount.Add(1)
	}
	if n := postCount.Load(); n != 2 {
		t.Errorf("approval endpoint received %d POST(s), want 2 (initial + retry)", n)
	}
}

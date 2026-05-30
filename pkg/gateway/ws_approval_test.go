//go:build !cgo

// This test file uses //go:build !cgo so it compiles when CGO is disabled.
// When CGO is enabled, pkg/gateway imports pkg/channels/matrix which requires
// the libolm system library (olm/olm.h). If that library is installed,
// remove this build constraint and run tests normally.

package gateway

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/agent"
)

// --- test helpers ---

// makeTestConn creates a minimal wsConn with a buffered sendCh and an open doneCh.
// The sendCh must be buffered so sendConnGenFrame does not block in tests.
func makeTestConn() *wsConn {
	return &wsConn{
		sendCh: make(chan []byte, 16),
		doneCh: make(chan struct{}),
	}
}

// makeTestHook creates a wsApprovalHook with a fresh registry and configurable timeout.
func makeTestHook(conn *wsConn, timeout time.Duration) (*wsApprovalHook, *wsApprovalRegistry) {
	reg := newWSApprovalRegistry()
	return &wsApprovalHook{
		conn:     conn,
		registry: reg,
		timeout:  timeout,
	}, reg
}

// unmarshalWSServerFrame decodes raw websocket message bytes into a replayFrameDecoder.
func unmarshalWSServerFrame(b []byte, f *replayFrameDecoder) error {
	return json.Unmarshal(b, f)
}

// writeWSClientFrame marshals and sends a wsClientFrameTestHelper over the WebSocket connection.
func writeWSClientFrame(t *testing.T, conn *websocket.Conn, frame wsClientFrameTestHelper) {
	t.Helper()
	data, err := json.Marshal(frame)
	require.NoError(t, err, "marshal client frame")
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, data), "write client frame")
}

// writeWSClientFrameNoFail is like writeWSClientFrame but returns the error instead of failing.
func writeWSClientFrameNoFail(conn *websocket.Conn, frame wsClientFrameTestHelper) error {
	data, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	return conn.WriteMessage(websocket.TextMessage, data)
}

// --- wsApprovalRegistry unit tests ---

// TestApprovalRegistry_RegisterResolve verifies the happy path: register an ID,
// resolve it with VerdictAllow, and verify the decision arrives on the channel.
// BDD: Given an empty registry,
// When register("req-1") is called then resolve("req-1", {VerdictAllow}) is called,
// Then the channel receives the decision and IsApproved() returns true.
// Traces to: vivid-roaming-planet.md line 137
func TestApprovalRegistry_RegisterResolve(t *testing.T) {
	reg := newWSApprovalRegistry()

	ch := reg.register("req-1", "")
	require.NotNil(t, ch, "register must return a non-nil channel")

	decision := agent.ApprovalDecision{Verdict: agent.VerdictAllow}
	resolved := reg.resolve("req-1", decision)
	assert.True(t, resolved, "resolve must return true for a registered ID")

	select {
	case received := <-ch:
		assert.True(t, received.IsApproved(), "VerdictAllow must be approved")
		assert.Equal(t, agent.VerdictAllow, received.Verdict)
	case <-time.After(1 * time.Second):
		t.Fatal("decision never arrived on channel")
	}
}

// TestApprovalRegistry_ResolveUnknown verifies that resolving an unregistered ID returns false.
// BDD: Given an empty registry,
// When resolve("nonexistent", decision) is called,
// Then it returns false and no panic occurs.
// Traces to: vivid-roaming-planet.md line 138
func TestApprovalRegistry_ResolveUnknown(t *testing.T) {
	reg := newWSApprovalRegistry()

	resolved := reg.resolve("nonexistent-id", agent.ApprovalDecision{Verdict: agent.VerdictAllow})
	assert.False(t, resolved, "resolving an unregistered ID must return false")
}

// TestApprovalRegistry_UnregisterBeforeResolve verifies that after unregister,
// resolve returns false for that ID.
// BDD: Given a registered ID "req-unregister",
// When unregister is called before resolve,
// Then resolve returns false.
// Traces to: vivid-roaming-planet.md line 139
func TestApprovalRegistry_UnregisterBeforeResolve(t *testing.T) {
	reg := newWSApprovalRegistry()

	reg.register("req-unregister", "")
	reg.unregister("req-unregister")

	resolved := reg.resolve("req-unregister", agent.ApprovalDecision{Verdict: agent.VerdictAllow})
	assert.False(t, resolved, "resolve after unregister must return false")
}

// TestApprovalRegistry_DuplicateRegister verifies that registering the same ID twice
// returns the existing channel — no silent overwrite, no goroutine is orphaned.
// BDD: Given a registered ID "req-dup",
// When register("req-dup") is called a second time,
// Then the same channel is returned and the first waiter still receives.
// Traces to: vivid-roaming-planet.md line 140
func TestApprovalRegistry_DuplicateRegister(t *testing.T) {
	reg := newWSApprovalRegistry()

	ch1 := reg.register("req-dup", "")
	ch2 := reg.register("req-dup", "")

	// The implementation returns the existing channel on duplicate.
	assert.Equal(t, ch1, ch2, "duplicate register must return the existing channel")

	// Resolve via the ID — the original waiter must receive the decision.
	reg.resolve("req-dup", agent.ApprovalDecision{Verdict: agent.VerdictDeny, Reason: "dup test"})

	select {
	case received := <-ch1:
		assert.Equal(t, agent.VerdictDeny, received.Verdict)
	case <-time.After(1 * time.Second):
		t.Fatal("decision never arrived on channel after duplicate register")
	}
}

// TestApprovalRegistry_ConcurrentAccess verifies no data races when multiple goroutines
// register and resolve different IDs simultaneously. Run with: go test -race ./pkg/gateway/...
// BDD: Given 50 goroutines each registering and resolving a unique ID concurrently,
// When all goroutines complete,
// Then all decisions are received and no race is detected by -race.
// Traces to: vivid-roaming-planet.md line 141
func TestApprovalRegistry_ConcurrentAccess(t *testing.T) {
	reg := newWSApprovalRegistry()
	const numGoroutines = 50

	var wg sync.WaitGroup

	for i := range numGoroutines {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			// Use a unique ID per goroutine.
			id := "concurrent-req-" + string(rune('A'+n%26)) + "-" + time.Now().String() + "-" + string(rune('0'+n%10))
			ch := reg.register(id, "")
			defer reg.unregister(id)

			// Resolve in another goroutine to simulate real async use.
			go func() {
				reg.resolve(id, agent.ApprovalDecision{Verdict: agent.VerdictAllow})
			}()

			select {
			case <-ch:
				// success
			case <-time.After(2 * time.Second):
				// timeout — not a race, but flag it
				t.Errorf("goroutine %d: decision timed out", n)
			}
		}(i)
	}

	wg.Wait()
}

// --- wsApprovalHook unit tests ---

// TestApprovalHook_HappyPath verifies that ApproveTool returns an approved decision
// when the registry is resolved with VerdictAllow from another goroutine.
// BDD: Given a connected wsApprovalHook with timeout=5s,
// When ApproveTool is called and the browser resolves with VerdictAllow,
// Then IsApproved() is true and error is nil.
// Traces to: vivid-roaming-planet.md line 145
func TestApprovalHook_HappyPath(t *testing.T) {
	conn := makeTestConn()
	hook, reg := makeTestHook(conn, 5*time.Second)

	req := &agent.ToolApprovalRequest{Tool: "read_file", Arguments: map[string]any{"path": "/tmp/test.txt"}}

	// Resolve asynchronously: read the approval-request frame from sendCh to extract the ID,
	// then call registry.resolve.
	go func() {
		select {
		case frameBytes := <-conn.sendCh:
			var frame replayFrameDecoder
			if err := unmarshalWSServerFrame(frameBytes, &frame); err != nil {
				return
			}
			if frame.Type == "exec_approval_request" {
				reg.resolve(frame.ID, agent.ApprovalDecision{Verdict: agent.VerdictAllow})
			}
		case <-time.After(2 * time.Second):
		}
	}()

	decision, err := hook.ApproveTool(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, decision.IsApproved(), "VerdictAllow must be approved")
}

// TestApprovalHook_Denial verifies that ApproveTool returns a denial when the registry
// is resolved with VerdictDeny and a reason string.
// BDD: Given a connected wsApprovalHook,
// When the browser resolves with VerdictDeny + reason "not allowed",
// Then IsApproved() is false and Reason equals "not allowed".
// Traces to: vivid-roaming-planet.md line 146
func TestApprovalHook_Denial(t *testing.T) {
	conn := makeTestConn()
	hook, reg := makeTestHook(conn, 5*time.Second)

	req := &agent.ToolApprovalRequest{Tool: "exec", Arguments: map[string]any{"command": "rm -rf /"}}

	go func() {
		select {
		case frameBytes := <-conn.sendCh:
			var frame replayFrameDecoder
			if err := unmarshalWSServerFrame(frameBytes, &frame); err != nil {
				return
			}
			if frame.Type == "exec_approval_request" {
				reg.resolve(frame.ID, agent.Deny("not allowed"))
			}
		case <-time.After(2 * time.Second):
		}
	}()

	decision, err := hook.ApproveTool(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, decision.IsApproved(), "VerdictDeny must not be approved")
	assert.Equal(t, "not allowed", decision.Reason)
}

// TestApprovalHook_Timeout verifies that ApproveTool returns a denial after the configured
// timeout fires without any registry resolution.
// BDD: Given a wsApprovalHook with timeout=100ms,
// When ApproveTool is called and no resolution arrives within 100ms,
// Then it returns a denial whose Reason contains "timed out".
// Traces to: vivid-roaming-planet.md line 147
func TestApprovalHook_Timeout(t *testing.T) {
	conn := makeTestConn()
	hook, _ := makeTestHook(conn, 100*time.Millisecond)
	t.Cleanup(func() { close(conn.sendCh) })

	req := &agent.ToolApprovalRequest{Tool: "slow_tool", Arguments: map[string]any{}}

	// Drain sendCh so the expiry frame does not block the select in ApproveTool.
	// The drainer exits when sendCh is closed in t.Cleanup.
	go func() {
		for range conn.sendCh {
		}
	}()

	start := time.Now()
	decision, err := hook.ApproveTool(context.Background(), req)
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.False(t, decision.IsApproved(), "timeout must result in denial")
	assert.Contains(t, decision.Reason, "timed out", "denial reason must mention timeout")
	assert.Less(t, elapsed, 1*time.Second, "must not wait longer than ~100ms")
}

// TestApprovalHook_ContextCancelled verifies that ApproveTool returns immediately when
// the parent context is canceled, returning denial + ctx.Err().
// BDD: Given a wsApprovalHook and a context canceled after 50ms,
// When ApproveTool is called,
// Then it returns denial with reason "context canceled" and err == context.Canceled.
// Traces to: vivid-roaming-planet.md line 148
func TestApprovalHook_ContextCancelled(t *testing.T) {
	conn := makeTestConn()
	hook, _ := makeTestHook(conn, 5*time.Second)
	t.Cleanup(func() { close(conn.sendCh) })

	req := &agent.ToolApprovalRequest{Tool: "long_tool", Arguments: map[string]any{}}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	// Drain sendCh so the approval-request frame does not block.
	// The drainer exits when sendCh is closed in t.Cleanup.
	go func() {
		for range conn.sendCh {
		}
	}()

	decision, err := hook.ApproveTool(ctx, req)

	assert.ErrorIs(t, err, context.Canceled, "context cancellation must return context.Canceled")
	assert.False(t, decision.IsApproved(), "canceled context must result in denial")
	assert.Contains(t, decision.Reason, "context canceled")
}

// TestApprovalHook_NilConn verifies that a hook with conn=nil returns an immediate denial.
// BDD: Given a wsApprovalHook with conn=nil,
// When ApproveTool is called,
// Then it returns a denial immediately without blocking.
// Traces to: vivid-roaming-planet.md line 149
func TestApprovalHook_NilConn(t *testing.T) {
	hook := &wsApprovalHook{
		conn:     nil,
		registry: newWSApprovalRegistry(),
		timeout:  5 * time.Second,
	}

	req := &agent.ToolApprovalRequest{Tool: "any_tool", Arguments: map[string]any{}}
	decision, err := hook.ApproveTool(context.Background(), req)

	require.NoError(t, err)
	assert.False(t, decision.IsApproved(), "nil conn must result in immediate denial")
}

// TestApprovalHook_ConnectionClosed verifies that ApproveTool returns a denial when
// the connection's doneCh is closed before the approval arrives.
// BDD: Given a wsApprovalHook whose conn.doneCh is already closed,
// When ApproveTool is called,
// Then it returns a denial with reason mentioning "connection closed".
// Traces to: vivid-roaming-planet.md line 150
func TestApprovalHook_ConnectionClosed(t *testing.T) {
	conn := makeTestConn()
	hook, _ := makeTestHook(conn, 5*time.Second)
	t.Cleanup(func() { close(conn.sendCh) })

	// Drain sendCh to avoid blocking on the approval-request send.
	// The drainer exits when sendCh is closed in t.Cleanup.
	go func() {
		for range conn.sendCh {
		}
	}()

	// Close doneCh immediately — simulates a dropped connection.
	conn.close()

	req := &agent.ToolApprovalRequest{Tool: "any_tool", Arguments: map[string]any{}}
	decision, err := hook.ApproveTool(context.Background(), req)

	require.NoError(t, err)
	assert.False(t, decision.IsApproved(), "closed connection must result in denial")
	assert.Contains(t, decision.Reason, "connection closed")
}

// --- handleApprovalResponse integration tests (full WebSocket round-trip) ---

// TestHandleApprovalResponse_Allow verifies that an exec_approval_response frame with
// decision "allow" causes the registry to resolve the pending request as VerdictAllow.
// BDD: Given an active WebSocket connection and a pre-registered pending approval ID,
// When the browser sends exec_approval_response with decision="allow",
// Then the registry channel receives VerdictAllow.
// Traces to: vivid-roaming-planet.md line 152
// runApprovalResponseRoundTrip drives one exec_approval_response round-trip
// for the three TestHandleApprovalResponse_* tests below. Centralizing this
// keeps the per-decision tests focused on their single assertion.
func runApprovalResponseRoundTrip(
	t *testing.T,
	pendingID, decision string,
	want agent.ApprovalVerdict,
	wantApproved bool,
) {
	t.Helper()
	handler, _, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	conn := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })

	sendWSAuthFrameDevMode(t, conn)

	ch := handler.approvalRegistry.register(pendingID, "")
	defer handler.approvalRegistry.unregister(pendingID)

	writeWSClientFrame(t, conn, wsClientFrameTestHelper{
		Type:     "exec_approval_response",
		ID:       pendingID,
		Decision: decision,
	})

	select {
	case got := <-ch:
		assert.Equal(t, wantApproved, got.IsApproved(), "decision=%s must yield IsApproved=%v", decision, wantApproved)
		assert.Equal(t, want, got.Verdict)
	case <-time.After(2 * time.Second):
		t.Fatalf("registry was not resolved after exec_approval_response with %s", decision)
	}
}

func TestHandleApprovalResponse_Allow(t *testing.T) {
	runApprovalResponseRoundTrip(t, "test-allow-request-id", "allow", agent.VerdictAllow, true)
}

// TestHandleApprovalResponse_Deny verifies that decision "deny" resolves as VerdictDeny.
// BDD: Given a pending approval request,
// When the browser sends exec_approval_response with decision="deny",
// Then the registry resolves with VerdictDeny and IsApproved() is false.
// Traces to: vivid-roaming-planet.md line 153
func TestHandleApprovalResponse_Deny(t *testing.T) {
	runApprovalResponseRoundTrip(t, "test-deny-request-id", "deny", agent.VerdictDeny, false)
}

// TestHandleApprovalResponse_Always verifies that decision "always" resolves as VerdictAlways.
// BDD: Given a pending approval request,
// When the browser sends exec_approval_response with decision="always",
// Then the registry resolves with VerdictAlways and IsApproved() is true.
// Traces to: vivid-roaming-planet.md line 154
func TestHandleApprovalResponse_Always(t *testing.T) {
	runApprovalResponseRoundTrip(t, "test-always-request-id", "always", agent.VerdictAlways, true)
}

// --- autoApproveSafeTool unit tests (Test Suite 2) ---

// TestAutoApproveSafeTool_AllSafeTools verifies that every tool in the allow-list
// returns true from autoApproveSafeTool.
// BDD: Given each tool in the pre-approved list,
// When autoApproveSafeTool is called,
// Then it returns true.
// Traces to: ws_approval.go — autoApproveSafeTool allow-list
func TestAutoApproveSafeTool_AllSafeTools(t *testing.T) {
	safeTools := []string{
		"read_file",
		"list_dir",
		"write_file",
		"edit_file",
		"append_file",
		"web_search",
		"web_fetch",
		"send_file",
		"message",
		"find_skills",
		"spawn",
		"subagent",
		"spawn_status",
		"cron",
	}

	for _, tool := range safeTools {
		// capture loop variable
		t.Run(tool, func(t *testing.T) {
			if !autoApproveSafeTool(tool) {
				t.Errorf("autoApproveSafeTool(%q) = false, want true", tool)
			}
		})
	}
}

// TestAutoApproveSafeTool_ExecRequiresApproval verifies that "exec" (shell commands)
// requires explicit user approval and is not auto-approved.
// BDD: Given tool name "exec",
// When autoApproveSafeTool is called,
// Then it returns false.
// Traces to: ws_approval.go — autoApproveSafeTool default case
func TestAutoApproveSafeTool_ExecRequiresApproval(t *testing.T) {
	if autoApproveSafeTool("exec") {
		t.Error("autoApproveSafeTool(\"exec\") = true, want false — exec requires user approval")
	}
}

// TestAutoApproveSafeTool_UnknownToolRequiresApproval verifies that an unrecognized
// tool name is not auto-approved.
// BDD: Given an unknown tool name,
// When autoApproveSafeTool is called,
// Then it returns false.
// Traces to: ws_approval.go — autoApproveSafeTool default case
func TestAutoApproveSafeTool_UnknownToolRequiresApproval(t *testing.T) {
	if autoApproveSafeTool("unknown_tool") {
		t.Error("autoApproveSafeTool(\"unknown_tool\") = true, want false")
	}
}

// --- Fix: TestApprovalHook_HappyPath must exercise the registry/resolution round-trip ---

// TestApprovalHook_HappyPath_ExecTool verifies that ApproveTool exercises the actual
// approval registry/resolution round-trip for a tool that requires interactive approval.
// Using "exec" (not "read_file") ensures the auto-approve path is bypassed and the
// approval flow is actually tested.
// BDD: Given a connected wsApprovalHook with timeout=5s,
// When ApproveTool is called with tool="exec" and the browser resolves with VerdictAllow,
// Then IsApproved() is true and error is nil.
// Traces to: vivid-roaming-planet.md line 145 (corrected tool name to exercise approval flow)
func TestApprovalHook_HappyPath_ExecTool(t *testing.T) {
	conn := makeTestConn()
	hook, reg := makeTestHook(conn, 5*time.Second)

	req := &agent.ToolApprovalRequest{Tool: "exec", Arguments: map[string]any{"command": "ls -la"}}

	// Resolve asynchronously: read the approval-request frame from sendCh to extract the ID,
	// then call registry.resolve.
	go func() {
		select {
		case frameBytes := <-conn.sendCh:
			var frame replayFrameDecoder
			if err := unmarshalWSServerFrame(frameBytes, &frame); err != nil {
				return
			}
			if frame.Type == "exec_approval_request" {
				reg.resolve(frame.ID, agent.ApprovalDecision{Verdict: agent.VerdictAllow})
			}
		case <-time.After(2 * time.Second):
		}
	}()

	decision, err := hook.ApproveTool(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, decision.IsApproved(), "VerdictAllow must be approved")
}

// TestHandleApprovalResponse_EmptyID verifies that an exec_approval_response with an
// empty ID does not crash the server and the connection remains open.
// BDD: Given an active WebSocket connection,
// When the browser sends exec_approval_response with an empty ID,
// Then the server ignores the frame and the connection stays open.
// Traces to: vivid-roaming-planet.md line 155
func TestHandleApprovalResponse_EmptyID(t *testing.T) {
	handler, _, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	conn := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })

	writeWSClientFrame(t, conn, wsClientFrameTestHelper{
		Type:     "exec_approval_response",
		ID:       "", // empty — server must not crash
		Decision: "allow",
	})

	// Give the server time to process the frame.
	time.Sleep(50 * time.Millisecond)

	// Connection must remain open — a subsequent write must succeed.
	err := writeWSClientFrameNoFail(conn, wsClientFrameTestHelper{Type: "message", Content: "still alive"})
	assert.NoError(t, err, "connection must remain open after empty-ID approval response")
}

// ---------------------------------------------------------------------------
// TestWS_ApprovalResponse_RejectsMismatchedSessionID (F11)
// ---------------------------------------------------------------------------

// TestWS_ApprovalResponse_RejectsMismatchedSessionID verifies that an
// exec_approval_response frame carrying a session_id that does not match
// the registry entry's session_id is rejected with an error frame, and the
// approval request is NOT resolved (remains pending).
func TestWS_ApprovalResponse_RejectsMismatchedSessionID(t *testing.T) {
	registry := newWSApprovalRegistry()

	// Register a request for session A.
	const approvalID = "approval-test-001"
	const sessionA = "session-aaa"
	ch := registry.register(approvalID, sessionA)

	// Build a minimal wsConn to capture outbound frames.
	// The sendCh is buffered (16 slots) so sendConnGenFrame will not block.
	wc := makeTestConn()

	// Build a WSHandler to call handleApprovalResponse with a mismatched session.
	h := &WSHandler{
		approvalRegistry: registry,
	}
	const mismatchedSession = "session-bbb"
	h.handleApprovalResponse(approvalID, "allow", mismatchedSession, wc)

	// The approval should NOT have been resolved — ch must still be empty.
	select {
	case <-ch:
		t.Error("approval must NOT be resolved when session_id mismatches")
	default:
		// expected: channel empty = not resolved
	}

	// An error frame must have been sent.
	select {
	case raw := <-wc.sendCh:
		var frame replayFrameDecoder
		if err := json.Unmarshal(raw, &frame); err != nil {
			t.Fatalf("could not unmarshal frame: %v", err)
		}
		if frame.Type != "error" {
			t.Errorf("expected error frame, got %q", frame.Type)
		}
		if frame.Message != "approval session_id mismatch" {
			t.Errorf("unexpected error message: %q", frame.Message)
		}
	default:
		t.Error("expected an error frame to be sent on session_id mismatch")
	}

	close(wc.sendCh)
}

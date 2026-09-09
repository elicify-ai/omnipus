// agent_switched_frame_test.go — ADR-071 §5.2.3 required acceptance test.
//
// Before D4, pkg/gateway/websocket.go gated the agent_switched frame on TWO
// exact-string branches (p.Tool == "hand_off" and p.Tool == "return_to_default").
// D4 merges both tools into switch_agent, with no arguments field in the
// event payload to distinguish which branch ran — so the frame is now
// derived by comparing the session's post-switch active agent against the
// registry's default agent id (see websocket.go's switch_agent case).
//
// §5.2.1 established that the hand_off-success path had ZERO test coverage
// anywhere in the repository before this file — a regression that only
// broke the named-target branch would have shipped green. This file closes
// that gap with the three required cases, asserted positively (a frame with
// these contents was emitted), never merely on the absence of the old name —
// per §5.2.3, a test that only checks absence would pass against a code path
// that emits nothing at all, which is the exact regression being guarded
// against.
package integration

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
)

// mockLLMSwitchAgentSequence scripts a 4-request sequence:
//  1. switch_agent(target: "jim", note: "...") — named-target hand-off.
//  2. Plain text — finishes turn 1.
//  3. switch_agent(target: "default", note: "...") — return to default.
//  4. Plain text — finishes turn 2.
//
// Any further request gets plain text.
func mockLLMSwitchAgentSequence(tb testing.TB) *httptest.Server {
	tb.Helper()
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.NotFound(w, r)
			return
		}
		callCount++
		n := callCount
		switch n {
		case 1:
			writeMockToolCallStream(w, "switch_agent", `{"target":"jim","note":"handing off to jim"}`)
		case 2:
			writeMockStream(w, "Switched to jim.")
		case 3:
			writeMockToolCallStream(w, "switch_agent", `{"target":"default","note":"done, returning"}`)
		case 4:
			writeMockStream(w, "Returned to default.")
		default:
			writeMockStream(w, fmt.Sprintf("reply %d", n))
		}
	}))
	tb.Cleanup(srv.Close)
	return srv
}

// mockLLMFailedSwitchAgent scripts a single request that attempts a
// switch_agent call to a nonexistent agent id, which SwitchAgentTool.Execute
// rejects with an error result — the switch never happens.
func mockLLMFailedSwitchAgent(tb testing.TB) *httptest.Server {
	tb.Helper()
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.NotFound(w, r)
			return
		}
		callCount++
		n := callCount
		switch n {
		case 1:
			writeMockToolCallStream(w, "switch_agent", `{"target":"totally-nonexistent-agent-id"}`)
		case 2:
			writeMockStream(w, "That agent does not exist.")
		default:
			writeMockStream(w, fmt.Sprintf("reply %d", n))
		}
	}))
	tb.Cleanup(srv.Close)
	return srv
}

// findFrame returns the first frame of the given type, or nil.
func findFrame(frames []map[string]any, frameType string) map[string]any {
	for _, f := range frames {
		if tp, _ := f["type"].(string); tp == frameType {
			return f
		}
	}
	return nil
}

// TestSwitchAgent_NamedTarget_EmitsAgentSwitchedFrameWithAgentIdSet is
// required case 1 (§5.2.3): switch_agent(target: "<agent-id>") succeeds ->
// an agent_switched frame IS emitted, with agent_id SET to that agent. This
// is the path that had ZERO prior coverage (the old hand_off-success branch).
func TestSwitchAgent_NamedTarget_EmitsAgentSwitchedFrameWithAgentIdSet(t *testing.T) {
	mock := mockLLMSwitchAgentSequence(t)
	gw := testutil.StartTestGateway(t,
		testutil.WithAPIBase(mock.URL),
		testutil.WithBearerAuth(),
	)

	conn := wsConnect(t, gw)
	sendMessage(t, conn, "please switch to jim")

	frames := collectFramesUntilDone(t, conn, 10*time.Second)
	if len(frames) == 0 {
		t.Fatal("no frames received after first message — turn did not complete")
	}
	logFrameTypes(t, frames)

	switched := findFrame(frames, "agent_switched")
	if switched == nil {
		t.Fatal("expected an agent_switched frame for a successful named-target switch_agent call, got none")
	}
	agentID, ok := switched["agent_id"].(string)
	if !ok || agentID == "" {
		t.Fatalf("expected agent_switched frame's agent_id to be SET to the target agent, got %v (raw frame: %+v)",
			switched["agent_id"], switched)
	}
	if agentID != "jim" {
		t.Errorf("expected agent_switched agent_id=%q, got %q", "jim", agentID)
	}
}

// TestSwitchAgent_Default_EmitsAgentSwitchedFrameWithAgentIdAbsent is
// required case 2 (§5.2.3): switch_agent(target: "default") succeeds -> an
// agent_switched frame IS emitted, with agent_id ABSENT/nil.
func TestSwitchAgent_Default_EmitsAgentSwitchedFrameWithAgentIdAbsent(t *testing.T) {
	mock := mockLLMSwitchAgentSequence(t)
	gw := testutil.StartTestGateway(t,
		testutil.WithAPIBase(mock.URL),
		testutil.WithBearerAuth(),
	)

	conn := wsConnect(t, gw)

	// Turn 1: switch to jim (named target) — establishes a non-default
	// active agent so turn 2's return-to-default is a genuine transition.
	sendMessage(t, conn, "please switch to jim")
	frames1 := collectFramesUntilDone(t, conn, 10*time.Second)
	if len(frames1) == 0 {
		t.Fatal("no frames received after first message — turn did not complete")
	}
	sessionID := extractSessionID(frames1)
	if sessionID == "" {
		t.Fatal("no session_id captured from first turn's session_started frame")
	}

	// Turn 2 (same session, jim now active): switch_agent(target: "default").
	sendMessage(t, conn, "ok now go back", sessionID)
	frames2 := collectFramesUntilDone(t, conn, 10*time.Second)
	if len(frames2) == 0 {
		t.Fatal("no frames received after second message — turn did not complete")
	}
	logFrameTypes(t, frames2)

	switched := findFrame(frames2, "agent_switched")
	if switched == nil {
		t.Fatal("expected an agent_switched frame for a successful target:\"default\" switch_agent call, got none")
	}
	if agentID, present := switched["agent_id"]; present && agentID != nil {
		t.Errorf("expected agent_switched frame's agent_id to be ABSENT/nil for a return-to-default switch, got %v (raw frame: %+v)",
			agentID, switched)
	}
}

// TestSwitchAgent_FailedSwitch_EmitsNoAgentSwitchedFrame is required case 3
// (§5.2.3): switch_agent returns an error (unknown target agent) -> NO
// agent_switched frame is emitted.
func TestSwitchAgent_FailedSwitch_EmitsNoAgentSwitchedFrame(t *testing.T) {
	mock := mockLLMFailedSwitchAgent(t)
	gw := testutil.StartTestGateway(t,
		testutil.WithAPIBase(mock.URL),
		testutil.WithBearerAuth(),
	)

	conn := wsConnect(t, gw)
	sendMessage(t, conn, "please switch to a nonexistent agent")

	frames := collectFramesUntilDone(t, conn, 10*time.Second)
	if len(frames) == 0 {
		t.Fatal("no frames received — turn did not complete")
	}
	logFrameTypes(t, frames)

	if switched := findFrame(frames, "agent_switched"); switched != nil {
		t.Errorf("expected NO agent_switched frame for a FAILED switch_agent call, got: %+v", switched)
	}
}

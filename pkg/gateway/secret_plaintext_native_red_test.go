package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// FR-070 / BDD-6.8 / chief Q3-A: exercise the ACTUAL native web turn, not
// only tool.Execute or ToolRegistry. Rejected credentials must be value-free
// before preview/log, serialized WS frame, approval and assistant persistence.
func TestWU4B_NativeProviderPlaintextRefusalPrecedesEveryEcho(t *testing.T) {
	for _, policy := range []string{"allow", "ask"} {
		t.Run(policy, func(t *testing.T) { q3RunNativeNoEcho(t, "configure_provider", policy) })
	}
}
func TestWU4B_NativeMCPPlaintextRefusalPrecedesEveryEcho(t *testing.T) {
	for _, policy := range []string{"allow", "ask"} {
		t.Run(policy, func(t *testing.T) { q3RunNativeNoEcho(t, "add_mcp_server", policy) })
	}
}
func TestWU4B_NativeChannelPlaintextRefusalPrecedesEveryEcho(t *testing.T) {
	for _, policy := range []string{"allow", "ask"} {
		t.Run(policy, func(t *testing.T) { q3RunNativeNoEcho(t, "configure_channel", policy) })
	}
}

func q3RunNativeNoEcho(t *testing.T, name, policy string) {
	t.Helper()
	f := newQ3NativeFixture(t, name, policy)
	f.handler.handleChatMessageWithClientID(context.Background(), "wu4b-q3-chat", f.sid,
		"Run the public control, then handle the integration setup.", "omnipus", nil, "", testHarnessWorkspaceMembershipID,
		"wu4b-q3-client", nil, f.conn)
	q3AwaitCompletedTurn(t, f.bus) // reply publication follows actual turn/persistence completion
	q3AssertFrames(t, f)
	f.provider.mu.Lock()
	prompts := append([][]providers.Message(nil), f.provider.prompts...)
	inputs := append([][]byte(nil), f.provider.inputs...)
	f.provider.mu.Unlock()
	require.Len(t, prompts, 2, "real turn must consume the returned tool result, not abort or silently drop the call")
	require.Len(t, inputs, 2, "instrument captures immutable inputs at the actual model boundary")
	q3AssertValueFree(t, "next model input", inputs[1])
	refusals := make([]string, 0, 1)
	for _, message := range prompts[1] {
		if message.Role == "tool" && message.ToolCallID == q3ForbiddenCall {
			refusals = append(refusals, message.Content)
		}
	}
	require.Len(t, refusals, 1, "the actual forbidden call receives exactly one refusal")
	assert.Contains(t, strings.ToLower(refusals[0]), "request a secret")
	assert.Contains(t, refusals[0], "failed: nothing saved", "refusal remains the structured change-tool outcome")
	// Mirror every field for test-only capture; keep the original JSON keys.
	type approvalSnapshot struct {
		ToolCallID string         `json:"ToolCallID"`
		ToolName   string         `json:"ToolName"`
		Args       map[string]any `json:"Args"`
		AgentID    string         `json:"AgentID"`
		SessionID  string         `json:"SessionID"`
		TurnID     string         `json:"TurnID"`
	}
	f.approver.mu.Lock()
	approvalCount := len(f.approver.requests)
	var approvalRequests []approvalSnapshot
	if f.approver.requests != nil {
		approvalRequests = make([]approvalSnapshot, approvalCount)
		for i, request := range f.approver.requests {
			approvalRequests[i] = approvalSnapshot{
				ToolCallID: request.ToolCallID, ToolName: request.ToolName, Args: request.Args,
				AgentID: request.AgentID, SessionID: request.SessionID, TurnID: request.TurnID,
			}
		}
	}
	approvalBytes, err := json.Marshal(approvalRequests)
	f.approver.mu.Unlock()
	require.NoError(t, err)
	q3AssertValueFree(t, "approval request payload", approvalBytes)
	assert.Zero(t, approvalCount, "plaintext must be refused BEFORE an approval can display its args")
	assert.Zero(t, f.saves, "no integration config save")
	assert.Zero(t, f.reloads)
	assert.Zero(t, f.reconciles)
	assert.Zero(t, f.listings, "no network probe for a refused credential")
	configAfter, err := os.ReadFile(f.configPath)
	require.NoError(t, err)
	assert.Zero(t, bytes.Compare(f.configBefore, configAfter), "exact persisted config unchanged")
	storeAfter, err := os.ReadFile(filepath.Join(f.home, "credentials.json"))
	require.NoError(t, err)
	assert.Zero(t, bytes.Compare(f.storeBefore, storeAfter), "exact encrypted store unchanged")
	memoryAfter, err := json.Marshal(f.al.GetConfig())
	require.NoError(t, err)
	assert.Zero(t, bytes.Compare(f.memoryBefore, memoryAfter), "live selected consumers unchanged")
	entries, err := f.al.GetSessionStore().ReadTranscript(f.sid)
	require.NoError(t, err)
	require.NotEmpty(t, entries, "instrument: actual web input and tool calls persisted")
	transcript, err := json.Marshal(entries)
	require.NoError(t, err)
	assert.Contains(t, string(transcript), q3PublicControl, "public control persisted, not a blank transcript")
	q3AssertValueFree(t, "assistant tool-call transcript", transcript)
	f.join(t) // no tail writes/goroutines may race the fresh-store/disk inspection
	fresh, err := session.NewUnifiedStore(filepath.Join(f.home, "sessions"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, fresh.Close()) })
	reloaded, err := fresh.ReadTranscript(f.sid)
	require.NoError(t, err)
	reloadedJSON, err := json.Marshal(reloaded)
	require.NoError(t, err)
	assert.Contains(t, string(reloadedJSON), q3PublicControl)
	q3AssertValueFree(t, "freshly reloaded transcript", reloadedJSON)
	logBytes, err := os.ReadFile(f.logPath)
	require.NoError(t, err)
	assert.Contains(t, string(logBytes), q3PublicControl, "instrument: real log sink was live")
	q3AssertValueFree(t, "native/registry argument-preview logs", logBytes)
	q3AssertPersistedTree(t, f.home) // includes real model-window/archive/audit bytes
}

// The sync hub enqueues native events before the completed outbound reply.
// A generated PongFrame after that reply is a FIFO drain barrier, not a fake
// product event. Reading through it proves we inspected every earlier frame;
// no sleep/quiet-window guess is used to infer absence.
func q3AssertFrames(t *testing.T, f *q3NativeFixture) {
	t.Helper()
	barrier, err := json.Marshal(gen.PongFrame{Type: "pong"})
	require.NoError(t, err)
	require.True(t, f.conn.enqueue(barrier), "frame-capture barrier must enter the real ordered queue")
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	controlStarts := 0
	for {
		select {
		case raw := <-f.conn.sendCh:
			q3AssertValueFree(t, "serialized live WebSocket frame", raw)
			var frame map[string]any
			require.NoError(t, json.Unmarshal(raw, &frame))
			if frame["type"] == "pong" {
				assert.Equal(t, 1, controlStarts, "instrument: non-secret tool arguments still reach a real serialized start frame")
				return
			}
			if frame["type"] == "tool_call_start" {
				var start gen.ToolCallStartFrame
				require.NoError(t, json.Unmarshal(raw, &start))
				if start.Tool == q3ControlTool {
					controlStarts++
					assert.Equal(t, f.sid, start.SessionId)
					assert.Equal(t, map[string]any{"label": q3PublicControl}, start.Params)
				}
			}
		case <-deadline.C:
			t.Fatal("ordered frame capture did not reach its barrier; absence is UNVERIFIED")
		}
	}
}

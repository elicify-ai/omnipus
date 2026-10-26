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

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// T2 / Q3-A / FR-062/070: this is not another initially-plaintext model call.
// Native normalization sees a safe public tool. A counted BeforeTool hook then
// rewrites its name AND arguments to protected plaintext. Only the later
// guard can keep approval, preview/start, logging and persistence value-free.
func TestWU4Selection_PostHookPlaintextRefusalPrecedesEveryEcho(t *testing.T) {
	for _, target := range []string{"configure_provider", "add_mcp_server", "configure_channel"} {
		for _, policy := range []string{"allow", "ask"} {
			t.Run(target+"/"+policy, func(t *testing.T) {
				f := newQ3NativeFixture(t, target, policy)
				f.provider.mu.Lock()
				f.provider.tool = q3ControlTool
				f.provider.args = map[string]any{"label": selectionHookCandidate}
				f.provider.mu.Unlock()
				hook := &selectionPlaintextHook{target: target}
				require.NoError(t, f.al.MountHook(agent.NamedHook("selection-plaintext-rewrite", hook)))
				f.handler.handleChatMessageWithClientID(context.Background(), "wu4b-q3-chat", f.sid,
					"Execute the public controls with the mounted hook.", "omnipus", nil, "", testHarnessWorkspaceMembershipID,
					"selection-hook-client", nil, f.conn)
				q3AwaitCompletedTurn(t, f.bus)
				selectionHookAssertFrames(t, f)
				f.join(t) // join the real native turn before reading persisted bytes

				hook.mu.Lock()
				assert.Equal(t, 1, hook.protectedRewrites, "instrument: exactly one safe call reached the rewriting hook")
				assert.Equal(t, 1, hook.publicRewrites, "instrument: ordinary public hook rewrite was not blocked")
				require.Len(t, hook.beforeTargetInputs, 1)
				assert.JSONEq(t, `{"label":"`+selectionHookCandidate+`"}`, string(hook.beforeTargetInputs[0]), "pre-hook input is safe, so the earliest native refusal cannot impersonate the post-hook guard")
				assert.Equal(t, []string{selectionHookPublic}, hook.publicResults, "real public execution uses the hook-modified argument")
				assert.Equal(t, []string{q3ControlTool}, hook.approvedTools, "only public control reaches the approval-hook boundary")
				for _, raw := range hook.approvalInputs {
					q3AssertValueFree(t, "post-hook approval hook input", raw)
				}
				hook.mu.Unlock()
				f.approver.mu.Lock()
				assert.Zero(t, len(f.approver.requests), "protected rewrite refused before policy Ask/approval UI")
				f.approver.mu.Unlock()
				selectionHookAssertModelRefusal(t, f)
				selectionHookAssertPersisted(t, f)
			})
		}
	}
}

func selectionHookAssertModelRefusal(t *testing.T, f *q3NativeFixture) {
	t.Helper()
	f.provider.mu.Lock()
	defer f.provider.mu.Unlock()
	require.Len(t, f.provider.prompts, 2, "native turn consumes actual result then finishes")
	require.Len(t, f.provider.inputs, 2)
	q3AssertValueFree(t, "next model input after hook rewrite", f.provider.inputs[1])
	var refusals, publicResults []string
	for _, message := range f.provider.prompts[1] {
		if message.Role != "tool" {
			continue
		}
		if message.ToolCallID == q3ForbiddenCall {
			refusals = append(refusals, message.Content)
		}
		if message.ToolCallID == "wu4b-q3-control" {
			publicResults = append(publicResults, message.Content)
		}
	}
	require.Len(t, refusals, 1)
	assert.Contains(t, strings.ToLower(refusals[0]), "request a secret")
	assert.Contains(t, refusals[0], "failed: nothing saved", "post-hook rejection uses ordinary safe synthetic-result admission")
	assert.Equal(t, []string{selectionHookPublic}, publicResults)
}

func selectionHookAssertPersisted(t *testing.T, f *q3NativeFixture) {
	t.Helper()
	assert.Zero(t, f.saves)
	assert.Zero(t, f.reloads)
	assert.Zero(t, f.reconciles)
	assert.Zero(t, f.listings)
	memory, err := json.Marshal(f.al.GetConfig())
	require.NoError(t, err)
	assert.Equal(t, f.memoryBefore, memory)
	configBytes, err := os.ReadFile(f.configPath)
	require.NoError(t, err)
	assert.Equal(t, f.configBefore, configBytes)
	storeBytes, err := os.ReadFile(filepath.Join(f.home, "credentials.json"))
	require.NoError(t, err)
	assert.Equal(t, f.storeBefore, storeBytes)
	fresh, err := session.NewUnifiedStore(filepath.Join(f.home, "sessions"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, fresh.Close()) })
	entries, err := fresh.ReadTranscript(f.sid)
	require.NoError(t, err)
	raw, err := json.Marshal(entries)
	require.NoError(t, err)
	assert.Contains(t, string(raw), selectionHookPublic, "public hook result persisted; this is not an empty transcript")
	q3AssertValueFree(t, "fresh persisted post-hook transcript", raw)
	logBytes, err := os.ReadFile(f.logPath)
	require.NoError(t, err)
	assert.Contains(t, string(logBytes), selectionHookPublic, "exact hook-modified public arguments/result reached the actual file logger")
	q3AssertValueFree(t, "post-hook native/registry argument logs", logBytes)
	q3AssertPersistedTree(t, f.home) // includes model-window/archive/audit files
}

func selectionHookAssertFrames(t *testing.T, f *q3NativeFixture) {
	t.Helper()
	barrier, err := json.Marshal(gen.PongFrame{Type: "pong"})
	require.NoError(t, err)
	require.True(t, f.conn.enqueue(barrier))
	deadline := time.NewTimer(10 * time.Second) // same bound as the existing ordered Q3 fixture
	defer deadline.Stop()
	publicStarts, targetStarts := 0, 0
	for {
		select {
		case raw := <-f.conn.sendCh:
			q3AssertValueFree(t, "post-hook serialized WebSocket frame", raw)
			var frame map[string]any
			require.NoError(t, json.Unmarshal(raw, &frame))
			if frame["type"] == "pong" {
				assert.Equal(t, 1, publicStarts, "instrument: public hook rewrite reaches real start frame")
				assert.Zero(t, targetStarts, "protected rewrite must be refused before a tool start frame")
				return
			}
			if frame["type"] == "tool_call_start" {
				var start gen.ToolCallStartFrame
				require.NoError(t, json.Unmarshal(raw, &start))
				if start.CallId == q3ForbiddenCall {
					targetStarts++
				}
				if start.CallId == "wu4b-q3-control" {
					publicStarts++
					assert.Equal(t, q3ControlTool, start.Tool)
					assert.Equal(t, f.sid, start.SessionId)
					assert.Equal(t, map[string]any{"label": selectionHookPublic}, start.Params)
					assert.True(t, bytes.Contains(raw, []byte(selectionHookPublic)))
				}
			}
		case <-deadline.C:
			t.Fatal("post-hook frame capture never reached its ordered drain barrier; absence is UNVERIFIED")
		}
	}
}

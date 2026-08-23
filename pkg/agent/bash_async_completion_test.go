// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// This file closes the single most important test-coverage gap identified in
// the ADR-036 bash/delegate consolidation fix-wave review: NO test anywhere
// exercised bash's real background-completion -> AsyncNotifier wiring through
// ExecuteAsync. Every pre-existing test in pkg/tools/bash_test.go calls
// ExecTool.Execute (the synchronous path, cb always nil); pkg/agent's own
// async_notifier_test.go calls al.asyncNotifier.Notify directly, never routing
// through a REAL *tools.ExecTool. A regression that silently dropped the
// callback wiring anywhere along the real chain —
//
//	runTurn's asyncCallback closure (loop.go)
//	  -> ToolRegistry.ExecuteWithContext (registry.go)
//	    -> ExecTool.ExecuteAsync (shell.go)
//	      -> runBackground's completion goroutine's `if cb != nil { cb(...) }`
//	  -> AsyncNotifier.Notify (async_notifier.go)
//	    -> bus.PublishInbound
//	  -> processSystemMessage (loop.go) -> a genuine second LLM turn
//
// — would pass every test that existed before this file. These two tests
// drive that ENTIRE chain for real: a real *tools.ExecTool registered on a
// real AgentLoop, a scripted LLM turn that calls `bash` with
// run_in_background=true, and assertions on the resulting bus message AND a
// genuine follow-up provider call.
//
// Traces to: docs/internal/specs/async-notifier-spec.md Test Implementation
// Order #8/#9 (lines 302-303), BDD Scenarios at lines 143 and 157,
// FR-N9 (line 350); docs/internal/specs/bash-tool-spec.md FR-B9 (line 667).

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// sessionCapturingBash wraps a REAL *tools.ExecTool and delegates every call
// to it unchanged — it exists purely so the test can learn the session_id a
// real run_in_background=true call produced (which a scripted LLM cannot
// "read" back out of a canned response). It is NOT a synthetic substitute for
// bash: ExecuteAsync below calls straight through to the real
// tools.ExecTool.ExecuteAsync, which is the exact production method under
// test — this wrapper only peeks at the (real) JSON response afterward.
type sessionCapturingBash struct {
	*tools.ExecTool

	mu                       sync.Mutex
	sessionID                string
	ownerTranscriptSessionID string
}

func (s *sessionCapturingBash) ExecuteAsync(
	ctx context.Context,
	args map[string]any,
	cb tools.AsyncCallback,
) *tools.ToolResult {
	result := s.ExecTool.ExecuteAsync(ctx, args, cb)
	if result != nil && !result.IsError {
		var resp tools.ExecResponse
		if err := json.Unmarshal([]byte(result.ForLLM), &resp); err == nil && resp.SessionID != "" {
			s.mu.Lock()
			s.sessionID = resp.SessionID
			// Capture the REAL owning transcript session id that turn.go's
			// turnCtx carried into this call (tools.WithTranscriptSessionID at
			// loop.go:7127) — this is the exact value shell.go's runBackground
			// stamps onto ProcessSession.OwnerSessionID (shell.go:571-572). A
			// production kill of this session must present the SAME id via
			// ToolTranscriptSessionID(ctx), or SessionManager.GetOwned (M5 fix,
			// pkg/tools/session.go:722) denies it — see this file's kill test.
			s.ownerTranscriptSessionID = tools.ToolTranscriptSessionID(ctx)
			s.mu.Unlock()
		}
	}
	return result
}

func (s *sessionCapturingBash) capturedSessionID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessionID
}

// capturedOwnerTranscriptSessionID returns the transcript session id that
// owns the background session captured above — the value a legitimate kill
// must present via tools.WithTranscriptSessionID for
// SessionManager.GetOwned to authorize it.
func (s *sessionCapturingBash) capturedOwnerTranscriptSessionID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ownerTranscriptSessionID
}

// newBashAsyncTestLoop builds a real AgentLoop wired with a real bash tool
// (godMode=true so background exec skips sandbox hardening plumbing that has
// nothing to do with what this test verifies) and grants every seeded agent
// "bash: allow" so the scripted turn is never blocked by FR-B12's
// deny-by-default seed. Returns the loop, its bus, the scripted provider (so
// the test can assert CallCount), and the capturing wrapper.
func newBashAsyncTestLoop(t *testing.T, provider *testutil.ScenarioProvider) (
	*AgentLoop, *bus.MessageBus, *sessionCapturingBash,
) {
	t.Helper()
	tmpHome := t.TempDir()
	workspaceDir := filepath.Join(tmpHome, "workspace")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o755))

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              workspaceDir,
				DefaultModel:      config.DefaultModel{Model: "scripted-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia", Home: workspaceDir}},
		},
	}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	t.Cleanup(al.Close)

	execTool, err := tools.NewExecToolWithDeps(workspaceDir, false, nil, tools.ExecToolDeps{GodMode: true})
	require.NoError(t, err)
	wrapper := &sessionCapturingBash{ExecTool: execTool}
	al.RegisterTool(wrapper)

	// Grant "bash: allow" on every seeded agent — sidesteps FR-B12's new
	// deny-by-default seed, which is out of scope for this file (covered by
	// pkg/sysagent/tools/agent_test.go's TestBash_NewCustomAgentDeniedByDefault).
	for _, agentID := range al.GetRegistry().ListAgentIDs() {
		ag, ok := al.GetRegistry().GetAgent(agentID)
		if !ok {
			continue
		}
		ag.StoreToolPolicy(&tools.ToolPolicyCfg{
			Policies: map[string]config.ToolPolicy{"bash": "allow"},
		})
	}

	return al, msgBus, wrapper
}

// TestBashRunInBackground_CompletionTriggersNewTurn drives the REAL chain
// described in this file's header comment for a command that completes
// successfully. Traces to async-notifier-spec.md's BDD Scenario "Backgrounded
// bash command wakes the agent on completion" (line 143) and Test
// Implementation Order #8 (line 302); also closes bash-tool-spec.md's FR-B9
// (line 667), which cites this exact test name.
func TestBashRunInBackground_CompletionTriggersNewTurn(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses POSIX sleep/echo")
	}

	// A distinctive marker proves the notification carries the REAL command's
	// real output — not a hardcoded/synthetic string a broken implementation
	// could fake regardless of what command was actually run.
	const marker = "ASYNC-BASH-COMPLETE-7f3ce1"

	provider := testutil.NewScenario().
		WithToolCall("bash", fmt.Sprintf(`{"command":"sleep 1; echo %s","run_in_background":true}`, marker)).
		WithText("Started the background job; I'll let you know when it's done.").
		WithText("Acknowledged — the background job finished.")

	al, msgBus, _ := newBashAsyncTestLoop(t, provider)

	const sessionKey = "test-session-bash-async-complete"
	ctx := context.Background()
	// "telegram"/"12345" chosen to match the BDD scenario's literal example
	// AND because "cli" is an internal channel that processSystemMessage
	// silently skips (pkg/constants/channels.go) — using it here would make
	// the follow-up-turn assertion below vacuously true.
	firstTurnResp, err := al.ProcessDirectWithChannel(
		ctx,
		"please run a background command",
		sessionKey,
		"telegram",
		"12345",
	)
	require.NoError(t, err)
	assert.Contains(t, firstTurnResp, "Started")
	require.Equal(t, 2, provider.CallCount(), "turn 1 must consume exactly the tool-call + text steps")

	// Wait for the REAL production wiring — runBackground's completion
	// goroutine firing cb, which loop.go's asyncCallback turns into a
	// AsyncNotifier.Notify call, which publishes to the bus — to fire.
	var msg bus.InboundMessage
	select {
	case msg = <-msgBus.InboundChan():
	case <-time.After(15 * time.Second):
		t.Fatal("BLOCKED: timed out waiting for the async completion notification — " +
			"the bash->AsyncNotifier callback wiring appears to be broken or dropped")
	}

	// (a) the callback fired with the right shape — sender/channel/chat must
	// route back to the ORIGINATING call, not be dropped or mislabeled.
	assert.Equal(t, "system", msg.Channel, "AsyncNotifier always publishes on the system channel")
	assert.Equal(t, "async:bash", msg.Sender.CanonicalID, "SourceKind must be \"bash\", not dropped or renamed")
	assert.Equal(t, "telegram:12345", msg.ChatID, "must carry the ORIGINATING channel/chat, not a placeholder")

	// (b) content indicates SUCCESS and includes the real command's real
	// output — proves this isn't a hardcoded response regardless of what ran.
	assert.Contains(t, msg.Content, marker, "content must include the command's actual output")
	assert.Contains(t, msg.Content, "finished", "success case must read as a normal completion")
	assert.NotContains(t, msg.Content, "killed", "success case must not claim termination")
	assert.NotContains(t, msg.Content, "timed out", "success case must not claim a timeout")

	// Drain: exactly one notification for exactly one completion — a second
	// stray publish would indicate cb firing more than once.
	select {
	case extra := <-msgBus.InboundChan():
		t.Fatalf("cb must fire exactly once; got a second inbound message: %+v", extra)
	case <-time.After(300 * time.Millisecond):
	}

	// Prove a genuine SECOND turn actually runs off this notification (not
	// just a message parked unread on the bus) — the exact dispatch path
	// AgentLoop.Run's select loop uses for any channel=="system" message.
	_, err = al.processSystemMessage(ctx, msg)
	require.NoError(t, err)
	assert.Equal(t, 3, provider.CallCount(),
		"the async completion must have triggered a genuine THIRD scripted provider call — "+
			"not just publish-and-ignore")
}

// TestBashRunInBackground_KillReportsTermination drives the same real chain
// as above, but for the kill path: after a real background session is
// started via the wired production path, it is killed via action="kill", and
// the ORIGINAL callback captured at start time (not a new one) must fire with
// content indicating termination, not success. Traces to
// async-notifier-spec.md's BDD Scenario "Killed background command reports
// termination, not success" (line 157) and Test Implementation Order #9
// (line 303).
func TestBashRunInBackground_KillReportsTermination(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses POSIX sleep")
	}

	provider := testutil.NewScenario().
		WithToolCall("bash", `{"command":"sleep 30","run_in_background":true}`).
		WithText("Started a long-running background job.").
		WithText("Understood — it was terminated.")

	al, msgBus, wrapper := newBashAsyncTestLoop(t, provider)

	const sessionKey = "test-session-bash-async-kill"
	ctx := context.Background()
	firstTurnResp, err := al.ProcessDirectWithChannel(
		ctx,
		"please start a long background job",
		sessionKey,
		"telegram",
		"12345",
	)
	require.NoError(t, err)
	assert.Contains(t, firstTurnResp, "Started")

	sessionID := wrapper.capturedSessionID()
	require.NotEmpty(t, sessionID, "the real run_in_background call must have produced a session_id")

	ownerTranscriptSessionID := wrapper.capturedOwnerTranscriptSessionID()
	require.NotEmpty(t, ownerTranscriptSessionID,
		"the real run_in_background call must have carried an owning transcript session id "+
			"(turn.go's turnCtx, stamped via tools.WithTranscriptSessionID at loop.go:7127)")

	// Kill via the SAME registered tool instance — this is the real
	// production executeKill code path, triggering the ALREADY-RUNNING
	// completion goroutine (spawned by the turn-1 call above, holding the
	// REAL production cb in its closure) to observe termination and fire it.
	//
	// The kill context MUST carry the session's OWNING transcript session id
	// via tools.WithTranscriptSessionID, mirroring how a real caller invokes
	// this: every production kill happens inside a turn, and turn.go's
	// turnCtx always carries ToolTranscriptSessionID (loop.go:7127) before any
	// tool executes. A bare context.Background() (no transcript session id)
	// is not a shape any real caller produces — it made this test fail
	// against SessionManager.GetOwned's M5 ownership gate (pkg/tools/session.go),
	// which correctly denies a kill whose caller doesn't own the session (a
	// confirmed real privilege-boundary fix, not a bug to route around).
	killCtx := tools.WithTranscriptSessionID(context.Background(), ownerTranscriptSessionID)
	killResult := wrapper.Execute(killCtx, map[string]any{
		"action":     "kill",
		"session_id": sessionID,
	})
	require.NotNil(t, killResult)
	require.False(t, killResult.IsError, "kill call itself must succeed, got ForLLM=%q", killResult.ForLLM)

	var msg bus.InboundMessage
	select {
	case msg = <-msgBus.InboundChan():
	case <-time.After(15 * time.Second):
		t.Fatal("BLOCKED: timed out waiting for the kill-path async notification — " +
			"the bash kill->AsyncNotifier callback wiring appears to be broken or dropped")
	}

	// (a) fired exactly once, with the right routing.
	assert.Equal(t, "system", msg.Channel)
	assert.Equal(t, "async:bash", msg.Sender.CanonicalID)
	assert.Equal(t, "telegram:12345", msg.ChatID)

	// (b) content indicates TERMINATION, not success — the whole point of
	// this test. A regression that always reports "finished" regardless of
	// how the session ended would be caught here.
	assert.Contains(t, msg.Content, "killed", "kill case must explicitly report termination")
	assert.NotContains(t, msg.Content, "finished (exit code", "kill case must NOT read as a normal success completion")

	select {
	case extra := <-msgBus.InboundChan():
		t.Fatalf("cb must fire exactly once; got a second inbound message: %+v", extra)
	case <-time.After(300 * time.Millisecond):
	}

	_, err = al.processSystemMessage(ctx, msg)
	require.NoError(t, err)
	assert.Equal(t, 3, provider.CallCount(),
		"the kill notification must have triggered a genuine THIRD scripted provider call")
}

// TestBashRunInBackground_KillOwnershipGate_EndToEnd closes the exact
// coverage gap that let commit c5ada0cb's M5 ownership fix (getSessionArg's
// SessionManager.GetOwned call, pkg/tools/session.go:722) break
// TestBashRunInBackground_KillReportsTermination unnoticed: nothing exercised
// that the LEGITIMATE owner's kill still succeeds when driven through the
// REAL agent-level wiring (ProcessDirectWithChannel -> turn.go's turnCtx,
// stamped with ToolTranscriptSessionID at loop.go:7127 -> ExecTool.Execute).
// pkg/tools/session_ownership_test.go's TestBash_CrossSessionPollReadKill_Denied
// already covers denial directly against a bare *tools.ExecTool; this test
// is its positive-path, agent-level counterpart (Binding Rule 4: pair a
// negative assertion with a positive lower bound).
//
// Negative half: a kill presented with no owning transcript session id, and
// one presented with a genuinely different (non-empty) one, are both denied
// with the generic "session not found" message GetOwned uses to avoid
// leaking a foreign session's existence.
// Positive half: a kill presented with the SAME owning transcript session id
// captured off the real turnCtx that started the session succeeds.
func TestBashRunInBackground_KillOwnershipGate_EndToEnd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses POSIX sleep")
	}

	provider := testutil.NewScenario().
		WithToolCall("bash", `{"command":"sleep 30","run_in_background":true}`).
		WithText("Started a long-running background job.")

	al, _, wrapper := newBashAsyncTestLoop(t, provider)

	ctx := context.Background()
	firstTurnResp, err := al.ProcessDirectWithChannel(
		ctx,
		"please start a long background job",
		"test-session-bash-ownership-gate",
		"telegram",
		"99999",
	)
	require.NoError(t, err)
	assert.Contains(t, firstTurnResp, "Started")

	sessionID := wrapper.capturedSessionID()
	require.NotEmpty(t, sessionID, "the real run_in_background call must have produced a session_id")
	ownerTranscriptSessionID := wrapper.capturedOwnerTranscriptSessionID()
	require.NotEmpty(t, ownerTranscriptSessionID,
		"the real run_in_background call must have carried an owning transcript session id")

	// Negative: no transcript session id at all (the exact shape that broke
	// TestBashRunInBackground_KillReportsTermination before this fix-wave —
	// proving the gate STILL denies it is what guards against re-introducing
	// that regression by weakening the gate instead of the fixture).
	deniedEmpty := wrapper.Execute(context.Background(), map[string]any{
		"action":     "kill",
		"session_id": sessionID,
	})
	require.NotNil(t, deniedEmpty)
	assert.True(t, deniedEmpty.IsError, "kill with no transcript session id must be denied")
	assert.Contains(t, deniedEmpty.ForLLM, "session not found",
		"denial must read as a generic not-found, never a distinguishable forbidden")

	// Negative: a genuinely different (non-empty) transcript session id —
	// proves the gate does a real equality check, not just an empty-string
	// special case.
	foreignCtx := tools.WithTranscriptSessionID(context.Background(), "some-other-unrelated-session-id")
	deniedForeign := wrapper.Execute(foreignCtx, map[string]any{
		"action":     "kill",
		"session_id": sessionID,
	})
	require.NotNil(t, deniedForeign)
	assert.True(t, deniedForeign.IsError, "kill from a genuinely foreign transcript session id must also be denied")
	assert.Contains(t, deniedForeign.ForLLM, "session not found")

	// Positive lower bound: the legitimate owner, driven through the exact
	// real agent-level wiring that started the session, succeeds — this is
	// the path that regressed and is the actual point of this test.
	ownerCtx := tools.WithTranscriptSessionID(context.Background(), ownerTranscriptSessionID)
	okResult := wrapper.Execute(ownerCtx, map[string]any{
		"action":     "kill",
		"session_id": sessionID,
	})
	require.NotNil(t, okResult)
	assert.False(t, okResult.IsError, "kill from the OWNING transcript session id must succeed, got ForLLM=%q", okResult.ForLLM)
}

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Regression test for FIX 2 (sendfile-fix review): runTurn's own deferred
// Finish call (loop.go) used to always pass a hardcoded `false`. For a turn
// hard-aborted via the session-wide web-cancel escalation path
// (RequestCancel -> [3s timer] -> InterruptSessionHard -> ts.requestHardAbort()),
// NOTHING ever calls Finish(true) directly — InterruptSessionHard only sets
// the hardAbort flag and fires providerCancel/turnCancel; only the legacy
// single-session HardAbort()/InterruptHard call Finish(true) explicitly. So
// runTurn's own deferred call was the ONLY Finish call for such a turn, and a
// hardcoded false silently mislabeled a genuine hard abort as a graceful
// finish. That value feeds directly into cancel.go's onCancelFinish callback
// (registered by RequestCancel), which persists it as
// TranscriptEntry.CancelMethod — the exact field
// pkg/gateway/replay.go:turnCancelledContent renders back to the user as
// "Turn canceled (%s)".
//
// This test drives the REAL production sequence end-to-end. It blocks the
// turn INSIDE TOOL EXECUTION rather than inside the LLM call, deliberately:
// RequestCancel's own graceful Phase A (InterruptSession) already fires
// providerCancel() on any CURRENTLY in-flight LLM call as its very first
// action (steering.go, "FR-12a: call providerCancel first"), so a turn
// blocked on the LLM call would already unwind via the plain
// context-canceled/graceful path before this test's simulated hard-abort
// escalation ever got a chance to matter — that was confirmed empirically
// while writing this test (the naive LLM-blocking version raced and always
// observed "graceful", not because FIX 2 was wrong but because the graceful
// phase's own immediate provider-cancel had already ended the turn first).
// Tool execution is NOT touched by the graceful phase's providerCancel; only
// requestHardAbort's turnCancel() reaches it (execCtx is derived from
// turnCtx) — so blocking inside a tool call is the faithful way to observe a
// turn still alive when the hard-abort escalation lands, matching the
// production case (e.g. mid-delegate/mid-tool) this fix protects.

package agent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// hardAbortToolCallProvider returns a single scripted tool call on its first
// Chat invocation. There is deliberately no second scripted step: the turn is
// expected to be hard-aborted while that first tool call is still executing,
// so no follow-up LLM call should ever happen.
type hardAbortToolCallProvider struct{}

func (p *hardAbortToolCallProvider) Chat(
	_ context.Context,
	_ []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	fc := providers.FunctionCall{Name: "hard_abort_test_tool", Arguments: "{}"}
	return &providers.LLMResponse{
		ToolCalls: []providers.ToolCall{{ID: "call_1", Type: "function", Name: "hard_abort_test_tool", Function: &fc}},
	}, nil
}

func (p *hardAbortToolCallProvider) GetDefaultModel() string {
	return "hard-abort-tool-call-mock"
}

func TestRunTurn_HardAbortViaInterruptSessionHard_RecordsCancelMethodHard(t *testing.T) {
	tmpDir := t.TempDir()
	// No "main" sentinel to fall back to anymore (it was deleted along with
	// registry.go's implicit registration) — this test needs a REAL agent in
	// cfg.Agents.List so GetDefaultAgent() has something to resolve to.
	const testAgentID = "mia"
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: testAgentID, Name: "Mia"}},
		},
	}

	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &hardAbortToolCallProvider{})
	t.Cleanup(al.Close)

	started := make(chan struct{})
	al.RegisterTool(&interruptibleTool{name: "hard_abort_test_tool", started: started})

	defaultAgent := al.registry.GetDefaultAgent()
	require.NotNil(t, defaultAgent, "expected default agent")
	// No-default-policy model (CLAUDE.md hard constraint 6): the test tool
	// needs an explicit agent-level grant or it fails closed to deny before
	// ever executing and signaling `started`.
	defaultAgent.StoreToolPolicy(&tools.ToolPolicyCfg{
		Policies: map[string]config.ToolPolicy{"hard_abort_test_tool": "allow"},
	})

	store := al.GetSessionStore()
	require.NotNil(t, store, "shared session store must be non-nil")

	meta, err := store.NewSession(session.SessionTypeChat, "test-hard-abort", testAgentID)
	require.NoError(t, err)
	sessionID := meta.ID

	type outcome struct {
		resp string
		err  error
	}
	resultCh := make(chan outcome, 1)
	go func() {
		resp, runErr := al.ProcessScheduled(
			context.Background(),
			testAgentID,
			sessionID,
			"please help with something",
			"test-hard-abort",
			"chat1",
		)
		resultCh <- outcome{resp: resp, err: runErr}
	}()

	select {
	case <-started:
		// hard_abort_test_tool is now blocked inside its Execute call, waiting
		// on ctx.Done() — the turn is genuinely in flight, mid-tool-execution.
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for the blocked tool call to start")
	}

	cancelOutcome, cancelErr := al.RequestCancel(
		context.Background(),
		CancelScope{SessionID: sessionID},
		CancelCanceller{UserID: "test-user", Channel: "web"},
		CancelHooks{},
	)
	require.NoError(t, cancelErr)
	require.True(t, cancelOutcome.Fired, "RequestCancel must claim the in-flight turn")

	// Simulate RequestCancel's own PHASE B (3s timer -> InterruptSessionHard)
	// firing immediately, rather than waiting out the real 3 seconds — this
	// calls the exact same production method the real timer calls. Graceful
	// Phase A above does NOT reach a tool already executing (only
	// requestHardAbort's turnCancel() cascades into the tool's execCtx), so
	// the tool is still blocked until this call.
	_, hardErr := al.InterruptSessionHard(sessionID, ScopeSubtree, "test hard escalation")
	require.NoError(t, hardErr)

	select {
	case r := <-resultCh:
		// A turn ended via the hardInterruptAbortReason path returns a nil
		// error — see abortTurn's Case 1 doc comment (loop.go).
		assert.NoError(t, r.err, "a clean hard-abort must not surface a synthesized error")
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for the turn to finish after hard abort")
	}

	// Allow the onCancelFinish callback's AppendTranscript call (fired from
	// inside Finish, itself called from runTurn's deferred call) to land —
	// fileutil.WriteFileAtomic is fast but asynchronous relative to this
	// goroutine's own wakeup from resultCh.
	require.Eventually(t, func() bool {
		entries, readErr := store.ReadTranscript(sessionID)
		if readErr != nil {
			return false
		}
		for i := range entries {
			if entries[i].Type == session.EntryTypeTurnCancelled {
				return true
			}
		}
		return false
	}, 3*time.Second, 20*time.Millisecond, "expected a turn_canceled transcript entry to eventually appear")

	entries, err := store.ReadTranscript(sessionID)
	require.NoError(t, err)

	var canceled *session.TranscriptEntry
	for i := range entries {
		if entries[i].Type == session.EntryTypeTurnCancelled {
			cp := entries[i]
			canceled = &cp
			break
		}
	}
	require.NotNil(t, canceled, "expected a turn_canceled transcript entry; entries found: %+v", entries)
	assert.Equal(t, "hard", canceled.CancelMethod,
		"FIX 2: a turn hard-aborted via InterruptSessionHard must record CancelMethod \"hard\" — "+
			"runTurn's deferred Finish call must reflect ts.hardAbortRequested(), not a hardcoded false "+
			"(which would record \"graceful\" and mislead pkg/gateway/replay.go's "+
			"\"Turn canceled (%%s)\" rendering)")
}

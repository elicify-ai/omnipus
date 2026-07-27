// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Regression test for FIX 3 (sendfile-fix review): an e2e investigator
// reported a mechanism where a canceled turn keeps working — specifically, a
// third turn appearing seconds after a cancel completed, narrating the
// delegation, and in several runs issuing ANOTHER delegate call after the
// user had clicked Stop.
//
// Root cause CONFIRMED by tracing the real production chain (not the
// investigator's original gracefulTerminalUsed hypothesis — see this
// session's report for why that mechanism was refuted): loop.go's generic
// asyncCallback closure (passed to ToolRegistry.ExecuteWithContext as the
// AsyncCallback for delegate/background-bash) fires on its own goroutine
// whenever the async tool finishes, completely independent of whether the
// PARENT turn that dispatched it was canceled in the meantime — which is the
// COMMON case, since async dispatch means the parent moves on immediately.
// Before this fix, the callback called AsyncNotifier.Notify unconditionally,
// which published an inbound "system" message that processSystemMessage
// turned into a BRAND NEW, fully-tooled turn — able to narrate the
// delegation and dispatch further tool calls (including another `delegate`)
// even though the user had already canceled.
//
// The fix gates the Notify call on ts.cancelFired (set exactly once, and
// never reset, by ClaimCancel — see cancel.go's RequestCancel) — the SAME
// turnState the asyncCallback closure captures at dispatch time. This test
// proves the async-notify continuation turn is suppressed once the
// originating turn has been canceled, using a REAL background bash job (the
// same production wiring bash_async_completion_test.go's
// TestBashRunInBackground_CompletionTriggersNewTurn drives) with a
// deterministic synchronization point instead of a timing race: a custom
// BeforeLLM hook claims the cancel on the dispatching turnState BEFORE the
// LLM even decides to call bash — hook execution happens synchronously
// inside runTurn's own goroutine, strictly after registerActiveTurn has
// stored the turnState and strictly before the tool is ever requested, so
// there is no window for the background job to complete first.

package agent

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
)

// cancelOnFirstLLMCallHook is a minimal LLMInterceptor whose BeforeLLM, on
// its first invocation, looks up the dispatching turnState by SessionKey
// (carried on every LLMHookRequest.Meta) and calls ClaimCancel() on it
// directly — reproducing "RequestCancel already claimed this turn"
// (turnState.cancelFired) without invoking the full graceful/hard escalation
// machinery, so the test isolates exactly the signal FIX 3's guard depends
// on. BeforeLLM runs synchronously inside runTurn's own goroutine.
type cancelOnFirstLLMCallHook struct {
	al   *AgentLoop
	once sync.Once
}

func (h *cancelOnFirstLLMCallHook) BeforeLLM(
	_ context.Context,
	req *LLMHookRequest,
) (*LLMHookRequest, HookDecision, error) {
	h.once.Do(func() {
		if val, ok := h.al.activeTurnStates.Load(req.Meta.SessionKey); ok {
			if ts, ok := val.(*turnState); ok {
				ts.ClaimCancel()
			}
		}
	})
	return req, HookDecision{Action: HookActionContinue}, nil
}

func (h *cancelOnFirstLLMCallHook) AfterLLM(
	_ context.Context,
	resp *LLMHookResponse,
) (*LLMHookResponse, HookDecision, error) {
	return resp, HookDecision{Action: HookActionContinue}, nil
}

func TestAsyncNotify_SuppressedWhenOriginatingTurnWasCanceled(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses POSIX sleep/echo")
	}

	const marker = "ASYNC-BASH-POSTCANCEL-9d21f0"
	provider := testutil.NewScenario().
		WithToolCall("bash", fmt.Sprintf(`{"command":"sleep 1; echo %s","run_in_background":true}`, marker)).
		WithText("Started the background job.")

	al, msgBus, _ := newBashAsyncTestLoop(t, provider)

	hook := &cancelOnFirstLLMCallHook{al: al}
	require.NoError(t, al.MountHook(NamedHook("cancel-before-first-llm", hook)))

	const sessionKey = "test-session-bash-async-postcancel"
	ctx := context.Background()
	firstTurnResp, err := al.ProcessDirectWithChannel(
		ctx, "please run a background command", sessionKey, "telegram", "12345",
	)
	require.NoError(t, err)
	assert.Contains(t, firstTurnResp, "Started")

	// The background job sleeps 1s before completing; give it ample margin
	// (well past that) and confirm NO async-notify continuation turn ever
	// arrives on the bus — proving the canceled turn cannot spring back to
	// life via this path.
	select {
	case msg := <-msgBus.InboundChan():
		t.Fatalf("FIX 3 regression: async-notify continuation turn fired for a "+
			"CANCELED originating turn — got inbound message %+v; a canceled "+
			"turn must not be able to start new tool calls (e.g. another delegate)", msg)
	case <-time.After(3 * time.Second):
		// Correct: no continuation turn was triggered.
	}
}

// TestAsyncNotify_FiresNormallyWhenOriginatingTurnWasNotCanceled is the
// control case: without any cancel claim, the SAME background-job wiring
// still fires the continuation turn as before — proving the guard suppresses
// ONLY the canceled case, not async-notify in general.
func TestAsyncNotify_FiresNormallyWhenOriginatingTurnWasNotCanceled(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses POSIX sleep/echo")
	}

	const marker = "ASYNC-BASH-NOCANCEL-3b71ac"
	provider := testutil.NewScenario().
		WithToolCall("bash", fmt.Sprintf(`{"command":"sleep 1; echo %s","run_in_background":true}`, marker)).
		WithText("Started the background job.")

	al, msgBus, _ := newBashAsyncTestLoop(t, provider)

	const sessionKey = "test-session-bash-async-nocancel"
	ctx := context.Background()
	firstTurnResp, err := al.ProcessDirectWithChannel(
		ctx, "please run a background command", sessionKey, "telegram", "12345",
	)
	require.NoError(t, err)
	assert.Contains(t, firstTurnResp, "Started")

	select {
	case msg := <-msgBus.InboundChan():
		assert.Equal(t, "system", msg.Channel)
		assert.Equal(t, "async:bash", msg.Sender.CanonicalID)
	case <-time.After(15 * time.Second):
		t.Fatal("expected the async-notify continuation turn to fire when the " +
			"originating turn was never canceled")
	}
}

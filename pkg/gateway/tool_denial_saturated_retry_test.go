// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-058 (tool-denial semantics) — W6/Wave 2: BDD-07 / AC-06, THE positive
// lower bound (spec §3.5, Binding Rule 4).
//
// Traces to: docs/internal/specs/adr-058-tool-denial-semantics-spec.md §6
// BDD-07 (line ~403) and §7's traceability row "FR-058-16 | D1 #3 | BDD-07 |
// pkg/gateway::tool_denial_saturated_retry_test.go | AC-06".
//
// Why this file is the single highest-priority test in the whole W6 unit:
// without a genuine positive-retry proof, an implementation that quarantines
// on the FIRST denial of every reason — or classifies every reason as
// permanent — would pass every OTHER acceptance criterion in this spec
// (AC-01 through AC-05) completely (spec §8.2's own AC-06 row). This file
// exists to make that stub fail.
//
// Real-mechanism design (no spy — spec §8.1): a real approvalRegistryV2
// capped at max_pending=1 is deliberately saturated by a DIRECTLY-created
// holder entry (simulating some unrelated in-flight approval occupying the
// only slot) before the turn under test ever calls RequestApproval. The
// tool under test ("web_fetch") is then genuinely denied "saturated" on its
// first attempt, and genuinely approved and genuinely EXECUTED on its
// second — proven via the tool's own atomic execution counter and the real
// ToolExecEnd event payload, never a scripted/faked result string.
//
// syncedApprover below is NOT a spy standing in for a canned approval
// outcome — every call it receives is forwarded to the REAL
// policyApproverAdapter for the REAL decision. Its only job is to pause
// deterministically between the two real RequestApproval calls so the test
// can release the holder without a data race against the turn's own second
// dispatch (both run in the SAME real registry with no other coordination
// point available). Without this pause, releasing the holder from the test
// goroutine could race the turn goroutine's second RequestApproval call,
// occasionally saturating THAT one too and flaking the test — the pause
// removes that race, it does not fabricate the retry's outcome.
package gateway

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	agentpkg "github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// distinctFetchCall builds a single-tool-call step with an explicit,
// unique ID — WithToolCall (singular) always hardcodes ID = name + "-0",
// which would collide if used twice for the same tool name across two
// scripted steps in this test.
func distinctFetchCall(toolName, id string) providers.ToolCall {
	fc := providers.FunctionCall{Name: toolName, Arguments: "{}"}
	return providers.ToolCall{ID: id, Function: &fc}
}

// fetchRetryStub is a tool literally named "web_fetch" (Binding Rule 4's
// fixture: "tools bash (permanent path) and web_fetch (saturated path)").
// Its Execute produces a DISTINCT, unpredictable-if-faked marker string per
// call, tracked by a real atomic counter — a stub or a fabricated success
// path cannot reproduce "the real Execute ran and its real content reached
// the turn" without actually calling this method.
type fetchRetryStub struct {
	tools.BaseTool
	calls atomic.Int32
}

func (f *fetchRetryStub) Name() string { return "web_fetch" }
func (f *fetchRetryStub) Description() string {
	return "test stub named web_fetch — real execution tracked"
}
func (f *fetchRetryStub) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (f *fetchRetryStub) Scope() tools.ToolScope { return tools.ScopeGeneral }
func (f *fetchRetryStub) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	n := f.calls.Add(1)
	return &tools.ToolResult{ForLLM: fmt.Sprintf("FETCHED-REAL-CONTENT-marker-%d", n), IsError: false}
}

// syncedApprover wraps a real agent.PolicyApprover and pauses its SECOND
// invocation until the test signals proceed — see this file's header
// comment for why this pause exists and why it is not a spy.
type syncedApprover struct {
	inner             agentpkg.PolicyApprover
	calls             atomic.Int32
	secondCallStarted chan struct{}
	proceed           chan struct{}
}

func (s *syncedApprover) RequestApproval(ctx context.Context, req agentpkg.PolicyApprovalReq) (bool, string) {
	n := s.calls.Add(1)
	if n == 2 {
		close(s.secondCallStarted)
		<-s.proceed
	}
	return s.inner.RequestApproval(ctx, req)
}

// TestSaturatedDenial_PositiveLowerBound_RetrySucceedsAfterQueueDrains is
// BDD-07 / AC-06. Given a real registry capped at 1 pending approval, with
// the only slot already held by an unrelated pending entry:
//
//  1. The agent's first "web_fetch" call is denied "saturated" (permanent
//     false) — asserted from the ACTUAL message the scripted LLM provider
//     received (real content, not a call-count spy).
//  2. Once the holder is released and the retry is approved through the
//     SAME real registry, web_fetch's Execute genuinely runs (atomic
//     counter == 1) and its real result text reaches the turn's tool-exec-end
//     event.
//  3. The turn completes normally (no abort) — a saturated denial must never
//     quarantine the tool (FR-058-16).
func TestSaturatedDenial_PositiveLowerBound_RetrySucceedsAfterQueueDrains(t *testing.T) {
	tmpHome := t.TempDir()

	const agentID = "jim"        // Binding Rule 4 fixture: the allowed agent
	const toolName = "web_fetch" // Binding Rule 4 fixture: the saturated-path tool

	reg := newApprovalRegistryV2(1, 30*time.Second) // long timeout: resolution is explicit, never via timer

	// Directly occupy the sole pending slot with an UNRELATED entry (a
	// different tool name so it can never be confused with the web_fetch
	// entries under test) — simulating some other in-flight approval that
	// happens to be occupying the registry's one slot when the agent's own
	// turn starts.
	holderEntry, accepted := reg.requestApproval(
		"tc-w6-saturated-holder", "other_tool",
		map[string]any{}, "holder-agent", "holder-session", "holder-turn",
	)
	require.True(t, accepted, "the holder must be the FIRST request, so it occupies the only slot")

	// Empty args: fetchRetryStub's Parameters() declares no properties (matching
	// this package's other stub tools), so the args payload must be "{}" — a
	// non-empty object here would fail the tool registry's own schema
	// validation BEFORE Execute is ever reached, which would make this test
	// fail for a reason unrelated to ADR-058.
	provider := testutil.NewScenario().
		WithToolCalls([]providers.ToolCall{distinctFetchCall(toolName, "web_fetch-attempt-1")}).
		WithToolCalls([]providers.ToolCall{distinctFetchCall(toolName, "web_fetch-attempt-2")}).
		WithText("fetched successfully on retry")

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpHome,
				DefaultModel:      config.DefaultModel{Model: "scripted-model"},
				MaxTokens:         4096,
				MaxToolIterations: 20,
			},
			List: []config.AgentConfig{
				{ID: agentID, Home: tmpHome, Model: &config.AgentModelConfig{Primary: "scripted-model"}},
			},
		},
		Sandbox: config.OmnipusSandboxConfig{AuditLog: true},
	}

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, provider)
	defer al.Close()

	stub := &fetchRetryStub{}
	al.RegisterTool(stub)
	for _, id := range al.GetRegistry().ListAgentIDs() {
		if inst, ok := al.GetRegistry().GetAgent(id); ok {
			inst.StoreToolPolicy(&tools.ToolPolicyCfg{
				Policies: map[string]config.ToolPolicy{toolName: config.ToolPolicyAsk},
			})
		}
	}

	sync := &syncedApprover{
		inner:             newPolicyApproverAdapter(reg, &WSHandler{}),
		secondCallStarted: make(chan struct{}),
		proceed:           make(chan struct{}),
	}
	al.SetToolApprover(sync)

	sub := al.SubscribeEvents(64)
	defer al.UnsubscribeEvents(sub.ID)

	done := make(chan struct{})
	var finalContent string
	var runErr error
	go func() {
		defer close(done)
		finalContent, runErr = al.ProcessDirect(context.Background(), "please fetch the url", "sess-w6-saturated-retry")
	}()

	// Wait until the SECOND real RequestApproval call has started (and is
	// paused) — at this point the first call has already gone through the
	// real registry, hit the cap, and been denied "saturated" synchronously.
	select {
	case <-sync.secondCallStarted:
	case <-time.After(10 * time.Second):
		t.Fatal("BLOCKED: the retried web_fetch call's RequestApproval never started")
	}

	// Now safe to release the holder — the second call has not yet reached
	// the registry (it is parked on syncedApprover.proceed).
	ok, gone := reg.resolve(holderEntry.ApprovalID, ApprovalActionCancel)
	require.True(t, ok)
	require.False(t, gone)

	close(sync.proceed) // let the second real RequestApproval reach the registry

	// The retry must open a REAL new pending entry now that the slot is
	// free — poll the registry's own pending list (real state), then
	// approve it through the real resolve() path.
	var retryEntry *approvalEntry
	require.Eventually(t, func() bool {
		for _, e := range reg.pendingApprovals() {
			if e.ToolName == toolName {
				retryEntry = e
				return true
			}
		}
		return false
	}, 5*time.Second, 10*time.Millisecond,
		"the retried web_fetch call must open a genuine new pending approval entry once the queue drains")

	ok, gone = reg.resolve(retryEntry.ApprovalID, ApprovalActionApprove)
	require.True(t, ok)
	require.False(t, gone)

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("BLOCKED: the turn never finished after the retry was approved")
	}

	require.NoError(t, runErr, "the turn must complete normally — a saturated-then-approved retry must not abort")
	assert.Equal(t, "fetched successfully on retry", finalContent,
		"the turn must reach the THIRD scripted response, proving the retry's real result was fed back")

	// --- AC-06's stub-resistance assertions ---

	// (1) The FIRST attempt's denial payload, inspected from the ACTUAL
	// message history the scripted LLM received (real content, not a
	// call-count spy) — permanent:false and reason:saturated together. An
	// implementation that classified saturated as permanent, or that never
	// really dispatched the saturated payload at all, fails this.
	var sawSaturatedPayload bool
	for _, m := range provider.LastMessages() {
		if strings.Contains(m.Content, `"reason":"saturated"`) {
			sawSaturatedPayload = true
			assert.Contains(t, m.Content, `"permanent":false`,
				"the saturated denial payload must be marked permanent:false")
		}
	}
	require.True(t, sawSaturatedPayload,
		"the model must have actually received a real reason:saturated permission_denied payload for attempt 1")

	// (2) web_fetch's Execute genuinely ran EXACTLY ONCE — never on attempt
	// 1 (denied before dispatch), exactly once on attempt 2 (approved).
	assert.Equal(t, int32(1), stub.calls.Load(),
		"web_fetch must execute exactly once — the approved retry, and never the denied first attempt")

	// (3) The real ToolExecEnd event for the successful retry carries the
	// tool's own, distinctly-numbered result content — proving "its real
	// result text appears in the turn's output" against genuine execution,
	// not a scripted echo.
	var sawRealResult bool
	events := drainEvents(sub.C)
	for _, evt := range events {
		if evt.Kind != agentpkg.EventKindToolExecEnd {
			continue
		}
		payload, ok := evt.Payload.(agentpkg.ToolExecEndPayload)
		if !ok || payload.Tool != toolName {
			continue
		}
		sawRealResult = true
		assert.False(t, payload.IsError, "the successful retry's tool_exec_end must not be an error")
		assert.Contains(t, payload.Result, "FETCHED-REAL-CONTENT-marker-1",
			"the tool_exec_end payload must carry the tool's OWN real output, matching its atomic call count")
	}
	require.True(t, sawRealResult, "a real tool_exec_end event for web_fetch must have been emitted")

	// (4) FR-058-16's other half: saturated never quarantines. Proven
	// behaviorally above (the retry reached a REAL new pending entry and
	// REAL execution, which a quarantined tool could never do — quarantine
	// serves every subsequent call from a cached DENIAL payload with no
	// RequestApproval at all, so reaching a second real approval entry is
	// itself proof of non-quarantine).
}

// drainEvents reads every currently-buffered event off ch without blocking
// once the channel is empty for a short grace period — the turn has already
// finished (via the done channel) by the time this is called, so its event
// producer side is done sending.
func drainEvents(ch <-chan agentpkg.Event) []agentpkg.Event {
	var events []agentpkg.Event
	for {
		select {
		case evt := <-ch:
			events = append(events, evt)
		case <-time.After(200 * time.Millisecond):
			return events
		}
	}
}

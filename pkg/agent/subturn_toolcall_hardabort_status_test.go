// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// M4 regression test (ADR-057 UAT 2026-08-04): a delegated span that was
// hard-cancelled WHILE BLOCKED IN A TOOL CALL (the live UAT's exact
// scenario: chat-wide Stop killing a child blocked in a bash tool call)
// emitted a subagent_end frame with status:"success". The cancel control
// itself works correctly — the child's turn genuinely stops — but the
// REPORTING lies, so the UI shows a killed subagent as having succeeded.
//
// Root cause: pkg/agent/loop.go's abortTurn Case 1 (a tool-call-time hard
// interrupt) deliberately returns turnResult{status: TurnEndStatusAborted}
// with a NIL error (so the root turn's own transcript is not polluted with
// a synthetic failure for an intentional stop — see abortTurn's own doc
// comment). spawnSubTurn's cleanup defer (subturn.go) computed its
// endStatus switch purely from `err != nil`, so this specific nil-error
// abort fell through every case and left endStatus at its
// SubTurnStatusSuccess initializer.
//
// This is DISTINCT from the pre-existing subturn_cancel_status_test.go
// coverage, which only exercises an LLM-CALL-blocked child (canceled via
// context while its Chat() call is in flight — a path that already worked
// correctly, because that cancellation surfaces as a non-nil err the
// existing err != nil branches already classify correctly). The gap this
// test closes is specifically the TOOL-CALL-blocked shape: the child is
// hard-aborted while genuinely inside ts.agent.Tools.ExecuteWithContext,
// which is exactly where abortTurn's nil-error Case 1 is reached
// (loop.go's `if ts.hardAbortRequested() { ... return al.abortTurn(ts,
// "after_tool_exec", hardInterruptAbortReason) }`, checked immediately
// after ExecuteWithContext returns).
package agent

import (
	"context"
	"fmt"
	"sync"
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

// m4ToolCallProvider issues exactly one tool call (to the slow tool named
// by toolName) on its first invocation. It is not expected to be invoked a
// second time in this scenario (abortTurn's Case 1 returns immediately,
// with no further LLM call) but does not hard-fail if it is, since M4 is
// specifically about status REPORTING, not about the loop's own stop
// behavior (that is C2's concern, covered elsewhere).
type m4ToolCallProvider struct {
	toolName string
}

func (p *m4ToolCallProvider) Chat(
	_ context.Context,
	_ []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	return &providers.LLMResponse{
		ToolCalls: []providers.ToolCall{
			{
				ID:       "m4-slow-tool-0",
				Function: &providers.FunctionCall{Name: p.toolName, Arguments: `{}`},
			},
		},
	}, nil
}

func (p *m4ToolCallProvider) GetDefaultModel() string { return "m4-hardabort-mock" }

// TestSpawnSubTurn_ToolCallTimeHardAbort_RecordsCancelledNotSuccess drives a
// REAL child sub-turn that is hard-aborted (via turnState.requestHardAbort,
// the exact primitive InterruptSessionHard/InterruptHard use) WHILE it is
// genuinely blocked inside a real tool Execute call — not merely while
// blocked on the LLM — and asserts the resulting EventKindSubTurnEnd is
// recorded as SubTurnStatusCancelled, not SubTurnStatusSuccess.
//
// Negative-test discipline: confirmed to FAIL (Status == "success") against
// the pre-fix cleanup defer (endStatus switch gated solely on err != nil)
// before the fix (lastTurnStatus / the new `case lastTurnStatus ==
// TurnEndStatusAborted` branch) was applied.
func TestSpawnSubTurn_ToolCallTimeHardAbort_RecordsCancelledNotSuccess(t *testing.T) {
	const toolName = "m4_slow_bash"
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: t.TempDir(), DefaultModel: config.DefaultModel{Provider: "mock", Model: "m4-hardabort-mock"}},
			List: []config.AgentConfig{{ID: "mia"}},
		},
	}
	msgBus := bus.NewMessageBus()
	provider := &m4ToolCallProvider{toolName: toolName}
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	t.Cleanup(al.Close)

	// A tool that blocks for a real, observable duration regardless of ctx
	// cancellation (time.Sleep, not ctx-aware) — mirroring a real bash
	// command that keeps running after a cancel signal until the sandbox
	// actually reaps it. execCh lets the test know the instant Execute has
	// genuinely started, so the hard-abort below fires WHILE the tool is
	// still in flight, not before the child even reaches it.
	execCh := make(chan struct{})
	slow := &slowTool{name: toolName, duration: 300 * time.Millisecond, execCh: execCh}
	al.RegisterTool(slow)
	for _, agentID := range al.GetRegistry().ListAgentIDs() {
		ag, ok := al.GetRegistry().GetAgent(agentID)
		if !ok {
			continue
		}
		ag.StoreToolPolicy(&tools.ToolPolicyCfg{
			Policies: map[string]config.ToolPolicy{toolName: "allow"},
		})
	}

	store := al.GetSessionStore()
	require.NotNil(t, store, "shared session store must be non-nil")
	meta, err := store.NewSession(session.SessionTypeChat, "web", "main")
	require.NoError(t, err)

	var mu sync.Mutex
	var subTurnEndEvents []SubTurnEndPayload
	sub := al.SubscribeEvents(32)
	defer al.UnsubscribeEvents(sub.ID)
	go func() {
		for evt := range sub.C {
			if payload, ok := evt.Payload.(SubTurnEndPayload); ok {
				mu.Lock()
				subTurnEndEvents = append(subTurnEndEvents, payload)
				mu.Unlock()
			}
		}
	}()

	ctx := context.Background()
	parentTS := &turnState{
		turnID:              "parent-m4-toolcall-hardabort",
		transcriptSessionID: meta.ID,
		routingSessionID:    session.RoutingSessionID(meta.ID),
		depth:               0,
		session:             newEphemeralSession(nil),
		pendingResults:      make(chan *tools.ToolResult, 16),
		concurrencySem:      make(chan struct{}, testMaxConcurrentSubTurns),
		al:                  al,
		finishedChan:        make(chan struct{}),
		transcriptStore:     store,
	}
	parentTS.ctx, parentTS.cancelFunc = context.WithCancel(ctx)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		spawnCtx := withSpawnToolCallID(parentTS.ctx, "call-m4-toolcall-1")
		subCfg := SubTurnConfig{Model: "m4-hardabort-mock", Async: true, Critical: true}
		_, _ = spawnSubTurn(spawnCtx, al, parentTS, subCfg)
	}()

	require.Eventually(t, func() bool {
		parentTS.mu.RLock()
		defer parentTS.mu.RUnlock()
		return len(parentTS.childTurnIDs) > 0
	}, 2*time.Second, 5*time.Millisecond, "child sub-turn must register itself on the parent before the abort fires")

	// Wait for the child to genuinely be INSIDE the tool's Execute call
	// (not merely dispatched) before hard-aborting it — this is what makes
	// the scenario tool-call-time, as opposed to the pre-existing
	// LLM-call-time coverage.
	select {
	case <-execCh:
	case <-time.After(2 * time.Second):
		t.Fatal("BLOCKED: the child never reached the slow tool's Execute call")
	}

	parentTS.mu.RLock()
	childID := parentTS.childTurnIDs[0]
	parentTS.mu.RUnlock()
	val, ok := al.activeTurnStates.Load(childID)
	require.True(t, ok, "the child turnState must be registered in activeTurnStates")
	childTS, ok := val.(*turnState)
	require.True(t, ok)

	// The exact primitive InterruptSessionHard/InterruptHard use (turn.go) —
	// called DIRECTLY on the CHILD's own turnState, exactly as a chat-wide
	// Stop's ScopeSubtree cascade would reach it (resolveInterruptTargets
	// walks descendants and calls requestHardAbort on each one directly;
	// this is a genuinely different mechanism from turnState.Finish(true)'s
	// cascade, which only cancels contexts and is what the pre-existing
	// LLM-call-time test already exercises).
	require.True(t, childTS.requestHardAbort(), "requestHardAbort must succeed on a live, not-yet-aborted child turn")

	wg.Wait()

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(subTurnEndEvents) > 0
	}, 2*time.Second, 5*time.Millisecond, "EventKindSubTurnEnd must be observed by the subscriber")

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, subTurnEndEvents, 1, "exactly one EventKindSubTurnEnd must be emitted")
	assert.Equal(t, SubTurnStatusCancelled, subTurnEndEvents[0].Status,
		fmt.Sprintf("a child hard-aborted while blocked in a real tool call must be recorded as "+
			"%q, not %q — reporting success for a killed span is the live UAT's exact defect (M4)",
			SubTurnStatusCancelled, SubTurnStatusSuccess))
}

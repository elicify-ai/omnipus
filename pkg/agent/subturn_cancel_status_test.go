// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// FIX 5 regression coverage: cancelling a parent turn while an async
// delegate is in flight must record the sub-turn's EventKindSubTurnEnd
// status as SubTurnStatusInterrupted, not SubTurnStatusError — the latter
// is indistinguishable from a genuine failure on replay, sitting right next
// to the parent's own correctly-labeled "(interrupted)" entry. Before this
// fix, spawnSubTurn's cleanup defer computed a strictly binary
// success/error status; SubTurnStatusCancelled/SubTurnStatusInterrupted were
// defined (pkg/agent/events.go) but never assigned anywhere.

package agent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// TestSpawnSubTurn_ParentHardAbort_RecordsInterruptedStatus drives a REAL
// hard-abort cascade — pkg/agent/turn.go's Finish(true) walks
// parentTS.childTurnIDs and calls Finish(true) on each registered child,
// which cancels the child's OWN context (childTS.cancelFunc, assigned from
// the context.WithTimeout call in spawnSubTurn) — and asserts the resulting
// EventKindSubTurnEnd carries Status=SubTurnStatusInterrupted.
//
// BDD:
//
//	Given a parent turn with an async delegate sub-turn in flight (blocked
//	  on a slow LLM call),
//	When the parent is hard-aborted (as RequestCancel's escalation path
//	  does after a graceful-cancel timeout, or a user-initiated /stop),
//	Then the cascade cancels the child's context, and the sub-turn's
//	  EventKindSubTurnEnd is recorded with Status="interrupted" — not
//	  "error".
//
// Negative-test discipline: this test was confirmed to FAIL (Status ==
// "error") against the pre-fix binary success/error computation before the
// fix was applied — see the delivery report.
func TestSpawnSubTurn_ParentHardAbort_RecordsInterruptedStatus(t *testing.T) {
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Provider: "mock"},
		},
	}
	msgBus := bus.NewMessageBus()
	// Long enough that the hard abort below reliably fires while the child's
	// LLM call is still blocked waiting on ctx.Done() (see slowMockProvider).
	provider := &slowMockProvider{delay: 5 * time.Second}
	al := mustNewAgentLoop(t, cfg, msgBus, provider)

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
		turnID:         "parent-hard-abort",
		depth:          0,
		session:        newEphemeralSession(nil),
		pendingResults: make(chan *tools.ToolResult, 16),
		concurrencySem: make(chan struct{}, testMaxConcurrentSubTurns),
		al:             al, // required: Finish(true)'s cascade reads ts.al to find children
	}
	parentTS.ctx, parentTS.cancelFunc = context.WithCancel(ctx)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		spawnCtx := withSpawnToolCallID(parentTS.ctx, "call-hard-abort-1")
		// Critical: true is required for this scenario — a NON-critical
		// sub-turn takes an earlier, unrelated graceful-exit shortcut
		// (runTurn's own ts.IsParentEnded() check, loop.go ~5481) the
		// instant it sees the parent has finished, regardless of hard vs.
		// graceful, WITHOUT ever touching childCtx.Err(). Critical:true
		// disables that shortcut ("All SubTurns are canceled regardless of
		// Critical flag" under a hard abort specifically — see
		// SubTurnConfig.Critical's doc comment), so the child instead
		// relies on the cascade's context cancellation, exercising the
		// exact path this fix targets.
		subCfg := SubTurnConfig{Model: "slow-model", Async: true, Critical: true}
		_, _ = spawnSubTurn(spawnCtx, al, parentTS, subCfg)
	}()

	// Give the child goroutine time to register itself (activeTurnStates,
	// parentTS.childTurnIDs) and enter its blocking LLM call before the
	// cascade fires — without this the cascade could race ahead of
	// registration and find no children to cancel.
	require.Eventually(t, func() bool {
		parentTS.mu.RLock()
		defer parentTS.mu.RUnlock()
		return len(parentTS.childTurnIDs) > 0
	}, 2*time.Second, 5*time.Millisecond, "child sub-turn must register itself on the parent before the abort fires")

	parentTS.Finish(true) // hard abort — cascades to the in-flight child

	wg.Wait()

	// wg.Wait() only guarantees spawnSubTurn has returned — the EventBus
	// subscription is consumed by a SEPARATE goroutine (started above), so a
	// brief poll is needed for it to have actually processed the event.
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(subTurnEndEvents) > 0
	}, 2*time.Second, 5*time.Millisecond, "EventKindSubTurnEnd must be observed by the subscriber")

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, subTurnEndEvents, 1, "exactly one EventKindSubTurnEnd must be emitted")
	assert.Equal(t, SubTurnStatusInterrupted, subTurnEndEvents[0].Status,
		"a sub-turn canceled via the parent's hard-abort cascade must be recorded as "+
			"'interrupted', not 'error' — 'error' is indistinguishable from a genuine failure "+
			"on replay, sitting right next to the parent's own correctly-labeled (interrupted) entry")
}

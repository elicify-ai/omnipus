// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/plan"
)

// DESIGN.md sections 4-6 (#1725, option 2a) exempt reload, not shutdown,
// from Stop. Shutdown drains real plan work without cancelling its context.
func TestPlanEngine_NonReloadTeardownDrainsTurnAndStops(t *testing.T) {
	h := newPlanReload1725Harness(t, true, false)
	var shutdownDone chan struct{}
	t.Cleanup(func() {
		// Independent CHECK may remove only the gateway's Stop call (M5).
		// Release and join first; direct Stop is failure cleanup, not the oracle.
		h.owner.release()
		if shutdownDone != nil {
			waitPlanReload1725(t, shutdownDone, "cleanup: original non-reload teardown")
		}
		h.engine.Stop()
	})

	// Positive instrument control: Start on the live engine must be a no-op.
	// Only the stable hook observer and the engine subscribe in this fixture;
	// no WebSocket or other subscriber constructor runs during these probes.
	require.NoError(t, h.engine.Start(context.Background()))
	runningProbe := h.al.SubscribeEvents(1)
	h.al.UnsubscribeEvents(runningProbe.ID)
	require.NotZero(t, runningProbe.ID, "instrument: allocate a real subscription probe")
	require.Equal(t, h.events.ID+1, runningProbe.ID,
		"instrument: Start on an already-running engine must not allocate a subscription")

	p, _ := h.runningPlan(t, "shutdown-wake-1725")
	stopped, err := h.engine.StopPlan(context.Background(), p.ID, "shutdown-test", "cli")
	require.NoError(t, err)
	require.Equal(t, plan.StateFailed, stopped.State, "setup: dispatch the real origin-less owner wake")
	call := h.owner.awaitEntry(t)
	current, err := h.al.GetPlanStore().Get(p.ID)
	require.NoError(t, err)
	require.NotEmpty(t, current.OwnerSessionID, "setup: the held wake must own a durable session")

	// A partial-boot-shaped services value isolates the engine's teardown
	// branch; the existing harness still performs full service cleanup once.
	shutdownServices := &services{PlanEngine: h.engine}
	shutdownDone = make(chan struct{})
	go func() {
		defer close(shutdownDone)
		stopAndCleanupServices(shutdownServices, 5*time.Second, false)
	}()

	// This is a fixture observation window, not a new product latency bound.
	// The discrete restart/subscription oracle below also detects missing Stop
	// if unrelated services happen to keep teardown busy throughout the window.
	const heldObservation = 300 * time.Millisecond
	timer := time.NewTimer(heldObservation)
	defer timer.Stop()
	select {
	case <-shutdownDone:
		t.Error("non-reload teardown returned while the real plan wake was still held; shutdown must drain it")
	case <-call.ctx.Done():
		t.Errorf("shutdown cancelled the held plan turn instead of draining it: %v", call.ctx.Err())
	case <-timer.C:
	}
	assert.NoError(t, call.ctx.Err(), "shutdown must NOT cancel an in-flight plan turn")
	select {
	case <-h.owner.finished:
		t.Error("held plan wake returned before its explicit release during shutdown")
	default:
	}

	h.owner.release()
	waitPlanReload1725(t, shutdownDone, "non-reload teardown after releasing the plan wake")
	// Normal provider cleanup cancels its child context after Chat returns;
	// non-cancellation is asserted while held, normal completion afterwards.
	select {
	case <-h.owner.finished:
	default:
		t.Fatal("non-reload teardown returned before the released provider call settled")
	}
	h.awaitTurnEnd(t, planReload1725Owner, current.OwnerSessionID)
	assertPlanShutdown1725Reply(t, h, current.OwnerSessionID)
	assert.Equal(t, int32(1), h.owner.calls.Load(), "shutdown must not replay the owner wake")

	// Finite stopped-state proof: after Stop, Start must be a real restart,
	// consuming one engine subscription before our next probe. Without Stop,
	// Start remains an idempotent no-op and this exact ID assertion fails.
	require.NoError(t, h.engine.Start(context.Background()), "a stopped engine must be restartable")
	restartedProbe := h.al.SubscribeEvents(1)
	h.al.UnsubscribeEvents(restartedProbe.ID)
	require.NotZero(t, restartedProbe.ID, "instrument: allocate a real post-shutdown probe")
	assert.Equal(t, runningProbe.ID+2, restartedProbe.ID,
		"non-reload teardown must stop the engine: the next Start must create exactly one fresh engine subscription")
	t.Log("non-reload teardown drained the uncancelled owner turn; exact reply persisted; post-shutdown Start created one fresh engine subscription")
}

func assertPlanShutdown1725Reply(t *testing.T, h *planReload1725Harness, sessionID string) {
	t.Helper()
	store := h.al.ResolveSessionStore(sessionID)
	require.NotNil(t, store, "the drained plan session must remain readable")
	entries, err := store.ReadTranscript(sessionID)
	require.NoError(t, err)
	var replies []string
	for _, entry := range entries {
		if entry.Role == "assistant" {
			replies = append(replies, entry.Content)
			assert.Equal(t, planReload1725Owner, entry.AgentID, "the real owner must persist its reply")
			assert.Equal(t, "", entry.ErrorCode, "the drained reply must not be an error")
			assert.NotEqual(t, "interrupted", entry.Status, "shutdown must not interrupt the plan turn")
		}
	}
	assert.Equal(t, []string{planReload1725Reply}, replies,
		"shutdown must drain exactly one intact owner reply into the durable plan transcript")
}

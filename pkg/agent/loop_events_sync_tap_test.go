// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// loop_events_sync_tap_test.go — #823 catch-up redesign: proves
// AgentLoop.SetEventSyncTap actually reaches the underlying EventBus (added
// so pkg/gateway, which cannot reach the unexported eventBus field, can
// install the session-hub sync tap — see loop_events.go's cross-lane note).

package agent

import "testing"

func TestAgentLoop_SetEventSyncTap_ReachesTheBus(t *testing.T) {
	al := &AgentLoop{eventBus: NewEventBus()}
	calls := 0
	al.SetEventSyncTap(func(Event) { calls++ })
	al.eventBus.Emit(Event{Kind: EventKindLLMRequest})
	if calls != 1 {
		t.Fatalf("expected 1 tap call, got %d", calls)
	}

	al.SetEventSyncTap(nil)
	al.eventBus.Emit(Event{Kind: EventKindLLMRequest})
	if calls != 1 {
		t.Fatalf("expected no further calls after clearing, got %d total", calls)
	}
}

func TestAgentLoop_SetEventSyncTap_NilLoopOrBusIsNoop(t *testing.T) {
	var nilLoop *AgentLoop
	nilLoop.SetEventSyncTap(func(Event) { t.Fatal("must never be called") }) // must not panic

	al := &AgentLoop{}                                                  // eventBus is nil
	al.SetEventSyncTap(func(Event) { t.Fatal("must never be called") }) // must not panic
}

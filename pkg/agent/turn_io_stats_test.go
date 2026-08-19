// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import "testing"

// TestTurnState_AddTurnIOStats_Accumulates covers the accumulator that carries
// the provider's input/output split across the LLM iterations of one turn.
//
// Before it existed, only Usage.TotalTokens survived the call site in
// pkg/agent/loop.go, so nothing downstream could report tokens_in.
func TestTurnState_AddTurnIOStats_Accumulates(t *testing.T) {
	ts := &turnState{}

	ts.AddTurnIOStats(800, 200)
	ts.AddTurnIOStats(1200, 300)

	prompt, completion := ts.GetTurnIOStats()
	if prompt != 2000 {
		t.Errorf("prompt tokens = %d, want 2000", prompt)
	}
	if completion != 500 {
		t.Errorf("completion tokens = %d, want 500", completion)
	}
}

// An abandoned turn must not keep mutating stats — the same B4 suppression the
// sibling accumulators honour. Without this, a cancelled turn would keep
// inflating the session's token counts after the user stopped it.
func TestTurnState_AddTurnIOStats_SuppressedWhenAbandoned(t *testing.T) {
	ts := &turnState{}
	ts.AddTurnIOStats(100, 10)
	ts.abandoned.Store(true)
	ts.AddTurnIOStats(999, 999)

	prompt, completion := ts.GetTurnIOStats()
	if prompt != 100 || completion != 10 {
		t.Errorf("abandoned turn kept accumulating: prompt=%d completion=%d, want 100/10", prompt, completion)
	}
}

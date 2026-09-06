// Omnipus — AskUserQuestion Stop-cancel wiring test (spec US-6 S2, Test 7's
// session-Stop backend half): a session Stop cancels the pending question
// set even when no turn is active (the park already ended the turn).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/askuser"
)

type fakeAskCancelRegistry struct {
	mu        sync.Mutex
	cancelled []string
}

func (f *fakeAskCancelRegistry) CreatePending(*askuser.PendingSet) error { return nil }

func (f *fakeAskCancelRegistry) PendingForSession(string) (*askuser.PendingSet, bool) {
	return nil, false
}

func (f *fakeAskCancelRegistry) CancelOnSessionStop(key string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancelled = append(f.cancelled, key)
	return true
}

func TestRequestCancel_CancelsPendingAskWithoutActiveTurn(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	reg := &fakeAskCancelRegistry{}
	al.SetAskUserRegistry(reg)

	// No active turn is registered for this session — exactly the parked
	// shape: the turn ended TurnEndStatusParked, the card pends, the user
	// hits Stop.
	fired, _, err := al.RequestCancelForSession(context.Background(), "session_parked_ask", "alice", "web")
	if err != nil {
		t.Fatalf("RequestCancelForSession: %v", err)
	}
	if fired {
		t.Fatal("sanity: no active turn should have been claimed")
	}
	reg.mu.Lock()
	defer reg.mu.Unlock()
	if len(reg.cancelled) != 1 || reg.cancelled[0] != "session_parked_ask" {
		t.Fatalf("Stop must cancel the pending ask set for the scope's session id; got %v", reg.cancelled)
	}
}

func TestRequestCancel_NilRegistryIsNoop(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	// No registry wired: Stop must not panic and must behave as before.
	if _, _, err := al.RequestCancelForSession(context.Background(), "session_no_reg", "alice", "web"); err != nil {
		t.Fatalf("RequestCancelForSession with nil registry: %v", err)
	}
}

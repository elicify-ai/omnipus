// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"sync"
	"testing"
	"time"
)

// TestServedSubdirs_RegisterAndLookup verifies that Register stores an entry
// retrievable by the returned token.
func TestServedSubdirs_RegisterAndLookup(t *testing.T) {
	t.Parallel()
	s := NewServedSubdirs()
	defer s.Stop()

	token, deadline, err := s.Register("agent-1", "/tmp/ws", 5*time.Minute)
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}
	if token == "" {
		t.Fatal("expected non-empty token")
	}
	if deadline.IsZero() {
		t.Fatal("expected non-zero deadline")
	}

	entry := s.Lookup(token)
	if entry == nil {
		t.Fatal("Lookup returned nil for a valid token")
	}
	if entry.AgentID != "agent-1" {
		t.Errorf("AgentID = %q, want %q", entry.AgentID, "agent-1")
	}
	if entry.AbsDir != "/tmp/ws" {
		t.Errorf("AbsDir = %q, want %q", entry.AbsDir, "/tmp/ws")
	}
	if !entry.Deadline.Equal(deadline) {
		t.Errorf("Deadline mismatch: got %v, want %v", entry.Deadline, deadline)
	}
}

// TestServedSubdirs_UnknownToken verifies that Lookup returns nil for an
// unknown token.
func TestServedSubdirs_UnknownToken(t *testing.T) {
	t.Parallel()
	s := NewServedSubdirs()
	defer s.Stop()

	if entry := s.Lookup("completely-unknown-token"); entry != nil {
		t.Fatalf("expected nil for unknown token, got %+v", entry)
	}
}

// TestServedSubdirs_ExpiredToken verifies that Lookup returns nil for an
// entry whose deadline has passed, even if the janitor hasn't run yet.
func TestServedSubdirs_ExpiredToken(t *testing.T) {
	t.Parallel()
	s := NewServedSubdirs()
	defer s.Stop()

	// Register with a tiny duration that expires immediately.
	token, _, err := s.Register("agent-2", "/tmp/ws2", time.Millisecond)
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	// Wait for the entry to expire.
	time.Sleep(5 * time.Millisecond)

	entry := s.Lookup(token)
	if entry != nil {
		t.Fatalf("expected nil for expired token, got %+v", entry)
	}
}

// TestServedSubdirs_PerAgentCapReplacesToken verifies that registering a
// second time for the same agent atomically replaces the first token.
func TestServedSubdirs_PerAgentCapReplacesToken(t *testing.T) {
	t.Parallel()
	s := NewServedSubdirs()
	defer s.Stop()

	token1, _, err := s.Register("agent-3", "/tmp/ws3a", 5*time.Minute)
	if err != nil {
		t.Fatalf("first Register failed: %v", err)
	}

	token2, _, err := s.Register("agent-3", "/tmp/ws3b", 5*time.Minute)
	if err != nil {
		t.Fatalf("second Register failed: %v", err)
	}

	if token1 == token2 {
		t.Fatal("expected second Register to produce a different token")
	}

	// Old token should no longer resolve.
	if entry := s.Lookup(token1); entry != nil {
		t.Fatalf("old token should be invalidated; got %+v", entry)
	}

	// New token should resolve and point to the new directory.
	entry := s.Lookup(token2)
	if entry == nil {
		t.Fatal("new token should resolve")
	}
	if entry.AbsDir != "/tmp/ws3b" {
		t.Errorf("AbsDir = %q, want %q", entry.AbsDir, "/tmp/ws3b")
	}
}

// TestServedSubdirs_ActiveForAgent verifies the per-agent active-token query.
func TestServedSubdirs_ActiveForAgent(t *testing.T) {
	t.Parallel()
	s := NewServedSubdirs()
	defer s.Stop()

	// No registration yet.
	if _, _, ok := s.ActiveForAgent("agent-4"); ok {
		t.Fatal("expected no active registration before Register")
	}

	token, deadline, err := s.Register("agent-4", "/tmp/ws4", 10*time.Minute)
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	gotToken, gotDeadline, ok := s.ActiveForAgent("agent-4")
	if !ok {
		t.Fatal("expected active registration after Register")
	}
	if gotToken != token {
		t.Errorf("token = %q, want %q", gotToken, token)
	}
	if !gotDeadline.Equal(deadline) {
		t.Errorf("deadline = %v, want %v", gotDeadline, deadline)
	}
}

// TestServedSubdirs_Evict verifies that Evict removes the registration for an
// agent immediately (no janitor delay required).
func TestServedSubdirs_Evict(t *testing.T) {
	t.Parallel()
	s := NewServedSubdirs()
	defer s.Stop()

	token, _, err := s.Register("agent-5", "/tmp/ws5", 5*time.Minute)
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	s.Evict("agent-5")

	if entry := s.Lookup(token); entry != nil {
		t.Fatalf("expected nil after Evict, got %+v", entry)
	}
	if _, _, ok := s.ActiveForAgent("agent-5"); ok {
		t.Fatal("expected no active registration after Evict")
	}
}

// TestServedSubdirs_JanitorCleansExpired verifies that the janitor goroutine
// eventually removes expired entries from the internal map. We trigger
// purgeExpired directly rather than waiting 30 seconds.
func TestServedSubdirs_JanitorCleansExpired(t *testing.T) {
	t.Parallel()
	s := NewServedSubdirs()
	defer s.Stop()

	token, _, err := s.Register("agent-6", "/tmp/ws6", time.Millisecond)
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	// Let it expire.
	time.Sleep(5 * time.Millisecond)

	// Run the janitor logic directly (no need to wait 30 s in a test).
	s.purgeExpired()

	// Token and agent entry should both be removed.
	s.mu.RLock()
	_, tokenPresent := s.byToken[token]
	_, agentPresent := s.byAgent["agent-6"]
	s.mu.RUnlock()

	if tokenPresent {
		t.Error("janitor should have removed expired byToken entry")
	}
	if agentPresent {
		t.Error("janitor should have removed expired byAgent entry")
	}
}

// TestServedSubdirs_MultipleAgents verifies that separate agents maintain
// independent registrations.
func TestServedSubdirs_MultipleAgents(t *testing.T) {
	t.Parallel()
	s := NewServedSubdirs()
	defer s.Stop()

	tokenA, _, err := s.Register("agent-A", "/tmp/wsA", 5*time.Minute)
	if err != nil {
		t.Fatalf("Register A failed: %v", err)
	}
	tokenB, _, err := s.Register("agent-B", "/tmp/wsB", 5*time.Minute)
	if err != nil {
		t.Fatalf("Register B failed: %v", err)
	}

	if tokenA == tokenB {
		t.Fatal("expected different tokens for different agents")
	}

	entryA := s.Lookup(tokenA)
	if entryA == nil || entryA.AgentID != "agent-A" {
		t.Fatalf("Lookup tokenA failed: got %+v", entryA)
	}
	entryB := s.Lookup(tokenB)
	if entryB == nil || entryB.AgentID != "agent-B" {
		t.Fatalf("Lookup tokenB failed: got %+v", entryB)
	}
}

// TestServedSubdirs_ReRegisterSamePath_ReusesToken pins the fix for a preview
// URL dying under the agent's normal workflow.
//
// Observed in a real session: the agent served a directory, gave the user the
// URL, edited the files, re-served the SAME directory, and the URL it had
// already handed over began returning 404. Re-serving after an edit is the
// common case, not an edge case.
func TestServedSubdirs_ReRegisterSamePath_ReusesToken(t *testing.T) {
	t.Parallel()
	s := NewServedSubdirs()
	defer s.Stop()

	dir := t.TempDir()

	first, firstDeadline, err := s.Register("jim", dir, time.Hour)
	if err != nil {
		t.Fatalf("first Register: %v", err)
	}

	second, secondDeadline, err := s.Register("jim", dir, 2*time.Hour)
	if err != nil {
		t.Fatalf("second Register: %v", err)
	}

	if first != second {
		t.Errorf("re-serving the same directory must reuse the token: got %q, want %q", second, first)
	}
	if s.Lookup(first) == nil {
		t.Error("the URL already given to the user must still resolve after a re-serve")
	}
	if !secondDeadline.After(firstDeadline) {
		t.Errorf("re-serving must renew the deadline: second %v not after first %v", secondDeadline, firstDeadline)
	}
	if entry := s.Lookup(first); entry != nil && entry.AbsDir != dir {
		t.Errorf("AbsDir = %q, want %q", entry.AbsDir, dir)
	}
}

// TestServedSubdirs_ReRegisterDifferentPath_StillRotates proves the fix did not
// dissolve the per-agent cap: switching to a different directory must still
// invalidate the old token, so a stale URL cannot keep serving content the
// agent has moved on from.
func TestServedSubdirs_ReRegisterDifferentPath_StillRotates(t *testing.T) {
	t.Parallel()
	s := NewServedSubdirs()
	defer s.Stop()

	dirA, dirB := t.TempDir(), t.TempDir()

	first, _, err := s.Register("jim", dirA, time.Hour)
	if err != nil {
		t.Fatalf("Register dirA: %v", err)
	}
	second, _, err := s.Register("jim", dirB, time.Hour)
	if err != nil {
		t.Fatalf("Register dirB: %v", err)
	}

	if first == second {
		t.Error("serving a different directory must mint a new token")
	}
	if s.Lookup(first) != nil {
		t.Error("the superseded token must stop resolving")
	}
	entry := s.Lookup(second)
	if entry == nil {
		t.Fatal("the new token must resolve")
	}
	if entry.AbsDir != dirB {
		t.Errorf("AbsDir = %q, want %q", entry.AbsDir, dirB)
	}
}

// TestServedSubdirs_ReRegisterAfterExpiry_MintsNewToken covers the boundary
// between the two behaviours above: an expired registration must not be
// renewed back to life under its old token.
func TestServedSubdirs_ReRegisterAfterExpiry_MintsNewToken(t *testing.T) {
	t.Parallel()
	s := NewServedSubdirs()
	defer s.Stop()

	dir := t.TempDir()

	first, _, err := s.Register("jim", dir, 1*time.Millisecond)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	if s.Lookup(first) != nil {
		t.Fatal("precondition: the first registration should have expired")
	}

	second, _, err := s.Register("jim", dir, time.Hour)
	if err != nil {
		t.Fatalf("re-Register: %v", err)
	}
	if first == second {
		t.Error("an expired token must not be resurrected")
	}
	if s.Lookup(second) == nil {
		t.Error("the fresh token must resolve")
	}
}

// TestServedSubdirs_ConcurrentRenewAndLookup is what makes the renewal path's
// safety claim enforceable rather than aspirational.
//
// Register renews by REPLACING the *ServedEntry rather than mutating it,
// because Lookup hands the pointer to callers and reads Deadline after
// dropping the read lock. A "simplification" back to `prevEntry.Deadline =
// deadline` passes every other test in this file — including under -race —
// because nothing else ever exercises the two concurrently. This does.
//
// Run with -race for it to mean anything.
func TestServedSubdirs_ConcurrentRenewAndLookup(t *testing.T) {
	s := NewServedSubdirs()
	defer s.Stop()

	dir := t.TempDir()
	token, _, err := s.Register("jim", dir, time.Hour)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Renewers: repeatedly re-serve the same directory.
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					if _, _, regErr := s.Register("jim", dir, time.Hour); regErr != nil {
						t.Errorf("concurrent Register: %v", regErr)
						return
					}
				}
			}
		}()
	}

	// Readers: read the field the renewal path writes.
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					if entry := s.Lookup(token); entry != nil {
						_ = entry.Deadline
						_ = entry.AbsDir
					}
				}
			}
		}()
	}

	time.Sleep(100 * time.Millisecond)
	close(stop)
	wg.Wait()

	if s.Lookup(token) == nil {
		t.Error("the token must survive concurrent renewal")
	}
}

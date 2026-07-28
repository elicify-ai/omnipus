// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
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

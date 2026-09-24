// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// D-13 security-review fix (2026-09-24): RecordNetworkHosts/NetworkHostsFor/
// SessionEgressToken/NetworkTokensForSession — the session-scoped host set
// and egress-proxy token an approved D8 escalation now records, on top of
// the existing port-level RecordNetworkGrant/HasNetworkGrant.
package security

import (
	"fmt"
	"sync"
	"testing"
)

func TestApprovalGrantStore_RecordNetworkHosts_SessionScoped(t *testing.T) {
	s := NewApprovalGrantStore()

	if got := s.NetworkHostsFor("session-1", "agent-a"); got != nil {
		t.Fatalf("NetworkHostsFor before any record = %v, want nil", got)
	}
	if !s.RecordNetworkHosts("session-1", "agent-a", []string{"example.com"}) {
		t.Fatal("RecordNetworkHosts must succeed for a well-formed key")
	}
	got := s.NetworkHostsFor("session-1", "agent-a")
	if len(got) != 1 || got[0] != "example.com" {
		t.Errorf("NetworkHostsFor = %v, want [example.com]", got)
	}

	// A DIFFERENT agent in the same session, and a DIFFERENT session for
	// the same agent, must not see this host — "the grant is scoped to the
	// session" (founder decision B).
	if got := s.NetworkHostsFor("session-1", "agent-b"); got != nil {
		t.Errorf("a different agent in the same session saw %v, want nil", got)
	}
	if got := s.NetworkHostsFor("session-2", "agent-a"); got != nil {
		t.Errorf("a different session for the same agent saw %v, want nil", got)
	}

	// Union, not replace: a second, different host is added, not swapped in.
	s.RecordNetworkHosts("session-1", "agent-a", []string{"other.example"})
	got = s.NetworkHostsFor("session-1", "agent-a")
	if len(got) != 2 {
		t.Fatalf("NetworkHostsFor after second record = %v, want two hosts (union)", got)
	}
}

func TestApprovalGrantStore_RecordNetworkHosts_FailSafe(t *testing.T) {
	var nilStore *ApprovalGrantStore
	if nilStore.NetworkHostsFor("s", "a") != nil {
		t.Error("a nil store's NetworkHostsFor must return nil")
	}
	if nilStore.RecordNetworkHosts("s", "a", []string{"x"}) {
		t.Error("a nil store's RecordNetworkHosts must no-op (false)")
	}
	s := NewApprovalGrantStore()
	if s.RecordNetworkHosts("", "a", []string{"x"}) {
		t.Error("an empty sessionID must fail")
	}
	if s.RecordNetworkHosts("s", "", []string{"x"}) {
		t.Error("an empty agentID must fail")
	}
	if s.RecordNetworkHosts("s", "a", nil) {
		t.Error("an empty hosts slice must fail (nothing to grant)")
	}
}

// TestApprovalGrantStore_SessionEgressToken_StableAndScoped proves the
// token-minting contract requestNoSandboxApproval/grantEgressHosts rely on:
// the SAME (session, agent) always gets the SAME token back (so a
// session's egress-proxy grants accumulate under one key), a DIFFERENT
// (session, agent) gets a DIFFERENT, unguessable-looking token, and the
// token is never empty for a well-formed key.
func TestApprovalGrantStore_SessionEgressToken_StableAndScoped(t *testing.T) {
	s := NewApprovalGrantStore()

	tok1 := s.SessionEgressToken("session-1", "agent-a")
	if tok1 == "" {
		t.Fatal("SessionEgressToken must not be empty for a well-formed key")
	}
	tok1Again := s.SessionEgressToken("session-1", "agent-a")
	if tok1Again != tok1 {
		t.Errorf("SessionEgressToken must return the SAME token on a second call: got %q, want %q", tok1Again, tok1)
	}

	tok2 := s.SessionEgressToken("session-2", "agent-a")
	if tok2 == tok1 {
		t.Error("a different session must never get the same token — a rogue process must not be able to guess it and piggyback on another session's grant")
	}

	tok3 := s.SessionEgressToken("session-1", "agent-b")
	if tok3 == tok1 {
		t.Error("a different agent in the same session must never get the same token")
	}
}

func TestApprovalGrantStore_SessionEgressToken_FailSafe(t *testing.T) {
	var nilStore *ApprovalGrantStore
	if nilStore.SessionEgressToken("s", "a") != "" {
		t.Error("a nil store's SessionEgressToken must return \"\"")
	}
	s := NewApprovalGrantStore()
	if s.SessionEgressToken("", "a") != "" {
		t.Error("an empty sessionID must return \"\"")
	}
}

// TestApprovalGrantStore_NetworkTokensForSession_ThenClearSession proves the
// AgentLoop.CloseSession contract: every token minted for a session is
// discoverable (so the caller can revoke it on the separate egress proxy)
// BEFORE ClearSession wipes this store's own copy, and is gone from the
// store afterwards.
func TestApprovalGrantStore_NetworkTokensForSession_ThenClearSession(t *testing.T) {
	s := NewApprovalGrantStore()
	tokA := s.SessionEgressToken("session-1", "agent-a")
	tokB := s.SessionEgressToken("session-1", "agent-b")
	_ = s.SessionEgressToken("session-2", "agent-a") // a different session — must not appear below

	got := s.NetworkTokensForSession("session-1")
	if len(got) != 2 {
		t.Fatalf("NetworkTokensForSession(session-1) = %v, want 2 tokens", got)
	}
	seen := map[string]bool{}
	for _, tok := range got {
		seen[tok] = true
	}
	if !seen[tokA] || !seen[tokB] {
		t.Errorf("NetworkTokensForSession(session-1) = %v, want to contain %q and %q", got, tokA, tokB)
	}

	s.ClearSession("session-1")
	if got := s.NetworkTokensForSession("session-1"); got != nil {
		t.Errorf("NetworkTokensForSession after ClearSession = %v, want nil", got)
	}
	if got := s.NetworkHostsFor("session-1", "agent-a"); got != nil {
		t.Errorf("NetworkHostsFor after ClearSession = %v, want nil", got)
	}
	// A different session's own token must survive session-1's clear.
	if got := s.NetworkTokensForSession("session-2"); len(got) != 1 {
		t.Errorf("NetworkTokensForSession(session-2) after clearing session-1 = %v, want 1 token untouched", got)
	}
}

// TestApprovalGrantStore_SessionEgressToken_ConcurrentSessionsRaceSafe is
// founder decision B point 4's "race-safe" requirement: many goroutines
// minting/recording hosts for MANY DIFFERENT sessions concurrently must
// never corrupt the store or cross-contaminate one session's token/hosts
// into another's. Run with -race in CI.
func TestApprovalGrantStore_SessionEgressToken_ConcurrentSessionsRaceSafe(t *testing.T) {
	s := NewApprovalGrantStore()
	const n = 50
	var wg sync.WaitGroup
	tokens := make([]string, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sessID := fmt.Sprintf("session-%d", i)
			tokens[i] = s.SessionEgressToken(sessID, "agent")
			s.RecordNetworkHosts(sessID, "agent", []string{"host.example"})
		}(i)
	}
	wg.Wait()
	for _, tok := range tokens {
		if tok == "" {
			t.Error("a concurrent SessionEgressToken call returned an empty token")
		}
	}
}

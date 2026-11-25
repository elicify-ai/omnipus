// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Functional proof for the three ADR-092 grant kinds (D4 prefix, D7 path
// widening, D8 network widening) this lane (L4) adds to ApprovalGrantStore.

package security

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/fspolicy"
)

func TestApprovalGrantStore_PrefixGrant_TokenBoundaryMatch(t *testing.T) {
	s := NewApprovalGrantStore()
	ok := s.RecordPrefixGrant("session-1", "agent-a", "bash", ShellPrefixGrant{Binary: "/usr/bin/npm", ArgPrefix: "run test"})
	if !ok {
		t.Fatal("RecordPrefixGrant must succeed for a well-formed grant")
	}

	if !s.IsPrefixAllowed("session-1", "agent-a", "bash", "/usr/bin/npm", []string{"run", "test", "-v"}, false) {
		t.Error("\"npm run test -v\" must match a grant for \"npm run test\" — the extra word extends the prefix")
	}
	if s.IsPrefixAllowed("session-1", "agent-a", "bash", "/usr/bin/npm", []string{"run", "testfoo"}, false) {
		t.Error("\"npm run testfoo\" must NOT match a grant for \"npm run test\" — token-boundary matched, not string-prefix (FR-024)")
	}
	if s.IsPrefixAllowed("session-1", "agent-a", "bash", "/usr/bin/npm", []string{"run", "test"}, true) {
		t.Error("run_in_background is a separate match dimension (D4) — a foreground grant must not cover a background call")
	}
	if s.IsPrefixAllowed("session-1", "agent-b", "bash", "/usr/bin/npm", []string{"run", "test"}, false) {
		t.Error("a different agent in the same session must not inherit the grant")
	}
	if s.IsPrefixAllowed("session-1", "agent-a", "bash", "/usr/bin/yarn", []string{"run", "test"}, false) {
		t.Error("a grant for a different resolved binary must not match — this is D3's look-alike defence at the grant layer")
	}
}

func TestApprovalGrantStore_PrefixGrant_BareBinaryMatchesAnyArgs(t *testing.T) {
	s := NewApprovalGrantStore()
	s.RecordPrefixGrant("session-1", "agent-a", "bash", ShellPrefixGrant{Binary: "/usr/bin/git"})
	if !s.IsPrefixAllowed("session-1", "agent-a", "bash", "/usr/bin/git", []string{"status"}, false) {
		t.Error("an empty ArgPrefix must match any arguments (a bare-binary grant)")
	}
	if !s.IsPrefixAllowed("session-1", "agent-a", "bash", "/usr/bin/git", nil, false) {
		t.Error("an empty ArgPrefix must match zero arguments too")
	}
}

func TestApprovalGrantStore_PrefixGrant_FailSafeOnEmptyKey(t *testing.T) {
	s := NewApprovalGrantStore()
	if s.RecordPrefixGrant("", "agent-a", "bash", ShellPrefixGrant{Binary: "/bin/true"}) {
		t.Error("RecordPrefixGrant must no-op on an empty sessionID")
	}
	if s.IsPrefixAllowed("", "agent-a", "bash", "/bin/true", nil, false) {
		t.Error("IsPrefixAllowed must fail closed (false) on an empty sessionID")
	}
	var nilStore *ApprovalGrantStore
	if nilStore.IsPrefixAllowed("s", "a", "bash", "/bin/true", nil, false) {
		t.Error("a nil store must fail closed (false), never panic")
	}
	if nilStore.RecordPrefixGrant("s", "a", "bash", ShellPrefixGrant{Binary: "/bin/true"}) {
		t.Error("a nil store's RecordPrefixGrant must be a no-op (false), never panic")
	}
}

func TestApprovalGrantStore_PathGrant_UnionsAccessOnSamePath(t *testing.T) {
	s := NewApprovalGrantStore()
	s.RecordPathGrant("session-1", "agent-a", fspolicy.PathGrant{Path: "/tmp/shared/out.txt", Access: fspolicy.PathGrantAccessRead})
	s.RecordPathGrant("session-1", "agent-a", fspolicy.PathGrant{Path: "/tmp/shared/out.txt", Access: fspolicy.PathGrantAccessWrite})

	grants := s.PathGrantsFor("session-1", "agent-a")
	if len(grants) != 1 {
		t.Fatalf("a second widening for the SAME path must union into one entry, got %d entries", len(grants))
	}
	want := fspolicy.PathGrantAccessRead | fspolicy.PathGrantAccessWrite
	if grants[0].Access != want {
		t.Errorf("Access = %d, want the union %d (read|write)", grants[0].Access, want)
	}
}

func TestApprovalGrantStore_PathGrant_FailSafe(t *testing.T) {
	s := NewApprovalGrantStore()
	if s.RecordPathGrant("session-1", "agent-a", fspolicy.PathGrant{Path: "", Access: fspolicy.PathGrantAccessWrite}) {
		t.Error("an empty Path must be rejected")
	}
	if s.RecordPathGrant("session-1", "agent-a", fspolicy.PathGrant{Path: "/tmp/x", Access: 0}) {
		t.Error("a zero Access bitmask must be rejected")
	}
	if got := s.PathGrantsFor("", "agent-a"); got != nil {
		t.Error("PathGrantsFor must return nil for an empty sessionID, not leak a stored grant under an empty key")
	}
	var nilStore *ApprovalGrantStore
	if got := nilStore.PathGrantsFor("s", "a"); got != nil {
		t.Error("a nil store must return nil, never panic")
	}
}

func TestApprovalGrantStore_PathGrant_DefensiveCopy(t *testing.T) {
	s := NewApprovalGrantStore()
	s.RecordPathGrant("session-1", "agent-a", fspolicy.PathGrant{Path: "/tmp/x", Access: fspolicy.PathGrantAccessRead})

	got := s.PathGrantsFor("session-1", "agent-a")
	got[0].Access = fspolicy.PathGrantAccessWrite // mutate the caller's copy

	got2 := s.PathGrantsFor("session-1", "agent-a")
	if got2[0].Access != fspolicy.PathGrantAccessRead {
		t.Error("PathGrantsFor must return a defensive copy — mutating the caller's slice must not affect the store")
	}
}

func TestApprovalGrantStore_NetworkGrant(t *testing.T) {
	s := NewApprovalGrantStore()
	if s.HasNetworkGrant("session-1", "agent-a") {
		t.Error("HasNetworkGrant must be false before any RecordNetworkGrant")
	}
	if !s.RecordNetworkGrant("session-1", "agent-a") {
		t.Fatal("RecordNetworkGrant must succeed for a well-formed key")
	}
	if !s.HasNetworkGrant("session-1", "agent-a") {
		t.Error("HasNetworkGrant must be true after RecordNetworkGrant")
	}
	if s.HasNetworkGrant("session-1", "agent-b") {
		t.Error("a different agent in the same session must not inherit the network grant")
	}
	// C-26 (Lifetime and scope, rows Key and Covers): the key is (agent, kind,
	// subject) and the session id is no longer part of it, so the SAME agent in
	// ANOTHER chat holds the network allowance too. FLIPPED from "a different
	// session must not inherit the network grant", which that row reverses.
	if !s.HasNetworkGrant("session-2", "agent-a") {
		t.Error("C-26: the same agent in a different session must hold the network allowance " +
			"(the session id is no longer part of the key)")
	}
	// Agent isolation holds on both axes at once: another agent in another chat.
	if s.HasNetworkGrant("session-2", "agent-b") {
		t.Error("a different agent in a different session must not hold agent-a's network allowance")
	}
	// Idempotent.
	s.RecordNetworkGrant("session-1", "agent-a")
	if !s.HasNetworkGrant("session-1", "agent-a") {
		t.Error("recording twice must leave the grant in place")
	}
}

func TestApprovalGrantStore_NetworkGrant_FailSafe(t *testing.T) {
	var nilStore *ApprovalGrantStore
	if nilStore.HasNetworkGrant("s", "a") {
		t.Error("a nil store must fail closed (false)")
	}
	if nilStore.RecordNetworkGrant("s", "a") {
		t.Error("a nil store's RecordNetworkGrant must no-op (false)")
	}
}

// allowanceKindFixture is one of the four allowance kinds the store holds
// (exact tool call, command family, path, network), with one recorder and one
// reader that both go through the store's real methods. The reader answers for
// exactly the subject the recorder stored, so "holds" is the same question for
// every kind.
type allowanceKindFixture struct {
	name   string
	record func(s *ApprovalGrantStore, sessionID, agentID string) bool
	holds  func(s *ApprovalGrantStore, sessionID, agentID string) bool
}

var allowanceKindFixtures = []allowanceKindFixture{
	{
		name: "exact",
		record: func(s *ApprovalGrantStore, sid, agent string) bool {
			return s.Record(sid, agent, "bash", map[string]any{"command": "ls"})
		},
		holds: func(s *ApprovalGrantStore, sid, agent string) bool {
			return s.IsAllowed(sid, agent, "bash", map[string]any{"command": "ls"})
		},
	},
	{
		name: "prefix",
		record: func(s *ApprovalGrantStore, sid, agent string) bool {
			return s.RecordPrefixGrant(sid, agent, "bash", ShellPrefixGrant{Binary: "/usr/bin/npm", ArgPrefix: "run test"})
		},
		holds: func(s *ApprovalGrantStore, sid, agent string) bool {
			return s.IsPrefixAllowed(sid, agent, "bash", "/usr/bin/npm", []string{"run", "test"}, false)
		},
	},
	{
		name: "path",
		record: func(s *ApprovalGrantStore, sid, agent string) bool {
			return s.RecordPathGrant(sid, agent, fspolicy.PathGrant{Path: "/tmp/x", Access: fspolicy.PathGrantAccessWrite})
		},
		holds: func(s *ApprovalGrantStore, sid, agent string) bool {
			grants := s.PathGrantsFor(sid, agent)
			return len(grants) == 1 && grants[0].Path == "/tmp/x"
		},
	},
	{
		name: "network",
		record: func(s *ApprovalGrantStore, sid, agent string) bool {
			return s.RecordNetworkGrant(sid, agent)
		},
		holds: func(s *ApprovalGrantStore, sid, agent string) bool {
			return s.HasNetworkGrant(sid, agent)
		},
	},
}

// TestApprovalGrantStore_AllFourKinds_NotInheritedByDelegateAgent replaces
// TestApprovalGrantStore_InheritFrom_AllFourKindsInheritOnDelegation, which
// proved D7/D8's old text ("a delegate inherits it via the same InheritFrom
// mechanism as command grants").
//
// FLIPPED by C-26 (row ApprovalGrantStore.InheritFrom: "copies no grants
// (self-helpers share the key; other agents get nothing)"; security point 5):
// none of the four kinds reaches a delegate agent. KEPT: the original fixture
// (all four kinds held by the parent in one store) and the delegator's own
// allowances staying in place after the call.
func TestApprovalGrantStore_AllFourKinds_NotInheritedByDelegateAgent(t *testing.T) {
	s := NewApprovalGrantStore()
	for _, k := range allowanceKindFixtures {
		if !k.record(s, "parent-session", "parent-agent") {
			t.Fatalf("fixture: recording the %s allowance for the parent must succeed", k.name)
		}
		// Instrument: the reader sees what the recorder stored, under that key.
		if !k.holds(s, "parent-session", "parent-agent") {
			t.Fatalf("instrument: the %s allowance recorded for {parent-session, parent-agent} must resolve for that key", k.name)
		}
		if k.holds(s, "child-session", "child-agent") {
			t.Fatalf("precondition violated: the %s allowance must not resolve for the delegate before the delegation step", k.name)
		}
	}

	s.InheritFrom("parent-session", "parent-agent", "child-session", "child-agent")

	for _, k := range allowanceKindFixtures {
		if k.holds(s, "child-session", "child-agent") {
			t.Errorf("C-26: the %s allowance must NOT be inherited by a delegate agent (InheritFrom copies no grants)", k.name)
		}
		if !k.holds(s, "parent-session", "parent-agent") {
			t.Errorf("the delegator must keep its own %s allowance after a delegation", k.name)
		}
	}
}

// TestApprovalGrantStore_FourKinds_SharedBySameAgentAcrossSessions is the
// C-26 replacement for what TestApprovalGrantStore_InheritFrom_PathOnly
// SourceIsNotTreatedAsEmpty guarded. That test pinned a copy-path bug (a
// source holding only a path grant tripped the "no exact grants, nothing to
// inherit" short-circuit); with no copy there is no short-circuit to trip, so
// the test is RETIRED (spec row InheritFrom: copies no grants), and its
// still-applicable half is stated here: each kind ALONE in a store is visible
// to the same agent in another chat with no copy step (C-26 Key and Covers),
// and to no other agent in either chat.
func TestApprovalGrantStore_FourKinds_SharedBySameAgentAcrossSessions(t *testing.T) {
	for _, k := range allowanceKindFixtures {
		t.Run(k.name, func(t *testing.T) {
			s := NewApprovalGrantStore()
			if !k.record(s, "session-1", "agent-a") {
				t.Fatalf("fixture: recording the %s allowance must succeed", k.name)
			}
			if !k.holds(s, "session-1", "agent-a") {
				t.Fatalf("instrument: the %s allowance recorded for {session-1, agent-a} must resolve for that key", k.name)
			}

			if !k.holds(s, "session-2", "agent-a") {
				t.Errorf("C-26 Key/Covers: the %s allowance answered in session-1 must resolve for the same agent in session-2", k.name)
			}
			if k.holds(s, "session-1", "agent-b") || k.holds(s, "session-2", "agent-b") {
				t.Errorf("C-26 Covers: another agent must not hold agent-a's %s allowance in any chat", k.name)
			}
		})
	}
}

// TestApprovalGrantStore_ClearSession_KeepsAllFourKinds replaces
// TestApprovalGrantStore_ClearSession_ClearsAllFourKinds.
//
// FLIPPED by C-26 (Code paths that change, row ApprovalGrantStore.ClearSession:
// "no longer clears allowances (they belong to the agent)"; Lifetime and scope,
// row End: an allowance ends at its time, at a gateway stop or restart, when
// its agent is deleted or when the permission it answers changes — a chat
// closing is none of those): closing a chat ends none of the four kinds, in
// that chat or any other chat of the agent.
// KEPT: the original fixture (all four kinds in one store, the same four
// subjects) and its agent/session names. The egress-token half of ClearSession
// ("still revokes the session's egress tokens") is pinned in
// approvalgrants_network_hosts_test.go.
func TestApprovalGrantStore_ClearSession_KeepsAllFourKinds(t *testing.T) {
	s := NewApprovalGrantStore()
	s.Record("session-1", "agent-a", "bash", nil)
	s.RecordPrefixGrant("session-1", "agent-a", "bash", ShellPrefixGrant{Binary: "/bin/true"})
	s.RecordPathGrant("session-1", "agent-a", fspolicy.PathGrant{Path: "/tmp/x", Access: fspolicy.PathGrantAccessWrite})
	s.RecordNetworkGrant("session-1", "agent-a")

	// INSTRUMENT (true before and after C-26): each reader below sees its kind,
	// so "still there after the close" is a statement about the close.
	if !s.IsAllowed("session-1", "agent-a", "bash", nil) ||
		!s.IsPrefixAllowed("session-1", "agent-a", "bash", "/bin/true", nil, false) ||
		len(s.PathGrantsFor("session-1", "agent-a")) != 1 ||
		!s.HasNetworkGrant("session-1", "agent-a") {
		t.Fatal("instrument: all four kinds recorded for {session-1, agent-a} must resolve before the close")
	}

	s.ClearSession("session-1")

	if !s.IsAllowed("session-1", "agent-a", "bash", nil) {
		t.Error("C-26: exact allowance must survive a chat close")
	}
	if !s.IsPrefixAllowed("session-1", "agent-a", "bash", "/bin/true", nil, false) {
		t.Error("C-26: prefix allowance must survive a chat close")
	}
	if got := s.PathGrantsFor("session-1", "agent-a"); len(got) != 1 || got[0].Path != "/tmp/x" {
		t.Errorf("C-26: path allowance must survive a chat close, got %+v", got)
	}
	if !s.HasNetworkGrant("session-1", "agent-a") {
		t.Error("C-26: network allowance must survive a chat close")
	}
	// The allowance belongs to the agent, so another chat of the agent still
	// sees it and another agent still does not.
	if !s.IsAllowed("session-2", "agent-a", "bash", nil) {
		t.Error("C-26: the agent's exact allowance must still resolve in another chat after the close")
	}
	if s.IsAllowed("session-1", "agent-b", "bash", nil) {
		t.Error("a chat close must not hand another agent the allowance")
	}
}

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
	if s.HasNetworkGrant("session-2", "agent-a") {
		t.Error("a different session must not inherit the network grant")
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

// TestApprovalGrantStore_InheritFrom_AllFourKindsInheritOnDelegation proves
// D7/D8's own text ("a delegate inherits it via the same InheritFrom
// mechanism as command grants", "same session/delegate/clear-on-close
// lifetime as PathGrants"): a session holding ONLY a path grant — no exact,
// no prefix, no network grant — must still be recognized as a non-empty
// source and copied, not silently treated as an empty source and skipped.
func TestApprovalGrantStore_InheritFrom_AllFourKindsInheritOnDelegation(t *testing.T) {
	s := NewApprovalGrantStore()
	s.Record("parent-session", "parent-agent", "bash", map[string]any{"command": "ls"})
	s.RecordPrefixGrant("parent-session", "parent-agent", "bash", ShellPrefixGrant{Binary: "/usr/bin/npm", ArgPrefix: "run test"})
	s.RecordPathGrant("parent-session", "parent-agent", fspolicy.PathGrant{Path: "/tmp/x", Access: fspolicy.PathGrantAccessWrite})
	s.RecordNetworkGrant("parent-session", "parent-agent")

	s.InheritFrom("parent-session", "parent-agent", "child-session", "child-agent")

	if !s.IsAllowed("child-session", "child-agent", "bash", map[string]any{"command": "ls"}) {
		t.Error("exact grant must inherit")
	}
	if !s.IsPrefixAllowed("child-session", "child-agent", "bash", "/usr/bin/npm", []string{"run", "test"}, false) {
		t.Error("prefix grant must inherit")
	}
	grants := s.PathGrantsFor("child-session", "child-agent")
	if len(grants) != 1 || grants[0].Path != "/tmp/x" {
		t.Errorf("path grant must inherit, got %+v", grants)
	}
	if !s.HasNetworkGrant("child-session", "child-agent") {
		t.Error("network grant must inherit")
	}
}

// TestApprovalGrantStore_InheritFrom_PathOnlySourceIsNotTreatedAsEmpty is the
// narrow regression for the bug this lane's own InheritFrom fix closes: a
// source holding ONLY a path grant (no exact-fingerprint grant at all) used
// to trip the function's original "len(srcSet) == 0 => nothing to inherit"
// short-circuit before the new grant-kind copy blocks ever ran.
func TestApprovalGrantStore_InheritFrom_PathOnlySourceIsNotTreatedAsEmpty(t *testing.T) {
	s := NewApprovalGrantStore()
	s.RecordPathGrant("parent-session", "parent-agent", fspolicy.PathGrant{Path: "/tmp/only-path", Access: fspolicy.PathGrantAccessRead})

	s.InheritFrom("parent-session", "parent-agent", "child-session", "child-agent")

	grants := s.PathGrantsFor("child-session", "child-agent")
	if len(grants) != 1 || grants[0].Path != "/tmp/only-path" {
		t.Errorf("a path-only source must still inherit, got %+v", grants)
	}
	if s.InheritSourceMissCount() != 0 {
		t.Errorf("a path-only source must NOT be counted as a source miss, got %d", s.InheritSourceMissCount())
	}
}

func TestApprovalGrantStore_ClearSession_ClearsAllFourKinds(t *testing.T) {
	s := NewApprovalGrantStore()
	s.Record("session-1", "agent-a", "bash", nil)
	s.RecordPrefixGrant("session-1", "agent-a", "bash", ShellPrefixGrant{Binary: "/bin/true"})
	s.RecordPathGrant("session-1", "agent-a", fspolicy.PathGrant{Path: "/tmp/x", Access: fspolicy.PathGrantAccessWrite})
	s.RecordNetworkGrant("session-1", "agent-a")

	s.ClearSession("session-1")

	if s.IsAllowed("session-1", "agent-a", "bash", nil) {
		t.Error("exact grant must clear")
	}
	if s.IsPrefixAllowed("session-1", "agent-a", "bash", "/bin/true", nil, false) {
		t.Error("prefix grant must clear")
	}
	if got := s.PathGrantsFor("session-1", "agent-a"); got != nil {
		t.Errorf("path grant must clear, got %+v", got)
	}
	if s.HasNetworkGrant("session-1", "agent-a") {
		t.Error("network grant must clear")
	}
}

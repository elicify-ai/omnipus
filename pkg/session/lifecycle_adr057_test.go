// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-057 W6, U13 — coverage for LifecycleFilter.ParentDurableKey (FR-019),
// the doc-comment corrections (FR-022, U13's two-of-three sites), and the
// U13-scoped slice of FR-023's static gate. Per binding Rule 1, every
// behavioural assertion runs against a REAL *LifecycleStore rooted at a
// t.TempDir() — never a spy/fake standing in for the store.

package session

import (
	"os"
	"strings"
	"testing"
)

// TestLifecycleFilter_ParentDurableKey_DirectChildrenOnly is test #10 / BDD-18
// ("Children-of-X returns direct children only"): given persisted lifecycle
// records for chat A, its child B, its OTHER child C, and B's own child
// (grandchild) D, List(LifecycleFilter{ParentDurableKey: A}) must return
// exactly B and C — never D, and never A itself.
func TestLifecycleFilter_ParentDurableKey_DirectChildrenOnly(t *testing.T) {
	s := newTestLifecycleStore(t)

	// FR-074 — every id here is deliberately distinct; asserted below rather
	// than merely hoped for.
	const (
		chatA  = "chat-A-10"
		childB = "child-B-10"
		childC = "child-C-10"
		grandD = "grandchild-D-10"
	)
	ids := []string{chatA, childB, childC, grandD}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			t.Fatalf("fixture bug: id %q reused — FR-074 requires pairwise-distinct ids", id)
		}
		seen[id] = true
	}

	// A itself: a top-level chat, no parent.
	if err := s.Persist(&LifecycleRecord{
		SessionID: chatA, State: LifecycleRunning,
		OwnerScopeKind: OwnerScopeHuman,
		WorkspaceID:    "ws-1", AgentID: "mia",
	}); err != nil {
		t.Fatalf("persist chatA: %v", err)
	}
	// B and C: direct children of A.
	for _, id := range []string{childB, childC} {
		if err := s.Persist(&LifecycleRecord{
			SessionID: id, State: LifecycleRunning,
			OwnerScopeKind: OwnerScopeParentSession, OwnerScopeID: chatA,
			ParentAgentID: "mia", ParentDurableKey: chatA,
			WorkspaceID: "ws-1", AgentID: "ray",
		}); err != nil {
			t.Fatalf("persist %q: %v", id, err)
		}
	}
	// D: a GRANDCHILD — B's own child, ParentDurableKey = B (its DIRECT
	// parent), never A.
	if err := s.Persist(&LifecycleRecord{
		SessionID: grandD, State: LifecycleRunning,
		OwnerScopeKind: OwnerScopeParentSession, OwnerScopeID: childB,
		ParentAgentID: "ray", ParentDurableKey: childB,
		WorkspaceID: "ws-1", AgentID: "ava",
	}); err != nil {
		t.Fatalf("persist grandD: %v", err)
	}

	got, err := s.List(LifecycleFilter{ParentDurableKey: chatA})
	if err != nil {
		t.Fatalf("List(ParentDurableKey=%q): %v", chatA, err)
	}
	gotIDs := map[string]bool{}
	for _, r := range got {
		gotIDs[r.SessionID] = true
	}
	if len(gotIDs) != 2 || !gotIDs[childB] || !gotIDs[childC] {
		t.Fatalf("List(ParentDurableKey=%q) = %v, want exactly [%q %q]", chatA, listedSessionIDs(got), childB, childC)
	}
	if gotIDs[grandD] {
		t.Fatalf("List(ParentDurableKey=%q) leaked the GRANDCHILD %q — must be depth-1 only", chatA, grandD)
	}
	if gotIDs[chatA] {
		t.Fatalf("List(ParentDurableKey=%q) leaked %q itself", chatA, chatA)
	}

	// The reciprocal query: children of B is exactly D.
	got, err = s.List(LifecycleFilter{ParentDurableKey: childB})
	if err != nil {
		t.Fatalf("List(ParentDurableKey=%q): %v", childB, err)
	}
	if len(got) != 1 || got[0].SessionID != grandD {
		t.Fatalf("List(ParentDurableKey=%q) = %v, want exactly [%q]", childB, listedSessionIDs(got), grandD)
	}

	// A parent with no children at all returns an empty, non-nil-error slice.
	got, err = s.List(LifecycleFilter{ParentDurableKey: "nobody-persisted-this-10"})
	if err != nil {
		t.Fatalf("List(ParentDurableKey=<absent>): %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("List(ParentDurableKey=<absent>) = %v, want empty", listedSessionIDs(got))
	}
}

// TestLifecycleFilter_ParentDurableKey_ComposesWithOtherFilterFields proves
// the index-backed path (listByParentDurableKey) still applies every OTHER
// LifecycleFilter field exactly like the full-scan path does — the index
// only narrows the CANDIDATE set, it never bypasses filter.matches.
func TestLifecycleFilter_ParentDurableKey_ComposesWithOtherFilterFields(t *testing.T) {
	s := newTestLifecycleStore(t)
	const parent = "chat-compose-10b"
	const runningChild = "child-compose-10b-running"
	const doneChild = "child-compose-10b-done"

	if err := s.Persist(&LifecycleRecord{
		SessionID: runningChild, State: LifecycleRunning,
		OwnerScopeKind: OwnerScopeParentSession, OwnerScopeID: parent,
		ParentDurableKey: parent, WorkspaceID: "ws-1", AgentID: "ray",
	}); err != nil {
		t.Fatalf("persist runningChild: %v", err)
	}
	if err := s.Persist(&LifecycleRecord{
		SessionID: doneChild, State: LifecycleCompleted,
		OwnerScopeKind: OwnerScopeParentSession, OwnerScopeID: parent,
		ParentDurableKey: parent, WorkspaceID: "ws-1", AgentID: "ava",
	}); err != nil {
		t.Fatalf("persist doneChild: %v", err)
	}

	got, err := s.List(LifecycleFilter{ParentDurableKey: parent, NonTerminalOnly: true})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].SessionID != runningChild {
		t.Fatalf("List(ParentDurableKey=%q, NonTerminalOnly=true) = %v, want exactly [%q] — "+
			"the index path must still apply NonTerminalOnly, not just resolve the candidate set",
			parent, listedSessionIDs(got), runningChild)
	}

	got, err = s.List(LifecycleFilter{ParentDurableKey: parent, AgentID: "ava"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].SessionID != doneChild {
		t.Fatalf("List(ParentDurableKey=%q, AgentID=ava) = %v, want exactly [%q]", parent, listedSessionIDs(got), doneChild)
	}
}

// --- FR-022 doc-comment corrections (U13's two of the three named sites) ---
//
// The third site, pkg/tools/list_jobs_sources.go:311-315, is owned by U14
// and is out of this unit's write scope (and this test's reach, since it
// lives in a different package with its own unexported symbols); it is
// covered by U14's own gate. This test covers exactly the two blocks that
// live inside pkg/session/lifecycle.go, which U13 owns.

// docCommentGateAnchors locates the two U13-owned doc-comment blocks FR-022
// requires rewriting, each identified by a stable substring from the
// CURRENT (post-rewrite) text — not a hardcoded line number, since editing
// this file (adding the ParentDurableKey field/clause/index wiring) shifts
// every line below it. Returns the located blocks' surrounding text windows
// for content assertions.
func docCommentGateAnchors(t *testing.T, src string) (parentAgentIDFieldBlock, matchesClauseBlock string) {
	t.Helper()

	// Block 1 — the ParentAgentID struct field's "MUST NOT be inferred"
	// bullet list (pre-rewrite: lifecycle.go:225-228 named the retracted
	// claim inside this block's ParentDurableKey bullet).
	anchor1 := "MUST NOT be inferred from any other field, ever:"
	idx1 := strings.Index(src, anchor1)
	if idx1 < 0 {
		t.Fatal("FR-022 gate: could not locate the ParentAgentID struct-field doc comment block " +
			"(anchor 'MUST NOT be inferred from any other field, ever:' not found) — the gate cannot run")
	}
	end1 := idx1 + 600
	if end1 > len(src) {
		end1 = len(src)
	}

	// Block 2 — the matches() ParentAgentID-clause inline comment
	// (pre-rewrite: lifecycle.go:572-575).
	anchor2 := "// ParentAgentID is matched against the record's OWN ParentAgentID and"
	idx2 := strings.Index(src, anchor2)
	if idx2 < 0 {
		t.Fatal("FR-022 gate: could not locate the matches() ParentAgentID-clause comment block " +
			"(anchor '// ParentAgentID is matched against the record's OWN ParentAgentID and' not found) — the gate cannot run")
	}
	end2 := idx2 + 400
	if end2 > len(src) {
		end2 = len(src)
	}

	return src[idx1:end1], src[idx2:end2]
}

// TestLifecycleDocComments_NoSharedParentChildClaim_U13Scope is U13's slice
// of test #12 (BDD-22, FR-022): reads the ACTUAL pkg/session/lifecycle.go
// source from disk and asserts, in order:
//  1. (positive lower bound, binding Rule 4) both U13-owned blocks are
//     LOCATED by anchor text;
//  2. (negative) neither block describes ParentDurableKey as shared
//     parent<->child in the retracted, pre-ADR-057 sense ("SHARES with its
//     child", "cousins", "grandchildren" as a LEAK, "(shared parent<->child)").
func TestLifecycleDocComments_NoSharedParentChildClaim_U13Scope(t *testing.T) {
	src, err := os.ReadFile("lifecycle.go")
	if err != nil {
		t.Fatalf("read lifecycle.go: %v", err)
	}
	content := string(src)

	// Step 1 — positive lower bound: both blocks must be located BEFORE any
	// content assertion runs (a missing anchor is a hard test failure inside
	// docCommentGateAnchors, never a silent pass-through).
	block1, block2 := docCommentGateAnchors(t, content)

	// Step 2 — the negative assertion the FR/BDD actually requires.
	retractedPhrases := []string{
		"SHARES with its child",
		"cousins and grandchildren",
		"(shared parent↔child)",
		"the sibling/cousin/grandchild leak",
	}
	for _, phrase := range retractedPhrases {
		if strings.Contains(block1, phrase) {
			t.Errorf("ParentAgentID field doc comment still contains the retracted claim %q — "+
				"FR-022 requires it no longer describe ParentDurableKey as shared parent<->child", phrase)
		}
		if strings.Contains(block2, phrase) {
			t.Errorf("matches() ParentAgentID-clause comment still contains the retracted claim %q — "+
				"FR-022 requires it no longer describe ParentDurableKey as shared parent<->child", phrase)
		}
	}

	// Positive corroboration: both blocks now correctly attribute
	// ParentDurableKey's D1 semantics rather than merely NOT containing the
	// old phrasing (silence about a field is not the same as correcting the
	// claim about it).
	if !strings.Contains(block1, "ParentDurableKey") {
		t.Error("ParentAgentID field doc comment no longer mentions ParentDurableKey at all — " +
			"the corrected block must still explain why ParentAgentID cannot be inferred from it")
	}
	if !strings.Contains(block2, "ParentDurableKey") {
		t.Error("matches() ParentAgentID-clause comment no longer mentions ParentDurableKey at all")
	}
}

// --- FR-023 (U13-scoped slice of the joint static gate, test #106) ---

// extractFunctionBody returns the source text of the named top-level
// function, from its `func` keyword through its own closing brace (assumed
// to be the first line consisting of exactly "}" after the signature — true
// for every top-level Go function, since every NESTED block's closing brace
// is indented). Used to scope the FR-023 gate to exactly matches()'s body
// rather than the whole file.
func extractFunctionBody(t *testing.T, src, funcAnchor string) string {
	t.Helper()
	start := strings.Index(src, funcAnchor)
	if start < 0 {
		t.Fatalf("could not locate function anchor %q", funcAnchor)
	}
	rest := src[start:]
	end := strings.Index(rest, "\n}\n")
	if end < 0 {
		t.Fatalf("could not locate the closing brace of the function starting %q", funcAnchor)
	}
	return rest[:end+len("\n}")]
}

// TestLifecycleFilterMatches_ParentDurableKeyIsTheParentageReadU13Scope is
// U13's slice of FR-023 / test #106 ("the system MUST NOT use OwnerScopeID
// or ParentAgentID as the parentage edge"). The FULL cross-package gate
// (verifyCallerOwnsSession + callerOwnerKey + their transitive callees in
// pkg/tools, jointly owned with U14) is out of this unit's write scope —
// LifecycleFilter.matches is unexported, so that gate necessarily parses
// this file's source rather than importing the symbol, and U14 owns writing
// it. This test covers exactly the piece U13 is accountable for: matches()
// ITSELF, read from the real, current source.
//
// Per binding Rule 4, the positive lower bound (>= 1 real ParentDurableKey
// read) is asserted FIRST, proving the extraction actually reached the
// function body before the zero-count clause is trusted.
func TestLifecycleFilterMatches_ParentDurableKeyIsTheParentageReadU13Scope(t *testing.T) {
	src, err := os.ReadFile("lifecycle.go")
	if err != nil {
		t.Fatalf("read lifecycle.go: %v", err)
	}
	body := extractFunctionBody(t, string(src), "func (f LifecycleFilter) matches(r *LifecycleRecord) bool {")

	// Positive lower bound (Rule 4): matches() must actually read
	// r.ParentDurableKey at least once — the FR-019 clause this unit added.
	if got := strings.Count(body, "r.ParentDurableKey"); got < 1 {
		t.Fatalf("positive lower bound failed: matches() contains %d reads of r.ParentDurableKey, want >= 1 — "+
			"either the FR-019 clause is missing or this gate's extraction is broken", got)
	}

	// Negative assertion: matches() must never read the RECORD's own
	// OwnerScopeID field as a parentage predicate. (r.ParentAgentID legitimately
	// appears — that is the SEPARATE, pre-existing, agent-identity axis
	// FR-013 established; FR-023 forbids OwnerScopeID/ParentAgentID being
	// used AS THE PARENTAGE EDGE, not the existence of ParentAgentID's own,
	// unrelated filter clause. Zero r.OwnerScopeID reads is the part of that
	// prohibition this file alone can assert.)
	if strings.Contains(body, "r.OwnerScopeID") {
		t.Error("matches() reads r.OwnerScopeID — FR-023 forbids using OwnerScopeID as (part of) the parentage edge")
	}
}

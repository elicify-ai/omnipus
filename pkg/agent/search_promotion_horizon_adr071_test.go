// Omnipus — ADR-071 D3 §4.3.1(a) / §4.6 tests
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// search_promotion_horizon_adr071_test.go covers the pendingSearchPromotions
// side table (§4.3.1a: recordPendingSearchPromotions, clearPendingSearchPromotion,
// tickSearchPromotionHorizon) and the composite (agent, session) bucket's
// remaining §4.6 required tests not already covered by
// tool_manifest_adr057_test.go: round-trip preservation and the no-bucket-leak
// assertion, extended here to cover the side table too (§4.3.1a r5 point (i)).
//
// Binding rule 1 (real state, never a spy): every assertion below drives the
// real AgentLoop methods over real maps and reads back what is actually
// there. Nothing asserts that a function ran.

package agent

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/tools"
)

// newSearchPromotionLoop returns the minimal REAL AgentLoop the
// pendingSearchPromotions / bucketTurnCounter helpers need.
func newSearchPromotionLoop() *AgentLoop {
	return &AgentLoop{
		loadedTools:             make(map[string]map[string]bool),
		pendingSearchPromotions: make(map[string]map[string]int),
		bucketTurnCounter:       make(map[string]int),
	}
}

// TestBucketKey_RoundTripPreservation is §4.6's second required case: after A
// loads a tool, the conversation switches to B, then switches BACK to A — A's
// bucket still contains what it loaded before the switch away (distinct
// buckets, not a reset).
func TestBucketKey_RoundTripPreservation(t *testing.T) {
	al := newSearchPromotionLoop()
	const (
		sid      = "sess-roundtrip"
		toolName = "browser_navigate"
	)
	bucketA := manifestBucketKey("jim", sid, sid)
	bucketB := manifestBucketKey("ava", sid, sid)

	al.markToolsLoaded(bucketA, []string{toolName})
	if !al.sessionLoadedTools(bucketA)[toolName] {
		t.Fatalf("fixture: %q must be loaded in A's bucket", toolName)
	}

	// "Switch" to B: B's bucket is untouched and empty.
	if got := al.sessionLoadedTools(bucketB); len(got) != 0 {
		t.Fatalf("fixture: B's bucket must start empty, got %v", got)
	}

	// "Switch back" to A: A's bucket must still contain the tool — the
	// switch away must not have reset or evicted it.
	if !al.sessionLoadedTools(bucketA)[toolName] {
		t.Error("round-trip: A's loaded tool must still be present after switching to B and back — " +
			"the switch away must not reset A's bucket")
	}
}

// TestBucketKey_NoLeakAfterSessionClose is §4.6's third required case (the
// one that "ships green if missed"): after forgetSession(sessionID) on a
// session that had loads under TWO different agent ids, len(al.loadedTools)
// must be 0 — an exact-key delete would match neither composite key and
// silently leak both.
func TestBucketKey_NoLeakAfterSessionClose(t *testing.T) {
	al := newSearchPromotionLoop()
	const sid = "sess-two-agent-leak"

	bucketA := manifestBucketKey("jim", sid, sid)
	bucketB := manifestBucketKey("ava", sid, sid)
	al.markToolsLoaded(bucketA, []string{"browser_navigate"})
	al.markToolsLoaded(bucketB, []string{"send_email"})
	if len(al.loadedTools) != 2 {
		t.Fatalf("fixture: expected 2 buckets before forgetSession, got %d", len(al.loadedTools))
	}

	al.forgetSession(sid)

	if n := len(al.loadedTools); n != 0 {
		t.Errorf("loadedTools must be empty after forgetSession(%q) for a session with two agent "+
			"buckets, got %d entries: %v — an exact-key delete leaks composite keys silently", sid, n, al.loadedTools)
	}
}

// TestPendingDiscoveries_CrossAgentIsolation is §4.3.1(a)'s required test 3:
// A promotes tool T via the query path; the conversation switches to B; B
// calls T inside the horizon (clearing B's OWN pending entry, which never
// existed since B never searched for it — only dispatch-clears an entry that
// exists); driving past the horizon for A's bucket must still fire the
// no-followup counter exactly once, because A's promotion was genuinely
// wasted regardless of what B did with the same tool name under its own key.
func TestPendingDiscoveries_CrossAgentIsolation(t *testing.T) {
	al := newSearchPromotionLoop()
	const (
		sid      = "sess-cross-agent-pending"
		toolName = "browser_navigate"
	)
	bucketA := manifestBucketKey("jim", sid, sid)
	bucketB := manifestBucketKey("ava", sid, sid)

	// A discovers T via query path — pending entry recorded under A's bucket.
	al.recordPendingSearchPromotions(bucketA, []string{toolName})

	// B "calls" T within the horizon — this only clears B's own (nonexistent)
	// pending entry; it must NOT reach into A's bucket.
	al.clearPendingSearchPromotion(bucketB, toolName)

	before := tools.ToolSearchNoFollowUpCalls()

	// Drive A's bucket past the horizon.
	for i := 0; i < searchPromotionHorizonTurns+1; i++ {
		al.tickSearchPromotionHorizon(bucketA)
	}

	after := tools.ToolSearchNoFollowUpCalls()
	if after != before+1 {
		t.Errorf("omnipus_toolsearch_no_followup_total delta = %d, want 1 — A's promotion was "+
			"wasted regardless of B's unrelated activity under a different bucket", after-before)
	}

	// A's own pending entry must be gone after firing (fires exactly once).
	al.loadedToolsMu.Lock()
	remaining := len(al.pendingSearchPromotions[bucketA])
	al.loadedToolsMu.Unlock()
	if remaining != 0 {
		t.Errorf("A's pendingSearchPromotions bucket must be empty after the horizon fires, got %d entries", remaining)
	}
}

// TestPendingDiscoveries_UsedNeverCounted is the negative twin: if the SAME
// bucket that promoted a tool then calls it within the horizon, the
// no-followup counter must never fire for that discovery.
func TestPendingDiscoveries_UsedNeverCounted(t *testing.T) {
	al := newSearchPromotionLoop()
	const (
		sid      = "sess-used-not-counted"
		toolName = "browser_navigate"
	)
	bucket := manifestBucketKey("jim", sid, sid)

	al.recordPendingSearchPromotions(bucket, []string{toolName})
	// Used on the very next turn.
	al.clearPendingSearchPromotion(bucket, toolName)

	before := tools.ToolSearchNoFollowUpCalls()
	for i := 0; i < searchPromotionHorizonTurns+3; i++ {
		al.tickSearchPromotionHorizon(bucket)
	}
	after := tools.ToolSearchNoFollowUpCalls()

	if after != before {
		t.Errorf("omnipus_toolsearch_no_followup_total delta = %d, want 0 — a used discovery must never be counted", after-before)
	}
}

// TestPendingDiscoveries_ByNameLoadNeverRecorded is FR-038a's negative case:
// an exact-name `names` load must never create a pending-discovery entry in
// the first place — recordPendingSearchPromotions is the write path, and it
// is (per the production wiring in loop.go's markLoaded closure) called ONLY
// when tools.IsSearchPromotion(ctx) is true. This test pins the primitive's
// own behavior directly: if nothing ever calls recordPendingSearchPromotions
// for a name, no entry exists and the horizon can never fire for it.
func TestPendingDiscoveries_ByNameLoadNeverRecorded(t *testing.T) {
	al := newSearchPromotionLoop()
	const (
		sid      = "sess-byname-noop"
		toolName = "browser_navigate"
	)
	bucket := manifestBucketKey("jim", sid, sid)

	// Simulate a by-name load: markToolsLoaded is called (as it always is),
	// but recordPendingSearchPromotions is NOT (the by-name path in
	// tools_tool.go never sets tools.WithSearchPromotion).
	al.markToolsLoaded(bucket, []string{toolName})

	al.loadedToolsMu.Lock()
	pending := len(al.pendingSearchPromotions[bucket])
	al.loadedToolsMu.Unlock()
	if pending != 0 {
		t.Fatalf("fixture: a by-name load must never create a pending entry, found %d", pending)
	}

	before := tools.ToolSearchNoFollowUpCalls()
	for i := 0; i < searchPromotionHorizonTurns+3; i++ {
		al.tickSearchPromotionHorizon(bucket)
	}
	after := tools.ToolSearchNoFollowUpCalls()
	if after != before {
		t.Errorf("omnipus_toolsearch_no_followup_total delta = %d, want 0 for a by-name load", after-before)
	}
}

// TestPendingDiscoveries_NoSideTableLeak is §4.3.1(a) r5's required test 4:
// after forgetSession(sessionID) on a session with pending entries under TWO
// different agent ids, len(al.pendingSearchPromotions) must be 0. Sharing
// loadedToolsMu does NOT sweep this second map — the mutex protects, it does
// not enumerate — so this is the test that would ship green if the sweep
// forgot the side table.
func TestPendingDiscoveries_NoSideTableLeak(t *testing.T) {
	al := newSearchPromotionLoop()
	const sid = "sess-side-table-leak"

	bucketA := manifestBucketKey("jim", sid, sid)
	bucketB := manifestBucketKey("ava", sid, sid)
	al.recordPendingSearchPromotions(bucketA, []string{"browser_navigate"})
	al.recordPendingSearchPromotions(bucketB, []string{"send_email"})
	if len(al.pendingSearchPromotions) != 2 {
		t.Fatalf("fixture: expected 2 pending buckets before forgetSession, got %d", len(al.pendingSearchPromotions))
	}

	before := tools.ToolSearchNoFollowUpCalls()
	al.forgetSession(sid)
	after := tools.ToolSearchNoFollowUpCalls()

	if n := len(al.pendingSearchPromotions); n != 0 {
		t.Errorf("pendingSearchPromotions must be empty after forgetSession(%q) for a session with "+
			"two agent buckets, got %d entries: %v", sid, n, al.pendingSearchPromotions)
	}
	// forgetSession must count BOTH abandoned promotions (one per agent
	// bucket) before deleting them — a session shorter than the horizon must
	// still have its unused discoveries counted (Acceptance Scenario 7 /
	// the "conversation shorter than the horizon" BDD scenario).
	if after != before+2 {
		t.Errorf("omnipus_toolsearch_no_followup_total delta = %d, want 2 (one per abandoned "+
			"agent bucket, counted at session close)", after-before)
	}
	if n := len(al.bucketTurnCounter); n != 0 {
		t.Errorf("bucketTurnCounter must also be swept by forgetSession, got %d entries: %v", n, al.bucketTurnCounter)
	}
}

// TestPendingDiscoveries_HorizonIsIndependentFromMCPTTL pins FR-038's
// explicit warning against conflating searchPromotionHorizonTurns with
// cfg.Tools.MCP.Discovery.TTL: the two constants share a default value (5) by
// coincidence, not by derivation. This test does not (and cannot) assert
// non-derivation structurally, but it documents the invariant at the
// constant's own use site so a future "simplification" that points one at
// the other fails a code-review reading of this test, if not the test
// itself.
func TestPendingDiscoveries_HorizonIsIndependentFromMCPTTL(t *testing.T) {
	if searchPromotionHorizonTurns != 5 {
		t.Fatalf("searchPromotionHorizonTurns = %d, want 5 (ADR-071 §4.3.1a) — if intentionally "+
			"changed, verify it was NOT changed by pointing it at cfg.Tools.MCP.Discovery.TTL (FR-038 "+
			"forbids that conflation explicitly)", searchPromotionHorizonTurns)
	}
}

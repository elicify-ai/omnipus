package security

// approvalgrants_adr057_test.go — tests for ADR-057 W10 / unit U17a
// (pkg/security/approvalgrants.go).
//
// Per the ADR-057 session-unification spec's binding rule 5, every unit's new
// tests go in a NEW file named `<subject>_adr057_test.go`; this file is
// U17a's. It covers TDD plan rows #25, #84 and #85 (BDD-31, BDD-32, BDD-88,
// BDD-89 / US-6) and the "Inheritance source/destination" dataset rows 1-6.
//
// Binding rule 1 (real state, never a spy) is satisfied structurally: the
// subject is an in-memory store with no I/O, so every assertion below reads
// the store's own resolution (IsAllowed) or its own counters — never "the
// function was called".
//
// Corollary "distinct ids everywhere" and dataset row 5 (`parentSid ==
// childSid` is a FORBIDDEN fixture shape) are enforced by requireDistinct
// below, which every fixture calls before exercising the subject: a fixture
// whose two session ids coincide cannot distinguish a working two-key re-key
// from grill finding C-1's silent no-op, and would pass green either way.

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// requireDistinct fails the test if the source and destination session ids
// coincide. See the file comment: this is dataset row 5, and it is a fixture
// defect rather than a subject defect, so it fails loudly at construction.
func requireDistinct(t *testing.T, srcSessionID, dstSessionID string) {
	t.Helper()
	if srcSessionID == "" || dstSessionID == "" {
		t.Fatalf("fixture defect: session ids must be non-empty, got src=%q dst=%q",
			srcSessionID, dstSessionID)
	}
	if srcSessionID == dstSessionID {
		t.Fatalf("fixture defect (dataset row 5): src and dst session ids must be "+
			"distinct or the test cannot distinguish a working re-key from C-1's "+
			"no-op; both were %q", srcSessionID)
	}
}

// captureLogs installs a Debug-level text handler as the default slog logger
// for the duration of the test and returns the buffer it writes into.
//
// This asserts on emitted output — real bytes produced by the subject — not on
// a recording spy, so it is inside binding rule 2 rather than an exception to
// it.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

// TestApprovalGrants_InheritFromTwoKeys is TDD plan row #25, tracing to
// BDD-31, BDD-32 and BDD-88 (US-6 acceptance scenarios 1 and 2).
//
// BDD-88:
//
//	Given  parentSessionID != childSessionID, and a grant recorded ONLY under
//	       {parentSessionID, parentAgentID}, VERIFIED ABSENT under
//	       {childSessionID, childAgentID}
//	When   InheritFrom(parentSessionID, parentAgentID, childSessionID, childAgentID)
//	       runs
//	Then   the grant resolves under {childSessionID, childAgentID}
//	And    it still resolves under {parentSessionID, parentAgentID} —
//	       inheritance is a copy, not a move
//
// The "absent before" assertion is the load-bearing one and the reason this
// test was rewritten: a test that seeds the grant under the destination key to
// begin with passes green while production hangs, because it never observes a
// transition. Everything after it is only meaningful because it ran.
func TestApprovalGrants_InheritFromTwoKeys(t *testing.T) {
	const (
		parentSid   = "sess_parent_9f13"
		childSid    = "sess_child_4a02"
		parentAgent = "jim"
		childAgent  = "ava"
		toolT       = "bash"
	)
	requireDistinct(t, parentSid, childSid)

	s := NewApprovalGrantStore()
	if !s.Record(parentSid, parentAgent, toolT, nil) {
		t.Fatal("fixture: Record on the source key must succeed")
	}

	// --- ABSENT BEFORE (BDD-88, dataset row 1). Without this the rest of the
	// test is vacuous.
	if s.IsAllowed(childSid, childAgent, toolT, nil) {
		t.Fatalf("precondition violated: %q must NOT resolve under the destination "+
			"key {%s, %s} before InheritFrom runs", toolT, childSid, childAgent)
	}

	s.InheritFrom(parentSid, parentAgent, childSid, childAgent)

	if !s.IsAllowed(childSid, childAgent, toolT, nil) {
		t.Errorf("BDD-88: after InheritFrom, %q must resolve under the destination "+
			"key {%s, %s}", toolT, childSid, childAgent)
	}
	// Copy, not move.
	if !s.IsAllowed(parentSid, parentAgent, toolT, nil) {
		t.Errorf("BDD-88: inheritance is a COPY — %q must still resolve under the "+
			"source key {%s, %s}", toolT, parentSid, parentAgent)
	}
	// Scoping is otherwise unchanged: the destination AGENT under the SOURCE
	// session, and the source agent under the destination session, are both
	// still unrelated keys.
	if s.IsAllowed(parentSid, childAgent, toolT, nil) {
		t.Errorf("InheritFrom must not widen scope: {%s, %s} must not resolve",
			parentSid, childAgent)
	}
	if s.IsAllowed(childSid, parentAgent, toolT, nil) {
		t.Errorf("InheritFrom must not widen scope: {%s, %s} must not resolve",
			childSid, parentAgent)
	}
	// No no-op branch was taken.
	if got := s.InheritSourceMissCount(); got != 0 {
		t.Errorf("source-miss count = %d, want 0 on a successful inheritance", got)
	}
	if got := s.InheritInvalidKeyCount(); got != 0 {
		t.Errorf("invalid-key count = %d, want 0 on a successful inheritance", got)
	}

	// --- Dataset row 4: a destination that ALREADY holds a grant of its own
	// gets the union, not a replacement.
	const toolU = "web_fetch"
	if !s.Record(childSid, childAgent, toolU, nil) {
		t.Fatal("fixture: Record on the destination key must succeed")
	}
	s.InheritFrom(parentSid, parentAgent, childSid, childAgent)
	if !s.IsAllowed(childSid, childAgent, toolT, nil) || !s.IsAllowed(childSid, childAgent, toolU, nil) {
		t.Errorf("dataset row 4: destination must hold the UNION {%s, %s}, got T=%v U=%v",
			toolT, toolU,
			s.IsAllowed(childSid, childAgent, toolT, nil),
			s.IsAllowed(childSid, childAgent, toolU, nil))
	}
	// The union must not have leaked back into the source.
	if s.IsAllowed(parentSid, parentAgent, toolU, nil) {
		t.Errorf("dataset row 4: the destination's own grant %q must not leak into "+
			"the source key", toolU)
	}

	// --- BDD-32: the child's inherited grant does not outlive the child, and
	// clearing the child leaves the parent untouched.
	s.ClearSession(childSid)
	if s.IsAllowed(childSid, childAgent, toolT, nil) || s.IsAllowed(childSid, childAgent, toolU, nil) {
		t.Errorf("BDD-32: after ClearSession(%s) the child's grant set must be gone", childSid)
	}
	if !s.IsAllowed(parentSid, parentAgent, toolT, nil) {
		t.Errorf("BDD-32: clearing the child must leave the PARENT's grant set untouched")
	}
}

// TestApprovalGrants_InheritFrom_SelfDelegationUnion is TDD plan row #85,
// tracing to BDD-88 and dataset row 3.
//
// Self-delegation is a same-AGENT, different-SESSION union. It is the case the
// retired single-key form cannot express at all: with one session parameter,
// srcAgentID == dstAgentID collapses the whole call to an identity.
func TestApprovalGrants_InheritFrom_SelfDelegationUnion(t *testing.T) {
	const (
		parentSid = "sess_self_parent_0001"
		childSid  = "sess_self_child_0002"
		agentX    = "ray"
		toolT     = "bash"
	)
	requireDistinct(t, parentSid, childSid)

	s := NewApprovalGrantStore()
	if !s.Record(parentSid, agentX, toolT, nil) {
		t.Fatal("fixture: Record must succeed")
	}
	if s.IsAllowed(childSid, agentX, toolT, nil) {
		t.Fatalf("precondition violated: %q must NOT resolve under {%s, %s} before "+
			"InheritFrom runs", toolT, childSid, agentX)
	}

	s.InheritFrom(parentSid, agentX, childSid, agentX)

	if !s.IsAllowed(childSid, agentX, toolT, nil) {
		t.Errorf("dataset row 3: same agent, different session — %q must resolve "+
			"under the destination session %s", toolT, childSid)
	}
	if !s.IsAllowed(parentSid, agentX, toolT, nil) {
		t.Errorf("dataset row 3: the source session %s must be untouched", parentSid)
	}
	if got := s.InheritSourceMissCount(); got != 0 {
		t.Errorf("source-miss count = %d, want 0", got)
	}
}

// TestApprovalGrants_InheritFrom_SourceMissIsNotSilent is TDD plan row #84,
// tracing to BDD-89 (US-6 acceptance scenario 7) and dataset rows 2 and 6.
//
// BDD-89:
//
//	Given  a source that holds NO grants
//	When   InheritFrom runs
//	Then   a log record names the source key, the destination key and the fact
//	       that there was nothing to inherit
//	And    a counter increments across the call
//	But    the function does not return silently as the retired single-key
//	       Inherit did
//
// The counter is the tripwire the spec asks for: it is what makes a future
// re-key that misses its source visible instead of green.
func TestApprovalGrants_InheritFrom_SourceMissIsNotSilent(t *testing.T) {
	const (
		parentSid   = "sess_empty_parent_aa11"
		childSid    = "sess_empty_child_bb22"
		parentAgent = "jim"
		childAgent  = "ava"
	)
	requireDistinct(t, parentSid, childSid)

	logs := captureLogs(t)
	s := NewApprovalGrantStore()

	// --- Dataset row 2: well-formed keys, but the source holds nothing.
	if got := s.InheritSourceMissCount(); got != 0 {
		t.Fatalf("fixture: source-miss count must start at 0, got %d", got)
	}
	s.InheritFrom(parentSid, parentAgent, childSid, childAgent)

	if got := s.InheritSourceMissCount(); got != 1 {
		t.Errorf("BDD-89: source-miss count = %d, want 1 — the empty-source branch "+
			"must be COUNTED, not a bare return", got)
	}
	out := logs.String()
	for _, want := range []string{parentSid, parentAgent, childSid, childAgent, "no grants to inherit"} {
		if !strings.Contains(out, want) {
			t.Errorf("BDD-89: the log record must name %q; got:\n%s", want, out)
		}
	}
	// The no-op is still a no-op: nothing was invented under either key.
	if s.IsAllowed(childSid, childAgent, "bash", nil) || s.IsAllowed(parentSid, parentAgent, "bash", nil) {
		t.Error("BDD-89: a source miss must not create any grant")
	}

	// --- Dataset row 6: an empty key component is a CALLER defect, counted
	// separately and logged at Warn, and it must not be confused with a
	// legitimate empty source set.
	before := s.InheritSourceMissCount()
	s.InheritFrom("", parentAgent, childSid, childAgent)
	s.InheritFrom(parentSid, "", childSid, childAgent)
	s.InheritFrom(parentSid, parentAgent, "", childAgent)
	s.InheritFrom(parentSid, parentAgent, childSid, "")
	if got := s.InheritInvalidKeyCount(); got != 4 {
		t.Errorf("dataset row 6: invalid-key count = %d, want 4 (one per empty "+
			"component)", got)
	}
	if got := s.InheritSourceMissCount(); got != before {
		t.Errorf("dataset row 6: an invalid key must not be counted as a source "+
			"miss; count moved %d -> %d", before, got)
	}
	if !strings.Contains(logs.String(), "level=WARN") {
		t.Errorf("dataset row 6: an empty key component is a caller defect and must "+
			"be logged at WARN; got:\n%s", logs.String())
	}

	// --- Nil-receiver safety: no panic, no counting, no state.
	var nilStore *ApprovalGrantStore
	nilStore.InheritFrom(parentSid, parentAgent, childSid, childAgent)
	if got := nilStore.InheritSourceMissCount(); got != 0 {
		t.Errorf("nil store: source-miss count = %d, want 0", got)
	}
	if got := nilStore.InheritInvalidKeyCount(); got != 0 {
		t.Errorf("nil store: invalid-key count = %d, want 0", got)
	}
}

// TestApprovalGrants_SingleKeyFormCannotSatisfyBDD88 is the executable form of
// ADR-057 grill finding C-1, and the permanent negative control for
// TestApprovalGrants_InheritFromTwoKeys.
//
// A single-key inheritance is, by construction, a call in which one session id
// serves as BOTH source and destination. This test drives exactly that shape
// the way v1's FR-031 described it — "Inherit's first argument MUST become the
// child's own session id" — and pins the consequence: the SOURCE lookup misses,
// so the call does nothing at all while reporting success. The same fixture
// then succeeds through the two-key form.
//
// It is written against InheritFrom rather than the retired Inherit shim on
// purpose, so that it keeps discriminating after unit U7 deletes that shim.
// The shim delegates to precisely this shape, so this covers it too.
//
// Without this control, TestApprovalGrants_InheritFromTwoKeys would still pass
// against any implementation that happened to seed both keys, and the one-line
// "fix" that caused C-1 would look indistinguishable from the real one.
func TestApprovalGrants_SingleKeyFormCannotSatisfyBDD88(t *testing.T) {
	const (
		parentSid   = "sess_c1_parent_7777"
		childSid    = "sess_c1_child_8888"
		parentAgent = "jim"
		childAgent  = "ava"
		toolT       = "bash"
	)
	requireDistinct(t, parentSid, childSid)

	s := NewApprovalGrantStore()
	if !s.Record(parentSid, parentAgent, toolT, nil) {
		t.Fatal("fixture: Record on the source key must succeed")
	}
	if s.IsAllowed(childSid, childAgent, toolT, nil) {
		t.Fatalf("precondition violated: %q must NOT resolve under {%s, %s} yet",
			toolT, childSid, childAgent)
	}

	// v1's literal FR-031: re-key the single argument to the child. One id has
	// to serve as both source and destination, so the source lookup misses.
	s.InheritFrom(childSid, parentAgent, childSid, childAgent)

	if s.IsAllowed(childSid, childAgent, toolT, nil) {
		t.Fatalf("C-1 control is broken: the single-key form must NOT be able to " +
			"carry a grant across two different session ids — if it can, this test " +
			"no longer discriminates and the two-key requirement is unproven")
	}
	if got := s.InheritSourceMissCount(); got != 1 {
		t.Errorf("the C-1 no-op must be COUNTED so it can never be silent again; "+
			"source-miss count = %d, want 1", got)
	}

	// The two-key form, same fixture, does carry it.
	s.InheritFrom(parentSid, parentAgent, childSid, childAgent)
	if !s.IsAllowed(childSid, childAgent, toolT, nil) {
		t.Errorf("InheritFrom must succeed on the fixture the single-key form fails")
	}
}

// TestApprovalGrants_InheritFrom_ConcurrentSafe exercises the two-key path
// under concurrency so the -race detector covers the lock discipline around
// the new counters, which live outside the mutex.
func TestApprovalGrants_InheritFrom_ConcurrentSafe(t *testing.T) {
	s := NewApprovalGrantStore()
	s.Record("sess_conc_src_1", "jim", "bash", nil)

	done := make(chan struct{})
	const workers = 8
	for i := 0; i < workers; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for j := 0; j < 200; j++ {
				s.InheritFrom("sess_conc_src_1", "jim", "sess_conc_dst_1", "ava")
				s.InheritFrom("sess_conc_absent", "jim", "sess_conc_dst_1", "ava")
				_ = s.IsAllowed("sess_conc_dst_1", "ava", "bash", nil)
				_ = s.InheritSourceMissCount()
			}
		}()
	}
	for i := 0; i < workers; i++ {
		<-done
	}

	if !s.IsAllowed("sess_conc_dst_1", "ava", "bash", nil) {
		t.Error("concurrent InheritFrom must still produce the destination grant")
	}
	if got, want := s.InheritSourceMissCount(), int64(workers*200); got != want {
		t.Errorf("source-miss count = %d, want %d — every miss must be counted exactly once",
			got, want)
	}
}

package security

// approvalgrants_adr057_test.go — ADR-057 W10 / unit U17a tests for
// pkg/security/approvalgrants.go, MIGRATED to C-26 of the Skills & Tools
// redesign spec (docs/internal/specs/skills-tools-redesign-spec.md, "C-26
// Timed allowances replace Always Allow on every approval card", founder
// R2-Q1, 2026-11-22). Fix round 4, candidate 6 (CHECK round 4).
//
// WHAT THE SPEC CHANGED FOR THIS FILE (section and row cited, never a line):
//   - C-26 "Lifetime and scope", row Key: "(agent id, kind, subject) ... The
//     session id is no longer part of the key."
//   - same section, row Covers: every session of that agent "and helpers it
//     starts for itself (same agent id). A helper of another agent, or a
//     delegation to another agent, is not covered and asks as usual".
//   - "How a timed allowance reaches a helper run": a helper the agent starts
//     for itself "looks up the same allowances with no copy at spawn", and
//     inheritSessionPermissions "stops copying grants (ApprovalGrantStore.
//     InheritFrom is removed or reduced to egress-token bookkeeping)".
//   - "Code paths that change", row ApprovalGrantStore.InheritFrom: "copies no
//     grants (self-helpers share the key; other agents get nothing)".
//   - "Security-relevant change", point 5: "a delegation to another agent no
//     longer inherits the delegating chat's grants."
//
// DISPOSITION OF THE FIVE ADR-057 TESTS. An expectation is flipped only where
// a quoted spec row says the old one is wrong; an assertion is retired only
// with the behaviour it pinned; every other assertion is kept.
//
//	TestApprovalGrants_InheritFromTwoKeys (BDD-31/32/88, rows 1 and 4)
//	  -> TestApprovalGrants_DelegateToAnotherAgentInheritsNothing
//	  FLIPPED: the delegate no longer gets the delegator's allowance (copy ->
//	  nothing; spec rows InheritFrom and security point 5). KEPT: copy-not-move
//	  (the delegator still holds it), no widening in either direction, the
//	  destination's own allowance survives (row 4, "not replaced").
//	  MOVED: BDD-32 (chat close) to approvalgrants_test.go::
//	  TestApprovalGrantStore_ClearSession_KeepsTheAgentAllowances, because the
//	  spec reverses it in its own row (ClearSession).
//	TestApprovalGrants_InheritFrom_SelfDelegationUnion (row #85, dataset row 3)
//	  -> TestApprovalGrants_SelfDelegationSharesAgentAllowance
//	  FLIPPED: the old precondition "must NOT resolve before InheritFrom" is
//	  what "no copy at spawn" reverses; it now must already resolve.
//	TestApprovalGrants_InheritFrom_SourceMissIsNotSilent (row #84, BDD-89)
//	  -> TestApprovalGrants_InheritFrom_InvalidKeysAreInert
//	  RETIRED with the behaviour they pinned: the source-miss counter, the
//	  invalid-key counter and the "no grants to inherit" / level=WARN log
//	  lines. They describe a copy that "copies no grants" removes: there is no
//	  source to miss. (The developer's GREEN keeps the counters; the spec does
//	  not require them, so no test does.) KEPT: an invalid key never gives
//	  anyone an allowance, the existing allowance is untouched, a nil
//	  receiver never panics.
//	TestApprovalGrants_SingleKeyFormCannotSatisfyBDD88 (grill finding C-1)
//	  -> TestApprovalGrants_OneIdInheritFromCopiesNothing
//	  RETIRED: the negative control of a copy that no longer exists (it proved
//	  the two-key copy works where the one-id copy no-ops). KEPT: the one-id
//	  form copies nothing and leaves the delegator's allowance in place.
//	TestApprovalGrants_InheritFrom_ConcurrentSafe
//	  -> TestApprovalGrants_ConcurrentAgentKeyedSafe
//	  RETIRED: the exact miss COUNT (no counter, see above). KEPT: the -race
//	  lock-discipline coverage, now with the agent-keyed scope as the oracle.
//
// Binding rule 1 (real state, never a spy): the subject is an in-memory store
// with no I/O, so every assertion reads the store's own resolution
// (IsAllowed, PathGrantsFor, ...) — never "the function was called".
//
// RECORDING: allowances here are recorded with the store's existing recording
// methods and read back through its real lookups. The spec's duration-bearing
// recorders and the clock are exercised by
// approval_grants_agent_timed_red_test.go; expiry is not this file's subject.
// A scope assertion fails on today's session-keyed store with a real
// expected-vs-actual (RED); a safety property C-26 keeps passes before and
// after and is marked as such. No test in this file is a GREEN claim.
//
// Dataset row 5 (parentSid == childSid is a FORBIDDEN fixture shape) is
// enforced by requireDistinct below: with equal session ids a test cannot
// tell a session-keyed store from an agent-keyed one and would pass either
// way.

import (
	"fmt"
	"sync"
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
			"distinct or the test cannot distinguish an agent-keyed store from a "+
			"session-keyed one; both were %q", srcSessionID)
	}
}

// TestApprovalGrants_DelegateToAnotherAgentInheritsNothing replaces
// TestApprovalGrants_InheritFromTwoKeys (BDD-31/88, dataset rows 1 and 4).
//
// C-26 (row InheritFrom; Covers; security point 5):
//
//	Given  jim holds an allowance answered in chat P and ava holds her own
//	When   jim delegates to ava in chat C and the runtime calls
//	       InheritFrom(P, jim, C, ava)
//	Then   ava does NOT hold jim's allowance, in C or in P
//	And    jim still holds it in P (copy-not-move survives as "untouched")
//	And    ava keeps her own allowance, and it does not reach jim
func TestApprovalGrants_DelegateToAnotherAgentInheritsNothing(t *testing.T) {
	const (
		parentSid   = "sess_parent_9f13"
		childSid    = "sess_child_4a02"
		parentAgent = "jim"
		childAgent  = "ava"
		toolT       = "bash"
		toolU       = "web_fetch"
	)
	requireDistinct(t, parentSid, childSid)

	s := NewApprovalGrantStore()
	if !s.Record(parentSid, parentAgent, toolT, nil) {
		t.Fatal("fixture: Record on the delegator's key must succeed")
	}
	// Dataset row 4: the destination already holds an allowance of its own.
	if !s.Record(childSid, childAgent, toolU, nil) {
		t.Fatal("fixture: Record on the delegate's key must succeed")
	}

	// INSTRUMENT (true before and after C-26): the lookup behind every negative
	// below can see each allowance under the key it was answered for. Without
	// this, "ava does not hold it" could be an observer that sees nothing.
	if !s.IsAllowed(parentSid, parentAgent, toolT, nil) {
		t.Fatalf("instrument: %q recorded for {%s, %s} must resolve for that key", toolT, parentSid, parentAgent)
	}
	if !s.IsAllowed(childSid, childAgent, toolU, nil) {
		t.Fatalf("instrument: %q recorded for {%s, %s} must resolve for that key", toolU, childSid, childAgent)
	}
	// ABSENT BEFORE (BDD-88): without it "ava does not hold it after" proves nothing.
	if s.IsAllowed(childSid, childAgent, toolT, nil) {
		t.Fatalf("precondition violated: %q must not resolve for {%s, %s} before the delegation step",
			toolT, childSid, childAgent)
	}

	s.InheritFrom(parentSid, parentAgent, childSid, childAgent)

	if s.IsAllowed(childSid, childAgent, toolT, nil) {
		t.Errorf("C-26 (InheritFrom copies no grants; security point 5): a delegation to "+
			"another agent must not inherit %q into {%s, %s}", toolT, childSid, childAgent)
	}
	if s.IsAllowed(parentSid, childAgent, toolT, nil) {
		t.Errorf("no widening: ava must not hold %q in the delegator's chat %s either", toolT, parentSid)
	}
	// Copy, not move: the delegator keeps what it had.
	if !s.IsAllowed(parentSid, parentAgent, toolT, nil) {
		t.Errorf("the delegator's allowance must survive a delegation untouched: "+
			"%q must still resolve for {%s, %s}", toolT, parentSid, parentAgent)
	}
	// Dataset row 4, migrated: the destination's own allowance is neither
	// replaced nor leaked back to the delegator.
	if !s.IsAllowed(childSid, childAgent, toolU, nil) {
		t.Errorf("dataset row 4: ava's own allowance %q must survive the delegation step", toolU)
	}
	if s.IsAllowed(parentSid, parentAgent, toolU, nil) || s.IsAllowed(childSid, parentAgent, toolU, nil) {
		t.Errorf("no widening: ava's own allowance %q must not reach jim", toolU)
	}
}

// TestApprovalGrants_OneIdInheritFromCopiesNothing is the control that
// survives of TestApprovalGrants_SingleKeyFormCannotSatisfyBDD88 (ADR-057
// grill finding C-1): the degenerate call in which one session id serves as
// both source and destination, with two different agents, hands over nothing.
// It passes on today's product and after C-26 (spec row InheritFrom: copies no
// grants), so it is a kept safety property, not a RED row.
func TestApprovalGrants_OneIdInheritFromCopiesNothing(t *testing.T) {
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
	if !s.IsAllowed(parentSid, parentAgent, toolT, nil) {
		t.Fatalf("instrument: %q recorded for {%s, %s} must resolve for that key", toolT, parentSid, parentAgent)
	}

	s.InheritFrom(childSid, parentAgent, childSid, childAgent)

	if s.IsAllowed(childSid, childAgent, toolT, nil) {
		t.Errorf("the one-id InheritFrom form must copy nothing to ava in {%s, %s}", childSid, childAgent)
	}
	if !s.IsAllowed(parentSid, parentAgent, toolT, nil) {
		t.Errorf("the one-id InheritFrom form must leave the delegator's allowance in place")
	}
}

// TestApprovalGrants_SelfDelegationSharesAgentAllowance replaces
// TestApprovalGrants_InheritFrom_SelfDelegationUnion (TDD plan row #85,
// dataset row 3).
//
// C-26 "How a timed allowance reaches a helper run": a helper the agent starts
// for itself "looks up the same allowances with no copy at spawn".
//
//	Given  ray holds an allowance answered in chat P
//	When   ray starts a helper for itself (same agent id, chat H) — with or
//	       without the runtime's InheritFrom call
//	Then   the allowance resolves in H, and already did BEFORE any inherit step
//	       (the old "must NOT resolve before InheritFrom" precondition is what
//	       the spec reverses)
//	But    another agent in H or in P still does not hold it
func TestApprovalGrants_SelfDelegationSharesAgentAllowance(t *testing.T) {
	const (
		parentSid  = "sess_self_parent_0001"
		childSid   = "sess_self_child_0002"
		agentX     = "ray"
		otherAgent = "kay"
		toolT      = "bash"
	)
	requireDistinct(t, parentSid, childSid)

	s := NewApprovalGrantStore()
	if !s.Record(parentSid, agentX, toolT, nil) {
		t.Fatal("fixture: Record must succeed")
	}
	if !s.IsAllowed(parentSid, agentX, toolT, nil) {
		t.Fatalf("instrument: %q recorded for {%s, %s} must resolve for that key", toolT, parentSid, agentX)
	}

	if !s.IsAllowed(childSid, agentX, toolT, nil) {
		t.Errorf("C-26 (helper shares, no copy at spawn): %q must resolve for the same agent %s in "+
			"its helper's chat %s before any InheritFrom runs", toolT, agentX, childSid)
	}

	s.InheritFrom(parentSid, agentX, childSid, agentX)

	if !s.IsAllowed(childSid, agentX, toolT, nil) {
		t.Errorf("dataset row 3: same agent, different session — %q must resolve under "+
			"the helper's session %s", toolT, childSid)
	}
	if !s.IsAllowed(parentSid, agentX, toolT, nil) {
		t.Errorf("dataset row 3: the source session %s must be untouched", parentSid)
	}
	if s.IsAllowed(childSid, otherAgent, toolT, nil) || s.IsAllowed(parentSid, otherAgent, toolT, nil) {
		t.Errorf("no widening: agent %s must not hold %s's allowance in either chat", otherAgent, agentX)
	}
}

// TestApprovalGrants_InheritFrom_InvalidKeysAreInert replaces
// TestApprovalGrants_InheritFrom_SourceMissIsNotSilent (TDD plan row #84,
// BDD-89, dataset rows 2 and 6).
//
// RETIRED, with the spec row: the source-miss counter, the invalid-key counter
// and the log lines "no grants to inherit" / level=WARN. Row InheritFrom says
// it "copies no grants", so an empty source is not a miss: there is nothing to
// copy and nothing to report, and the spec names no counter or log for it.
//
// KEPT, against the new semantics: an empty key component is a caller defect
// that must stay fail-closed. An invalid InheritFrom hands nobody an
// allowance, the allowance that exists is untouched, and a nil receiver never
// panics and never reports a grant. This test passes on today's product and
// must keep passing: it is the invalid-input safety the brief asks to
// preserve, not a RED row.
func TestApprovalGrants_InheritFrom_InvalidKeysAreInert(t *testing.T) {
	const (
		parentSid   = "sess_empty_parent_aa11"
		childSid    = "sess_empty_child_bb22"
		parentAgent = "jim"
		childAgent  = "ava"
		toolT       = "bash"
	)
	requireDistinct(t, parentSid, childSid)

	// Dataset row 2: well-formed keys, a source that holds nothing.
	empty := NewApprovalGrantStore()
	empty.InheritFrom(parentSid, parentAgent, childSid, childAgent)
	for _, key := range [][2]string{{parentSid, parentAgent}, {childSid, childAgent}, {childSid, parentAgent}, {parentSid, childAgent}} {
		if empty.IsAllowed(key[0], key[1], toolT, nil) {
			t.Errorf("an empty source must not create a grant; {%s, %s} resolves %q", key[0], key[1], toolT)
		}
	}

	// A real allowance, so "inert" is observable in both directions.
	s := NewApprovalGrantStore()
	if !s.Record(parentSid, parentAgent, toolT, nil) {
		t.Fatal("fixture: Record on the source key must succeed")
	}
	if !s.IsAllowed(parentSid, parentAgent, toolT, nil) {
		t.Fatalf("instrument: %q recorded for {%s, %s} must resolve for that key", toolT, parentSid, parentAgent)
	}

	// Dataset row 6: one empty component per call.
	s.InheritFrom("", parentAgent, childSid, childAgent)
	s.InheritFrom(parentSid, "", childSid, childAgent)
	s.InheritFrom(parentSid, parentAgent, "", childAgent)
	s.InheritFrom(parentSid, parentAgent, childSid, "")

	if s.IsAllowed(childSid, childAgent, toolT, nil) || s.IsAllowed(parentSid, childAgent, toolT, nil) {
		t.Errorf("an invalid-key InheritFrom must not give the delegate %q", toolT)
	}
	if s.IsAllowed(childSid, "", toolT, nil) {
		t.Errorf("an invalid-key InheritFrom must not create an allowance for the empty agent id")
	}
	if !s.IsAllowed(parentSid, parentAgent, toolT, nil) {
		t.Errorf("an invalid-key InheritFrom must not remove the existing allowance for {%s, %s}",
			parentSid, parentAgent)
	}

	// Nil-receiver safety: no panic, and a nil store never reports a grant.
	var nilStore *ApprovalGrantStore
	nilStore.InheritFrom(parentSid, parentAgent, childSid, childAgent)
	if nilStore.IsAllowed(childSid, childAgent, toolT, nil) || nilStore.IsAllowed(parentSid, parentAgent, toolT, nil) {
		t.Error("a nil store must never report a grant")
	}
}

// TestApprovalGrants_ConcurrentAgentKeyedSafe replaces
// TestApprovalGrants_InheritFrom_ConcurrentSafe. It exercises the agent-keyed
// store under concurrency so the -race detector covers its lock discipline,
// and checks that parallel recording, lookups, delegation calls and chat
// closes never move an allowance between agents.
//
// RETIRED with the counters: the old "source-miss count == workers*200"
// assertion (spec row InheritFrom: no copy, so no miss to count).
//
// Oracle, from C-26: each worker's agent holds ITS OWN tool in every chat the
// worker touches (Key: the agent, not the session), no other agent ever holds
// it (Covers), and a chat close ends nothing (row ClearSession).
func TestApprovalGrants_ConcurrentAgentKeyedSafe(t *testing.T) {
	const (
		workers = 8
		rounds  = 200
	)
	agentOf := func(w int) string { return fmt.Sprintf("agent_conc_%d", w) }
	toolOf := func(w int) string { return fmt.Sprintf("tool_conc_%d", w) }
	chatOf := func(w int) string { return fmt.Sprintf("sess_conc_%d", w) }
	helperChatOf := func(w int) string { return fmt.Sprintf("sess_conc_helper_%d", w) }

	s := NewApprovalGrantStore()

	var (
		mu       sync.Mutex
		failures []string
	)
	fail := func(w, round int, format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		failures = append(failures, fmt.Sprintf("worker %d round %d: ", w, round)+fmt.Sprintf(format, args...))
	}

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			agent, tool, chat, helper := agentOf(w), toolOf(w), chatOf(w), helperChatOf(w)
			neighbour := agentOf((w + 1) % workers)
			if !s.Record(chat, agent, tool, nil) {
				fail(w, -1, "Record must succeed for {%s, %s}", chat, agent)
				return
			}
			for j := 0; j < rounds; j++ {
				// A delegation to the neighbouring agent and a close of the
				// helper's chat, interleaved with every other worker's calls.
				s.InheritFrom(chat, agent, helper, neighbour)
				if s.IsAllowed(helper, neighbour, tool, nil) {
					fail(w, j, "the delegate %s must never hold %s's tool %q", neighbour, agent, tool)
				}
				if !s.IsAllowed(helper, agent, tool, nil) {
					fail(w, j, "agent %s must hold its own tool %q in its helper's chat %s", agent, tool, helper)
				}
				s.ClearSession(helper)
			}
		}(w)
	}
	wg.Wait()

	const maxReported = 5
	for i, f := range failures {
		if i == maxReported {
			t.Errorf("... and %d more failures", len(failures)-maxReported)
			break
		}
		t.Error(f)
	}

	for w := 0; w < workers; w++ {
		agent, tool := agentOf(w), toolOf(w)
		for _, chat := range []string{chatOf(w), helperChatOf(w)} {
			if !s.IsAllowed(chat, agent, tool, nil) {
				t.Errorf("after the concurrent run, agent %s must still hold %q in chat %s "+
					"(the allowance belongs to the agent and no chat close ends it)", agent, tool, chat)
			}
		}
		for v := 0; v < workers; v++ {
			if v == w {
				continue
			}
			for _, chat := range []string{chatOf(w), helperChatOf(w), chatOf(v)} {
				if s.IsAllowed(chat, agentOf(v), tool, nil) {
					t.Errorf("cross-agent contamination: agent %s holds %s's tool %q in chat %s",
						agentOf(v), agent, tool, chat)
				}
			}
		}
	}
}

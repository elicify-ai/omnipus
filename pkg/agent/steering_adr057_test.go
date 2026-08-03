package agent

// steering_adr057_test.go — tests for ADR-057 U8 (pkg/agent/steering.go):
// W4 (role-B predicate rebase onto routingSessionID) and W13/W13b (the
// InterruptScope collapse). Per the ADR-057 session-unification spec's
// binding rule 5, every unit's new tests go in a NEW file named
// `<subject>_adr057_test.go`; this file is U8's.
//
// Binding rule 1 (no spies): every assertion below drives REAL turnStates
// registered in a REAL AgentLoop's activeTurnStates via al.Interrupt /
// al.InterruptSessionHard — never a fake that records its argument and
// reports success. This project has been burned by exactly that shape
// before (message_parent_real_context_test.go's derived-session-id note,
// and #576/#577 themselves): a mock that never actually looks anything up
// cannot regress the way #577 did.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// --------------------------------------------------------------------------
// Test fixture: a real 4-node delegation tree, registered in
// al.activeTurnStates exactly as pkg/agent/subturn.go's spawnSubTurn leaves
// it — chat A with children B and C, and B with its own child D.
// --------------------------------------------------------------------------

// buildU8DelegationTree constructs chat A with children B and C, and B with
// child D, as REAL turnStates registered in al.activeTurnStates. Per binding
// rule "distinct ids everywhere" (Corollary, spec p.165), every node gets
// its own distinct sessionKey — A's sessionKey doubles as "the chat's own
// session id" (root turns are registered under their own id in production
// too, via a composite sessionKey; using the plain id here is what lets the
// Range-fallback half of resolveInterruptAnchors exercise the SAME id shape
// a real caller — cancel.go's GetActiveTurnHookForSession/
// resolveSessionIDByChannelChat — hands to Interrupt).
//
// All four are constructed with the SAME TranscriptSessionID (A's own id),
// which is deliberate, not a simplification: it is exactly what production
// looks like TODAY, pre-D1 (pkg/agent/subturn.go:1034 inherits
// parentTS.transcriptSessionID verbatim at every depth) — and it is exactly
// why newTurnState defaults routingSessionID to a turn's own
// transcriptSessionID (turn.go): pre-D1 that default reproduces "inherited
// verbatim through the whole subtree" for free, with no extra wiring, which
// is what lets W4's rebase land in this wave (U3, U8) before U7 (Wave F)
// implements the explicit childTS.routingSessionID = parentTS.routingSessionID
// overwrite for the post-D1 world.
//
// parentTurnID/depth are set exactly as pkg/agent/subturn.go's spawnSubTurn
// sets them at spawn (childTS.parentTurnID = parentTS.turnID), giving
// collectLiveDescendantTurnStates a real chain to walk.
func buildU8DelegationTree(t *testing.T, al *AgentLoop) (a, b, c, d *turnState) {
	t.Helper()
	agentInst := al.registry.GetDefaultAgent()
	if agentInst == nil {
		t.Fatal("buildU8DelegationTree: expected default agent")
	}

	const chatID = "u8-chat-A-session"

	mk := func(sessionKey string) *turnState {
		opts := processOptions{
			SessionKey:          sessionKey,
			Channel:             "test",
			ChatID:              "u8-chat1",
			TranscriptSessionID: chatID,
		}
		scope := al.newTurnEventScope(agentInst.ID, sessionKey)
		ts := newTurnState(agentInst, opts, scope)
		al.activeTurnStates.Store(ts.sessionKey, ts)
		t.Cleanup(func() { al.activeTurnStates.Delete(ts.sessionKey) })
		return ts
	}

	a = mk(chatID)              // root: registered under the chat's own plain session id
	b = mk("u8-delegate-B-key") // distinct, unique per-delegation address (mirrors D1's sessionKey==own id)
	c = mk("u8-delegate-C-key")
	d = mk("u8-delegate-D-key")

	b.depth, b.parentTurnID = 1, a.turnID
	c.depth, c.parentTurnID = 1, a.turnID
	d.depth, d.parentTurnID = 2, b.turnID

	return a, b, c, d
}

// --------------------------------------------------------------------------
// #27 TestInterruptScope_RequiredByCompiler — BDD-45, AC-8.
// --------------------------------------------------------------------------

// TestInterruptScope_RequiredByCompiler is test #27 from the ADR-057 spec
// ("Negative-gate positive lower bounds" table, "As #3"), tracing to BDD-45
// (US-9 Acceptance Scenario 1).
//
// This is a negative/compile-fail gate, so binding rule 4 requires it to
// prove its search is live before trusting the negative result: the fixture
// file must exist and be located by path — asserted first, as a hard
// failure (never a silent skip) if the fixture is missing.
//
// KNOWN TRANSITIONAL CAVEAT (stated, not silently worked around): the
// fixture imports pkg/agent, so `go build` on it requires the WHOLE of
// pkg/agent to compile — including pkg/agent/cancel.go (owned by U15, Wave
// F) and pkg/agent/session_messaging_wire.go (owned by U19, Wave G), both of
// which still call the pre-collapse two-argument InterruptSession/
// InterruptSessionHard/InterruptBySessionKey/InterruptBySessionKeyHard until
// those units land (FR-041's own ownership table names this exact
// dependency: "U8 (W13) before U14, U15 and U19"). Until then, `go build`
// on this fixture fails at pkg/agent's own (unrelated) compile errors before
// ever reaching this fixture's missing-argument error, so the two message
// assertions below may not find "Interrupt"/"InterruptScope" in the output
// on an intermediate, not-yet-fully-landed tree — this is the same
// documented cross-unit gap as `go build ./pkg/agent/...` itself failing on
// cancel.go/session_messaging_wire.go right now, not a defect in this test
// or the fixture. It requires no further action from this unit: once
// U15/U19 land, this test starts passing with no changes here.
func TestInterruptScope_RequiredByCompiler(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("TestInterruptScope_RequiredByCompiler: getwd: %v", err)
	}
	fixtureDir := filepath.Join(cwd, "testdata", "interrupt_scope_required")
	fixtureFile := filepath.Join(fixtureDir, "main.go")
	if info, statErr := os.Stat(fixtureFile); statErr != nil {
		t.Fatalf("TestInterruptScope_RequiredByCompiler: compile-fail fixture not located at %s: %v (binding rule 4: a missing fixture is a test failure, never a pass)", fixtureFile, statErr)
	} else if info.IsDir() {
		t.Fatalf("TestInterruptScope_RequiredByCompiler: expected a file at %s, found a directory", fixtureFile)
	}

	outPath := filepath.Join(t.TempDir(), "interrupt_scope_required_bin")
	cmd := exec.Command("go", "build", "-o", outPath, fixtureDir)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	out, buildErr := cmd.CombinedOutput()

	if buildErr == nil {
		t.Fatalf("TestInterruptScope_RequiredByCompiler: `go build` on the fixture unexpectedly SUCCEEDED (output: %s) — Interrupt accepted a call missing its InterruptScope argument, which FR-041 forbids", out)
	}
	if _, statErr := os.Stat(outPath); statErr == nil {
		t.Fatalf("TestInterruptScope_RequiredByCompiler: build reported failure but produced a binary at %s", outPath)
	}

	output := string(out)
	if !strings.Contains(output, "Interrupt") {
		t.Logf("TestInterruptScope_RequiredByCompiler: build error does not name Interrupt (see the KNOWN TRANSITIONAL CAVEAT doc comment — pkg/agent may not yet compile as a whole on this tree); got:\n%s", output)
		t.Fail()
	}
}

// --------------------------------------------------------------------------
// #53 TestInterrupt_SubtreeAtChildSparesParentAndSibling — BDD-46, AC-8.
// --------------------------------------------------------------------------

// TestInterrupt_SubtreeAtChildSparesParentAndSibling is test #53 from the
// ADR-057 spec, tracing to BDD-46 (US-9 Acceptance Scenario 2) and AC-8's
// first clause: "Interrupt(childB, ScopeSubtree) cancels B and B's own
// children, and leaves parent A and sibling C running."
//
// This is the NEW-invariant assertion the collapse must make possible: the
// pre-collapse tree had no way to express "cancel exactly this delegate's
// own subtree" — InterruptSession/InterruptSessionHard's transcriptSessionID
// Range always reached the WHOLE chat, and InterruptBySessionKey/
// InterruptBySessionKeyHard's point lookup reached exactly ONE turn, never a
// subtree. U21 separately inverts pkg/agent/interrupt_by_session_key_test.go
// (not this unit's file) to retire the old two-namespace assertion; this
// test is the new one, in this unit's own new file per Rule 5.
func TestInterrupt_SubtreeAtChildSparesParentAndSibling(t *testing.T) {
	al, cleanup := newAL(t)
	defer cleanup()

	a, b, c, d := buildU8DelegationTree(t, al)

	descendants, err := al.Interrupt(b.sessionKey, ScopeSubtree, "cancel B's subtree")
	if err != nil {
		t.Fatalf("Interrupt(B, ScopeSubtree) returned unexpected error: %v", err)
	}

	got := map[string]bool{}
	for _, id := range descendants {
		got[id] = true
	}
	if !got[b.turnID] {
		t.Errorf("Interrupt(B, ScopeSubtree): descendants %v must include B (%s)", descendants, b.turnID)
	}
	if !got[d.turnID] {
		t.Errorf("Interrupt(B, ScopeSubtree): descendants %v must include D, B's own child (%s)", descendants, d.turnID)
	}
	if got[a.turnID] {
		t.Errorf("Interrupt(B, ScopeSubtree): descendants %v must NOT include parent A (%s) — FR-042", descendants, a.turnID)
	}
	if got[c.turnID] {
		t.Errorf("Interrupt(B, ScopeSubtree): descendants %v must NOT include sibling C (%s) — FR-042", descendants, c.turnID)
	}
	if len(descendants) != 2 {
		t.Errorf("Interrupt(B, ScopeSubtree): expected exactly 2 descendants (B, D), got %d: %v", len(descendants), descendants)
	}

	// Cross-check against the REAL, observable turnState flags — not just
	// the returned slice (binding rule 2: land on the artefact, not the
	// invocation record).
	if interrupted, _ := b.gracefulInterruptRequested(); !interrupted {
		t.Error("B's turnState must have gracefulInterrupt set after Interrupt(B, ScopeSubtree)")
	}
	if interrupted, _ := d.gracefulInterruptRequested(); !interrupted {
		t.Error("D's turnState must have gracefulInterrupt set after Interrupt(B, ScopeSubtree) — B's own child")
	}
	if interrupted, _ := a.gracefulInterruptRequested(); interrupted {
		t.Error("A's turnState must NOT have gracefulInterrupt set — Interrupt(B, ScopeSubtree) must spare the parent")
	}
	if interrupted, _ := c.gracefulInterruptRequested(); interrupted {
		t.Error("C's turnState must NOT have gracefulInterrupt set — Interrupt(B, ScopeSubtree) must spare the sibling")
	}
}

// --------------------------------------------------------------------------
// #54 TestInterrupt_SubtreeAtChatReachesAllDepths — BDD-47, AC-8.
// --------------------------------------------------------------------------

// TestInterrupt_SubtreeAtChatReachesAllDepths is test #54 from the ADR-057
// spec, tracing to BDD-47 (US-9 Acceptance Scenario 3) and AC-8's second
// clause: "Interrupt(chat, ScopeSubtree) reaches all three depths."
//
// This is the whole-chat cascade — byte-identical in outcome to the retired
// InterruptSession, now reached via resolveInterruptAnchors's Range fallback
// on routingSessionID (W4's rebase) rather than a direct transcriptSessionID
// Range, because every node in the tree shares A's own id as its
// routingSessionID (pre-D1, by construction — see buildU8DelegationTree's
// doc comment).
func TestInterrupt_SubtreeAtChatReachesAllDepths(t *testing.T) {
	al, cleanup := newAL(t)
	defer cleanup()

	a, b, c, d := buildU8DelegationTree(t, al)

	descendants, err := al.Interrupt(a.sessionKey, ScopeSubtree, "stop the whole chat")
	if err != nil {
		t.Fatalf("Interrupt(chat, ScopeSubtree) returned unexpected error: %v", err)
	}

	got := map[string]bool{}
	for _, id := range descendants {
		got[id] = true
	}
	for _, node := range []*turnState{a, b, c, d} {
		if !got[node.turnID] {
			t.Errorf("Interrupt(chat, ScopeSubtree): descendants %v must include %s (turn %s)", descendants, node.sessionKey, node.turnID)
		}
	}
	if len(descendants) != 4 {
		t.Errorf("Interrupt(chat, ScopeSubtree): expected exactly 4 descendants (A,B,C,D), got %d: %v", len(descendants), descendants)
	}

	for _, node := range []*turnState{a, b, c, d} {
		if interrupted, _ := node.gracefulInterruptRequested(); !interrupted {
			t.Errorf("%s's turnState must have gracefulInterrupt set after Interrupt(chat, ScopeSubtree)", node.sessionKey)
		}
	}
}

// --------------------------------------------------------------------------
// The #577 property, preserved: ScopeSelfOnly still cancels exactly the one
// addressed delegate, by its own session key, never the parent or a sibling.
// --------------------------------------------------------------------------

// TestInterrupt_SelfOnlyPreservesIssue577Property drives the collapsed
// Interrupt through EXACTLY the shape #577's fix (the retired
// InterruptBySessionKey) existed to guarantee: a delegate cancel addressed
// by the delegate's OWN session key reaches that ONE turn and nothing else
// — not the parent, not a sibling, not even its own child (ScopeSelfOnly,
// unlike ScopeSubtree, does not cascade).
//
// D8's reconciliation note (ADR-057 §"Reconciling with #577, explicitly")
// says the fix's INTENT ("cancel one delegation without touching the parent
// or siblings") survives the collapse as ScopeSelfOnly's contract, even
// though the retired InterruptBySessionKey name and its own dedicated test
// file (interrupt_by_session_key_test.go, U21's, inverted separately) go
// away. This test is the U8-side half of that reconciliation: it proves the
// PRIMITIVE (Interrupt/resolveInterruptAnchors) still has the property, using
// a fresh fixture in this unit's own new file (Rule 5) rather than touching
// U21's file.
func TestInterrupt_SelfOnlyPreservesIssue577Property(t *testing.T) {
	al, cleanup := newAL(t)
	defer cleanup()

	a, b, c, d := buildU8DelegationTree(t, al)

	descendants, err := al.Interrupt(b.sessionKey, ScopeSelfOnly, "cancel exactly this delegate")
	if err != nil {
		t.Fatalf("Interrupt(B, ScopeSelfOnly) returned unexpected error: %v", err)
	}
	if len(descendants) != 1 || descendants[0] != b.turnID {
		t.Fatalf("Interrupt(B, ScopeSelfOnly): descendants = %v, want exactly [%s]", descendants, b.turnID)
	}

	if interrupted, _ := b.gracefulInterruptRequested(); !interrupted {
		t.Error("B's turnState must have gracefulInterrupt set after Interrupt(B, ScopeSelfOnly)")
	}
	for name, node := range map[string]*turnState{"A (parent)": a, "C (sibling)": c, "D (B's own child)": d} {
		if interrupted, _ := node.gracefulInterruptRequested(); interrupted {
			t.Errorf("%s must NOT have gracefulInterrupt set — Interrupt(B, ScopeSelfOnly) must not cascade at all (this is the #577 property: %s)", name, "cancel one delegation without touching anything else")
		}
	}

	// Hard-escalation counterpart, same property (mirrors the old
	// InterruptBySessionKeyHard called at t=3s for `delegate cancel(hard=true)`).
	hardDescendants, err := al.InterruptSessionHard(c.sessionKey, ScopeSelfOnly, "hard-cancel exactly this delegate")
	if err != nil {
		t.Fatalf("InterruptSessionHard(C, ScopeSelfOnly) returned unexpected error: %v", err)
	}
	if len(hardDescendants) != 1 || hardDescendants[0] != c.turnID {
		t.Fatalf("InterruptSessionHard(C, ScopeSelfOnly): descendants = %v, want exactly [%s]", hardDescendants, c.turnID)
	}
	if !c.hardAbortRequested() {
		t.Error("C's turnState must have hardAbort set after InterruptSessionHard(C, ScopeSelfOnly)")
	}
	for name, node := range map[string]*turnState{"A (parent)": a, "B (sibling)": b, "D (C's nephew)": d} {
		if node.hardAbortRequested() {
			t.Errorf("%s must NOT have hardAbort set — InterruptSessionHard(C, ScopeSelfOnly) must not cascade", name)
		}
	}
}

// --------------------------------------------------------------------------
// Red/green: a naive collapse (dropping the point-lookup half of
// resolveInterruptAnchors and matching every id against routingSessionID
// alone) reproduces the #577 regression this collapse must not reintroduce.
// --------------------------------------------------------------------------

// naiveResolveBySharedRoutingIDOnly is a deliberately WRONG anchor resolver,
// kept here only to demonstrate — against the SAME real fixture, not a
// spy or a hypothetical — why resolveInterruptAnchors tries the point Load
// FIRST rather than matching every id against routingSessionID alone. It is
// exactly the simplification a less careful collapse could make: "one scope
// enum, one matching mechanism" sounds right until a delegate's own address
// is checked against a field (routingSessionID) that every node in the tree
// shares with its root, not with itself.
func naiveResolveBySharedRoutingIDOnly(al *AgentLoop, id string) []*turnState {
	var anchors []*turnState
	al.activeTurnStates.Range(func(_, value any) bool {
		ts := value.(*turnState)
		if string(ts.routingSessionID) == id {
			anchors = append(anchors, ts)
		}
		return true
	})
	return anchors
}

// TestNaiveCollapse_RoutingIDOnlyRegressesIssue577 is the negative control
// the task's red/green requirement asks for: it shows that a naive collapse
// — one that matches every id against the shared routingSessionID field
// alone, without first trying the point lookup resolveInterruptAnchors
// tries — reintroduces #577's exact symptom (zero matches, silently
// reported as "nothing to do") when a delegate is addressed by its own
// session key. B's routingSessionID equals A's own id (inherited verbatim,
// pre-D1 — see buildU8DelegationTree), never B's own sessionKey, so a
// routingSessionID-only match against B's address finds NOTHING.
//
// binding rule 4: this is itself a zero-count gate, so its positive lower
// bound is asserted first — the SAME naive resolver, given the CHAT's own
// id (the shape it does handle correctly), MUST find all 4 real registered
// nodes; a naive resolver that cannot even do that would make the
// zero-for-B result meaningless (a broken resolver finding nothing is not
// evidence of the #577 regression, just evidence of a broken test).
func TestNaiveCollapse_RoutingIDOnlyRegressesIssue577(t *testing.T) {
	al, cleanup := newAL(t)
	defer cleanup()

	a, b, c, d := buildU8DelegationTree(t, al)

	// Positive lower bound: the naive resolver DOES correctly find the
	// whole chat when addressed by the chat's own id (proving the resolver
	// itself works and the fixture is live, before trusting its zero result
	// below).
	whole := naiveResolveBySharedRoutingIDOnly(al, a.sessionKey)
	if len(whole) != 4 {
		t.Fatalf("naive resolver sanity check: Range-by-routingSessionID(chat) found %d nodes, want 4 (A,B,C,D) — binding rule 4: a broken positive control makes the negative result below meaningless", len(whole))
	}

	// The regression: addressed by B's OWN session key (exactly the id
	// `delegate action=cancel` hands to a cancel hook, and exactly the id
	// #577's fix — InterruptBySessionKey's direct point Load — was written
	// to resolve), the naive routingSessionID-only resolver finds ZERO
	// matches, because B's routingSessionID is A's id, not B's own.
	naive := naiveResolveBySharedRoutingIDOnly(al, b.sessionKey)
	if len(naive) != 0 {
		t.Fatalf("naive resolver unexpectedly found %d matches for B's own session key (%v) — this test's premise (routingSessionID-only matching cannot address a delegate by its own id) no longer holds; re-derive the demonstration", len(naive), naive)
	}

	// The real, shipped resolver does NOT have this gap: it tries the point
	// Load first (which hits — B's own sessionKey is a real activeTurnStates
	// key) before ever consulting routingSessionID.
	real := al.resolveInterruptAnchors(b.sessionKey)
	if len(real) != 1 || real[0].turnID != b.turnID {
		t.Fatalf("resolveInterruptAnchors(B) = %v, want exactly [B] (%s) — the shipped resolver must not reproduce the naive gap just demonstrated", real, b.turnID)
	}

	_, _ = c, d // C and D are part of the shared fixture tree; not addressed directly by this test
}

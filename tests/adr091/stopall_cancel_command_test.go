package adr091_test

// ADR-20260928 D7/D9 + MAJ-002 + MAJ-009 + T21 — RED tests for the /cancel
// command's Stop-all (tree) scope, driven end to end through the REAL command
// executor and the REAL agent loop, asserting on REAL lifecycle records and
// REAL goal records.
//
// D9 (surface → scope): /cancel is a Stop-all surface — scope "tree", itself
// the confirmation. D7: a tree stop cascades DOWN only, lands every reached
// session `stopped` (cause "cascade" on descendants; the named session is the
// cascade's own direct target), on the SAME generation, never terminal
// (done/failed are final — stopped is not), never ends any goal (own,
// descendants', or ancestors'), and is idempotent on already-stopped
// descendants (D5/D8.2 — their existing note is NOT rewritten).
// ADR-20261004, locked decision 6: "The 24-hour person-question expiry is
// removed with the pause. Do not design a replacement expiry."
// Correction C2: "A helper question to its parent is an ordinary message and
// does not park the helper." The cascade still reaches that ordinary helper.
// MAJ-002: omitted/session scope must
// NEVER cascade — the contrast test pins the existing single-session stop.
//
// Case table (oracle: the ADR sections above + common.md decided behaviour —
// no expected value was derived from running the implementation):
//
//	1  /cancel@root    → Root,A,B,C,S all `stopped`, same generations,
//	                     descendants cause "cascade", non-terminal; ordinary
//	                     working helper W reached → stopped, no question park;
//	                     pre-stopped child keeps its
//	                     original "stop" note; goals of Root/A/B/C stay active.
//	2  /cancel@B       → B,C stopped (cascade, same generations); parent A,
//	                     sibling S and root UNAFFECTED (running); goals of
//	                     root, A and B stay active (ancestors' and own).
//	3  repeat /cancel@root → idempotent: states, generations, causes unchanged;
//	                     goals still active.
//	4  single-session stop on B (the pre-D9 path /cancel used) → B stopped
//	     ONLY; C, A, S, root all untouched (MAJ-002: no cascade without tree
//	     scope). Contrast pin — guards the scope boundary.
//
// RED expectation (2026-10-02): /cancel still routes to the single-turn
// RequestCancelForSession path, so tests 1-3 fail on descendants that must
// have landed stopped but remain running. Test 4 pins today's correct
// session-scope behaviour and is expected to pass before and after GREEN.

import (
	"context"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/commands"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/task"
)

const stopAllWaitBudget = 10 * time.Second

// stopAllHarness is a blocking-provider harness: every launched session holds
// a LIVE turn parked in the provider until the release channel closes, so
// "working child" is a real running turn, not a completed one.
type stopAllHarness struct {
	*e2eHarness
	release chan struct{}
}

func newStopAllHarness(t *testing.T, configure ...func(*config.Config)) *stopAllHarness {
	t.Helper()
	h := &stopAllHarness{release: make(chan struct{})}
	t.Cleanup(func() { close(h.release) })
	h.e2eHarness = newE2EHarnessCustom(t, &e2eBlockingProvider{release: h.release}, testutil.RecordingOutbound(t), false, nil, configure...)
	return h
}

// driveCancelCommand types "/cancel" at targetSessionID through the real
// command registry + executor exactly as a chat channel delivers it, with the
// real agent loop wired (loop_slash.go's buildCommandsRuntime shape).
func driveCancelCommand(t *testing.T, h *stopAllHarness, targetSessionID string) string {
	t.Helper()
	rt := &commands.Runtime{SessionID: func() string { return targetSessionID }}
	rt = rt.WithAgentLoop(h.al)
	exec := commands.NewExecutor(commands.NewRegistry(commands.BuiltinDefinitions()), rt)
	var reply string
	res := exec.Execute(context.Background(), commands.Request{
		Channel:  "webchat",
		ChatID:   targetSessionID,
		SenderID: "adr091-fixture-owner",
		Text:     "/cancel",
		Reply: func(text string) error {
			reply = text
			return nil
		},
	})
	if res.Outcome != commands.OutcomeHandled {
		t.Fatalf("/cancel was not handled (outcome %v) — the command must be registered and web-surface-allowed", res.Outcome)
	}
	return reply
}

// loadRecord loads a lifecycle record, failing the test with the session's
// fixture name on error — a missing record is a fixture/behaviour bug, never
// something to skip.
func loadRecord(t *testing.T, h *stopAllHarness, node testutil.TreeNode) *session.LifecycleRecord {
	t.Helper()
	rec, err := h.lifecycle.Load(node.SessionID)
	if err != nil {
		t.Fatalf("load lifecycle record for %s (%s): %v", node.Name, node.SessionID, err)
	}
	return rec
}

// waitForLandedStopped polls until the record's state has LANDED
// LifecycleStopped (not merely the in-flight Stop fence — Stopped() is true
// while a stop is still unwinding, and the D7 oracle is the landed state).
func waitForLandedStopped(t *testing.T, h *stopAllHarness, node testutil.TreeNode) *session.LifecycleRecord {
	t.Helper()
	deadline := time.Now().Add(stopAllWaitBudget)
	for {
		rec := loadRecord(t, h, node)
		if rec.State == session.LifecycleStopped {
			return rec
		}
		if time.Now().After(deadline) {
			return rec
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// assertLandedStopped asserts one reached session's durable outcome: state
// stopped, SAME generation (D7 — a cascade never mints a generation),
// non-terminal (stopped is not final), and the expected note cause.
func assertLandedStopped(t *testing.T, h *stopAllHarness, node testutil.TreeNode, before *session.LifecycleRecord, wantCause session.StopCause) *session.LifecycleRecord {
	t.Helper()
	rec := waitForLandedStopped(t, h, node)
	if rec.State != session.LifecycleStopped {
		t.Fatalf("%s must land %q after /cancel tree stop, got %q (D7: cascade lands every reached session stopped)", node.Name, session.LifecycleStopped, rec.State)
	}
	if rec.Generation != before.Generation {
		t.Errorf("%s generation = %d, want %d unchanged (D7: same generation — a stop never mints one)", node.Name, rec.Generation, before.Generation)
	}
	if rec.Terminal() {
		t.Errorf("%s is terminal after the stop — stopped must never be final (D7: never done/failed)", node.Name)
	}
	if rec.StopNote == nil {
		t.Fatalf("%s landed stopped without a stop_note (D2/CRIT-001: a landed stopped record must carry the lasting note)", node.Name)
	}
	if rec.StopNote.Cause != wantCause {
		t.Errorf("%s stop_note.cause = %q, want %q", node.Name, rec.StopNote.Cause, wantCause)
	}
	return rec
}

// assertStillRunning asserts a session the tree stop must NOT have touched.
func assertStillRunning(t *testing.T, h *stopAllHarness, node testutil.TreeNode) {
	t.Helper()
	rec := loadRecord(t, h, node)
	if rec.State != session.LifecycleRunning {
		t.Fatalf("%s must be untouched by the tree stop (state %q, want %q) — the cascade never walks UP or SIDEWAYS (D7)", node.Name, rec.State, session.LifecycleRunning)
	}
}

// launchWorkingChild launches one REAL extra child under parent through the
// production launcher (the same path the named nodes use), holding a live
// blocking turn. Uses an agent the harness config defines.
func launchWorkingChild(t *testing.T, h *stopAllHarness, parent testutil.TreeNode, name string) testutil.TreeNode {
	t.Helper()
	result, err := h.launcher.Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: parent.SessionID,
		TargetAgentID:     "adr091-fixture-task-agent",
		Label:             "ADR-091 fixture " + name,
		Task:              "Build fixture node " + name,
		Origin:            steer.Origin{Kind: steer.OriginKindDelegate, CallID: "adr091-call-" + name},
		ToolExclusions:    []string{"switch_agent"},
	})
	if err != nil {
		t.Fatalf("launch working child %s under %s: %v", name, parent.Name, err)
	}
	if _, err := h.launcher.Dispatch(context.Background(), result.SessionID, result.Generation); err != nil {
		t.Fatalf("dispatch working child %s: %v", name, err)
	}
	return testutil.TreeNode{
		Name: name, SessionID: result.SessionID, AgentID: "adr091-fixture-task-agent",
		WorkspaceID: h.tree.Root.WorkspaceID, Generation: result.Generation,
	}
}

// persistRecordOnlyChild writes a child lifecycle record directly (the same
// shape testutil's createChildDirect mints), then applies mutate — used for
// children that need no live turn: a needs_input waiter and an
// already-stopped descendant.
func persistRecordOnlyChild(t *testing.T, h *stopAllHarness, parent testutil.TreeNode, name, agentID string, mutate func(*session.LifecycleRecord)) testutil.TreeNode {
	t.Helper()
	label := "ADR-091 fixture " + name
	meta, err := h.sessions.NewSession(session.SessionTypeDelegate, "webchat", agentID)
	if err != nil {
		t.Fatalf("create %s session: %v", name, err)
	}
	owner := "adr091-fixture-owner"
	if err := h.sessions.SetMeta(meta.ID, session.MetaPatch{
		Title: &label, Owner: &owner, WorkspaceID: &h.tree.Root.WorkspaceID, ParentSessionID: &parent.SessionID,
	}); err != nil {
		t.Fatalf("stamp %s metadata: %v", name, err)
	}
	rec := &session.LifecycleRecord{
		SessionID: meta.ID, Generation: 1, State: session.LifecycleRunning,
		Origin: &session.Origin{Kind: session.OriginKindDelegate, CallID: "adr091-call-" + name},
		SteeredBy: &session.SteeredBy{
			SteeringSessionID: parent.SessionID,
			RootSessionID:     h.tree.Root.SessionID,
			ReportingTarget:   session.ReportingTarget{SessionID: parent.SessionID, Channel: "webchat", ChatID: parent.SessionID},
			Authorization:     session.Authorization{Mode: session.AuthorizationModeDirect, RemainingDepth: 0},
			ToolExclusions:    []string{"switch_agent"},
		},
		OwnerScopeKind: session.OwnerScopeParentSession,
		OwnerScopeID:   parent.SessionID,
		WorkspaceID:    h.tree.Root.WorkspaceID, AgentID: agentID, ParentAgentID: parent.AgentID,
	}
	if mutate != nil {
		mutate(rec)
	}
	if err := h.lifecycle.Persist(rec); err != nil {
		t.Fatalf("persist %s lifecycle: %v", name, err)
	}
	return testutil.TreeNode{
		Name: name, SessionID: meta.ID, AgentID: agentID,
		WorkspaceID: h.tree.Root.WorkspaceID, Generation: 1,
	}
}

// armActiveSessionGoal mints and activates one REAL session-owned goal via
// the real goal store (the same record shape chat-compiled goals carry), and
// returns its id.
func armActiveSessionGoal(t *testing.T, sessionID string) string {
	t.Helper()
	gs := goal.NewStore(config.OmnipusHomeDir())
	g, err := goal.New(
		generated.GoalOwnerKindSession, sessionID, generated.GoalSourceChatCompiled,
		"fixture goal: survive Stop all", "",
		nil,
		[]task.AcceptanceCriterion{{
			Kind: "prose", Judgment: "boolean",
			Text: "fixture: this goal must stay active across any Stop all", Provenance: "stated",
			Author: task.CriterionAuthor{Kind: task.AuthorKindUser, ID: "adr091-fixture-owner"},
		}},
		3, time.Now().UTC(),
	)
	if err != nil {
		t.Fatalf("arm goal for %s: %v", sessionID, err)
	}
	if err := gs.Create(g); err != nil {
		t.Fatalf("create goal for %s: %v", sessionID, err)
	}
	gid := g.GoalID
	if _, err := gs.Update(gid, func(cur *goal.Goal) error { return cur.Activate(sessionID, time.Now().UTC()) }); err != nil {
		t.Fatalf("activate goal %s: %v", gid, err)
	}
	return gid
}

// assertGoalStillActive asserts the D7 invariant: a Stop all never ends any
// goal — not the stopped session's own, not a descendant's, not an ancestor's.
func assertGoalStillActive(t *testing.T, gid, label string) {
	t.Helper()
	gs := goal.NewStore(config.OmnipusHomeDir())
	g, err := gs.Get(gid)
	if err != nil {
		t.Fatalf("load goal %s (%s) after Stop all: %v", gid, label, err)
	}
	if g.State != generated.GoalStateActive {
		t.Errorf("goal %s (%s) state = %q, want %q — D7: Stop all never ends any goal (own, descendant or ancestor)", gid, label, g.State, generated.GoalStateActive)
	}
}

// TestE2E_CancelCommand_TreeScope_CascadesDownFromRoot — case 1.
func TestE2E_CancelCommand_TreeScope_CascadesDownFromRoot(t *testing.T) {
	// W is a real fourth-level child under C. Set this fixture's depth to
	// four before the chain launches; every other test keeps its default.
	// This replaces the old record-only park without bypassing admission.
	h := newStopAllHarness(t, func(cfg *config.Config) {
		cfg.Performance.MaxDelegationDepth = 4
	})

	rootBefore := loadRecord(t, h, h.tree.Root)
	aBefore := loadRecord(t, h, h.tree.A)
	bBefore := loadRecord(t, h, h.tree.B)
	cBefore := loadRecord(t, h, h.tree.C)

	// S: a real, live working sibling of B (child of A) — inside the subtree,
	// so a root Stop all must reach it too.
	sNode := launchWorkingChild(t, h, h.tree.A, "S")
	sBefore := loadRecord(t, h, sNode)

	// W keeps the deepest cascade leg, but no longer fabricates a question
	// park. ADR-20261004 locked decision 6: "There is no special person-only
	// question, no special pause, and no special message type for it."
	// C2: "A helper question to its parent is an ordinary message and does
	// not park the helper." W is now a real admitted, live ordinary helper.
	wNode := launchWorkingChild(t, h, h.tree.C, "W")

	// P: an already-stopped descendant under C — the cascade must reach it
	// idempotently WITHOUT rewriting its original note (D5/D8.2).
	pNode := persistRecordOnlyChild(t, h, h.tree.C, "P", "adr091-fixture-task-agent", func(rec *session.LifecycleRecord) {
		rec.State = session.LifecycleStopped
		rec.StopNote = &session.StopNote{
			At:    time.Now().UTC().Add(-time.Minute),
			By:    session.StopActorHumanUser("adr091-fixture-owner"),
			Seq:   1,
			Cause: session.StopCauseStop,
		}
	})
	wBefore := loadRecord(t, h, wNode)
	pBefore := loadRecord(t, h, pNode)

	goalRoot := armActiveSessionGoal(t, h.tree.Root.SessionID)
	goalA := armActiveSessionGoal(t, h.tree.A.SessionID)
	goalB := armActiveSessionGoal(t, h.tree.B.SessionID)
	goalC := armActiveSessionGoal(t, h.tree.C.SessionID)

	reply := driveCancelCommand(t, h, h.tree.Root.SessionID)

	if reply == "Nothing to cancel" {
		t.Errorf("/cancel on a root with live descendants replied %q — not truthful (D9: /cancel is a Stop-all surface with work to stop)", reply)
	}

	// The named session (root) is the cascade's own direct target; everything
	// below it is a descendant and must carry cause "cascade".
	assertLandedStopped(t, h, h.tree.Root, rootBefore, session.StopCauseStop)
	assertLandedStopped(t, h, h.tree.A, aBefore, session.StopCauseCascade)
	assertLandedStopped(t, h, h.tree.B, bBefore, session.StopCauseCascade)
	assertLandedStopped(t, h, h.tree.C, cBefore, session.StopCauseCascade)
	assertLandedStopped(t, h, sNode, sBefore, session.StopCauseCascade)

	// Preserve W's reached/state/generation/cause/nonterminal cascade oracle.
	// Only the retired question/correlation/TTL expectations are superseded.
	// ADR-20261004 C2: "`needs_input` (`LifecycleNeedsInput`)" is "Deleted
	// outright — its only writer was the pause".
	wRec := assertLandedStopped(t, h, wNode, wBefore, session.StopCauseCascade)
	if wRec.NeedsInput != nil {
		t.Fatalf("ordinary helper %s retained a person-question park after Stop all: %+v — ADR-20261004 C2 deletes that park outright", wNode.Name, wRec.NeedsInput)
	}

	// The already-stopped descendant: still stopped, SAME generation, and its
	// ORIGINAL cause — the cascade must not rewrite history (D5/D8.2).
	assertLandedStopped(t, h, pNode, pBefore, session.StopCauseStop)

	// Goals: own and descendants' — none may end (D7).
	assertGoalStillActive(t, goalRoot, "root")
	assertGoalStillActive(t, goalA, "A")
	assertGoalStillActive(t, goalB, "B")
	assertGoalStillActive(t, goalC, "C")
}

// TestE2E_CancelCommand_TreeScope_FromHelper_NeverUpwardOrSideways — case 2.
func TestE2E_CancelCommand_TreeScope_FromHelper_NeverUpwardOrSideways(t *testing.T) {
	h := newStopAllHarness(t)

	bBefore := loadRecord(t, h, h.tree.B)
	cBefore := loadRecord(t, h, h.tree.C)

	sNode := launchWorkingChild(t, h, h.tree.A, "S")

	goalRoot := armActiveSessionGoal(t, h.tree.Root.SessionID)
	goalA := armActiveSessionGoal(t, h.tree.A.SessionID)
	goalB := armActiveSessionGoal(t, h.tree.B.SessionID)

	reply := driveCancelCommand(t, h, h.tree.B.SessionID)
	if reply == "Nothing to cancel" {
		t.Errorf("/cancel on a helper with live descendants replied %q — not truthful", reply)
	}

	assertLandedStopped(t, h, h.tree.B, bBefore, session.StopCauseStop)
	assertLandedStopped(t, h, h.tree.C, cBefore, session.StopCauseCascade)

	// Upward and sideways: parent A, sibling S, and the root stay running.
	assertStillRunning(t, h, h.tree.A)
	assertStillRunning(t, h, sNode)
	assertStillRunning(t, h, h.tree.Root)

	// Goals of ancestors AND of the reached session itself stay active (D7).
	assertGoalStillActive(t, goalRoot, "root")
	assertGoalStillActive(t, goalA, "A")
	assertGoalStillActive(t, goalB, "B")
}

// TestE2E_CancelCommand_TreeScope_RepeatIsIdempotent — case 3.
func TestE2E_CancelCommand_TreeScope_RepeatIsIdempotent(t *testing.T) {
	h := newStopAllHarness(t)

	rootBefore := loadRecord(t, h, h.tree.Root)
	aBefore := loadRecord(t, h, h.tree.A)
	bBefore := loadRecord(t, h, h.tree.B)
	cBefore := loadRecord(t, h, h.tree.C)

	goalRoot := armActiveSessionGoal(t, h.tree.Root.SessionID)

	driveCancelCommand(t, h, h.tree.Root.SessionID)
	assertLandedStopped(t, h, h.tree.Root, rootBefore, session.StopCauseStop)
	assertLandedStopped(t, h, h.tree.A, aBefore, session.StopCauseCascade)
	assertLandedStopped(t, h, h.tree.B, bBefore, session.StopCauseCascade)
	assertLandedStopped(t, h, h.tree.C, cBefore, session.StopCauseCascade)

	firstPass := map[string]*session.LifecycleRecord{
		h.tree.Root.SessionID: loadRecord(t, h, h.tree.Root),
		h.tree.A.SessionID:    loadRecord(t, h, h.tree.A),
		h.tree.B.SessionID:    loadRecord(t, h, h.tree.B),
		h.tree.C.SessionID:    loadRecord(t, h, h.tree.C),
	}

	// Second /cancel on the now fully-stopped subtree: pure no-op.
	driveCancelCommand(t, h, h.tree.Root.SessionID)

	for _, node := range h.tree.Nodes {
		after := loadRecord(t, h, node)
		first := firstPass[node.SessionID]
		if after.State != first.State {
			t.Errorf("%s state changed on the repeat /cancel: %q → %q (idempotency, D5)", node.Name, first.State, after.State)
		}
		if after.Generation != first.Generation {
			t.Errorf("%s generation changed on the repeat /cancel: %d → %d (D5: a repeat stop never mints a generation)", node.Name, first.Generation, after.Generation)
		}
		if (first.StopNote == nil) != (after.StopNote == nil) {
			t.Errorf("%s stop_note presence changed on the repeat /cancel (%v → %v)", node.Name, first.StopNote, after.StopNote)
		} else if first.StopNote != nil && after.StopNote.Cause != first.StopNote.Cause {
			t.Errorf("%s stop_note.cause rewritten on the repeat /cancel: %q → %q (D5/D8.2: an already-stopped record's note is not rewritten)", node.Name, first.StopNote.Cause, after.StopNote.Cause)
		}
	}

	assertGoalStillActive(t, goalRoot, "root")
}

// TestE2E_CancelCommand_SessionScope_SingleStopDoesNotCascade — case 4
// (contrast pin, MAJ-002: omitted/session scope must NEVER cascade). Drives
// the server's own single-session stop primitive —
// SteerCanceller.StopTurns(subtree=false), the exact function the gateway's
// scope-session path calls (pkg/gateway/websocket_stop_scope.go) and the path
// /cancel used before D9 — and asserts the subtree below survives.
func TestE2E_CancelCommand_SessionScope_SingleStopDoesNotCascade(t *testing.T) {
	h := newStopAllHarness(t)

	bBefore := loadRecord(t, h, h.tree.B)
	cBefore := loadRecord(t, h, h.tree.C)
	sNode := launchWorkingChild(t, h, h.tree.A, "S")

	sc := agent.NewSteerCanceller(h.lifecycle, h.al.SteerGenerationCancel)
	if _, err := sc.StopTurns(context.Background(), h.tree.B.SessionID,
		steer.Principal{Kind: steer.PrincipalKindHuman, ID: "adr091-fixture-owner"},
		false, h.al.SteerGenerationCancel); err != nil {
		t.Fatalf("single-session StopTurns(B, subtree=false) returned error: %v", err)
	}

	// B itself: stopped, same generation, direct-stop cause.
	assertLandedStopped(t, h, h.tree.B, bBefore, session.StopCauseStop)

	// Everything else — child C, sibling S, parent A, root — untouched.
	cAfter := loadRecord(t, h, h.tree.C)
	if cAfter.State == session.LifecycleStopped {
		t.Errorf("C landed %q after a SINGLE-SESSION stop on B — a session-scoped stop must never cascade (MAJ-002)", cAfter.State)
	}
	if cAfter.State != cBefore.State || cAfter.Generation != cBefore.Generation {
		t.Errorf("C record changed after a single-session stop on B: state %q→%q generation %d→%d", cBefore.State, cAfter.State, cBefore.Generation, cAfter.Generation)
	}
	assertStillRunning(t, h, sNode)
	assertStillRunning(t, h, h.tree.A)
	assertStillRunning(t, h, h.tree.Root)
}

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// RED pack for the message_parent delegate-session-id wiring fix
// (architect note: coordination/logs/msgparent-architect-note.md, written
// 2026-09-27; branch fix/message-parent-delegate-session-id).
//
// These are REAL-PATH tests: every context below is produced by the actual
// production turn-construction pipeline — SteerLauncher.Launch mints the
// durable record, reconstructSteeredTurn builds the turnState from it, and
// runTurn's own conductor stages (createTurnContext + registerTurnContext)
// stamp the per-turn context — never a hand-stamped context. The
// message_parent tool is the auto-registered instance wired with the real
// SteerUpwardDeliverer and the real on-disk lifecycle/inbox stores, exactly
// as session_messaging_wire.go wires it at boot.
//
// RED status at authoring (pre-change code: zero production call sites of
// tools.WithDelegateSessionID, verified by the architect via grep):
//
//   - TestSteeredChildTurnCtxCarriesOwnSessionIDAsDelegateSessionID   — RED (the reachability gap).
//   - TestSteeredChildMessageParentReachesLifecycleAndDelivers        — RED (the reachability gap).
//   - TestRevivedOrdinaryRootTurnCtxCarriesNoDelegateSessionID        — GREEN today by design; a regression
//     guard: it dies if GREEN stamps the field unconditionally (without the
//     rec.SteeredBy != nil gate, an ordinary-root revival would carry the
//     root's own id under the delegate key).
//   - TestRootTurnMessageParentStillStructurallyRefused               — GREEN today by design; a regression
//     guard: it dies if the structural "no session context available" refusal
//     stops firing for a root that has its own LifecycleRecord (exactly the
//     regression architect note §2 confirms an unconditional stamp causes).
//   - TestMessageParentTaskOriginContextKeepsTaskRefusal              — GREEN today by design; a labeled
//     CHARACTERIZATION test (see its own comment) pinning the Flag-2 boundary:
//     a steered task-origin session keeps its task-specific refusal.
//   - TestRevivedSteeredChildTurnCtxCarriesSameDelegateSessionID  — GREEN by design; a coverage-gap
//     closer for pr-test-analyzer's standard-gate finding on cde5b4d9b
//     (severity 6): no test drove a REVIVED steered child (ADR-093 D4's
//     redispatch-as-steered-turn) through the delegate-session-id wiring. It
//     drives the REAL child revive path (executeSteer's widened terminal
//     predicate → Reviver.ReviveStoppedSession → steered redispatch) and
//     asserts the revived generation's turn context carries the SAME own
//     session id under the delegate key — the revival mints a generation,
//     never a session id (ADR-093 D4). A FAIL here is a production
//     regression to report, never a test to adjust.
//
// GREEN is backend-lead adding the processOptions.SteeredSessionID field, the
// gated set in reconstructSteeredTurn, and the WithDelegateSessionID stamp in
// registerTurnContext — never a change to these tests.
package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// mpwRegisteredTurnContext drives the REAL per-turn context construction for
// a turnState a real reconstructSteeredTurn call produced: the conductor
// assembly below is copied from loop.go::runTurn's own wiring, and the two
// stage functions it calls — createTurnContext and registerTurnContext — are
// the production code every turn's context goes through. Nothing here is
// hand-stamped: the delegate-session-id value must arrive in the context via
// registerTurnContext alone, or the assertion that reads it fails.
func mpwRegisteredTurnContext(t *testing.T, al *AgentLoop, ts *turnState) context.Context {
	t.Helper()
	rz := &agentLoopRunTurnFinalize{}
	rz.rc = &agentLoopRunTurnConductor{}
	rz.rc.rx = &agentLoopRunTurnTools{ctx: context.Background()}
	rz.rc.rx.rr = &agentLoopRunTurnResponse{}
	rz.rc.rx.rr.rq = &agentLoopRunTurnRequest{}
	rz.rc.rx.rr.rq.ri = &agentLoopRunTurnIteration{}
	rz.rc.rx.rr.rq.ri.rf = &agentLoopRunTurnFallbacks{}
	rz.rc.rx.rr.rq.ri.rf.rt = &agentLoopRunTurn{al: al, ts: ts}

	switch rz.rc.createTurnContext() {
	case agentLoopRunTurnConductorReturn:
		t.Fatalf("createTurnContext refused the turn: %v", rz.rc.ret1)
	}
	t.Cleanup(rz.rc.turnCancel)
	rz.rc.registerTurnContext()
	return rz.rc.rx.rr.rq.ri.rf.rt.turnCtx
}

// mpwWiredMessageParentTool wires the loop's steer audience deps exactly as
// production does (real SteerUpwardDeliverer back-wired to the loop, real
// classifier over the real stores) and returns the auto-registered
// message_parent tool instance the wire pass reconstructed — the same
// sequence session_messaging_wire_test.go's
// TestSessionMessagingConsumer_MessageParentDirectPath_ReachesInbox uses,
// and the same wireSessionMessagingForAgent reconstruction production's
// gateway boot runs.
func mpwWiredMessageParentTool(t *testing.T, al *AgentLoop) *tools.MessageParentTool {
	t.Helper()
	al.SetSteerAudienceDeps(
		NewSteerAudienceResolver(NewSteerRecordClassifier(al.GetSessionLifecycleStore(), al.GetSessionStore())),
		nil,
		NewSteerUpwardDeliverer(),
	)
	inst, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	if !ok {
		t.Fatalf("test agent %q not found in registry", testDefaultAgentID)
	}
	mpAny, ok := inst.Tools.Get("message_parent")
	if !ok {
		t.Fatal("message_parent tool not registered after session-messaging wiring")
	}
	mp, ok := mpAny.(*tools.MessageParentTool)
	if !ok {
		t.Fatalf("message_parent tool is %T, want *tools.MessageParentTool", mpAny)
	}
	return mp
}

// mpwLaunchChild delegates a real child from a real steering chat session and
// returns the pair (steerer id, child id). SteerLauncher.Launch — not a
// hand-built LifecycleRecord — is the only record source these tests accept:
// the note's §1 one-id invariant (mint → session → record → LaunchResult)
// is part of the behaviour under test.
func mpwLaunchChild(t *testing.T, al *AgentLoop, task string) (string, string) {
	t.Helper()
	launcher := NewSteerLauncher(al)
	parentID := newTestSteeringSession(t, al, "ws-1")
	launched, err := launcher.Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: parentID,
		TargetAgentID:     testDefaultAgentID,
		Task:              task,
		Origin:            steer.Origin{Kind: steer.OriginKindDelegate, CallID: "call-delegate-session-wiring"},
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	return parentID, launched.SessionID
}

// TestSteeredChildTurnCtxCarriesOwnSessionIDAsDelegateSessionID is Flag 3(a):
// a turn reconstructed by reconstructSteeredTurn for a record with
// rec.SteeredBy != nil must resolve tools.ToolDelegateSessionID(ctx) to the
// session's OWN LifecycleRecord.SessionID (§1's identifier; the same id
// message_parent.go::messageParentToolExecute.prepareMessage loads the
// record by). The value already sits under the transcript key for a steered
// turn — the assertion is deliberately on the DELEGATE key, the semantic key
// the fix adds (Flag 3: "assert the semantic key, not the value's existence
// under another key").
func TestSteeredChildTurnCtxCarriesOwnSessionIDAsDelegateSessionID(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	parentID, childID := mpwLaunchChild(t, al, "report progress to your parent")

	rec, err := al.GetSessionLifecycleStore().Load(childID)
	if err != nil {
		t.Fatalf("Load child lifecycle: %v", err)
	}
	if rec.SteeredBy == nil {
		t.Fatal("precondition failed: steered child record has no SteeredBy edge")
	}

	ts, err := al.reconstructSteeredTurn(rec, nil)
	if err != nil {
		t.Fatalf("reconstructSteeredTurn: %v", err)
	}
	turnCtx := mpwRegisteredTurnContext(t, al, ts)

	// Exact equality on the delegate key (architect note Flag 3a). Today the
	// delegate key is never stamped, so this reads "" — the reachability gap
	// this RED pack exists to pin.
	if got := tools.ToolDelegateSessionID(turnCtx); got != rec.SessionID {
		t.Fatalf("delegate session id = %q, want the child's own LifecycleRecord.SessionID %q (Flag 3a)",
			got, rec.SessionID)
	}
	// §1's one-id invariant: the record, the created session and the launch
	// result all carry the one minted id.
	if rec.SessionID != childID {
		t.Fatalf("one-id invariant broken: rec.SessionID %q != launched session id %q", rec.SessionID, childID)
	}
	if parentID == "" {
		t.Fatal("precondition failed: steering session id is empty")
	}
}

// TestRevivedOrdinaryRootTurnCtxCarriesNoDelegateSessionID is Flag 3(b): a
// turn reconstructed for an ordinary-root record (rec.SteeredBy == nil — the
// ClassOrdinaryRoot revival/re-entry class reconstructSteeredTurn's own
// SendResponse comment documents) must resolve tools.ToolDelegateSessionID
// as "". The steerer's own record is minted by the REAL launch below
// (launchSteered: "this steering session's first delegation — mint its own
// ordinary_root record now"), OwnerScopeHuman, no edge — the exact shape the
// note's §2 GREEN shape item 2 says stays unstamped.
func TestRevivedOrdinaryRootTurnCtxCarriesNoDelegateSessionID(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	parentID, _ := mpwLaunchChild(t, al, "child work that mints the steerer's own record")

	parentRec, err := al.GetSessionLifecycleStore().Load(parentID)
	if err != nil {
		t.Fatalf("Load steerer's own lifecycle record: %v", err)
	}
	if parentRec.SteeredBy != nil {
		t.Fatal("precondition failed: steering session's own record unexpectedly carries a SteeredBy edge")
	}
	if parentRec.OwnerScopeKind != session.OwnerScopeHuman {
		t.Fatalf("precondition failed: steerer's own record owner scope = %q, want %q",
			parentRec.OwnerScopeKind, session.OwnerScopeHuman)
	}

	ts, err := al.reconstructSteeredTurn(parentRec, nil)
	if err != nil {
		t.Fatalf("reconstructSteeredTurn(ordinary root): %v", err)
	}
	turnCtx := mpwRegisteredTurnContext(t, al, ts)

	if got := tools.ToolDelegateSessionID(turnCtx); got != "" {
		t.Fatalf("delegate session id = %q, want \"\" for an ordinary-root turn (Flag 3b — the root must stay unstamped)", got)
	}
}

// TestRootTurnMessageParentStillStructurallyRefused is Flag 3(c): a root
// chat's message_parent call must keep getting the structural "no session
// context available for this call" refusal — asserted EXACTLY, because the
// regression this pins is subtle: a root that has delegated at least once
// HAS a LifecycleRecord of its own (minted by the real launch below), so an
// unconditional stamp would let the call pass the structural gate,
// lifecycle.Load would SUCCEED, and the call would die later with the
// misleading "(owner_scope_id unset)" error instead (note §2). The exact
// match distinguishes the structural refusal from every downstream variant.
func TestRootTurnMessageParentStillStructurallyRefused(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	parentID, _ := mpwLaunchChild(t, al, "child work whose launch minted the root's own record")
	mp := mpwWiredMessageParentTool(t, al)
	enableSessionMessaging(al)

	parentRec, err := al.GetSessionLifecycleStore().Load(parentID)
	if err != nil {
		t.Fatalf("Load steerer's own lifecycle record: %v", err)
	}
	if parentRec.SteeredBy != nil {
		t.Fatal("precondition failed: root's own record unexpectedly carries a SteeredBy edge")
	}

	ts, err := al.reconstructSteeredTurn(parentRec, nil)
	if err != nil {
		t.Fatalf("reconstructSteeredTurn(ordinary root): %v", err)
	}
	turnCtx := mpwRegisteredTurnContext(t, al, ts)

	res := mp.Execute(turnCtx, map[string]any{
		"kind": "progress",
		"text": "a root session must never reach any parent inbox",
	})
	if !res.IsError {
		t.Fatalf("REGRESSION: root turn's message_parent call was ACCEPTED (%s) — the structural refusal must keep firing (Flag 3c)", res.ForLLM)
	}
	if res.ForLLM != "message_parent: no session context available for this call" {
		t.Fatalf("refusal = %q, want exactly %q — the structural refusal, not a downstream owner-scope variant (Flag 3c / note §2)",
			res.ForLLM, "message_parent: no session context available for this call")
	}
}

// TestSteeredChildMessageParentReachesLifecycleAndDelivers is Flag 3(d): a
// steered child's message_parent call, on a context produced by the real
// turn-construction pipeline, must pass validateContext, reach
// lifecycle.Load(rec.SessionID) successfully (record found), and deliver —
// the message lands in the parent's durable inbox keyed by the owner key
// ownerKeyFor derives from rec.SteeringSessionID() (the steering session's
// id). Drain under (parentID, childID) succeeding with exactly one entry
// proves both: Load found the record and the owner key routed to the parent.
func TestSteeredChildMessageParentReachesLifecycleAndDelivers(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	parentID, childID := mpwLaunchChild(t, al, "report progress to your parent")
	mp := mpwWiredMessageParentTool(t, al)
	enableSessionMessaging(al)

	rec, err := al.GetSessionLifecycleStore().Load(childID)
	if err != nil {
		t.Fatalf("Load child lifecycle: %v", err)
	}
	if rec.SteeredBy == nil {
		t.Fatal("precondition failed: steered child record has no SteeredBy edge")
	}
	if got := rec.SteeringSessionID(); got != parentID {
		t.Fatalf("precondition failed: rec.SteeringSessionID() = %q, want the steering session %q", got, parentID)
	}

	ts, err := al.reconstructSteeredTurn(rec, nil)
	if err != nil {
		t.Fatalf("reconstructSteeredTurn: %v", err)
	}
	turnCtx := mpwRegisteredTurnContext(t, al, ts)

	res := mp.Execute(turnCtx, map[string]any{
		"kind": "progress",
		"text": "child progress line from a real steered turn context",
	})
	// RED today: without the delegate-session-id stamp the call dies in
	// validateContext with "message_parent: no session context available for
	// this call" — the reachability gap. GREEN: accepted.
	if res.IsError {
		t.Fatalf("message_parent refused a steered child's call on a real turn context: %s", res.ForLLM)
	}

	msgs, _, _, err := al.GetMessageInboxStore().Drain(parentID, childID, "", 10)
	if err != nil {
		t.Fatalf("Drain(parent inbox): %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("parent inbox under owner key %q holds %d messages for child %q, want exactly 1 — the delivered progress message", parentID, len(msgs), childID)
	}
}

// TestMessageParentTaskOriginContextKeepsTaskRefusal pins the Flag-2
// boundary: a steered TASK-origin session keeps its task-specific refusal.
//
// Characterization test (elicify-test-writing): this pins the tool's
// RunningTaskID branch as designed behaviour — the oracle is Flag 2 of the
// architect note plus the branch's own documented rationale in
// message_parent.go::validateContext ("a task dispatch is not a delegate.run
// call... there is no parent inbox this call could ever reach"), not an
// independent spec of the string. The context shape below —
// WithRunningTaskID, no delegate-session-id — is the shape
// task_executor.go::dispatchLaunchedTask → processTaskDirect produces
// (it stamps RunningTaskID and deliberately never a delegate id), which
// GREEN must NOT change: dispatchLaunchedTask never enters
// reconstructSteeredTurn, so no steered-task turn may gain the stamp.
func TestMessageParentTaskOriginContextKeepsTaskRefusal(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	mp := mpwWiredMessageParentTool(t, al)
	enableSessionMessaging(al)

	taskCtx := tools.WithRunningTaskID(context.Background(), "task-delegate-wiring-red")
	res := mp.Execute(taskCtx, map[string]any{
		"kind": "progress",
		"text": "a task-origin session reports via goal_claim, never via message_parent",
	})
	if !res.IsError {
		t.Fatal("REGRESSION: a task-origin session's message_parent call was accepted — the task-specific refusal must keep firing (Flag 2)")
	}
	if !strings.Contains(res.ForLLM, "this task run has no parent session to message") ||
		!strings.Contains(res.ForLLM, "goal_claim") {
		t.Fatalf("refusal = %q, want the task-specific refusal routing to goal_claim (Flag 2 / ADR-053's division)", res.ForLLM)
	}
}

// TestRevivedSteeredChildTurnCtxCarriesSameDelegateSessionID closes the
// coverage gap pr-test-analyzer found in the standard-gate review of commit
// cde5b4d9b (severity 6): none of the tests above drive a REVIVED steered
// child — ADR-093 D4's redispatch-as-steered-turn — through the
// delegate-session-id wiring. Only a first-run steered child
// (TestSteeredChildTurnCtxCarriesOwnSessionIDAsDelegateSessionID) and a
// revived ORDINARY root (TestRevivedOrdinaryRootTurnCtxCarriesNoDelegate
// SessionID) reach the context level, and D4's own record-level test
// (adr093_open_conversation_test.go::
// TestAdr093FollowUpToTerminalChild_RevivesAndRedispatches) never builds a
// turn context — so the combination "SteeredBy != nil AND a revived
// generation (not the first run)" had zero context-level coverage.
//
// Oracle (ADR-093 D4, docs/internal/architecture/
// ADR-093-open-conversation-must-keep-delegation.md, not the implementation):
// the revival is "generation + 1, ResumedFrom = this session id" — the
// session CONTINUES, its LifecycleRecord.SessionID is unchanged across the
// revival, and the delegate-session-id wiring must carry that same own id on
// the revived generation's turn context, exactly as it does on the first
// run's. The revive-and-redispatch path below is the REAL production path D4
// names for a terminal child: executeSteer's widened terminal predicate
// (delegate_followup.go) → Reviver.ReviveStoppedSession → steered
// redispatch — never a hand-built record or a hand-stamped context.
//
// GREEN by design at authoring: the wiring is already in place and the
// standard gate itself found no defect in it — the gap was coverage, not
// code. A FAIL here is a real production regression this suite would
// otherwise have missed; never adjust the test to fit.
func TestRevivedSteeredChildTurnCtxCarriesSameDelegateSessionID(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	steererID := newTestSteeringSession(t, al, "ws-1")
	childID, childGen := launchSteeredChild(t, al, steererID, "call-revived-delegate-id", "first phase of the work")

	// The child's record goes terminal the way the boot sweep leaves a child
	// mid-flight at restart: failed(interrupted), no Stop marker — D4's
	// F890-1 terminal-WITHOUT-Stop shape (the same terminalisation the
	// precedent TestAdr093FollowUpToTerminalChild_RevivesAndRedispatches
	// uses, which its comment cites).
	preRec, err := al.GetSessionLifecycleStore().Load(childID)
	if err != nil {
		t.Fatalf("Load child lifecycle (pre-revival): %v", err)
	}
	if preRec.SteeredBy == nil {
		t.Fatal("precondition failed: launched child record has no SteeredBy edge")
	}
	if preRec.Stop != nil {
		t.Fatalf("precondition failed: child unexpectedly carries a Stop marker (%+v) — this test drives the terminal-WITHOUT-Stop revival, not the stopped one", preRec.Stop)
	}
	term := *preRec
	term.State = session.LifecycleFailed
	term.FailedReason = failedReasonInterrupted
	if persistErr := al.GetSessionLifecycleStore().Persist(&term); persistErr != nil {
		t.Fatalf("persist terminalised child: %v", persistErr)
	}

	// Park the provider so the redispatched (revived) turn waits mid-flight
	// and the record stays running while the context below is built.
	pp, release := installParkedProvider(t, al)
	defer release()

	result := runDelegateSteer(t, al, steererID, childID, "Continue with phase two")
	if result == nil {
		t.Fatal("delegate(steer) returned nil")
	}
	if result.IsError {
		t.Fatalf("delegate(steer) to a terminal-without-Stop child was refused — ADR-093 D4: a parent's follow-up must revive and redispatch it:\n%s", result.ForLLM)
	}

	// The revival really happened — the precondition for the combination
	// under test: next generation, resumed_from = the child's own id,
	// running, failed reason cleared, and the SteeredBy edge survived (the
	// revived generation must reconstruct as a steered turn).
	revivedRec, err := al.GetSessionLifecycleStore().Load(childID)
	if err != nil {
		t.Fatalf("Load child lifecycle (revived): %v", err)
	}
	if revivedRec.Generation != childGen+1 {
		t.Fatalf("child generation after revival = %d, want %d (ADR-093 D4: the revival mints the next generation)", revivedRec.Generation, childGen+1)
	}
	if revivedRec.ResumedFrom != childID {
		t.Fatalf("child resumed_from after revival = %q, want %q (ADR-093 D4)", revivedRec.ResumedFrom, childID)
	}
	if revivedRec.State != session.LifecycleRunning || revivedRec.FailedReason != "" {
		t.Fatalf("child state after revival = %q/%q, want running with the failed reason cleared (ADR-093 D4)", revivedRec.State, revivedRec.FailedReason)
	}
	if revivedRec.SteeredBy == nil {
		t.Fatal("revived record lost its SteeredBy edge — the revived generation would not reconstruct as a steered turn")
	}

	// THE assertion: the revived generation's turn context — built by the
	// real production construction pipeline (reconstructSteeredTurn +
	// createTurnContext + registerTurnContext, the same functions the
	// redispatched turn itself runs) — carries the child's OWN session id
	// under the delegate key, the SAME id the first run carried: the revival
	// mints a generation, never a new session id (ADR-093 D4's "generation +
	// 1, ResumedFrom = this session id").
	ts, err := al.reconstructSteeredTurn(revivedRec, nil)
	if err != nil {
		t.Fatalf("reconstructSteeredTurn(revived): %v", err)
	}
	turnCtx := mpwRegisteredTurnContext(t, al, ts)
	if got := tools.ToolDelegateSessionID(turnCtx); got != preRec.SessionID {
		t.Fatalf("delegate session id on the revived generation's turn context = %q, want the SAME own id the first run carried %q (ADR-093 D4: generation changes, session id does not)", got, preRec.SessionID)
	}
	if preRec.SessionID != childID {
		t.Fatalf("one-id invariant broken: pre-revival rec.SessionID %q != launched session id %q", preRec.SessionID, childID)
	}

	// The real redispatch reached the actual turn pipeline on this record —
	// the parked provider holds the revived turn (not merely a record the
	// test reconstructed at will).
	adr093WaitForEntered(t, pp, 30*time.Second)
}

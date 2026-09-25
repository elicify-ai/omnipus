// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-093 (docs/internal/architecture/ADR-093-open-conversation-must-keep-
// delegation.md, corrected 2026-09-26, commit 42b878f04) RED tests for the
// decisions that live in pkg/agent decisions: D2 (a launch under a
// stopped/terminal parent is refused outright, leaving no artifacts), D3
// (the boot sweep exempts standing roots), D4 (revival via the ordinary
// inbound-turn admission path, classified first), D6 (a task created from a
// stopped/terminal chat runs as an ordinary root), F890-1 (a parent's
// follow-up to an existing child works at any time), MIN-001 (a resume
// starts exactly one run) and MIN-004 (system wakes never revive a record).
//
// Oracles are the ADR and the re-pinned founder text (docs/internal/
// architecture/ADR-093-founder-decisions.md, F890-1..F890-4), never the
// current implementation. Real stores under temp dirs; the only doubles are
// the LLM provider doubles at the process edge.

package agent

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// adr093Workspace is an arbitrary but stable workspace stamp, shared by the
// records each test later references, so parent/child/creator fields line up.
const adr093Workspace = "ws-adr093"

// adr093Record is one lifecycle record, pre-filled with the fields every
// record in this file needs (owner scope, workspace, agent, origin chat) so
// each test states only what it varies.
func adr093Record(id string, gen int, state session.LifecycleState) *session.LifecycleRecord {
	return &session.LifecycleRecord{
		SessionID:      id,
		Generation:     gen,
		State:          state,
		OwnerScopeKind: session.OwnerScopeHuman,
		WorkspaceID:    adr093Workspace,
		AgentID:        testDefaultAgentID,
		Origin:         &session.Origin{Kind: session.OriginKindChat},
	}
}

// adr093Persist persists rec, naming the session in a failure.
func adr093Persist(t *testing.T, al *AgentLoop, rec *session.LifecycleRecord) {
	t.Helper()
	if err := al.GetSessionLifecycleStore().Persist(rec); err != nil {
		t.Fatalf("persist lifecycle %s: %v", rec.SessionID, err)
	}
}

// adr093Load asserts the record loads and returns it.
func adr093Load(t *testing.T, al *AgentLoop, id string) *session.LifecycleRecord {
	t.Helper()
	rec, err := al.GetSessionLifecycleStore().Load(id)
	if err != nil {
		t.Fatalf("Load lifecycle %s: %v", id, err)
	}
	return rec
}

// adr093LoadStore is adr093Load against a raw lifecycle store (the boot
// sweep harness holds the store directly, not the loop).
func adr093LoadStore(t *testing.T, ls *session.LifecycleStore, id string) *session.LifecycleRecord {
	t.Helper()
	rec, err := ls.Load(id)
	if err != nil {
		t.Fatalf("Load lifecycle %s: %v", id, err)
	}
	return rec
}

// adr093UnifiedSession mints the unified session a creator session needs so
// GetMeta loads (the D6 creator-detection branch) — a plain chat session
// with the workspace stamped.
func adr093UnifiedSession(t *testing.T, al *AgentLoop, id string) {
	t.Helper()
	if _, err := al.GetSessionStore().CreateSessionWithID(id, "", session.SessionTypeChat, "webchat", testDefaultAgentID); err != nil {
		t.Fatalf("CreateSessionWithID(%s): %v", id, err)
	}
	ws := adr093Workspace
	if err := al.GetSessionStore().SetMeta(id, session.MetaPatch{WorkspaceID: &ws}); err != nil {
		t.Fatalf("SetMeta(%s).WorkspaceID: %v", id, err)
	}
}

// adr093TaskExecutor builds a TaskExecutor over the loop's real task store
// with a launcher on the loop's real stores — the production shape, no
// doubles. (Unexported fields; same package.)
func adr093TaskExecutor(t *testing.T, al *AgentLoop) *TaskExecutor {
	t.Helper()
	return &TaskExecutor{
		agentLoop:    al,
		store:        al.taskStore,
		running:      make(map[string]*taskSlot),
		dispatchSema: newDispatchSemaphore(4),
		launcher:     NewSteerLauncher(al),
	}
}

// adr093DelegateTool builds the production-wired delegate tool.
func adr093DelegateTool(t *testing.T, al *AgentLoop) *tools.DelegateTool {
	t.Helper()
	tool := tools.NewDelegateTool("test-model", 0, 0)
	tool.SetSessionLauncher(NewSteerLauncher(al))
	tool.SetDelegationDenyCheckerBackground(func(context.Context, string) *tools.DelegationDenial { return nil })
	return tool
}

// adr093HumanMessage is the InboundMessage a human sends into one specific
// chat session (the ordinary inbound-turn admission path, ADR-093 D4).
func adr093HumanMessage(content, sessionID string) bus.InboundMessage {
	return bus.InboundMessage{
		Channel:    "webchat",
		ChatID:     "chat-" + sessionID,
		Content:    content,
		SessionID:  sessionID,
		SessionKey: "agent:" + testDefaultAgentID + ":session:" + sessionID,
	}
}

// adr093AssertNoSteeredInstructionEntry pins ADR-093 D4's "never dispatched
// as a steered turn": ReviveStoppedSession's own mechanism appends the
// message as a steering instruction (appendSteeredInstruction's transcript
// entry, ID "<sessionID>-instruction-<uuid>", Role user) BEFORE dispatching
// a steered turn. For a ClassOrdinaryRoot record the ADR forbids exactly
// that: Revive-only, ordinary path. Any "-instruction-" entry on the root's
// transcript is the steered machinery having run.
func adr093AssertNoSteeredInstructionEntry(t *testing.T, al *AgentLoop, sessionID string) {
	t.Helper()
	entries, err := al.GetSessionStore().ReadTranscript(sessionID)
	if err != nil {
		t.Fatalf("ReadTranscript(%s): %v", sessionID, err)
	}
	for _, e := range entries {
		if strings.Contains(e.ID, "-instruction-") && e.Role == "user" {
			t.Fatalf("root transcript carries a steering-instruction entry (%s) — ADR-093 D4: a ClassOrdinaryRoot revival is Revive-only on the ordinary path; the steered machinery (ReviveStoppedSession: append instruction + dispatchSteeredSession) must never run for it", e.ID)
		}
	}
}

// adr093WaitForEntered fails the test unless a turn enters the parked
// provider within the timeout.
func adr093WaitForEntered(t *testing.T, pp *parkedProvider, timeout time.Duration) {
	t.Helper()
	select {
	case <-pp.entered:
	case <-time.After(timeout):
		t.Fatalf("no turn entered the parked provider within %s — the resume never started a run", timeout)
	}
}

// adr093AssertNotTerminalWithin polls the record and fails the test the
// MOMENT it turns terminal — the assertion is that it never terminalises in
// the window (MIN-001: a resume starts exactly one run and leaves the chat
// root's record alive).
func adr093AssertNotTerminalWithin(t *testing.T, al *AgentLoop, id string, window time.Duration) {
	t.Helper()
	deadline := time.Now().Add(window)
	for time.Now().Before(deadline) {
		rec := adr093Load(t, al, id)
		if rec.Terminal() {
			t.Fatalf("record %s turned terminal (state %q, reason %q) — ADR-093 D4: the revival's one run must not end the root; the ordinary path keeps the chat resumable", id, rec.State, rec.FailedReason)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestAdr093BootSweep_StandingRootsExempt is ADR-093 D3 / F890-2: after a
// restart, a standing conversation root — chat, channel, heartbeat,
// scheduled, or a record with no origin kind — stays usable: the boot sweep
// must exempt it (stays running), while a steered child and a task root are
// still swept to failed(interrupted).
func TestAdr093BootSweep_StandingRootsExempt(t *testing.T) {
	h := newBootSweepHarness(t)

	// Five standing roots across every exempt origin kind (ADR-093 D3).
	standingIDs := []string{
		"adr093-root-chat",
		"adr093-root-channel",
		"adr093-root-heartbeat",
		"adr093-root-scheduled",
		"adr093-root-nokind",
	}
	standingOrigins := map[string]*session.Origin{
		"adr093-root-chat":      {Kind: session.OriginKindChat},
		"adr093-root-channel":   {Kind: session.OriginKindChannel},
		"adr093-root-heartbeat": {Kind: session.OriginKindHeartbeat},
		"adr093-root-scheduled": {Kind: session.OriginKindScheduled},
		// No origin at all: the sweep cannot prove the record belongs to a
		// delegation, so it must not be swept (ADR-093 D3's exemption rule).
		"adr093-root-nokind": nil,
	}
	for _, id := range standingIDs {
		rec := adr093Record(id, 1, session.LifecycleRunning)
		rec.Origin = standingOrigins[id]
		persistLifecycle(t, h.ls, rec)
	}

	// Two sweepable shapes that MUST stay sweepable (ADR-093 D3).
	swept := adr093Record("adr093-child-steered", 1, session.LifecycleRunning)
	swept.Origin = &session.Origin{Kind: session.OriginKindChat}
	swept.SteeredBy = &session.SteeredBy{SteeringSessionID: "adr093-root-chat", RootSessionID: "adr093-root-chat"}
	persistLifecycle(t, h.ls, swept)

	taskRoot := adr093Record("adr093-root-task", 1, session.LifecycleRunning)
	taskRoot.Origin = &session.Origin{Kind: session.OriginKindTask, TaskID: "task-adr093"}
	persistLifecycle(t, h.ls, taskRoot)

	res := h.pe.runBootSweep(context.Background())

	gotSwept := append([]string{}, res.SweptToFailed...)
	sort.Strings(gotSwept)
	wantSwept := []string{"adr093-child-steered", "adr093-root-task"}
	if len(gotSwept) != len(wantSwept) {
		t.Fatalf("swept set = %v, want exactly %v (ADR-093 D3: only the delegation-provable records are swept)", gotSwept, wantSwept)
	}
	for i := range wantSwept {
		if gotSwept[i] != wantSwept[i] {
			t.Fatalf("swept set = %v, want exactly %v (ADR-093 D3)", gotSwept, wantSwept)
		}
	}

	for _, id := range standingIDs {
		rec := adr093LoadStore(t, h.ls, id)
		if rec.State != session.LifecycleRunning {
			t.Fatalf("standing root %s was swept to %q/%q — ADR-093 D3 / F890-2: the restart sweep never makes a standing conversation unusable", id, rec.State, rec.FailedReason)
		}
	}
	for _, id := range wantSwept {
		rec := adr093LoadStore(t, h.ls, id)
		if rec.State != session.LifecycleFailed || rec.FailedReason != failedReasonInterrupted {
			t.Fatalf("sweepable record %s = %q/%q, want failed/%s (ADR-093 D3 keeps the delegation sweep)", id, rec.State, rec.FailedReason, failedReasonInterrupted)
		}
	}
}

// TestAdr093Launch_StoppedParentRefusalLeavesNoArtifacts is ADR-093 D2's
// stopped branch: a Launch under a parent carrying a Stop for its CURRENT
// generation is refused outright — before any artifact is built — and the
// parent's record is untouched. RED today: the stamp-at-launch branch
// publishes a Stop-stamped child instead (steer_launcher.go::launchSteered).
func TestAdr093Launch_StoppedParentRefusalLeavesNoArtifacts(t *testing.T) {
	al, cleanup := newSteerAL(t)
	t.Cleanup(cleanup)
	parentID := newTestSteeringSession(t, al, adr093Workspace)

	rec := adr093Record(parentID, 1, session.LifecycleRunning)
	rec.Stop = &session.Stop{At: time.Now(), Generation: 1, By: session.Principal{Kind: session.PrincipalKindHuman}}
	adr093Persist(t, al, rec)

	_, err := NewSteerLauncher(al).Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: parentID,
		TargetAgentID:     testDefaultAgentID,
		Task:              "Follow up on the draft",
		Origin:            steer.Origin{Kind: steer.OriginKindDelegate},
	})

	// D2: refused outright — and not with the lifecycle store's own terminal
	// error (that would mean the refusal came after artifacts were built).
	if err == nil {
		t.Fatalf("Launch under a stopped parent succeeded — ADR-093 D2 refuses it outright (today the stamp-at-launch branch publishes a stamped child instead)")
	}
	if errors.Is(err, session.ErrLifecycleTerminalImmutable) {
		t.Fatalf("refusal error is the lifecycle store's own (%v) — ADR-093 D2: the refusal is the launcher's own decision BEFORE any artifact is built", err)
	}

	// No artifacts: only the parent's lifecycle record exists, unchanged.
	records, err := al.GetSessionLifecycleStore().List(session.LifecycleFilter{})
	if err != nil {
		t.Fatalf("List lifecycle after refused launch: %v", err)
	}
	if len(records) != 1 || records[0].SessionID != parentID {
		ids := make([]string, 0, len(records))
		for _, r := range records {
			ids = append(ids, r.SessionID)
		}
		t.Fatalf("lifecycle records after the refused launch = %v, want only the parent %s (ADR-093 D2: the refusal leaves no child record)", ids, parentID)
	}
	parent := adr093Load(t, al, parentID)
	if parent.Generation != 1 || parent.State != session.LifecycleRunning || parent.Stop == nil || parent.Stop.Generation != 1 {
		t.Fatalf("parent after refused launch = gen %d state %q Stop %+v, want gen 1 running with the human Stop intact (ADR-093 D2: the refusal never touches the parent)", parent.Generation, parent.State, parent.Stop)
	}
}

// TestAdr093Launch_AfterRevivalStopRefusesLaunch is D2's second site: the
// same refusal applies when the parent's record is generation 2 (a revival
// already happened) and a new human Stop for the CURRENT generation 2 sits
// on it. RED today: same stamp-at-launch defect.
func TestAdr093Launch_AfterRevivalStopRefusesLaunch(t *testing.T) {
	al, cleanup := newSteerAL(t)
	t.Cleanup(cleanup)
	parentID := newTestSteeringSession(t, al, adr093Workspace)

	rec := adr093Record(parentID, 2, session.LifecycleRunning)
	rec.ResumedFrom = parentID
	rec.Stop = &session.Stop{At: time.Now(), Generation: 2, By: session.Principal{Kind: session.PrincipalKindHuman}}
	adr093Persist(t, al, rec)

	_, err := NewSteerLauncher(al).Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: parentID,
		TargetAgentID:     testDefaultAgentID,
		Task:              "Follow up on the draft",
		Origin:            steer.Origin{Kind: steer.OriginKindDelegate},
	})

	if err == nil {
		t.Fatalf("Launch under a generation-2 parent with a current human Stop succeeded — ADR-093 D2 refuses it outright")
	}
	if errors.Is(err, session.ErrLifecycleTerminalImmutable) {
		t.Fatalf("refusal error is the lifecycle store's own (%v) — ADR-093 D2: the refusal is the launcher's own decision BEFORE any artifact is built", err)
	}

	records, err := al.GetSessionLifecycleStore().List(session.LifecycleFilter{})
	if err != nil {
		t.Fatalf("List lifecycle after refused launch: %v", err)
	}
	if len(records) != 1 || records[0].SessionID != parentID {
		ids := make([]string, 0, len(records))
		for _, r := range records {
			ids = append(ids, r.SessionID)
		}
		t.Fatalf("lifecycle records after the refused launch = %v, want only the parent %s (ADR-093 D2)", ids, parentID)
	}
	parent := adr093Load(t, al, parentID)
	if parent.Generation != 2 || parent.ResumedFrom != parentID || parent.Stop == nil || parent.Stop.Generation != 2 || parent.State != session.LifecycleRunning {
		t.Fatalf("parent after refused launch = gen %d resumed_from %q state %q Stop %+v, want gen 2 running resumed_from=self with the Stop for generation 2 intact (ADR-093 D2)", parent.Generation, parent.ResumedFrom, parent.State, parent.Stop)
	}
}

// TestAdr093FollowUpToTerminalChild_RevivesAndRedispatches is F890-1: "the
// parent session could even send a follow-up to the child session … sessions
// and child sessions are always resumable". A parent's delegate(steer) to a
// child whose record is already terminal (failed/interrupted) must revive it
// (next generation via resumed_from) and redispatch the instruction — not
// refuse with "cannot be steered". RED today: executeSteer refuses a
// terminal child (delegate_followup.go::executeSteer's terminal check).
func TestAdr093FollowUpToTerminalChild_RevivesAndRedispatches(t *testing.T) {
	al, cleanup := newSteerAL(t)
	t.Cleanup(cleanup)
	steererID := newTestSteeringSession(t, al, adr093Workspace)

	childID, _ := launchSteeredChild(t, al, steererID, "call-adr093-fu", "First phase of the work")

	// The child's record goes terminal the way a sweep or a failed turn
	// would leave it: failed(interrupted) at its current generation.
	childRec := adr093Load(t, al, childID)
	term := *childRec
	term.State = session.LifecycleFailed
	term.FailedReason = failedReasonInterrupted
	adr093Persist(t, al, &term)

	// Park the child's next turn so the test can inspect the record between
	// "revived" and "ran to completion".
	pp, release := installParkedProvider(t, al)
	defer release()

	result := runDelegateSteer(t, al, steererID, childID, "Continue with phase two")
	if result == nil {
		t.Fatal("delegate(steer) returned nil")
	}
	if result.IsError {
		t.Fatalf("delegate(steer) to a terminal child was refused — F890-1: a parent's follow-up to an existing child must revive and redispatch it:\n%s", result.ForLLM)
	}

	// While the redispatched turn waits in the provider, the record shows the
	// revival: next generation, resumed_from = the child itself, running,
	// failed reason cleared.
	rec := adr093Load(t, al, childID)
	if rec.Generation != childRec.Generation+1 {
		t.Fatalf("child generation after follow-up = %d, want %d (ADR-093 F890-1: the follow-up revives the child via resumed_from)", rec.Generation, childRec.Generation+1)
	}
	if rec.ResumedFrom != childID {
		t.Fatalf("child resumed_from after follow-up = %q, want %q (ADR-093 F890-1)", rec.ResumedFrom, childID)
	}
	if rec.State != session.LifecycleRunning || rec.FailedReason != "" {
		t.Fatalf("child state after follow-up = %q/%q, want running with the failed reason cleared (ADR-093 F890-1)", rec.State, rec.FailedReason)
	}
	adr093WaitForEntered(t, pp, 30*time.Second)
}

// TestAdr093RootRevival_ClassifyFirstOneTurnOneGeneration is ADR-093 D4 +
// MIN-001: a human message into a "running but stopped" chat root is
// classified BEFORE any steering machinery runs: ClassOrdinaryRoot → the
// record is revived (next generation via resumed_from, running) and the
// message is processed on the ordinary inbound-turn path — exactly one run
// starts (one provider entry), and the root's record never turns terminal
// (today's defect: the steered-completion path terminalises the revived
// root, MAJ-003).
func TestAdr093RootRevival_ClassifyFirstOneTurnOneGeneration(t *testing.T) {
	al, cleanup := newSteerAL(t)
	t.Cleanup(cleanup)
	parentID := newTestSteeringSession(t, al, adr093Workspace)

	rec := adr093Record(parentID, 1, session.LifecycleRunning)
	rec.Stop = &session.Stop{At: time.Now(), Generation: 1, By: session.Principal{Kind: session.PrincipalKindHuman}}
	adr093Persist(t, al, rec)

	// Park the provider so the revived turn waits, and let the admission
	// path run in the background (it dispatches the turn).
	pp, release := installParkedProvider(t, al)
	defer release()

	errCh := make(chan error, 1)
	go func() {
		errCh <- al.enqueueSteeringFromMessage(adr093HumanMessage("Right, carry on with the plan.", parentID))
	}()

	adr093WaitForEntered(t, pp, 30*time.Second)

	// The revival happened on the ordinary path: one generation bump.
	rec = adr093Load(t, al, parentID)
	if rec.Generation != 2 {
		t.Fatalf("record generation after the human message = %d, want 2 (ADR-093 D4: revival mints the next generation via resumed_from)", rec.Generation)
	}
	if rec.ResumedFrom != parentID {
		t.Fatalf("record resumed_from after the human message = %q, want %q (ADR-093 D4)", rec.ResumedFrom, parentID)
	}
	if rec.State != session.LifecycleRunning || rec.FailedReason != "" {
		t.Fatalf("record state after the human message = %q/%q, want running, reason cleared (ADR-093 D4)", rec.State, rec.FailedReason)
	}
	// The revival must be Revive-only on the ordinary path — the steered
	// machinery's instruction write is the forbidden fingerprint (MAJ-003).
	adr093AssertNoSteeredInstructionEntry(t, al, parentID)

	// Exactly one run: release the parked turn and let it finish; the record
	// must never turn terminal (today's MAJ-003 defect), and no second run
	// may enter the provider.
	release()
	adr093AssertNotTerminalWithin(t, al, parentID, 3*time.Second)
	if extra := len(pp.entered); extra != 0 {
		t.Fatalf("%d additional run(s) entered the provider after the resume — ADR-093 MIN-001: a resume starts exactly one run", extra)
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("enqueueSteeringFromMessage returned an error for a human message into a stopped chat: %v", err)
		}
	default:
		// The admission call may still be draining the dispatched turn; its
		// outcome is observable through the record assertions above.
	}
}

// adr093CreatorTask builds the task.Task a creator chat produces, wired to
// that chat via OriginSessionID (the D6 creator-detection input).
func adr093CreatorTask(title, creatorID string) *task.Task {
	return &task.Task{
		Title:           title,
		Prompt:          "Draft the quarterly numbers",
		AgentID:         testDefaultAgentID,
		Action:          task.ActionLLM,
		Status:          task.StatusNext,
		WorkspaceID:     adr093Workspace,
		OriginSessionID: creatorID,
	}
}

// adr093AssertOrdinaryTaskRoot asserts a launched task session is an
// ORDINARY root (ADR-093 D6): steered by nobody, task origin carrying the
// task id.
func adr093AssertOrdinaryTaskRoot(t *testing.T, al *AgentLoop, childID, tkID string) {
	t.Helper()
	rec := adr093Load(t, al, childID)
	if rec.SteeredBy != nil {
		t.Fatalf("task session %s is steered by %q — ADR-093 D6: a task from a stopped/terminal chat runs as an ordinary root, with no steering edge", childID, rec.SteeredBy.SteeringSessionID)
	}
	if rec.Origin == nil || rec.Origin.Kind != session.OriginKindTask || rec.Origin.TaskID != tkID {
		t.Fatalf("task session %s origin = %+v, want task origin with task id %s (ADR-093 D6)", childID, rec.Origin, tkID)
	}
}

// TestAdr093TaskFromTerminalCreator_RunsAsOrdinaryRoot is ADR-093 D6 /
// F890-2: a task created from a chat whose own record is already terminal
// (failed/interrupted) still runs — as an ordinary root, without reviving
// the chat. RED today: the steered launch under a terminal parent fails the
// parent-record mutation.
func TestAdr093TaskFromTerminalCreator_RunsAsOrdinaryRoot(t *testing.T) {
	al, cleanup := newSteerAL(t)
	t.Cleanup(cleanup)
	creatorID := newTestSteeringSession(t, al, adr093Workspace)

	creator := adr093Record(creatorID, 1, session.LifecycleFailed)
	creator.FailedReason = failedReasonInterrupted
	adr093Persist(t, al, creator)

	te := adr093TaskExecutor(t, al)
	tk := adr093CreatorTask("Terminal creator task", creatorID)
	if err := te.store.Create(tk); err != nil {
		t.Fatalf("create task: %v", err)
	}

	childID, err := te.startTaskNowViaLauncher(context.Background(), tk)
	if err != nil {
		t.Fatalf("startTaskNowViaLauncher from a terminal creator chat failed — F890-2 / ADR-093 D6: the task still runs, as an ordinary root: %v", err)
	}
	if childID == "" || childID == creatorID {
		t.Fatalf("task session id = %q, want a fresh session distinct from the creator %s", childID, creatorID)
	}
	adr093AssertOrdinaryTaskRoot(t, al, childID, tk.ID)

	// The chat is NOT revived by a task launch (ADR-093 D6: no revival of
	// the chat): its record stays failed at generation 1.
	got := adr093Load(t, al, creatorID)
	if got.Generation != 1 || got.State != session.LifecycleFailed || got.FailedReason != failedReasonInterrupted {
		t.Fatalf("creator chat after task launch = gen %d state %q/%q, want gen 1 failed/%s unchanged (ADR-093 D6: a task launch never revives the chat)", got.Generation, got.State, got.FailedReason, failedReasonInterrupted)
	}
}

// TestAdr093TaskFromStoppedCreator_RunsAsOrdinaryRoot is D6's stopped
// branch: a task created from a "running but stopped" chat still runs — as
// an ordinary root, with the chat's Stop untouched. RED today: the steered
// launch stamps the child with the parent's Stop and the dispatch refuses
// with ErrDispatchCancelled.
func TestAdr093TaskFromStoppedCreator_RunsAsOrdinaryRoot(t *testing.T) {
	al, cleanup := newSteerAL(t)
	t.Cleanup(cleanup)
	creatorID := newTestSteeringSession(t, al, adr093Workspace)

	creator := adr093Record(creatorID, 1, session.LifecycleRunning)
	creator.Stop = &session.Stop{At: time.Now(), Generation: 1, By: session.Principal{Kind: session.PrincipalKindHuman}}
	adr093Persist(t, al, creator)

	te := adr093TaskExecutor(t, al)
	tk := adr093CreatorTask("Stopped creator task", creatorID)
	if err := te.store.Create(tk); err != nil {
		t.Fatalf("create task: %v", err)
	}

	childID, err := te.startTaskNowViaLauncher(context.Background(), tk)
	if err != nil {
		t.Fatalf("startTaskNowViaLauncher from a stopped creator chat failed — F890-2 / ADR-093 D6: the task still runs, as an ordinary root: %v", err)
	}
	adr093AssertOrdinaryTaskRoot(t, al, childID, tk.ID)

	got := adr093Load(t, al, creatorID)
	if got.Generation != 1 || got.State != session.LifecycleRunning || got.Stop == nil || got.Stop.Generation != 1 {
		t.Fatalf("creator chat after task launch = gen %d state %q Stop %+v, want gen 1 running with the human Stop intact (ADR-093 D6: the task never touches the chat)", got.Generation, got.State, got.Stop)
	}
}

// TestAdr093TaskFromLiveCreator_SteeredUnchanged pins the behavior D6
// deliberately keeps: a task created from a LIVE creator chat (record
// running, no Stop) is still a STEERED launch — the child carries the
// steering edge and the task origin. Characterization pin from the ADR's
// "unchanged" list, not a RED test.
func TestAdr093TaskFromLiveCreator_SteeredUnchanged(t *testing.T) {
	al, cleanup := newSteerAL(t)
	t.Cleanup(cleanup)
	creatorID := newTestSteeringSession(t, al, adr093Workspace)

	adr093Persist(t, al, adr093Record(creatorID, 1, session.LifecycleRunning))

	te := adr093TaskExecutor(t, al)
	tk := adr093CreatorTask("Live creator task", creatorID)
	if err := te.store.Create(tk); err != nil {
		t.Fatalf("create task: %v", err)
	}

	childID, err := te.startTaskNowViaLauncher(context.Background(), tk)
	if err != nil {
		t.Fatalf("startTaskNowViaLauncher from a live creator chat: %v", err)
	}
	rec := adr093Load(t, al, childID)
	if rec.SteeredBy == nil || rec.SteeredBy.SteeringSessionID != creatorID {
		t.Fatalf("task session %s steered_by = %+v, want steered by the live creator %s (ADR-093 D6: the live-creator path is unchanged)", childID, rec.SteeredBy, creatorID)
	}
	if rec.Origin == nil || rec.Origin.Kind != session.OriginKindTask || rec.Origin.TaskID != tk.ID {
		t.Fatalf("task session %s origin = %+v, want task origin with task id %s", childID, rec.Origin, tk.ID)
	}
}

// TestAdr093SystemWakeIntoTerminalRoot_NoRevival is MIN-004's negative pin:
// a system wake (a heartbeat-style wake, not a human message) into a chat
// whose record is terminal does NOT revive the record, and a delegation
// attempted from that state still gets D5's refusal. The revival trigger is
// the human's message — nothing else.
func TestAdr093SystemWakeIntoTerminalRoot_NoRevival(t *testing.T) {
	al, cleanup := newSteerAL(t)
	t.Cleanup(cleanup)
	parentID := newTestSteeringSession(t, al, adr093Workspace)

	parent := adr093Record(parentID, 1, session.LifecycleFailed)
	parent.FailedReason = failedReasonInterrupted
	adr093Persist(t, al, parent)

	// A plain system wake routed at the chat's transcript — NOT the steered
	// wake path (no steer_message_id metadata).
	if _, err := al.processSystemMessage(context.Background(), bus.InboundMessage{
		Channel:                  "system",
		ChatID:                   "webchat:" + parentID,
		Content:                  "scheduled heartbeat wake",
		AsyncTranscriptSessionID: parentID,
		AsyncOriginAgentID:       testDefaultAgentID,
	}); err != nil {
		t.Fatalf("processSystemMessage: %v", err)
	}

	got := adr093Load(t, al, parentID)
	if got.Generation != 1 || got.State != session.LifecycleFailed || got.ResumedFrom != "" {
		t.Fatalf("record after system wake = gen %d state %q resumed_from %q — ADR-093 MIN-004: a system wake never revives a record", got.Generation, got.State, got.ResumedFrom)
	}

	// And delegation from that state is still the D5 backstop refusal.
	tool := adr093DelegateTool(t, al)
	callCtx := tools.WithToolCallID(
		tools.WithAgentID(tools.WithTranscriptSessionID(context.Background(), parentID), testDefaultAgentID),
		"call-adr093-wake",
	)
	result := tool.Execute(callCtx, map[string]any{
		"action":   "run",
		"agent_id": testDefaultAgentID,
		"task":     "Prepare the spreadsheet",
	})
	if result == nil {
		t.Fatal("delegate(run) returned nil")
	}
	if !result.IsError {
		t.Fatalf("delegate from a terminal chat after a system wake must be refused (ADR-093 D2/D5), got success:\n%s", result.ForLLM)
	}
	assertIssue890RefusalText(t, "ForLLM", result.ForLLM, parentID)
}

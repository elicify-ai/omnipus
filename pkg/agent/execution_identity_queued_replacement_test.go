// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Independent qa-lead RED pack for the W2b execution-identity seam, written
// from the frozen ADR before asserting on this tree (elicify-test-writing:
// oracle independence — every expected value below derives from the ADR
// text, never from observed behaviour of the code under test).
//
// Source: ADR-20260928-sub-agent-control-plane (frozen asset cd20cf8b),
// D2 "Execution identity and effect-boundary checks" and D2 CRIT-001
// "One outcome/publication commit boundary":
//
//   - The internal identity is (session_id, generation, boot_seq, run_id),
//     persisted under the lifecycle lock BEFORE admission; "a new admission
//     never reuses a previous run's identity ... even at the same generation
//     and in the same boot".
//   - The terminal/outbox mutation "checks the producing execution identity
//     (including run_id), current generation, terminal state and
//     current-generation Stop fence under the same lock. A late completion
//     from a replaced execution cannot commit against the resumed run, even
//     at the same generation". A refusal "writes no final outbox entry,
//     appends no parent inbox message/frame"; the record keeps the resumed
//     run's queued state, and "same-generation RESUME may then commit/publish
//     a *new* final under the unused id".
//
// The scenario is driven through normal production dependencies only: real
// lifecycle/unified/inbox stores, one BootEpochStore genuinely minted once
// through session.NewBootEpochStore(...).Mint() and injected via
// SetBootEpochStore, SteerLauncher.Launch+Dispatch (the admission path that
// stamps identity before enqueue), SteerCanceller.CancelSubtree (the real
// fence + live-turn cascade), SteerCanceller.Revive (the real same-generation
// resume), the real completion boundary completeSteeredTurnForExecution
// carrying the production claim builder tsExecutionClaim over the stopped
// run's immutable turn handle, and the real queue promotion
// (drainSteerQueue -> dispatchSteeredSessionReserved) for the replacement.
// No production symbol is mocked and no test-only production hook is set
// (completeStateWriteTestHook and friends stay nil); the LLM provider is the
// one process-edge mock, parking the old and blocker turns and answering the
// replacement's turn after a test-side gate.
//
// Interleaving under test (T27's old-effect/new-RESUME shape, one step
// further: the old run's TERMINAL completion arrives after the replacement's
// stamp while the replacement is still QUEUED):
//
//	a-run dispatched and parked (holds the only admission slot)
//	  -> blocker dispatched, queues behind it
//	  -> Stop(a) lands a stopped (fence spent, note kept, never final)
//	  -> a's slot release promotes the blocker
//	  -> RESUME(a) queues the same generation and clears a's identity
//	  -> Dispatch(a) admits the replacement: stamps a fresh run_id, then
//	     queues behind the blocker — stamped and QUEUED, nothing live
//	  -> BARRIER: the replacement's stamp verified in the real store
//	  -> the stopped run's terminal completion is released now, carrying its
//	     immutable handle's claim
//	  -> ORACLE: the completion must be refused outright
//	  -> the replacement is promoted, runs, and publishes its OWN final
//
// Deferred to CHECK (the audit side of the skill's proof-of-failability, not
// claimed here): mutation probes against commitSteeredCompletion's identity
// check. Deliberate gaps: crash cuts between stamp and dispatch (no store
// seam); the bare-loop boot_seq-0 shape (bootEpochFor with no wired store) is
// NOT exercised — faking boot values to reach green is forbidden, and the
// production mint-before-wire ordering is a separate open finding, not a
// subject of this test.

package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

const (
	queuedReplOldAnswer    = "queued-replacement-A-final-must-never-publish"
	queuedReplBlockerTask  = "queued-replacement blocker: occupy the only admission slot"
	queuedReplOldTask      = "queued-replacement A: do the first round of delegated work"
	queuedReplResumeAnswer = "queued-replacement-B-final-the-only-legitimate-result"
)

// replacementScenarioProvider is the one process-edge mock: the blocker
// turn parks until its own release (or the turn's context is cancelled);
// every child-session turn of the steered child parks until childRelease is
// closed and then returns childAnswer. The child's replacement turn carries
// the same transcript as the stopped run's turn, so the gate — not the
// message text — tells the two phases apart: the stopped run's turn exits
// through context cancellation (the Stop), and the replacement's turn is
// only ever dispatched after the gate is armed.
type replacementScenarioProvider struct {
	mu             sync.Mutex
	blockerTask    string
	childAnswer    string
	childRelease   chan struct{}
	blockerRelease chan struct{}
	entered        chan string
}

func newReplacementScenarioProvider(blockerTask string) *replacementScenarioProvider {
	return &replacementScenarioProvider{
		blockerTask:    blockerTask,
		childRelease:   make(chan struct{}),
		blockerRelease: make(chan struct{}),
		entered:        make(chan string, 8),
	}
}

func (p *replacementScenarioProvider) Chat(ctx context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	last := ""
	for _, m := range messages {
		if m.Role == "user" {
			last = m.Content
		}
	}
	p.mu.Lock()
	select {
	case p.entered <- last:
	default:
	}
	childRelease, childAnswer := p.childRelease, p.childAnswer
	blockerRelease, blockerTask := p.blockerRelease, p.blockerTask
	p.mu.Unlock()
	if last == blockerTask {
		select {
		case <-blockerRelease:
			return &providers.LLMResponse{Content: "queued-replacement blocker finished"}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	select {
	case <-childRelease:
		return &providers.LLMResponse{Content: childAnswer}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (p *replacementScenarioProvider) GetDefaultModel() string { return "queued-replacement-scenario" }

// armChildAnswer arms the child gate with answer and closes it: every later
// child-session turn returns answer immediately. Idempotent — a second call
// (e.g. from the teardown cleanup after the test armed it) updates the
// answer without re-closing.
func (p *replacementScenarioProvider) armChildAnswer(answer string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.childAnswer = answer
	select {
	case <-p.childRelease:
	default:
		close(p.childRelease)
	}
}

func (p *replacementScenarioProvider) releaseBlocker() {
	p.mu.Lock()
	defer p.mu.Unlock()
	select {
	case <-p.blockerRelease:
	default:
		close(p.blockerRelease)
	}
}

// waitRecordState polls the real lifecycle store until cond holds — an
// event wait with a deadline on real state, never a wall-clock assertion.
func waitRecordState(t *testing.T, lifecycle *session.LifecycleStore, childID, what string, cond func(*session.LifecycleRecord) bool) *session.LifecycleRecord {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		rec, err := lifecycle.Load(childID)
		if err == nil && rec != nil && cond(rec) {
			return rec
		}
		time.Sleep(10 * time.Millisecond)
	}
	rec, loadErr := lifecycle.Load(childID)
	state, gen := "", -1
	if loadErr == nil && rec != nil {
		state, gen = string(rec.State), rec.Generation
	}
	t.Fatalf("timed out waiting for %s (last state=%q generation=%d load=%v)", what, state, gen, loadErr)
	return nil
}

// waitForGate polls a real in-memory gate condition with a deadline.
func waitForGate(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// setupQueuedReplacementHarness wires newSteerAL with the scenario provider,
// a cap-1 admission gate and one genuinely minted BootEpochStore, mirroring
// the production wiring order (the store is minted once at boot, then
// injected via SetBootEpochStore).
func setupQueuedReplacementHarness(t *testing.T) (*AgentLoop, *replacementScenarioProvider) {
	t.Helper()
	al, _ := newSteerAL(t)
	al.GetConfig().Performance.MaxParallelAgents = 1

	agentInst, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	if !ok {
		t.Fatal("test agent is not registered")
	}
	provider := newReplacementScenarioProvider(queuedReplBlockerTask)
	agentInst.Provider = provider
	// Registered AFTER newSteerAL's own t.Cleanup(al.Close), so — by LIFO —
	// every parked turn is released before Close's bounded drain runs, the
	// same ordering installParkedProvider's doc comment establishes.
	t.Cleanup(func() {
		provider.armChildAnswer("queued-replacement teardown release")
		provider.releaseBlocker()
	})
	wireSteerCompletionDeps(t, al)

	boot := session.NewBootEpochStore(al.GetConfig().Agents.Defaults.Home)
	epoch, err := boot.Mint()
	if err != nil {
		t.Fatalf("Mint(boot epoch): %v", err)
	}
	if epoch != 1 {
		t.Fatalf("boot epoch = %d, want 1 (one genuine mint on a fresh store)", epoch)
	}
	al.SetBootEpochStore(boot)
	return al, provider
}

// launchParkedChild launches a steered child (Launch only — no admission).
func launchParkedChild(t *testing.T, al *AgentLoop, parentID, callID, task string) (string, int) {
	t.Helper()
	res, err := NewSteerLauncher(al).Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: parentID,
		TargetAgentID:     testDefaultAgentID,
		Task:              task,
		Origin:            steer.Origin{Kind: steer.OriginKindDelegate, CallID: callID},
	})
	if err != nil {
		t.Fatalf("Launch(%s): %v", callID, err)
	}
	return res.SessionID, res.Generation
}

// dispatchChild runs the production Dispatch and requires the expected
// admission decision.
func dispatchChild(t *testing.T, al *AgentLoop, sessionID string, gen int, wantRunning bool) {
	t.Helper()
	res, err := NewSteerLauncher(al).Dispatch(context.Background(), sessionID, gen)
	if err != nil {
		t.Fatalf("Dispatch(%s): %v", sessionID, err)
	}
	if wantRunning && res.State != steer.DispatchRunning {
		t.Fatalf("Dispatch(%s) = %+v, want running", sessionID, res)
	}
	if !wantRunning && res.State != steer.DispatchQueued {
		t.Fatalf("Dispatch(%s) = %+v, want queued", sessionID, res)
	}
}

// awaitChildTurnEntered waits for the provider to announce the child
// session's turn and returns its registered immutable handle.
func awaitChildTurnEntered(t *testing.T, al *AgentLoop, provider *replacementScenarioProvider, childID, task string) *turnState {
	t.Helper()
	deadline := time.After(30 * time.Second)
	for {
		select {
		case got := <-provider.entered:
			if got != task {
				continue
			}
			ts := al.getActiveTurnState(childID)
			if ts == nil {
				t.Fatalf("the child turn reached its provider but no turn is registered for %s", childID)
			}
			return ts
		case <-deadline:
			t.Fatalf("the child turn never reached its provider (waiting for task %q)", task)
		}
	}
}

// dispatchParkedRunningChild launches a child, dispatches it, waits for its
// turn to park in the provider, and returns the record snapshot, the
// registered immutable handle and the stamped run identity.
func dispatchParkedRunningChild(t *testing.T, al *AgentLoop, provider *replacementScenarioProvider, parentID, callID, task string) (*session.LifecycleRecord, *turnState, string) {
	t.Helper()
	lifecycle := al.GetSessionLifecycleStore()
	childID, childGen := launchParkedChild(t, al, parentID, callID, task)
	dispatchChild(t, al, childID, childGen, true)
	ts := awaitChildTurnEntered(t, al, provider, childID, task)
	rec := waitRecordState(t, lifecycle, childID, "the child's record to reach running", func(rec *session.LifecycleRecord) bool {
		return rec.State == session.LifecycleRunning
	})
	if rec.ExecutionID == nil || rec.ExecutionID.RunID == "" {
		t.Fatalf("premise: the running child carries no admission stamp: %+v", rec.ExecutionID)
	}
	if handleRunID, _ := ts.executionIdentity(); handleRunID != rec.ExecutionID.RunID {
		t.Fatalf("premise: turn handle run %q disagrees with the stamped record %q", handleRunID, rec.ExecutionID.RunID)
	}
	return rec, ts, rec.ExecutionID.RunID
}

// queueReplacementBehindBlocker drives the production stop -> slot release ->
// same-generation RESUME -> replacement admission sequence and returns the
// replacement's persisted stamp after verifying the barrier in the real
// store: the replacement is stamped with a fresh run at the genuinely minted
// boot epoch while still QUEUED with nothing live.
func queueReplacementBehindBlocker(t *testing.T, al *AgentLoop, oldRunID string, childID string, childGen int, blockerID string, blockerGen int) (string, uint64) {
	t.Helper()
	lifecycle := al.GetSessionLifecycleStore()

	// Stop the old run through the real cascade: fence + hard abort; the
	// turn unwinds with context.Canceled and the fence branch of the
	// completion boundary lands the record stopped — never final.
	canceller := al.steerCanceller()
	if _, err := canceller.CancelSubtree(context.Background(), childID, steer.Principal{
		Kind: steer.PrincipalKindHuman,
		ID:   "queued-replacement-owner",
	}); err != nil {
		t.Fatalf("CancelSubtree(child): %v", err)
	}
	stoppedRec := waitRecordState(t, lifecycle, childID, "the stop to land stopped (fence spent, note kept)", func(rec *session.LifecycleRecord) bool {
		return rec.State == session.LifecycleStopped && rec.StopNote != nil &&
			!(rec.Stop != nil && rec.Stop.Generation == rec.Generation)
	})
	if stoppedRec.Generation != childGen {
		t.Fatalf("premise: stop moved the generation %d -> %d, want the same generation", childGen, stoppedRec.Generation)
	}
	if stoppedRec.Terminal() {
		t.Fatalf("premise: the stop landed terminal (state=%q) — D6: stopped is non-terminal", stoppedRec.State)
	}

	// The old run's slot release promotes the blocker (production drain).
	waitForGate(t, "the blocker to receive the released admission slot", func() bool {
		return al.steerAdmission().hasReservation(blockerID, blockerGen)
	})

	// RESUME: the production same-generation revive queues the record and
	// clears the stopped run's identity.
	resumedGen, err := canceller.Revive(context.Background(), childID, steer.Principal{
		Kind: steer.PrincipalKindHuman,
		ID:   "queued-replacement-owner",
	})
	if err != nil {
		t.Fatalf("Revive(child): %v", err)
	}
	if resumedGen != childGen {
		t.Fatalf("premise: RESUME minted generation %d, want the same generation %d (D2 CRIT-001)", resumedGen, childGen)
	}

	// The replacement's admission: the production dispatch stamps a fresh
	// identity BEFORE enqueue, then queues behind the blocker.
	dispatchChild(t, al, childID, childGen, false)

	// BARRIER: the replacement's stamp is verified in the real store BEFORE
	// the old completion is released, and the replacement is provably still
	// queued with nothing live.
	bRec := waitRecordState(t, lifecycle, childID, "the replacement admission to stamp while queued", func(rec *session.LifecycleRecord) bool {
		return rec.State == session.LifecycleQueued && rec.ExecutionID != nil && rec.ExecutionID.RunID != oldRunID
	})
	bRunID, bBootSeq := bRec.ExecutionID.RunID, bRec.ExecutionID.BootSeq
	if bBootSeq == 0 {
		t.Fatalf("premise: replacement boot_seq = 0, want the genuinely minted epoch (> 0)")
	}
	if al.getActiveTurnState(childID) != nil {
		t.Fatalf("premise: a live turn is registered for %s — the replacement must be queued, not live", childID)
	}
	if got := al.steerAdmission().queueLen(); got != 1 {
		t.Fatalf("premise: admission queue length = %d, want 1 (the replacement waiting behind the blocker)", got)
	}
	return bRunID, bBootSeq
}

// queuedReplInboxFinal scans the parent inbox for entries with the exact
// final id, returning their raw payloads and the ack count.
func queuedReplInboxFinal(t *testing.T, al *AgentLoop, parentID, finalID string) (bodies []string, acks int) {
	t.Helper()
	entries, err := al.GetMessageInboxStore().Entries(parentID)
	if err != nil {
		t.Fatalf("Entries(parent inbox): %v", err)
	}
	for _, entry := range entries {
		if entry.Kind == session.InboxEntryAck {
			for _, id := range entry.AckedIDs {
				if id == finalID {
					acks++
				}
			}
			continue
		}
		if entry.Kind != session.InboxEntryMessage || entry.Message == nil {
			continue
		}
		if messageIDOf(*entry.Message) != finalID {
			continue
		}
		raw, err := entry.Message.MarshalJSON()
		if err != nil {
			bodies = append(bodies, fmt.Sprintf("<marshal: %v>", err))
			continue
		}
		bodies = append(bodies, string(raw))
	}
	return bodies, acks
}

// queuedReplInboxContainsText reports whether any inbox entry carries the
// given text anywhere in its raw payload.
func queuedReplInboxContainsText(t *testing.T, al *AgentLoop, parentID, text string) bool {
	t.Helper()
	entries, err := al.GetMessageInboxStore().Entries(parentID)
	if err != nil {
		t.Fatalf("Entries(parent inbox): %v", err)
	}
	for _, entry := range entries {
		if entry.Kind != session.InboxEntryMessage || entry.Message == nil {
			continue
		}
		raw, err := entry.Message.MarshalJSON()
		if err != nil {
			continue
		}
		if strings.Contains(string(raw), text) {
			return true
		}
	}
	return false
}

// queuedReplDumpInbox renders every parent inbox entry (kind, message id,
// payload head) for failure diagnostics — a distinguishable failure, never a
// silent absence.
func queuedReplDumpInbox(t *testing.T, al *AgentLoop, ownerID string) string {
	t.Helper()
	entries, err := al.GetMessageInboxStore().Entries(ownerID)
	if err != nil {
		return fmt.Sprintf("<Entries(%s): %v>", ownerID, err)
	}
	if len(entries) == 0 {
		return "<empty>"
	}
	parts := make([]string, 0, len(entries))
	for _, entry := range entries {
		id, kind := "-", entry.Kind
		if entry.Message != nil {
			id = messageIDOf(*entry.Message)
		}
		parts = append(parts, fmt.Sprintf("{kind=%v id=%s}", kind, id))
	}
	return strings.Join(parts, " ")
}

// waitInboxFinalCount polls the parent inbox until exactly want entries with
// finalID exist (the publish follows the terminal commit — CRIT-001's
// commit-first ordering — so a completed record does not imply the parent
// inbox append has landed yet). Returns the observed bodies.
func waitInboxFinalCount(t *testing.T, al *AgentLoop, parentID, finalID string, want int) []string {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	var bodies []string
	for time.Now().Before(deadline) {
		bodies, _ = queuedReplInboxFinal(t, al, parentID, finalID)
		if len(bodies) == want {
			return bodies
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("parent inbox messages with id %s settled at %d, want exactly %d within the deadline; full inbox dump: %s",
		finalID, len(bodies), want, queuedReplDumpInbox(t, al, parentID))
	return nil
}

// TestExecutionIdentity_QueuedReplacement_RefusesStoppedRunLateCompletion is
// the RED core: ADR D2 lines 225-231 require the late terminal completion of
// the stopped run A to be refused under the lifecycle lock once the
// same-generation replacement B has stamped the record's execution identity —
// even while B is QUEUED and no turn is live — and CRIT-001 requires the
// refusal to leave no final outbox entry, no parent inbox message and the
// deterministic final id unused for B's own later legitimate final.
func TestExecutionIdentity_QueuedReplacement_RefusesStoppedRunLateCompletion(t *testing.T) {
	al, provider := setupQueuedReplacementHarness(t)
	lifecycle := al.GetSessionLifecycleStore()
	parentID := newTestSteeringSession(t, al, "ws-queued-replacement")

	blockerID, blockerGen := launchParkedChild(t, al, parentID, "call-queued-repl-blocker", queuedReplBlockerTask)
	aRec, aTS, aRunID := dispatchParkedRunningChild(t, al, provider, parentID, "call-queued-repl-a", queuedReplOldTask)
	childID, childGen := aRec.SessionID, aRec.Generation

	dispatchChild(t, al, blockerID, blockerGen, false)
	bRunID, bBootSeq := queueReplacementBehindBlocker(t, al, aRunID, childID, childGen, blockerID, blockerGen)

	// The delayed completion of the stopped run A, exactly as production's
	// disposeSteeredTurnResult presents it: the producing turn's immutable
	// handle through the production claim builder, a publishable final answer.
	claim := al.tsExecutionClaim(aTS, childID)
	if claim.RunID != aRunID {
		t.Fatalf("premise: the claim names run %q, want the stopped run's stamped %q", claim.RunID, aRunID)
	}
	lateErr := al.completeSteeredTurnForExecution(context.Background(), aRec, turnResult{finalContent: queuedReplOldAnswer}, nil, claim)

	// ORACLE (ADR D2 lines 225-231 + CRIT-001): the late completion must be
	// refused under the lifecycle lock. The completion call is synchronous,
	// so one reload decides; a poll would only mask the violation as a
	// timeout instead of naming it.
	after, err := lifecycle.Load(childID)
	if err != nil {
		t.Fatalf("Load(child after the late completion): %v", err)
	}
	if after.State != session.LifecycleQueued {
		t.Fatalf("ORACLE VIOLATION: the stopped run's late completion was NOT refused — state = %q, want %q "+
			"(CRIT-001: a late completion from a replaced execution cannot commit against the resumed run, even at "+
			"the same generation; D2 225-231: B's identity is stamped, so the completion must refuse while B is "+
			"queued); boundary error: %v; outbox: %+v",
			after.State, session.LifecycleQueued, lateErr, after.FinalDelivery)
	}
	if after.Generation != childGen {
		t.Errorf("generation after the late completion = %d, want %d (a refusal never moves the generation)", after.Generation, childGen)
	}
	if after.ExecutionID == nil || after.ExecutionID.RunID != bRunID || after.ExecutionID.BootSeq != bBootSeq {
		t.Errorf("execution identity after the late completion = %+v, want the replacement's untouched stamp (%q, %d)",
			after.ExecutionID, bRunID, bBootSeq)
	}
	if after.FinalDelivery != nil {
		t.Errorf("final-delivery outbox after the late completion = %+v, want nil — CRIT-001: a completion from a "+
			"replaced execution writes no outbox entry (this commit bound A's payload to commit_id %q, the QUEUED "+
			"replacement's run id)", after.FinalDelivery, after.FinalDelivery.CommitID)
	}
	finalID := fmt.Sprintf("%s:%d:final", childID, childGen)
	bodies, acks := queuedReplInboxFinal(t, al, parentID, finalID)
	if len(bodies) != 0 {
		t.Errorf("parent inbox messages with id %s = %d, want 0 — the replaced run's final must never publish; "+
			"bodies: %s; boundary error: %v", finalID, len(bodies), bodies, lateErr)
	}
	if acks != 0 {
		t.Errorf("acks of %s = %d, want 0", finalID, acks)
	}
	if queuedReplInboxContainsText(t, al, parentID, queuedReplOldAnswer) {
		t.Errorf("the stopped run's answer text reached the parent inbox — a late completion from a replaced " +
			"execution must not publish any payload (CRIT-001)")
	}

	// POSITIVE INSTRUMENT (ADR CRIT-001: the resumed run may later commit and
	// publish a new final under the unused id): arm B's answer, release the
	// blocker; the production drain promotes B, B runs and completes under
	// its own identity. This also proves the refusal above is not
	// fail-closed-by-default — the boundary still commits for the rightful
	// owner.
	provider.armChildAnswer(queuedReplResumeAnswer)
	provider.releaseBlocker()
	bDone := waitRecordState(t, lifecycle, childID, "the replacement to complete its own turn", func(rec *session.LifecycleRecord) bool {
		return rec.State == session.LifecycleCompleted
	})
	if bDone.FinalDelivery == nil || bDone.FinalDelivery.CommitID != bRunID {
		t.Errorf("replacement's committed final = %+v, want commit_id %q (the replacement's own stamped run)", bDone.FinalDelivery, bRunID)
	}
	bodies = waitInboxFinalCount(t, al, parentID, finalID, 1)
	if !strings.Contains(bodies[0], queuedReplResumeAnswer) {
		t.Errorf("the published final payload = %s, want the replacement's own answer %q", bodies[0], queuedReplResumeAnswer)
	}
	if strings.Contains(bodies[0], queuedReplOldAnswer) {
		t.Errorf("the published final carries the STOPPED run's answer (%s) — the replacement must publish only its own result", queuedReplOldAnswer)
	}
}

// TestExecutionIdentity_SameOwnerCompletion_CommitsAndPublishes is the
// negative control: with no stop and no replacement, the same production
// boundary commits the rightful owner's completion — the record lands done,
// the outbox tuple binds commit_id to the owner's own stamped run, and the
// deterministic final publishes to the parent. It proves the refusal oracle
// above is not satisfiable by a fail-closed boundary, and it must stay green
// through any fix of the queued-replacement hole.
func TestExecutionIdentity_SameOwnerCompletion_CommitsAndPublishes(t *testing.T) {
	al, provider := setupQueuedReplacementHarness(t)
	lifecycle := al.GetSessionLifecycleStore()
	parentID := newTestSteeringSession(t, al, "ws-queued-replacement-control")

	aRec, _, aRunID := dispatchParkedRunningChild(t, al, provider, parentID, "call-queued-repl-control", queuedReplOldTask)
	childID, childGen := aRec.SessionID, aRec.Generation

	// The owner's own turn finishes normally through the production dispose
	// path: the provider releases it with its final answer.
	provider.armChildAnswer(queuedReplOldAnswer)
	done := waitRecordState(t, lifecycle, childID, "the owner's own completion to commit", func(rec *session.LifecycleRecord) bool {
		return rec.State == session.LifecycleCompleted
	})
	if done.Generation != childGen {
		t.Errorf("generation after the owner's completion = %d, want %d", done.Generation, childGen)
	}
	if done.FinalDelivery == nil {
		t.Fatalf("the owner's own completion committed no outbox tuple — the boundary is not exercising its winning path")
	}
	if done.FinalDelivery.CommitID != aRunID {
		t.Errorf("commit_id = %q, want the owner's own stamped run %q", done.FinalDelivery.CommitID, aRunID)
	}
	finalID := fmt.Sprintf("%s:%d:final", childID, childGen)
	bodies := waitInboxFinalCount(t, al, parentID, finalID, 1)
	if !strings.Contains(bodies[0], queuedReplOldAnswer) {
		t.Errorf("published payload = %s, want the owner's answer %q", bodies[0], queuedReplOldAnswer)
	}
}

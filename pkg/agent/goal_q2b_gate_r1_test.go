package agent

// RED coverage for the Q2=B feature-gate round-1 BLOCK findings.
//
// Oracle sources (never the implementation):
//   - coordination/logs/fix890-opus/codex-gate-q2b-security-lead.last.md
//   - coordination/logs/fix890-opus/codex-gate-q2b-silent-failure-hunter.last.md
//   - coordination/logs/fix890-opus/codex-gate-q2b-pr-test-analyzer.last.md
//   - coordination/logs/fix890-opus/gate-q2b-type-design-analyzer.report.md
//   - coordination/logs/fix890-opus/arch-q2-design.md (Invariants, "Re-trigger
//     behavior", "Tests required")
//   - coordination/logs/fix890-opus/lc947-defect1-design-note.md (Q7=A: no
//     restart resume; the parked-child limitation — both explicitly NOT
//     findings here)
//
// Every expected value below is the reviewers' own stated invariant, never a
// value read off pkg/agent's current behaviour.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// -----------------------------------------------------------------------
// security-lead finding 1 / silent-failure-hunter: a goal restate leaves the
// old completion fence controlling the new goal revision.
// -----------------------------------------------------------------------

// TestGoalQ2B_RestateWhileWaitingDescendantsClearsCompletionFence covers the
// security-lead gate-round-1 finding 1's first half: "session C claims met
// while descendant D is active, installing waiting_descendants. The operator
// then restates C's goal ... The revised goal cannot launch needed
// descendants until D terminates."
//
// Oracle: codex-gate-q2b-security-lead.last.md finding 1 ("Required
// direction: invalidate the completion phase on every successful
// restate/supersession"). Drives the REAL production restate entry point
// (AgentLoop.applyGoalCommandPrompt), not the bare Goal.Restate method.
func TestGoalQ2B_RestateWhileWaitingDescendantsClearsCompletionFence(t *testing.T) {
	h := newQ2BHarness(t, "q2b-restate-waiting")
	q2bPendingClaimWithRunningDescendant(t, h, "q2b-restate-waiting-descendant")

	const newIntent = "the delegated worker's original scope has changed; deliver the revised report instead"
	matched, handled, reply := h.al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal " + newIntent, UserInitiated: true}, h.childTurn.agent, &h.childTurn.opts)
	if !matched || handled || reply != "" {
		t.Fatalf("restate: matched=%v handled=%v reply=%q, want a same-turn restate (true,false,\"\")", matched, handled, reply)
	}

	after := h.goalRecord()
	if after.Prompt != newIntent {
		t.Fatalf("Prompt after restate = %q, want %q", after.Prompt, newIntent)
	}
	if h.al.goalCompletionWaiting(h.child.GoalRef) {
		t.Fatal("BUG: completion phase remained waiting_descendants after a restate; the revised goal is fenced from launching new descendants until the OLD (pre-restate) descendant terminates")
	}

	before := q2bChildCount(t, h.lifecycle, h.child.SessionID)
	_, err := NewSteerLauncher(h.al).Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: h.child.SessionID,
		TargetAgentID:     "native-agent",
		Task:              "needed for the revised goal",
		Origin:            steer.Origin{Kind: steer.OriginKindDelegate, CallID: "q2b-restate-waiting-new-child"},
	})
	if err != nil {
		t.Fatalf("launch after restate = %v, want allowed — restate must clear the stale completion fence", err)
	}
	if afterCount := q2bChildCount(t, h.lifecycle, h.child.SessionID); afterCount != before+1 {
		t.Errorf("child records after allowed post-restate launch = %d, want %d", afterCount, before+1)
	}
}

// TestGoalQ2B_RestateDuringAdjudicationClearsCompletionPhaseInsteadOfWedging
// covers finding 1's second half: "The adjudicating variant also wedges: if
// a verdict is discarded because the goal was restated during Judge
// execution, runGoalAdjudication returns without clearing
// goalCompletionAdjudicating."
//
// Mirrors the existing (accepted, production) restate race
// TestGoalAdjudication_DiscardsAVerdictForARestatedGoal in
// goal_restate_supersede_test.go — this test additionally asserts the
// process-local completion-phase invariant that test never checked.
func TestGoalQ2B_RestateDuringAdjudicationClearsCompletionPhaseInsteadOfWedging(t *testing.T) {
	h := newQ2BHarness(t, "q2b-restate-adjudicating")
	const restatedIntent = "the delegated worker's scope changed; deliver the revised report instead"

	h.judge = &fakeJudgeProvider{chatFn: func(n int) (*providers.LLMResponse, error) {
		if n == 1 {
			matched, handled, reply := h.al.applyGoalCommandPrompt(context.Background(),
				bus.InboundMessage{Content: "/goal " + restatedIntent, UserInitiated: true},
				h.childTurn.agent, &h.childTurn.opts)
			if !matched || handled || reply != "" {
				t.Errorf("fixture: restate during judging: matched=%v handled=%v reply=%q", matched, handled, reply)
			}
		}
		return &providers.LLMResponse{Content: cannedJudgeVerdictJSON(true, "everything appears done")}, nil
	}}
	h.judgeInst.Provider = h.judge

	claim := h.claimMet("ready for judge review")
	work := claim.goalDeferredAdjudication
	if work == nil {
		t.Fatal("arrange: claim did not schedule adjudication")
	}
	h.al.dispatchDeferredGoalAdjudication(work)

	after := h.goalRecord()
	if after.State != generated.GoalStateActive || after.Prompt != restatedIntent {
		t.Fatalf("goal after restate-during-judging = state %q prompt %q, want active/%q", after.State, after.Prompt, restatedIntent)
	}
	if after.LatestVerdict != nil || after.Round != 0 {
		t.Fatalf("verdict recorded / round consumed for a superseded definition: verdict=%v round=%d", after.LatestVerdict, after.Round)
	}

	triggerState := goalTriggers()
	triggerState.mu.Lock()
	phase, present := triggerState.completionPhase[h.child.GoalRef]
	triggerState.mu.Unlock()
	if present && phase != goalCompletionNone {
		t.Fatalf("BUG: completion phase after a restate discarded the in-flight verdict = %v (present=%v), want cleared — the restated goal is wedged and can never claim completion again", phase, present)
	}
}

// -----------------------------------------------------------------------
// security-lead finding 2 / silent-failure-hunter finding 2: the completion
// fence fails open when the goal store is unreadable.
// -----------------------------------------------------------------------

// TestGoalQ2B_FenceFailsOpenWhenGoalStoreUnreadable covers: "If the goal
// directory read fails, activeGoalForSession treats the session as
// goal-less, so goalCompletionFenceActive returns false and publishes a new
// child." Corrupts the real goal entity directory (replaces it with a
// plain file so os.ReadDir errors with something other than os.ErrNotExist)
// rather than relying on chmod, which is unreliable under a root-run
// container (memory: docker-container-acceptance-harness).
func TestGoalQ2B_FenceFailsOpenWhenGoalStoreUnreadable(t *testing.T) {
	h := newQ2BHarness(t, "q2b-fence-store-unreadable")
	h.al.goalSetCompletionPhase(h.child.GoalRef, goalCompletionWaitingDescendants)
	if !h.al.goalPromoteCompletionToAdjudicating(h.child.GoalRef) {
		t.Fatal("arrange: could not promote completion phase to adjudicating")
	}

	goalsDir := resolveGoalRecordStore().Dir()
	if err := os.RemoveAll(goalsDir); err != nil {
		t.Fatalf("remove goals dir: %v", err)
	}
	if err := os.WriteFile(goalsDir, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("replace goals dir with a file: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Remove(goalsDir)
		_ = os.MkdirAll(goalsDir, 0o700)
	})

	before := q2bChildCount(t, h.lifecycle, h.child.SessionID)
	_, err := NewSteerLauncher(h.al).Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: h.child.SessionID,
		TargetAgentID:     "native-agent",
		Task:              "must be refused when the completion fence's authority cannot be read",
		Origin:            steer.Origin{Kind: steer.OriginKindDelegate, CallID: "q2b-fence-unreadable-store"},
	})
	if err == nil {
		t.Fatal("BUG: launch succeeded while the goal store directory was unreadable and adjudication was in flight; want fail-closed refusal (the fence must not fail open on a read error)")
	}
	if after := q2bChildCount(t, h.lifecycle, h.child.SessionID); after != before {
		t.Errorf("child records after a would-be fail-open launch = %d, want unchanged %d", after, before)
	}
}

// -----------------------------------------------------------------------
// security-lead finding 3 (Medium, Inferred): unbounded goal-directory scan
// under the parent publication lock.
// -----------------------------------------------------------------------

// q2bSeedRetainedGoals populates n unrelated goal records directly in the
// store's directory — the "many retained terminal goals accumulate"
// scenario the finding names. entity.Store.List reads and parses EVERY
// file in the directory regardless of state (goal/predicate.go's own doc
// comment: "a full List() scan filtered in memory"), so these decoys
// inflate the cost of every subsequent ListActive() call exactly like real
// retained history would.
//
// Writes the entity.Store[Goal] file shape directly (json.MarshalIndent +
// a plain file write to <dir>/<id>.json, matching entity/store.go::write's
// own format byte-for-byte) rather than calling Store.Create n times — the
// per-Create sidecar flock (pkg/entity/store.go, cross-process safety) made
// seeding itself the dominant cost at this N (a first measurement: 3000
// sequential Create calls alone took ~168s), which would have hidden the
// scan-side latency this test exists to catch.
func q2bSeedRetainedGoals(t *testing.T, n int) {
	t.Helper()
	dir := resolveGoalRecordStore().Dir()
	now := time.Now().UTC()
	dod := newFloorDoD()
	for i := 0; i < n; i++ {
		g, err := goal.New(generated.GoalOwnerKindSession, fmt.Sprintf("q2b-decoy-session-%d", i),
			generated.GoalSourceChatCompiled, "decoy retained goal", "", nil, dod, config.DefaultGoalMaxRounds, now)
		if err != nil {
			t.Fatalf("seed retained goal %d: %v", i, err)
		}
		g.GoalID = newGoalID()
		g.State = generated.GoalStateCleared
		g.TerminalReason = "session ended: the session was cancelled"
		data, merr := json.MarshalIndent(g, "", "  ")
		if merr != nil {
			t.Fatalf("marshal retained goal %d: %v", i, merr)
		}
		if werr := os.WriteFile(filepath.Join(dir, g.GoalID+".json"), data, 0o600); werr != nil {
			t.Fatalf("write retained goal %d: %v", i, werr)
		}
	}
}

// TestGoalQ2B_LaunchStaysPromptUnderManyRetainedGoals covers: "Every
// descendant launch now performs an unbounded global goal scan while
// holding the parent publication lock ... This can delay launches and
// lifecycle operations for unrelated sessions sharing that stripe."
//
// Correction recorded during RED (two false starts, both run to ground
// before writing this final version): the FIRST measurement seeded 3000
// decoys via Store.Create and saw the launch step itself blow the 2s
// budget — but that 170s total turned out to be Store.Create's own
// per-record sidecar flock (pkg/entity/store.go, cross-process safety)
// during SETUP, not the scan. Switching q2bSeedRetainedGoals to a direct
// file write (bypassing the flock) made the IDENTICAL N=3000 scan finish
// in ~6.5s total — PASS. So this is EXPECTED GREEN on today's code, exactly
// the case the elicify-test-writing skill anticipates: the finding's own
// certainty is "Inferred... its practical latency threshold has not been
// benchmarked", and at a fast, correctly-isolated N=3000 the current O(n)
// scan has no measured cost problem. It stays as a regression guard against
// a REAL future blow-up (no scan budget, deadline or index exists — the
// finding's own fix direction); failability proven by mutation in a scratch
// clone under /tmp (qa-lead RED report), never committed here.
func TestGoalQ2B_LaunchStaysPromptUnderManyRetainedGoals(t *testing.T) {
	h := newQ2BHarness(t, "q2b-retained-scan")
	q2bSeedRetainedGoals(t, 3000)

	done := make(chan error, 1)
	go func() {
		_, err := NewSteerLauncher(h.al).Launch(context.Background(), steer.LaunchRequest{
			SteeringSessionID: h.child.SessionID,
			TargetAgentID:     "native-agent",
			Task:              "must complete promptly regardless of accumulated retained goal history",
			Origin:            steer.Origin{Kind: steer.OriginKindDelegate, CallID: "q2b-retained-scan-child"},
		})
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("launch under 3000 retained goal records = %v, want success", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("launch under 3000 retained goal records did not complete within 2s — the per-launch completion-fence scan is unbounded by retained goal count (gate round 1 finding 3)")
	}
}

// -----------------------------------------------------------------------
// silent-failure-hunter finding 1: a re-evaluation queued while the
// steered-turn concurrency cap is full loses its prompt.
// -----------------------------------------------------------------------

// q2bFillAdmissionCap occupies every slot of al's steered-turn admission
// gate with synthetic filler sessions and returns their ids, so a caller can
// deterministically drive ANY steered wake into the queued branch
// regardless of the operator-configured cap value. The bound covers the
// unset-config backstop too (pkg/config/config_defaults_apply.go's
// physicalConcurrencySafetyCeiling = 2000, returned when no operator value
// is configured — as in this harness's minimal test config).
func q2bFillAdmissionCap(t *testing.T, al *AgentLoop) []string {
	t.Helper()
	gate := al.steerAdmission()
	var fillers []string
	for i := 0; i < 2100; i++ {
		id := fmt.Sprintf("q2b-cap-filler-%d", i)
		admitted, _, _ := gate.tryAdmit(id, 0)
		if !admitted {
			return fillers
		}
		fillers = append(fillers, id)
	}
	t.Fatal("arrange: admission cap did not fill within 2100 filler attempts")
	return nil
}

// TestGoalQ2B_ReevaluationQueuedAtAdmissionCapReplaysLaunchInstructionNotPrompt
// covers: "the last descendant finishes while the steered-turn concurrency
// cap is full ... dispatchSteeredSessionReserved reconstructs with
// wake=nil, causing reconstructSteeredTurn to replay the original launch
// instruction — not the fresh-claim prompt."
//
// Drives the exact production call chain named in the finding:
// processSteeredSystemWake (queued branch) then
// AgentLoop.drainSteerQueue -> dispatchSteeredSessionReserved ->
// dispatchSteeredSessionWithReservation -> reconstructSteeredTurn(rec, nil).
// turnRegisteredTestHook (steer_launcher.go, already a production test seam)
// captures the promoted turn's opts.UserMessage the instant it registers.
func TestGoalQ2B_ReevaluationQueuedAtAdmissionCapReplaysLaunchInstructionNotPrompt(t *testing.T) {
	h := newQ2BHarness(t, "q2b-cap-full")
	fillers := q2bFillAdmissionCap(t, h.al)

	msg := bus.InboundMessage{
		Channel:                  "system",
		AsyncTranscriptSessionID: h.child.SessionID,
		Content:                  q2bReevaluationPrompt,
		Metadata: map[string]string{
			"steer_message_id": "q2b-reeval-msg-1",
			"steer_generation": strconv.Itoa(h.child.Generation),
		},
	}
	if _, err := h.al.processSteeredSystemWake(context.Background(), msg); err != nil {
		t.Fatalf("processSteeredSystemWake(queued): %v", err)
	}
	queued, err := h.lifecycle.Load(h.child.SessionID)
	if err != nil {
		t.Fatalf("Load(child after queue): %v", err)
	}
	if queued.State != session.LifecycleQueued {
		t.Fatalf("arrange: child state after cap-full wake = %q, want queued", queued.State)
	}

	captured := make(chan string, 1)
	var once sync.Once
	oldHook := turnRegisteredTestHook
	turnRegisteredTestHook = func(sessionID string, ts *turnState) {
		if sessionID != h.child.SessionID {
			return
		}
		once.Do(func() { captured <- ts.opts.UserMessage })
	}
	t.Cleanup(func() { turnRegisteredTestHook = oldHook })

	h.al.drainSteerQueue(fillers[0], 0)

	select {
	case userMessage := <-captured:
		if userMessage != q2bReevaluationPrompt {
			t.Fatalf("BUG: promoted turn's user message = %q, want the queued re-evaluation prompt %q — it replayed the ORIGINAL launch instruction instead, losing the prompt", userMessage, q2bReevaluationPrompt)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the queued re-evaluation to be promoted and registered")
	}
}

// -----------------------------------------------------------------------
// pr-test-analyzer finding 1: notification recovery bypasses the keeper.
// -----------------------------------------------------------------------

// TestGoalQ2B_KeeperTickRetriesFailedReevaluationNotification covers:
// "drive a real keeper tick after the injected notification failure and
// assert exactly one successful re-evaluation." Calls the production tick
// (goal_triggers.go::AgentLoop.maybeSettleGoalIdle) instead of the manual
// resumeDeferredGoalForSession call the existing
// TestGoalQ2B_NotificationFailureRestoresWaitingAndLaterTriggerRetriesOnce
// (goal_q2b_fix_round1_test.go) makes.
//
// EXPECTED GREEN on today's code: goal_triggers.go::maybeSettleGoalIdle
// already carries the Q2 branch (goalRestoreWaitingCompletion +
// goalCompletionWaiting -> resumeDeferredGoalForSession) — this is the
// "verified coverage gap" pr-test-analyzer certainty states, not a
// production defect. Failability proven by mutation (removing that branch)
// in a scratch clone, qa-lead RED report; never committed here.
func TestGoalQ2B_KeeperTickRetriesFailedReevaluationNotification(t *testing.T) {
	h := newQ2BHarness(t, "q2b-keeper-retry")
	descendant := q2bPendingClaimWithRunningDescendant(t, h, "q2b-keeper-retry-descendant")
	if err := h.lifecycle.Mutate(descendant.SessionID, func(rec *session.LifecycleRecord) error {
		rec.State = session.LifecycleCompleted
		return nil
	}); err != nil {
		t.Fatalf("terminalise descendant without live hook: %v", err)
	}

	originalBus := h.al.bus
	h.al.bus = nil
	h.al.resumeDeferredGoalForSession(h.child.SessionID, h.child.GoalRef)
	h.al.bus = originalBus
	if !h.al.goalCompletionWaiting(h.child.GoalRef) {
		t.Fatalf("arrange: completion phase after failed notification is not waiting_descendants")
	}

	store := h.al.GetSessionStore()
	meta, err := store.GetMeta(h.child.SessionID)
	if err != nil {
		t.Fatalf("GetMeta(child): %v", err)
	}
	rec := h.goalRecord()

	h.al.maybeSettleGoalIdle(time.Now().UTC(), store, meta, rec)

	// AsyncNotifier's observer fires "independent of the publish outcome"
	// (async_notifier.go::notifyObservers's own doc comment, FR-N5) — the
	// failed arrange-step attempt is recorded too, exactly as the accepted
	// precedent TestGoalQ2B_NotificationFailureRestoresWaitingAndLaterTriggerRetriesOnce
	// (goal_q2b_fix_round1_test.go) already asserts: "one failed attempt
	// plus exactly one successful retry" = 2, never 1.
	if got := q2bCountReevaluations(h.dispatches.all(), h.child.SessionID); got != 2 {
		t.Fatalf("keeper-tick re-evaluation attempts after one failed notification = %d, want exactly 2 (one failed attempt plus one successful keeper-driven retry)", got)
	}
	if h.al.goalCompletionWaiting(h.child.GoalRef) {
		t.Fatal("completion phase remained waiting after the keeper-driven retry")
	}
	if calls := h.judge.callCount(); calls != 0 {
		t.Fatalf("Judge calls without a fresh claim = %d, want 0", calls)
	}
}

// -----------------------------------------------------------------------
// pr-test-analyzer finding 2: the defensive post-Judge quietness fence has
// no test.
// -----------------------------------------------------------------------

// TestGoalQ2B_PostJudgeQuietnessFenceFailsClosedOnReadErrorDuringAdjudication
// covers: "block the Judge, corrupt or make the lifecycle listing fail,
// release the Judge with a met verdict, then assert no verdict, round
// consumption, completion tail, message, or wake."
//
// EXPECTED GREEN on today's code, run and confirmed: PASS in 1.89s.
// deferMetWhileDescendantsActive (goal_child_completion.go) already fails
// closed on this exact read error — this is the "Verified coverage gap"
// pr-test-analyzer's own certainty states, not a live defect. Failability
// proven by mutation in a scratch clone (qa-lead RED report), never
// committed here.
func TestGoalQ2B_PostJudgeQuietnessFenceFailsClosedOnReadErrorDuringAdjudication(t *testing.T) {
	h := newQ2BHarness(t, "q2b-post-judge-fence")
	descendant := q2bLaunchDescendant(t, h, "q2b-post-judge-fence-descendant", session.LifecycleCompleted)

	entered := make(chan struct{})
	release := make(chan struct{})
	var enteredOnce sync.Once
	h.judge = &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		enteredOnce.Do(func() { close(entered) })
		<-release
		return &providers.LLMResponse{Content: metGoalVerdict947(t, h.child)}, nil
	}}
	h.judgeInst.Provider = h.judge

	claim := h.claimMet("no descendant is live yet")
	work := claim.goalDeferredAdjudication
	if work == nil {
		t.Fatal("arrange: claim did not schedule adjudication")
	}

	done := make(chan string, 1)
	oldDone := goalDeferredAdjudicationDoneFn
	goalDeferredAdjudicationDoneFn = func(sessionID string) { done <- sessionID }
	t.Cleanup(func() { goalDeferredAdjudicationDoneFn = oldDone })
	go h.al.dispatchDeferredGoalAdjudication(work)
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("Judge did not enter adjudication")
	}

	// Corrupt the descendant's lifecycle record WHILE the Judge is blocked —
	// the SECOND, post-Judge quietness fence (deferMetWhileDescendantsActive)
	// must fail closed on this read error, exactly like the pre-dispatch
	// read already does (TestGoalQ2B_QuietnessReadFailureFailsClosed).
	path := filepath.Join(h.lifecycle.Dir(), descendant.SessionID+".jsonl")
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove descendant lifecycle fixture: %v", err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatalf("replace descendant lifecycle file with unreadable directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(path) })

	close(release)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for adjudication to finish")
	}

	g := h.goalRecord()
	if g.State != generated.GoalStateActive || g.LatestVerdict != nil || g.Round != 0 {
		t.Fatalf("goal after post-Judge quietness read failure = state %q verdict %+v round %d, want active/no verdict/round 0", g.State, g.LatestVerdict, g.Round)
	}
	if calls := h.judge.callCount(); calls != 1 {
		t.Fatalf("Judge calls = %d, want exactly 1 (the blocked call itself; no verdict may land from it)", calls)
	}
	if messages := h.parentMessages(); len(messages) != 0 {
		t.Errorf("parent messages after a discarded met verdict = %d, want 0", len(messages))
	}
	if wakes := h.parentWakeEvents(); len(wakes) != 0 {
		t.Errorf("parent wake events after a discarded met verdict = %d, want 0", len(wakes))
	}
	if !h.al.goalCompletionWaiting(h.child.GoalRef) {
		t.Error("completion phase after the post-Judge read failure is not waiting_descendants; want held for a later re-evaluation")
	}
}

// -----------------------------------------------------------------------
// pr-test-analyzer finding 3: boot recovery is not tested through
// production wiring or real boot ordering.
// -----------------------------------------------------------------------

// TestGoalQ2B_BootRunFailsPendingOwnerInterruptedThroughRealSweep covers the
// "no Q2 test calls Run" half of the finding: drives the real
// SteerBootRecovery.Run entry point (real sessionIDs() scan, real sort
// order) instead of the existing TestGoalQ2B_RestartFailsPendingOwnerAsInterrupted's
// manually-ordered recoverSteered(owner) then recoverSteered(descendant)
// calls.
//
// NOTE on the finding's OTHER half ("boot processes descendant D before its
// pending goal owner C"): session ids are ULID-timestamped
// (session.NewSessionID), and this harness creates the owner strictly
// before its descendant, so their ids are monotonically ordered in this
// process and Run()'s lexicographic sort can never place D before C here —
// forcing that exact adversarial order is not constructible without a new
// test seam. Missing seam, named per RED rule 4: SteerBootRecovery
// (pkg/agent/boot_sweep.go) has no way to override sessionIDs()'s scan
// order; an injectable `idsOverride func() ([]string, error)` field (test-
// only, mirroring goalDeferredAdjudicationDoneFn's pattern) would let a
// test force descendant-before-owner processing deterministically. Reported
// to team-lead as a finding, not built here (RED never adds production
// code).
//
// Run and confirmed: PASS in 1.39s for the realistic (owner-first) order —
// this test closes the literal coverage gap without claiming to reproduce
// the adversarial ordering, which remains untested pending the seam above.
func TestGoalQ2B_BootRunFailsPendingOwnerInterruptedThroughRealSweep(t *testing.T) {
	h := newQ2BHarness(t, "q2b-boot-run-owner-first")
	q2bPendingClaimWithRunningDescendant(t, h, "q2b-boot-run-owner-first-descendant")

	recovery := q2bBootRecoveryForHarness(h)
	if err := recovery.Run(context.Background()); err != nil {
		t.Fatalf("Run(): %v", err)
	}

	child, err := h.lifecycle.Load(h.child.SessionID)
	if err != nil {
		t.Fatalf("Load(pending owner after Run): %v", err)
	}
	if child.State != session.LifecycleFailed || child.FailedReason != "interrupted" {
		t.Errorf("pending owner after Run() = state %q reason %q, want failed/interrupted", child.State, child.FailedReason)
	}
	endedGoal := h.goalRecord()
	if endedGoal.State != generated.GoalStateCleared {
		t.Errorf("pending owner's goal after Run() = %q, want cleared", endedGoal.State)
	}
	if calls := h.judge.callCount(); calls != 0 {
		t.Errorf("Judge calls after Run() = %d, want 0", calls)
	}
	if got := q2bCountReevaluations(h.dispatches.all(), h.child.SessionID); got != 0 {
		t.Errorf("re-evaluations dispatched by Run() = %d, want 0", got)
	}
	for _, wake := range h.parentWakeEvents() {
		if wake.SourceKind == "message_parent:handback" {
			t.Errorf("completion handback reached the parent after Run(): %+v", wake)
		}
	}
}

// -----------------------------------------------------------------------
// type-design-analyzer finding 1: a second claim must not downgrade an
// Adjudicating phase; install-and-promote must be atomic.
// -----------------------------------------------------------------------

// TestGoalQ2B_SecondClaimBeforeDispatchMustNotDowngradeAdjudicatingPhase
// covers: "goal_loop.go::handleOutcome unconditionally
// goalSetCompletionPhase(..., WaitingDescendants) then
// goalPromoteCompletionToAdjudicating whose CAS precondition is that same
// value — two separate s.mu sections; a second claim ... overwrites an
// Adjudicating phase and the CAS always succeeds."
//
// Drives the real call site (handleOutcome via h.claimMet) twice in a row,
// before the first claim's Judge goroutine is ever dispatched — at that
// point goalAdjudicationInFlight (a SEPARATE verifier-registry check) is
// still false, so the second claim reaches the exact
// goalSetCompletionPhase+goalPromoteCompletionToAdjudicating sequence the
// finding names.
func TestGoalQ2B_SecondClaimBeforeDispatchMustNotDowngradeAdjudicatingPhase(t *testing.T) {
	h := newQ2BHarness(t, "q2b-no-downgrade")

	first := h.claimMet("first claim reaches the judge")
	if first.goalDeferredAdjudication == nil {
		t.Fatal("arrange: first claim did not schedule adjudication")
	}
	if h.al.goalAdjudicationInFlight(h.child.SessionID) {
		t.Fatal("arrange: adjudication already registered in-flight before dispatch; this test targets the window where it is NOT registered yet")
	}
	triggerState := goalTriggers()
	triggerState.mu.Lock()
	phaseAfterFirst := triggerState.completionPhase[h.child.GoalRef]
	triggerState.mu.Unlock()
	if phaseAfterFirst != goalCompletionAdjudicating {
		t.Fatalf("arrange: phase after first claim = %v, want adjudicating", phaseAfterFirst)
	}

	second := h.claimMet("second claim races the still-undispatched first")

	triggerState.mu.Lock()
	phaseAfterSecond := triggerState.completionPhase[h.child.GoalRef]
	triggerState.mu.Unlock()
	if phaseAfterSecond != goalCompletionAdjudicating {
		t.Errorf("phase after a second claim raced an undispatched first = %v, want left at adjudicating (never downgraded)", phaseAfterSecond)
	}
	if second.goalDeferredAdjudication != nil {
		t.Error("BUG: second claim scheduled its OWN adjudication work while the first was already promoted and undispatched — install-and-promote is not atomic, so the CAS always succeeds for a racing second claim")
	}
}

// -----------------------------------------------------------------------
// type-design-analyzer finding 3: redriveGoalAdjudication's store==nil
// early return leaves completionPhase stuck at Adjudicating.
// -----------------------------------------------------------------------

// TestGoalQ2B_RedriveWithNoSessionStoreClearsPhaseInsteadOfStranding covers:
// "goal_child_completion.go::redriveGoalAdjudication store==nil early
// return leaves completionPhase stuck at Adjudicating (no clear, no
// tail)."
func TestGoalQ2B_RedriveWithNoSessionStoreClearsPhaseInsteadOfStranding(t *testing.T) {
	h := newQ2BHarness(t, "q2b-redrive-no-store")
	claim := h.claimMet("ready for judge review")
	work := claim.goalDeferredAdjudication
	if work == nil {
		t.Fatal("arrange: claim did not schedule adjudication")
	}
	triggerState := goalTriggers()
	triggerState.mu.Lock()
	phaseBefore := triggerState.completionPhase[h.child.GoalRef]
	triggerState.mu.Unlock()
	if phaseBefore != goalCompletionAdjudicating {
		t.Fatalf("arrange: phase before redrive = %v, want adjudicating", phaseBefore)
	}

	originalStore := h.al.sharedSessionStore
	h.al.sharedSessionStore = nil
	t.Cleanup(func() { h.al.sharedSessionStore = originalStore })

	h.al.redriveGoalAdjudication(work)

	triggerState.mu.Lock()
	phaseAfter, present := triggerState.completionPhase[h.child.GoalRef]
	triggerState.mu.Unlock()
	if present && phaseAfter == goalCompletionAdjudicating {
		t.Fatalf("BUG: completion phase after a no-session-store redrive = %v (present=%v), want cleared — a goal stuck at adjudicating can never claim completion or launch a new descendant again", phaseAfter, present)
	}
	if calls := h.judge.callCount(); calls != 0 {
		t.Errorf("Judge calls with no session store = %d, want 0", calls)
	}
}

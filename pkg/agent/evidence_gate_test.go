// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// evidence_gate_test.go covers ADR-052 FR-035 (R3-13): the evidence-marker
// gate wired into TaskExecutor.finishTaskRun's claim-acceptance path, right
// before parseTaskCompletionSignal — a completion claim (a TASK_STATUS
// marker line, success or failure) with no genuine [goal:evidence] line
// immediately preceding it is rejected and the worker is re-prompted BEFORE
// any verifier dispatch, WITHOUT consuming an attempt (rejectBareEvidenceClaim,
// task_executor.go) — distinct from the "no signal at all" and judge-"unmet"
// paths, both of which DO consume an attempt via consumeAttemptOrExhaust.
package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// TestEvidenceGate_BareClaimRejectedWithoutConsumingAttempt proves the core
// FR-035 contract: a completion claim with a TASK_STATUS marker but NO
// preceding [goal:evidence] line is rejected pre-judge (the verifier is
// NEVER dispatched — fake judge provider fails the test if called), the
// worker is re-prompted (a non-empty redispatch id comes back, and the
// re-prompt text names the evidence-marker requirement), and — the
// attempt-semantics half of this wave's brief — AttemptCount stays at 0:
// this is a mechanical formatting miss, not a genuine work-verification
// failure, so it must not cost the worker a real attempt.
func TestEvidenceGate_BareClaimRejectedWithoutConsumingAttempt(t *testing.T) {
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	fake := &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		t.Fatal("the verifier must NEVER be dispatched for a bare (evidence-free) completion claim")
		return nil, nil
	}}
	judgeInst.Provider = fake

	taskStore := GetTaskStore(al)
	tk := &task.Task{
		ID: "t-evidence-bare", AgentID: "native-agent", WorkspaceID: "test-ws", Title: "evidence gate bare claim",
		Status:   task.StatusInProgress,
		Criteria: []task.AcceptanceCriterion{proseCriterion("c1", "must do X")},
	}
	if err := taskStore.Create(tk); err != nil {
		t.Fatalf("create task: %v", err)
	}

	resp := "I finished the work.\nTASK_STATUS: success\n"
	redispatch := al.taskExecutor.finishTaskRun(context.Background(), tk, "", resp, nil, "", nil)

	if redispatch == "" {
		t.Fatal("expected a non-empty re-dispatch id — a bare claim must be actively re-prompted, not silently dropped")
	}
	if redispatch != tk.ID {
		t.Errorf("redispatch id = %q, want %q", redispatch, tk.ID)
	}

	final, err := taskStore.Get(tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.AttemptCount != 0 {
		t.Errorf("attempt_count = %d, want 0 — a bare evidence-marker claim must NOT consume an attempt (FR-035)",
			final.AttemptCount)
	}
	if final.Status != task.StatusNext {
		t.Errorf("status = %q, want %q (re-dispatchable)", final.Status, task.StatusNext)
	}
	if !strings.Contains(final.Result, "goal:evidence") {
		t.Errorf("result = %q, want it to name the [goal:evidence] requirement (the re-prompt steering text)",
			final.Result)
	}
	if !strings.Contains(final.Result, "TASK_STATUS") {
		t.Errorf("result = %q, want it to reference TASK_STATUS (one marker family, not a second protocol)",
			final.Result)
	}
}

// TestEvidenceGate_FailureClaimAlsoRequiresEvidence proves the gate applies
// uniformly to a FAILURE marker too (checkEvidenceMarkerGate's own doc
// comment: "success OR failure — the gate does not care which") — a bare
// give-up claim is re-prompted exactly like a bare success claim, never
// reaching the immediate-terminal SD-B1 failure path.
func TestEvidenceGate_FailureClaimAlsoRequiresEvidence(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)

	taskStore := GetTaskStore(al)
	tk := &task.Task{
		ID: "t-evidence-bare-failure", AgentID: "native-agent", WorkspaceID: "test-ws",
		Title:    "evidence gate bare failure claim",
		Status:   task.StatusInProgress,
		Criteria: []task.AcceptanceCriterion{proseCriterion("c1", "must do X")},
	}
	if err := taskStore.Create(tk); err != nil {
		t.Fatalf("create task: %v", err)
	}

	resp := "I could not finish.\nTASK_STATUS: failure\n"
	redispatch := al.taskExecutor.finishTaskRun(context.Background(), tk, "", resp, nil, "", nil)

	if redispatch == "" {
		t.Fatal("expected a non-empty re-dispatch id for a bare failure claim too")
	}
	final, err := taskStore.Get(tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status == task.StatusFailed {
		t.Error("a bare failure claim must NOT be accepted as the immediate SD-B1 terminal give-up — " +
			"it must be re-prompted for evidence first")
	}
	if final.AttemptCount != 0 {
		t.Errorf("attempt_count = %d, want 0", final.AttemptCount)
	}
}

// TestEvidenceGate_ScratchpadTaskExempt proves FR-048's scratchpad
// exemption composes correctly with the NEW evidence gate: a Scratchpad
// task's marker is trusted directly even with no [goal:evidence] line —
// scratchpad tasks never reach the judge/verifier either way, so the gate
// has nothing to protect and must not add friction there.
func TestEvidenceGate_ScratchpadTaskExempt(t *testing.T) {
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	fake := &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		t.Fatal("a Scratchpad task must never dispatch the verifier")
		return nil, nil
	}}
	judgeInst.Provider = fake

	taskStore := GetTaskStore(al)
	tk := &task.Task{
		ID: "t-evidence-scratchpad", AgentID: "native-agent", WorkspaceID: "test-ws",
		Title:      "scratchpad exemption",
		Status:     task.StatusInProgress,
		Scratchpad: true,
	}
	if err := taskStore.Create(tk); err != nil {
		t.Fatalf("create task: %v", err)
	}

	resp := "noted.\nTASK_STATUS: success\n"
	redispatch := al.taskExecutor.finishTaskRun(context.Background(), tk, "", resp, nil, "", nil)
	if redispatch != "" {
		t.Errorf("redispatch = %q, want \"\" — a Scratchpad claim is trusted directly, no re-prompt loop", redispatch)
	}
	final, err := taskStore.Get(tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != task.StatusDone {
		t.Errorf("status = %q, want done (trusted directly)", final.Status)
	}
}

// TestEvidenceGate_ClaimWithGenuineEvidenceReachesVerifier proves the
// contrasting happy path: a completion claim WITH a genuine, non-empty
// [goal:evidence] line immediately before the TASK_STATUS marker is NOT
// rejected — it proceeds to parseTaskCompletionSignal and on to
// adjudicateClaim, actually reaching the verifier (proving the gate is a
// real pre-check, not an always-reject stub) and resolving to done on a met
// verdict.
func TestEvidenceGate_ClaimWithGenuineEvidenceReachesVerifier(t *testing.T) {
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	called := false
	fake := &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		called = true
		return &providers.LLMResponse{
			Content: `{"met": true, "criteria": [{"id":"c1","met":true,"reason":"ok"}]}`,
		}, nil
	}}
	judgeInst.Provider = fake

	taskStore := GetTaskStore(al)
	tk := &task.Task{
		ID: "t-evidence-ok", AgentID: "native-agent", WorkspaceID: "test-ws", Title: "evidence gate happy path",
		Status:   task.StatusInProgress,
		Criteria: []task.AcceptanceCriterion{proseCriterion("c1", "must do X")},
	}
	if err := taskStore.Create(tk); err != nil {
		t.Fatalf("create task: %v", err)
	}

	resp := "I verified the output against the spec.\n" +
		"[goal:evidence] compared output to acceptance criterion c1, matches\n" +
		"TASK_STATUS: success\n"
	redispatch := al.taskExecutor.finishTaskRun(context.Background(), tk, "", resp, nil, "", nil)

	if !called {
		t.Fatal("a claim WITH a genuine evidence marker must reach the verifier")
	}
	if redispatch != "" {
		t.Errorf("redispatch = %q, want \"\" (the verifier found the criterion met; task should complete)", redispatch)
	}
	final, err := taskStore.Get(tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != task.StatusDone {
		t.Errorf("status = %q, want done", final.Status)
	}
	if final.AttemptCount != 0 {
		t.Errorf("attempt_count = %d, want 0 (a MET verdict never consumes an attempt)", final.AttemptCount)
	}
}

// TestEvidenceGate_SteeringDeliveredToRedispatchedPrompt is Fix-Wave-2's
// fix 2 regression proof: the steering text rejectBareEvidenceClaim computes
// for a bare claim must actually reach the worker on the VERY NEXT dispatch.
// Before this fix it did not: rejectBareEvidenceClaim wrote the steering to
// t.Result, but buildPrompt only renders t.Result inside its "AttemptCount >
// 0" block — and rejectBareEvidenceClaim deliberately never increments
// AttemptCount (a mechanical formatting miss must not cost a real attempt),
// so that block's guard was never true for this path. The transcript-only
// write was ALSO orphaned: createTaskSessionSync mints a fresh session on
// every re-dispatch, so the entry appended to the OLD session was never seen
// again either. Net effect pre-fix: the re-dispatched prompt was
// byte-identical to the first attempt — a deterministic livelock. This test
// proves buildPrompt's output for the re-dispatched task now contains the
// gate's steering text.
func TestEvidenceGate_SteeringDeliveredToRedispatchedPrompt(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)

	taskStore := GetTaskStore(al)
	tk := &task.Task{
		ID: "t-evidence-steering-delivery", AgentID: "native-agent", WorkspaceID: "test-ws",
		Title:    "steering delivery",
		Status:   task.StatusInProgress,
		Criteria: []task.AcceptanceCriterion{proseCriterion("c1", "must do X")},
	}
	if err := taskStore.Create(tk); err != nil {
		t.Fatalf("create task: %v", err)
	}

	resp := "I finished the work.\nTASK_STATUS: success\n"
	redispatch := al.taskExecutor.finishTaskRun(context.Background(), tk, "", resp, nil, "", nil)
	if redispatch == "" {
		t.Fatal("expected a non-empty re-dispatch id")
	}

	redispatched, err := taskStore.Get(redispatch)
	if err != nil {
		t.Fatalf("get redispatched task: %v", err)
	}
	prompt := al.taskExecutor.buildPrompt(redispatched)
	if !strings.Contains(prompt, evidenceGateSteeringText) {
		t.Fatalf("re-dispatched prompt does not contain the evidence-marker gate's steering text — the "+
			"worker would see the SAME prompt as attempt 1 and repeat the same mistake forever:\n%s", prompt)
	}
}

// TestEvidenceGate_NeverEmittedMarker_TerminatesWithinBudget is Fix-Wave-2's
// fix 3 livelock-bound regression proof: a worker that NEVER emits
// [goal:evidence] — whatever it is claiming — must still reach a terminal
// task state within the attempt/hardCeiling budget, not loop the LLM forever
// with AttemptCount frozen at 0. Before this fix, rejectBareEvidenceClaim
// always took the free (non-attempt-consuming) re-dispatch path with no
// ceiling of its own, bypassing consumeAttemptOrExhaust entirely — this test
// would time out (or loop until the process is killed) waiting for a
// terminal status that never arrives. max_attempts=1 keeps the dispatch
// count small and the test fast: dispatch 1 is the FREE first rejection
// (streak=1, AttemptCount stays 0); dispatch 2 hits
// evidenceGateMaxConsecutiveRejections (streak=2) and routes through
// consumeAttemptOrExhaust, which exhausts immediately (newAttempt=1 is not <
// maxAttempts=1) and marks the task Failed.
//
// Deadline sizing: this used to be a HARD 2s bound, deliberately shorter than
// waitForCompletionContractTerminal's 5s default, on the theory that the
// test's whole point is proving the loop does NOT run away and so it should
// fail fast if the bound regresses. In practice the two real dispatches this
// path performs go through the full session-create + transcript-append file
// I/O path (fsync per write), and were observed passing at 1.98s against
// that 2s bound on ordinary (non-isolated) CI hardware — a ~1% margin, i.e.
// a coin flip, not a deadline. Reproduced failing at exactly this assertion
// under package-suite load (see pkg/agent's flaky-suite root-cause report).
// 10s preserves the "must not run away" intent — an actual livelock
// regression re-dispatches through the same real file-I/O path on EVERY
// retry with no bound at all (the pre-fix behavior this test guards against;
// see its own history: "would time out (or loop until the process is
// killed)"), so it blows well past 10s just as reliably as it blew past 2s —
// while giving the two-real-dispatch happy path roughly 5x the margin
// observed necessary, instead of a margin so thin normal jitter trips it.
func TestEvidenceGate_NeverEmittedMarker_TerminatesWithinBudget(t *testing.T) {
	worker := &scriptedProvider{
		responseBody: "did the work\nTASK_STATUS: success\nTASK_SUMMARY: I finished it.",
	}
	al, _ := newGoalLoopTestLoop(t, worker, nil)

	maxAttempts := 1
	tk := &task.Task{
		Title: "never emits evidence marker", Prompt: "do it", Action: task.ActionLLM,
		AgentID: "native-agent", Priority: 3, WorkspaceID: "default", Status: task.StatusNext,
		MaxAttempts: &maxAttempts,
		Criteria:    []task.AcceptanceCriterion{proseCriterion("c1", "the work is really done")},
	}
	if err := al.taskStore.Create(tk); err != nil {
		t.Fatalf("create task: %v", err)
	}

	if err := al.taskExecutor.ExecuteTask(context.Background(), tk.ID, nil); err != nil {
		t.Fatalf("ExecuteTask: %v", err)
	}

	deadline := time.Now().Add(10 * time.Second)
	var final *task.Task
	for time.Now().Before(deadline) {
		got, err := al.taskStore.Get(tk.ID)
		if err != nil {
			t.Fatalf("get task: %v", err)
		}
		if task.IsTerminal(got.Status) {
			final = got
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if final == nil {
		t.Fatal("task never reached a terminal status within 10s — the evidence-marker gate is looping " +
			"without a bound (livelock)")
	}
	if final.Status != task.StatusFailed {
		t.Errorf("status = %q, want %q — a worker that never emits [goal:evidence] must fail out, not "+
			"succeed by exhausting the free ride", final.Status, task.StatusFailed)
	}
	if final.AttemptCount != 1 {
		t.Errorf("attempt_count = %d, want 1 — exactly one real attempt should have been consumed once "+
			"the consecutive-rejection bound tripped", final.AttemptCount)
	}
	worker.mu.Lock()
	dispatches := worker.callCount
	worker.mu.Unlock()
	if dispatches != 2 {
		t.Errorf("worker dispatched %d times, want exactly 2 (1 free rejection + 1 bound-triggered "+
			"attempt before exhaustion) — an unbounded count would mean the fix regressed", dispatches)
	}
}

// TestEvidenceGate_ConsecutiveRejectionsRouteThroughAttemptBudgetOnSecond
// (formerly "NeverCollideOnTranscriptID") pins Fix-Wave-2's fix 3 boundary
// precisely: the FIRST bare-claim rejection for a task is free (no
// AttemptCount change, an "evidence-gate"-tagged transcript entry using a
// nanosecond-timestamped id distinct from writeSteeringPrompt's own
// AttemptCount-derived scheme) — but the SECOND CONSECUTIVE bare-claim
// rejection hits evidenceGateMaxConsecutiveRejections and routes through
// consumeAttemptOrExhaust instead of taking another free ride: AttemptCount
// increments, and the resulting steering transcript entry uses
// writeSteeringPrompt's OWN `<task>-steering-<AttemptCount+1>` id scheme,
// never an "evidence-gate"-tagged one. Before this fix, BOTH consecutive
// calls took the free path forever (the original form of this test asserted
// AttemptCount stayed 0 and found 2 "evidence-gate" entries) — this test now
// pins the point where that free ride stops, and that the two id schemes
// still never collide with each other.
func TestEvidenceGate_ConsecutiveRejectionsRouteThroughAttemptBudgetOnSecond(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)

	taskStore := GetTaskStore(al)
	tk := &task.Task{
		ID: "t-evidence-repeat", AgentID: "native-agent", WorkspaceID: "test-ws",
		Title:    "repeated bare claim",
		Status:   task.StatusInProgress,
		Criteria: []task.AcceptanceCriterion{proseCriterion("c1", "must do X")},
	}
	if err := taskStore.Create(tk); err != nil {
		t.Fatalf("create task: %v", err)
	}

	sessStore := al.GetAgentStore("native-agent")
	if sessStore == nil {
		t.Fatal("native-agent session store not available")
	}
	meta, err := sessStore.NewSession(session.SessionTypeTask, "system", "native-agent")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	taskSessionID := meta.ID

	resp := "still working.\nTASK_STATUS: success\n"
	for i := 0; i < 2; i++ {
		if i > 0 {
			// ADR-052 FR-014/§6.4(b) TOCTOU fix: rejectBareEvidenceClaim's
			// free-retry write is now a CAS expecting in_progress (the SAME
			// guard consumeAttemptOrExhaust's write uses) — mirror what the
			// REAL dispatch cycle does between re-dispatch attempts
			// (ExecuteTask -> ClaimForRun, next -> in_progress) rather than
			// calling finishTaskRun a second time against a task this test
			// left at `next` (attempt 0's free-retry write, which itself
			// only succeeds because the task WAS genuinely in_progress at
			// that point). Skipping this claim would make attempt 1 look,
			// from the store's perspective, indistinguishable from a
			// concurrent Stop having landed — which it is not.
			if _, claimErr := taskStore.ClaimForRun(tk.ID, time.Now()); claimErr != nil {
				t.Fatalf("re-claim before attempt %d: %v", i, claimErr)
			}
		}
		current, gerr := taskStore.Get(tk.ID)
		if gerr != nil {
			t.Fatalf("re-read task before attempt %d: %v", i, gerr)
		}
		if redispatch := al.taskExecutor.finishTaskRun(
			context.Background(), current, taskSessionID, resp, nil, "", nil,
		); redispatch == "" {
			t.Fatalf("attempt %d: expected a re-dispatch id", i)
		}
	}

	final, err := taskStore.Get(tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.AttemptCount != 1 {
		t.Errorf("attempt_count = %d, want 1 — the SECOND consecutive bare claim must consume a real "+
			"attempt via consumeAttemptOrExhaust, not ride free again", final.AttemptCount)
	}

	entries, terr := sessStore.ReadTranscript(taskSessionID)
	if terr != nil {
		t.Fatalf("ReadTranscript: %v", terr)
	}
	var evidenceGateIDs, steeringIDs []string
	for _, e := range entries {
		switch {
		case strings.Contains(e.ID, "evidence-gate"):
			evidenceGateIDs = append(evidenceGateIDs, e.ID)
		case strings.Contains(e.ID, "-steering-"):
			steeringIDs = append(steeringIDs, e.ID)
		}
	}
	if len(evidenceGateIDs) != 1 {
		t.Errorf("found %d evidence-gate-tagged transcript entries, want exactly 1 (only the FIRST, free "+
			"rejection uses this id scheme): %v", len(evidenceGateIDs), evidenceGateIDs)
	}
	if len(steeringIDs) != 1 {
		t.Errorf("found %d steering-tagged transcript entries, want exactly 1 (the SECOND, bound-triggered "+
			"rejection goes through consumeAttemptOrExhaust's writeSteeringPrompt instead): %v",
			len(steeringIDs), steeringIDs)
	}
	if len(evidenceGateIDs) == 1 && len(steeringIDs) == 1 && evidenceGateIDs[0] == steeringIDs[0] {
		t.Errorf("the free-rejection and bound-triggered steering entries share the SAME id %q — collision",
			evidenceGateIDs[0])
	}
}

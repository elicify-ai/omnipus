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
	redispatch := al.taskExecutor.finishTaskRun(context.Background(), tk, "", resp, nil, "")

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
	redispatch := al.taskExecutor.finishTaskRun(context.Background(), tk, "", resp, nil, "")

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
	redispatch := al.taskExecutor.finishTaskRun(context.Background(), tk, "", resp, nil, "")
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
	redispatch := al.taskExecutor.finishTaskRun(context.Background(), tk, "", resp, nil, "")

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

// TestEvidenceGate_RepeatedBareClaimsNeverCollideOnTranscriptID guards
// rejectBareEvidenceClaim's own documented risk: since AttemptCount is
// deliberately left unchanged across repeated bare-claim rejections, a
// transcript entry id NAIVELY derived from AttemptCount (mirroring
// writeSteeringPrompt's `<task>-steering-<AttemptCount+1>` scheme) would
// collide across two such rejections in a row — both would compute the
// SAME id, since AttemptCount stays 0 both times. Calling finishTaskRun
// twice in a row against a real, pre-created session and reading the
// transcript back proves the two evidence-gate steering entries got
// DISTINCT ids.
func TestEvidenceGate_RepeatedBareClaimsNeverCollideOnTranscriptID(t *testing.T) {
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
		current, gerr := taskStore.Get(tk.ID)
		if gerr != nil {
			t.Fatalf("re-read task before attempt %d: %v", i, gerr)
		}
		if redispatch := al.taskExecutor.finishTaskRun(
			context.Background(), current, taskSessionID, resp, nil, "",
		); redispatch == "" {
			t.Fatalf("attempt %d: expected a re-dispatch id", i)
		}
	}

	final, err := taskStore.Get(tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.AttemptCount != 0 {
		t.Errorf("attempt_count = %d, want 0 after two repeated bare-claim rejections", final.AttemptCount)
	}

	entries, terr := sessStore.ReadTranscript(taskSessionID)
	if terr != nil {
		t.Fatalf("ReadTranscript: %v", terr)
	}
	var steeringIDs []string
	for _, e := range entries {
		if strings.Contains(e.ID, "evidence-gate") {
			steeringIDs = append(steeringIDs, e.ID)
		}
	}
	if len(steeringIDs) != 2 {
		t.Fatalf("found %d evidence-gate steering transcript entries, want 2: %v", len(steeringIDs), steeringIDs)
	}
	if steeringIDs[0] == steeringIDs[1] {
		t.Errorf("the two evidence-gate steering entries share the SAME id %q — collision", steeringIDs[0])
	}
}

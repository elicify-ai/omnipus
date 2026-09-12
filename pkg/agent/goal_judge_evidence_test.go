// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// goal_judge_evidence_test.go covers the two defects the
// Conformance_t0_ChatGoalE2E conformance test caught on 2026-09-12, both of
// which made a chat `/goal` structurally impossible to complete:
//
//	F1 — the prose Judge's evidence bundle was EMPTY for a goal-scope
//	     adjudication. The machine-check result never reached it (the evidence
//	     record was dropped whenever there was no TaskID, and a goal-scope
//	     input can never carry one), and the transcript window rendered tool
//	     calls as bare "[tool_call] <name> -> <status>" lines with no output.
//	     The built-in floor DoD items ("No secrets or credentials appear in
//	     the output", "Every factual claim is grounded, not assumed" — literals
//	     from goal_compile.go's newFloorDoD, authorized by ADR-080's D-DOD
//	     layer 3) were then unprovable, the Judge correctly fail-closed them,
//	     and the goal could never be met. The worker's only remaining route to
//	     a MET verdict was to MANUFACTURE evidence — which is exactly what the
//	     git-evidence sandbox denies.
//
//	F2 — a goal-bearing session could be wedged `active` forever with no
//	     terminal state, because the idleSettling marker is cleared only by
//	     bumpGoalActivityOnTurn and two live paths never reach it.
//
// Every assertion here is written against the ENGINE'S OWN OUTPUT (what the
// verifier was actually shown, what the bus actually carried), not against a
// verdict the fake provider was told to return — a verdict-only assertion
// cannot see either defect.
package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// noMachineEvidenceMarker is the literal buildJudgeUserContent writes when the
// evidence slice is empty. Its presence in a goal adjudication whose machine
// check just PASSED is the F1 defect, verbatim.
const noMachineEvidenceMarker = "(no machine-check results on this attempt)"

// The two redaction markers windowEvidenceRedactor can leave behind, asserted
// literally because "a secret is absent" is also satisfied by a redactor that
// deletes silently — and a silent deletion reads to the Judge as a clean
// output, which is the opposite of what the no-secrets floor item needs.
const (
	registeredSecretMarker = "[FILTERED]" // config.SensitiveDataReplacer (registered values)
	patternSecretMarker    = "[REDACTED]" // audit.Redactor (SEC-16 secret shapes)
)

// judgeProviderRecording returns a fakeJudgeProvider that meets criterionID
// and records the prompt it was shown (fakeJudgeProvider.promptText).
func judgeProviderRecording(criterionID string) *fakeJudgeProvider {
	return &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return &providers.LLMResponse{
			Content: `{"met": true, "criteria": [{"id":"` + criterionID +
				`","met":true,"reason":"grounded in the machine-check result"}]}`,
		}, nil
	}}
}

// === F1: the goal-scope evidence bundle ==================================

// TestJudgeEvidence_GoalScope_MachineCheckResultReachesTheVerifier is the
// direct regression for the CI failure. A chat `/goal [check: true exit:0]`
// compiles to one machine criterion plus the floor DoD prose criteria; the
// machine criterion passes; the prose Judge must SEE that it passed.
//
// Oracle: a goal-scope JudgeCriteriaInput can never carry a TaskID —
// JudgeCriteriaInput.validate() rejects one outright ("scope %q must not also
// carry TaskID/PlanID"). So the old `if taskID == "" { return nil }` in the
// evidence wrapper was not an edge case for goals, it was the ONLY case: 100%
// of chat goals reached the verifier with an empty machine-check section.
func TestJudgeEvidence_GoalScope_MachineCheckResultReachesTheVerifier(t *testing.T) {
	resetGoalTriggerStateForTest()
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	workerInst, ok := al.GetRegistry().GetAgent("native-agent")
	if !ok {
		t.Fatal("native-agent not found")
	}
	workerInst.Tools.RegisterReplacing(&fakeBashTool{result: &tools.ToolResult{
		ForLLM: "\n[Command exited with code 0]",
	}})
	allowBashPolicy(workerInst)

	jp := judgeProviderRecording("dod-grounded")
	judgeInst.Provider = jp

	_, sid := newGoalTestSession(t, al, workerInst.ID)

	result := al.JudgeCriteria(context.Background(), JudgeCriteriaInput{
		Scope:           task.VerdictScopeGoal,
		GoalSessionID:   sid,
		AssigneeAgentID: workerInst.ID,
		Attempt:         1,
		Criteria: []task.AcceptanceCriterion{
			machineCriterion("goal-check-true", "true", 0),
			proseCriterion("dod-grounded", "Every factual claim is grounded, not assumed."),
		},
	})
	if result.Unavailable {
		t.Fatalf("goal adjudication unexpectedly Unavailable: %s", result.Reason)
	}
	if jp.callCount() == 0 {
		t.Fatal("the prose verifier was never dispatched; nothing to assert about its evidence bundle")
	}

	prompt := jp.promptText()
	if strings.Contains(prompt, noMachineEvidenceMarker) {
		t.Fatalf("F1 REGRESSION: the verifier was told %q for a goal whose machine check "+
			"had just PASSED — the floor DoD prose criteria are unprovable from that bundle "+
			"and the goal can never be met.\n\nprompt:\n%s", noMachineEvidenceMarker, prompt)
	}
	// The evidence must be the REAL result, not merely a non-empty section.
	for _, want := range []string{"goal-check-true", `"command": "true"`, `"exit_code": 0`} {
		if !strings.Contains(prompt, want) {
			t.Errorf("verifier prompt is missing machine-check evidence %q\n\nprompt:\n%s", want, prompt)
		}
	}
}

// TestJudgeEvidence_TaskScope_StillPersistsToDisk guards the refactor that
// made the above possible: splitting EvidenceStore.Record into Build + write
// must NOT have stopped task-scope evidence from reaching
// $OMNIPUS_HOME/tasks_evidence/. A test that only checked the Judge's prompt
// would pass with persistence silently deleted.
func TestJudgeEvidence_TaskScope_StillPersistsToDisk(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	workerInst, _ := al.GetRegistry().GetAgent("native-agent")
	workerInst.Tools.RegisterReplacing(&fakeBashTool{result: &tools.ToolResult{
		ForLLM: "all green\n[Command exited with code 0]",
	}})
	allowBashPolicy(workerInst)

	result := al.JudgeCriteria(context.Background(), JudgeCriteriaInput{
		Scope:           task.VerdictScopeTask,
		TaskID:          "t-persist",
		AssigneeAgentID: workerInst.ID,
		Attempt:         3,
		Criteria:        []task.AcceptanceCriterion{machineCriterion("c1", "make test", 0)},
	})
	if result.Unavailable {
		t.Fatalf("unexpected Unavailable: %s", result.Reason)
	}
	recs, err := al.evidenceStore().List("t-persist")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("persisted evidence records = %d, want 1 (task-scope evidence must still reach disk)", len(recs))
	}
	if recs[0].CriterionID != "c1" || recs[0].Attempt != 3 || recs[0].ExitCode != 0 {
		t.Errorf("persisted record = %+v, want criterion c1 / attempt 3 / exit 0", recs[0])
	}
	if !strings.Contains(recs[0].Output, "all green") {
		t.Errorf("persisted output = %q, want it to carry the real command output", recs[0].Output)
	}
}

// TestMachineCheckEvidence_NoTask_RedactsAndCaps proves the unpersisted
// (goal-scope) record is scrubbed and bounded by the SAME rules the persisted
// one is — the evidence the Judge sees must not be a second, laxer channel.
//
// The secret is placed at the START of the output, well inside the cap, so a
// passing redaction assertion cannot be explained by truncation instead. (An
// earlier draft of this test put it at the cap boundary and passed with
// redaction entirely disabled — the cut, not the scrubber, removed it.)
//
// tools.filter_sensitive_data is deliberately left FALSE (SEC review F-2):
// machine-check Output goes verbatim into the Judge's prompt, so its
// redaction must not depend on an operator display toggle. The record must
// come back scrubbed with the flag off — the ungated path.
func TestMachineCheckEvidence_NoTask_RedactsAndCaps(t *testing.T) {
	const secret = "sk-liveCredentialThatMustNeverReachTheJudge"
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, func(cfg *config.Config) {
		cfg.Tools.FilterSensitiveData = false
		cfg.RegisterSensitiveValues([]string{secret})
	})

	output := "token=" + secret + " " + strings.Repeat("y", task.DefaultEvidenceOutputCap+4096)

	rec := al.machineCheckEvidence("", "goal-check-true", 1, "true", output, 0, false, false)
	if rec == nil {
		t.Fatal("F1 REGRESSION: a task-less (goal-scope) machine check produced NO evidence record; " +
			"the prose Judge is shown \"no machine-check results\" and the goal can never be met")
	}
	if rec.ExitCode != 0 || rec.CriterionID != "goal-check-true" || rec.Command != "true" {
		t.Errorf("record = %+v, want the real criterion/command/exit values", rec)
	}
	if strings.Contains(rec.Output, secret) {
		t.Error("SEC F-2 REGRESSION: the registered credential survived into the Judge's evidence " +
			"bundle because tools.filter_sensitive_data is off; this redaction must be ungated")
	}
	if !strings.Contains(rec.Output, registeredSecretMarker) {
		t.Errorf("the scrub must leave a visible %s marker (a silently-deleted secret reads as a clean "+
			"output to the no-secrets floor item), got prefix %q",
			registeredSecretMarker, rec.Output[:min(80, len(rec.Output))])
	}
	if !rec.Truncated {
		t.Error("an over-cap output must be marked Truncated")
	}
	if !strings.Contains(rec.Output, "truncated") {
		t.Error("a truncated output must carry a truncation marker, or a partial result reads as a complete one")
	}
	if len(rec.Output) > task.DefaultEvidenceOutputCap+256 {
		t.Errorf("output length %d exceeds the cap plus its marker", len(rec.Output))
	}
}

// TestMachineCheckEvidence_NoTask_WritesNothingToDisk: the goal-scope record
// must be built, not filed. The on-disk layout is partitioned by task id, and
// an empty id must never become a directory name.
func TestMachineCheckEvidence_NoTask_WritesNothingToDisk(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	if rec := al.machineCheckEvidence("", "c1", 1, "true", "ok", 0, false, false); rec == nil {
		t.Fatal("no record built")
	}
	entries, err := os.ReadDir(filepath.Join(config.OmnipusHomeDir(), "tasks_evidence"))
	if err == nil && len(entries) != 0 {
		t.Errorf("a task-less adjudication wrote %d evidence dir(s); it must persist nothing", len(entries))
	}
}

// === F1: tool OUTPUT in the verifier window ==============================

// TestVerifierWindow_RendersToolOutput is the second half of F1. The Judge
// recorded its own complaint verbatim in all three CI attempts: "the
// transcript window contains only tool-call lines and worker narration, so
// nothing addresses whether the factual claims made are grounded".
func TestVerifierWindow_RendersToolOutput(t *testing.T) {
	entries := []session.TranscriptEntry{{
		Role:    "assistant",
		Content: "I read the addendum.",
		ToolCalls: []session.ToolCall{{
			Tool:   "read_file",
			Status: "success",
			Result: map[string]any{"text": "PASS 4: scan complete, 0 findings"},
		}},
	}}
	msgs := renderTranscriptEntriesForWindow(entries, nil)
	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2 (narration + tool call): %+v", len(msgs), msgs)
	}
	got := msgs[1].Content
	if !strings.Contains(got, "read_file") || !strings.Contains(got, "success") {
		t.Errorf("the call line regressed: %q", got)
	}
	if !strings.Contains(got, "PASS 4: scan complete, 0 findings") {
		t.Errorf("F1 REGRESSION: the tool's OUTPUT is absent from the verifier window, so a prose "+
			"criterion judged against \"the output\" has nothing to read: %q", got)
	}
}

// TestVerifierWindow_ToolOutputCappedWithMarker: an unmarked cut would let a
// truncated success read as a complete one.
func TestVerifierWindow_ToolOutputCappedWithMarker(t *testing.T) {
	long := strings.Repeat("z", verifierToolOutputCap*3)
	got := renderToolCallLines(session.ToolCall{
		Tool: "bash", Status: "success", Result: map[string]any{"text": long},
	}, nil)
	if len(got) > verifierToolOutputCap*2 {
		t.Errorf("rendered tool output is not capped: %d bytes", len(got))
	}
	if !strings.Contains(got, verifierToolOutputTruncationMarker) {
		t.Error("a capped tool output must carry the truncation marker")
	}
}

// TestVerifierWindow_ToolOutputRedacted proves the newly-exposed tool output
// is scrubbed before it reaches the verifier, and — just as importantly —
// that the scrub leaves a VISIBLE marker. The floor criterion "No secrets or
// credentials appear in the output" is judged against this very text; a
// silently-deleted secret would read as a clean output.
func TestVerifierWindow_ToolOutputRedacted(t *testing.T) {
	const registered = "hunter2-the-registered-one"
	const patterned = "ghp_0123456789abcdef0123456789abcdef0123"
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, func(cfg *config.Config) {
		cfg.RegisterSensitiveValues([]string{registered})
	})
	redact := al.windowEvidenceRedactor()

	got := renderToolCallLines(session.ToolCall{
		Tool: "bash", Status: "success",
		Result: map[string]any{"text": "token=" + patterned + " password=" + registered +
			strings.Repeat(" padding", 40)},
	}, redact)

	if strings.Contains(got, registered) {
		t.Error("a REGISTERED credential reached the verifier window unredacted")
	}
	if strings.Contains(got, patterned) {
		t.Error("a pattern-matched credential (ghp_…) reached the verifier window unredacted")
	}
	// Assert the markers THEMSELVES, one per layer. An earlier version of
	// this was `!Contains(got, "[REDACTED]") && !Contains(got, "[")`, which
	// can never be true — every rendered line starts with "[tool_call]" — so a
	// redactor that DELETED secrets silently passed it.
	if !strings.Contains(got, registeredSecretMarker) {
		t.Errorf("layer 1 must leave a visible %s marker so the no-secrets floor item is judgeable; "+
			"a silent deletion reads as a clean output: %q", registeredSecretMarker, got)
	}
	if !strings.Contains(got, patternSecretMarker) {
		t.Errorf("layer 2 must leave a visible %s marker: %q", patternSecretMarker, got)
	}
}

// TestVerifierWindow_ToolErrorRendered: RC-5 persists a failed call's reason
// in ToolCall.Error rather than Result. That reason is evidence too.
func TestVerifierWindow_ToolErrorRendered(t *testing.T) {
	got := renderToolCallLines(session.ToolCall{
		Tool: "write_file", Status: "error", Error: "permission denied: /etc/passwd",
	}, nil)
	if !strings.Contains(got, "permission denied") {
		t.Errorf("a failed tool call's reason must reach the verifier window: %q", got)
	}
}

// === F2: the goal must always reach a terminal state =====================

// TestGoalIdleSteer_ReInjectsOnTheClaimPathSeam proves the idle-settlement
// steer is published as a bus.InboundMessage stamped goalLoopFollowUpSenderID
// — the sender id checkGoalLoopAfterTurn's origin gate accepts.
//
// The wedge it replaces: the steer used to go through al.asyncNotifier, which
// publishes on the "system" channel as "async:goal_idle_settle" and routes to
// processSystemMessage — and processSystemMessage builds its processOptions
// with NO SenderID and no UserInitiated. So the steer turn was dropped by
// checkGoalLoopAfterTurn's origin gate before it could clear the idleSettling
// marker. Since that marker is cleared ONLY by bumpGoalActivityOnTurn, a goal
// got exactly ONE idle adjudication ever and then sat `active` forever.
func TestGoalIdleSteer_ReInjectsOnTheClaimPathSeam(t *testing.T) {
	resetGoalTriggerStateForTest()
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	_, sid := newGoalTestSession(t, al, agentInst.ID)
	al.recordGoalRouting(sid, "webchat", "chat-1", "session-key-1", agentInst.ID)

	al.goalMarkIdleSettling(sid, true)
	al.idleSteerDeliverer(sid)("Continue working toward the goal: ship it")

	select {
	case m := <-al.bus.InboundChan():
		if m.Sender.CanonicalID != goalLoopFollowUpSenderID {
			t.Fatalf("F2 REGRESSION: idle steer published with sender %q, want %q — any other sender is "+
				"dropped by checkGoalLoopAfterTurn's origin gate, which wedges the goal `active` forever",
				m.Sender.CanonicalID, goalLoopFollowUpSenderID)
		}
		if m.Channel != "webchat" || m.ChatID != "chat-1" || m.SessionID != sid {
			t.Errorf("idle steer routed to %s/%s session %q, want webchat/chat-1 session %q",
				m.Channel, m.ChatID, m.SessionID, sid)
		}
		if !strings.Contains(m.Content, "ship it") {
			t.Errorf("steer content lost: %q", m.Content)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("idle steer was never published on the bus")
	}
}

// TestGoalIdleSteer_NoRouting_ReArmsAnyway: a steer that cannot be delivered
// must not leave the goal wedged. It is better to re-adjudicate after the
// next quiet window (bounded by GoalMaxRounds, so it still terminates) than
// to sit `active` with no terminal state and nothing for the pill to render.
func TestGoalIdleSteer_NoRouting_ReArmsAnyway(t *testing.T) {
	resetGoalTriggerStateForTest()
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	_, sid := newGoalTestSession(t, al, agentInst.ID)
	// Deliberately NO recordGoalRouting.

	al.goalMarkIdleSettling(sid, true)
	al.idleSteerDeliverer(sid)("steer with nowhere to go")

	if al.goalIsIdleSettling(sid) {
		t.Fatal("an undeliverable steer must still re-arm the quiet window; leaving the marker set " +
			"wedges the goal `active` forever with no terminal state")
	}
}

// TestGoalRearmAfterAbnormalTurn_OriginGate proves the second F2 path AND its
// origin gate in one table. runAgentLoop returns EARLY — before
// checkGoalLoopAfterTurn — on a runTurn error and on a user-initiated hard
// abort, so neither ever bumped the goal's activity clock.
//
// The gate matters as much as the re-arm. `/goal` and `/loop` share a
// session, and BOTH call sites are also reached by ProcessScheduled and
// processSystemMessage (UserInitiated=false, SenderID=""). Re-arming from
// those would bump GoalLastActivityAt on every failed heartbeat; a heartbeat
// failing more often than goalIdleQuietWindow (60 s) means the quiet window
// NEVER elapses — the OPPOSITE wedge, and a quieter one.
func TestGoalRearmAfterAbnormalTurn_OriginGate(t *testing.T) {
	cases := []struct {
		name       string
		userInit   bool
		senderID   string
		wantRearm  bool
		wantPill   bool
		whyIfWrong string
	}{
		{
			name: "user-initiated abort re-arms", userInit: true,
			wantRearm: true, wantPill: true,
			whyIfWrong: "F2: a goal whose turn died before checkGoalLoopAfterTurn stays marked " +
				"idle-settling, so every later PlanEngine tick early-returns and the goal is wedged " +
				"`active` forever — no adjudication, no round, no terminal state, nothing user-visible",
		},
		{
			name: "goal-loop steer turn re-arms", senderID: goalLoopFollowUpSenderID,
			wantRearm: true, wantPill: true,
			whyIfWrong: "the goal loop's own re-injected follow-up is a goal turn; if its abnormal " +
				"end does not re-arm, one failed steer wedges the goal",
		},
		{
			name:      "scheduled / heartbeat / async origin must NOT re-arm",
			wantRearm: false, wantPill: false,
			whyIfWrong: "BLOCKER: a non-goal turn bumped the goal's activity clock. A heartbeat " +
				"failing more often than the 60 s quiet window would keep the window from EVER " +
				"elapsing — the inverse wedge",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetGoalTriggerStateForTest()
			al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
			agentInst, _ := al.GetRegistry().GetAgent("native-agent")
			store, sid := newGoalTestSession(t, al, agentInst.ID)
			setGoalRoundsArmed(t, store, sid, "ship the thing", 1, time.Now().Add(-2*time.Hour))
			collector, stop := newEventCollector(t, al)
			defer stop()

			al.goalMarkIdleSettling(sid, true)
			before, err := store.GetMeta(sid)
			if err != nil {
				t.Fatal(err)
			}

			al.rearmGoalAfterAbnormalTurn(processOptions{
				TranscriptStore:     store,
				TranscriptSessionID: sid,
				UserInitiated:       tc.userInit,
				SenderID:            tc.senderID,
			})

			if got := !al.goalIsIdleSettling(sid); got != tc.wantRearm {
				t.Fatalf("re-armed = %v, want %v — %s", got, tc.wantRearm, tc.whyIfWrong)
			}
			after, err := store.GetMeta(sid)
			if err != nil {
				t.Fatal(err)
			}
			bumped := after.GoalLastActivityAt != before.GoalLastActivityAt
			if bumped != tc.wantRearm {
				t.Errorf("activity clock bumped = %v, want %v — %s", bumped, tc.wantRearm, tc.whyIfWrong)
			}

			// The goal_status frame is the user-visible half of the fix; an
			// unasserted emission is an unverified one. The event bus delivers
			// to subscribers asynchronously, so poll rather than read once.
			sawFrame := func() bool { return len(goalStatusPayloadsFor(collector, sid)) > 0 }
			if tc.wantPill {
				require.Eventually(t, sawFrame, 3*time.Second, 20*time.Millisecond,
					"the re-arm must emit a goal_status frame so the pill reflects the live state")
				pills := goalStatusPayloadsFor(collector, sid)
				if got := pills[len(pills)-1].State; got != goalPillActive {
					t.Errorf("re-arm pill state = %q, want %q", got, goalPillActive)
				}
			} else {
				require.Never(t, sawFrame, 500*time.Millisecond, 20*time.Millisecond,
					"a gated-out origin must emit no goal_status frame at all — %s", tc.whyIfWrong)
			}

			// The re-arm is a re-arm and NOTHING else: never a verdict, a
			// round, or a termination.
			if after.GoalRoundsUsed != before.GoalRoundsUsed {
				t.Errorf("re-arm consumed a round (%d -> %d); an abnormal turn is not evidence about the goal",
					before.GoalRoundsUsed, after.GoalRoundsUsed)
			}
			if after.GoalCondition == "" {
				t.Error("re-arm must not clear the goal; it only restores the normal state machine")
			}
		})
	}
}

// TestGoalRearmAfterAbnormalTurn_WaitingOnUserKeepsItsPill: settlement is
// already suppressed for a waiting_on_user goal (maybeSettleGoalIdle), so the
// activity bump is harmless — but repainting the pill `active` would tell the
// user the goal is working when it is in fact still parked waiting for THEM.
func TestGoalRearmAfterAbnormalTurn_WaitingOnUserKeepsItsPill(t *testing.T) {
	resetGoalTriggerStateForTest()
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	setGoalRoundsArmed(t, store, sid, "ask me something", 1, time.Now().Add(-2*time.Hour))
	collector, stop := newEventCollector(t, al)
	defer stop()

	al.goalSetWaitingOnUser(sid, true)
	al.rearmGoalAfterAbnormalTurn(processOptions{
		TranscriptStore:     store,
		TranscriptSessionID: sid,
		UserInitiated:       true,
	})

	require.Never(t, func() bool { return len(goalStatusPayloadsFor(collector, sid)) > 0 },
		500*time.Millisecond, 20*time.Millisecond,
		"a waiting_on_user goal must not be repainted `active` by the re-arm; got frame state(s) %v",
		pillStates(goalStatusPayloadsFor(collector, sid)))
	if !al.goalIsWaitingOnUser(sid) {
		t.Error("the re-arm must not clear the waiting_on_user pause — only a real user reply resumes it")
	}
}

// pillStates flattens payload states for readable failure output.
func pillStates(ps []GoalStatusChangedPayload) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.State)
	}
	return out
}

// TestGoalRearmAfterAbnormalTurn_IsWiredIntoRunAgentLoop is the DEAD-WIRING
// guard. Every other test here calls rearmGoalAfterAbnormalTurn directly, so
// deleting both of its call sites in runAgentLoop left the whole suite green.
// This drives the REAL entry point: a BeforeLLM HookActionHardAbort makes
// runTurn return a non-nil error, which is exactly the `if err != nil` early
// return that skips checkGoalLoopAfterTurn.
//
// It covers the origin gate end-to-end too, so a gate that exists only in the
// unit test cannot pass here.
func TestGoalRearmAfterAbnormalTurn_IsWiredIntoRunAgentLoop(t *testing.T) {
	for _, tc := range []struct {
		name      string
		userInit  bool
		wantRearm bool
	}{
		{name: "user turn re-arms", userInit: true, wantRearm: true},
		{name: "scheduled origin does not", userInit: false, wantRearm: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetGoalTriggerStateForTest()
			al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
			agentInst, _ := al.GetRegistry().GetAgent("native-agent")
			store, sid := newGoalTestSession(t, al, agentInst.ID)
			setGoalRoundsArmed(t, store, sid, "ship the thing", 1, time.Now().Add(-2*time.Hour))

			// Same seam TestAgentLoop_AbortTurn_HookHardAbort_… uses: a
			// system-initiated hard abort surfaces a real error from
			// runAgentLoop, taking the pre-checkGoalLoopAfterTurn return.
			if err := al.MountHook(NamedHook("goal-rearm-hard-abort", &hardAbortBeforeLLMHook{})); err != nil {
				t.Fatalf("MountHook: %v", err)
			}
			al.goalMarkIdleSettling(sid, true)

			_, err := al.runAgentLoop(context.Background(), agentInst, processOptions{
				SessionKey:          "sk-" + sid,
				Channel:             "webchat",
				ChatID:              "chat-1",
				UserMessage:         "keep going",
				DefaultResponse:     defaultResponse,
				UserInitiated:       tc.userInit,
				TranscriptStore:     store,
				TranscriptSessionID: sid,
			})
			if err == nil {
				t.Fatal("expected runAgentLoop to return the hard-abort error; without it this test " +
					"never reaches the early return it exists to cover")
			}
			if got := !al.goalIsIdleSettling(sid); got != tc.wantRearm {
				t.Fatalf("after an abnormal runAgentLoop exit: re-armed = %v, want %v "+
					"(the rearmGoalAfterAbnormalTurn call site in runAgentLoop is the only thing "+
					"that can produce this)", got, tc.wantRearm)
			}
		})
	}
}

// TestGoalRearmAfterAbnormalTurn_NoGoalIsANoOp: the hook runs after EVERY
// abnormal turn in the process, so the no-goal path must be free and inert.
func TestGoalRearmAfterAbnormalTurn_NoGoalIsANoOp(t *testing.T) {
	resetGoalTriggerStateForTest()
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)

	al.goalMarkIdleSettling(sid, true) // stale marker, but no active goal
	al.rearmGoalAfterAbnormalTurn(processOptions{
		TranscriptStore:     store,
		TranscriptSessionID: sid,
		UserInitiated:       true,
	})
	if !al.goalIsIdleSettling(sid) {
		t.Error("a session with no active goal must not be touched by the re-arm")
	}

	// A task run and an unwired store must both short-circuit before any read.
	al.rearmGoalAfterAbnormalTurn(processOptions{
		IsTaskRun: true, TranscriptStore: store, TranscriptSessionID: sid, UserInitiated: true})
	al.rearmGoalAfterAbnormalTurn(processOptions{TranscriptSessionID: sid, UserInitiated: true})
	al.rearmGoalAfterAbnormalTurn(processOptions{TranscriptStore: store, UserInitiated: true})
}

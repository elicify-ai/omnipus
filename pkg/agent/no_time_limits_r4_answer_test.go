package agent

// PR #1311 ROUND 4 G8: the parent receives the helper's WORK, not the
// one-sentence goal_claim evidence. FR-094 independently keeps that evidence
// as the Judge's claim input. Launch/Dispatch, goal_claim, adjudication,
// completion and both stores stay real; only the provider is scripted.
// CHECK: use evidence as handback; drop earlier answer for a pointer tail;
// remove the contract's 50,000-character cap or its [truncated] marker.

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

type r4DelegateHarness struct {
	al       *AgentLoop
	parentID string
	agent    *AgentInstance
	judge    *AgentInstance
	launcher *SteerLauncher
}

func newR4DelegateHarness(t *testing.T, provider providers.LLMProvider) *r4DelegateHarness {
	t.Helper()
	oldRegistry := currentVerifierSessionRegistry()
	t.Cleanup(func() { SetVerifierSessionRegistry(oldRegistry) })
	SetVerifierSessionRegistry(NewVerifierSessionRegistry())
	al, judge := newGoalLoopTestLoop(t, provider, func(cfg *config.Config) {
		cfg.Agents.Defaults.MaxToolIterations = 64 // well above the eight-call guard and the finite scripts
		cfg.Sandbox.ToolPolicies["delegate"] = "allow"
		cfg.Sandbox.ToolPolicies["message_parent"] = "allow"
	})
	installTestSessionLauncher(t, al)
	wireSteerCompletionDeps(t, al)
	launcher := NewSteerLauncher(al)
	al.SetSteerSessionLauncher(launcher)
	agent, ok := al.GetRegistry().GetAgent("native-agent")
	if !ok {
		t.Fatal("SETUP native worker is not registered")
	}
	_, parent := newGoalTestSession(t, al, agent.ID)
	return &r4DelegateHarness{al: al, parentID: parent, agent: agent, judge: judge, launcher: launcher}
}

func (h *r4DelegateHarness) launch(t *testing.T, withGoal bool) steer.LaunchResult {
	t.Helper()
	req := steer.LaunchRequest{SteeringSessionID: h.parentID, TargetAgentID: h.agent.ID,
		Task:   "Produce the substantive analysis and return it to the parent",
		Origin: steer.Origin{Kind: steer.OriginKindDelegate, CallID: "r4-delegate"}}
	if withGoal {
		req.Goal = &steer.GoalSpec{Criteria: []steer.Criterion{{Text: "The analysis answers the requested question"}},
			DoD: []steer.Criterion{{Text: "The answer is supported by the checked work"}}}
	}
	child, err := h.launcher.Launch(context.Background(), req)
	if err != nil {
		t.Fatalf("SETUP real Launch: %v", err)
	}
	return child
}

func (h *r4DelegateHarness) dispatch(t *testing.T, child steer.LaunchResult) {
	t.Helper()
	got, err := h.launcher.Dispatch(context.Background(), child.SessionID, child.Generation)
	if err != nil || got.State != steer.DispatchRunning {
		t.Fatalf("SETUP real Dispatch = %+v err=%v, want running", got, err)
	}
}

func (h *r4DelegateHarness) parentContext() context.Context {
	return tools.WithAgentID(tools.WithTranscriptSessionID(context.Background(), h.parentID), h.agent.ID)
}

func (h *r4DelegateHarness) publicDelegate(args map[string]any) *tools.ToolResult {
	return h.agent.Tools.ExecuteWithContext(h.parentContext(), "delegate", args, "webchat", h.parentID, nil)
}

func r4FinalHandback(t *testing.T, h *r4DelegateHarness, childID string) generated.SessionMessageHandback {
	t.Helper()
	messages, _, _, err := h.al.GetMessageInboxStore().Drain(h.parentID, childID, "", 0)
	if err != nil {
		t.Fatalf("read real parent inbox: %v", err)
	}
	var final generated.SessionMessageHandback
	count := 0
	for _, message := range messages {
		if handback, decodeErr := message.AsSessionMessageHandback(); decodeErr == nil &&
			handback.Mode == generated.SessionMessageHandbackModeFinal {
			final = handback
			count++
		}
	}
	if count != 1 || final.SessionId != childID {
		t.Fatalf("SETUP parent has %d final handbacks, want exactly one from %s: %+v", count, childID, final)
	}
	return final
}

func r4RunGoalAnswer(t *testing.T, answer, finalMessage string) (generated.SessionMessageHandback, *fakeJudgeProvider) {
	t.Helper()
	const evidence = "EVIDENCE-R4: I checked the completed analysis against the acceptance criteria."
	provider := testutil.NewScenario().WithTextAndToolCall(answer, tools.GoalClaimToolName,
		fmt.Sprintf(`{"status":"met","evidence":%q}`, evidence)).WithText(finalMessage)
	h := newR4DelegateHarness(t, provider)
	child := h.launch(t, true)
	rec := mustLoadRec(t, h.al, child.SessionID)
	verdict := metGoalVerdict947(t, rec) // controlled provider reply over the INPUT ladder's identifiers
	judge := &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return &providers.LLMResponse{Content: verdict}, nil
	}}
	h.judge.Provider = judge
	done := make(chan struct{})
	closeDone := sync.OnceFunc(func() { close(done) })
	oldDone := goalDeferredAdjudicationDoneFn
	goalDeferredAdjudicationDoneFn = func(sid string) {
		if sid == child.SessionID {
			closeDone()
		}
	}
	t.Cleanup(func() { goalDeferredAdjudicationDoneFn = oldDone })
	h.dispatch(t, child)
	waitClosed(t, done, "the real helper's Judge/completion tail")
	h.al.steerAdmission().turns.Wait()
	if completed := mustLoadRec(t, h.al, child.SessionID); completed.State != session.LifecycleCompleted {
		t.Fatalf("SETUP the Judge-met helper did not complete: %+v", completed)
	}
	if calls := provider.CallCount(); calls != 2 || judge.callCount() != 1 {
		t.Fatalf("SETUP worker/Judge calls=%d/%d, want two scripted worker calls and one real adjudication", calls, judge.callCount())
	}
	g := mustGoalRecord(t, rec.GoalRef)
	if g.State != generated.GoalStateMet || g.LatestClaim == nil || g.LatestClaim.Evidence != evidence {
		t.Fatalf("SETUP real goal_claim/Judge did not record the specified evidence and met verdict: %+v", g)
	}
	judge.mu.Lock()
	requests := append([]providers.Message(nil), judge.lastMessages...)
	judge.mu.Unlock()
	claim := ""
	for _, message := range requests {
		if message.Role == "user" {
			_, tail, found := strings.Cut(message.Content, "## Worker's own completion claim")
			if found {
				_, claim, _ = strings.Cut(tail, "\n")
			}
		}
	}
	if got := strings.TrimSpace(claim); got != evidence {
		t.Errorf("G8 FR-094 Judge claim=%q, want ONLY evidence=%q (the answer must not replace the claim input)", got, evidence)
	}
	entries, err := h.al.GetSessionStore().ReadTranscript(child.SessionID)
	if err != nil {
		t.Fatalf("read real helper transcript: %v", err)
	}
	answerSaved := false
	for _, entry := range entries {
		if entry.Role == "assistant" && strings.Contains(entry.Content, answer) {
			answerSaved = true
		}
	}
	if !answerSaved {
		t.Fatal("SETUP substantive answer was not actually written to the helper's transcript")
	}
	return r4FinalHandback(t, h, child.SessionID), judge
}

func TestR4GoalHelper_HandbackCarriesAnswerWhileJudgeGetsOnlyEvidence(t *testing.T) {
	const answer = "ANALYSIS-R4: The failure is an ownership mismatch. The child stores its goal under its own session; cancellation routing belongs to the root. Track own-session work and queued descendants, preserve sibling isolation, and verify the three controls before landing."
	for _, tc := range []struct{ name, final string }{
		{"answer_in_final_message", answer},
		{"substantive_answer_before_pointer_only_final", "The full analysis is above; I have claimed completion."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handback, _ := r4RunGoalAnswer(t, answer, tc.final)
			if !strings.Contains(handback.ResultSoFar, answer) {
				t.Errorf("G8 parent handback lost the substantive answer: result_so_far=%q, want the full ANALYSIS-R4 text (not just goal_claim evidence)", handback.ResultSoFar)
			}
		})
	}
}

func TestR4GoalHelper_HandbackAnswerHonorsFiftyThousandCharacterCap(t *testing.T) {
	const limit = 50000 // SessionMessageHandback.result_so_far maxLength; ROUND 4 G8
	const prefix = "CAP-ANSWER-R4: "
	for _, size := range []int{limit - 1, limit, limit + 1} {
		t.Run(fmt.Sprintf("answer_%d_characters", size), func(t *testing.T) {
			answer := prefix + strings.Repeat("x", size-len(prefix))
			handback, _ := r4RunGoalAnswer(t, answer, "The report is above; completion has been claimed.")
			if size <= limit {
				if !strings.Contains(handback.ResultSoFar, answer) {
					t.Errorf("G8 answer at/below cap was lost or truncated: input=%d result=%d, want the complete answer", size, utf8.RuneCountInString(handback.ResultSoFar))
				}
				return
			}
			if !strings.Contains(handback.ResultSoFar, prefix) || !strings.HasSuffix(handback.ResultSoFar, "[truncated]") ||
				utf8.RuneCountInString(handback.ResultSoFar) > limit || strings.Contains(handback.ResultSoFar, answer) {
				t.Errorf("G8 over-cap answer must retain the substantive prefix, end with [truncated], and fit 50000 characters: result=%q length=%d", handback.ResultSoFar, utf8.RuneCountInString(handback.ResultSoFar))
			}
		})
	}
}

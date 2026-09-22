package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/task"
)

type steeredInputCaptureProvider struct {
	once     sync.Once
	messages []providers.Message
	done     chan struct{}
}

func (p *steeredInputCaptureProvider) Chat(_ context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	p.once.Do(func() {
		p.messages = append([]providers.Message(nil), messages...)
		close(p.done)
	})
	return &providers.LLMResponse{Content: "child answer"}, nil
}

func (p *steeredInputCaptureProvider) GetDefaultModel() string { return "steered-input-capture" }

func wireSteerCompletionDeps(t *testing.T, al *AgentLoop) {
	t.Helper()
	classifier := NewSteerRecordClassifier(al.GetSessionLifecycleStore(), al.GetSessionStore())
	al.SetSteerAudienceDeps(
		NewSteerAudienceResolver(classifier),
		steer.NopBoundaryObserver{},
		NewSteerUpwardDeliverer(),
	)
}

func launchRunningChild(t *testing.T, al *AgentLoop, parentID, callID string) *session.LifecycleRecord {
	t.Helper()
	res, err := NewSteerLauncher(al).Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: parentID,
		TargetAgentID:     testDefaultAgentID,
		Task:              "do delegated work",
		Origin:            steer.Origin{Kind: steer.OriginKindDelegate, CallID: callID},
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	rec, err := al.GetSessionLifecycleStore().Load(res.SessionID)
	if err != nil {
		t.Fatalf("Load(child): %v", err)
	}
	rec.State = session.LifecycleRunning
	if err := al.GetSessionLifecycleStore().Persist(rec); err != nil {
		t.Fatalf("Persist(running): %v", err)
	}
	return rec
}

func TestCompletion_Disposition_PersistedAndValidated(t *testing.T) {
	tests := []struct {
		name       string
		answer     string
		turnFailed bool
		withQueued bool
		wantState  session.LifecycleState
		wantKind   string
		wantText   string
		wantFatal  bool
	}{
		{name: "non-empty quiet", answer: "finished", wantState: session.LifecycleCompleted, wantKind: "handback"},
		{name: "empty quiet", answer: "   ", wantState: session.LifecycleFailed, wantKind: "error", wantText: "empty_answer:", wantFatal: true},
		{name: "iteration limit is a non-fatal lifecycle notice", answer: toolLimitResponse, turnFailed: true, wantState: session.LifecycleRunning, wantKind: "error", wantText: "max_tool_iterations:", wantFatal: false},
		{name: "non-empty queued descendant", answer: "parent answer", withQueued: true, wantState: session.LifecycleRunning},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			al, cleanup := newSteerAL(t)
			defer cleanup()
			wireSteerCompletionDeps(t, al)
			parentID := newTestSteeringSession(t, al, "ws-1")
			rec := launchRunningChild(t, al, parentID, "call-complete")
			if tc.withQueued {
				if _, err := NewSteerLauncher(al).Launch(context.Background(), steer.LaunchRequest{
					SteeringSessionID: rec.SessionID,
					TargetAgentID:     testDefaultAgentID,
					Task:              "queued grandchild",
					Origin:            steer.Origin{Kind: steer.OriginKindDelegate, CallID: "call-grandchild"},
				}); err != nil {
					t.Fatalf("Launch(grandchild): %v", err)
				}
			}

			al.completeSteeredTurn(context.Background(), rec, turnResult{finalContent: tc.answer, turnFailed: tc.turnFailed}, nil)

			got, err := al.GetSessionLifecycleStore().Load(rec.SessionID)
			if err != nil {
				t.Fatalf("Load(after completion): %v", err)
			}
			if got.State != tc.wantState {
				t.Fatalf("state = %q, want %q", got.State, tc.wantState)
			}
			msgs, _, _, err := al.GetMessageInboxStore().Drain(parentID, rec.SessionID, "", 10)
			if err != nil {
				t.Fatalf("Drain(parent): %v", err)
			}
			if tc.wantKind == "" {
				if len(msgs) != 0 {
					t.Fatalf("messages = %d, want none while descendant is queued", len(msgs))
				}
				return
			}
			if len(msgs) != 1 {
				t.Fatalf("messages = %d, want 1", len(msgs))
			}
			kind, err := msgs[0].Discriminator()
			if err != nil || kind != tc.wantKind {
				t.Fatalf("message kind = %q (%v), want %q", kind, err, tc.wantKind)
			}
			if tc.wantText != "" {
				v, err := msgs[0].AsSessionMessageError()
				if err != nil || !strings.Contains(v.Text, tc.wantText) {
					t.Fatalf("error message = %q (%v), want contains %q", v.Text, err, tc.wantText)
				}
				if v.Fatal != tc.wantFatal {
					t.Fatalf("error fatal = %v, want %v", v.Fatal, tc.wantFatal)
				}
			}
		})
	}
}

// TestCompletion_IterationLimit_NonFatal_DoesNotWakeParent covers landing
// order §2 I-5's "max-iterations lifecycle notice" row: a steered child whose
// turn ends at the tool-iteration ceiling (loop_run_turn.go::finalizeTurn
// sets finalContent to the toolLimitResponse sentinel and marks the turn
// failed) is delivered to its parent as an `error` entry with `fatal: false` —
// a notice that the child stopped early, not a crash — and a non-fatal error
// never wakes the parent. A genuine failure is the contrast: `fatal: true`
// and the parent is woken.
func TestCompletion_IterationLimit_NonFatal_DoesNotWakeParent(t *testing.T) {
	tests := []struct {
		name      string
		result    turnResult
		runErr    error
		wantFatal bool
		wantWake  bool
		wantText  string
		wantState session.LifecycleState
	}{
		{
			name:      "iteration limit produces a non-fatal notice and does not wake",
			result:    turnResult{finalContent: toolLimitResponse, turnFailed: true},
			wantFatal: false,
			wantWake:  false,
			wantText:  "max_tool_iterations:",
			wantState: session.LifecycleRunning,
		},
		{
			name:      "genuine failure is fatal and wakes the parent",
			runErr:    errors.New("downstream provider failure"),
			wantFatal: true,
			wantWake:  true,
			wantText:  "failed:",
			wantState: session.LifecycleFailed,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			al, cleanup := newSteerAL(t)
			defer cleanup()
			wireSteerCompletionDeps(t, al)
			parentID := newTestSteeringSession(t, al, "ws-1")
			rec := launchRunningChild(t, al, parentID, "call-"+tc.name)

			// The plain webchat parent carries an empty PeerID, so the wake
			// destination would be empty and WakeParentAlways would refuse.
			// Give the child's reporting target a routable address so the
			// wake-eligibility contrast is genuinely observable.
			if err := al.GetSessionLifecycleStore().Mutate(rec.SessionID, func(r *session.LifecycleRecord) error {
				r.SteeredBy.ReportingTarget = session.ReportingTarget{Channel: "webchat", ChatID: parentID}
				return nil
			}); err != nil {
				t.Fatalf("Mutate(reporting target): %v", err)
			}

			var wakes []string
			al.asyncNotifier.registerObserver(func(e AsyncNotifyEvent) {
				if strings.HasPrefix(e.SourceKind, "message_parent:") {
					wakes = append(wakes, e.SourceKind)
				}
			})

			if err := al.completeSteeredTurn(context.Background(), rec, tc.result, tc.runErr); err != nil {
				t.Fatalf("completeSteeredTurn: %v", err)
			}

			msgs, _, _, err := al.GetMessageInboxStore().Drain(parentID, rec.SessionID, "", 10)
			if err != nil {
				t.Fatalf("Drain(parent): %v", err)
			}
			if len(msgs) != 1 {
				t.Fatalf("parent messages = %d, want 1", len(msgs))
			}
			kind, err := msgs[0].Discriminator()
			if err != nil || kind != "error" {
				t.Fatalf("message kind = %q (%v), want error", kind, err)
			}
			e, err := msgs[0].AsSessionMessageError()
			if err != nil {
				t.Fatalf("AsSessionMessageError: %v", err)
			}
			if e.Fatal != tc.wantFatal {
				t.Fatalf("error fatal = %v, want %v", e.Fatal, tc.wantFatal)
			}
			if !strings.Contains(e.Text, tc.wantText) {
				t.Fatalf("error text = %q, want contains %q", e.Text, tc.wantText)
			}

			if gotWake := len(wakes) > 0; gotWake != tc.wantWake {
				t.Fatalf("parent woke = %v (sources %v), want %v", gotWake, wakes, tc.wantWake)
			}

			got, err := al.GetSessionLifecycleStore().Load(rec.SessionID)
			if err != nil {
				t.Fatalf("Load(child after completion): %v", err)
			}
			if got.State != tc.wantState {
				t.Fatalf("child state = %q, want %q", got.State, tc.wantState)
			}
		})
	}
}

func TestCompletion_LastChildCompletesWaitingParent(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	wireSteerCompletionDeps(t, al)
	rootID := newTestSteeringSession(t, al, "ws-1")
	waitingParent := launchRunningChild(t, al, rootID, "call-parent")
	if err := al.GetSessionStore().AppendTranscriptStrict(waitingParent.SessionID, session.TranscriptEntry{
		ID: "parent-answer", Role: "assistant", Content: "parent result", Timestamp: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("AppendTranscriptStrict(parent answer): %v", err)
	}
	lastChild := launchRunningChild(t, al, waitingParent.SessionID, "call-child")
	parked := launchRunningChild(t, al, waitingParent.SessionID, "call-parked")
	parked.State = session.LifecycleNeedsInput
	parked.NeedsInput = &session.NeedsInput{CorrelationID: "q-1", Reconstructable: true}
	if err := al.GetSessionLifecycleStore().Persist(parked); err != nil {
		t.Fatalf("Persist(parked): %v", err)
	}
	var question generated.SessionMessage
	if err := question.FromSessionMessageQuestion(generated.SessionMessageQuestion{
		Kind: generated.SessionMessageQuestionKindQuestion, MessageId: "q-1", SessionId: parked.SessionID,
		SenderIdentity: parked.AgentID, CreatedAt: time.Now().UTC(), Depth: 1, Text: "Which region?",
	}); err != nil {
		t.Fatalf("encode question: %v", err)
	}
	if _, err := al.GetMessageInboxStore().Append(waitingParent.SessionID, question); err != nil {
		t.Fatalf("Append(question): %v", err)
	}

	al.completeSteeredTurn(context.Background(), lastChild, turnResult{finalContent: "child result"}, nil)

	got, err := al.GetSessionLifecycleStore().Load(waitingParent.SessionID)
	if err != nil {
		t.Fatalf("Load(waiting parent): %v", err)
	}
	if got.State != session.LifecycleCompleted {
		t.Fatalf("waiting parent state = %q, want completed", got.State)
	}
	msgs, _, _, err := al.GetMessageInboxStore().Drain(rootID, waitingParent.SessionID, "", 10)
	if err != nil {
		t.Fatalf("Drain(root): %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("root messages = %d, want 1", len(msgs))
	}
	handback, err := msgs[0].AsSessionMessageHandback()
	if err != nil {
		t.Fatalf("handback: %v", err)
	}
	if handback.ResultSoFar != "parent result" {
		t.Errorf("result_so_far = %q, want parent result", handback.ResultSoFar)
	}
	if len(handback.OpenQuestions) != 1 || handback.OpenQuestions[0] != "Which region?" {
		t.Errorf("open_questions = %#v, want [Which region?]", handback.OpenQuestions)
	}
}

func TestSubagentLifecycleFrames_StartQueuedRunningTerminalEndOrder(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	wireSteerCompletionDeps(t, al)
	parentID := newTestSteeringSession(t, al, "ws-1")
	rec := launchRunningChild(t, al, parentID, "call-frame-order")
	al.deliverSubagentState(parentID, rec, string(session.LifecycleRunning))
	if err := al.completeSteeredTurn(context.Background(), rec, turnResult{finalContent: "done"}, nil); err != nil {
		t.Fatalf("completeSteeredTurn: %v", err)
	}

	entries, err := al.GetSessionStore().ReadTranscript(parentID)
	if err != nil {
		t.Fatalf("ReadTranscript(parent): %v", err)
	}
	var got []string
	for _, entry := range entries {
		switch entry.SystemSubtype {
		case session.SystemSubtypeSubagentStart:
			got = append(got, "start")
		case session.SystemSubtypeSubagentState:
			got = append(got, entry.SubagentState.State)
		case session.SystemSubtypeSubagentEnd:
			got = append(got, "end")
		}
	}
	want := []string{"start", "queued", "running", "completed", "end"}
	if len(got) != len(want) {
		t.Fatalf("frame order = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("frame order = %#v, want %#v", got, want)
		}
	}
}

func TestGoalDelegation_Judged(t *testing.T) {
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	lifecycle := session.NewLifecycleStore(t.TempDir())
	inbox := session.NewMessageInboxStore(t.TempDir())
	al.SetSessionMessagingStores(inbox, lifecycle)
	wireSteerCompletionDeps(t, al)

	parentMeta, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "native-agent")
	if err != nil {
		t.Fatalf("NewSession(parent): %v", err)
	}
	res, err := NewSteerLauncher(al).Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: parentMeta.ID,
		TargetAgentID:     "native-agent",
		Task:              "prove the goal",
		Origin:            steer.Origin{Kind: steer.OriginKindDelegate, CallID: "call-judged"},
		Goal: &steer.GoalSpec{
			Criteria: []steer.Criterion{{Text: "the work is complete"}},
			DoD:      []steer.Criterion{{Text: "the evidence is sufficient"}},
		},
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	rec, err := lifecycle.Load(res.SessionID)
	if err != nil {
		t.Fatalf("Load(child): %v", err)
	}
	rec.State = session.LifecycleRunning
	if err := lifecycle.Persist(rec); err != nil {
		t.Fatalf("Persist(running): %v", err)
	}

	g, err := resolveGoalRecordStore().Get(rec.GoalRef)
	if err != nil {
		t.Fatalf("Get(goal): %v", err)
	}
	type criterionVerdict struct {
		ID     string `json:"id"`
		Met    bool   `json:"met"`
		Reason string `json:"reason"`
	}
	verdicts := make([]criterionVerdict, 0, len(g.Criteria)+len(g.DoD))
	for _, criterion := range append(append([]task.AcceptanceCriterion{}, g.Criteria...), g.DoD...) {
		verdicts = append(verdicts, criterionVerdict{ID: criterion.ID, Met: true, Reason: "verified"})
	}
	body, err := json.Marshal(map[string]any{"met": true, "criteria": verdicts})
	if err != nil {
		t.Fatalf("Marshal(verdict): %v", err)
	}
	judgeInst.Provider = &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return &providers.LLMResponse{Content: string(body)}, nil
	}}

	ts, err := al.reconstructSteeredTurn(rec, nil)
	if err != nil {
		t.Fatalf("reconstructSteeredTurn: %v", err)
	}
	if !ts.opts.UserInitiated {
		t.Fatal("first steered turn is not marked user-initiated; goal claim would be ignored")
	}
	done := make(chan string, 1)
	oldDone := goalDeferredAdjudicationDoneFn
	goalDeferredAdjudicationDoneFn = func(sessionID string) { done <- sessionID }
	t.Cleanup(func() { goalDeferredAdjudicationDoneFn = oldDone })
	result := turnResult{finalContent: "[goal:evidence] verified the work\nGOAL_STATUS: met"}
	al.finishSteeredGoalTurn(ts, &result, nil)
	select {
	case got := <-done:
		if got != rec.SessionID {
			t.Fatalf("adjudicated session = %q, want %q", got, rec.SessionID)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for delegated goal adjudication")
	}

	messages, _, _, err := inbox.Drain(parentMeta.ID, rec.SessionID, "", 10)
	if err != nil {
		t.Fatalf("Drain(parent): %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("parent messages = %d, want one goal_status", len(messages))
	}
	status, err := messages[0].AsSessionMessageGoalStatus()
	if err != nil {
		t.Fatalf("AsSessionMessageGoalStatus: %v", err)
	}
	if status.Condition != generated.SessionMessageGoalStatusConditionMet ||
		status.Direction != generated.SessionMessageGoalStatusDirectionSessionToParent ||
		status.Evidence == nil || len(*status.Evidence) != len(verdicts) {
		t.Fatalf("goal_status = %+v, want met/session_to_parent with %d evidence rows", status, len(verdicts))
	}
}

// TestFinishSteeredGoalTurn_BareClaimFollowUpRoutesThroughAsyncNotifier proves
// the goal-loop follow-up finishSteeredGoalTurn dispatches for a steered
// child (checkGoalLoopAfterTurn's bare-claim teaching steer, G-4) is
// delivered through the SAME async-notifier re-inject primitive
// dispatchGoalAsyncFollowUp already uses for the idle-tick and deferred-
// claim paths (goal_triggers.go), rather than a second direct
// bus.MessageBus.PublishInbound call site — the guard's exactly-six census
// (scripts/check-operator-prompt-sites.sh, FR-029a) pins PublishInbound's
// call sites to the async-notifier's own site plus the five others; a
// steered turn runs through steer_launcher.go's own dispatch goroutine
// (never runAgentLoop's inline post-turn block, loop.go), so it needs its
// own re-inject seam — but that seam must reuse the existing primitive, not
// open a fresh, unclassified PublishInbound call.
func TestFinishSteeredGoalTurn_BareClaimFollowUpRoutesThroughAsyncNotifier(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	lifecycle := session.NewLifecycleStore(t.TempDir())
	inbox := session.NewMessageInboxStore(t.TempDir())
	al.SetSessionMessagingStores(inbox, lifecycle)
	wireSteerCompletionDeps(t, al)

	parentMeta, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "native-agent")
	if err != nil {
		t.Fatalf("NewSession(parent): %v", err)
	}
	res, err := NewSteerLauncher(al).Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: parentMeta.ID,
		TargetAgentID:     "native-agent",
		Task:              "prove the goal",
		Origin:            steer.Origin{Kind: steer.OriginKindDelegate, CallID: "call-bare-claim"},
		Goal: &steer.GoalSpec{
			Criteria: []steer.Criterion{{Text: "the work is complete"}},
			DoD:      []steer.Criterion{{Text: "the evidence is sufficient"}},
		},
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	rec, err := lifecycle.Load(res.SessionID)
	if err != nil {
		t.Fatalf("Load(child): %v", err)
	}
	rec.State = session.LifecycleRunning
	if err := lifecycle.Persist(rec); err != nil {
		t.Fatalf("Persist(running): %v", err)
	}

	ts, err := al.reconstructSteeredTurn(rec, nil)
	if err != nil {
		t.Fatalf("reconstructSteeredTurn: %v", err)
	}
	if !ts.opts.UserInitiated {
		t.Fatal("first steered turn is not marked user-initiated; the bare-claim gate would ignore it")
	}

	var mu sync.Mutex
	var captured []AsyncNotifyEvent
	al.asyncNotifier.registerObserver(func(event AsyncNotifyEvent) {
		mu.Lock()
		defer mu.Unlock()
		captured = append(captured, event)
	})

	// GOAL_STATUS: met with no [goal:evidence] line is a bare claim (G-4):
	// checkGoalLoopAfterTurn's handleBareGoalClaim appends a teaching-steer
	// follow-up to result.followUps on the first offense.
	result := turnResult{finalContent: "GOAL_STATUS: met"}
	al.finishSteeredGoalTurn(ts, &result, nil)

	mu.Lock()
	defer mu.Unlock()
	if len(captured) != 1 {
		t.Fatalf("async notifier observed %d events, want exactly 1 (the bare-claim follow-up)", len(captured))
	}
	got := captured[0]
	if got.SenderCanonicalID != goalLoopFollowUpSenderID {
		t.Fatalf("SenderCanonicalID = %q, want %q (checkGoalLoopAfterTurn's origin gate requires the sentinel)",
			got.SenderCanonicalID, goalLoopFollowUpSenderID)
	}
	if got.AgentID != ts.agentID {
		t.Fatalf("AgentID = %q, want %q (the steered child's own agent, never guessed)", got.AgentID, ts.agentID)
	}
	if got.TranscriptSessionID != rec.SessionID {
		t.Fatalf("TranscriptSessionID = %q, want %q", got.TranscriptSessionID, rec.SessionID)
	}
	// A bare delegate launch (steer_launcher.go::Launch) seeds the child
	// session's own meta.Channel/PeerID empty — ts.channel/ts.chatID are ""
	// here, so finishSteeredGoalTurn falls back to the same channel-less
	// "system"/synthetic-chat-id destination TaskExecutor.
	// wakeOwnerAttemptsExhausted (task_executor_judge.go) already uses.
	wantChatID := "steer:" + rec.SessionID
	if got.Channel != "system" || got.ChatID != wantChatID {
		t.Fatalf("Channel/ChatID = %q/%q, want %q/%q (the channel-less fallback destination)",
			got.Channel, got.ChatID, "system", wantChatID)
	}
	if got.Content == "" {
		t.Fatal("Content is empty; expected the bare-claim teaching steer text")
	}
}

func TestGoalDelegation_ParentGoalAbsentFromChildInput(t *testing.T) {
	provider := &steeredInputCaptureProvider{done: make(chan struct{})}
	al, _ := newGoalLoopTestLoop(t, provider, nil)
	lifecycle := session.NewLifecycleStore(t.TempDir())
	inbox := session.NewMessageInboxStore(t.TempDir())
	al.SetSessionMessagingStores(inbox, lifecycle)
	wireSteerCompletionDeps(t, al)

	parentMeta, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "native-agent")
	if err != nil {
		t.Fatalf("NewSession(parent): %v", err)
	}
	const parentGoalSecret = "PARENT-GOAL-MUST-NOT-CROSS-EDGE"
	activateTestGoalRecord(t, parentMeta.ID, parentGoalSecret)

	launcher := NewSteerLauncher(al)
	res, err := launcher.Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: parentMeta.ID,
		TargetAgentID:     "native-agent",
		Task:              "child-only instruction",
		Origin:            steer.Origin{Kind: steer.OriginKindDelegate, CallID: "call-isolation"},
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if _, err := launcher.Dispatch(context.Background(), res.SessionID, res.Generation); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	select {
	case <-provider.done:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for child model input")
	}

	var assembled strings.Builder
	for _, message := range provider.messages {
		assembled.WriteString(message.Content)
		assembled.WriteByte('\n')
	}
	input := assembled.String()
	if strings.Contains(input, parentGoalSecret) {
		t.Fatalf("assembled child input leaked the parent's goal:\n%s", input)
	}
	if !strings.Contains(input, "child-only instruction") {
		t.Fatalf("assembled child input omitted its own instruction:\n%s", input)
	}
}

package agent

// Shared real-session harness for the direct-child claim tests, plus the
// pre-existing completed-child handback check.
import (
	"context"
	"strings"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

type q2bHarness struct {
	t          *testing.T
	al         *AgentLoop
	judgeInst  *AgentInstance
	judge      *fakeJudgeProvider
	lifecycle  *session.LifecycleStore
	inbox      *session.MessageInboxStore
	dispatches *goalDispatchRecorder
	parentID   string
	child      *session.LifecycleRecord
	childTurn  *turnState
}

func newQ2BHarness(t *testing.T, callID string) *q2bHarness {
	t.Helper()
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	lifecycle := session.NewLifecycleStore(t.TempDir())
	inbox := session.NewMessageInboxStore(t.TempDir())
	al.SetSessionMessagingStores(inbox, lifecycle)
	wireSteerCompletionDeps(t, al)

	parent, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "native-agent")
	if err != nil {
		t.Fatalf("NewSession(parent): %v", err)
	}
	child := launchGoalBearingChild947(t, al, parent.ID, callID)
	childTurn, err := al.reconstructSteeredTurn(child, nil)
	if err != nil {
		t.Fatalf("reconstructSteeredTurn(child): %v", err)
	}

	judge := &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return &providers.LLMResponse{Content: metGoalVerdict947(t, child)}, nil
	}}
	judgeInst.Provider = judge

	return &q2bHarness{
		t: t, al: al, judgeInst: judgeInst, judge: judge,
		lifecycle: lifecycle, inbox: inbox, dispatches: recordGoalDispatches(al),
		parentID: parent.ID, child: child, childTurn: childTurn,
	}
}

func (h *q2bHarness) goalRecord() *goal.Goal {
	h.t.Helper()
	rec, err := resolveGoalRecordStore().Get(h.child.GoalRef)
	if err != nil {
		h.t.Fatalf("Get(goal %q): %v", h.child.GoalRef, err)
	}
	return rec
}

func (h *q2bHarness) parentMessages() []generated.SessionMessage {
	h.t.Helper()
	messages, _, _, err := h.inbox.Drain(h.parentID, h.child.SessionID, "", 32)
	if err != nil {
		h.t.Fatalf("Drain(parent): %v", err)
	}
	return messages
}

func (h *q2bHarness) parentWakeEvents() []AsyncNotifyEvent {
	h.t.Helper()
	var out []AsyncNotifyEvent
	for _, event := range h.dispatches.all() {
		if event.TranscriptSessionID == h.parentID && strings.HasPrefix(event.SourceKind, "message_parent:") {
			out = append(out, event)
		}
	}
	return out
}

func q2bLaunchDescendant(t *testing.T, h *q2bHarness, callID string, state session.LifecycleState) *session.LifecycleRecord {
	t.Helper()
	res, err := NewSteerLauncher(h.al).Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: h.child.SessionID,
		TargetAgentID:     "native-agent",
		Task:              "finish delegated evidence",
		Origin:            steer.Origin{Kind: steer.OriginKindDelegate, CallID: callID},
	})
	if err != nil {
		t.Fatalf("Launch(descendant): %v", err)
	}
	rec, err := h.lifecycle.Load(res.SessionID)
	if err != nil {
		t.Fatalf("Load(descendant): %v", err)
	}
	if state != session.LifecycleQueued {
		rec.State = state
		if err := h.lifecycle.Persist(rec); err != nil {
			t.Fatalf("Persist(descendant %s): %v", state, err)
		}
	}
	return rec
}

func q2bKeepTurnAlive(t *testing.T, al *AgentLoop, sessionID string) {
	t.Helper()
	ts := &turnState{sessionKey: sessionID, finishedChan: make(chan struct{})}
	if !al.registerTurnIfAbsent(ts) {
		t.Fatalf("registerTurnIfAbsent(%s): already occupied", sessionID)
	}
	t.Cleanup(func() { al.activeTurnStates.CompareAndDelete(sessionID, ts) })
}

func TestGoalQ2B_EndToEndOneParentWakeAndCompletedChild(t *testing.T) {
	h := newQ2BHarness(t, "q2b-e2e")
	done := make(chan string, 1)
	oldDone := goalDeferredAdjudicationDoneFn
	goalDeferredAdjudicationDoneFn = func(sessionID string) { done <- sessionID }
	t.Cleanup(func() { goalDeferredAdjudicationDoneFn = oldDone })

	result := turnResult{finalContent: "[goal:evidence] all delegated work is finished\nGOAL_STATUS: met"}
	h.al.finishSteeredGoalTurn(h.childTurn, h.child, &result, nil)
	select {
	case got := <-done:
		if got != h.child.SessionID {
			t.Errorf("adjudicated session = %q, want %q", got, h.child.SessionID)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for final adjudication")
	}

	wakes := h.parentWakeEvents()
	if len(wakes) != 1 {
		t.Errorf("parent wakes for completed child = %d, want exactly 1", len(wakes))
	} else if wakes[0].SourceKind != "message_parent:handback" {
		t.Errorf("only parent wake kind = %q, want message_parent:handback", wakes[0].SourceKind)
	}
	messages := h.parentMessages()
	if len(messages) != 1 {
		t.Errorf("parent inbox messages = %d, want exactly 1 final handback", len(messages))
	} else {
		class, err := session.ClassifySessionMessage(messages[0])
		if err != nil {
			t.Errorf("ClassifySessionMessage: %v", err)
		} else if class.Kind != "handback" {
			t.Errorf("only parent inbox message kind = %q, want handback", class.Kind)
		}
	}

	child, err := h.lifecycle.Load(h.child.SessionID)
	if err != nil {
		t.Fatalf("Load(child): %v", err)
	}
	if child.State != session.LifecycleCompleted || !child.Terminal() {
		t.Errorf("child lifecycle = %q (terminal=%v), want one completed lifecycle", child.State, child.Terminal())
	}
	children, err := h.lifecycle.List(session.LifecycleFilter{SteeringSessionID: h.parentID})
	if err != nil {
		t.Fatalf("List(parent children): %v", err)
	}
	if len(children) != 1 || children[0].SessionID != h.child.SessionID {
		t.Errorf("parent child lifecycles = %v, want exactly child %q", children, h.child.SessionID)
	}
	if calls := h.judge.callCount(); calls != 1 {
		t.Errorf("Judge calls = %d, want exactly 1", calls)
	}
}

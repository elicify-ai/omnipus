package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

const g5ProducingAnswer = "the producing turn's exact delegated evidence"

// g5GoalTailFixture is an actually admitted, already-exited goal turn. The
// provider is the only double; admission, immutable handle, disposal barrier,
// lifecycle, goal, transcript, inbox and completion path all stay real.
// No test-only production hook is installed by the new N1/N2 tests.
type g5GoalTailFixture struct {
	al        *AgentLoop
	judge     *AgentInstance
	lifecycle *session.LifecycleStore
	inbox     *session.MessageInboxStore
	parentID  string
	original  *session.LifecycleRecord
	producer  *turnState
	worker    *r1CompletionProvider
}

func newG5GoalTailFixture(t *testing.T, callID string) *g5GoalTailFixture {
	t.Helper()
	worker := &r1CompletionProvider{
		answers: []string{g5ProducingAnswer, "the replacement turn's own evidence"},
		entered: make(chan int, 2), release: []chan struct{}{make(chan struct{}), make(chan struct{})},
	}
	al, judge := newGoalLoopTestLoop(t, worker, nil)
	t.Cleanup(worker.openAll)
	lifecycle, inbox := session.NewLifecycleStore(t.TempDir()), session.NewMessageInboxStore(t.TempDir())
	al.SetSessionMessagingStores(inbox, lifecycle)
	wireSteerCompletionDeps(t, al)
	mintGenuineBootEpochForLoop(t, al)
	parent, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "native-agent")
	if err != nil {
		t.Fatalf("SETUP parent session: %v", err)
	}
	launched, err := NewSteerLauncher(al).Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: parent.ID, TargetAgentID: "native-agent", Task: "produce delegated evidence",
		Origin: steer.Origin{Kind: steer.OriginKindDelegate, CallID: callID},
		Goal: &steer.GoalSpec{
			Criteria: []steer.Criterion{{Text: "the work is complete"}},
			DoD:      []steer.Criterion{{Text: "the evidence is sufficient"}},
		},
	})
	if err != nil {
		t.Fatalf("SETUP real Launch: %v", err)
	}
	dispatched, err := NewSteerLauncher(al).Dispatch(context.Background(), launched.SessionID, launched.Generation)
	if err != nil || dispatched.State != steer.DispatchRunning {
		t.Fatalf("SETUP real Dispatch = %+v, error = %v, want running", dispatched, err)
	}
	r1AwaitProvider(t, worker, 0)
	original, err := lifecycle.Load(launched.SessionID)
	if err != nil {
		t.Fatalf("SETUP load admitted child: %v", err)
	}
	producer := al.getActiveTurnState(original.SessionID)
	if producer == nil || original.ExecutionID == nil || al.tsExecutionClaim(producer, original.SessionID) != al.executionClaimFor(original) {
		t.Fatalf("SETUP real producing handle does not carry persisted admission: %+v", original.ExecutionID)
	}
	disposition := producer.opts.executionDisposition
	if disposition == nil {
		t.Fatal("SETUP real admission has no owning disposal barrier")
	}
	worker.open(0)
	select {
	case <-disposition.done:
	case <-time.After(10 * time.Second):
		t.Fatal("SETUP producing turn did not finish its real disposal tail")
	}
	original, err = lifecycle.Load(original.SessionID)
	if err != nil || original.State != session.LifecycleRunning || original.FinalDelivery != nil {
		t.Fatalf("SETUP exited goal turn = %+v, error = %v, want running pending a claim without final outbox", original, err)
	}
	return &g5GoalTailFixture{al: al, judge: judge, lifecycle: lifecycle, inbox: inbox,
		parentID: parent.ID, original: original, producer: producer, worker: worker}
}

// goalWork persists a REAL registered goal_claim result, then runs the existing
// post-turn claim driver with the original producing handle's options and exact
// provider answer. The test joins dispatchDeferredGoalAdjudication itself,
// rather than installing the global asynchronous-adjudication test hook.
func (f *g5GoalTailFixture) goalWork(t *testing.T) *goalDeferredAdjudicationWork {
	t.Helper()
	claimTool, ok := f.producer.agent.Tools.Get(tools.GoalClaimToolName)
	if !ok {
		t.Fatal("BLOCKED: registered goal_claim missing — required by the session-goal completion contract")
	}
	b6ClaimMetAndPersist(t, claimTool, f.al.GetSessionStore(), f.original.SessionID, "g5-producing-met-claim", g5ProducingAnswer)
	result := turnResult{finalContent: g5ProducingAnswer}
	f.al.checkGoalLoopAfterTurn(context.Background(), f.producer.agent, f.producer.opts, &result)
	work := result.goalDeferredAdjudication
	if work == nil || work.sessionID != f.original.SessionID || work.claimText != g5ProducingAnswer {
		t.Fatalf("SETUP original producing claim work = %+v, want session %q with exact provider evidence %q", work, f.original.SessionID, g5ProducingAnswer)
	}
	return work
}

// N1 oracle: frozen sub-agent control-plane ADR D2, Publish only a committed
// outbox: "surface the failure rather than claim delivery". A refused atomic
// commit publishes no final and has no committed outbox for boot to repair.
// An internal warning alone is not a returned or parent/operator-facing error.
func TestGoalTailG5_RefusedCommitIsVisibleAndNotSilentlyRunning(t *testing.T) {
	f := newG5GoalTailFixture(t, "g5-n1-real-admission")
	claim := f.al.tsExecutionClaim(f.producer, f.original.SessionID)
	journal := filepath.Join(f.lifecycle.Dir(), f.original.SessionID+".jsonl")
	if err := os.Chmod(journal, 0o400); err != nil {
		t.Fatalf("SETUP deny real lifecycle journal append: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(journal, 0o600); err != nil {
			t.Errorf("restore lifecycle journal permissions: %v", err)
		}
	})
	runErr := errors.New("judge unavailable after three attempts: the producing claim could not be adjudicated")
	// Instrument control: the same real commit must return a specific permission
	// error. If this user can bypass file permissions, the fixture is invalid,
	// not a passing test; no skip or fabricated store failure is allowed.
	_, controlErr := f.al.completeSteeredTurnDurably(context.Background(), f.original, turnResult{}, runErr, claim)
	if !errors.Is(controlErr, os.ErrPermission) {
		t.Fatalf("BLOCKED: real journal seam returned %v, want os.ErrPermission before testing swallowed goal-tail errors (D2/N1)", controlErr)
	}
	t.Logf("INSTRUMENT real atomic commit refused with os.ErrPermission: %v", controlErr)
	if woke := f.al.completeSteeredTurnAfterGoal(context.Background(), f.original.SessionID, "", runErr); woke {
		t.Error("refused goal-tail commit claimed a final wake, want false")
	}
	current, err := session.NewLifecycleStore(f.lifecycle.Dir()).Load(f.original.SessionID)
	if err != nil {
		t.Fatalf("reopen child after refused goal tail: %v", err)
	}
	if current.FinalDelivery != nil || current.Terminal() {
		t.Errorf("refused commit changed terminal/outbox state: state=%s final=%+v, want uncommitted", current.State, current.FinalDelivery)
	}
	messages, _, _, err := f.inbox.Drain(f.parentID, f.original.SessionID, "", 10)
	if err != nil {
		t.Fatalf("read parent-visible fault surface: %v", err)
	}
	visibleFailure := false
	for _, message := range messages {
		class, err := session.ClassifySessionMessage(message)
		if err != nil {
			t.Fatalf("classify parent message: %v", err)
		}
		if class.Kind == "handback" {
			t.Error("uncommitted goal-tail final reached the parent")
		}
		if class.Kind == "error" {
			fault, err := message.AsSessionMessageError()
			if err != nil {
				t.Fatalf("decode parent-visible fault: %v", err)
			}
			visibleFailure = visibleFailure || strings.Contains(strings.ToLower(fault.Text), "permission")
		}
	}
	for _, id := range []string{f.parentID, f.original.SessionID} {
		entries, err := f.al.GetSessionStore().ReadTranscript(id)
		if err != nil {
			t.Fatalf("read operator-visible transcript %q: %v", id, err)
		}
		for _, entry := range entries {
			visibleFailure = visibleFailure || strings.Contains(strings.ToLower(entry.Content), "permission")
		}
	}
	if !visibleFailure {
		t.Error("N1: refused goal-tail commit surfaced no permission failure to parent/operator; false wake is not an error result and an internal warning is insufficient")
	}
	if current.State == session.LifecycleRunning && !visibleFailure {
		t.Errorf("N1: child %s is silently running after its producing execution retired and its commit was refused", current.SessionID)
	}
	// Positive repair control: an unchanged producing claim can commit once the
	// real store is writable. The session-owned goal must survive that failure.
	if err := os.Chmod(journal, 0o600); err != nil {
		t.Fatalf("repair journal permissions: %v", err)
	}
	if err := f.al.completeSteeredTurnForExecution(context.Background(), f.original, turnResult{}, runErr, claim); err != nil {
		t.Fatalf("positive repair control: %v", err)
	}
	if g := goalRecordForSession(t, f.original.SessionID); g.State != generated.GoalStateActive {
		t.Errorf("repair control ended session-owned goal: %q, want active (D6)", g.State)
	}
}

// N2 oracle: frozen ADR D2, One outcome/publication commit boundary:
// "A late completion from a replaced execution cannot commit against the
// resumed run, even at the same generation." The replacement is admitted by
// real Dispatch while the old claim's Judge is blocked at the provider edge.
func TestGoalTailG5_OldAdjudicationCannotCompleteSameGenerationReplacement(t *testing.T) {
	t.Run("original owner positive control", func(t *testing.T) {
		f := newG5GoalTailFixture(t, "g5-n2-original-control")
		f.judge.Provider = &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
			return &providers.LLMResponse{Content: metGoalVerdict947(t, f.original)}, nil
		}}
		f.al.dispatchDeferredGoalAdjudication(f.goalWork(t))
		got, err := f.lifecycle.Load(f.original.SessionID)
		if err != nil || got.State != session.LifecycleCompleted || got.FinalDelivery == nil || got.FinalDelivery.CommitID != f.original.ExecutionID.RunID {
			t.Fatalf("original-owner control = %+v, error = %v, want completed under its exact producing run", got, err)
		}
		assertMetGoalChildParentInbox947(t, f.inbox, f.parentID, f.original)
		messages, _, _, err := f.inbox.Drain(f.parentID, f.original.SessionID, "", 10)
		if err != nil || len(messages) != 1 {
			t.Fatalf("original-owner unacked messages = %d, error = %v, want one final", len(messages), err)
		}
		handback, err := messages[0].AsSessionMessageHandback()
		if err != nil || handback.ResultSoFar != g5ProducingAnswer {
			t.Fatalf("original-owner exact answer = %+v, error = %v, want %q", handback, err, g5ProducingAnswer)
		}
	})
	t.Run("same generation replacement owns its unfinished turn", func(t *testing.T) {
		f := newG5GoalTailFixture(t, "g5-n2-replacement")
		work := f.goalWork(t)
		oldClaim := f.al.tsExecutionClaim(f.producer, f.original.SessionID)
		verdict := metGoalVerdict947(t, f.original)
		judgeEntered, releaseJudge, adjudicationDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
		var releaseOnce sync.Once
		openJudge := func() { releaseOnce.Do(func() { close(releaseJudge) }) }
		f.judge.Provider = &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
			close(judgeEntered)
			<-releaseJudge
			return &providers.LLMResponse{Content: verdict}, nil
		}}
		go func() { f.al.dispatchDeferredGoalAdjudication(work); close(adjudicationDone) }()
		t.Cleanup(func() {
			openJudge()
			select {
			case <-adjudicationDone:
			case <-time.After(10 * time.Second):
				t.Error("old adjudication did not return after its real provider was released")
			}
		})
		select {
		case <-judgeEntered:
		case <-time.After(10 * time.Second):
			t.Fatal("original claim did not reach the real Judge provider boundary")
		}
		dispatched, err := NewSteerLauncher(f.al).Dispatch(context.Background(), f.original.SessionID, f.original.Generation)
		if err != nil || dispatched.State != steer.DispatchRunning {
			t.Fatalf("real same-generation replacement Dispatch = %+v, error = %v, want running", dispatched, err)
		}
		r1AwaitProvider(t, f.worker, 1)
		replacement, err := f.lifecycle.Load(f.original.SessionID)
		if err != nil || replacement.ExecutionID == nil || replacement.Generation != f.original.Generation || reflect.DeepEqual(replacement.ExecutionID, f.original.ExecutionID) {
			t.Fatalf("SETUP replacement = %+v, error = %v, want fresh identity on original generation", replacement, err)
		}
		newHandle := f.al.getActiveTurnState(replacement.SessionID)
		if newHandle == nil || f.al.tsExecutionClaim(newHandle, replacement.SessionID) != f.al.executionClaimFor(replacement) {
			t.Fatal("SETUP replacement admission lacks its exact immutable producing handle")
		}
		t.Cleanup(func() {
			f.worker.open(1)
			select {
			case <-newHandle.opts.executionDisposition.done:
			case <-time.After(10 * time.Second):
				t.Error("replacement execution did not retire after provider release")
			}
		})
		openJudge()
		select {
		case <-adjudicationDone:
		case <-time.After(10 * time.Second):
			t.Fatal("old adjudication did not complete its goal tail")
		}
		got, err := session.NewLifecycleStore(f.lifecycle.Dir()).Load(replacement.SessionID)
		if err != nil {
			t.Fatalf("reopen replacement after old goal tail: %v", err)
		}
		if got.Generation != replacement.Generation || got.State != session.LifecycleRunning || !reflect.DeepEqual(got.ExecutionID, replacement.ExecutionID) || got.FinalDelivery != nil {
			t.Errorf("N2: old goal tail claimed unfinished replacement: generation=%d state=%s identity=%+v final=%+v; want G=%d running under %+v without final", got.Generation, got.State, got.ExecutionID, got.FinalDelivery, replacement.Generation, replacement.ExecutionID)
		}
		if gotClaim := f.al.tsExecutionClaim(f.producer, f.original.SessionID); gotClaim != oldClaim {
			t.Errorf("producing handle changed identity: %+v -> %+v, want immutable original", oldClaim, gotClaim)
		}
		entries, err := f.inbox.Entries(f.parentID)
		if err != nil {
			t.Fatalf("read parent inbox after old tail: %v", err)
		}
		finalID := fmt.Sprintf("%s:%d:final", replacement.SessionID, replacement.Generation)
		for _, entry := range entries {
			if entry.Message != nil && messageIDOf(*entry.Message) == finalID {
				t.Errorf("N2: old producing answer used replacement's same-generation final id %q before replacement returned", finalID)
			}
		}
	})
}

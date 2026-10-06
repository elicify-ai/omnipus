package agent

import (
	"bytes"
	"context"
	"os"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// This external provider deliberately does not cooperate with cancellation
// until released. It holds the producing turn while the REAL store read seam
// is delayed; it replaces neither Stop nor admission nor the Redirect waiter.
type qa2RedirectProvider struct{ r1CompletionProvider }

func (p *qa2RedirectProvider) Chat(ctx context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	p.mu.Lock()
	index := len(p.requests)
	p.requests = append(p.requests, append([]providers.Message(nil), messages...))
	p.mu.Unlock()
	if index >= len(p.answers) {
		return nil, providers.ErrProviderNeedsSignIn
	}
	p.entered <- index
	<-p.release[index]
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &providers.LLMResponse{Content: p.answers[index], FinishReason: "stop"}, nil
}

// A2. Oracle: control-plane ADR D2/D5/T27: every delayed effect belongs to
// its selected execution/control; a newer Stop must survive an old Redirect's
// resume half. Redirect R, both Stop landings, and explicit Resume B use the
// actual public entries. No direct call to awaitStoppedAndRevive is made.
func TestQAGate2Redirect_OldWaiterCannotReviveNewerStoppedRun(t *testing.T) {
	p := &qa2RedirectProvider{r1CompletionProvider{
		answers: []string{"original answer", "newer resumed answer", "obsolete redirect answer"},
		entered: make(chan int, 3), release: []chan struct{}{make(chan struct{}), make(chan struct{}), make(chan struct{})},
	}}
	al, closeLoop := newSteerALWithProvider(t, p)
	t.Cleanup(closeLoop)
	t.Cleanup(p.openAll)
	mintGenuineBootEpochForLoop(t, al)
	wireSteerCompletionDeps(t, al)
	parentID := newTestSteeringSession(t, al, "")
	launch, err := NewSteerLauncher(al).Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: parentID, TargetAgentID: testDefaultAgentID, Task: "original work",
		Origin: steer.Origin{Kind: steer.OriginKindDelegate, CallID: "qa2-a2-real-redirect"},
	})
	if err != nil {
		t.Fatalf("SETUP real Launch: %v", err)
	}
	dispatched, err := NewSteerLauncher(al).Dispatch(context.Background(), launch.SessionID, launch.Generation)
	if err != nil || dispatched.State != steer.DispatchRunning {
		t.Fatalf("SETUP real Dispatch = %+v/%v, want running", dispatched, err)
	}
	r1AwaitProvider(t, &p.r1CompletionProvider, 0)
	a := rootReopenedRecord(t, al, launch.SessionID)
	aHandle := al.getActiveTurnState(a.SessionID)
	qa2JoinHeldExecutionOnCleanup(t, al, a, func() { p.open(0) })

	// Reopen the SAME concrete journal as a delayed-reader seam. A's admitted
	// dependencies keep their original real store. R captures this reader;
	// subsequent live controls use the original concrete writer. This lets
	// real writes progress while ONLY R's old read is held, without a fake
	// store, timer override, global hook, or hand-written lifecycle state.
	writer := al.GetSessionLifecycleStore()
	reader := session.NewLifecycleStore(writer.Dir())
	al.SetSessionMessagingStores(al.GetMessageInboxStore(), reader)
	al.SetSteerCanceller(NewSteerCanceller(reader, al.SteerGenerationCancel))
	const oldInstruction = "obsolete redirect R must never override the newer Stop"
	by := steer.Principal{Kind: steer.PrincipalKindHuman, ID: "qa2-a2-owner"}
	if err := al.RedirectSteeredSession(context.Background(), a.SessionID, by, oldInstruction); err != nil {
		t.Fatalf("SETUP real Redirect R acceptance: %v", err)
	}
	readLock := reader.Lock(a.SessionID)
	readLock.Lock()
	var unlockOnce sync.Once
	unlockReader := func() { unlockOnce.Do(readLock.Unlock) }
	t.Cleanup(unlockReader)
	qa2AwaitRedirectReadBlocked(t)
	al.SetSessionMessagingStores(al.GetMessageInboxStore(), writer)
	al.SetSteerCanceller(NewSteerCanceller(writer, al.SteerGenerationCancel))
	p.open(0)
	qa2AwaitDisposed(t, aHandle.opts.executionDisposition.done, "R's selected original execution")
	landedA := rootReopenedRecord(t, al, a.SessionID)
	if landedA.State != session.LifecycleStopped || landedA.Stop != nil {
		t.Fatalf("SETUP R's original Stop did not land: %+v", landedA)
	}

	const newInstruction = "newer explicit Resume B"
	revived, err := al.ReviveStoppedSession(context.Background(), a.SessionID, by, newInstruction)
	if err != nil || !revived {
		t.Fatalf("SETUP real newer Resume B = %v/%v, want admitted", revived, err)
	}
	r1AwaitProvider(t, &p.r1CompletionProvider, 1)
	b := rootReopenedRecord(t, al, a.SessionID)
	if b.Generation != a.Generation || b.ExecutionID == nil || reflect.DeepEqual(b.ExecutionID, a.ExecutionID) {
		t.Fatalf("SETUP B = %+v, want same generation and a fresh producing run", b)
	}
	bHandle := al.getActiveTurnState(b.SessionID)
	qa2JoinHeldExecutionOnCleanup(t, al, b, func() { p.open(1) })
	stopped, err := al.StopSession(context.Background(), StopRequest{SessionID: b.SessionID, By: by, Channel: "agent"})
	if err != nil || stopped.RootErr != nil || len(stopped.Report.Unreachable) != 0 || len(stopped.Report.Reached) != 1 {
		t.Fatalf("SETUP genuinely newer Stop = %+v/%v, want accepted for B", stopped, err)
	}
	p.open(1)
	qa2AwaitDisposed(t, bHandle.opts.executionDisposition.done, "newer stopped B")
	newStop := rootReopenedRecord(t, al, b.SessionID)
	if newStop.State != session.LifecycleStopped || newStop.Stop != nil || newStop.StopNote == nil || !reflect.DeepEqual(newStop.ExecutionID, b.ExecutionID) {
		t.Fatalf("SETUP newer Stop did not land on B: %+v", newStop)
	}
	journalPath := qaReceiptJournalPath(al, b.SessionID)
	before, err := os.ReadFile(journalPath)
	if err != nil {
		t.Fatal(err)
	}

	unlockReader()
	qa2AwaitRedirectRequestFinished(t, al)
	got := rootReopenedRecord(t, al, b.SessionID)
	after, err := os.ReadFile(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) || got.State != session.LifecycleStopped || !reflect.DeepEqual(got.StopNote, newStop.StopNote) || !reflect.DeepEqual(got.ExecutionID, b.ExecutionID) {
		t.Errorf("A2: old Redirect revived newer stopped B: state=%s note=%+v identity=%+v; want B's stopped execution/note and unchanged journal", got.State, got.StopNote, got.ExecutionID)
	}
	entries, err := al.GetSessionStore().ReadTranscript(b.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Role == "user" && entry.Content == oldInstruction {
			t.Error("A2: obsolete R instruction was appended after the newer Stop; want undelivered")
		}
	}
	if calls := len(p.Requests()); calls != 2 {
		t.Errorf("A2: obsolete waiter dispatched another provider turn: calls=%d, want exactly original A and explicit B", calls)
	}
	parentEntries, err := al.GetSessionStore().ReadTranscript(parentID)
	if err != nil {
		t.Fatal(err)
	}
	// The ADR requires a visible refusal of THIS child's Redirect, not one
	// fixed prose sentence. With both healthy Stops joined above and the
	// no-revival/no-instruction assertions, count the child-specific typed
	// error notice without copying reportUndeliveredRedirect's wording.
	undelivered := 0
	for _, entry := range parentEntries {
		frame := entry.SubagentMessage
		if frame != nil && frame.Kind == "error" && frame.ChildSessionId != nil && *frame.ChildSessionId == b.SessionID && frame.Text != nil && strings.TrimSpace(*frame.Text) != "" {
			undelivered++
		}
	}
	if undelivered != 1 {
		t.Errorf("A2: parent-visible undelivered Redirect notices=%d, want exactly 1; rejecting stale R must not be log-only", undelivered)
	}
}

func qa2AwaitDisposed(t *testing.T, done <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("%s did not finish its real owning disposal barrier", what)
	}
}

func qa2AwaitRedirectReadBlocked(t *testing.T) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		buf := make([]byte, 1<<20)
		n := runtime.Stack(buf, true)
		for _, stack := range strings.Split(string(buf[:n]), "\n\n") {
			if strings.Contains(stack, ".awaitStoppedAndRevive(") && strings.Contains(stack, "(*LifecycleStore).Load(") && strings.Contains(stack, "sync.(*Mutex).Lock") {
				t.Log("INSTRUMENT: R's actual waiter is blocked at the concrete lifecycle read seam")
				return
			}
		}
		select {
		case <-deadline.C:
			t.Fatal("BLOCKED: old Redirect waiter never reached the held real lifecycle read; cannot claim the required ordering")
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func qa2AwaitRedirectRequestFinished(t *testing.T, al *AgentLoop) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		al.activeRequests.mu.Lock()
		active := al.activeRequests.active
		al.activeRequests.mu.Unlock()
		if active == 0 {
			return
		}
		select {
		case <-deadline.C:
			t.Fatal("old Redirect request did not finish after its real read seam was released")
		case <-time.After(5 * time.Millisecond):
		}
	}
}

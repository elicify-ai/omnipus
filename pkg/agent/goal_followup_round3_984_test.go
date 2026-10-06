package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

func TestGoal984_StoredNotWokenHandbackFallsBackToOneVerdictWake(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	wireSteerCompletionDeps(t, al)
	parentID := newTestSteeringSession(t, al, "ws-1")
	child := g1AdmitCompletionChild(t, al, parentID, "call-f1-stored-not-woken")
	inbox := al.GetMessageInboxStore()
	const goalID = "goal-f1-stored-not-woken"
	verdictID := appendTestMetVerdict(t, inbox, parentID, child.SessionID, goalID, 1)

	wireTerminalReportDeliverer(al, &recordingUpwardDeliverer{delivery: steer.Delivery{
		MessageID: fmt.Sprintf("%s:%d:final", child.SessionID, child.Generation),
		Outcome:   steer.DeliveryStoredNotWoken,
	}})
	woke, err := al.deliverSteeredCompletion(context.Background(), child, steer.OutcomeFinalAnswer,
		session.LifecycleCompleted, "finished", "")
	if err != nil {
		t.Fatalf("deliverSteeredCompletion: %v", err)
	}
	if woke {
		t.Fatal("stored-not-woken hand-back reported a wake-producing delivery")
	}
	assertUnackedMessageIDs(t, inbox, parentID, child.SessionID, verdictID)

	wireSteerCompletionDeps(t, al)
	var mu sync.Mutex
	var wakeIDs []string
	al.asyncNotifier.registerObserver(func(event AsyncNotifyEvent) {
		mu.Lock()
		defer mu.Unlock()
		wakeIDs = append(wakeIDs, fmt.Sprint(event.Metadata["steer_message_id"]))
	})
	if err := al.wakeMetVerdictEntry(child.SessionID, goalID, 1); err != nil {
		t.Fatalf("wakeMetVerdictEntry: %v", err)
	}
	mu.Lock()
	gotWakeIDs := append([]string(nil), wakeIDs...)
	mu.Unlock()
	if len(gotWakeIDs) != 1 || gotWakeIDs[0] != verdictID {
		t.Fatalf("parent wakes = %v, want exactly verdict %q", gotWakeIDs, verdictID)
	}
	assertUnackedMessageIDs(t, inbox, parentID, child.SessionID, verdictID)
}

func TestGoal984_WakeMetVerdictLeavesEntryUnackedWhenParentStopped(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	wireSteerCompletionDeps(t, al)
	parentID := newTestSteeringSession(t, al, "ws-1")
	child := launchRunningChild(t, al, parentID, "call-f1-parent-stopped")
	inbox := al.GetMessageInboxStore()
	const goalID = "goal-f1-parent-stopped"
	verdictID := appendTestMetVerdict(t, inbox, parentID, child.SessionID, goalID, 1)

	lifecycle := al.GetSessionLifecycleStore()
	if err := lifecycle.Mutate(parentID, func(rec *session.LifecycleRecord) error {
		rec.Stop = &session.Stop{
			At: time.Now().UTC(), Generation: rec.Generation,
			By: session.Principal{Kind: session.PrincipalKindHuman, ID: "operator"},
		}
		return nil
	}); err != nil {
		t.Fatalf("stop parent: %v", err)
	}
	if err := al.wakeMetVerdictEntry(child.SessionID, goalID, 1); err == nil {
		t.Fatal("stored-not-woken fallback reported success for a stopped parent")
	}
	assertUnackedMessageIDs(t, inbox, parentID, child.SessionID, verdictID)
}

func TestGoal984_PausedPreflightCallerDoesNotRedeliverAfterCompletion(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	wireSteerCompletionDeps(t, al)
	parentID := newTestSteeringSession(t, al, "ws-1")
	child := g1AdmitCompletionChild(t, al, parentID, "call-f4-paused-preflight")

	firstArrived := make(chan struct{})
	secondArrived := make(chan struct{})
	releaseFirst := make(chan struct{})
	releaseSecond := make(chan struct{})
	var hookMu sync.Mutex
	hookCalls := 0
	completeBeforeDeliveryTestHook = func(sessionID string) {
		if sessionID != child.SessionID {
			return
		}
		hookMu.Lock()
		hookCalls++
		call := hookCalls
		hookMu.Unlock()
		switch call {
		case 1:
			close(firstArrived)
			<-releaseFirst
		case 2:
			close(secondArrived)
			<-releaseSecond
		}
	}
	t.Cleanup(func() { completeBeforeDeliveryTestHook = nil })

	var wakeMu sync.Mutex
	var wakeIDs []string
	al.asyncNotifier.registerObserver(func(event AsyncNotifyEvent) {
		wakeMu.Lock()
		defer wakeMu.Unlock()
		wakeIDs = append(wakeIDs, fmt.Sprint(event.Metadata["steer_message_id"]))
	})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- al.completeSteeredTurn(context.Background(), child, turnResult{finalContent: "first"}, nil)
	}()
	<-firstArrived
	secondDone := make(chan error, 1)
	go func() {
		secondDone <- al.completeSteeredTurn(context.Background(), child, turnResult{finalContent: "stale second"}, nil)
	}()
	<-secondArrived
	close(releaseFirst)
	if err := <-firstDone; err != nil {
		t.Fatalf("first completion: %v", err)
	}
	close(releaseSecond)
	if err := <-secondDone; err != nil {
		t.Fatalf("paused completion: %v", err)
	}

	wakeMu.Lock()
	gotWakeIDs := append([]string(nil), wakeIDs...)
	wakeMu.Unlock()
	wantID := fmt.Sprintf("%s:%d:final", child.SessionID, child.Generation)
	if len(gotWakeIDs) != 1 || gotWakeIDs[0] != wantID {
		t.Fatalf("parent wakes = %v, want one final wake %q", gotWakeIDs, wantID)
	}
}

func TestGoal984_StaleGenerationCompletionCannotOccupyRevivedFinal(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	wireSteerCompletionDeps(t, al)
	parentID := newTestSteeringSession(t, al, "ws-1")
	childGen1 := g1AdmitCompletionChild(t, al, parentID, "call-f4-stale-generation")

	staleArrived := make(chan struct{})
	releaseStale := make(chan struct{})
	completeBeforeDeliveryTestHook = func(sessionID string) {
		if sessionID == childGen1.SessionID {
			close(staleArrived)
			<-releaseStale
		}
	}
	t.Cleanup(func() { completeBeforeDeliveryTestHook = nil })

	staleDone := make(chan error, 1)
	go func() {
		staleDone <- al.completeSteeredTurn(context.Background(), childGen1, turnResult{finalContent: "stale generation one"}, nil)
	}()
	<-staleArrived
	lifecycle := al.GetSessionLifecycleStore()
	// Frozen D2 CRIT-001: an explicit RESUME "changes the same-generation
	// state to queued, and installs a new execution identity". The superseded
	// fixture revived a forged in-flight fence as generation two. Land the
	// stop through its real owner first; the old snapshot remains the stale
	// producer even though the resumed final uses the SAME generation/id.
	stopped := g1StopThroughOwner(t, al, childGen1)
	gen2, err := NewSteerCanceller(lifecycle).Revive(context.Background(), childGen1.SessionID,
		steer.Principal{Kind: steer.PrincipalKindHuman, ID: "operator"})
	if err != nil {
		t.Fatalf("Revive: %v", err)
	}
	if gen2 != childGen1.Generation || stopped.Generation != childGen1.Generation {
		t.Fatalf("stopped RESUME generation=%d, want original generation %d", gen2, childGen1.Generation)
	}
	queued, err := lifecycle.Load(childGen1.SessionID)
	if err != nil || queued.State != session.LifecycleQueued || queued.Stop != nil || queued.StopNote != nil {
		t.Fatalf("same-generation resume did not atomically clear note/fence into queued: %+v error=%v", queued, err)
	}
	replacement := g1AdmitResumedChild(t, al, queued)
	if replacement.ExecutionID.RunID == childGen1.ExecutionID.RunID || replacement.ExecutionID.BootSeq != childGen1.ExecutionID.BootSeq {
		t.Fatalf("resumed execution=%+v, want fresh run in the same genuine boot (old=%+v)", replacement.ExecutionID, childGen1.ExecutionID)
	}
	close(releaseStale)
	if staleErr := <-staleDone; staleErr != nil {
		t.Fatalf("stale completion: %v", staleErr)
	}

	entries, err := al.GetMessageInboxStore().Entries(parentID)
	if err != nil {
		t.Fatalf("Entries(parent): %v", err)
	}
	wantGen2ID := fmt.Sprintf("%s:%d:final", childGen1.SessionID, gen2)
	for _, entry := range entries {
		if entry.Message != nil && messageIDOf(*entry.Message) == wantGen2ID {
			t.Fatalf("stale generation-one caller occupied revived final id %q", wantGen2ID)
		}
	}

	completeBeforeDeliveryTestHook = nil
	childGen2, err := lifecycle.Load(childGen1.SessionID)
	if err != nil {
		t.Fatalf("Load resumed execution: %v", err)
	}
	if completeErr := al.completeSteeredTurn(context.Background(), childGen2, turnResult{finalContent: "legitimate generation two"}, nil); completeErr != nil {
		t.Fatalf("resumed-execution completion: %v", completeErr)
	}
	entries, err = al.GetMessageInboxStore().Entries(parentID)
	if err != nil {
		t.Fatalf("Entries(parent after resumed execution): %v", err)
	}
	var gen2Handbacks int
	for _, entry := range entries {
		if entry.Message == nil || messageIDOf(*entry.Message) != wantGen2ID {
			continue
		}
		handback, decodeErr := entry.Message.AsSessionMessageHandback()
		if decodeErr != nil {
			t.Fatalf("AsSessionMessageHandback: %v", decodeErr)
		}
		gen2Handbacks++
		if handback.ResultSoFar != "legitimate generation two" {
			t.Fatalf("resumed-execution hand-back result = %q, want legitimate result", handback.ResultSoFar)
		}
	}
	if gen2Handbacks != 1 {
		t.Fatalf("resumed-execution hand-backs = %d, want 1", gen2Handbacks)
	}
}

func TestGoal984_CompletionFlightsRemovedOnEveryExit(t *testing.T) {
	terminalWriteErr := errors.New("injected terminal write failure")
	tests := []struct {
		name     string
		complete func(*AgentLoop, *session.LifecycleRecord) (bool, error)
		wantErr  error
	}{
		{name: "success", complete: func(*AgentLoop, *session.LifecycleRecord) (bool, error) { return true, nil }},
		{name: "stop", complete: func(al *AgentLoop, rec *session.LifecycleRecord) (bool, error) {
			err := al.GetSessionLifecycleStore().Mutate(rec.SessionID, func(cur *session.LifecycleRecord) error {
				cur.Stop = &session.Stop{At: time.Now().UTC(), Generation: cur.Generation,
					By: session.Principal{Kind: session.PrincipalKindHuman, ID: "operator"}}
				return nil
			})
			return true, err
		}},
		{name: "revive", complete: func(al *AgentLoop, rec *session.LifecycleRecord) (bool, error) {
			if err := al.GetSessionLifecycleStore().Mutate(rec.SessionID, func(cur *session.LifecycleRecord) error {
				cur.Stop = &session.Stop{At: time.Now().UTC(), Generation: cur.Generation,
					By: session.Principal{Kind: session.PrincipalKindHuman, ID: "operator"}}
				return nil
			}); err != nil {
				return true, err
			}
			_, err := NewSteerCanceller(al.GetSessionLifecycleStore()).Revive(context.Background(), rec.SessionID,
				steer.Principal{Kind: steer.PrincipalKindHuman, ID: "operator"})
			return true, err
		}},
		{name: "terminal write failure", complete: func(*AgentLoop, *session.LifecycleRecord) (bool, error) {
			return true, terminalWriteErr
		}, wantErr: terminalWriteErr},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			al, cleanup := newSteerAL(t)
			defer cleanup()
			boot := session.NewBootEpochStore(al.GetConfig().Agents.Defaults.Home)
			bootSeq, err := boot.Mint()
			if err != nil {
				t.Fatalf("Mint(boot epoch): %v", err)
			}
			al.SetBootEpochStore(boot)
			provider, _ := installParkedProvider(t, al)
			wireSteerCompletionDeps(t, al)
			parentID := newTestSteeringSession(t, al, "ws-1")
			launcher := NewSteerLauncher(al)
			child, err := launcher.Launch(context.Background(), steer.LaunchRequest{
				SteeringSessionID: parentID,
				TargetAgentID:     testDefaultAgentID,
				Task:              "do delegated work",
				Origin:            steer.Origin{Kind: steer.OriginKindDelegate, CallID: "call-f4-cleanup-" + tt.name},
			})
			if err != nil {
				t.Fatalf("Launch: %v", err)
			}
			dispatched, err := launcher.Dispatch(context.Background(), child.SessionID, child.Generation)
			if err != nil {
				t.Fatalf("Dispatch: %v", err)
			}
			if dispatched.State != steer.DispatchRunning || dispatched.Generation != child.Generation {
				t.Fatalf("Dispatch = %+v, want running generation %d", dispatched, child.Generation)
			}
			select {
			case <-provider.entered:
			case <-time.After(10 * time.Second):
				t.Fatal("producing turn did not reach the parked provider")
			}
			rec, err := al.GetSessionLifecycleStore().Load(child.SessionID)
			if err != nil {
				t.Fatalf("Load(producing child): %v", err)
			}
			if rec.ExecutionID == nil || rec.ExecutionID.RunID == "" || bootSeq == 0 {
				t.Fatal("producing child has no genuine admission identity")
			}
			producer := al.getActiveTurnState(child.SessionID)
			if producer == nil {
				t.Fatal("producing child has no registered execution handle")
			}
			// D2's full tuple is captured BEFORE Stop/Revive. In particular, a
			// replacement's current lifecycle owner cannot change this key.
			claim := al.tsExecutionClaim(producer, child.SessionID)
			wantClaim := executionClaim{SessionID: child.SessionID, Generation: child.Generation,
				BootSeq: bootSeq, RunID: rec.ExecutionID.RunID}
			if claim != wantClaim || rec.ExecutionID.BootSeq != bootSeq {
				t.Fatalf("producer claim = %+v, record identity = %+v, want %+v", claim, rec.ExecutionID, wantClaim)
			}
			key := steeredCompletionFlightKey{loop: al, sessionID: claim.SessionID, generation: claim.Generation,
				bootSeq: claim.BootSeq, runID: claim.RunID}
			calls := 0
			woke, completionErr := al.runSteeredCompletionOnce(rec, claim, func() (bool, error) {
				calls++
				// Positive control: absence after return is meaningful only if
				// this exact producing flight was present during completion.
				if _, present := steeredCompletionFlights.Load(key); !present {
					t.Fatalf("producing completion flight missing during %s: %+v", tt.name, key)
				}
				return tt.complete(al, rec)
			})
			if calls != 1 {
				t.Fatalf("completion calls = %d, want exactly 1", calls)
			}
			if !woke || !errors.Is(completionErr, tt.wantErr) {
				t.Fatalf("completion result = (%v, %v), want (true, %v)", woke, completionErr, tt.wantErr)
			}
			if _, retained := steeredCompletionFlights.Load(key); retained {
				steeredCompletionFlights.Delete(key)
				t.Fatalf("completion flight retained after %s: %+v", tt.name, key)
			}
			steeredCompletionFlights.Range(func(rawKey, _ any) bool {
				if other, ok := rawKey.(steeredCompletionFlightKey); ok && other.loop == al {
					steeredCompletionFlights.Delete(rawKey)
					t.Errorf("unexpected completion flight retained after %s: %+v", tt.name, other)
				}
				return true
			})
		})
	}
}

func TestGoal984_DeliveredFinalRetriesTerminalWriteWithoutSecondWake(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	wireSteerCompletionDeps(t, al)
	parentID := newTestSteeringSession(t, al, "ws-1")
	child := g1AdmitCompletionChild(t, al, parentID, "call-f4-terminal-write-retry")
	lifecycle := al.GetSessionLifecycleStore()

	var wakeMu sync.Mutex
	var wakeIDs []string
	al.asyncNotifier.registerObserver(func(event AsyncNotifyEvent) {
		wakeMu.Lock()
		defer wakeMu.Unlock()
		wakeIDs = append(wakeIDs, fmt.Sprint(event.Metadata["steer_message_id"]))
	})

	lifecyclePath := filepath.Join(lifecycle.Dir(), child.SessionID+".jsonl")
	backupPath := lifecyclePath + ".write-failure"
	var injectErr error
	completeStateWriteTestHook = func(sessionID string) {
		if sessionID != child.SessionID {
			return
		}
		completeStateWriteTestHook = nil
		if err := os.Rename(lifecyclePath, backupPath); err != nil {
			injectErr = fmt.Errorf("rename lifecycle record: %w", err)
			return
		}
		if err := os.Mkdir(lifecyclePath, 0o700); err != nil {
			injectErr = fmt.Errorf("replace lifecycle record with directory: %w", err)
		}
	}
	t.Cleanup(func() { completeStateWriteTestHook = nil })

	firstErr := al.completeSteeredTurn(context.Background(), child, turnResult{finalContent: "finished"}, nil)
	if injectErr != nil {
		t.Fatalf("inject terminal write failure: %v", injectErr)
	}
	if firstErr == nil {
		t.Fatal("first completion succeeded; injected terminal write failure did not reach the lifecycle write")
	}
	if err := os.Remove(lifecyclePath); err != nil {
		t.Fatalf("remove injected lifecycle directory: %v", err)
	}
	if err := os.Rename(backupPath, lifecyclePath); err != nil {
		t.Fatalf("restore lifecycle record: %v", err)
	}
	wantID := fmt.Sprintf("%s:%d:final", child.SessionID, child.Generation)
	// Frozen D2: "A producer must not call the upward deliverer before
	// this commit." The old fixture acknowledged a final before a failed
	// terminal write; that publication-before-commit premise is superseded.
	entries, entriesErr := al.GetMessageInboxStore().Entries(parentID)
	if entriesErr != nil || len(entries) != 0 {
		t.Fatalf("parent inbox before successful terminal commit = %d, error=%v, want 0", len(entries), entriesErr)
	}
	wakeMu.Lock()
	preCommitWakes := len(wakeIDs)
	wakeMu.Unlock()
	if preCommitWakes != 0 {
		t.Fatalf("parent wakes before successful terminal commit = %d, want 0", preCommitWakes)
	}

	if secondErr := al.completeSteeredTurn(context.Background(), child, turnResult{finalContent: "finished"}, nil); secondErr != nil {
		t.Fatalf("retry completion: %v", secondErr)
	}

	if ackErr := al.GetMessageInboxStore().Ack(parentID, []string{wantID}); ackErr != nil {
		t.Fatalf("ack genuinely committed and delivered final before publication retry: %v", ackErr)
	}
	// D2: "A genuinely acknowledged matching id means this committed result
	// was already consumed; do not send a second final." Preserve the original
	// no-second-wake oracle with an actual post-consumption publisher retry.
	if woke, retryErr := g1RetryCommittedFinal(t, al, child); retryErr != nil || woke {
		t.Fatalf("acknowledged committed-final retry = woke %v, error=%v, want no wake and no error", woke, retryErr)
	}
	wakeMu.Lock()
	gotWakeIDs := append([]string(nil), wakeIDs...)
	wakeMu.Unlock()
	if len(gotWakeIDs) != 1 || gotWakeIDs[0] != wantID {
		t.Fatalf("parent wakes = %v, want exactly one deterministic final wake %q across both attempts", gotWakeIDs, wantID)
	}
	got, err := lifecycle.Load(child.SessionID)
	if err != nil {
		t.Fatalf("Load(child after retry): %v", err)
	}
	if got.State != session.LifecycleCompleted {
		t.Fatalf("child state after retry = %q, want %q", got.State, session.LifecycleCompleted)
	}
}

func TestGoal1000_UnackedStoredFinalWithoutWakeRedeliversIntoFullLiveQueue(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	wireSteerCompletionDeps(t, al)
	parentID := newTestSteeringSession(t, al, "ws-1")
	child := g1AdmitCompletionChild(t, al, parentID, "call-n1-unacked-redelivery")
	inbox := al.GetMessageInboxStore()

	// Reproduce the durable state left by the round-5 failure: Deliver stored
	// the deterministic final, but the old ten-item cap refused its live-turn
	// wake. The retry must not mistake entry existence for successful waking.
	// D2: "Publish only a committed outbox." A stray inbox final without
	// its matching outcome/outbox commit is not a valid redelivery fixture.
	committed, commitErr := al.commitSteeredCompletion(al.GetSessionLifecycleStore(), child,
		session.LifecycleCompleted, steer.OutcomeFinalAnswer, "finished", "", al.executionClaimFor(child), nil)
	if commitErr != nil || committed.kind != steeredCommitTerminal || committed.commit == nil {
		t.Fatalf("SETUP real unpublished terminal/outbox commit = %+v, error=%v", committed, commitErr)
	}
	message := committed.message
	wantID := fmt.Sprintf("%s:%d:final", child.SessionID, child.Generation)
	if _, appendErr := inbox.Append(parentID, message); appendErr != nil {
		t.Fatalf("append stored final: %v", appendErr)
	}

	full := make([]steeringQueueItem, MaxQueueSize)
	for i := range full {
		full[i] = steeringQueueItem{message: providers.Message{Role: "user", Content: fmt.Sprintf("ordinary-%03d", i)}}
	}
	al.steering.mu.Lock()
	al.steering.queues[parentID] = full
	al.steering.mu.Unlock()
	al.activeTurnStates.Store(parentID, &turnState{sessionKey: parentID})
	t.Cleanup(func() { al.activeTurnStates.Delete(parentID) })

	if _, completeErr := g1RetryCommittedFinal(t, al, child); completeErr != nil {
		t.Fatalf("retry exact committed final: %v", completeErr)
	}

	al.steering.mu.Lock()
	items := append([]steeringQueueItem(nil), al.steering.queues[parentID]...)
	al.steering.mu.Unlock()
	if len(items) != MaxQueueSize+1 {
		t.Fatalf("queue length after retry = %d, want %d (stored final was not re-woken)", len(items), MaxQueueSize+1)
	}
	if gotWake := items[len(items)-1].wake; gotWake == nil || gotWake.messageID != wantID {
		t.Fatalf("retry wake = %+v, want message id %q", gotWake, wantID)
	}
	got, loadErr := al.GetSessionLifecycleStore().Load(child.SessionID)
	if loadErr != nil {
		t.Fatalf("Load(child after retry): %v", loadErr)
	}
	if got.State != session.LifecycleCompleted {
		t.Fatalf("child state after retry = %q, want %q", got.State, session.LifecycleCompleted)
	}
}

func appendTestMetVerdict(t *testing.T, inbox *session.MessageInboxStore, parentID, childID, goalID string, round int) string {
	t.Helper()
	id := goalVerdictUpwardMessageID(goalID, round)
	var message generated.SessionMessage
	if err := message.FromSessionMessageGoalStatus(generated.SessionMessageGoalStatus{
		Kind: generated.SessionMessageGoalStatusKindGoalStatus, MessageId: id, SessionId: childID,
		SenderIdentity: "judge", CreatedAt: time.Now().UTC(), Depth: 1, GoalId: goalID,
		Condition: generated.SessionMessageGoalStatusConditionMet,
		Direction: generated.SessionMessageGoalStatusDirectionSessionToParent,
	}); err != nil {
		t.Fatalf("encode goal verdict: %v", err)
	}
	if _, err := inbox.Append(parentID, message); err != nil {
		t.Fatalf("append goal verdict: %v", err)
	}
	return id
}

func assertUnackedMessageIDs(t *testing.T, inbox *session.MessageInboxStore, ownerKey, childID string, want ...string) {
	t.Helper()
	messages, _, _, err := inbox.Drain(ownerKey, childID, "", 32)
	if err != nil {
		t.Fatalf("Drain(%q): %v", ownerKey, err)
	}
	got := make([]string, 0, len(messages))
	for _, message := range messages {
		got = append(got, messageIDOf(message))
	}
	if !slices.Equal(got, want) {
		t.Fatalf("unacknowledged message ids = %v, want %v", got, want)
	}
}

package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
)

func TestGoal1000_TerminalWriteRetryBeforeConsumptionQueuesOneWakeAndOneParentTurn(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	wireSteerCompletionDeps(t, al)
	parentID := newTestSteeringSession(t, al, "ws-1")
	child := launchRunningChild(t, al, parentID, "call-f4-terminal-write-retry-before-consumption")
	lifecycle := al.GetSessionLifecycleStore()

	al.activeTurnStates.Store(parentID, &turnState{sessionKey: parentID})
	t.Cleanup(func() { al.activeTurnStates.Delete(parentID) })

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

	// Retry before the live parent consumes the first queued wake. The inbox
	// entry is still unacknowledged, so Deliver legitimately reaches the wake
	// path again; queue admission must coalesce the repeated message id.
	if secondErr := al.completeSteeredTurn(context.Background(), child, turnResult{finalContent: "finished"}, nil); secondErr != nil {
		t.Fatalf("retry completion: %v", secondErr)
	}

	wantID := fmt.Sprintf("%s:%d:final", child.SessionID, child.Generation)
	al.steering.mu.Lock()
	queued := append([]steeringQueueItem(nil), al.steering.queues[parentID]...)
	al.steering.mu.Unlock()
	if len(queued) != 1 {
		t.Fatalf("queued wakes after terminal-write retry = %d, want 1", len(queued))
	}
	if queued[0].wake == nil || queued[0].wake.messageID != wantID {
		t.Fatalf("queued wake = %+v, want message id %q", queued[0].wake, wantID)
	}

	// sessionWorker.processTurn calls Continue once per non-empty dequeue in
	// one-at-a-time mode. Count those exact continuation batches without
	// introducing an unrelated provider turn into this queue regression.
	parentTurns := 0
	for {
		_, items := al.steering.dequeueItemsScope(parentID)
		if len(items) == 0 {
			break
		}
		parentTurns++
	}
	if parentTurns != 1 {
		t.Fatalf("parent turns from queued completion wakes = %d, want 1", parentTurns)
	}
}

func TestGoal1000_DistinctWakeIDsRemainFIFO(t *testing.T) {
	const wakeCount = 17
	sq := newSteeringQueue(SteeringAll)

	for i := 0; i < wakeCount; i++ {
		messageID := fmt.Sprintf("wake-%02d", i)
		if err := sq.pushItemScope("parent", steeringQueueItem{
			message: providers.Message{Role: "user", Content: messageID},
			wake:    &steeringWake{messageID: messageID},
		}); err != nil {
			t.Fatalf("push distinct wake %d: %v", i, err)
		}
	}

	_, items := sq.dequeueItemsScope("parent")
	if len(items) != wakeCount {
		t.Fatalf("distinct queued wakes = %d, want %d", len(items), wakeCount)
	}
	for i, item := range items {
		wantID := fmt.Sprintf("wake-%02d", i)
		if item.wake == nil || item.wake.messageID != wantID {
			t.Fatalf("wake %d = %+v, want message id %q", i, item.wake, wantID)
		}
	}
}

func TestGoal1000_ConsumedWakeIDCanBeQueuedAgain(t *testing.T) {
	sq := newSteeringQueue(SteeringOneAtATime)
	item := steeringQueueItem{
		message: providers.Message{Role: "user", Content: "child completed"},
		wake:    &steeringWake{messageID: "wake-reusable-after-consumption"},
	}

	if err := sq.pushItemScope("parent", item); err != nil {
		t.Fatalf("push first wake: %v", err)
	}
	_, consumed := sq.dequeueItemsScope("parent")
	if len(consumed) != 1 {
		t.Fatalf("consumed wakes = %d, want 1", len(consumed))
	}
	if err := sq.pushItemScope("parent", item); err != nil {
		t.Fatalf("requeue consumed wake id: %v", err)
	}
	if got := sq.lenScope("parent"); got != 1 {
		t.Fatalf("queued wakes after reusing consumed id = %d, want 1", got)
	}
}

func TestGoal1000_MarkerFailureRestoreCoalescesConcurrentWakeByID(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	al.steering = newSteeringQueue(SteeringAll)

	const (
		scope     = "parent"
		messageID = "wake-marker-failure-retry"
	)
	original := steeringQueueItem{
		message: providers.Message{Role: "user", Content: "original delivery"},
		wake: &steeringWake{
			messageID:           messageID,
			transcriptSessionID: "missing-transcript",
			agentID:             "mia",
		},
	}
	if err := al.steering.pushItemScope(scope, original); err != nil {
		t.Fatalf("push original wake: %v", err)
	}

	actualScope, dequeued := al.steering.dequeueItemsScope(scope)
	if len(dequeued) != 1 {
		t.Fatalf("dequeued wakes = %d, want 1", len(dequeued))
	}
	retry := original
	retry.message.Content = "concurrent retry"
	if err := al.steering.pushItemScope(scope, retry); err != nil {
		t.Fatalf("push concurrent retry: %v", err)
	}

	msgs, correlationIDs, consumedItems := al.consumeDequeuedSteering(actualScope, dequeued)
	if len(msgs) != 0 || len(correlationIDs) != 0 || len(consumedItems) != 0 {
		t.Fatalf("marker-failed consume = (%d messages, %d correlation ids, %d items), want all zero",
			len(msgs), len(correlationIDs), len(consumedItems))
	}

	al.steering.mu.Lock()
	queued := append([]steeringQueueItem(nil), al.steering.queues[scope]...)
	al.steering.mu.Unlock()
	if len(queued) != 1 {
		t.Fatalf("queued wakes after marker-failure restore = %d, want 1", len(queued))
	}
	if queued[0].wake == nil || queued[0].wake.messageID != messageID {
		t.Fatalf("queued wake = %+v, want message id %q", queued[0].wake, messageID)
	}
	if queued[0].message.Content != retry.message.Content {
		t.Fatalf("queued wake content = %q, want queue-resident retry %q",
			queued[0].message.Content, retry.message.Content)
	}
}

func TestGoal1000_MarkerFailureRestorePreservesDistinctWakeFIFO(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	al.steering = newSteeringQueue(SteeringAll)

	const scope = "parent"
	for i := 0; i < 3; i++ {
		messageID := fmt.Sprintf("wake-restore-%d", i)
		if err := al.steering.pushItemScope(scope, steeringQueueItem{
			message: providers.Message{Role: "user", Content: messageID},
			wake: &steeringWake{
				messageID:           messageID,
				transcriptSessionID: "missing-transcript",
				agentID:             "mia",
			},
		}); err != nil {
			t.Fatalf("push distinct wake %d: %v", i, err)
		}
	}

	actualScope, dequeued := al.steering.dequeueItemsScope(scope)
	if len(dequeued) != 3 {
		t.Fatalf("dequeued wakes = %d, want 3", len(dequeued))
	}
	al.consumeDequeuedSteering(actualScope, dequeued)

	al.steering.mu.Lock()
	queued := append([]steeringQueueItem(nil), al.steering.queues[scope]...)
	al.steering.mu.Unlock()
	if len(queued) != 3 {
		t.Fatalf("restored distinct wakes = %d, want 3", len(queued))
	}
	for i, item := range queued {
		wantID := fmt.Sprintf("wake-restore-%d", i)
		if item.wake == nil || item.wake.messageID != wantID {
			t.Fatalf("restored wake %d = %+v, want message id %q", i, item.wake, wantID)
		}
	}
}

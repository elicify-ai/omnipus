package agent

import (
	"fmt"
	"reflect"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// queuedSteeringWake is the existing pending-wake ID lookup shared by live,
// finishing and admission queues. The caller holds that queue's owning lock.
func queuedSteeringWake(items []steeringQueueItem, messageID string) *steeringQueueItem {
	for i := range items {
		if items[i].wake != nil && items[i].wake.messageID == messageID {
			return &items[i]
		}
	}
	return nil
}

// commitQueuedSystemWake runs under entryMu. The original admission may have
// just gained its reserved slot; its promotion still waits for entryMu, so the
// same owner can finish accepting its first queued input before reconstruction.
func (al *AgentLoop) commitQueuedSystemWake(lifecycle *session.LifecycleStore, claim executionClaim, generation int, messageID, content, agentID string) error {
	gate := al.steerAdmission()
	gate.mu.Lock()
	defer gate.mu.Unlock()
	for i := range gate.queue {
		if gate.queue[i].executionClaim() == claim {
			return al.commitQueuedSystemWakeLocked(lifecycle, &gate.queue[i], generation, messageID, content, agentID)
		}
	}
	if entry, ok := gate.active[claim.SessionID]; ok && entry.executionClaim() == claim && al.getActiveTurnState(claim.SessionID) == nil {
		if err := al.commitQueuedSystemWakeLocked(lifecycle, &entry, generation, messageID, content, agentID); err != nil {
			return err
		}
		gate.active[claim.SessionID] = entry
		return nil
	}
	return steer.ErrStaleGeneration
}

// commitQueuedSystemWakeLocked retains native wake items on their ORIGINAL
// admission, never in a session-wide dedup cache. Duplicate IDs validate the
// same live lifecycle claim/fence without appending another pending message;
// a conflicting payload leaves the accepted input untouched and fails visibly.
func (al *AgentLoop) commitQueuedSystemWakeLocked(lifecycle *session.LifecycleStore, entry *steerQueueEntry, generation int, messageID, content, agentID string) error {
	claim := entry.executionClaim()
	if generation != claim.Generation || claim.BootSeq != al.bootEpochFor() {
		return steer.ErrStaleGeneration
	}
	if messageID == "" {
		return fmt.Errorf("steer: queued wake requires a message ID")
	}
	item := steeringQueueItem{
		message: providers.Message{Role: "user", Content: content},
		wake:    &steeringWake{messageID: messageID, transcriptSessionID: claim.SessionID, agentID: agentID},
	}
	duplicate := queuedSteeringWake(entry.wakeInputs, messageID)
	if duplicate != nil && (!reflect.DeepEqual(duplicate.message, item.message) || *duplicate.wake != *item.wake) {
		return fmt.Errorf("steer: queued wake %q conflicts with its accepted payload", messageID)
	}
	pendingMessage := content
	if duplicate != nil {
		pendingMessage = ""
	}
	if _, err := commitSteeredExecutionState(lifecycle, claim, session.LifecycleQueued, pendingMessage); err != nil {
		return err
	}
	if duplicate == nil {
		entry.wakeInputs = append(entry.wakeInputs, item)
	}
	return nil
}

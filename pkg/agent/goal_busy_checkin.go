package agent

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const goalBusyLabelLimit = 10

// A reservation is process-local, and tied to the server's existing wake ID and
// predecessor identity before publication. Metadata alone cannot create one.
// The accepted execution identity is bound only at the real admission stamp.
type goalBusyCheckIn struct {
	sessionID string
	prompt    string
	messageID string
	parent    executionClaim
	execution executionClaim
	stallAt   time.Time
}

func observeGoalBusyStall(work *goalReminderWork, busy bool, progress goalReminderProgress, at time.Time) {
	if !busy {
		work.stallSince = time.Time{}
		work.checkInSent = false
		return
	}
	if work.stallSince.IsZero() || goalReminderMadeProgress(progress) {
		work.stallSince = at
		work.checkInSent = false
	}
}

// Only stable IDs are copied. No command, buffered output, pending question or
// approval text is read. Each collector visits each owned item once.
func noteGoalBusyLabel(work *goalReminderWork, kind, id string) {
	if id == "" {
		return
	}
	work.busyLabelCount++
	if len(work.busyLabels) < goalBusyLabelLimit {
		work.busyLabels = append(work.busyLabels, "- "+kind+" "+strconv.Quote(id))
	}
}

func goalBusyCheckInPrompt(work *goalReminderWork) string {
	items := strings.Join(work.busyLabels, "\n")
	if remaining := work.busyLabelCount - len(work.busyLabels); remaining > 0 {
		items += fmt.Sprintf("\n- and %d more", remaining)
	}
	return strings.Replace(goalBusyCheckInPromptTemplate, "{{WORK_ITEMS}}", items, 1)
}

func bindGoalCheckInWake(goalID string, res *goalBusyCheckIn, messageID string, parent executionClaim) error {
	if res == nil {
		return nil
	}
	work := goalReminderWorkFor(goalID)
	work.mu.Lock()
	defer work.mu.Unlock()
	if work.checkIn != res || messageID == "" || parent.SessionID != res.sessionID {
		return errOrdinaryGoalWakeIgnored
	}
	res.messageID, res.parent = messageID, parent
	return nil
}

func bindOrdinaryGoalCheckInWake(sessionID, goalID string, res *goalBusyCheckIn, metadata map[string]any) error {
	if res == nil {
		return nil
	}
	messageID, messageOK := metadata["steer_message_id"].(string)
	generation, generationOK := metadata["steer_generation"].(int)
	runID, runOK := metadata["goal_parent_run_id"].(string)
	bootSeq, bootOK := metadata["goal_parent_boot_seq"].(uint64)
	if !messageOK || !generationOK || !runOK || !bootOK {
		return errors.New("goal check-in: missing server-prepared wake identity")
	}
	return bindGoalCheckInWake(goalID, res, messageID, executionClaim{SessionID: sessionID, Generation: generation, RunID: runID, BootSeq: bootSeq})
}

// Publication happens outside work.mu. A failed actual handoff releases only
// this reservation, not a newer progress observation or another execution.
func rollbackGoalCheckIn(goalID string, res *goalBusyCheckIn) {
	if res == nil {
		return
	}
	work := goalReminderWorkFor(goalID)
	work.mu.Lock()
	defer work.mu.Unlock()
	if work.checkIn == res && res.execution.RunID == "" {
		work.checkIn = nil
		if work.stallSince.Equal(res.stallAt) {
			work.checkInSent = false
		}
	}
}

func noteGoalCheckInExecution(goalID, messageID string, claim executionClaim) {
	work := goalReminderWorkFor(goalID)
	work.mu.Lock()
	defer work.mu.Unlock()
	res := work.checkIn
	if res != nil && res.messageID == messageID && res.sessionID == claim.SessionID && claim.RunID != "" {
		res.execution = claim
	}
}

func goalCheckInOutcome(work *goalReminderWork) bool {
	return work.pending != nil && work.pending.execution != nil && work.checkIn != nil &&
		work.checkIn.execution.RunID != "" && work.checkIn.execution == work.pending.execution.claim
}

// A refusal before admission must not strand the prepared check-in. A real
// admitted owner is never rolled back by this cleanup.
func rollbackUnadmittedGoalCheckIn(sessionID string, wake *ordinaryGoalWake) {
	if wake == nil {
		return
	}
	work := goalReminderWorkFor(wake.goalID)
	work.mu.Lock()
	res := work.checkIn
	matching := res != nil && res.sessionID == sessionID && res.messageID == wake.messageID
	work.mu.Unlock()
	if matching {
		rollbackGoalCheckIn(wake.goalID, res)
	}
}

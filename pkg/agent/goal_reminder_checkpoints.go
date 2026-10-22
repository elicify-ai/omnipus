package agent

import "sync/atomic"

// goalReminderPassiveSeams lets tests hold an otherwise invisible ordering
// boundary. Application wiring never installs these seams. An unset pointer
// changes no admission, accounting, activity observation or publication rule.
// Tests scope their callbacks to their own session and join the producer before
// restoring the pointer; the atomic load also keeps unrelated readers race-free.
type goalReminderPassiveSeams struct {
	ordinaryPreRevival          func(sessionID string)
	ordinaryAfterGoalValidation func(sessionID string)
	goalPauseBeforeWaiting      func(sessionID, goalID string)
	goalPauseAfterFreshRead     func(sessionID, goalID string)
}

var goalReminderSeams atomic.Pointer[goalReminderPassiveSeams]

func goalReminderPreRevivalCheckpoint(sessionID string) {
	seams := goalReminderSeams.Load()
	if seams != nil && seams.ordinaryPreRevival != nil {
		seams.ordinaryPreRevival(sessionID)
	}
}

func goalReminderPauseCheckpoint(sessionID, goalID string) {
	seams := goalReminderSeams.Load()
	if seams != nil && seams.goalPauseBeforeWaiting != nil {
		seams.goalPauseBeforeWaiting(sessionID, goalID)
	}
}

func goalReminderAfterGoalValidationCheckpoint(sessionID string) {
	seams := goalReminderSeams.Load()
	if seams != nil && seams.ordinaryAfterGoalValidation != nil {
		seams.ordinaryAfterGoalValidation(sessionID)
	}
}

func goalReminderPauseFreshReadCheckpoint(sessionID, goalID string) {
	seams := goalReminderSeams.Load()
	if seams != nil && seams.goalPauseAfterFreshRead != nil {
		seams.goalPauseAfterFreshRead(sessionID, goalID)
	}
}

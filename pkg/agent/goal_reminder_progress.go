package agent

import (
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/tools"
)

// goalReminderProgress is deliberately policy-free observation data. Keep the
// progress definition below small so its meaning can be changed independently
// of registry, transcript and diff collection.
type goalReminderProgress struct {
	evidence   bool
	files      bool
	tool       bool
	descendant bool
	background bool
}

func goalReminderMadeProgress(p goalReminderProgress) bool {
	return p.evidence || p.files || p.tool || p.descendant || p.background
}

// noteGoalExecutedTool consumes the registry's live fact before display hooks
// or transcript serialization can replace/discard its execution provenance.
func noteGoalExecutedTool(ts *turnState, name string, result *tools.ToolResult) {
	if ts == nil || ts.goalID == "" || name == "set_todos" || name == "goal_claim" ||
		result == nil || !result.WasInvoked() || result.Deferred != nil {
		return
	}
	work := goalReminderWorkFor(ts.goalID)
	work.mu.Lock()
	work.toolActivityAt = time.Now().UTC()
	work.mu.Unlock()
}

// goalReminderOutcome exists only until the existing goal counter update is
// committed. It lets the normal idle path retry a failed completion assessment
// without dispatching another, uncounted reminder.
type goalReminderOutcome struct {
	turnID    string
	execution *executionDisposition
	failed    bool
}

// goalReminderWork is process-local observation state, not a second durable
// counter or jobs registry. The only persisted streak is Goal.ZeroOutputPushes;
// failedReminders stays in RAM, with only a committed pause reason saved.
// Retained process references preserve a real finish timestamp if the existing
// manager removes a long-running job before the next observation.
type goalReminderWork struct {
	mu                  sync.Mutex
	pending             *goalReminderOutcome
	lastAssessedTurn    string
	failedReminders     int
	pauseNoticeEnqueued string
	toolActivityAt      time.Time
	observedProgress    bool
	events              map[string]time.Time
	processes           map[string]*tools.ProcessSession
	reminderRuns        map[string]bool
}

func goalReminderWorkFor(goalID string) *goalReminderWork {
	s := goalTriggers()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reminderWork == nil {
		s.reminderWork = make(map[string]*goalReminderWork)
	}
	work := s.reminderWork[goalID]
	if work == nil {
		work = &goalReminderWork{events: make(map[string]time.Time), processes: make(map[string]*tools.ProcessSession)}
		s.reminderWork[goalID] = work
	}
	return work
}

func goalReminderWatermark(goalID string) time.Time {
	s := goalTriggers()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.outputWatermarks[goalID]
}

func setGoalReminderWatermark(goalID string, at time.Time) {
	s := goalTriggers()
	s.mu.Lock()
	s.outputWatermarks[goalID] = at
	s.mu.Unlock()
}

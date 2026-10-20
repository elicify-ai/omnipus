package heartbeat

import (
	"time"

	"github.com/elicify-ai/omnipus/pkg/logger"
)

// pollerStopJoinBudget bounds how long Stop waits for a cycle already in
// flight to return. A wedged cycle must not freeze shutdown or a config
// reload; after the budget Stop logs and returns.
const pollerStopJoinBudget = 10 * time.Second

// joinPollerLoop waits for a poller's loop goroutine to exit, bounded by
// pollerStopJoinBudget.
func joinPollerLoop(done <-chan struct{}, service string) {
	timer := time.NewTimer(pollerStopJoinBudget)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		logger.WarnCF("heartbeat", "Stop: a poller cycle still in flight did not return within its budget; proceeding",
			map[string]any{"service": service, "budget": pollerStopJoinBudget.String()})
	}
}

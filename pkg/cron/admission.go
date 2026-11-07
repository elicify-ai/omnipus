package cron

// admission.go — the one dispatch admission for every scheduled job, governed
// by memory (T10 spec r6 FR-07, founder T10-Q8). There is no configured lane
// limit and no fixed semaphore: schedules.max_concurrent_runs and the
// fixed-capacity lane were removed.
//
// The memory rule itself lives in pkg/config (MemoryAdmissionCap) so there is
// exactly one floor literal and one threshold for agent turns and scheduled
// runs alike. AdmissionRule is the hand-over point: the engine asks it at every
// admission decision and never caches the answer. pkg/cron does not import
// pkg/agent.
//
// Admission caps concurrent timer DISPATCHES (handler admissions), not whole
// task runs: a task handler that hands its run to the task executor returns,
// releases its admission, and the run continues under the executor's own
// controls. Admitted work is never preempted: a falling limit only holds new
// dispatches back. Every wait logs exactly one structured line naming the
// binding restriction (memory) and the job.

import (
	"context"
	"log/slog"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// AdmissionRule reports the current cap on concurrent scheduled dispatches and
// whether a cap applies at all. (0, false) means no cap.
type AdmissionRule func() (limit int, limited bool)

// admissionRecheckInterval bounds how long a waiting dispatch goes without
// re-asking the rule when no admitted run settles (memory may recover on its
// own). It is a re-check cadence, not a limit on any run.
const admissionRecheckInterval = 5 * time.Second

// SetAdmissionRule installs the admission hand-over point. nil restores the
// default memory rule (config.MemoryAdmissionCap). Safe to call at any time;
// the rule is consulted fresh at every admission decision.
func (cs *CronService) SetAdmissionRule(rule AdmissionRule) {
	cs.admitMu.Lock()
	defer cs.admitMu.Unlock()
	if rule == nil {
		rule = config.MemoryAdmissionCap
	}
	cs.admissionRule = rule
	cs.signalAdmissionLocked()
}

// admit blocks until jobID may start, or ctx is done (returns false). It logs
// one line per wait, never per re-check.
func (cs *CronService) admit(ctx context.Context, jobID string) bool {
	logged := false
	for {
		cs.admitMu.Lock()
		limit, limited := cs.admissionRule()
		if !limited || cs.admitted < limit {
			cs.admitted++
			cs.admitMu.Unlock()
			return true
		}
		wake := cs.admitWake
		admitted := cs.admitted
		cs.admitMu.Unlock()
		if !logged {
			logged = true
			slog.Warn("cron: scheduled run waiting for admission: memory limit reached",
				"job_id", jobID, "restriction", "memory", "limit", limit, "admitted", admitted)
		}
		timer := time.NewTimer(admissionRecheckInterval)
		select {
		case <-wake:
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return false
		}
		timer.Stop()
	}
}

// release returns one admission taken by admit and wakes every waiter.
func (cs *CronService) release() {
	cs.admitMu.Lock()
	defer cs.admitMu.Unlock()
	if cs.admitted > 0 {
		cs.admitted--
	}
	cs.signalAdmissionLocked()
}

// signalAdmissionLocked wakes all current waiters. Caller holds admitMu.
func (cs *CronService) signalAdmissionLocked() {
	if cs.admitWake != nil {
		close(cs.admitWake)
	}
	cs.admitWake = make(chan struct{})
}

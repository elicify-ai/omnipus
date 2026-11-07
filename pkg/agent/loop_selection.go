// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// loop_selection.go holds the single /loop ownership rule (T10 spec r6
// FR-05): the selected step. A session's selected step is its live
// `loop`-kind job with the latest scheduled instant; a nil next run ranks
// below any non-nil one. Fire time, every post-turn mutation and boot
// recovery all use this one predicate — the session's LoopJobID is a
// convenience pointer, never the authority.
//
// It also holds the per-run context a loop turn gets (FR-10 (d) and (e)).
package agent

import (
	"context"
	"errors"
	"os"
	"sync"

	"github.com/elicify-ai/omnipus/pkg/cron"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// sessionJobs returns every `loop`-kind job of sessionID, enabled or not.
// Jobs of other kinds are never loop-owned, whatever their name or message
// (FR-25).
func (s *LoopScheduler) sessionJobs(sessionID string) []cron.CronJob {
	var out []cron.CronJob
	for _, j := range s.cs.ListJobs(true) {
		if j.Payload.Kind == loopJobKind && j.Payload.Message == sessionID {
			out = append(out, j)
		}
	}
	return out
}

// selectStep returns the selected step among jobs: the enabled job with the
// latest next run, a nil next run ranking below every non-nil one. Ties go to
// the later-created job, then the larger ID, so the choice is deterministic.
func selectStep(jobs []cron.CronJob) (cron.CronJob, bool) {
	var best cron.CronJob
	found := false
	for _, j := range jobs {
		if !j.Enabled {
			continue
		}
		if !found || ranksAbove(j, best) {
			best, found = j, true
		}
	}
	return best, found
}

// ranksAbove reports whether a ranks above b as the selected step.
func ranksAbove(a, b cron.CronJob) bool {
	an, bn := a.State.NextRunAtMS, b.State.NextRunAtMS
	switch {
	case an != nil && bn == nil:
		return true
	case an == nil && bn != nil:
		return false
	case an != nil && *an != *bn:
		return *an > *bn
	case a.CreatedAtMS != b.CreatedAtMS:
		return a.CreatedAtMS > b.CreatedAtMS
	default:
		return a.ID > b.ID
	}
}

// isSelectedStep reports whether jobID is sessionID's selected step right now.
// The caller has already checked that the session's loop state is active.
func (s *LoopScheduler) isSelectedStep(sessionID, jobID string) bool {
	selected, ok := selectStep(s.sessionJobs(sessionID))
	return ok && selected.ID == jobID
}

// removeSessionJobs removes every `loop`-kind job of sessionID in one engine
// write (FR-05: /loop start replace, /loop stop, the idle sweep, the run cap
// and boot recovery remove them all). A persistence failure is returned and
// leaves the jobs in place.
func (s *LoopScheduler) removeSessionJobs(sessionID string) error {
	jobs := s.sessionJobs(sessionID)
	if len(jobs) == 0 {
		return nil
	}
	ids := make([]string, len(jobs))
	for i, j := range jobs {
		ids[i] = j.ID
	}
	return s.cs.RemoveJobs(ids)
}

// loopRunProcesses records the child processes one loop turn spawns
// (reported by the exec/shell tools through tools.TrackProcess) so they can
// be terminated when the turn returns (FR-10(d), the schedule runner's
// per-run cleanup). Best effort: a failed termination is logged, never
// returned, and never fails the run.
type loopRunProcesses struct {
	mu   sync.Mutex
	pids []int
}

func (p *loopRunProcesses) track(pid int) {
	if pid <= 0 {
		return
	}
	p.mu.Lock()
	p.pids = append(p.pids, pid)
	p.mu.Unlock()
}

func (p *loopRunProcesses) cleanup(sessionID, jobID string) {
	p.mu.Lock()
	pids := p.pids
	p.pids = nil
	p.mu.Unlock()
	for _, pid := range pids {
		proc, err := os.FindProcess(pid)
		if err == nil {
			err = proc.Kill()
		}
		if err != nil && !errors.Is(err, os.ErrProcessDone) {
			logger.WarnCF("agent", "loop: failed to terminate a child process of the run",
				map[string]any{"session_id": sessionID, "job_id": jobID, "pid": pid, "error": err.Error()})
		}
	}
}

// withLoopRunContext returns the context one loop turn runs under: the job
// identity for auto-deny audit entries (FR-10(e)) and a child-process tracker
// whose processes the caller terminates after the turn (FR-10(d)).
func withLoopRunContext(ctx context.Context, job *cron.CronJob) (context.Context, *loopRunProcesses) {
	procs := &loopRunProcesses{}
	ctx = tools.WithProcessTracker(ctx, procs.track)
	ctx = WithScheduledJobContext(ctx, job.ID, job.Name)
	return ctx, procs
}

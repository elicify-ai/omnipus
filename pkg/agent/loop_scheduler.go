// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// loop_scheduler.go implements the time-driven half of `/loop` (ADR-049
// D6/D7, spec Part B US-9): interval-mode cron `every` jobs and self-paced
// one-shot `at` jobs. Since T10 (schedules-t10-spec-r6 FR-01) the scheduler has
// no engine of its own: it is the `loop`-kind handler on the ONE shared timer
// engine (<home>/cron/jobs.json), registered by NewLoopScheduler. RunScheduled
// fires by calling back into AgentLoop.ProcessScheduled — the SAME headless,
// session-continuing entry point heartbeat jobs use, so a /loop run gets the
// identical transcript-recording and auto-deny-ask semantics.
//
// Ownership (T10 FR-05): a session's loop state lives in its session metadata;
// LoopJobID is a convenience pointer only. The single ownership rule is the
// selected step (loop_selection.go): a job may run its turn, and a settled
// callback may apply any post-turn mutation, only while it is the session's
// selected step. Boot recovery lives in loop_recovery.go.
package agent

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/cron"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// loopJobKind marks a cron job as belonging to /loop (mirrors
// heartbeatJobKind's convention, pkg/gateway/heartbeat_schedule.go).
const loopJobKind = cron.KindLoop

// loopStopOnlyCommand marks a loop step whose only purpose is to stop the
// loop: it is armed when the loop's last permitted run finished but the
// stopped state could not be saved. Such a step never runs a turn, whatever
// run count is saved (the count of that last run may have been lost with the
// stop), and boot recovery stops a loop whose selected step carries it.
const loopStopOnlyCommand = "loop-stop-only"

// isStopOnlyStep reports whether job is a stop-only loop step.
func isStopOnlyStep(job *cron.CronJob) bool {
	return job != nil && job.Payload.Command == loopStopOnlyCommand
}

// firstSelfPacedRunDelayMS is the delay used for a self-paced loop's very
// first run. It is NOT zero: cron.CronService.computeNextRun's "at" branch
// requires AtMS to be STRICTLY greater than "now" at creation time
// (`*schedule.AtMS > nowMS`) — an "at" job created with AtMS == now (a true
// zero-delay "run immediately") gets NextRunAtMS left nil forever and never
// fires. A short, effectively-immediate 1s delay sidesteps that edge case
// entirely rather than special-casing it.
const firstSelfPacedRunDelayMS = 1000

// defaultSelfPacedFallbackDelayMS is the delay used when a self-paced run's
// reply carries no parseable LOOP_NEXT marker (documented gap: FR-072
// specifies the agent "chooses" its next delay but does not mandate a
// mechanical marker format; this runtime instructs the model — via
// loopSelfPacedSteeringPrompt below — to emit one, and falls back rather
// than silently dropping the loop when a turn fails to comply). 15 minutes.
const defaultSelfPacedFallbackDelayMS = 15 * 60 * 1000

// loopNextMarkerRe matches the self-paced "state your next delay" marker
// this runtime asks the model to emit: `LOOP_NEXT: <N><s|m|h|d> — <reason>`.
// The reason capture group is optional (a marker with no reason still
// parses the delay).
var loopNextMarkerRe = regexp.MustCompile(`(?i)LOOP_NEXT:\s*([0-9]+)\s*([smhd])\s*(?:[-\x{2014}]\s*(.*))?`)

// loopJobName returns the deterministic cron job name for sessionID,
// namespacing /loop jobs the way heartbeatJobName does (heartbeat_schedule.go).
func loopJobName(sessionID string) string {
	return "loop:" + sessionID
}

// LoopScheduler is the `loop`-kind handler on the shared timer engine and the
// /loop job producer. It implements cron.ScheduledRunner.
type LoopScheduler struct {
	cs *cron.CronService
	al *AgentLoop
	// postTurnHook is a test seam run after ProcessScheduled returns and
	// before any post-turn mutation (SetPostTurnTestHook). nil in production.
	postTurnHook atomic.Pointer[func()]
	// runLimitStopHook is a test seam run when a fire finds its loop at the
	// run limit, before the stop is attempted (SetRunLimitStopTestHook). nil
	// in production.
	runLimitStopHook atomic.Pointer[func()]
	// runLimitStopFailedHook is a test seam run when a run-limit stop's save
	// failed, after the session lock is released and before the fire returns
	// (SetRunLimitStopFailedTestHook). nil in production.
	runLimitStopFailedHook atomic.Pointer[func()]
}

// NewLoopScheduler registers a scheduler as the `loop`-kind handler of the
// shared engine cs (T10 FR-01/FR-02). The engine's lifecycle (restore,
// activation, stop) belongs to its owner, the gateway, not to this scheduler.
func NewLoopScheduler(cs *cron.CronService, al *AgentLoop) *LoopScheduler {
	s := &LoopScheduler{cs: cs, al: al}
	if err := cs.RegisterKindHandler(cron.KindLoop, s); err != nil {
		// Only an unregistered kind or a nil handler can fail; neither is
		// possible here, so this is a programming error, not a runtime state.
		panic(fmt.Sprintf("loop_scheduler: register loop kind handler: %v", err))
	}
	return s
}

// Start starts the underlying engine. The gateway never calls it: it
// activates the shared engine itself after boot recovery (FR-03). This is for
// a standalone engine owned by a test.
func (s *LoopScheduler) Start() error { return s.cs.Start() }

// Stop stops the underlying engine. Same scope as Start: the gateway stops the
// shared engine once, on shutdown only (FR-26).
func (s *LoopScheduler) Stop() { s.cs.Stop() }

// SetClock passes a test clock through to the underlying engine.
func (s *LoopScheduler) SetClock(c cron.Clock) { s.cs.SetClock(c) }

// SetPostTurnTestHook installs fn to run after a fired turn returns and before
// the post-turn ownership check and mutations (tests only; nil clears).
func (s *LoopScheduler) SetPostTurnTestHook(fn func()) {
	if fn == nil {
		s.postTurnHook.Store(nil)
		return
	}
	s.postTurnHook.Store(&fn)
}

// SetRunLimitStopTestHook installs fn to run when a fire finds its loop at the
// run limit, after the fire-time checks and before the stop (tests only; nil
// clears).
func (s *LoopScheduler) SetRunLimitStopTestHook(fn func()) {
	if fn == nil {
		s.runLimitStopHook.Store(nil)
		return
	}
	s.runLimitStopHook.Store(&fn)
}

// SetRunLimitStopFailedTestHook installs fn to run when a run-limit stop's
// save failed, after the session lock is released and before the fire
// returns (tests only; nil clears).
func (s *LoopScheduler) SetRunLimitStopFailedTestHook(fn func()) {
	if fn == nil {
		s.runLimitStopFailedHook.Store(nil)
		return
	}
	s.runLimitStopFailedHook.Store(&fn)
}

// RunDueJobs drives dispatch deterministically for tests.
func (s *LoopScheduler) RunDueJobs(now time.Time) { s.cs.RunDueJobs(now) }

// WaitForLane blocks until in-flight lane runs complete (tests).
func (s *LoopScheduler) WaitForLane() { s.cs.WaitForLane() }

// ListEnabledJobs returns every currently-enabled /loop cron job — the R5
// admission-count source for kind "loop" (Wave 2-C2's RegisterActiveCounter
// wiring, gateway.go).
func (s *LoopScheduler) ListEnabledJobs() []cron.CronJob {
	var out []cron.CronJob
	for _, j := range s.cs.ListJobs(false) {
		if j.Payload.Kind == loopJobKind {
			out = append(out, j)
		}
	}
	return out
}

// AddInterval creates an interval-mode (`every`, continue-session) cron job
// for sessionID (FR-071). Returns the new job's ID.
func (s *LoopScheduler) AddInterval(ownerAgentID, sessionID string, everyMS int64) (string, error) {
	everyCopy := everyMS
	job, err := s.cs.AddJobFull(cron.JobSpec{
		Kind:        loopJobKind,
		Name:        loopJobName(sessionID),
		Schedule:    cron.CronSchedule{Kind: "every", EveryMS: &everyCopy},
		Message:     sessionID, // payload carries the session id; RunScheduled reads loop state from session meta
		AgentID:     ownerAgentID,
		SessionMode: cron.SessionModeContinue,
	})
	if err != nil {
		return "", fmt.Errorf("loop_scheduler: add interval job: %w", err)
	}
	return job.ID, nil
}

// AddOneShot creates a one-shot (`at`) cron job firing delayMS from the
// scheduler's OWN current time (FR-072 self-paced mode) — deliberately
// computed via s.cs.Now() (the injected Clock, real time by default) rather
// than accepting a caller-supplied "now", so a test's fake clock and this
// service's internal due-job comparison can never disagree (see
// cron.CronService.Now's doc comment for why that class of bug matters).
// The job auto-deletes itself once fired (AddJobFull's DeleteAfterRun :=
// Schedule.Kind=="at") — callers never need to remove it manually. Returns
// the new job's ID.
func (s *LoopScheduler) AddOneShot(ownerAgentID, sessionID string, delayMS int64) (string, error) {
	return s.addOneShot(ownerAgentID, sessionID, delayMS, "")
}

// addOneShot is AddOneShot with the step's command marker ("" for a turn
// step, loopStopOnlyCommand for a stop-only step).
func (s *LoopScheduler) addOneShot(ownerAgentID, sessionID string, delayMS int64, command string) (string, error) {
	at := s.cs.Now().UnixMilli() + delayMS
	job, err := s.cs.AddJobFull(cron.JobSpec{
		Kind:        loopJobKind,
		Name:        loopJobName(sessionID),
		Schedule:    cron.CronSchedule{Kind: "at", AtMS: &at},
		Message:     sessionID,
		Command:     command,
		AgentID:     ownerAgentID,
		SessionMode: cron.SessionModeContinue,
	})
	if err != nil {
		return "", fmt.Errorf("loop_scheduler: add one-shot job: %w", err)
	}
	return job.ID, nil
}

// RemoveJob removes jobID (a no-op, returning false, for an empty id — FR-073
// `/loop stop`).
func (s *LoopScheduler) RemoveJob(jobID string) bool {
	if jobID == "" {
		return false
	}
	return s.cs.RemoveJob(jobID)
}

// RunScheduled implements cron.ScheduledRunner. It fires exactly one /loop
// run, in this order:
//
//  1. A job whose session lost its loop state removes itself (FR-05(f)).
//  2. A job that is not the session's selected step runs no turn and removes
//     itself (FR-05 fire-time ownership).
//  3. A stop-only step, or a fire that finds the saved run count at the
//     run limit, runs no turn: it stops the loop (stopAtRunLimit).
//  4. The owner must be registered and must not be a worker (FR-10 (a)/(b),
//     the schedule runner's rule universalized); otherwise an error is
//     recorded and no turn runs.
//  5. The turn runs via ProcessScheduled with the job identity on the context
//     (auto-deny audit, FR-10(e)) and a per-run child-process tracker; the
//     tracked children are terminated when the turn returns (FR-10(d)).
//  6. Post-turn, under the session's lock and only while this job is still
//     the selected step (FR-05(g)): run_count and activity are persisted, the
//     run cap stops the loop, and a self-paced loop schedules its next step
//     from the reply's LOOP_NEXT marker (fixed fallback delay, WARN-logged).
//
// run_count is persisted AFTER the run, so a crash mid-run re-fires the same
// count; recovery decides eligibility itself (loop_recovery.go).
func (s *LoopScheduler) RunScheduled(ctx context.Context, job *cron.CronJob) (string, error) {
	sessionID := job.Payload.Message
	if sessionID == "" || s.al == nil {
		return "", fmt.Errorf("loop_scheduler: job %s has no session id", job.ID)
	}
	store := s.al.ResolveSessionStore(sessionID)
	if store == nil {
		s.cs.RemoveJob(job.ID)
		return "", fmt.Errorf("loop_scheduler: session %q store not found; job removed", sessionID)
	}
	meta, err := store.GetMeta(sessionID)
	if err != nil || meta == nil || meta.LoopMode == "" {
		// The loop was stopped/cleared out from under this job (e.g. /loop
		// stop raced a fire, or a crash left an orphan) — remove it rather
		// than firing a run nobody asked for. Not an error condition.
		s.cs.RemoveJob(job.ID)
		return "", nil //nolint:nilerr
	}
	if !s.isSelectedStep(sessionID, job.ID) {
		s.cs.RemoveJob(job.ID)
		logger.InfoCF("agent", "loop: a job that is not the session's selected step fired; removed without a turn",
			map[string]any{"session_id": sessionID, "job_id": job.ID})
		return "", nil
	}
	if isStopOnlyStep(job) || meta.LoopRunCount >= effectiveLoopMaxRuns(meta) {
		// The run limit was reached but its stop was not saved (settleTurn
		// or an earlier retry failed to save it). No turn runs past the
		// limit; the stop is retried here (spec section 8).
		if hook := s.runLimitStopHook.Load(); hook != nil {
			(*hook)()
		}
		return "", s.stopAtRunLimit(store, sessionID, job)
	}
	if oerr := s.checkOwner(job.AgentID); oerr != nil {
		logger.WarnCF("agent", "loop: run refused",
			map[string]any{"session_id": sessionID, "job_id": job.ID, "error": oerr.Error()})
		return "", oerr
	}

	prompt := meta.LoopPrompt
	if meta.LoopMode == loopModeSelfPaced {
		prompt = loopSelfPacedSteeringPrompt(prompt)
	}
	runCtx, procs := withLoopRunContext(ctx, job)
	reply, runErr := s.al.ProcessScheduled(runCtx, job.AgentID, sessionID, prompt, "", "")
	procs.cleanup(sessionID, job.ID)

	if hook := s.postTurnHook.Load(); hook != nil {
		(*hook)()
	}
	s.settleTurn(store, sessionID, job, reply)
	return reply, runErr
}

// stopAtRunLimit retries the stop of a loop that reached its run limit,
// without running a turn. The stop applies only while, under the session
// lock, the loop is still active, this job is still its selected step and
// the loop is still at its limit: the job is a stop-only step, or the saved
// run count reached the saved limit. A callback revoked in between (the loop
// was stopped or replaced) changes nothing (FR-05, S-B.8). A failed save
// arms the next stop-only retry under the same lock, so a revoked callback
// can never add a step next to a replacement.
func (s *LoopScheduler) stopAtRunLimit(store *session.UnifiedStore, sessionID string, job *cron.CronJob) error {
	guard := &loopStopGuard{
		owns: func(m *session.UnifiedMeta) bool {
			return m != nil && m.LoopMode != "" && s.isSelectedStep(sessionID, job.ID) &&
				(isStopOnlyStep(job) || m.LoopRunCount >= effectiveLoopMaxRuns(m))
		},
		onSaveFailed: func(m *session.UnifiedMeta) { s.armRunLimitStopRetry(sessionID, job, m) },
	}
	_, err := s.al.stopSessionLoop(sessionID, store, s, "run limit reached", guard)
	if errors.Is(err, errLoopStopRevoked) {
		logger.InfoCF("agent", "loop: a run-limit stop was revoked before it applied; nothing changed",
			map[string]any{"session_id": sessionID, "job_id": job.ID})
		return nil
	}
	var left *loopJobsLeftError
	if err != nil && !errors.As(err, &left) {
		logger.ErrorCF("agent", "loop: at its run limit and the stopped state still cannot be saved; no turn runs",
			map[string]any{"session_id": sessionID, "job_id": job.ID, "error": err.Error()})
		if hook := s.runLimitStopFailedHook.Load(); hook != nil {
			(*hook)()
		}
		return fmt.Errorf("loop_scheduler: stop the loop at its run limit: %w", err)
	}
	if left != nil {
		logger.WarnCF("agent", "loop: stopped at its run limit but some of its jobs could not be removed",
			map[string]any{"session_id": sessionID, "job_id": job.ID, "error": err.Error()})
	}
	return nil
}

// armRunLimitStopRetry gives a loop whose run-limit stop could not be saved
// a stop-only step to retry the stop on. The caller holds the session lock
// and has checked that job is the loop's selected step, so the step belongs
// to the loop that reached its limit and a replacement removes it.
//
// A self-paced loop gets a new stop-only one-shot (the engine deletes the
// one-shot that just fired) after the loop's saved next delay, else the
// first-step delay. An interval loop keeps its repeating job, marked
// stop-only, so its next tick retries the stop. The mark, not the saved run
// count, makes the retry stop-only: the count of the last run may not have
// been saved either. A failure is logged; the next gateway start stops a
// loop whose saved count reached its limit.
func (s *LoopScheduler) armRunLimitStopRetry(sessionID string, job *cron.CronJob, meta *session.UnifiedMeta) {
	if meta == nil {
		return
	}
	var err error
	switch meta.LoopMode {
	case loopModeSelfPaced:
		delay := meta.LoopNextDelayMS
		if delay <= 0 {
			delay = firstSelfPacedRunDelayMS
		}
		_, err = s.addOneShot(job.AgentID, sessionID, delay, loopStopOnlyCommand)
	case loopModeInterval:
		err = s.markStopOnly(job.ID)
	default:
		return
	}
	if err != nil {
		logger.ErrorCF("agent", "loop: could not arm the retry of an unsaved run-limit stop; the next gateway start stops the loop only if its saved count reached the limit",
			map[string]any{"session_id": sessionID, "job_id": job.ID, "error": err.Error()})
	}
}

// markStopOnly marks the engine job jobID as a stop-only step.
func (s *LoopScheduler) markStopOnly(jobID string) error {
	j, ok := s.cs.GetJob(jobID)
	if !ok {
		return fmt.Errorf("loop_scheduler: mark job %s stop-only: job not found", jobID)
	}
	if isStopOnlyStep(&j) {
		return nil
	}
	j.Payload.Command = loopStopOnlyCommand
	if err := s.cs.UpdateJob(&j); err != nil {
		return fmt.Errorf("loop_scheduler: mark job %s stop-only: %w", jobID, err)
	}
	return nil
}

// checkOwner applies the G5 owner rules of the schedule runner (FR-10 (a) and
// (b)): the owner must be registered, and a worker owner is refused — workers
// run only via delegation, never on a timer cadence.
func (s *LoopScheduler) checkOwner(owner string) error {
	reg := s.al.GetRegistry()
	if owner == "" || reg == nil {
		return fmt.Errorf("loop_scheduler: owner unavailable: agent %q is not registered", owner)
	}
	inst, ok := reg.GetAgent(owner)
	if !ok || inst == nil {
		return fmt.Errorf("loop_scheduler: owner unavailable: agent %q is not registered", owner)
	}
	if inst.IsWorker() {
		return fmt.Errorf(
			"loop_scheduler: owner unavailable: agent %q is a worker (workers run only via delegation, never on a timer cadence)",
			owner,
		)
	}
	return nil
}

// loopSettleOutcome is what settleTurn decided under the session lock.
type loopSettleOutcome struct {
	revoked bool
	mode    string
	run     int
	maxRuns int
	delayMS *int
	state   string
}

// settleTurn applies the post-turn mutations and publishes their status
// frame atomically with the selected-step check under the session's lock
// (FR-05(g); lock order session then engine), so a stop that follows it
// publishes after it. A revoked callback — its job replaced, stopped or no
// longer the selected step — changes nothing and publishes nothing. At the
// run limit the stopped state is saved before anything else; a failed save
// changes nothing more and publishes nothing.
func (s *LoopScheduler) settleTurn(store *session.UnifiedStore, sessionID string, job *cron.CronJob, reply string) {
	var out loopSettleOutcome
	err := store.UpdateMetaLocked(sessionID, func(meta *session.UnifiedMeta, set func(session.MetaPatch) error) error {
		if meta == nil || meta.LoopMode == "" || !s.isSelectedStep(sessionID, job.ID) {
			out.revoked = true
			return nil
		}
		out.mode, out.maxRuns, out.run = meta.LoopMode, effectiveLoopMaxRuns(meta), meta.LoopRunCount+1
		// FR-064/D7 idle-expiry: a fired run is activity for the calendar
		// brake, persisted in the SAME write as run_count; bumped even when
		// the turn errored (it still really executed).
		activityNow := time.Now().UTC().Format(time.RFC3339)
		if perr := set(session.MetaPatch{LoopRunCount: &out.run, LoopLastActivityAt: &activityNow}); perr != nil {
			logger.WarnCF("agent", "loop: failed to persist run_count",
				map[string]any{"session_id": sessionID, "error": perr.Error()})
		}
		if out.run >= out.maxRuns {
			if perr := set(loopStoppedPatch()); perr != nil {
				// Nothing else changes and nothing is published: the next
				// fire runs no turn and retries the stop (RunScheduled):
				// the loop gets a stop-only step for that fire, so the
				// retry runs no turn even if the count above was lost.
				s.armRunLimitStopRetry(sessionID, job, meta)
				return &loopCapStopError{err: perr}
			}
			s.al.finishLoopStop(sessionID, out.mode, out.maxRuns, out.run)
			if rerr := s.removeSessionJobs(sessionID); rerr != nil {
				// The cleared state makes any surviving job remove itself on
				// its next fire (step 1 of RunScheduled).
				logger.WarnCF("agent", "loop: failed to remove the loop's jobs at the run cap",
					map[string]any{"session_id": sessionID, "error": rerr.Error()})
			}
			return nil
		}
		if meta.LoopMode != loopModeSelfPaced {
			// Interval mode: the engine's computeNextRun re-arms the `every` job.
			out.state = "run_completed"
			s.al.emitLoopStatusFrame(sessionID, out.mode, out.run, out.maxRuns, nil, out.state)
			return nil
		}
		delayMS, _ := parseLoopNextMarker(reply)
		if delayMS <= 0 {
			delayMS = defaultSelfPacedFallbackDelayMS
			logger.WarnCF("agent", "loop: self-paced run produced no parseable LOOP_NEXT marker; using fallback delay",
				map[string]any{"session_id": sessionID, "fallback_ms": delayMS})
		}
		newJobID, aerr := s.AddOneShot(job.AgentID, sessionID, delayMS)
		if aerr != nil {
			// The run itself is recorded above; only its next step is missing.
			return &loopNextStepError{err: aerr}
		}
		if perr := set(session.MetaPatch{LoopJobID: &newJobID, LoopNextDelayMS: &delayMS}); perr != nil {
			logger.WarnCF("agent", "loop: failed to persist next-run job id",
				map[string]any{"session_id": sessionID, "error": perr.Error()})
		}
		delayInt := int(delayMS)
		out.delayMS, out.state = &delayInt, "scheduled_next"
		s.al.emitLoopStatusFrame(sessionID, out.mode, out.run, out.maxRuns, out.delayMS, out.state)
		return nil
	})
	var capErr *loopCapStopError
	if errors.As(err, &capErr) {
		logger.ErrorCF("agent", "loop: reached its run limit but the stopped state could not be saved; no further turn runs and the stop is retried at the next fire or gateway start",
			map[string]any{"session_id": sessionID, "job_id": job.ID, "error": capErr.err.Error()})
		return
	}
	var nextErr *loopNextStepError
	if errors.As(err, &nextErr) {
		// The self-paced loop has no next step in this process. Its saved
		// state is intact, so boot recovery (FR-05) gives it a fresh step at
		// the next gateway start.
		logger.ErrorCF("agent", "loop: could not schedule the next self-paced run; the loop resumes at the next gateway start",
			map[string]any{"session_id": sessionID, "job_id": job.ID, "error": nextErr.err.Error()})
		return
	}
	if err != nil {
		logger.WarnCF("agent", "loop: post-turn settle could not read the session; no post-turn change applied",
			map[string]any{"session_id": sessionID, "job_id": job.ID, "error": err.Error()})
		return
	}
	if out.revoked {
		logger.InfoCF("agent", "loop: a settled run is no longer the session's selected step; no post-turn change applied",
			map[string]any{"session_id": sessionID, "job_id": job.ID})
	}
}

// loopCapStopError is settleTurn's outcome when a run reached the loop's run
// limit but the stopped state could not be saved.
type loopCapStopError struct{ err error }

func (e *loopCapStopError) Error() string {
	return "save the stopped loop state at the run limit: " + e.err.Error()
}

func (e *loopCapStopError) Unwrap() error { return e.err }

// loopNextStepError is settleTurn's outcome when a self-paced run completed
// but its next step could not be added to the engine.
type loopNextStepError struct{ err error }

func (e *loopNextStepError) Error() string {
	return "schedule the next self-paced run: " + e.err.Error()
}

func (e *loopNextStepError) Unwrap() error { return e.err }

// effectiveLoopMaxRuns is the loop's saved run limit, defaulted when a saved
// value is below 1.
func effectiveLoopMaxRuns(meta *session.UnifiedMeta) int {
	if meta.LoopMaxRuns < 1 {
		return config.DefaultLoopMaxRuns
	}
	return meta.LoopMaxRuns
}

// IdleExpirySweep expires any `/loop` job idle for longer than its effective
// IdleExpiryDays bound (FR-064/D7, review r1 blocker) — the `/loop`
// counterpart to plan_engine.go's PlanEngine.idleExpirySweep, driven from the
// SAME periodic tick (PlanEngine.goalAndLoopIdleExpirySweep) rather than a
// second ticker. "idle" = no scheduled run has actually fired
// (LoopLastActivityAt, bumped in RunScheduled above) within the bound. now is
// caller-supplied (PlanEngine's own injectable clock) so tests can pin exact
// idle-boundary math without a real sleep.
func (s *LoopScheduler) IdleExpirySweep(cfg config.PlanningConfig, now time.Time) {
	if s.al == nil {
		return
	}
	maxDays := cfg.EffectiveIdleExpiryDays(nil)
	expired := map[string]bool{}
	for _, job := range s.ListEnabledJobs() {
		sessionID := job.Payload.Message
		if sessionID == "" || expired[sessionID] {
			continue
		}
		store := s.al.ResolveSessionStore(sessionID)
		if store == nil {
			continue
		}
		meta, err := store.GetMeta(sessionID)
		if err != nil || meta == nil || meta.LoopMode == "" {
			continue
		}
		last := effectiveLoopActivity(meta)
		if last.IsZero() {
			continue // nothing to compare against; skip rather than guess
		}
		if now.Sub(last) < time.Duration(maxDays)*24*time.Hour {
			continue
		}
		// Issue #1306 (no clock ends work): a loop whose session still has a
		// live turn (a run that is taking longer than the idle bound) is not
		// idle — its activity stamp is only written after the run returns.
		// Architect R2-A1: a due run still waiting for a cron lane slot has
		// no turn yet, but it is queued work all the same.
		if s.cs.JobDispatchInFlight(job.ID) {
			logger.InfoCF("agent", "loop idle sweep: loop past its idle bound but a run is queued or running — not expiring",
				map[string]any{"session_id": sessionID, "job_id": job.ID})
			continue
		}
		if busy, why := s.al.sessionWorkInFlight(sessionID); busy {
			logger.InfoCF("agent", "loop idle sweep: loop past its idle bound but a run is still working — not expiring",
				map[string]any{"session_id": sessionID, "job_id": job.ID, "why": why})
			continue
		}
		// The stopped state and every loop job of the session go together
		// under the session's lock (FR-05): a leftover step must not survive
		// the expiry, and a settling turn cannot add one after it.
		// stopSessionLoop also publishes the stopped frame and the "loop"
		// admission release once the stop is saved.
		_, serr := s.al.stopSessionLoop(sessionID, store, s, fmt.Sprintf("idle-expired after %d day(s)", maxDays), nil)
		var left *loopJobsLeftError
		if errors.As(serr, &left) {
			// The stop is saved; a leftover job removes itself when it fires.
			logger.WarnCF("agent", "loop idle sweep: the loop is stopped but some of its jobs could not be removed",
				map[string]any{"session_id": sessionID, "error": serr.Error()})
		} else if serr != nil {
			logger.WarnCF("agent", "loop idle sweep: could not save the stopped loop state — not expiring this pass",
				map[string]any{"session_id": sessionID, "error": serr.Error()})
			continue
		}
		expired[sessionID] = true
	}
}

// effectiveLoopActivity returns the best available "last real activity"
// timestamp for a loop session: LoopLastActivityAt when present, falling
// back to LoopStartedAt (mirrors plan_engine.go's effectiveLastActivity /
// goal_loop.go's effectiveGoalActivity).
func effectiveLoopActivity(m *session.UnifiedMeta) time.Time {
	for _, s := range []string{m.LoopLastActivityAt, m.LoopStartedAt} {
		if s == "" {
			continue
		}
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// loopSelfPacedSteeringPrompt wraps a self-paced /loop's stored prompt with
// the LOOP_NEXT marker instruction (FR-072: "the agent chooses its own next
// delay + stated reason").
func loopSelfPacedSteeringPrompt(prompt string) string {
	return prompt + "\n\n" +
		"This is a self-paced recurring check-in. At the end of your reply, " +
		"state your next check-in delay and a brief reason in EXACTLY this " +
		"format on its own line: `LOOP_NEXT: <N><s|m|h|d> — <reason>` " +
		"(e.g. `LOOP_NEXT: 10m — waiting for the build to finish`)."
}

// parseLoopNextMarker extracts the delay (in ms) and reason from a
// self-paced run's reply, via loopNextMarkerRe. Returns (0, "") when no
// marker is found.
func parseLoopNextMarker(reply string) (delayMS int64, reason string) {
	m := loopNextMarkerRe.FindStringSubmatch(reply)
	if m == nil {
		return 0, ""
	}
	n, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil || n <= 0 {
		return 0, ""
	}
	var unit time.Duration
	switch m[2] {
	case "s", "S":
		unit = time.Second
	case "m", "M":
		unit = time.Minute
	case "h", "H":
		unit = time.Hour
	case "d", "D":
		unit = 24 * time.Hour
	default:
		return 0, ""
	}
	return n * unit.Milliseconds(), m[3]
}

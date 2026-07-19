// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// loop_scheduler.go implements the time-driven half of `/loop` (ADR-049
// D6/D7, spec Part B US-9): interval-mode cron `every` jobs and self-paced
// one-shot `at` jobs. Mirrors TaskTriggerScheduler's proven shape exactly
// (task_trigger.go's doc comment) — a SECOND, dedicated pkg/cron.CronService
// instance (separate store file: <home>/loops/jobs.json) rather than
// co-mingling `/loop` jobs into the gateway's user-schedules service, for the
// same "keep the systems orthogonal" reason task triggers do. RunScheduled
// fires by calling back into AgentLoop.ProcessScheduled — the SAME headless,
// session-continuing entry point cron/heartbeat jobs use (pkg/gateway/
// schedules.go's own RunScheduled), so a /loop run gets the identical
// transcript-recording, auto-deny-ask, and deadline-bounded semantics a
// SessionMode=continue schedule fire gets.
package agent

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/cron"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// loopJobKind marks a cron job as belonging to /loop (mirrors
// heartbeatJobKind's convention, pkg/gateway/heartbeat_schedule.go).
const loopJobKind = "loop"

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

// LoopScheduler registers `/loop` cron jobs in a dedicated CronService and
// fires them by calling back into AgentLoop.ProcessScheduled. It implements
// cron.ScheduledRunner.
type LoopScheduler struct {
	cs *cron.CronService
	al *AgentLoop
}

// NewLoopScheduler creates a scheduler using a dedicated CronService whose
// store lives at storePath.
func NewLoopScheduler(storePath string, al *AgentLoop) *LoopScheduler {
	return &LoopScheduler{cs: cron.NewCronService(storePath), al: al}
}

// Start wires this scheduler as its own cron runner and starts the engine.
func (s *LoopScheduler) Start() error {
	s.cs.SetRunner(s)
	return s.cs.Start()
}

// Stop shuts down the underlying dedicated cron engine.
func (s *LoopScheduler) Stop() { s.cs.Stop() }

// SetClock passes a test clock through to the underlying CronService (used
// by the self-paced-reschedule fake-clock test).
func (s *LoopScheduler) SetClock(c cron.Clock) { s.cs.SetClock(c) }

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
		Name:        loopJobName(sessionID),
		Schedule:    cron.CronSchedule{Kind: "every", EveryMS: &everyCopy},
		Message:     sessionID, // payload carries the session id; RunScheduled reads loop state from session meta
		AgentID:     ownerAgentID,
		SessionMode: cron.SessionModeContinue,
	})
	if err != nil {
		return "", fmt.Errorf("loop_scheduler: add interval job: %w", err)
	}
	job.Payload.Kind = loopJobKind
	if uerr := s.cs.UpdateJob(job); uerr != nil {
		return "", fmt.Errorf("loop_scheduler: stamp interval job kind: %w", uerr)
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
	at := s.cs.Now().UnixMilli() + delayMS
	job, err := s.cs.AddJobFull(cron.JobSpec{
		Name:        loopJobName(sessionID),
		Schedule:    cron.CronSchedule{Kind: "at", AtMS: &at},
		Message:     sessionID,
		AgentID:     ownerAgentID,
		SessionMode: cron.SessionModeContinue,
	})
	if err != nil {
		return "", fmt.Errorf("loop_scheduler: add one-shot job: %w", err)
	}
	job.Payload.Kind = loopJobKind
	if uerr := s.cs.UpdateJob(job); uerr != nil {
		return "", fmt.Errorf("loop_scheduler: stamp one-shot job kind: %w", uerr)
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
// run: increments run_count (persisted before checking the bound, so a
// crash mid-run never re-fires the same count — mirrors the task attempt
// counter's "persist first" discipline), stops the loop on reaching
// LoopMaxRuns (FR-072's run-count boundary), and for self-paced mode
// schedules the next one-shot fire from the run's own LOOP_NEXT reply
// marker (falling back to a fixed delay, WARN-logged, if the model did not
// emit one).
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
		// than firing a run nobody asked for.
		s.cs.RemoveJob(job.ID)
		return "", nil
	}

	maxRuns := meta.LoopMaxRuns
	if maxRuns < 1 {
		maxRuns = config.DefaultLoopMaxRuns
	}
	newRun := meta.LoopRunCount + 1

	prompt := meta.LoopPrompt
	if meta.LoopMode == loopModeSelfPaced {
		prompt = loopSelfPacedSteeringPrompt(prompt)
	}

	reply, runErr := s.al.ProcessScheduled(ctx, job.AgentID, sessionID, prompt, "", "")

	if perr := store.SetMeta(sessionID, session.MetaPatch{LoopRunCount: &newRun}); perr != nil {
		logger.WarnCF("agent", "loop: failed to persist run_count",
			map[string]any{"session_id": sessionID, "error": perr.Error()})
	}

	if newRun >= maxRuns {
		s.al.stopLoop(sessionID, store, fmt.Sprintf("run cap reached (%d/%d)", newRun, maxRuns))
		s.cs.RemoveJob(job.ID)
		return reply, runErr
	}

	if meta.LoopMode == loopModeSelfPaced {
		delayMS, _ := parseLoopNextMarker(reply)
		if delayMS <= 0 {
			delayMS = defaultSelfPacedFallbackDelayMS
			logger.WarnCF("agent", "loop: self-paced run produced no parseable LOOP_NEXT marker; using fallback delay",
				map[string]any{"session_id": sessionID, "fallback_ms": delayMS})
		}
		newJobID, aerr := s.AddOneShot(job.AgentID, sessionID, delayMS)
		if aerr != nil {
			logger.WarnCF("agent", "loop: failed to schedule next self-paced run",
				map[string]any{"session_id": sessionID, "error": aerr.Error()})
			return reply, runErr
		}
		delayInt := int(delayMS)
		if perr := store.SetMeta(sessionID, session.MetaPatch{
			LoopJobID:       &newJobID,
			LoopNextDelayMS: &delayMS,
		}); perr != nil {
			logger.WarnCF("agent", "loop: failed to persist next-run job id",
				map[string]any{"session_id": sessionID, "error": perr.Error()})
		}
		s.al.emitLoopStatusFrame(sessionID, meta.LoopMode, newRun, maxRuns, &delayInt, "scheduled_next")
		return reply, runErr
	}

	// Interval mode: cron's own computeNextRun already recomputed
	// NextRunAtMS for the "every" schedule — nothing further to schedule.
	s.al.emitLoopStatusFrame(sessionID, meta.LoopMode, newRun, maxRuns, nil, "run_completed")
	return reply, runErr
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

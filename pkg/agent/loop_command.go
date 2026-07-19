// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// loop_command.go implements the `/loop` command surface (ADR-049 D6/D7,
// spec Part B US-9): parsing, admission, persistence, and status/stop
// verbs. applyLoopCommandPrompt is handleCommand's rewrite hook (mirrors
// applyGoalCommandPrompt's shape); the time-driven firing mechanics live in
// loop_scheduler.go's LoopScheduler.
package agent

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/commands"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// The two UnifiedMeta.LoopMode values (FR-071/072).
const (
	loopModeInterval  = "interval"
	loopModeSelfPaced = "self_paced"
)

// loopEveryPrefixRe matches "every <interval> <prompt>" (FR-071).
var loopEveryPrefixRe = regexp.MustCompile(`(?i)^every\s+(\S+)\s+(.+)$`)

// loopDaySuffixRe matches a bare "<N>d" interval — a day unit outside
// time.ParseDuration's vocabulary (s/m/h only).
var loopDaySuffixRe = regexp.MustCompile(`^([0-9]+)d$`)

// SetLoopScheduler installs the /loop time-driven scheduler (ADR-049 D6/D7).
// Called once at boot by the gateway (mirrors SetTaskTriggerScheduler/
// SetPlanEngine). Idempotent.
func (al *AgentLoop) SetLoopScheduler(s *LoopScheduler) {
	al.mu.Lock()
	al.loopSched = s
	al.mu.Unlock()
}

// loopScheduler returns the installed LoopScheduler (nil in tests or before
// boot wiring completes).
func (al *AgentLoop) loopScheduler() *LoopScheduler {
	al.mu.RLock()
	defer al.mu.RUnlock()
	return al.loopSched
}

// applyLoopCommandPrompt is handleCommand's rewrite hook for `/loop`. Every
// verb (status, stop, interval-set, self-paced-set) answers synchronously
// (matched=true, handled=true) — `/loop` itself never runs a worker turn;
// the cron-fired run does that later via LoopScheduler.RunScheduled.
func (al *AgentLoop) applyLoopCommandPrompt(
	ctx context.Context,
	msg bus.InboundMessage,
	agentInst *AgentInstance,
	opts *processOptions,
) (matched bool, handled bool, reply string) {
	cmdName, ok := commands.CommandName(msg.Content)
	if !ok || cmdName != "loop" {
		return false, false, ""
	}

	// Gap #8/r2, R6: fail-closed origin gate — see applyGoalCommandPrompt's
	// identical check for the full rationale.
	if opts == nil || !opts.UserInitiated || opts.IsTaskRun {
		return false, false, ""
	}

	if opts.TranscriptStore == nil || opts.TranscriptSessionID == "" {
		return true, true, "`/loop` requires an active session — start a chat first."
	}
	ls := al.loopScheduler()
	if ls == nil {
		return true, true, "`/loop` is unavailable (scheduler not initialized)."
	}
	if agentInst == nil {
		return true, true, "`/loop` requires a resolved agent."
	}

	store := opts.TranscriptStore
	sessionID := opts.TranscriptSessionID
	args := commands.CommandArgs(msg.Content)

	if args == "" {
		return true, true, al.loopStatusReply(sessionID, store)
	}
	if strings.EqualFold(strings.TrimSpace(args), "stop") {
		return true, true, al.stopLoopCommand(sessionID, store, ls)
	}

	if pe := GetPlanEngine(al); pe != nil {
		if admitted, active, capN := pe.Admit("loop"); !admitted {
			return true, true, fmt.Sprintf(
				"Cannot start a new loop: active loops %d/%d (cap reached). "+
					"Stop an existing /goal or /loop, or wait for a running plan to finish.",
				active, capN,
			)
		}
	}

	// Replace-on-set: an already-active loop on this session is stopped
	// first, mirroring /goal's FR-068 one-per-session discipline.
	if existing, merr := store.GetMeta(sessionID); merr == nil && existing != nil && existing.LoopMode != "" {
		ls.RemoveJob(existing.LoopJobID)
		al.stopLoop(sessionID, store, "replaced by new /loop")
	}

	maxRuns := config.DefaultLoopMaxRuns
	if cfg := al.GetConfig(); cfg != nil {
		maxRuns = cfg.Planning.EffectiveLoopMaxRuns()
	}

	if prompt, everyMS, ok := parseLoopEvery(args); ok {
		return true, true, al.startIntervalLoop(sessionID, store, ls, agentInst.ID, prompt, everyMS, maxRuns)
	}
	return true, true, al.startSelfPacedLoop(sessionID, store, ls, agentInst.ID, args, maxRuns)
}

// parseLoopEvery splits "every <interval> <prompt>" into its parts (FR-071).
// ok is false when args does not start with "every <token> ".
func parseLoopEvery(args string) (prompt string, everyMS int64, ok bool) {
	m := loopEveryPrefixRe.FindStringSubmatch(strings.TrimSpace(args))
	if m == nil {
		return "", 0, false
	}
	d, err := parseLoopInterval(m[1])
	if err != nil {
		return "", 0, false
	}
	return strings.TrimSpace(m[2]), d.Milliseconds(), true
}

// parseLoopInterval parses a duration token. Accepts anything
// time.ParseDuration accepts (s/m/h combinations, e.g. "5m", "1h30m") plus a
// bare "<N>d" day suffix (outside time.ParseDuration's vocabulary).
func parseLoopInterval(s string) (time.Duration, error) {
	if d, err := time.ParseDuration(s); err == nil && d > 0 {
		return d, nil
	}
	if m := loopDaySuffixRe.FindStringSubmatch(s); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil && n > 0 {
			return time.Duration(n) * 24 * time.Hour, nil
		}
	}
	return 0, fmt.Errorf("invalid interval %q", s)
}

// startIntervalLoop creates the cron `every` job and persists interval-mode
// loop state (FR-071).
func (al *AgentLoop) startIntervalLoop(
	sessionID string, store *session.UnifiedStore, ls *LoopScheduler,
	ownerAgentID, prompt string, everyMS int64, maxRuns int,
) string {
	jobID, err := ls.AddInterval(ownerAgentID, sessionID, everyMS)
	if err != nil {
		logger.WarnCF("agent", "loop: failed to create interval cron job",
			map[string]any{"session_id": sessionID, "error": err.Error()})
		return "Could not start the loop (internal error creating the schedule)."
	}
	mode := loopModeInterval
	zero := 0
	nowStr := time.Now().UTC().Format(time.RFC3339)
	if perr := store.SetMeta(sessionID, session.MetaPatch{
		LoopMode:       &mode,
		LoopPrompt:     &prompt,
		LoopRunCount:   &zero,
		LoopMaxRuns:    &maxRuns,
		LoopIntervalMS: &everyMS,
		LoopJobID:      &jobID,
		LoopStartedAt:  &nowStr,
	}); perr != nil {
		logger.WarnCF("agent", "loop: failed to persist interval loop set",
			map[string]any{"session_id": sessionID, "error": perr.Error()})
		ls.RemoveJob(jobID)
		return "Could not start the loop (internal error persisting session state)."
	}
	al.emitLoopStatusFrame(sessionID, mode, 0, maxRuns, nil, "active")
	return fmt.Sprintf(
		"Loop started: every %s — %q (run 0/%d).",
		time.Duration(everyMS)*time.Millisecond, prompt, maxRuns,
	)
}

// startSelfPacedLoop schedules the first (effectively-immediate) one-shot
// run and persists self-paced-mode loop state (FR-072).
func (al *AgentLoop) startSelfPacedLoop(
	sessionID string, store *session.UnifiedStore, ls *LoopScheduler,
	ownerAgentID, prompt string, maxRuns int,
) string {
	jobID, err := ls.AddOneShot(ownerAgentID, sessionID, firstSelfPacedRunDelayMS)
	if err != nil {
		logger.WarnCF("agent", "loop: failed to create self-paced cron job",
			map[string]any{"session_id": sessionID, "error": err.Error()})
		return "Could not start the loop (internal error creating the schedule)."
	}
	mode := loopModeSelfPaced
	zero := 0
	var zeroMS int64
	nowStr := time.Now().UTC().Format(time.RFC3339)
	if perr := store.SetMeta(sessionID, session.MetaPatch{
		LoopMode:       &mode,
		LoopPrompt:     &prompt,
		LoopRunCount:   &zero,
		LoopMaxRuns:    &maxRuns,
		LoopIntervalMS: &zeroMS,
		LoopJobID:      &jobID,
		LoopStartedAt:  &nowStr,
	}); perr != nil {
		logger.WarnCF("agent", "loop: failed to persist self-paced loop set",
			map[string]any{"session_id": sessionID, "error": perr.Error()})
		ls.RemoveJob(jobID)
		return "Could not start the loop (internal error persisting session state)."
	}
	al.emitLoopStatusFrame(sessionID, mode, 0, maxRuns, nil, "active")
	return fmt.Sprintf(
		"Self-paced loop started: %q (run 0/%d). The agent will choose its own check-in cadence after each run.",
		prompt, maxRuns,
	)
}

// loopStatusReply formats `/loop status`'s deterministic reply (FR-073).
func (al *AgentLoop) loopStatusReply(sessionID string, store *session.UnifiedStore) string {
	meta, err := store.GetMeta(sessionID)
	if err != nil || meta == nil || meta.LoopMode == "" {
		return "No active loop on this session. Use `/loop every <interval> <prompt>` or `/loop <prompt>` to start one."
	}
	active, capN := al.activeLoopsSnapshot("loop")
	elapsed := "unknown"
	if t, perr := time.Parse(time.RFC3339, meta.LoopStartedAt); perr == nil {
		elapsed = time.Since(t).Round(time.Second).String()
	}
	cadence := "self-paced"
	switch {
	case meta.LoopMode == loopModeInterval:
		cadence = fmt.Sprintf("every %s", time.Duration(meta.LoopIntervalMS)*time.Millisecond)
	case meta.LoopNextDelayMS > 0:
		cadence = fmt.Sprintf("self-paced (next in %s)", time.Duration(meta.LoopNextDelayMS)*time.Millisecond)
	}
	return fmt.Sprintf(
		"Loop: %s\nMode: %s\nCadence: %s\nRuns: %d/%d\nElapsed: %s\nActive loops: %d/%d",
		meta.LoopPrompt, meta.LoopMode, cadence, meta.LoopRunCount, meta.LoopMaxRuns, elapsed, active, capN,
	)
}

// stopLoopCommand implements user-invoked `/loop stop` (FR-073): removes the
// cron job and clears loop state, returning the user-facing reply.
func (al *AgentLoop) stopLoopCommand(sessionID string, store *session.UnifiedStore, ls *LoopScheduler) string {
	meta, err := store.GetMeta(sessionID)
	hadLoop := err == nil && meta != nil && meta.LoopMode != ""
	if hadLoop {
		ls.RemoveJob(meta.LoopJobID)
	}
	al.stopLoop(sessionID, store, "stopped by user")
	if !hadLoop {
		return "No active loop to stop."
	}
	return "Loop stopped."
}

// stopLoop clears the session's loop fields — the shared body for
// `/loop stop`, the run-cap brake (LoopScheduler.RunScheduled), and a
// replace-on-set. Does NOT remove the cron job itself; callers that know a
// job is in flight (as opposed to already auto-deleted by cron for a
// one-shot "at" fire) must call LoopScheduler.RemoveJob separately.
func (al *AgentLoop) stopLoop(sessionID string, store *session.UnifiedStore, note string) {
	empty := ""
	zero := 0
	var zeroMS int64
	if err := store.SetMeta(sessionID, session.MetaPatch{
		LoopMode:        &empty,
		LoopPrompt:      &empty,
		LoopRunCount:    &zero,
		LoopMaxRuns:     &zero,
		LoopIntervalMS:  &zeroMS,
		LoopNextDelayMS: &zeroMS,
		LoopJobID:       &empty,
		LoopStartedAt:   &empty,
	}); err != nil {
		logger.WarnCF("agent", "loop: failed to clear loop state",
			map[string]any{"session_id": sessionID, "error": err.Error(), "note": note})
	}
	al.emitLoopStatusFrame(sessionID, "", 0, 0, nil, "stopped")
}

// emitLoopStatusFrame publishes a loop_status WS frame (FR-073).
func (al *AgentLoop) emitLoopStatusFrame(sessionID, mode string, run, maxRuns int, nextDelayMS *int, state string) {
	al.EmitLoopStatusChanged(LoopStatusChangedPayload{
		SessionID: sessionID,
		Mode:      mode,
		Run:       run,
		MaxRuns:   maxRuns,
		NextDelay: nextDelayMS,
		State:     state,
	})
}

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// loop_recovery.go is the /loop physical-boot recovery (T10 spec r6 FR-05,
// closes #1491). It runs after the shared engine's checked load and crash-flag
// clear and BEFORE the engine is activated (FR-03), so nothing it removes can
// fire and nothing it keeps fires before its identity is recorded.
//
// Per session, from durable state:
//   - no loop metadata: every loop job of the session is an orphan and goes;
//   - saved run count at the saved limit (a): every job goes and the stopped
//     state is persisted, nothing is armed;
//   - a selected step that is a stop-only step (the last run's stop, and
//     maybe its count, was not saved): the stopped state is persisted first
//     and only then every job goes, so a failed persist keeps the step as
//     the record of the reached limit; nothing is armed;
//   - otherwise exactly ONE next step survives (c): the selected step when it
//     is still due in the future; a fresh step of the session's own mode
//     otherwise — self-paced: an `at` at boot time plus the persisted positive
//     delay (b); interval: an `every` at the persisted interval. Every other
//     loop job of the session goes;
//   - the session's LoopJobID is repaired to the survivor (d).
//
// A required write that fails — the identity repair, or the stopped-state
// persist — makes recovery incomplete (h): no stopped state is claimed, a
// job created or kept for the session is removed (a stop-only step is
// kept), and the error names the
// session so the gateway aborts the boot before activation. The next boot
// retries from durable state. Recovery is idempotent.
package agent

import (
	"errors"
	"fmt"
	"os"
	"sort"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// Reconcile is the /loop boot recovery. See the file comment for the rules.
// It touches only `loop`-kind jobs (FR-25).
func (s *LoopScheduler) Reconcile() error {
	if s.al == nil {
		return errors.New("loop recovery: no agent loop to read session loop state from")
	}
	sessions := map[string]bool{}
	for _, j := range s.cs.ListJobs(true) {
		if j.Payload.Kind == loopJobKind {
			sessions[j.Payload.Message] = true
		}
	}
	if store := s.al.GetSessionStore(); store != nil {
		metas, err := store.ListSessions()
		if err != nil {
			return fmt.Errorf("loop recovery: list sessions: %w", err)
		}
		for _, m := range metas {
			if m != nil && m.LoopMode != "" {
				sessions[m.ID] = true
			}
		}
	}
	ids := make([]string, 0, len(sessions))
	for id := range sessions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, sessionID := range ids {
		if err := s.recoverSession(sessionID); err != nil {
			return err
		}
	}
	return nil
}

// recoverSession applies the recovery rules to one session.
func (s *LoopScheduler) recoverSession(sessionID string) error {
	var store *session.UnifiedStore
	var meta *session.UnifiedMeta
	if sessionID != "" {
		store = s.al.ResolveSessionStore(sessionID)
	}
	if store != nil {
		m, err := store.GetMeta(sessionID)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("loop recovery: session %s: read loop state: %w", sessionID, err)
		}
		meta = m
	}
	if store == nil || meta == nil || meta.LoopMode == "" {
		if err := s.removeSessionJobs(sessionID); err != nil {
			return fmt.Errorf("loop recovery: session %q: remove orphan loop jobs: %w", sessionID, err)
		}
		return nil
	}
	if meta.LoopRunCount >= effectiveLoopMaxRuns(meta) {
		return s.recoverStopped(store, sessionID, "saved run count reached its limit")
	}
	if selected, ok := selectStep(s.sessionJobs(sessionID)); ok && isStopOnlyStep(&selected) {
		// The last permitted run finished but its stop (and maybe its
		// count) was not saved: the loop is at its limit.
		return s.recoverPendingStop(store, sessionID)
	}
	keepID, created, err := s.recoverNextStep(sessionID, meta)
	if err != nil {
		return err
	}
	if keepID == "" {
		return s.recoverStopped(store, sessionID, "loop state cannot be re-armed")
	}
	if err := s.removeOtherSessionJobs(sessionID, keepID); err != nil {
		return s.abandonStep(sessionID, keepID, created, fmt.Errorf("remove superseded loop jobs: %w", err))
	}
	if meta.LoopJobID != keepID {
		id := keepID
		if err := store.SetMeta(sessionID, session.MetaPatch{LoopJobID: &id}); err != nil {
			return s.abandonStep(sessionID, keepID, true, fmt.Errorf("record the next step %s: %w", keepID, err))
		}
	}
	return nil
}

// recoverNextStep returns the job that survives as the session's next step:
// the selected step when it is still due in the future (an interval job is
// always kept — the engine re-arms `every` jobs itself), otherwise a fresh
// step of the session's own mode. An empty ID with no error means the
// persisted loop state cannot produce a step (logged by the caller's stop).
func (s *LoopScheduler) recoverNextStep(sessionID string, meta *session.UnifiedMeta) (id string, created bool, err error) {
	now := s.cs.Now().UnixMilli()
	selected, ok := selectStep(s.sessionJobs(sessionID))
	if ok && (selected.Schedule.Kind != "at" ||
		(selected.State.NextRunAtMS != nil && *selected.State.NextRunAtMS > now)) {
		return selected.ID, false, nil
	}
	owner := meta.AgentID
	if ok && selected.AgentID != "" {
		owner = selected.AgentID
	}
	switch {
	case owner == "":
		logger.WarnCF("agent", "loop recovery: the loop has no owning agent to re-arm",
			map[string]any{"session_id": sessionID})
		return "", false, nil
	case meta.LoopMode == loopModeInterval && meta.LoopIntervalMS > 0:
		id, err = s.AddInterval(owner, sessionID, meta.LoopIntervalMS)
	case meta.LoopMode == loopModeSelfPaced:
		delay := meta.LoopNextDelayMS
		if delay <= 0 {
			delay = firstSelfPacedRunDelayMS
		}
		id, err = s.AddOneShot(owner, sessionID, delay)
	default:
		logger.WarnCF("agent", "loop recovery: the saved loop mode or interval is not valid",
			map[string]any{"session_id": sessionID, "mode": meta.LoopMode, "interval_ms": meta.LoopIntervalMS})
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("loop recovery: session %q: arm the next step: %w", sessionID, err)
	}
	return id, true, nil
}

// recoverStopped removes every loop job of the session and persists the
// stopped state. A failed persist is a recovery error (FR-05(h)).
func (s *LoopScheduler) recoverStopped(store *session.UnifiedStore, sessionID, why string) error {
	if err := s.removeSessionJobs(sessionID); err != nil {
		return fmt.Errorf("loop recovery: session %q: remove loop jobs: %w", sessionID, err)
	}
	if err := store.SetMeta(sessionID, loopStoppedPatch()); err != nil {
		return fmt.Errorf("loop recovery: session %q: persist the stopped loop state: %w", sessionID, err)
	}
	logger.InfoCF("agent", "loop recovery: loop stopped at boot", map[string]any{"session_id": sessionID, "reason": why})
	return nil
}

// recoverPendingStop stops a loop whose selected step is a stop-only step.
// That step may be the only saved record that the loop reached its run limit
// (the count of its last run may not have been saved), so the stopped state
// is persisted BEFORE any job is removed: a failed persist leaves the step in
// place and the next boot retries the stop (FR-05(h)). Once the stop is
// saved, a job that cannot be removed is an orphan the next boot removes.
func (s *LoopScheduler) recoverPendingStop(store *session.UnifiedStore, sessionID string) error {
	if err := store.SetMeta(sessionID, loopStoppedPatch()); err != nil {
		return fmt.Errorf("loop recovery: session %q: persist the stopped loop state: %w", sessionID, err)
	}
	if err := s.removeSessionJobs(sessionID); err != nil {
		return fmt.Errorf("loop recovery: session %q: remove loop jobs: %w", sessionID, err)
	}
	logger.InfoCF("agent", "loop recovery: loop stopped at boot",
		map[string]any{"session_id": sessionID, "reason": "run limit reached; its stop was not saved"})
	return nil
}

// removeOtherSessionJobs removes every loop job of the session except keepID.
func (s *LoopScheduler) removeOtherSessionJobs(sessionID, keepID string) error {
	var ids []string
	for _, j := range s.sessionJobs(sessionID) {
		if j.ID != keepID {
			ids = append(ids, j.ID)
		}
	}
	return s.cs.RemoveJobs(ids)
}

// abandonStep is the FR-05(h) failure path: the step whose identity could not
// be recorded is removed, so no unrecorded job survives into activation, and
// the recovery error names the session.
func (s *LoopScheduler) abandonStep(sessionID, jobID string, remove bool, cause error) error {
	err := fmt.Errorf("loop recovery: session %q: %w", sessionID, cause)
	if remove {
		if rerr := s.cs.RemoveJobs([]string{jobID}); rerr != nil {
			err = errors.Join(err, fmt.Errorf("loop recovery: session %q: remove unrecorded step %s: %w", sessionID, jobID, rerr))
		}
	}
	return err
}

//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/cron"
	"github.com/dapicom-ai/omnipus/pkg/workspace"
)

// ADR-027 — heartbeat is now workspace-scoped. A workspace heartbeat is
// implemented as a recurring schedule in the one Schedules/cron engine, keyed
// "heartbeat:<workspaceID>:<agentID>". This file reconciles the cron store so
// that exactly the set of (workspace, agent) pairs with an enabled heartbeat
// carry a recurring job, using the body stored in member_configs as the prompt.

// heartbeatJobKind marks a cron job as a workspace-scoped heartbeat (vs. a
// user schedule). Shared with listSessions so isHeartbeatJob stays consistent.
const heartbeatJobKind = "heartbeat"

// heartbeatJobNamePrefix namespaces heartbeat jobs so the reconciler can
// find and own exactly its jobs without colliding with user-created schedules.
const heartbeatJobNamePrefix = "heartbeat:"

// heartbeatJobName returns the deterministic job name for a (workspace, agent) pair.
// Format: "heartbeat:<workspaceID>:<agentID>"
func heartbeatJobName(workspaceID, agentID string) string {
	return heartbeatJobNamePrefix + workspaceID + ":" + agentID
}

// isHeartbeatJob reports whether a cron job is a workspace-heartbeat job
// owned by this reconciler.
func isHeartbeatJob(j cron.CronJob) bool {
	return j.Payload.Kind == heartbeatJobKind || strings.HasPrefix(j.Name, heartbeatJobNamePrefix)
}

// workspaceListFunc returns all workspace records. Injected so the reconciler
// stays testable without a real filesystem.
type workspaceListFunc func() ([]workspace.Workspace, error)

// desiredHeartbeat is the reconciler's target spec for one (workspace, agent) pair.
type desiredHeartbeat struct {
	workspaceID string
	agentID     string
	interval    int // minutes (>= 5)
	message     string
	sessionID   string // eager standing session id (may be empty)
}

// buildHeartbeatMessage wraps a member's heartbeat body in the standard
// scheduled-heartbeat prompt. An empty body is accepted — the wrapper alone
// is enough to trigger a scheduled check-in.
func buildHeartbeatMessage(body string) string {
	trimmed := strings.TrimSpace(body)
	return fmt.Sprintf(`# Heartbeat Check

You are a proactive AI assistant. This is a scheduled heartbeat run.
Review the following tasks and execute any necessary actions using your available tools.
If nothing requires attention, respond ONLY with: HEARTBEAT_OK

%s`, trimmed)
}

// computeDesiredHeartbeats returns the heartbeat jobs that SHOULD exist: one
// per (workspace, agent) pair where member_configs[agentID].heartbeat.enabled
// is true. Workers are never given a heartbeat even if one is configured (the
// workspace handler rejects it at write time, but we defend here too).
//
// isWorker is used as a defense-in-depth guard in the reconciler (the primary
// enforcement is ValidateMemberConfigs at write time).
func computeDesiredHeartbeats(workspaces []workspace.Workspace, isWorker func(agentID string) bool) []desiredHeartbeat {
	var out []desiredHeartbeat
	for _, ws := range workspaces {
		for agentID, mc := range ws.MemberConfigs {
			hb := mc.Heartbeat
			if hb == nil || !hb.Enabled {
				continue
			}
			if isWorker != nil && isWorker(agentID) {
				slog.Warn("heartbeat reconcile: skipping worker agent (defense-in-depth)",
					"workspace_id", ws.ID, "agent_id", agentID)
				continue
			}
			interval := hb.IntervalMinutes
			if interval < 5 {
				interval = 30 // safe default when stored value is somehow below minimum
			}
			out = append(out, desiredHeartbeat{
				workspaceID: ws.ID,
				agentID:     agentID,
				interval:    interval,
				message:     buildHeartbeatMessage(hb.Body),
				sessionID:   hb.SessionID,
			})
		}
	}
	return out
}

// reconcileHeartbeatSchedules is the restAPI hook that re-runs the heartbeat
// reconciler after workspace member_configs change (workspace PUT). Best-effort:
// a nil cron service or a reconcile error is logged, never fatal — the persisted
// config is already the source of truth and the next boot reconcile will converge.
func (a *restAPI) reconcileHeartbeatSchedules() {
	cs := a.cronService.Load()
	if cs == nil {
		return
	}
	cfg := a.agentLoop.GetConfig()
	workspaces, err := listWorkspaceFiles(a.homePath)
	if err != nil {
		slog.Warn("rest: heartbeat reconcile: list workspaces failed", "error", err)
		return
	}
	isWorker := func(agentID string) bool {
		if cfg == nil {
			return false
		}
		for _, ac := range cfg.Agents.List {
			if ac.ID == agentID {
				return ac.IsWorker()
			}
		}
		return false
	}
	if err := ReconcileHeartbeatSchedules(cs, workspaces, isWorker); err != nil {
		slog.Warn("rest: heartbeat schedule reconcile failed", "error", err)
	}
}

// ReconcileHeartbeatSchedules brings the cron store into line with the
// workspace-scoped heartbeat configs (ADR-027). It is idempotent: create
// missing heartbeat jobs, update jobs whose interval/message/sessionID
// drifted, and remove heartbeat jobs whose (workspace, agent) pair no longer
// has an enabled heartbeat. It never touches non-heartbeat (user) schedules.
//
// Returns the first error encountered; reconciliation continues past per-job
// errors so one bad entry does not block the rest.
func ReconcileHeartbeatSchedules(cs *cron.CronService, workspaces []workspace.Workspace, isWorker func(agentID string) bool) error {
	if cs == nil {
		return fmt.Errorf("heartbeat reconcile: cron service is nil")
	}

	desired := computeDesiredHeartbeats(workspaces, isWorker)
	desiredByName := make(map[string]desiredHeartbeat, len(desired))
	for _, d := range desired {
		desiredByName[heartbeatJobName(d.workspaceID, d.agentID)] = d
	}

	// Index existing heartbeat jobs by name (include disabled so we can
	// re-enable or remove them).
	existing := make(map[string]cron.CronJob)
	for _, j := range cs.ListJobs(true) {
		if isHeartbeatJob(j) {
			existing[j.Name] = j
		}
	}

	var firstErr error
	recordErr := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}

	// Create or update desired jobs.
	for name, d := range desiredByName {
		everyMS := int64(d.interval) * 60_000
		if cur, ok := existing[name]; ok {
			// Update in place when interval, message, sessionID, enabled, or
			// agent/kind drifted.
			needsUpdate := !cur.Enabled ||
				cur.Schedule.Kind != "every" ||
				cur.Schedule.EveryMS == nil || *cur.Schedule.EveryMS != everyMS ||
				cur.Payload.Message != d.message ||
				cur.AgentID != d.agentID ||
				cur.Payload.Kind != heartbeatJobKind ||
				cur.SessionID != d.sessionID
			if needsUpdate {
				cur.Enabled = true
				cur.AgentID = d.agentID
				cur.SessionMode = cron.SessionModeContinue
				cur.SessionID = d.sessionID
				cur.Schedule = cron.CronSchedule{Kind: "every", EveryMS: &everyMS}
				cur.Payload = cron.CronPayload{Kind: heartbeatJobKind, Message: d.message, Deliver: false}
				if err := cs.UpdateJob(&cur); err != nil {
					recordErr(fmt.Errorf("heartbeat reconcile: update %s: %w", name, err))
				}
			}
			continue
		}
		// Create a new heartbeat job.
		enabled := true
		created, err := cs.AddJobFull(cron.JobSpec{
			Name:        name,
			Schedule:    cron.CronSchedule{Kind: "every", EveryMS: &everyMS},
			Message:     d.message,
			AgentID:     d.agentID,
			SessionMode: cron.SessionModeContinue,
			SessionID:   d.sessionID,
			Enabled:     &enabled,
		})
		if err != nil {
			recordErr(fmt.Errorf("heartbeat reconcile: add %s: %w", name, err))
			continue
		}
		// AddJobFull stamps Payload.Kind="agent_turn"; re-stamp it as a heartbeat
		// so isHeartbeatJob recognizes it on the next reconcile by kind, not just
		// name. The returned job is a copy carrying the assigned ID.
		if created != nil {
			created.Payload.Kind = heartbeatJobKind
			if err := cs.UpdateJob(created); err != nil {
				recordErr(fmt.Errorf("heartbeat reconcile: stamp kind %s: %w", name, err))
			}
		}
	}

	// Remove heartbeat jobs that are no longer desired.
	for name, j := range existing {
		if _, ok := desiredByName[name]; !ok {
			if !cs.RemoveJob(j.ID) {
				recordErr(fmt.Errorf("heartbeat reconcile: remove %s (id=%s) failed", name, j.ID))
			}
		}
	}

	slog.Info("heartbeat schedules reconciled",
		"desired", len(desiredByName), "existing_before", len(existing),
		"reconciled_at", time.Now().UTC().Format(time.RFC3339))
	return firstErr
}

// releaseHeartbeatJobsForWorkspace removes all heartbeat cron jobs associated
// with a workspace (used in cascade-delete: workspace DELETE → cron cleanup).
// Best-effort: each job that cannot be removed is logged and skipped.
func releaseHeartbeatJobsForWorkspace(cs *cron.CronService, workspaceID string) {
	if cs == nil {
		return
	}
	prefix := heartbeatJobNamePrefix + workspaceID + ":"
	for _, j := range cs.ListJobs(true) {
		if !strings.HasPrefix(j.Name, prefix) {
			continue
		}
		if !cs.RemoveJob(j.ID) {
			slog.Warn("heartbeat cascade: failed to remove cron job for workspace",
				"workspace_id", workspaceID, "job_name", j.Name, "job_id", j.ID)
		}
	}
}

// agentWorkspaceFunc is kept for backward compat with any call sites that
// reference it by name (e.g. legacy test helpers). It is no longer used by the
// main reconciler path, which iterates workspaces directly.
//
// Deprecated: use ReconcileHeartbeatSchedules with a []workspace.Workspace slice.
type agentWorkspaceFunc = func(agentID string) string

// configOnlyIsWorker returns an isWorker predicate backed by a config snapshot.
// Exported for test use.
func configOnlyIsWorker(cfg *config.Config) func(agentID string) bool {
	return func(agentID string) bool {
		if cfg == nil {
			return false
		}
		for _, ac := range cfg.Agents.List {
			if ac.ID == agentID {
				return ac.IsWorker()
			}
		}
		return false
	}
}

//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/cron"
)

// O6 — "heartbeat IS a schedule." A Main agent's heartbeat is implemented as a
// per-agent recurring schedule in the one Schedules/cron engine, NOT as separate
// loop.go logic. This file reconciles the cron store so that exactly the set of
// Main agents with an enabled per-agent heartbeat carry a recurring heartbeat job
// that runs their HEARTBEAT.md. Subagents (Type=worker) never get one.

// heartbeatJobKind marks a cron job as an agent heartbeat (vs. a user schedule).
const heartbeatJobKind = "heartbeat"

// heartbeatJobNamePrefix namespaces heartbeat jobs so the reconciler can find
// and own exactly its jobs without colliding with user-created schedules.
const heartbeatJobNamePrefix = "heartbeat:"

// heartbeatJobName returns the deterministic job name for an agent's heartbeat.
func heartbeatJobName(agentID string) string {
	return heartbeatJobNamePrefix + agentID
}

// isHeartbeatJob reports whether a cron job is an agent-heartbeat job owned by
// this reconciler.
func isHeartbeatJob(j cron.CronJob) bool {
	return j.Payload.Kind == heartbeatJobKind || strings.HasPrefix(j.Name, heartbeatJobNamePrefix)
}

// agentWorkspaceFunc resolves an agent ID to its workspace directory (where
// HEARTBEAT.md lives). Injected so the reconciler stays testable without a full
// gateway.
type agentWorkspaceFunc func(agentID string) string

// buildHeartbeatMessage reads the agent's HEARTBEAT.md and wraps it in the
// scheduled-heartbeat prompt. Returns "" when the file is missing/empty (the
// caller still schedules the job; an empty body just runs the wrapper, mirroring
// the legacy heartbeat service's tolerance for an empty file).
func buildHeartbeatMessage(workspace string) string {
	content := ""
	if workspace != "" {
		data, err := os.ReadFile(filepath.Join(workspace, "HEARTBEAT.md"))
		if err == nil {
			content = strings.TrimSpace(string(data))
		} else if !os.IsNotExist(err) {
			slog.Warn("heartbeat schedule: read HEARTBEAT.md failed", "workspace", workspace, "error", err)
		}
	}
	return fmt.Sprintf(`# Heartbeat Check

You are a proactive AI assistant. This is a scheduled heartbeat run.
Review the following tasks and execute any necessary actions using your available tools.
If nothing requires attention, respond ONLY with: HEARTBEAT_OK

%s`, content)
}

// desiredHeartbeat is the reconciler's target spec for one agent.
type desiredHeartbeat struct {
	agentID  string
	interval int // minutes (>= 5)
	message  string
}

// computeDesiredHeartbeats returns the heartbeat jobs that SHOULD exist: one per
// Main agent (non-worker) with an enabled per-agent heartbeat. The constant
// config.DefaultHeartbeatIntervalMinutes is the fallback when an agent leaves its
// own interval unset.
func computeDesiredHeartbeats(cfg *config.Config, agentWorkspace agentWorkspaceFunc) []desiredHeartbeat {
	if cfg == nil {
		return nil
	}
	var out []desiredHeartbeat
	for i := range cfg.Agents.List {
		ac := cfg.Agents.List[i]
		if ac.IsWorker() {
			continue // subagents never have a heartbeat
		}
		if !ac.HeartbeatIsEnabled() {
			continue
		}
		ws := ""
		if agentWorkspace != nil {
			ws = agentWorkspace(ac.ID)
		}
		out = append(out, desiredHeartbeat{
			agentID:  ac.ID,
			interval: ac.HeartbeatIntervalMinutes(config.DefaultHeartbeatIntervalMinutes),
			message:  buildHeartbeatMessage(ws),
		})
	}
	return out
}

// reconcileHeartbeatSchedules is the restAPI hook that re-runs the heartbeat
// reconciler after an agent's heartbeat config changes (create/update). It reads
// the live cron service + config and resolves each agent's workspace. Best-effort:
// a nil cron service (not wired in a unit test) or a reconcile error is logged,
// never fatal — the persisted config is already the source of truth and the next
// boot reconcile will converge.
func (a *restAPI) reconcileHeartbeatSchedules() {
	cs := a.cronService.Load()
	if cs == nil {
		return
	}
	cfg := a.agentLoop.GetConfig()
	if err := ReconcileHeartbeatSchedules(cs, cfg, func(agentID string) string {
		ws, wsErr := agentWorkspacePath(cfg, agentID, "", a.homePath)
		if wsErr != nil {
			return ""
		}
		return ws
	}); err != nil {
		slog.Warn("rest: heartbeat schedule reconcile failed", "error", err)
	}
}

// ReconcileHeartbeatSchedules brings the cron store into line with the per-agent
// heartbeat config (O6). It is idempotent: create missing heartbeat jobs, update
// jobs whose interval/message drifted, and remove heartbeat jobs whose agent no
// longer wants one. It never touches non-heartbeat (user) schedules.
//
// Returns the first error encountered; reconciliation continues past per-job
// errors so one bad agent does not block the rest.
func ReconcileHeartbeatSchedules(cs *cron.CronService, cfg *config.Config, agentWorkspace agentWorkspaceFunc) error {
	if cs == nil {
		return fmt.Errorf("heartbeat reconcile: cron service is nil")
	}

	desired := computeDesiredHeartbeats(cfg, agentWorkspace)
	desiredByName := make(map[string]desiredHeartbeat, len(desired))
	for _, d := range desired {
		desiredByName[heartbeatJobName(d.agentID)] = d
	}

	// Index existing heartbeat jobs by name (include disabled so we can re-enable
	// or remove them).
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
			// Update in place when interval, message, enabled, or agent drifted.
			needsUpdate := !cur.Enabled ||
				cur.Schedule.Kind != "every" ||
				cur.Schedule.EveryMS == nil || *cur.Schedule.EveryMS != everyMS ||
				cur.Payload.Message != d.message ||
				cur.AgentID != d.agentID ||
				cur.Payload.Kind != heartbeatJobKind
			if needsUpdate {
				cur.Enabled = true
				cur.AgentID = d.agentID
				cur.SessionMode = cron.SessionModeContinue
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

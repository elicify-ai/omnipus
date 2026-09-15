// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// handleTaskRuns handles GET /api/v1/tasks/{id}/runs. Called from
// HandleTasks' sub-resource switch (rest_tasks.go), which wraps it in
// withRateLimit(taskReadLimiter, ...).
func (a *restAPI) handleTaskRuns(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		// falls through to the GET (history-list) body below
	case http.MethodPost:
		a.handleTaskRunNow(w, r, id)
		return
	default:
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if err := validateEntityID(id); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid task ID")
		return
	}

	// The run-history list is scoped to a real task: 404 for an id that
	// never existed or was deleted, mirroring handleTaskGet's convention.
	// (A task whose SCHEDULE was later edited still has its history —
	// RD8/§3.6's "independent of occurrence projection" — this check is
	// about the task row itself, not whether its trigger still projects
	// the runs' occurrence_ms values.)
	if _, err := a.taskStore.Get(id); err != nil {
		if errors.Is(err, task.ErrNotFound) {
			jsonErr(w, http.StatusNotFound, "task not found")
			return
		}
		slog.Error("rest: task runs: get task failed", "task_id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not read task")
		return
	}

	runs, err := a.taskStore.ListRuns(id)
	if err != nil {
		slog.Error("rest: list task runs failed", "task_id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not list task runs")
		return
	}

	// ListRuns already returns newest-first (started_at desc); preserve
	// that order end to end — the wire array is `TaskRun[]` with no
	// re-sorting contract. toWireTaskRun's ok=false (H4: an invalid stored
	// status) drops just that record, mirroring foldRunsLocked's own
	// malformed-record skip rather than propagating a schema-invalid entry.
	out := make([]gen.TaskRun, 0, len(runs))
	for _, run := range runs {
		if wire, ok := toWireTaskRun(run); ok {
			out = append(out, wire)
		}
	}
	jsonOK(w, out)
}

// handleTaskRunNow handles POST /api/v1/tasks/{id}/runs ("Run now", ADR-050
// RD7, task-run-history-spec.md §3.4). With occurrence_ms it runs that
// specific recurring occurrence (materialize-on-demand); without it, re-runs
// a normal/once task as a fresh run (prior runs preserved). Dispatch is
// async: StartOccurrenceRun (pkg/agent/task_executor.go) synchronously claims
// the task (SpawnReset, then the same ClaimForRun exactly-once dispatch guard
// scheduled fires use) and launches execution in a background goroutine —
// the actual run-open (task.Store.OpenRun, idempotent per (task,
// occurrence_ms) vs a concurrent scheduler fire landing on the same
// occurrence) happens inside that goroutine, NOT before this handler
// returns. So the run row may not exist yet at the moment this 202 is
// written; a client that immediately calls GET /tasks/{id}/runs can race the
// open and see nothing for this attempt yet. The client observes progress
// via the task_run_status WS frame, or by polling GET /tasks/{id}/runs.
// Returns 202.
func (a *restAPI) handleTaskRunNow(w http.ResponseWriter, r *http.Request, id string) {
	if err := validateEntityID(id); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid task ID")
		return
	}
	if _, err := a.taskStore.Get(id); err != nil {
		if errors.Is(err, task.ErrNotFound) {
			jsonErr(w, http.StatusNotFound, "task not found")
			return
		}
		slog.Error("rest: run now: get task failed", "task_id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not read task")
		return
	}

	// Body is optional — an empty body re-runs a normal/once task (occurrence_ms nil).
	var req gen.RunNowRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		jsonErr(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// D1 (operator decision 2026-07-20, TaskRunStatusField.tsx): Run-now is
	// NEVER allowed for a FUTURE occurrence — running it early would still
	// materialize a run now AND the scheduler would fire the same occurrence
	// again at its real instant (the scheduler is RRULE/Task.status-driven
	// and has no awareness of TaskRuns), double-executing it. The React
	// gate (`occurrence.ms > now`) only protects the calendar UI; a direct
	// POST must be rejected at the API boundary too. Task-level Run-now
	// (occurrence_ms omitted) and a past/current occurrence (<= now) are
	// unaffected — mirrors the client's own `occurrenceMs! > now` threshold
	// (strictly greater-than, so "now" itself is still allowed).
	if req.OccurrenceMs != nil && *req.OccurrenceMs > time.Now().UnixMilli() {
		jsonErr(w, http.StatusBadRequest, "cannot Run-now a future occurrence; it will run at its scheduled time")
		return
	}

	if a.taskExecutor == nil {
		slog.Warn("rest: run now: taskExecutor is nil (gateway degraded)", "task_id", id)
		jsonErr(w, http.StatusServiceUnavailable, "task executor unavailable")
		return
	}

	if err := a.taskExecutor.StartOccurrenceRun(r.Context(), id, req.OccurrenceMs); err != nil {
		slog.Error("rest: run now: start occurrence run failed", "task_id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not start run")
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// toWireTaskRun converts an internal task.TaskRun (pkg/task/run_store.go) to
// the generated wire type (contracts/components/schemas/TaskRun.yaml). ok is
// false when r.Status is not one of the three valid TaskRun statuses
// (in_progress/done/failed) — H4: validated via task.IsValidRunStatus rather
// than trusting the bare gen.TaskRunStatus(r.Status) conversion, so a
// corrupt/foreign stored status is dropped (logged at Warn, mirroring
// foldRunsLocked's own malformed-record skip, pkg/task/run_store.go) instead
// of being emitted as a schema-invalid value the SPA's zod edge would reject.
func toWireTaskRun(r task.TaskRun) (gen.TaskRun, bool) {
	if !task.IsValidRunStatus(r.Status) {
		slog.Warn("rest: task run has invalid status, dropping from run-history response",
			"task_id", r.TaskID, "run_id", r.RunID, "status", r.Status)
		return gen.TaskRun{}, false
	}
	out := gen.TaskRun{
		RunId:     r.RunID,
		TaskId:    r.TaskID,
		Status:    gen.TaskRunStatus(r.Status),
		SessionId: r.SessionID,
		Kind:      gen.TaskRunKind(r.Kind),
		// StartedAt is `required` (non-nullable) on the wire; parseTimeOrNow
		// (rest_tasks.go) is the codebase's existing tolerant-parse
		// convention for a stored RFC 3339 string that is normally always
		// well-formed (OpenRun always writes time.Now().UTC().Format(RFC3339)).
		StartedAt: parseTimeOrNow(r.StartedAt),
	}
	if r.OccurrenceMs != nil {
		out.OccurrenceMs = ptr(*r.OccurrenceMs)
	}
	if r.Result != "" {
		out.Result = ptr(r.Result)
	}
	if r.EndedAt != nil {
		if ts, err := time.Parse(time.RFC3339, *r.EndedAt); err == nil {
			out.EndedAt = &ts
		} else if out.Status == gen.TaskRunStatusDone || out.Status == gen.TaskRunStatusFailed {
			// Corrupt-ended_at honesty fix: a TERMINAL run (status already
			// validated above) with an unparseable ended_at must not render
			// as "done, but no finish time" — nil EndedAt paired with a
			// terminal Status is exactly that, and the client has no other
			// field to fall back to. Degrade honestly to the run's own
			// started_at (already parsed into out.StartedAt above) rather
			// than silently omitting EndedAt.
			slog.Warn("rest: task run has corrupt ended_at on a terminal run, falling back to started_at",
				"task_id", r.TaskID, "run_id", r.RunID, "value", *r.EndedAt)
			startedAt := out.StartedAt
			out.EndedAt = &startedAt
		} else {
			slog.Warn(
				"rest: task run has corrupt ended_at, omitting",
				"task_id",
				r.TaskID,
				"run_id",
				r.RunID,
				"value",
				*r.EndedAt,
			)
		}
	}
	return out, true
}

// --- moved from rest_tasks.go 2026-09-15 ---

// handleTaskStop handles POST /api/v1/tasks/{id}/stop (ADR-052 US-7/FR-025).
// Delegates to PlanEngine.StopTask, which cancels the task's own worker
// session AND its registered verifier session (if adjudication is in
// flight — via the same RequestCancelForSession primitive Tier A /cancel
// uses, a safe no-op when no turn is active) and marks the task
// failed+cancel_reason=stopped_by_user. Works identically for a standalone
// task or a SINGLE in-plan member (member-Stop, A5) — StopTask deliberately
// does not touch the task's plan; the plan's other independent members keep
// running (the engine's own reactive dispatch loop evaluates FR-041 "no
// further progress possible" on its next pass, triggered by the
// task_status_changed event StopTask's cancelMemberLocked already emits).
// Rejected 400 when the task is not currently in_progress (nothing running
// to stop — StopTask's own precondition; checked here first for a clearer
// error message and to avoid engaging the engine for an obviously-invalid
// call).
func (a *restAPI) handleTaskStop(w http.ResponseWriter, r *http.Request, id string) {
	if err := validateEntityID(id); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid task ID")
		return
	}
	t, err := a.taskStore.Get(id)
	if err != nil {
		if errors.Is(err, task.ErrNotFound) {
			jsonErr(w, http.StatusNotFound, "task not found")
			return
		}
		slog.Error("rest: task stop: get failed", "id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not read task")
		return
	}
	if t.Status != task.StatusInProgress {
		jsonErr(w, http.StatusBadRequest, fmt.Sprintf("task is %q; only an in-progress task can be stopped", t.Status))
		return
	}

	pe := agent.GetPlanEngine(a.agentLoop)
	if pe == nil {
		jsonErr(w, http.StatusServiceUnavailable, "plan engine is not available")
		return
	}
	c := a.callerIdentity(r)
	updated, serr := pe.StopTask(r.Context(), id, c.Username, "system")
	if serr != nil {
		if errors.Is(serr, task.ErrNotFound) {
			jsonErr(w, http.StatusNotFound, "task not found")
			return
		}
		// TOCTOU between the handler's own precheck (above) and StopTask's
		// own authoritative re-check under planDecisionMu: the task left
		// in_progress in that window. That has two, NOT equivalent, causes:
		//
		//  1. A concurrent Stop request for the SAME task won the race —
		//     the task is now failed+CancelReasonStoppedByUser. This
		//     caller's own request achieved nothing itself, but the outcome
		//     it asked for ("this task is stopped") is already true. A stop
		//     that achieved its purpose must not be reported as an error —
		//     idempotent 200, not 409 (this is the bug this branch used to
		//     have: it mapped ANY non-in_progress re-read to 409, including
		//     this one).
		//  2. The task reached some OTHER terminal state on its own
		//     (completed normally, or failed for an unrelated reason) —
		//     that is a genuine conflict: this Stop request cannot apply,
		//     and never did. 409, same as before.
		//
		// taskStopConflictOutcome makes that call from the re-read task's
		// own Status/CancelReason (see its doc for the full state table). A
		// re-read failure (rerr != nil — including the task having vanished
		// between the two reads) or a re-read that STILL shows in_progress
		// means StopTask's error had nothing to do with the task's status
		// (e.g. an I/O failure from inside the engine's own Get or its
		// cancelMemberLocked write) — falls through to the generic 500
		// below, same mapping handlePlanStop uses for its own non-status
		// engine failures.
		if reread, rerr := a.taskStore.Get(id); rerr == nil {
			if outcome, handled := taskStopConflictOutcome(reread); handled {
				if outcome.alreadyStopped {
					a.auditTask("task.stop", id)
					a.writeWireTask(w, http.StatusOK, *reread)
					return
				}
				jsonErr(w, http.StatusConflict, outcome.message)
				return
			}
		}
		slog.Error("rest: task stop: engine stop failed", "id", id, "error", serr)
		jsonErr(w, http.StatusInternalServerError, "could not stop task")
		return
	}
	// StopTask's own cancelMemberLocked already emits task_status_changed —
	// do not double-emit here. Audit logging remains a REST-layer concern
	// (the engine package writes no audit entries).
	a.auditTask("task.stop", id)
	a.writeWireTask(w, http.StatusOK, *updated)
}

// taskStopOutcome is what taskStopConflictOutcome decided for a task re-read
// after PlanEngine.StopTask has already reported an error for it.
type taskStopOutcome struct {
	// alreadyStopped is true when the task is now failed with
	// CancelReasonStoppedByUser — some request (not necessarily this one)
	// already stopped it. handleTaskStop maps this to 200: the caller's
	// stop achieved its purpose.
	alreadyStopped bool
	// message is the 409 body text; set only when alreadyStopped is false.
	message string
}

// taskStopConflictOutcome maps a task re-read taken AFTER PlanEngine.StopTask
// has already returned an error for taskID to the correct REST outcome. It
// distinguishes "this task is now stopped, just not by this exact request"
// (idempotent success) from a genuine conflict (409): only the task's own
// Status/CancelReason on disk can tell those apart, since StopTask's error
// text alone does not (both cases produce the identical "task %q is %s, not
// in_progress" wrapping, see plan_engine.go's StopTask).
//
// handled is false when t is still in_progress: StopTask's error then can't
// be explained by the task's own status having moved (an I/O failure
// surfaced from inside the engine), and the caller should fall back to its
// own generic 500 rather than treat this as a status-driven outcome.
func taskStopConflictOutcome(t *task.Task) (outcome taskStopOutcome, handled bool) {
	if t.Status == task.StatusInProgress {
		return taskStopOutcome{}, false
	}
	if t.Status == task.StatusFailed && t.CancelReason == task.CancelReasonStoppedByUser {
		return taskStopOutcome{alreadyStopped: true}, true
	}
	return taskStopOutcome{
		message: fmt.Sprintf("task is %q; only an in-progress task can be stopped", t.Status),
	}, true
}

// handleTaskRestart handles POST /api/v1/tasks/{id}/restart (ADR-052
// FR-026, the ▶ Play route for a standalone task previously stopped by the
// user). Per contracts/openapi.yaml's restartTask: rejected 409 when the
// task belongs to a plan (restart the plan instead, via
// POST /plans/{id}/restart, which re-runs its non-done members — G4) or is
// not in a restartable state — `failed` with cancel_reason
// stopped_by_user, specifically. Unlike a plan restart's member reset (which
// un-freezes failed->next for ANY reason, FR-016/DS-5), this standalone-task
// endpoint is reason-gated the same way the plan-level restart guard is: a
// genuinely-failed standalone task (attempts exhausted) is not restartable
// here — same "author fresh" posture as a genuinely-failed plan.
func (a *restAPI) handleTaskRestart(w http.ResponseWriter, id string) {
	if err := validateEntityID(id); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid task ID")
		return
	}
	t, err := a.taskStore.Get(id)
	if err != nil {
		if errors.Is(err, task.ErrNotFound) {
			jsonErr(w, http.StatusNotFound, "task not found")
			return
		}
		slog.Error("rest: task restart: get failed", "id", id, "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not read task")
		return
	}
	if t.PlanID != "" {
		jsonErr(w, http.StatusConflict,
			"task is a plan member; restart the plan instead via POST /plans/{id}/restart")
		return
	}
	// Gate delegated to task.ValidateStandaloneRestart (pkg/task/store.go) —
	// single source of truth mirroring plan.ValidateRestartTransition,
	// rather than an inline reason check hardcoded here. The handler keeps
	// its own specific, actionable 409 message; the helper's error is
	// wrapped rather than surfaced verbatim.
	if verr := task.ValidateStandaloneRestart(t.Status, t.CancelReason); verr != nil {
		jsonErr(w, http.StatusConflict, fmt.Sprintf(
			"task is %q (cancel_reason=%q); only a task stopped by the user "+
				"(status=failed, cancel_reason=stopped_by_user) can be restarted: %s",
			t.Status, t.CancelReason, verr))
		return
	}

	updated, rerr := a.taskStore.RestartReset(id)
	if rerr != nil {
		if errors.Is(rerr, task.ErrNotFound) {
			jsonErr(w, http.StatusNotFound, "task not found")
			return
		}
		if errors.Is(rerr, task.ErrNotRestartable) {
			jsonErr(w, http.StatusConflict, rerr.Error())
			return
		}
		slog.Error("rest: task restart: reset failed", "id", id, "error", rerr)
		jsonErr(w, http.StatusInternalServerError, "could not restart task")
		return
	}
	a.auditTask("task.restart", id)
	a.emitTaskStatus(updated)
	if a.agentLoop != nil {
		a.agentLoop.NotifyTaskUpserted(updated)
	}
	a.writeWireTask(w, http.StatusOK, *updated)
}

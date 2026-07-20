//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// rest_task_runs.go — GET /api/v1/tasks/{id}/runs (ADR-050
// docs/internal/architecture/ADR-050-task-run-history-model.md RD8,
// docs/internal/specs/task-run-history-spec.md §3.6): the authoritative,
// projection-independent run-history list for a task. Read-only: this file
// only translates the domain task.TaskRun (pkg/task/run_store.go) into the
// generated gen.TaskRun wire type and maps errors to HTTP status codes,
// mirroring HandleTaskOccurrences' split (rest_tasks.go) between REST
// plumbing and pure domain logic (task_occurrences.go handles the separate
// occurrence-run OVERLAY, not this file).
//
// Routed from HandleTasks' sub-resource dispatch (rest_tasks.go's "runs"
// case) rather than its own top-level registerAdditionalEndpoints entry —
// unlike the FIXED "/api/v1/tasks/occurrences" path, "/api/v1/tasks/{id}/runs"
// has a dynamic {id} segment between two fixed path parts, and
// dynamicServeMux (pkg/channels/dynamic_mux.go) only supports exact-path or
// trailing-slash PREFIX matches — it cannot route on a pattern with a
// variable middle segment. HandleTasks already owns the "/api/v1/tasks/"
// prefix and already parses {id}/{sub} for "subtasks"/"todos"/"dependencies",
// so a "runs" case there is the only integration point. The DEDICATED
// taskReadLimiter (240/min, rest_auth.go) — the SAME limiter
// /tasks/occurrences uses per spec §3.6 — is applied at that dispatch point
// since HandleTasks itself carries no rate limiter.

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

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
			slog.Warn("rest: task run has corrupt ended_at, omitting", "task_id", r.TaskID, "run_id", r.RunID, "value", *r.EndedAt)
		}
	}
	return out, true
}

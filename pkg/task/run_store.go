// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package task

// Per-task run history (ADR-050, docs/internal/specs/task-run-history-spec.md).
// Runs are a purely additive record layer — Task.status/result/session_id
// keep their exact current behavior and writers (RD2); this file adds the
// append-only, day-partitioned TaskRun store the calendar occurrence overlay
// and task-detail run-history list read from.

import (
	"bytes"
	crand "crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

// RunKind classifies how a TaskRun started (spec §2.1).
type RunKind string

const (
	// RunKindScheduled marks a run opened by an automatic trigger fire
	// (once/every/recurring dispatch).
	RunKindScheduled RunKind = "scheduled"
	// RunKindManual marks a run opened by a user-initiated Run-now — either a
	// specific recurring occurrence, or a fresh attempt on a normal task.
	RunKindManual RunKind = "manual"
)

// IsValidRunKind reports whether k is a known RunKind.
func IsValidRunKind(k RunKind) bool {
	return k == RunKindScheduled || k == RunKindManual
}

// IsValidRunStatus reports whether s is one of the three statuses a TaskRun
// may carry (in_progress, done, failed) — a narrower subset of the 7-state
// Task Status vocabulary. ADR-050 RD10 deliberately excludes `canceled`/
// `queued` from v1: no cancel producer exists anywhere in the codebase today,
// and a stuck-run reaper (PruneRuns) closes abandoned runs to `failed`
// instead of introducing a new terminal value.
func IsValidRunStatus(s Status) bool {
	return s == StatusInProgress || s == StatusDone || s == StatusFailed
}

// ErrRunNotFound is returned by CloseRun when runID does not match any
// currently-known run for the task (folded across its day files).
var ErrRunNotFound = errors.New("task: run not found")

// ErrRunAlreadyClosed is returned by CloseRun when the run's current folded
// state already carries a terminal status — a duplicate completion signal
// for the same run_id.
var ErrRunAlreadyClosed = errors.New("task: run already closed")

// TaskRun is one execution record for a task (ADR-050, additive run-history
// layer). Event-sourced: OpenRun appends an "open" record (Status:
// StatusInProgress, EndedAt: nil) to the task's runs/<day>.jsonl file;
// CloseRun appends a "close" record carrying the SAME RunID with a terminal
// Status + Result + EndedAt. Both records carry the FULL set of fields — the
// close record is a complete copy of the open record's identity fields
// (TaskID/OccurrenceMs/Kind/StartedAt/SessionID) plus the terminal outcome —
// so folding is a simple "last record for this RunID wins", no field-level
// merge logic, and a run's identity survives even if its original
// open-record day file is later pruned (RD9).
//
// Task.status/result/session_id keep their existing behavior completely
// unchanged (RD2) — TaskRun is purely additive, read by the calendar
// occurrence overlay and the task-detail run-history list.
//
// not-wire-format: internal disk/domain struct; mapped to gen.TaskRun at the
// REST layer.
type TaskRun struct { //nolint:revive // exported name matches package purpose
	RunID  string `json:"run_id"`
	TaskID string `json:"task_id"`
	// OccurrenceMs is the scheduled RRULE instant this run realizes — the
	// calendar join key. nil for an ad-hoc/once/manual run.
	OccurrenceMs *int64 `json:"occurrence_ms"`
	Status       Status `json:"status"`
	// Result is the terminal-run output; empty (and omitted on disk) until
	// the run closes — mirrors Task.Result's own "absent while running"
	// convention.
	Result    string  `json:"result,omitempty"`
	SessionID string  `json:"session_id"`
	Kind      RunKind `json:"kind"`
	// StartedAt is the open time, RFC 3339 UTC.
	StartedAt string `json:"started_at"`
	// EndedAt is the close time, RFC 3339 UTC; nil while in_progress.
	EndedAt *string `json:"ended_at"`
}

// IsOpen reports whether r has not yet been closed (EndedAt is nil).
func (r *TaskRun) IsOpen() bool { return r.EndedAt == nil }

// sameOccurrence reports whether a and b denote the same occurrence key: both
// nil (an ad-hoc/manual run's key), or both non-nil with equal values.
func sameOccurrence(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// runsDir returns the directory holding taskID's day-partitioned run files:
// <tasksDir>/<taskID>/runs/. This is distinct from the flat
// <tasksDir>/<taskID>.json task file — a directory and a same-stem file never
// collide on disk.
func (s *Store) runsDir(taskID string) string {
	return filepath.Join(s.dir, taskID, "runs")
}

// runsDayFilePath returns the day file path for taskID's runs directory on
// the UTC calendar day of at.
func (s *Store) runsDayFilePath(taskID string, at time.Time) string {
	return filepath.Join(s.runsDir(taskID), at.UTC().Format("2006-01-02")+".jsonl")
}

// newRunID mints a ULID-based run identifier (lexically sortable,
// time-ordered) using a fresh crypto-random entropy source per call —
// mirrors pkg/session.NewSessionID rather than the package-level monotonic
// ulid.Make() source used elsewhere in the codebase (e.g.
// pkg/sysagent/tools/task.go), so concurrent run creation across goroutines
// never contends on a shared monotonic reader.
func newRunID() (string, error) {
	id, err := ulid.New(ulid.Timestamp(time.Now()), crand.Reader)
	if err != nil {
		return "", fmt.Errorf("task: generate run id: %w", err)
	}
	return id.String(), nil
}

// foldRunsLocked reads every day file under taskID's runs directory and folds
// records by RunID, last record wins. os.ReadDir returns entries sorted by
// filename ascending — day files are named YYYY-MM-DD.jsonl, so this is also
// chronological order; within a file, lines are read top-to-bottom in append
// order. A single forward pass where each record overwrites any prior
// same-RunID entry therefore yields "last (most recent) record wins" with no
// timestamp comparison needed.
//
// A missing runs directory (a task with zero runs) is not an error — it
// folds to an empty map. Unreadable files and malformed lines are logged at
// Warn and skipped (mirrors Store.ListWithUnreadable / session.readPartition).
//
// Caller must hold the per-task lock (s.lock.Get(taskID)).
func (s *Store) foldRunsLocked(taskID string) (map[string]TaskRun, error) {
	dir := s.runsDir(taskID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]TaskRun{}, nil
		}
		return nil, fmt.Errorf("task: list runs dir %q: %w", dir, err)
	}

	folded := make(map[string]TaskRun)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			slog.Warn("task: skip unreadable run day file", "task_id", taskID, "file", path, "error", readErr)
			continue
		}
		for _, line := range bytes.Split(data, []byte{'\n'}) {
			line = bytes.TrimSpace(line)
			if len(line) == 0 {
				continue
			}
			var rec TaskRun
			if unmarshalErr := json.Unmarshal(line, &rec); unmarshalErr != nil {
				slog.Warn("task: skip malformed run record", "task_id", taskID, "file", path, "error", unmarshalErr)
				continue
			}
			folded[rec.RunID] = rec
		}
	}
	return folded, nil
}

// appendRunRecord appends rec as one JSONL line to taskID's run day file for
// the UTC calendar day of at, under an OS-level advisory flock guarding a
// read-modify-write cycle: read the existing day file (if any), append rec's
// line, and rewrite it atomically via fileutil.WriteFileAtomic. Day files are
// bounded in size — one calendar day's worth of runs for one task (spec
// §2.2) — so a whole-file read-modify-write per record is acceptable here,
// unlike an unbounded append-only log; this also gives the read-modify-write
// cycle the same crash-safety (temp file + rename) every other task-store
// write already has, plus cross-process mutual exclusion via WithFlock
// (defense-in-depth alongside the caller's in-process StripedLock).
func (s *Store) appendRunRecord(taskID string, rec TaskRun, at time.Time) error {
	path := s.runsDayFilePath(taskID, at)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("task: create runs dir: %w", err)
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("task: marshal run record %q: %w", rec.RunID, err)
	}
	return fileutil.WithFlock(path, func() error {
		existing, readErr := os.ReadFile(path)
		if readErr != nil && !os.IsNotExist(readErr) {
			return fmt.Errorf("task: read run day file %q: %w", path, readErr)
		}
		var buf bytes.Buffer
		if len(existing) > 0 {
			buf.Write(existing)
			if existing[len(existing)-1] != '\n' {
				buf.WriteByte('\n')
			}
		}
		buf.Write(line)
		buf.WriteByte('\n')
		return fileutil.WriteFileAtomic(path, buf.Bytes(), 0o600)
	})
}

// OpenRun atomically creates-or-returns the currently-open (EndedAt == nil)
// run for (taskID, occurrenceMs), under the same per-task StripedLock domain
// (s.lock, i.e. TaskFileLock) every other task read-modify-write uses — the
// same lock domain ClaimForRun/SpawnReset already gate exactly-once dispatch
// under. This is the missing per-occurrence idempotency guard (RD7): a
// second caller racing for the same (taskID, occurrenceMs) key — e.g. a
// scheduler fire and a concurrent Run-now for the same occurrence — finds
// the first caller's still-open run and returns it with created=false rather
// than opening a duplicate.
//
// occurrenceMs == nil keys an ad-hoc/once/manual run — idempotency there is
// "does any run with a nil occurrence_ms already have EndedAt == nil", which
// naturally excludes any PRIOR run that has already been closed: a failed
// once-task re-run via Run-now always opens a genuinely new run, and the
// prior failed run is preserved (RD7's "normal / once task re-run"
// behavior).
func (s *Store) OpenRun(taskID string, occurrenceMs *int64, kind RunKind, sessionID string) (*TaskRun, bool, error) {
	if err := validateID(taskID); err != nil {
		return nil, false, err
	}
	if !IsValidRunKind(kind) {
		return nil, false, fmt.Errorf("task: invalid run kind %q", kind)
	}

	mu := s.lock.Get(taskID)
	mu.Lock()
	defer mu.Unlock()

	folded, err := s.foldRunsLocked(taskID)
	if err != nil {
		return nil, false, err
	}
	for _, existing := range folded {
		if existing.IsOpen() && sameOccurrence(existing.OccurrenceMs, occurrenceMs) {
			existingCopy := existing
			return &existingCopy, false, nil
		}
	}

	runID, err := newRunID()
	if err != nil {
		return nil, false, err
	}
	var occCopy *int64
	if occurrenceMs != nil {
		v := *occurrenceMs
		occCopy = &v
	}
	now := time.Now().UTC()
	run := TaskRun{
		RunID:        runID,
		TaskID:       taskID,
		OccurrenceMs: occCopy,
		Status:       StatusInProgress,
		SessionID:    sessionID,
		Kind:         kind,
		StartedAt:    now.Format(time.RFC3339),
		EndedAt:      nil,
	}
	if err := s.appendRunRecord(taskID, run, now); err != nil {
		return nil, false, err
	}
	return &run, true, nil
}

// CloseRun appends the terminal record for runID: the SAME RunID with status
// (must be StatusDone or StatusFailed — IsTerminal), result, and EndedAt
// stamped to now. The close record is a full copy of the run's current
// folded state (TaskID/OccurrenceMs/Kind/StartedAt/SessionID all carried
// forward) so that later folding needs no field-level merge and the run's
// identity survives even if the original open record's day file is pruned
// later (RD9).
//
// Returns ErrRunNotFound when runID has no known record for taskID, and
// ErrRunAlreadyClosed when the run's current folded state is already
// terminal (a duplicate completion signal for the same run).
func (s *Store) CloseRun(taskID, runID string, status Status, result string) error {
	if err := validateID(taskID); err != nil {
		return err
	}
	if runID == "" {
		return fmt.Errorf("task: run id must not be empty")
	}
	if !IsTerminal(status) {
		return fmt.Errorf("task: CloseRun status %q must be a terminal status (done or failed)", status)
	}
	if len(result) > 50000 {
		return fmt.Errorf("task: run result must be 50000 characters or fewer")
	}

	mu := s.lock.Get(taskID)
	mu.Lock()
	defer mu.Unlock()

	folded, err := s.foldRunsLocked(taskID)
	if err != nil {
		return err
	}
	existing, ok := folded[runID]
	if !ok {
		return fmt.Errorf("task: close run %q: %w", runID, ErrRunNotFound)
	}
	if !existing.IsOpen() {
		return fmt.Errorf("task: close run %q: %w", runID, ErrRunAlreadyClosed)
	}

	now := time.Now().UTC()
	endedAt := now.Format(time.RFC3339)
	closeRec := existing
	closeRec.Status = status
	closeRec.Result = result
	closeRec.EndedAt = &endedAt
	return s.appendRunRecord(taskID, closeRec, now)
}

// closeStaleRunLocked is PruneRuns' reaper counterpart to CloseRun: it closes
// an already-known-open, already-known-stale run to StatusFailed with an
// explanatory result. Caller must hold the per-task lock and have already
// verified run.IsOpen().
func (s *Store) closeStaleRunLocked(taskID string, run TaskRun, staleAfter time.Duration) error {
	now := time.Now().UTC()
	endedAt := now.Format(time.RFC3339)
	run.Status = StatusFailed
	run.Result = fmt.Sprintf(
		"run abandoned: no completion recorded within %s of starting (closed by the stuck-run reaper)",
		staleAfter,
	)
	run.EndedAt = &endedAt
	return s.appendRunRecord(taskID, run, now)
}

// parseRunTime parses an RFC 3339 timestamp, falling back to the zero time on
// a parse error (mirrors Task.CreatedTime's tolerant-parse convention).
func parseRunTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// ListRuns folds every run recorded for taskID (across all its day files —
// PruneRuns, not ListRuns, is what enforces the retention window on disk)
// and returns them newest-first by StartedAt. Malformed/unparseable
// StartedAt values sort last (treated as the zero time).
func (s *Store) ListRuns(taskID string) ([]TaskRun, error) {
	if err := validateID(taskID); err != nil {
		return nil, err
	}
	mu := s.lock.Get(taskID)
	mu.Lock()
	defer mu.Unlock()

	folded, err := s.foldRunsLocked(taskID)
	if err != nil {
		return nil, err
	}
	runs := make([]TaskRun, 0, len(folded))
	for _, r := range folded {
		runs = append(runs, r)
	}
	sort.Slice(runs, func(i, j int) bool {
		ti, tj := parseRunTime(runs[i].StartedAt), parseRunTime(runs[j].StartedAt)
		if !ti.Equal(tj) {
			return ti.After(tj)
		}
		return runs[i].RunID > runs[j].RunID
	})
	return runs, nil
}

// RunsInRange folds every run recorded for taskID and returns those with a
// non-nil OccurrenceMs in the half-open range [fromMs, toMs) — matching the
// half-open convention GET /tasks/occurrences already uses — ascending by
// OccurrenceMs. This is the calendar occurrence-overlay join (RD6): the
// caller matches each returned run to the occurrence instant it realizes.
// Ad-hoc/manual runs (OccurrenceMs == nil) are never returned — they have no
// occurrence to join to.
func (s *Store) RunsInRange(taskID string, fromMs, toMs int64) ([]TaskRun, error) {
	if err := validateID(taskID); err != nil {
		return nil, err
	}
	mu := s.lock.Get(taskID)
	mu.Lock()
	defer mu.Unlock()

	folded, err := s.foldRunsLocked(taskID)
	if err != nil {
		return nil, err
	}
	runs := make([]TaskRun, 0)
	for _, r := range folded {
		if r.OccurrenceMs == nil {
			continue
		}
		if *r.OccurrenceMs < fromMs || *r.OccurrenceMs >= toMs {
			continue
		}
		runs = append(runs, r)
	}
	sort.Slice(runs, func(i, j int) bool {
		if *runs[i].OccurrenceMs != *runs[j].OccurrenceMs {
			return *runs[i].OccurrenceMs < *runs[j].OccurrenceMs
		}
		return runs[i].RunID < runs[j].RunID
	})
	return runs, nil
}

// PruneRuns enforces the run-history retention window for taskID (RD9) and
// closes long-abandoned open runs (RD10's stuck-run reaper):
//
//  1. Reaper: any currently-open (EndedAt == nil) run whose StartedAt is
//     older than staleAfter (relative to now) is closed to StatusFailed via
//     closeStaleRunLocked — writing its close record to TODAY's day file, so
//     the run's identity survives even if step 2 below deletes the day file
//     its open record originally lived in. The reaper runs BEFORE the
//     retention sweep so a stale run's terminal record always lands before
//     its origin file might be removed.
//  2. Retention sweep: every day file under taskID's runs directory whose
//     mtime is strictly before cutoff is deleted — EXCEPT the single
//     most-recently-named file, which is always retained (the floor-of-one:
//     a long-idle task never loses its last run). Because step 1 may have
//     just written to today's file, today's file is always the
//     most-recently-named file and is therefore always protected by the
//     floor, independent of cutoff.
//
// A missing runs directory (a task with zero runs) is a no-op success.
func (s *Store) PruneRuns(taskID string, cutoff time.Time, staleAfter time.Duration) error {
	if err := validateID(taskID); err != nil {
		return err
	}
	mu := s.lock.Get(taskID)
	mu.Lock()
	defer mu.Unlock()

	// Step 1: reaper — close stale in_progress runs before any file deletion.
	staleCutoff := time.Now().Add(-staleAfter)
	folded, err := s.foldRunsLocked(taskID)
	if err != nil {
		return err
	}
	for _, run := range folded {
		if !run.IsOpen() {
			continue
		}
		if parseRunTime(run.StartedAt).After(staleCutoff) {
			continue // still fresh
		}
		if closeErr := s.closeStaleRunLocked(taskID, run, staleAfter); closeErr != nil {
			return fmt.Errorf("task: reaper: close stale run %q: %w", run.RunID, closeErr)
		}
	}

	// Step 2: day-file retention sweep with a keep-newest-day floor.
	dir := s.runsDir(taskID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("task: list runs dir %q: %w", dir, err)
	}
	var dayFiles []os.DirEntry
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".jsonl") {
			dayFiles = append(dayFiles, e)
		}
	}
	if len(dayFiles) <= 1 {
		return nil // floor-of-one: nothing eligible for deletion.
	}
	// os.ReadDir sorts by filename ascending; day files are named
	// YYYY-MM-DD.jsonl, so the last entry is the newest day — always retained.
	newestIdx := len(dayFiles) - 1
	for i, e := range dayFiles {
		if i == newestIdx {
			continue
		}
		path := filepath.Join(dir, e.Name())
		info, statErr := e.Info()
		if statErr != nil {
			slog.Warn("task: prune runs: stat failed", "task_id", taskID, "file", path, "error", statErr)
			continue
		}
		if !info.ModTime().Before(cutoff) {
			continue // not old enough yet
		}
		if rmErr := os.Remove(path); rmErr != nil && !os.IsNotExist(rmErr) {
			slog.Warn("task: prune runs: delete failed", "task_id", taskID, "file", path, "error", rmErr)
		}
	}
	return nil
}

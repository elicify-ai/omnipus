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
	"unicode/utf8"

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
// `queued` from v1: no cancel producer exists anywhere in the codebase today.
// Consumed by CloseRun as a belt-and-suspenders guard alongside IsTerminal
// (H4): IsTerminal is the broader Task-status terminal set (done/failed
// today), while IsValidRunStatus is the TaskRun-specific vocabulary — should
// Task ever grow a new terminal status IsTerminal would accept but v1
// TaskRun does not (e.g. a future `canceled`), CloseRun must still reject it
// here rather than silently widening what a run can close to.
func IsValidRunStatus(s Status) bool {
	return s == StatusInProgress || s == StatusDone || s == StatusFailed
}

// maxRunResultChars is the TaskRun.result contract cap (TaskRun.yaml,
// task-run-history-spec.md §2.1). CloseRun truncates rather than rejects a
// result over this cap (H9) — with no stuck-run reaper, a rejected close
// would strand the run in_progress forever with no path to a terminal state.
const maxRunResultChars = 50000

// runResultTruncationMarker is appended to a truncated CloseRun result so
// readers (run-history list, calendar slide-over) can tell the stored result
// is not the full original output.
const runResultTruncationMarker = "\n\n[... result truncated: exceeded the 50000-character run-history cap ...]"

// truncateRunResult trims result to fit within maxRunResultChars (including
// runResultTruncationMarker), cutting on a valid UTF-8 boundary so a
// multi-byte rune straddling the cut point is dropped whole rather than
// corrupted into invalid UTF-8.
func truncateRunResult(result string) string {
	if len(result) <= maxRunResultChars {
		return result
	}
	limit := maxRunResultChars - len(runResultTruncationMarker)
	if limit < 0 {
		limit = 0
	}
	if limit > len(result) {
		limit = len(result)
	}
	truncated := result[:limit]
	for len(truncated) > 0 && !utf8.ValidString(truncated) {
		truncated = truncated[:len(truncated)-1]
	}
	return truncated + runResultTruncationMarker
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
// records by RunID, last record wins. Callers that need the task's FULL run
// history for correctness (ListRuns, CloseRun — a run's open record can live
// in an arbitrarily old day file, e.g. the one floor-of-one guarantees
// survival of) must use this, not foldRunsLockedFrom's filtered variant.
//
// Caller must hold the per-task lock (s.lock.Get(taskID)).
func (s *Store) foldRunsLocked(taskID string) (map[string]TaskRun, error) {
	return s.foldRunsLockedFrom(taskID, "")
}

// foldRunsLockedFrom reads day files under taskID's runs directory whose
// filename (YYYY-MM-DD.jsonl) is >= minDay (lexical string compare ==
// chronological compare for this fixed-width ISO date format), folding
// records by RunID, last record wins. minDay == "" means no lower bound —
// every day file is read (equivalent to foldRunsLocked).
//
// os.ReadDir returns entries sorted by filename ascending, so this is also
// chronological order; within a file, lines are read top-to-bottom in append
// order. A single forward pass where each record overwrites any prior
// same-RunID entry therefore yields "last (most recent) record wins" with no
// timestamp comparison needed.
//
// A missing runs directory (a task with zero runs) is not an error — it
// folds to an empty map. Unreadable files and malformed lines are logged at
// Warn and skipped (mirrors Store.ListWithUnreadable / session.readPartition).
//
// L1 perf fix (spec §3.5): RunsInRange uses a non-empty minDay to skip
// parsing day files that provably cannot contain a match — see RunsInRange's
// own doc comment for the "StartedAt's calendar day is never before the
// realized occurrence's calendar day" argument this depends on, and the
// accepted edge case it does not cover.
//
// Caller must hold the per-task lock (s.lock.Get(taskID)).
func (s *Store) foldRunsLockedFrom(taskID, minDay string) (map[string]TaskRun, error) {
	dir := s.runsDir(taskID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]TaskRun{}, nil
		}
		return nil, fmt.Errorf("task: list runs dir %q: %w", dir, err)
	}

	var minName string
	if minDay != "" {
		minName = minDay + ".jsonl"
	}

	folded := make(map[string]TaskRun)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		if minName != "" && e.Name() < minName {
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

// findOpenRunLocked scans taskID's day files NEWEST-FIRST looking for a
// currently-open (EndedAt == nil) run matching occurrenceMs, returning as
// soon as one is found (L1 perf fix, spec §3.5). OpenRun's idempotency guard
// (RD7) exists to catch a near-simultaneous DUPLICATE dispatch for a run
// that was just opened — exactly the case this finds without reading the
// task's entire history, since the match sits in the newest (typically
// today's) file.
//
// When no match is found, every day file must still be read to conclusively
// answer "no open run exists for this key" — a run may, per the operator's
// 2026-07-20 no-reaper decision, legitimately stay in_progress for days, so
// an older file can never be assumed irrelevant just because it is old. This
// path is therefore no worse than the previous full-fold-then-scan
// implementation; only the found/duplicate-dispatch path gets faster.
//
// Newest-first correctness argument: a CloseRun record's day file is never
// older than its OpenRun record's day file (a run cannot be closed before it
// is opened), so walking files newest-to-oldest and keeping only the FIRST
// (i.e. newest) record seen per run_id yields each run's true latest state —
// equivalent to foldRunsLocked's ascending "last record wins", walked in the
// opposite direction so a fresh match can be found without visiting any
// older file at all. Within a single file, lines are folded exactly like
// foldRunsLockedFrom (last line in the file wins) before being merged into
// the cross-file "first (newest) file wins" resolution.
//
// Caller must hold the per-task lock (s.lock.Get(taskID)).
func (s *Store) findOpenRunLocked(taskID string, occurrenceMs *int64) (*TaskRun, error) {
	dir := s.runsDir(taskID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("task: list runs dir %q: %w", dir, err)
	}
	var dayFiles []os.DirEntry
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".jsonl") {
			dayFiles = append(dayFiles, e)
		}
	}

	resolved := make(map[string]bool) // run_id -> final state already known from a newer file
	for i := len(dayFiles) - 1; i >= 0; i-- {
		name := dayFiles[i].Name()
		path := filepath.Join(dir, name)
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			slog.Warn("task: skip unreadable run day file", "task_id", taskID, "file", path, "error", readErr)
			continue
		}
		perFile := make(map[string]TaskRun)
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
			perFile[rec.RunID] = rec // last line in THIS file wins
		}
		for runID, rec := range perFile {
			if resolved[runID] {
				continue // a newer file already determined this run's final state
			}
			resolved[runID] = true
			if rec.IsOpen() && sameOccurrence(rec.OccurrenceMs, occurrenceMs) {
				recCopy := rec
				return &recCopy, nil
			}
		}
	}
	return nil, nil
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
//
// sessionID is expected to always be non-empty (TaskRun.yaml's contract
// doc), but is not rejected when empty — the executor's session-creation
// degrade path (pkg/agent/task_executor.go's openRun) intentionally still
// records a run with a best-effort blank session_id rather than losing run
// history entirely; a distinct Warn log makes that anomaly visible instead
// of silently persisting.
func (s *Store) OpenRun(taskID string, occurrenceMs *int64, kind RunKind, sessionID string) (*TaskRun, bool, error) {
	if err := validateID(taskID); err != nil {
		return nil, false, err
	}
	if !IsValidRunKind(kind) {
		return nil, false, fmt.Errorf("task: invalid run kind %q", kind)
	}
	if sessionID == "" {
		occLog := "nil"
		if occurrenceMs != nil {
			occLog = fmt.Sprintf("%d", *occurrenceMs)
		}
		slog.Warn("task: OpenRun called with empty session_id — TaskRun.session_id is documented as always non-empty; recording the run anyway rather than losing run history",
			"task_id", taskID, "occurrence_ms", occLog, "kind", string(kind))
	}

	mu := s.lock.Get(taskID)
	mu.Lock()
	defer mu.Unlock()

	existing, err := s.findOpenRunLocked(taskID, occurrenceMs)
	if err != nil {
		return nil, false, err
	}
	if existing != nil {
		return existing, false, nil
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
// (must be StatusDone or StatusFailed — IsTerminal and IsValidRunStatus,
// H4), result, and EndedAt stamped to now. The close record is a full copy
// of the run's current folded state (TaskID/OccurrenceMs/Kind/StartedAt/
// SessionID all carried forward) so that later folding needs no field-level
// merge and the run's identity survives even if the original open record's
// day file is pruned later (RD9).
//
// A result over maxRunResultChars is TRUNCATED, not rejected (H9): with no
// stuck-run reaper (operator decision 2026-07-20), a rejected close would
// strand the run in_progress forever — a terminal close must always
// succeed. The full untruncated length is logged at Warn so the anomaly is
// visible.
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
	if !IsValidRunStatus(status) {
		return fmt.Errorf("task: CloseRun status %q is not a valid run status", status)
	}
	if !IsTerminal(status) {
		return fmt.Errorf("task: CloseRun status %q must be a terminal status (done or failed)", status)
	}
	if len(result) > maxRunResultChars {
		originalLen := len(result)
		result = truncateRunResult(result)
		slog.Warn("task: run result exceeded the retention cap, truncated so the run still closes",
			"task_id", taskID, "run_id", runID, "original_len", originalLen, "cap", maxRunResultChars)
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

// RunsInRange folds runs recorded for taskID and returns those with a
// non-nil OccurrenceMs in the half-open range [fromMs, toMs) — matching the
// half-open convention GET /tasks/occurrences already uses — ascending by
// OccurrenceMs. This is the calendar occurrence-overlay join (RD6): the
// caller matches each returned run to the occurrence instant it realizes.
// Ad-hoc/manual runs (OccurrenceMs == nil) are never returned — they have no
// occurrence to join to.
//
// L1 perf fix (spec §3.5): day files named strictly before fromMs's UTC
// calendar day (minus one day of slack for boundary safety) are skipped
// without being parsed, via foldRunsLockedFrom. This is sound because a run
// is never dispatched (StartedAt) before the occurrence instant it realizes
// — the earliest a day file relevant to occurrence_ms >= fromMs can be named
// is fromMs's own calendar day — and a CloseRun record's day is never older
// than its OpenRun record's day, so no file that could hold either an open
// or a close record for an in-range occurrence is excluded. No upper-bound
// filter is applied: a past occurrence can legitimately be re-run (RD7) an
// arbitrary number of days after it fired, so today's file must always stay
// in scope regardless of toMs.
//
// Accepted edge case: RD8 documents that a FUTURE occurrence can be
// Run-now'd ahead of its schedule. If that manual dispatch starts more than
// one day before the occurrence's own calendar day, its record lands in a
// day file older than this filter's floor and would be missed by a query
// whose fromMs sits at or after that occurrence's instant — mirroring
// ADR-050's own accepted DST-join-precision limitation, this is an accepted,
// documented limitation rather than a full scan on every call.
func (s *Store) RunsInRange(taskID string, fromMs, toMs int64) ([]TaskRun, error) {
	if err := validateID(taskID); err != nil {
		return nil, err
	}
	mu := s.lock.Get(taskID)
	mu.Lock()
	defer mu.Unlock()

	var minDay string
	if fromMs > 0 {
		minDay = time.UnixMilli(fromMs).UTC().AddDate(0, 0, -1).Format("2006-01-02")
	}
	folded, err := s.foldRunsLockedFrom(taskID, minDay)
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

// openRunSourceFiles reads every day file under taskID's runs directory and
// folds records by RunID exactly like foldRunsLockedFrom (files processed in
// os.ReadDir's ascending-filename order, last record per RunID wins), but
// additionally remembers which file produced each run's WINNING (final,
// folded) record. It returns the subset of file names whose winning record
// is still open (TaskRun.IsOpen — EndedAt == nil).
//
// PruneRuns (delta-review Fix 1, 2026-07-20) uses this to decide whether a
// day file is safe to delete: fold-awareness must be GLOBAL, not per-file.
// CloseRun always appends the close record to the day file matching the
// CLOSE time (appendRunRecord's own doc comment), not the file holding the
// original open record — so a run that opened on one calendar day and closed
// on a later one has its open record and close record in two DIFFERENT
// files, and the older (open-record) file is safe to delete once the close
// record lands in a newer file: the run's identity survives via the close
// record's full field copy (RD9). Only when a run's true cross-file-folded
// state is STILL open — no close record exists anywhere, e.g. a
// crashed/orphaned execution with no reaper to close it (operator's
// 2026-07-20 no-reaper decision) — must its file be protected: deleting it
// would make the run vanish from history entirely, which is worse than the
// intended "stays in_progress forever".
//
// Caller must hold the per-task lock (s.lock.Get(taskID)).
func (s *Store) openRunSourceFiles(taskID string) (map[string]bool, error) {
	dir := s.runsDir(taskID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]bool{}, nil
		}
		return nil, fmt.Errorf("task: list runs dir %q: %w", dir, err)
	}

	type winningRecord struct {
		rec  TaskRun
		file string
	}
	winners := make(map[string]winningRecord)
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
			winners[rec.RunID] = winningRecord{rec: rec, file: e.Name()}
		}
	}

	openFiles := make(map[string]bool)
	for _, w := range winners {
		if w.rec.IsOpen() {
			openFiles[w.file] = true
		}
	}
	return openFiles, nil
}

// PruneRuns enforces the run-history retention window for taskID (RD9):
// every day file under taskID's runs directory whose mtime is strictly
// before cutoff is deleted — EXCEPT the single most-recently-named file,
// which is always retained (the floor-of-one: a long-idle task never loses
// its last run), AND except any file that is the sole surviving record of a
// still-open run (delta-review Fix 1, 2026-07-20, see openRunSourceFiles):
// with no stuck-run reaper, a run whose open record lives ONLY in an old day
// file must keep that file until its own execution eventually closes it —
// otherwise the run would vanish from history entirely rather than staying
// visible as in_progress.
//
// No stuck-run reaper: a run stays in_progress until its own execution
// closes it (operator decision 2026-07-20). A liveness-aware reaper is a
// planned follow-up requiring careful design (ADR-050 follow-up).
//
// A missing runs directory (a task with zero runs) is a no-op success.
func (s *Store) PruneRuns(taskID string, cutoff time.Time) error {
	if err := validateID(taskID); err != nil {
		return err
	}
	mu := s.lock.Get(taskID)
	mu.Lock()
	defer mu.Unlock()

	// Day-file retention sweep with a keep-newest-day floor.
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
	openFiles, err := s.openRunSourceFiles(taskID)
	if err != nil {
		return err
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
		if openFiles[e.Name()] {
			// This file holds the ONLY surviving record of a run whose
			// cross-file-folded state is still in_progress — deleting it
			// would make the run vanish from history entirely rather than
			// staying visible as in_progress until something closes it.
			slog.Info("task: prune runs: retaining stale day file holding an open run",
				"task_id", taskID, "file", path)
			continue
		}
		if rmErr := os.Remove(path); rmErr != nil && !os.IsNotExist(rmErr) {
			slog.Warn("task: prune runs: delete failed", "task_id", taskID, "file", path, "error", rmErr)
		}
	}
	return nil
}

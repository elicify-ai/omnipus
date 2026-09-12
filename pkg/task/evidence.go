// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// evidence.go persists EvidenceRecords — the redacted, size-capped proof of a
// single machine-check attempt (ADR-049 D2, spec Part A §C) — under
// $OMNIPUS_HOME/tasks_evidence/<task_id>/<criterion_id>-<attempt>.json
// (SD-A10). Storage posture mirrors sessions: file 0600, dir 0700.
package task

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

// evidenceDirName is the sibling directory of the task store's "tasks"
// directory that holds per-task evidence subdirectories.
const evidenceDirName = "tasks_evidence"

// DefaultEvidenceOutputCap is the default per-attempt Output size cap in bytes
// (spec Part A §C, "e.g. 64 KiB").
const DefaultEvidenceOutputCap = 64 * 1024

// EvidenceRecord is the persisted proof of one check-criterion attempt
// (ADR-049 D2). ExitCode carries the sentinel -1 when TimedOut or
// PolicyDenied is true — consumers must check those booleans first.
type EvidenceRecord struct {
	ID           string `json:"id"`
	TaskID       string `json:"task_id"`
	CriterionID  string `json:"criterion_id"`
	Attempt      int    `json:"attempt"`
	Command      string `json:"command"`
	ExitCode     int    `json:"exit_code"`
	Output       string `json:"output"`
	Truncated    bool   `json:"truncated"`
	TimedOut     bool   `json:"timed_out"`
	PolicyDenied bool   `json:"policy_denied"`
	RecordedAt   string `json:"recorded_at"`
}

// EvidenceStore persists EvidenceRecords under
// <home>/tasks_evidence/<task_id>/<criterion_id>-<attempt>.json.
type EvidenceStore struct {
	dir string
	// Redact is applied to Command and Output BEFORE truncation and BEFORE
	// the record is marshalled/written (SD-A13/FR-020) — a secret straddling
	// the size-cap boundary must still be fully scrubbed. A nil Redact is a
	// no-op passthrough (e.g. test harnesses with no registered secrets).
	Redact func(string) string
	// OutputCap bounds the per-attempt (post-redaction) Output size in bytes.
	// <= 0 uses DefaultEvidenceOutputCap.
	OutputCap int
}

// NewEvidenceStore returns an EvidenceStore rooted at
// <homeDir>/tasks_evidence. redact is typically wired from
// config.Config.SensitiveDataReplacer() (RegisterSensitiveValues) at the
// gateway boot seam; nil is a valid no-op passthrough.
func NewEvidenceStore(homeDir string, redact func(string) string) *EvidenceStore {
	return &EvidenceStore{
		dir:       filepath.Join(homeDir, evidenceDirName),
		Redact:    redact,
		OutputCap: DefaultEvidenceOutputCap,
	}
}

// taskEvidenceDir returns the per-task evidence directory.
func (es *EvidenceStore) taskEvidenceDir(taskID string) string {
	return filepath.Join(es.dir, taskID)
}

// Record redacts, truncates, and persists a new EvidenceRecord for one
// check-criterion attempt (FR-019..022). The sentinel ExitCode -1 is applied
// automatically when timedOut or policyDenied is true, matching the
// EvidenceRecord.ExitCode contract.
//
// Record is exactly Build + write: every redaction/cap rule lives in Build,
// so a caller with nothing to persist against still gets an identically
// scrubbed record (see Build's own doc comment).
//
// On a WRITE failure Record returns the built record AND the error (not a nil
// record). The record is already fully redacted and capped, and the write is
// the only thing that failed — a caller that needs it for something other
// than the audit trail (the Judge's evidence bundle) can use it rather than
// rebuilding a second, differently-identified copy. A caller that only cares
// about persistence still checks err first, exactly as before. VALIDATION
// failures still return (nil, err): there is no valid record in that case.
func (es *EvidenceStore) Record(
	taskID, criterionID string,
	attempt int,
	command, output string,
	exitCode int,
	timedOut, policyDenied bool,
) (*EvidenceRecord, error) {
	if err := validateID(taskID); err != nil {
		return nil, fmt.Errorf("task: evidence: invalid task_id: %w", err)
	}
	rec, err := es.Build(taskID, criterionID, attempt, command, output, exitCode, timedOut, policyDenied)
	if err != nil {
		return nil, err
	}
	if err := es.write(rec); err != nil {
		return rec, err // built-but-unpersisted; see the doc comment
	}
	return rec, nil
}

// Build assembles one check-criterion attempt's EvidenceRecord — redacted and
// size-capped by the SAME rules Record persists under — WITHOUT writing
// anything to disk.
//
// It exists because the on-disk evidence layout is partitioned by task id
// (<home>/tasks_evidence/<task_id>/…), while the prose Judge's evidence
// bundle is NOT a task-scoped concern: a chat `/goal` adjudication carries no
// TaskID at all (JudgeCriteriaInput.TaskID is empty for
// task.VerdictScopeGoal by construction). Before this split, the agent-side
// wrapper answered "no task id" with a nil record, so a goal whose machine
// check genuinely PASSED reached the Judge with an empty machine-check
// section and its floor DoD prose criteria ("No secrets or credentials appear
// in the output", "Every factual claim is grounded, not assumed") were
// structurally unprovable — the Judge fail-closed them forever and the goal
// could never be met. Building the record without persisting it keeps the
// Judge's evidence honest without inventing a second, goal-shaped evidence
// store (DoD-11 anti-drift).
//
// taskID may be EMPTY here — it is only the on-disk partition key, which an
// unpersisted record does not need. Record validates it before calling in, so
// no unvalidated id can ever reach write() through this seam.
func (es *EvidenceStore) Build(
	taskID, criterionID string,
	attempt int,
	command, output string,
	exitCode int,
	timedOut, policyDenied bool,
) (*EvidenceRecord, error) {
	if err := validateID(criterionID); err != nil {
		return nil, fmt.Errorf("task: evidence: invalid criterion_id: %w", err)
	}

	redact := es.Redact
	if redact == nil {
		redact = func(s string) string { return s }
	}
	// Redaction MUST precede truncation (SD-A13) — otherwise a secret
	// straddling the cap boundary could survive partially un-scrubbed.
	redactedCmd := redact(command)
	redactedOut := redact(output)

	outputCap := es.OutputCap
	if outputCap <= 0 {
		outputCap = DefaultEvidenceOutputCap
	}
	truncated := false
	out := redactedOut
	if len(out) > outputCap {
		cut := len(out) - outputCap
		out = out[:outputCap] + fmt.Sprintf("...[truncated %d bytes]", cut)
		truncated = true
	}

	ec := exitCode
	if timedOut || policyDenied {
		ec = -1
	}

	rec := &EvidenceRecord{
		ID:           uuid.New().String(),
		TaskID:       taskID,
		CriterionID:  criterionID,
		Attempt:      attempt,
		Command:      redactedCmd,
		ExitCode:     ec,
		Output:       out,
		Truncated:    truncated,
		TimedOut:     timedOut,
		PolicyDenied: policyDenied,
		RecordedAt:   time.Now().UTC().Format(time.RFC3339),
	}
	return rec, nil
}

// write atomically persists rec to
// <home>/tasks_evidence/<task_id>/<criterion_id>-<attempt>.json (0600, dir 0700).
func (es *EvidenceStore) write(rec *EvidenceRecord) error {
	dir := es.taskEvidenceDir(rec.TaskID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("task: evidence: create dir: %w", err)
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("task: evidence: marshal: %w", err)
	}
	path := filepath.Join(dir, fmt.Sprintf("%s-%d.json", rec.CriterionID, rec.Attempt))
	return fileutil.WithFlock(path, func() error {
		return fileutil.WriteFileAtomic(path, data, 0o600)
	})
}

// List returns every EvidenceRecord persisted for taskID. Unreadable/corrupt
// files are skipped (never fatal to the caller — mirrors Store.List).
func (es *EvidenceStore) List(taskID string) ([]EvidenceRecord, error) {
	if err := validateID(taskID); err != nil {
		return nil, fmt.Errorf("task: evidence: invalid task_id: %w", err)
	}
	dir := es.taskEvidenceDir(taskID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("task: evidence: list dir: %w", err)
	}
	out := make([]EvidenceRecord, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, rerr := os.ReadFile(filepath.Join(dir, e.Name()))
		if rerr != nil {
			continue
		}
		var rec EvidenceRecord
		if jerr := json.Unmarshal(data, &rec); jerr != nil {
			continue
		}
		out = append(out, rec)
	}
	return out, nil
}

// DeleteTaskEvidence removes taskID's entire evidence directory (FR-023,
// SD-A10) — called by Store.Delete's cascade. A missing directory is a
// success no-op.
func (es *EvidenceStore) DeleteTaskEvidence(taskID string) error {
	if err := validateID(taskID); err != nil {
		return fmt.Errorf("task: evidence: invalid task_id: %w", err)
	}
	if err := os.RemoveAll(es.taskEvidenceDir(taskID)); err != nil {
		return fmt.Errorf("task: evidence: remove dir for %q: %w", taskID, err)
	}
	return nil
}

// evidenceDirForTaskStoreDir derives the tasks_evidence root from a
// task.Store's directory (which is, by universal convention across every call
// site in this codebase, always "<home>/tasks"). Used by Store.Delete's
// evidence cascade so it does not require a Store API/signature change (which
// would ripple across ~30 call sites in other packages).
func evidenceDirForTaskStoreDir(storeDir string) string {
	return filepath.Join(filepath.Dir(storeDir), evidenceDirName)
}
